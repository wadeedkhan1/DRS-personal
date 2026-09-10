package com.drs.agent

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.BatteryManager
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import androidx.core.app.NotificationCompat
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.modules.core.DeviceEventManagerModule

/**
 * DRSScreenCapture native module.
 *
 * Screen capture itself is handled entirely by react-native-webrtc's getDisplayMedia
 * (which owns the MediaProjection consent dialog and its own foreground service). This
 * module provides what JS cannot do on its own:
 *   - device telemetry for the heartbeat (battery, model, OS),
 *   - the keep-alive foreground service that holds the control socket up,
 *   - a NATIVE heartbeat tick. React Native's setInterval is driven by the display
 *     frame callback, which Android pauses when the app is backgrounded or the screen
 *     is off — so the JS heartbeat stops and the server drops the agent after ~30s. A
 *     native Handler on its own thread keeps firing regardless (the foreground service
 *     holds a wake lock so the CPU stays up), and we emit a tick to JS to send a beat.
 *   - the connected/disconnected flag, which has to outlive the JS context,
 *   - the capture-approval notification, the only way to get the MediaProjection consent
 *     dialog on screen when an operator starts a session on a backgrounded phone.
 */
class ScreenCaptureModule(private val reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

    companion object {
        private const val APPROVAL_CHANNEL_ID = "drs_capture_approval_channel"
        private const val APPROVAL_NOTIFICATION_ID = 4003
    }

    private var tickThread: HandlerThread? = null
    private var tickHandler: Handler? = null
    private var tickIntervalMs: Long = 5000
    private val tickRunnable = object : Runnable {
        override fun run() {
            emitTick()
            tickHandler?.postDelayed(this, tickIntervalMs)
        }
    }

    override fun getName(): String = "DRSScreenCapture"

    @ReactMethod
    fun getDeviceTelemetry(promise: Promise) {
        val map = Arguments.createMap()

        val ifilter = IntentFilter(Intent.ACTION_BATTERY_CHANGED)
        val batteryStatus: Intent? = reactContext.registerReceiver(null, ifilter)
        val level: Int = batteryStatus?.getIntExtra(BatteryManager.EXTRA_LEVEL, -1) ?: -1
        val scale: Int = batteryStatus?.getIntExtra(BatteryManager.EXTRA_SCALE, -1) ?: -1
        val batteryPct = if (level != -1 && scale != -1) (level * 100 / scale.toFloat()) else 100f

        map.putDouble("batteryLevel", batteryPct.toDouble())
        map.putString("osVersion", "Android " + Build.VERSION.RELEASE)
        map.putString("deviceModel", Build.MANUFACTURER + " " + Build.MODEL)
        promise.resolve(map)
    }

    /** Start the keep-alive foreground service so the socket survives backgrounding. */
    @ReactMethod
    fun startConnectionService(promise: Promise) {
        val intent = Intent(reactContext, ConnectionService::class.java).apply {
            action = ConnectionService.ACTION_START
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            reactContext.startForegroundService(intent)
        } else {
            reactContext.startService(intent)
        }
        promise.resolve(true)
    }

    /** Stop the keep-alive foreground service. */
    @ReactMethod
    fun stopConnectionService(promise: Promise) {
        stopHeartbeatInternal()
        cancelApprovalNotification()
        val intent = Intent(reactContext, ConnectionService::class.java).apply {
            action = ConnectionService.ACTION_STOP
        }
        reactContext.startService(intent)
        promise.resolve(true)
    }

    /**
     * Update the ongoing notification's text so it reads like a VPN's: what the agent is
     * doing right now, not a fixed string. [detail] carries the operator's name during a
     * session, so the phone's owner can see who is watching without opening the app.
     */
    @ReactMethod
    fun setAgentState(status: String, detail: String?, promise: Promise) {
        val intent = Intent(reactContext, ConnectionService::class.java).apply {
            action = ConnectionService.ACTION_UPDATE
            putExtra(ConnectionService.EXTRA_STATUS, status)
            putExtra(ConnectionService.EXTRA_DETAIL, detail)
        }
        // Plain startService: the service is already foregrounded by the time any state
        // update happens, and startForegroundService here would demand another
        // startForeground call it does not need.
        try {
            reactContext.startService(intent)
        } catch (e: IllegalStateException) {
            // Backgrounded with no running service; the text is cosmetic, so drop it.
        }
        promise.resolve(true)
    }

    /** Whether the agent should be connected. Survives the JS context; see AgentPrefs. */
    @ReactMethod
    fun getAgentEnabled(promise: Promise) {
        promise.resolve(AgentPrefs.isEnabled(reactContext))
    }

    @ReactMethod
    fun setAgentEnabled(enabled: Boolean, promise: Promise) {
        AgentPrefs.setEnabled(reactContext, enabled)
        promise.resolve(true)
    }

    /**
     * Whether an activity of ours is currently on screen.
     *
     * This is what decides whether `getDisplayMedia` can be called directly: Android
     * refuses to start the MediaProjection consent activity from the background, and the
     * failure is silent enough to look like a broken stream.
     */
    @ReactMethod
    fun isAppForeground(promise: Promise) {
        promise.resolve(reactContext.currentActivity != null)
    }

    /**
     * Ask the phone's owner to bring the app up so a capture requested while it was
     * backgrounded can go ahead.
     *
     * The notification does exactly one thing: **open the app**. It deliberately does not
     * carry an "approve" action of its own, and deliberately does not use a full-screen
     * intent. Both were tried and both are wrong here:
     *
     *  - An action that grants consent directly would make the notification the consent
     *    gate, which it must not be. On API 24-28 the system may *launch* a full-screen
     *    intent with no user interaction at all when the device is locked — so pointing
     *    one at a "grant" action would let an operator start capture on a locked phone
     *    without the owner ever touching it. The real gate is, and stays, Android's own
     *    MediaProjection dialog, which only appears once the app is genuinely in front.
     *  - A full-screen intent also needs `USE_FULL_SCREEN_INTENT`, which since API 34 is
     *    granted only to calling and alarm apps. A heads-up notification is what this
     *    would degrade to anyway, so it is what it asks for.
     *
     * Heads-up (IMPORTANCE_HIGH) so it appears over whatever is on screen, and visible on
     * the lock screen, because the request expires after a minute.
     */
    @ReactMethod
    fun postCaptureApprovalNotification(operator: String?, promise: Promise) {
        createApprovalChannel()

        val launch = reactContext.packageManager
            .getLaunchIntentForPackage(reactContext.packageName)
            ?.apply { addFlags(Intent.FLAG_ACTIVITY_NEW_TASK) }
        val open = PendingIntent.getActivity(
            reactContext, 2, launch,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val who = if (operator.isNullOrBlank()) "An operator" else operator
        val notification = NotificationCompat.Builder(reactContext, APPROVAL_CHANNEL_ID)
            .setContentTitle("Screen sharing requested")
            .setContentText("$who wants to view this device. Tap to open DRS and allow it.")
            .setSmallIcon(R.drawable.ic_notification)
            .setContentIntent(open)
            .setAutoCancel(true)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
            .addAction(0, "Open DRS", open)
            .build()

        notificationManager()?.notify(APPROVAL_NOTIFICATION_ID, notification)
        promise.resolve(true)
    }

    @ReactMethod
    fun cancelCaptureApprovalNotification(promise: Promise) {
        cancelApprovalNotification()
        promise.resolve(true)
    }

    /**
     * Start emitting a "DRSHeartbeatTick" event every [intervalMs] ms from a native
     * thread that keeps running in the background / with the screen off.
     */
    @ReactMethod
    fun startHeartbeat(intervalMs: Double, promise: Promise) {
        tickIntervalMs = intervalMs.toLong().coerceIn(1000, 60000)
        stopHeartbeatInternal()
        val thread = HandlerThread("drs-heartbeat").apply { start() }
        val handler = Handler(thread.looper)
        tickThread = thread
        tickHandler = handler
        handler.postDelayed(tickRunnable, tickIntervalMs)
        promise.resolve(true)
    }

    @ReactMethod
    fun stopHeartbeat(promise: Promise) {
        stopHeartbeatInternal()
        promise.resolve(true)
    }

    // Required so NativeEventEmitter does not warn on Android.
    @ReactMethod
    fun addListener(eventName: String) {}

    @ReactMethod
    fun removeListeners(count: Int) {}

    private fun notificationManager(): NotificationManager? =
        reactContext.getSystemService(NotificationManager::class.java)

    private fun createApprovalChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                APPROVAL_CHANNEL_ID,
                "Screen sharing requests",
                NotificationManager.IMPORTANCE_HIGH
            ).apply {
                description = "Asks you to approve a remote request to view this screen"
            }
            notificationManager()?.createNotificationChannel(channel)
        }
    }

    private fun cancelApprovalNotification() {
        notificationManager()?.cancel(APPROVAL_NOTIFICATION_ID)
    }

    private fun stopHeartbeatInternal() {
        tickHandler?.removeCallbacks(tickRunnable)
        tickThread?.quitSafely()
        tickHandler = null
        tickThread = null
    }

    private fun emitTick() {
        if (reactContext.hasActiveReactInstance()) {
            reactContext
                .getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
                .emit("DRSHeartbeatTick", null)
        }
    }

    override fun onCatalystInstanceDestroy() {
        stopHeartbeatInternal()
    }
}

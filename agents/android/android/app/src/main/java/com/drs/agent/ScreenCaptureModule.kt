package com.drs.agent

import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.BatteryManager
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
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
 */
class ScreenCaptureModule(private val reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

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
        val intent = Intent(reactContext, ConnectionService::class.java).apply {
            action = ConnectionService.ACTION_STOP
        }
        reactContext.startService(intent)
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

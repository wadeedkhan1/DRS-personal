package com.drs.agent

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import androidx.core.app.NotificationCompat
import com.facebook.react.ReactApplication

/**
 * ConnectionService keeps the app process alive while the agent is backgrounded, the
 * screen is off, or the task has been swiped out of recents, so the outbound WebSocket
 * control channel (and its heartbeat) survive.
 *
 * It is deliberately NOT the screen-capture service. MediaProjection capture is owned by
 * react-native-webrtc, which runs its own `mediaProjection` foreground service on demand.
 * This one is a lightweight `dataSync` foreground service that only holds the process up.
 *
 * The notification is the disconnect control, the way a VPN's is: it carries a Disconnect
 * action and shows the live connection state, so the agent can be stopped without opening
 * the app — and, just as importantly, cannot be stopped by accident merely by closing it.
 */
class ConnectionService : Service() {

    companion object {
        const val CHANNEL_ID = "drs_connection_channel"
        const val NOTIFICATION_ID = 4002
        const val ACTION_START = "com.drs.agent.START_CONNECTION"
        const val ACTION_STOP = "com.drs.agent.STOP_CONNECTION"
        /** Refresh the notification text from JS as the connection state changes. */
        const val ACTION_UPDATE = "com.drs.agent.UPDATE_CONNECTION"
        const val EXTRA_STATUS = "status"
        const val EXTRA_DETAIL = "detail"
    }

    private var wakeLock: PowerManager.WakeLock? = null
    private var foregrounded = false
    private var statusText = "Connecting…"
    private var detailText: String? = null

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                releaseWakeLock()
                foregrounded = false
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf()
                return START_NOT_STICKY
            }
            ACTION_UPDATE -> {
                statusText = intent.getStringExtra(EXTRA_STATUS) ?: statusText
                detailText = intent.getStringExtra(EXTRA_DETAIL)
                // Only repaint if we are already showing; an update that arrives before
                // the service was started should not promote it to the foreground.
                if (foregrounded) {
                    notificationManager()?.notify(NOTIFICATION_ID, buildNotification())
                }
            }
            else -> {
                ensureForeground()
                acquireWakeLock()
            }
        }
        // START_STICKY so a process killed for memory comes back and reconnects. The stop
        // path returns START_NOT_STICKY above, so a deliberate disconnect stays stopped.
        return START_STICKY
    }

    /**
     * The task was swiped out of recents.
     *
     * Deliberately does not stop: dismissing the app is not disconnecting it, exactly as
     * swiping away a VPN client's task leaves the tunnel up. The only ways out are the
     * notification's Disconnect action and the in-app button.
     *
     * React Native tears the root component down when the activity dies, which is why the
     * connection lives in a module-level runtime rather than in a component — but the JS
     * context itself can also be lost, so it is recreated here if it has gone.
     */
    override fun onTaskRemoved(rootIntent: Intent?) {
        ensureForeground()
        ensureReactContext()
        super.onTaskRemoved(rootIntent)
    }

    /** Bring the JS runtime back up headlessly if the activity took it down with it. */
    private fun ensureReactContext() {
        try {
            val host = (application as? ReactApplication)?.reactNativeHost ?: return
            if (!host.hasInstance()) {
                host.reactInstanceManager.createReactContextInBackground()
            }
        } catch (e: Exception) {
            // A failure here costs a reconnect after the process is next started, not a
            // crash of the service that is holding everything else up.
        }
    }

    private fun ensureForeground() {
        val notification = buildNotification()
        if (!foregrounded) {
            startForeground(NOTIFICATION_ID, notification)
            foregrounded = true
        } else {
            notificationManager()?.notify(NOTIFICATION_ID, notification)
        }
    }

    private fun acquireWakeLock() {
        if (wakeLock?.isHeld == true) {
            return
        }
        val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
        // Keeps the CPU running with the screen off, so the native heartbeat tick keeps
        // firing and the control socket stays alive while the phone is monitored.
        wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "drs:connection").apply {
            setReferenceCounted(false)
            acquire()
        }
    }

    private fun releaseWakeLock() {
        if (wakeLock?.isHeld == true) {
            wakeLock?.release()
        }
        wakeLock = null
    }

    private fun notificationManager(): NotificationManager? =
        getSystemService(NotificationManager::class.java)

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "DRS Connection",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Keeps the DRS agent connected to the server"
                setShowBadge(false)
            }
            notificationManager()?.createNotificationChannel(channel)
        }
    }

    private fun buildNotification(): Notification {
        val launch = packageManager.getLaunchIntentForPackage(packageName)
        val content = PendingIntent.getActivity(
            this, 0, launch,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        // The disconnect control. A broadcast rather than a service intent so the receiver
        // can tell JS to close the socket cleanly before the service goes away.
        val disconnect = PendingIntent.getBroadcast(
            this,
            1,
            Intent(this, AgentCommandReceiver::class.java).apply {
                action = AgentCommandReceiver.ACTION_DISCONNECT
                setPackage(packageName)
            },
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("DRS Agent")
            .setContentText(statusText)
            .apply { detailText?.let { setSubText(it) } }
            .setSmallIcon(R.drawable.ic_notification)
            .setContentIntent(content)
            .addAction(0, "Disconnect", disconnect)
            .setOngoing(true)
            .setShowWhen(false)
            .setSilent(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .build()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        releaseWakeLock()
        foregrounded = false
        super.onDestroy()
    }
}

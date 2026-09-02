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

/**
 * ConnectionService keeps the app process alive while the agent is backgrounded or the
 * screen is off, so the outbound WebSocket control channel (and its heartbeat) survive.
 *
 * It is deliberately NOT the screen-capture service. MediaProjection capture is owned by
 * react-native-webrtc, which runs its own `mediaProjection` foreground service on demand.
 * This one is a lightweight `dataSync` foreground service that only holds the process up.
 */
class ConnectionService : Service() {

    companion object {
        const val CHANNEL_ID = "drs_connection_channel"
        const val NOTIFICATION_ID = 4002
        const val ACTION_START = "com.drs.agent.START_CONNECTION"
        const val ACTION_STOP = "com.drs.agent.STOP_CONNECTION"
    }

    private var wakeLock: PowerManager.WakeLock? = null

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                releaseWakeLock()
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf()
            }
            else -> {
                startForeground(NOTIFICATION_ID, buildNotification())
                acquireWakeLock()
            }
        }
        return START_STICKY
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

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "DRS Connection",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Keeps the DRS agent connected to the server"
            }
            getSystemService(NotificationManager::class.java)?.createNotificationChannel(channel)
        }
    }

    private fun buildNotification(): Notification {
        val launch = packageManager.getLaunchIntentForPackage(packageName)
        val pending = PendingIntent.getActivity(
            this, 0, launch,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("DRS Agent")
            .setContentText("Connected and available for monitoring")
            .setSmallIcon(android.R.drawable.stat_sys_data_bluetooth)
            .setContentIntent(pending)
            .setOngoing(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        releaseWakeLock()
        super.onDestroy()
    }
}

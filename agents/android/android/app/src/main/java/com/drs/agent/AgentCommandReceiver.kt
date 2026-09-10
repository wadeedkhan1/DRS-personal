package com.drs.agent

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build
import com.facebook.react.ReactApplication
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.ReactContext
import com.facebook.react.modules.core.DeviceEventManagerModule

/**
 * Turns a tap on the agent's Disconnect notification action into a command for the JS
 * runtime.
 *
 * Notification actions land in the app process but outside React, so they cannot call the
 * runtime directly. This bridges the gap: it emits a `DRSAgentCommand` event that
 * `src/runtime.ts` listens for, and only then lets the side effect happen.
 *
 * Disconnect deliberately goes through JS rather than just killing the service. Closing
 * the WebSocket properly is what makes the portal show the device offline immediately;
 * dropping the process instead would leave the server waiting three missed heartbeats.
 *
 * Note what is *not* here: there is no "approve capture" action. Granting screen capture
 * from a notification would make the notification the consent gate, and on API 24-28 the
 * system can fire a full-screen intent with no user interaction on a locked device. The
 * capture-request notification therefore only opens the app, and consent stays where it
 * belongs — Android's own MediaProjection dialog, which needs the app in the foreground.
 */
class AgentCommandReceiver : BroadcastReceiver() {

    companion object {
        const val ACTION_DISCONNECT = "com.drs.agent.action.DISCONNECT"

        const val EVENT = "DRSAgentCommand"
    }

    override fun onReceive(context: Context, intent: Intent) {
        when (intent.action) {
            ACTION_DISCONNECT -> {
                // Persist the intent first: if the process dies before JS acts, a restart
                // must not helpfully reconnect something the user just switched off.
                AgentPrefs.setEnabled(context, false)
                emit(context, "stop")
                // Give the runtime a moment to close the socket; the service stops itself
                // from JS via stopConnectionService(). Stop it here too as a backstop, in
                // case there is no live JS context to receive the event at all.
                if (!hasReactContext(context)) {
                    stopService(context)
                }
            }
        }
    }

    private fun emit(context: Context, action: String) {
        val reactContext = reactContext(context) ?: return
        val payload = Arguments.createMap().apply { putString("action", action) }
        reactContext
            .getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
            .emit(EVENT, payload)
    }

    private fun reactContext(context: Context): ReactContext? {
        val host = (context.applicationContext as? ReactApplication)?.reactNativeHost ?: return null
        val reactContext = host.reactInstanceManager.currentReactContext
        return if (reactContext?.hasActiveReactInstance() == true) reactContext else null
    }

    private fun hasReactContext(context: Context): Boolean = reactContext(context) != null

    private fun stopService(context: Context) {
        val stop = Intent(context, ConnectionService::class.java).apply {
            action = ConnectionService.ACTION_STOP
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            context.startForegroundService(stop)
        } else {
            context.startService(stop)
        }
    }
}

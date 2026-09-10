package com.drs.agent

import android.content.Context

/**
 * The "should this agent be connected" flag.
 *
 * It lives in SharedPreferences rather than AsyncStorage because native code needs to
 * read it at moments when there is no JS context to ask — a notification action arriving
 * after the process was recycled, or a service restarted by START_STICKY. AsyncStorage
 * remains the home of the enrolled identity, which only JS ever reads.
 *
 * The distinction it records is the one the UI cannot: an agent that is offline because
 * the network dropped should reconnect, and one that is offline because the user pressed
 * Disconnect should stay that way.
 */
object AgentPrefs {
    private const val FILE = "drs_agent_prefs"
    private const val KEY_ENABLED = "enabled"

    fun isEnabled(context: Context): Boolean =
        prefs(context).getBoolean(KEY_ENABLED, false)

    fun setEnabled(context: Context, enabled: Boolean) {
        prefs(context).edit().putBoolean(KEY_ENABLED, enabled).apply()
    }

    private fun prefs(context: Context) =
        context.applicationContext.getSharedPreferences(FILE, Context.MODE_PRIVATE)
}

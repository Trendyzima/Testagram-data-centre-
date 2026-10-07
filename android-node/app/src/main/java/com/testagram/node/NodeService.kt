package com.testagram.node

import android.app.Notification
import android.app.NotificationChannel
import android.app.Service
import android.content.Intent
import android.os.IBinder
import android.os.StatFs
import android.util.Log
import org.json.JSONObject
import java.io.File

class NodeService : Service() {
    companion object {
        const val PREFS = "node-runtime"
        const val KEY_RUNNING = "running"
        const val KEY_STATE = "state"
        const val KEY_LAST_HEARTBEAT = "last_heartbeat"
    }

    @Volatile private var stopping = false

    override fun onCreate() {
        super.onCreate()
        val channel = NotificationChannel("testagram-node", "Testagram VPS", android.app.NotificationManager.IMPORTANCE_LOW)
        getSystemService(android.app.NotificationManager::class.java).createNotificationChannel(channel)
        val notification: Notification = Notification.Builder(this, "testagram-node")
            .setContentTitle("Testagram VPS")
            .setContentText("Local node is running")
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setOngoing(true)
            .build()
        startForeground(42, notification)
        getSharedPreferences(PREFS, MODE_PRIVATE).edit()
            .putBoolean(KEY_RUNNING, true).putString(KEY_STATE, "Starting").apply()

        Thread {
            val prefs = getSharedPreferences("node-config", MODE_PRIVATE)
            val url = prefs.getString("control_plane_url", "") ?: ""
            val bootstrap = prefs.getString("bootstrap_token", "") ?: ""
            val name = prefs.getString("node_name", "android-node") ?: "android-node"
            if (url.isBlank() || bootstrap.isBlank()) {
                publish("Configuration required")
                stopSelf()
                return@Thread
            }
            NodeClient(NodeConfig(url, bootstrap, name)).runForever(
                shouldStop = { stopping },
                onState = { state ->
                    getSharedPreferences(PREFS, MODE_PRIVATE).edit()
                        .putString(KEY_STATE, state)
                        .putLong(KEY_LAST_HEARTBEAT, System.currentTimeMillis()).apply()
                    Log.i("TestagramVPS", state)
                }
            )
            publish("Stopped")
        }.start()
    }

    private fun publish(state: String) {
        getSharedPreferences(PREFS, MODE_PRIVATE).edit()
            .putBoolean(KEY_RUNNING, state != "Stopped")
            .putString(KEY_STATE, state).apply()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == "STOP_VPS") {
            stopping = true
            publish("Stopping")
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        stopping = true
        getSharedPreferences(PREFS, MODE_PRIVATE).edit()
            .putBoolean(KEY_RUNNING, false).putString(KEY_STATE, "Stopped").apply()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null
}

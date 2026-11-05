package com.testagram.node

import android.app.*
import android.content.Intent
import android.os.IBinder
import android.util.Log

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
        val channel = NotificationChannel("testagram-node", "Testagram Node B", NotificationManager.IMPORTANCE_LOW)
        getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
        startForeground(
            42,
            Notification.Builder(this, "testagram-node")
                .setContentTitle("Testagram Node B")
                .setContentText("Removable SD-card storage node")
                .setSmallIcon(android.R.drawable.stat_sys_upload)
                .setOngoing(true)
                .build()
        )

        getSharedPreferences(PREFS, 0).edit()
            .putBoolean(KEY_RUNNING, true)
            .putString(KEY_STATE, "Checking removable SD card")
            .apply()

        val storage = StorageVolumeManager(this)
        val layout = storage.ensureLayout()
        val writeCheck = storage.verifyWritable()
        if (!storage.hasPersistentAccess() || layout.isFailure || writeCheck.isFailure || storage.selectedCardInfo() == null) {
            val reason = layout.exceptionOrNull()?.message
                ?: writeCheck.exceptionOrNull()?.message
                ?: "A mounted, writable removable SD card is required"
            publish("STOPPED • SD CARD CHECK FAILED • $reason")
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
            return
        }

        startForegroundService(Intent(this, LocalMediaService::class.java))
        Thread {
            val prefs = getSharedPreferences("node-config", 0)
            val url = prefs.getString("control_plane_url", "") ?: ""
            val bootstrap = prefs.getString("bootstrap_token", "") ?: ""
            val nodeName = prefs.getString("node_name", "android-node-b") ?: "android-node-b"
            if (url.isBlank() || bootstrap.isBlank()) {
                publish("Configuration required")
                stopSelf()
                return@Thread
            }
            NodeClient(this, NodeConfig(url, bootstrap, nodeName)).runForever(
                shouldStop = { stopping },
                onState = { state ->
                    getSharedPreferences(PREFS, 0).edit()
                        .putString(KEY_STATE, state)
                        .putLong(KEY_LAST_HEARTBEAT, System.currentTimeMillis())
                        .apply()
                    Log.i("TestagramNodeB", state)
                }
            )
            publish("Stopped")
        }.start()
    }

    private fun publish(state: String) {
        getSharedPreferences(PREFS, 0).edit()
            .putBoolean(KEY_RUNNING, state != "Stopped" && !state.startsWith("STOPPED"))
            .putString(KEY_STATE, state)
            .apply()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == "STOP_VPS") {
            stopping = true
            publish("Stopping")
            stopService(Intent(this, LocalMediaService::class.java))
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        stopping = true
        stopService(Intent(this, LocalMediaService::class.java))
        getSharedPreferences(PREFS, 0).edit()
            .putBoolean(KEY_RUNNING, false)
            .putString(KEY_STATE, "Stopped")
            .apply()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null
}

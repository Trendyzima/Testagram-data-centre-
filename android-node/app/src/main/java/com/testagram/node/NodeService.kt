package com.testagram.node

import android.app.Notification
import android.app.NotificationChannel
import android.app.Service
import android.content.Intent
import android.os.IBinder

class NodeService : Service() {
    override fun onCreate() {
        super.onCreate()
        val channel = NotificationChannel("testagram-node", "Testagram Node", android.app.NotificationManager.IMPORTANCE_LOW)
        getSystemService(android.app.NotificationManager::class.java).createNotificationChannel(channel)
        val notification: Notification = Notification.Builder(this, "testagram-node")
            .setContentTitle("Testagram Node")
            .setContentText("Connecting to the Testagram VPS control plane")
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setOngoing(true)
            .build()
        startForeground(42, notification)

        Thread {
            val prefs = getSharedPreferences("node-config", MODE_PRIVATE)
            val url = prefs.getString("control_plane_url", "") ?: ""
            val bootstrap = prefs.getString("bootstrap_token", "") ?: ""
            val name = prefs.getString("node_name", "android-node") ?: "android-node"
            if (url.isBlank() || bootstrap.isBlank()) return@Thread
            NodeClient(NodeConfig(url, bootstrap, name)).runForever { state ->
                android.util.Log.i("TestagramNode", state)
            }
        }.start()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int = START_STICKY
    override fun onBind(intent: Intent?): IBinder? = null
}

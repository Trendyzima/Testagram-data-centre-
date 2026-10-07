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
            .setContentText("Node is available for local storage and control-plane work")
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setOngoing(true)
            .build()
        startForeground(42, notification)
    }
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int = START_STICKY
    override fun onBind(intent: Intent?): IBinder? = null
}

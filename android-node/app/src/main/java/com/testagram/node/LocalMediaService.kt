package com.testagram.node
import android.app.*
import android.content.Intent
import android.os.IBinder
class LocalMediaService:Service(){
 companion object{const val PREFS="media-runtime";const val KEY_RUNNING="running";const val KEY_STATE="state";const val KEY_PORT="port";private const val PORT=8788}
 private var server:LocalMediaServer?=null
 override fun onCreate(){super.onCreate();val c=NotificationChannel("testagram-media","Testagram media edge",NotificationManager.IMPORTANCE_LOW);getSystemService(NotificationManager::class.java).createNotificationChannel(c);startForeground(43,Notification.Builder(this,"testagram-media").setContentTitle("Testagram local media").setContentText("Serving local media").setSmallIcon(android.R.drawable.stat_sys_upload).setOngoing(true).build());val s=LocalMediaServer(this,PORT);server=s;val ok=s.start();getSharedPreferences(PREFS,0).edit().putBoolean(KEY_RUNNING,ok).putString(KEY_STATE,if(ok)"READY" else "STORAGE NOT READY").putInt(KEY_PORT,PORT).apply();if(!ok)stopSelf()}
 override fun onStartCommand(i:Intent?,f:Int,id:Int)=if(i?.action=="STOP_MEDIA"){stopSelf();START_STICKY}else START_STICKY
 override fun onDestroy(){server?.stop();getSharedPreferences(PREFS,0).edit().putBoolean(KEY_RUNNING,false).putString(KEY_STATE,"STOPPED").apply();stopForeground(STOP_FOREGROUND_REMOVE);super.onDestroy()}
 override fun onBind(i:Intent?):IBinder?=null
}
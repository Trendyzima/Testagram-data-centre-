package com.testagram.node
import android.app.*
import android.content.Intent
import android.os.IBinder
import android.util.Log
class NodeService:Service(){
 companion object{const val PREFS="node-runtime";const val KEY_RUNNING="running";const val KEY_STATE="state";const val KEY_LAST_HEARTBEAT="last_heartbeat"}
 @Volatile private var stopping=false
 override fun onCreate(){super.onCreate();val c=NotificationChannel("testagram-node","Testagram VPS",NotificationManager.IMPORTANCE_LOW);getSystemService(NotificationManager::class.java).createNotificationChannel(c);startForeground(42,Notification.Builder(this,"testagram-node").setContentTitle("Testagram VPS").setContentText("Local node is running").setSmallIcon(android.R.drawable.stat_sys_upload).setOngoing(true).build());getSharedPreferences(PREFS,0).edit().putBoolean(KEY_RUNNING,true).putString(KEY_STATE,"Starting").apply();startForegroundService(Intent(this,LocalMediaService::class.java));Thread{val p=getSharedPreferences("node-config",0);val u=p.getString("control_plane_url","")?:"";val b=p.getString("bootstrap_token","")?:"";val n=p.getString("node_name","android-node")?:"android-node";if(u.isBlank()||b.isBlank()){publish("Configuration required");stopSelf();return@Thread};NodeClient(NodeConfig(u,b,n)).runForever(shouldStop={stopping},onState={s->getSharedPreferences(PREFS,0).edit().putString(KEY_STATE,s).putLong(KEY_LAST_HEARTBEAT,System.currentTimeMillis()).apply();Log.i("TestagramVPS",s)});publish("Stopped")}.start()}
 private fun publish(s:String){getSharedPreferences(PREFS,0).edit().putBoolean(KEY_RUNNING,s!="Stopped").putString(KEY_STATE,s).apply()}
 override fun onStartCommand(i:Intent?,f:Int,id:Int):Int{if(i?.action=="STOP_VPS"){stopping=true;publish("Stopping");stopService(Intent(this,LocalMediaService::class.java));stopForeground(STOP_FOREGROUND_REMOVE);stopSelf()};return START_STICKY}
 override fun onDestroy(){stopping=true;stopService(Intent(this,LocalMediaService::class.java));getSharedPreferences(PREFS,0).edit().putBoolean(KEY_RUNNING,false).putString(KEY_STATE,"Stopped").apply();super.onDestroy()}
 override fun onBind(i:Intent?):IBinder?=null
}
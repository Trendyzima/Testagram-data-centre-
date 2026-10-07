package com.testagram.node
import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.StatFs
import android.widget.*
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.util.Locale

class MainActivity:Activity(){
 private val treeRequest=4101;private val handler=Handler(Looper.getMainLooper());private lateinit var status:TextView;private lateinit var health:TextView;private lateinit var metrics:TextView;private lateinit var power:Button;private lateinit var cp:EditText;private lateinit var boot:EditText;private lateinit var name:EditText;private lateinit var secret:EditText
 private val refresh=object:Runnable{override fun run(){refreshDashboard();handler.postDelayed(this,3000)}}
 override fun onCreate(b:Bundle?){super.onCreate(b);val p=getSharedPreferences("node-config",0);status=label("TESTAGRAM VPS");health=label("Health: checking");metrics=label("System: checking");cp=input("Control-plane URL",p.getString("control_plane_url",""));boot=input("Bootstrap token",p.getString("bootstrap_token",""));boot.inputType=129;name=input("Node name",p.getString("node_name","android-node"));secret=input("Media signing secret (32+ chars)",p.getString("media_signing_secret",""));secret.inputType=129;power=Button(this);power.setOnClickListener{toggle()};val save=Button(this);save.text="SAVE CONNECTION";save.setOnClickListener{saveConfig();toast("Connection saved")};val choose=Button(this);choose.text="SELECT LOCAL STORAGE";choose.setOnClickListener{startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION or Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION),treeRequest)};val box=LinearLayout(this);box.orientation=LinearLayout.VERTICAL;box.setPadding(24,24,24,24);box.addView(status);box.addView(health);box.addView(metrics);box.addView(power);box.addView(cp);box.addView(boot);box.addView(name);box.addView(secret);box.addView(save);box.addView(choose);box.addView(label("Android node: local storage is authoritative for media when a SAF volume is selected. The media edge serves signed uploads and playback."));setContentView(ScrollView(this).apply{addView(box)});refreshDashboard()}
 private fun saveConfig(){getSharedPreferences("node-config",0).edit().putString("control_plane_url",cp.text.toString().trim()).putString("bootstrap_token",boot.text.toString()).putString("node_name",name.text.toString().trim().ifBlank{"android-node"}).putString("media_signing_secret",secret.text.toString()).apply()}
 private fun toggle(){if(getSharedPreferences(NodeService.PREFS,0).getBoolean(NodeService.KEY_RUNNING,false)){startService(Intent(this,NodeService::class.java).setAction("STOP_VPS"))}else{if(cp.text.isBlank()||boot.text.isBlank()){toast("Enter control-plane URL and bootstrap token");return};saveConfig();startForegroundService(Intent(this,NodeService::class.java))}}
 private fun refreshDashboard(){val r=getSharedPreferences(NodeService.PREFS,0);val run=r.getBoolean(NodeService.KEY_RUNNING,false);power.text=if(run)"TURN VPS OFF" else "TURN VPS ON";status.text="TESTAGRAM VPS\n"+if(run)"● RUNNING" else "○ STOPPED"+"\n"+(r.getString(NodeService.KEY_STATE,"Idle")?:"Idle");val storage=StorageVolumeManager(this);val ready=storage.hasPersistentAccess()&&storage.ensureLayout().isSuccess;val media=getSharedPreferences(LocalMediaService.PREFS,0);val s=StatFs(filesDir.path);val total=s.blockCountLong*s.blockSizeLong;val free=s.availableBlocksLong*s.blockSizeLong;metrics.text=String.format(Locale.US,"Storage\nTotal %.2f GB\nFree %.2f GB\nUsed %.2f GB\n\nCPU cores %d\nLocal storage authority: %s\nLocal media edge: %s",total/1e9,free/1e9,(total-free)/1e9,Runtime.getRuntime().availableProcessors(),if(ready)"READY" else "NOT SELECTED",media.getString(LocalMediaService.KEY_STATE,"STOPPED"));val u=cp.text.toString().trim();if(u.isBlank()){health.text="Health: NOT CONFIGURED"}else Thread{val ok=try{val c=URL(u.trimEnd('/')+"/readyz").openConnection() as HttpURLConnection;c.connectTimeout=3000;c.readTimeout=3000;c.responseCode in 200..299}catch(_:Exception){false};runOnUiThread{health.text="Health: "+if(ok)"● READY" else "○ UNAVAILABLE"}}.start()}
 private fun label(s:String)=TextView(this).apply{text=s;textSize=16f;setPadding(0,12,0,12)}
 private fun input(h:String,v:String?)=EditText(this).apply{hint=h;setText(v?:"")}
 private fun toast(s:String)=Toast.makeText(this,s,Toast.LENGTH_SHORT).show()
 override fun onResume(){super.onResume();handler.post(refresh)}
 override fun onPause(){handler.removeCallbacks(refresh);super.onPause()}
 override fun onActivityResult(r:Int,c:Int,d:Intent?){super.onActivityResult(r,c,d);if(r==treeRequest&&c==RESULT_OK)d?.data?.let{StorageVolumeManager(this).rememberTree(it)}}
}
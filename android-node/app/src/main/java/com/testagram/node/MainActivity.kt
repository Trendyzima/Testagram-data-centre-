package com.testagram.node

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.StatFs
import android.graphics.Color
import android.view.Gravity
import android.widget.*
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.util.Locale

class MainActivity : Activity() {
    private val treeRequest = 4101
    private val handler = Handler(Looper.getMainLooper())
    private lateinit var status: TextView
    private lateinit var health: TextView
    private lateinit var metrics: TextView
    private lateinit var power: Button
    private lateinit var controlPlane: EditText
    private lateinit var bootstrap: EditText
    private lateinit var nodeName: EditText

    private val refreshTask = object : Runnable {
        override fun run() { refreshDashboard(); handler.postDelayed(this, 3000) }
    }

    override fun onCreate(b: Bundle?) {
        super.onCreate(b)
        val prefs = getSharedPreferences("node-config", MODE_PRIVATE)
        val runtime = getSharedPreferences(NodeService.PREFS, MODE_PRIVATE)

        val root = File(filesDir, "node-data/volumes").apply { mkdirs() }
        status = label("TESTAGRAM VPS")
        health = label("Health: checking…")
        metrics = label("System: checking…")
        controlPlane = input("Control-plane URL", prefs.getString("control_plane_url", ""))
        bootstrap = input("Bootstrap token", prefs.getString("bootstrap_token", ""))
        bootstrap.inputType = 0x00000081
        nodeName = input("Node name", prefs.getString("node_name", "android-node"))

        power = Button(this).apply {
            text = if (runtime.getBoolean(NodeService.KEY_RUNNING, false)) "TURN VPS OFF" else "TURN VPS ON"
            setOnClickListener { toggleVps() }
        }
        val save = Button(this).apply {
            text = "SAVE CONNECTION"
            setOnClickListener {
                prefs.edit().putString("control_plane_url", controlPlane.text.toString().trim())
                    .putString("bootstrap_token", bootstrap.text.toString())
                    .putString("node_name", nodeName.text.toString().trim().ifBlank { "android-node" }).apply()
                toast("Connection saved")
                refreshDashboard()
            }
        }
        val choose = Button(this).apply {
            text = "SELECT LOCAL STORAGE"
            setOnClickListener {
                startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
                    addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION or Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION)
                }, treeRequest)
            }
        }

        val scroll = ScrollView(this)
        scroll.addView(LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(28, 28, 28, 28)
            addView(status)
            addView(health)
            addView(metrics)
            addView(power)
            addView(controlPlane)
            addView(bootstrap)
            addView(nodeName)
            addView(save)
            addView(choose)
            addView(label("Runtime
The APK controls the Android node and its local storage. Full Docker/Supabase Linux services require a Linux/container runtime; Android itself cannot run Docker containers natively without an additional runtime/root layer."))
        })
        setContentView(scroll)
        refreshDashboard()
    }

    private fun toggleVps() {
        if (getSharedPreferences(NodeService.PREFS, MODE_PRIVATE).getBoolean(NodeService.KEY_RUNNING, false)) {
            startService(Intent(this, NodeService::class.java).setAction("STOP_VPS"))
        } else {
            if (controlPlane.text.isBlank() || bootstrap.text.isBlank()) {
                toast("Enter the control-plane URL and bootstrap token first"); return
            }
            getSharedPreferences("node-config", MODE_PRIVATE).edit()
                .putString("control_plane_url", controlPlane.text.toString().trim())
                .putString("bootstrap_token", bootstrap.text.toString())
                .putString("node_name", nodeName.text.toString().trim().ifBlank { "android-node" }).apply()
            startForegroundService(Intent(this, NodeService::class.java))
        }
        handler.postDelayed({ refreshDashboard() }, 500)
    }

    private fun refreshDashboard() {
        val runtime = getSharedPreferences(NodeService.PREFS, MODE_PRIVATE)
        val running = runtime.getBoolean(NodeService.KEY_RUNNING, false)
        power.text = if (running) "TURN VPS OFF" else "TURN VPS ON"
        power.setTextColor(if (running) Color.rgb(180, 20, 20) else Color.rgb(20, 120, 50))
        status.text = "TESTAGRAM VPS\n" + if (running) "● RUNNING" else "○ STOPPED" +
            "\n" + (runtime.getString(NodeService.KEY_STATE, "Idle") ?: "Idle")
        val cp = controlPlane.text.toString().trim()
        health.text = "Health: " + if (cp.isBlank()) "NOT CONFIGURED" else "checking control plane…"
        if (cp.isNotBlank()) Thread {
            val ok = try {
                val c = (URL(cp.trimEnd('/') + "/readyz").openConnection() as HttpURLConnection)
                c.connectTimeout = 3000; c.readTimeout = 3000; c.responseCode in 200..299
            } catch (_: Exception) { false }
            runOnUiThread { health.text = "Health: " + if (ok) "● READY" else "○ UNAVAILABLE" }
        }.start()
        val s = StatFs(rootDir().path)
        val total = s.blockCountLong * s.blockSizeLong
        val free = s.availableBlocksLong * s.blockSizeLong
        metrics.text = String.format(Locale.US, "Storage\nTotal: %.2f GB\nFree: %.2f GB\nUsed: %.2f GB\n\nCPU cores: %d\nLast heartbeat: %s",
            total/1e9, free/1e9, (total-free)/1e9, Runtime.getRuntime().availableProcessors(),
            runtime.getLong(NodeService.KEY_LAST_HEARTBEAT, 0L).let { if(it==0L) "never" else java.text.DateFormat.getTimeInstance().format(java.util.Date(it)) })
    }

    private fun rootDir() = File(filesDir, "node-data/volumes").apply { mkdirs() }
    private fun label(s:String) = TextView(this).apply { text=s; textSize=17f; setPadding(0,14,0,14) }
    private fun input(h:String,v:String?) = EditText(this).apply { hint=h; setText(v ?: "") }
    private fun toast(s:String) = Toast.makeText(this,s,Toast.LENGTH_SHORT).show()

    override fun onResume() { super.onResume(); handler.post(refreshTask) }
    override fun onPause() { handler.removeCallbacks(refreshTask); super.onPause() }

    override fun onActivityResult(requestCode:Int,resultCode:Int,data:Intent?) {
        super.onActivityResult(requestCode,resultCode,data)
        if(requestCode==treeRequest && resultCode==RESULT_OK) data?.data?.let { StorageVolumeManager(this).rememberTree(it) }
    }
}

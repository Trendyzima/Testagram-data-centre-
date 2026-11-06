package com.testagram.node

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
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
    private lateinit var cp: EditText
    private lateinit var boot: EditText
    private lateinit var name: EditText
    private lateinit var secret: EditText
    private lateinit var storage: StorageVolumeManager

    private val refresh = object : Runnable {
        override fun run() {
            refreshDashboard()
            handler.postDelayed(this, 5000)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        storage = StorageVolumeManager(this)
        val prefs = getSharedPreferences("node-config", 0)

        status = label("TESTAGRAM NODE B")
        health = label("Backend health: checking")
        metrics = label("SD-card storage: checking")

        cp = input("Control-plane URL", prefs.getString("control_plane_url", ""))
        boot = input("Bootstrap token", prefs.getString("bootstrap_token", ""))
        boot.inputType = 129
        name = input("Node name", prefs.getString("node_name", "android-node-b"))
        secret = input("Media signing secret (32+ characters)", prefs.getString("media_signing_secret", ""))
        secret.inputType = 129

        power = Button(this).apply { setOnClickListener { toggleNode() } }
        val save = Button(this).apply {
            text = "SAVE CONNECTION"
            setOnClickListener { saveConfig(); toast("Connection saved") }
        }
        val choose = Button(this).apply {
            text = "SELECT REMOVABLE SD CARD"
            setOnClickListener {
                val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).addFlags(
                    Intent.FLAG_GRANT_READ_URI_PERMISSION or
                        Intent.FLAG_GRANT_WRITE_URI_PERMISSION or
                        Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION
                )
                startActivityForResult(intent, treeRequest)
            }
        }

        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(24, 24, 24, 24)
            addView(status)
            addView(health)
            addView(metrics)
            addView(power)
            addView(choose)
            addView(cp)
            addView(boot)
            addView(name)
            addView(secret)
            addView(save)
            addView(label("Storage rule: media is written only to the selected removable SD card. Internal phone storage is never used as a media fallback. Video transcoding is disabled on this node to reduce heat, CPU and battery use."))
        }
        setContentView(ScrollView(this).apply { addView(box) })
        refreshDashboard()
    }

    private fun saveConfig() {
        getSharedPreferences("node-config", 0).edit()
            .putString("control_plane_url", cp.text.toString().trim())
            .putString("bootstrap_token", boot.text.toString())
            .putString("node_name", name.text.toString().trim().ifBlank { "android-node-b" })
            .putString("media_signing_secret", secret.text.toString())
            .apply()
    }

    private fun toggleNode() {
        val runtime = getSharedPreferences(NodeService.PREFS, 0)
        if (runtime.getBoolean(NodeService.KEY_RUNNING, false)) {
            startService(Intent(this, NodeService::class.java).setAction("STOP_VPS"))
            return
        }
        if (storage.selectedCardInfo() == null || !storage.hasPersistentAccess()) {
            toast("Select a mounted removable SD card first. Internal storage is not accepted.")
            return
        }
        val layout = storage.ensureLayout()
        if (layout.isFailure) {
            toast(layout.exceptionOrNull()?.message ?: "SD-card setup failed")
            return
        }
        val writeCheck = storage.verifyWritable()
        if (writeCheck.isFailure) {
            toast(writeCheck.exceptionOrNull()?.message ?: "SD card write test failed")
            return
        }
        if (cp.text.isBlank() || boot.text.isBlank()) {
            toast("Enter the control-plane URL and bootstrap token")
            return
        }
        saveConfig()
        startForegroundService(Intent(this, NodeService::class.java))
    }

    private fun refreshDashboard() {
        val runtime = getSharedPreferences(NodeService.PREFS, 0)
        val running = runtime.getBoolean(NodeService.KEY_RUNNING, false)
        power.text = if (running) "TURN NODE OFF" else "TURN NODE ON"
        status.text = "TESTAGRAM NODE B\n" +
            (if (running) "● RUNNING" else "○ STOPPED") +
            "\n" + (runtime.getString(NodeService.KEY_STATE, "Idle") ?: "Idle")

        val card = storage.selectedCardInfo()
        val cards = storage.mountedRemovableCards()
        val media = getSharedPreferences(LocalMediaService.PREFS, 0)
        metrics.text = buildString {
            append("REMOVABLE SD-CARD STORAGE\n")
            append("Selected card: ").append(card?.name ?: "NOT SELECTED").append('\n')
            append("Card identity: ").append(card?.uuid ?: "—").append('\n')
            if (card != null) {
                append("Total: ").append(formatBytes(card.totalBytes)).append('\n')
                append("Used: ").append(formatBytes(card.usedBytes)).append('\n')
                append("Available: ").append(formatBytes(card.availableBytes)).append('\n')
            } else {
                append("Total / used / free: unavailable until a mounted SD card is selected\n")
            }
            append("\nDetected removable cards:\n")
            append(if (cards.isEmpty()) "No mounted removable SD card detected" else cards.joinToString("\n"))
            append("\n\nInternal phone storage: EXCLUDED")
            append("\nVideo transcoding: DISABLED")
            append("\nMedia edge: ").append(media.getString(LocalMediaService.KEY_STATE, "STOPPED"))
        }

        val url = cp.text.toString().trim()
        if (url.isBlank()) {
            health.text = "Backend health: NOT CONFIGURED"
        } else {
            Thread {
                val ready = try {
                    val connection = URL(url.trimEnd('/') + "/readyz").openConnection() as HttpURLConnection
                    connection.connectTimeout = 3000
                    connection.readTimeout = 3000
                    try { connection.responseCode in 200..299 } finally { connection.disconnect() }
                } catch (_: Exception) { false }
                runOnUiThread { health.text = "Backend health: " + if (ready) "● READY" else "○ UNAVAILABLE" }
            }.start()
        }
    }

    private fun formatBytes(bytes: Long): String =
        String.format(Locale.US, "%.2f GiB", bytes / (1024.0 * 1024.0 * 1024.0))

    private fun label(value: String) = TextView(this).apply {
        text = value
        textSize = 15f
        setPadding(0, 12, 0, 12)
    }

    private fun input(hintText: String, value: String?) = EditText(this).apply {
        hint = hintText
        setText(value ?: "")
    }

    private fun toast(message: String) =
        Toast.makeText(this, message, Toast.LENGTH_LONG).show()

    override fun onResume() {
        super.onResume()
        handler.post(refresh)
    }

    override fun onPause() {
        handler.removeCallbacks(refresh)
        super.onPause()
    }

    @Deprecated("Deprecated in Android API; retained for broad device compatibility")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != treeRequest || resultCode != RESULT_OK) return
        val uri = data?.data ?: return
        try {
            storage.rememberTree(uri)
            val layout = storage.ensureLayout().getOrThrow()
            storage.verifyWritable().getOrThrow()
            toast("SD card selected and verified. Created ${layout.size} storage directories.")
        } catch (error: Exception) {
            toast(error.message ?: "That location is not an eligible removable SD card.")
        }
        refreshDashboard()
    }
}

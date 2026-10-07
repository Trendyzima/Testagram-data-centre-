package com.testagram.node

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import java.io.File

class MainActivity : Activity() {
    private lateinit var status: TextView
    private val treeRequest = 4101

    override fun onCreate(b: Bundle?) {
        super.onCreate(b)
        val root = File(filesDir, "node-data/volumes").apply { mkdirs() }
        val prefs = getSharedPreferences("node-config", MODE_PRIVATE)

        status = TextView(this).apply { setPadding(32, 32, 32, 16) }
        val controlPlane = EditText(this).apply {
            hint = "VPS control-plane HTTPS URL"
            setText(prefs.getString("control_plane_url", ""))
        }
        val bootstrap = EditText(this).apply {
            hint = "Node bootstrap token"
            setText(prefs.getString("bootstrap_token", ""))
        }
        val nodeName = EditText(this).apply {
            hint = "Node name"
            setText(prefs.getString("node_name", "android-node"))
        }

        val save = Button(this).apply {
            text = "Save node connection"
            setOnClickListener {
                prefs.edit()
                    .putString("control_plane_url", controlPlane.text.toString().trim())
                    .putString("bootstrap_token", bootstrap.text.toString())
                    .putString("node_name", nodeName.text.toString().trim().ifBlank { "android-node" })
                    .apply()
                status.text = "Node connection settings saved. Start the node to connect."
            }
        }

        val choose = Button(this).apply {
            text = "Choose microSD / external storage"
            setOnClickListener {
                startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
                    addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                    addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
                    addFlags(Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION)
                }, treeRequest)
            }
        }

        val start = Button(this).apply {
            text = "Start Testagram Node"
            setOnClickListener {
                if (controlPlane.text.isBlank() || bootstrap.text.isBlank()) {
                    status.text = "Enter the VPS control-plane URL and bootstrap token first."
                    return@setOnClickListener
                }
                prefs.edit()
                    .putString("control_plane_url", controlPlane.text.toString().trim())
                    .putString("bootstrap_token", bootstrap.text.toString())
                    .putString("node_name", nodeName.text.toString().trim().ifBlank { "android-node" })
                    .apply()
                startForegroundService(Intent(this@MainActivity, NodeService::class.java))
                status.text = "Node service started."
            }
        }

        val refresh = Button(this).apply {
            text = "Refresh storage"
            setOnClickListener { refreshStorage(root) }
        }

        setContentView(LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            addView(status)
            addView(controlPlane)
            addView(bootstrap)
            addView(nodeName)
            addView(save)
            addView(choose)
            addView(start)
            addView(refresh)
        })
        refreshStorage(root)
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == treeRequest && resultCode == RESULT_OK) {
            data?.data?.let { StorageVolumeManager(this).rememberTree(it) }
            refreshStorage(File(filesDir, "node-data/volumes"))
        }
    }

    private fun refreshStorage(root: File) {
        val s = root.statfs()
        val tree = StorageVolumeManager(this).tree()
        status.text = "Testagram Node\n\nApp storage: " + root.absolutePath +
            "\nFree: " + (s.availableBlocksLong * s.blockSizeLong / (1024 * 1024)) + " MB" +
            "\nExternal volume: " + (tree?.toString() ?: "not selected")
    }
}

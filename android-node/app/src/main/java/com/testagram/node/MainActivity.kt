package com.testagram.node

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import java.io.File

class MainActivity : Activity() {
    private lateinit var status: TextView
    private val treeRequest = 4101

    override fun onCreate(b: Bundle?) {
        super.onCreate(b)
        val root = File(filesDir, "node-data/volumes").apply { mkdirs() }
        status = TextView(this).apply { setPadding(32, 48, 32, 32) }
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
                startForegroundService(Intent(this@MainActivity, NodeService::class.java))
                refresh(root)
            }
        }
        val refreshButton = Button(this).apply { text = "Refresh"; setOnClickListener { refresh(root) } }
        setContentView(LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            addView(status); addView(choose); addView(start); addView(refreshButton)
        })
        refresh(root)
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == treeRequest && resultCode == RESULT_OK) {
            data?.data?.let { StorageVolumeManager(this).rememberTree(it) }
            refresh(File(filesDir, "node-data/volumes"))
        }
    }

    private fun refresh(root: File) {
        val s = root.statfs()
        val tree = StorageVolumeManager(this).tree()
        val external = if (tree == null) "not selected" else tree.toString()
        status.text = "Testagram Node\n\nApp storage: " + root.absolutePath +
            "\nFree: " + (s.availableBlocksLong * s.blockSizeLong / (1024 * 1024)) +
            " MB\nExternal volume: " + external
    }
}

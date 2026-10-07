package com.testagram.node

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.documentfile.provider.DocumentFile

class StorageVolumeManager(private val context: Context) {
    private val prefs = context.getSharedPreferences("node-storage", Context.MODE_PRIVATE)
    fun rememberTree(uri: Uri) {
        context.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
        prefs.edit().putString("tree", uri.toString()).apply()
    }
    fun tree(): Uri? = prefs.getString("tree", null)?.let(Uri::parse)
    fun root(): DocumentFile? = tree()?.let { DocumentFile.fromTreeUri(context, it) }
}

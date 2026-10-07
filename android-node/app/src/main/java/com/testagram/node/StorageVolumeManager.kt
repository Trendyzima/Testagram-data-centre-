package com.testagram.node

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.documentfile.provider.DocumentFile

class StorageVolumeManager(private val context: Context) {
    companion object {
        private const val PREFS = "node-storage"
        private const val TREE_KEY = "tree"
        val REQUIRED_DIRECTORIES = listOf(
            "media", "media/posts", "media/posts/images", "media/posts/videos", "media/uploads",
            "media/videos", "media/videos/originals", "media/videos/hls", "media/videos/posters",
            "media/avatars", "media/stories", "media/messages", "media/attachments", "media/tmp",
            "supabase", "nodes"
        )
    }

    private val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun rememberTree(uri: Uri) {
        context.contentResolver.takePersistableUriPermission(
            uri,
            Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
        )
        prefs.edit().putString(TREE_KEY, uri.toString()).apply()
    }

    fun tree(): Uri? = prefs.getString(TREE_KEY, null)?.let(Uri::parse)
    fun root(): DocumentFile? = tree()?.let { DocumentFile.fromTreeUri(context, it) }

    fun hasPersistentAccess(): Boolean =
        root()?.let { it.isDirectory && it.canRead() && it.canWrite() } == true

    fun ensureLayout(): Result<List<String>> = runCatching {
        val root = root() ?: error("local storage is not selected")
        if (!root.isDirectory || !root.canWrite()) error("selected storage is not writable")
        REQUIRED_DIRECTORIES.forEach { ensureDirectory(root, it) }
        REQUIRED_DIRECTORIES
    }

    fun ensureDirectory(relativePath: String): DocumentFile {
        val root = root() ?: error("local storage is not selected")
        return ensureDirectory(root, relativePath)
    }

    fun createFile(relativePath: String, mimeType: String): DocumentFile {
        require(relativePath.isNotBlank() && !relativePath.startsWith("/") && !relativePath.contains("..")) {
            "invalid media path"
        }
        val slash = relativePath.lastIndexOf('/')
        val parent = if (slash < 0) "." else relativePath.substring(0, slash)
        val name = if (slash < 0) relativePath else relativePath.substring(slash + 1)
        require(name.isNotBlank()) { "invalid media filename" }
        return ensureDirectory(parent).createFile(mimeType, name)
            ?: error("failed to create media file")
    }

    fun findFile(relativePath: String): DocumentFile? {
        require(relativePath.isNotBlank() && !relativePath.startsWith("/") && !relativePath.contains("..")) {
            "invalid media path"
        }
        val parts = relativePath.split('/').filter { it.isNotBlank() && it != "." }
        if (parts.isEmpty()) return null
        var current = root() ?: return null
        for (segment in parts.dropLast(1)) {
            current = current.findFile(segment)?.takeIf { it.isDirectory } ?: return null
        }
        return current.findFile(parts.last())
    }

    private fun ensureDirectory(root: DocumentFile, relativePath: String): DocumentFile {
        var current = root
        relativePath.split('/').filter { it.isNotBlank() && it != "." }.forEach { segment ->
            require(segment != "..") { "invalid storage path" }
            current = current.findFile(segment)?.takeIf { it.isDirectory }
                ?: current.createDirectory(segment)
                ?: error("failed to create directory: $segment")
        }
        return current
    }
}

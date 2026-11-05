package com.testagram.node

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.StatFs
import android.os.storage.StorageManager
import android.provider.DocumentsContract
import androidx.documentfile.provider.DocumentFile
import java.io.IOException
import java.util.UUID

data class SdCardInfo(
    val uuid: String,
    val name: String,
    val totalBytes: Long,
    val availableBytes: Long,
    val usedBytes: Long
)

class StorageVolumeManager(private val context: Context) {
    companion object {
        private const val PREFS = "node-storage"
        private const val TREE_KEY = "tree"
        val REQUIRED_DIRECTORIES = listOf(
            "TestagramNode", "TestagramNode/media", "TestagramNode/media/posts",
            "TestagramNode/media/posts/images", "TestagramNode/media/posts/videos",
            "TestagramNode/media/uploads", "TestagramNode/media/videos",
            "TestagramNode/media/videos/originals", "TestagramNode/media/videos/hls",
            "TestagramNode/media/videos/posters", "TestagramNode/media/avatars",
            "TestagramNode/media/stories", "TestagramNode/media/messages",
            "TestagramNode/media/attachments", "TestagramNode/media/tmp",
            "TestagramNode/checksums", "TestagramNode/quarantine"
        )
    }

    private val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
    private val storageManager = context.getSystemService(StorageManager::class.java)

    fun rememberTree(uri: Uri) {
        require(isRemovableCardTree(uri)) {
            "Select a mounted removable SD card. Phone internal storage and cloud providers are not allowed."
        }
        context.contentResolver.takePersistableUriPermission(
            uri,
            Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
        )
        prefs.edit().putString(TREE_KEY, uri.toString()).apply()
    }

    fun tree(): Uri? = prefs.getString(TREE_KEY, null)?.let(Uri::parse)
    fun selectedTreeRoot(): DocumentFile? = tree()?.let { DocumentFile.fromTreeUri(context, it) }

    fun root(): DocumentFile? = selectedTreeRoot()?.findFile("TestagramNode")?.takeIf { it.isDirectory }

    fun hasPersistentAccess(): Boolean {
        val uri = tree() ?: return false
        val root = DocumentFile.fromTreeUri(context, uri) ?: return false
        return isRemovableCardTree(uri) && root.isDirectory && root.canRead() && root.canWrite()
    }

    fun selectedCardInfo(): SdCardInfo? {
        val uri = tree() ?: return null
        val volume = removableVolumeForTree(uri) ?: return null
        if (volume.state != Environment.MEDIA_MOUNTED) return null
        val directory = (if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) volume.directory else null)
            ?: return null
        return runCatching {
            val stat = StatFs(directory.absolutePath)
            val total = stat.totalBytes
            val available = stat.availableBytes
            SdCardInfo(
                uuid = volume.uuid ?: return null,
                name = volume.getDescription(context),
                totalBytes = total,
                availableBytes = available,
                usedBytes = (total - available).coerceAtLeast(0L)
            )
        }.getOrNull()
    }

    fun ensureLayout(): Result<List<String>> = runCatching {
        val root = selectedTreeRoot() ?: error("removable SD card is not selected")
        if (!hasPersistentAccess()) error("selected SD card is unavailable or read-only")
        REQUIRED_DIRECTORIES.forEach { ensureDirectory(root, it) }
        REQUIRED_DIRECTORIES
    }

    fun verifyWritable(): Result<Unit> = runCatching {
        val root = selectedTreeRoot() ?: error("removable SD card is not selected")
        if (!hasPersistentAccess()) error("selected SD card is unavailable or read-only")
        val name = ".testagram-write-${UUID.randomUUID()}.tmp"
        val probe = root.createFile("application/octet-stream", name)
            ?: error("cannot create verification file on SD card")
        try {
            val expected = "testagram-sd-card-write-check".toByteArray(Charsets.UTF_8)
            val output = context.contentResolver.openOutputStream(probe.uri, "w")
                ?: error("cannot write to selected SD card")
            output.use { it.write(expected); it.flush() }
            val input = context.contentResolver.openInputStream(probe.uri)
                ?: error("cannot read verification file from selected SD card")
            val actual = input.use { it.readBytes() }
            if (!expected.contentEquals(actual)) throw IOException("SD card read-back verification failed")
        } finally {
            probe.delete()
        }
    }

    fun createFile(relativePath: String, mimeType: String): DocumentFile {
        validateRelativePath(relativePath)
        val slash = relativePath.lastIndexOf('/')
        val parent = if (slash < 0) "." else relativePath.substring(0, slash)
        val name = if (slash < 0) relativePath else relativePath.substring(slash + 1)
        require(name.isNotBlank()) { "invalid media filename" }
        return ensureDirectory(parent).createFile(mimeType, name)
            ?: error("failed to create media file on selected SD card")
    }

    fun ensureDirectory(relativePath: String): DocumentFile {
        val root = root() ?: error("removable SD card is not selected")
        check(hasPersistentAccess()) { "selected SD card is unavailable" }
        return ensureDirectory(root, relativePath)
    }

    fun findFile(relativePath: String): DocumentFile? {
        validateRelativePath(relativePath)
        val parts = relativePath.split('/').filter { it.isNotBlank() && it != "." }
        if (parts.isEmpty()) return null
        var current = root() ?: return null
        for (segment in parts.dropLast(1)) {
            current = current.findFile(segment)?.takeIf { it.isDirectory } ?: return null
        }
        return current.findFile(parts.last())
    }

    private fun validateRelativePath(path: String) {
        require(path.isNotBlank() && !path.startsWith("/") && !path.contains("..") && !path.contains('\\')) {
            "invalid media path"
        }
    }

    private fun isRemovableCardTree(uri: Uri): Boolean = removableVolumeForTree(uri) != null

    private fun removableVolumeForTree(uri: Uri) =
        runCatching {
            if (uri.authority != "com.android.externalstorage.documents") return@runCatching null
            val documentId = DocumentsContract.getTreeDocumentId(uri)
            val volumeId = documentId.substringBefore(':')
            if (volumeId.equals("primary", ignoreCase = true) || volumeId.isBlank()) return@runCatching null
            storageManager.storageVolumes.firstOrNull { volume ->
                volume.isRemovable &&
                    volume.uuid?.equals(volumeId, ignoreCase = true) == true &&
                    volume.state == Environment.MEDIA_MOUNTED
            }
        }.getOrNull()

    private fun ensureDirectory(root: DocumentFile, relativePath: String): DocumentFile {
        var current = root
        relativePath.split('/').filter { it.isNotBlank() && it != "." }.forEach { segment ->
            require(segment != "..") { "invalid storage path" }
            current = current.findFile(segment)?.takeIf { it.isDirectory }
                ?: current.createDirectory(segment)
                ?: error("failed to create SD-card directory: $segment")
        }
        return current
    }
}

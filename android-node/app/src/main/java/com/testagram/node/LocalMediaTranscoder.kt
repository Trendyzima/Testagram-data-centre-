package com.testagram.node

import android.content.Context
import androidx.documentfile.provider.DocumentFile
import com.arthenica.ffmpegkit.FFmpegKit
import com.arthenica.ffmpegkit.ReturnCode
import java.io.File
import java.io.FileOutputStream
import java.util.concurrent.Executors

class LocalMediaTranscoder(
    private val context: Context,
    private val storage: StorageVolumeManager
) {
    private val executor = Executors.newSingleThreadExecutor()

    fun enqueue(originalKey: String) {
        if (!originalKey.startsWith("media/videos/originals/")) return
        executor.execute {
            runCatching { transcode(originalKey) }
                .onFailure { android.util.Log.e(TAG, "HLS transcode failed for $originalKey", it) }
        }
    }

    fun shutdown() { executor.shutdownNow() }

    private fun transcode(originalKey: String) {
        val source = storage.findFile(originalKey) ?: error("source not found: $originalKey")
        val videoId = originalKey.removePrefix("media/videos/originals/").substringBefore('/')
        require(videoId.isNotBlank()) { "missing video id" }

        val base = File(context.cacheDir, "testagram-transcode/$videoId")
        base.deleteRecursively()
        val input = File(base, "source.mp4")
        input.parentFile?.mkdirs()
        copyDocument(source, input)

        val work = File(base, "hls")
        work.mkdirs()
        val encoder = chooseH264Encoder()

        listOf(
            Variant("240p", 426, 240, "500k"),
            Variant("360p", 640, 360, "900k"),
            Variant("720p", 1280, 720, "2500k")
        ).forEach { variant ->
            val playlist = File(work, "${variant.name}.m3u8")
            val segments = File(work, "${variant.name}_%03d.ts")
            val scale = "scale=w=${variant.width}:h=${variant.height}:force_original_aspect_ratio=decrease:force_divisible_by=2"
            val command = "-y -hide_banner -loglevel error " +
                "-i ${quote(input.absolutePath)} -map 0:v:0 -map 0:a:0? " +
                "-vf ${quote(scale)} -c:v $encoder -pix_fmt yuv420p -b:v ${variant.bitrate} " +
                "-c:a aac -b:a 96k -ac 2 -f hls -hls_time 4 -hls_playlist_type vod " +
                "-hls_flags independent_segments -hls_segment_filename ${quote(segments.absolutePath)} ${quote(playlist.absolutePath)}"
            val session = FFmpegKit.execute(command)
            if (!ReturnCode.isSuccess(session.returnCode)) {
                error("FFmpeg ${variant.name} failed: ${session.failStackTrace ?: session.output ?: "unknown error"}")
            }
        }

        val destination = "media/videos/hls/$videoId"
        storage.ensureDirectory(destination)
        work.listFiles()?.filter { it.isFile }?.forEach { file ->
            writeDocument("$destination/${file.name}", file, mime(file.name))
        }

        val master = File(work, "master.m3u8")
        master.writeText(
            "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-INDEPENDENT-SEGMENTS\n" +
                "#EXT-X-STREAM-INF:BANDWIDTH=600000,RESOLUTION=426x240\n240p.m3u8\n" +
                "#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=640x360\n360p.m3u8\n" +
                "#EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1280x720\n720p.m3u8\n"
        )
        writeDocument("$destination/master.m3u8", master, "application/vnd.apple.mpegurl")

        val poster = File(base, "poster.jpg")
        val posterCommand = "-y -hide_banner -loglevel error -i ${quote(input.absolutePath)} " +
            "-frames:v 1 -vf ${quote("scale=1280:-2:force_divisible_by=2")} ${quote(poster.absolutePath)}"
        val posterSession = FFmpegKit.execute(posterCommand)
        if (!ReturnCode.isSuccess(posterSession.returnCode) || !poster.isFile) {
            error("poster generation failed: ${posterSession.failStackTrace ?: posterSession.output}")
        }
        storage.ensureDirectory("media/videos/posters/$videoId")
        writeDocument("media/videos/posters/$videoId/poster.jpg", poster, "image/jpeg")
        base.deleteRecursively()
    }

    private fun chooseH264Encoder(): String {
        val session = FFmpegKit.execute("-hide_banner -encoders")
        if (!ReturnCode.isSuccess(session.returnCode)) error("FFmpeg encoder probe failed")
        val output = session.output.orEmpty()
        return when {
            output.lines().any { it.contains("libopenh264") && it.contains(" V") } -> "libopenh264"
            output.lines().any { it.contains("h264_mediacodec") && it.contains(" V") } -> "h264_mediacodec"
            else -> error("no supported H.264 encoder in bundled FFmpeg")
        }
    }

    private fun copyDocument(document: DocumentFile, target: File) {
        val input = context.contentResolver.openInputStream(document.uri) ?: error("cannot read ${document.uri}")
        input.use { source -> FileOutputStream(target).use { output -> source.copyTo(output) } }
    }

    private fun writeDocument(key: String, source: File, mimeType: String) {
        storage.findFile(key)?.delete()
        val document = storage.createFile(key, mimeType)
        val output = context.contentResolver.openOutputStream(document.uri, "w")
            ?: error("cannot open output for $key")
        output.use { target -> source.inputStream().use { it.copyTo(target) } }
    }

    private fun mime(name: String): String = when {
        name.endsWith(".m3u8") -> "application/vnd.apple.mpegurl"
        name.endsWith(".ts") -> "video/mp2t"
        name.endsWith(".jpg") -> "image/jpeg"
        else -> "application/octet-stream"
    }

    private fun quote(value: String): String = "'" + value.replace("'", "'\\''") + "'"

    private data class Variant(val name: String, val width: Int, val height: Int, val bitrate: String)

    companion object { private const val TAG = "TestagramMedia" }
}

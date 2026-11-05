package com.testagram.node

import android.app.ActivityManager
import android.content.Context
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

data class NodeConfig(val controlPlaneUrl: String, val bootstrapToken: String, val nodeName: String)

class NodeClient(private val context: Context, private val config: NodeConfig) {
    private var nodeToken: String? = null
    private val storage = StorageVolumeManager(context)

    private fun post(path: String, body: JSONObject): JSONObject? {
        val connection = (URL(config.controlPlaneUrl.trimEnd('/') + path).openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"
            connectTimeout = 10000
            readTimeout = 10000
            doOutput = true
            setRequestProperty("Content-Type", "application/json")
            setRequestProperty("Authorization", "Bearer " + (nodeToken ?: config.bootstrapToken))
        }
        return try {
            connection.outputStream.use { it.write(body.toString().toByteArray(Charsets.UTF_8)) }
            val code = connection.responseCode
            if (code == HttpURLConnection.HTTP_NO_CONTENT) return null
            if (code !in 200..299) throw IllegalStateException("control plane HTTP $code")
            connection.inputStream.bufferedReader().use { JSONObject(it.readText()) }
        } finally {
            connection.disconnect()
        }
    }

    fun ready(): Boolean {
        val connection = (URL(config.controlPlaneUrl.trimEnd('/') + "/readyz").openConnection() as HttpURLConnection).apply {
            requestMethod = "GET"
            connectTimeout = 5000
            readTimeout = 5000
        }
        return try { connection.responseCode in 200..299 } finally { connection.disconnect() }
    }

    private fun card() = storage.selectedCardInfo()
        ?: throw IllegalStateException("selected removable SD card is unavailable")

    private fun memoryBytes(): Long {
        val manager = context.getSystemService(Context.ACTIVITY_SERVICE) as ActivityManager
        val info = ActivityManager.MemoryInfo()
        manager.getMemoryInfo(info)
        return info.totalMem
    }

    private fun storageCapabilities(card: SdCardInfo) = JSONObject()
        .put("localVolumes", true)
        .put("microSD", true)
        .put("storageOnly", true)
        .put("workloadExecution", false)
        .put("storageType", "removable_sd")
        .put("storageIdentity", card.uuid)
        .put("onDeviceTranscoding", false)

    fun register() {
        val card = card()
        val result = post("/v1/nodes/register", JSONObject()
            .put("name", config.nodeName)
            .put("platform", "android")
            .put("arch", "arm64")
            .put("cpuCores", Runtime.getRuntime().availableProcessors())
            .put("memoryBytes", memoryBytes())
            .put("storageBytes", card.totalBytes)
            .put("freeBytes", card.availableBytes)
            .put("endpoint", "outbound-only")
            .put("capabilities", storageCapabilities(card)))
            ?: throw IllegalStateException("empty registration response")
        nodeToken = result.getString("token")
    }

    private fun heartbeat() {
        if (nodeToken == null) register()
        val card = card()
        post("/v1/nodes/heartbeat", JSONObject()
            .put("cpuCores", Runtime.getRuntime().availableProcessors())
            .put("memoryBytes", memoryBytes())
            .put("storageBytes", card.totalBytes)
            .put("freeBytes", card.availableBytes)
            .put("status", "online")
            .put("capabilities", storageCapabilities(card)))
    }

    fun runForever(shouldStop: () -> Boolean, onState: (String) -> Unit) {
        while (!shouldStop()) {
            try {
                if (nodeToken == null) register()
                heartbeat()
                val currentCard = card()
                onState("ONLINE • SD card " + currentCard.name + " • " + currentCard.availableBytes + " bytes free • heartbeat OK")
            } catch (error: Exception) {
                nodeToken = null
                onState("OFFLINE • retry: " + (error.message ?: "unknown"))
            }
            repeat(15) { if (!shouldStop()) Thread.sleep(1000) }
        }
    }
}

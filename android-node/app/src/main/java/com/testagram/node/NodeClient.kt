package com.testagram.node

import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

data class NodeConfig(val controlPlaneUrl: String, val bootstrapToken: String, val nodeName: String)

class NodeClient(private val config: NodeConfig) {
    private var nodeToken: String? = null

    private fun post(path: String, body: JSONObject): JSONObject? {
        val connection = (URL(config.controlPlaneUrl.trimEnd('/') + path).openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"
            connectTimeout = 10000
            readTimeout = 10000
            doOutput = true
            setRequestProperty("Content-Type", "application/json")
            setRequestProperty("Authorization", "Bearer " + (nodeToken ?: config.bootstrapToken))
        }
        connection.outputStream.use { it.write(body.toString().toByteArray(Charsets.UTF_8)) }
        val code = connection.responseCode
        if (code == HttpURLConnection.HTTP_NO_CONTENT) return null
        if (code !in 200..299) throw IllegalStateException("control plane HTTP $code")
        return connection.inputStream.bufferedReader().use { JSONObject(it.readText()) }
    }

    fun ready(): Boolean {
        val connection = (URL(config.controlPlaneUrl.trimEnd('/') + "/readyz").openConnection() as HttpURLConnection).apply {
            requestMethod = "GET"; connectTimeout = 5000; readTimeout = 5000
        }
        return try { connection.responseCode in 200..299 } finally { connection.disconnect() }
    }

    fun register() {
        val result = post("/v1/nodes/register", JSONObject()
            .put("name", config.nodeName).put("platform", "android").put("arch", "arm64")
            .put("cpuCores", Runtime.getRuntime().availableProcessors())
            .put("endpoint", "outbound-only")
            .put("capabilities", JSONObject().put("localVolumes", true).put("microSD", true)))
            ?: throw IllegalStateException("empty registration response")
        nodeToken = result.getString("token")
    }

    private fun heartbeat() {
        if (nodeToken == null) register()
        post("/v1/nodes/heartbeat", JSONObject()
            .put("cpuCores", Runtime.getRuntime().availableProcessors())
            .put("status", "online")
            .put("capabilities", JSONObject().put("localVolumes", true).put("microSD", true)))
    }

    fun runForever(shouldStop: () -> Boolean, onState: (String) -> Unit) {
        while (!shouldStop()) {
            try {
                if (nodeToken == null) register()
                heartbeat()
                onState("ONLINE • heartbeat OK")
            } catch (e: Exception) {
                nodeToken = null
                onState("OFFLINE • retry: " + (e.message ?: "unknown"))
            }
            repeat(15) { if (!shouldStop()) Thread.sleep(1000) }
        }
    }
}

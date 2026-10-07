package com.testagram.node

import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

data class NodeConfig(val controlPlaneUrl: String, val bootstrapToken: String, val nodeName: String)

class NodeClient(private val config: NodeConfig) {
    private var nodeToken: String? = null

    private fun post(path: String, body: JSONObject): JSONObject? {
        val url = URL(config.controlPlaneUrl.trimEnd('/') + path)
        val connection = (url.openConnection() as HttpURLConnection).apply {
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

    fun register() {
        val body = JSONObject()
            .put("name", config.nodeName)
            .put("platform", "android")
            .put("arch", "arm64")
            .put("cpuCores", Runtime.getRuntime().availableProcessors())
            .put("endpoint", "outbound-only")
            .put("capabilities", JSONObject().put("localVolumes", true).put("microSD", true))
        val result = post("/v1/nodes/register", body)
            ?: throw IllegalStateException("empty registration response")
        nodeToken = result.getString("token")
    }

    fun heartbeat() {
        if (nodeToken == null) register()
        post("/v1/nodes/heartbeat", JSONObject()
            .put("cpuCores", Runtime.getRuntime().availableProcessors())
            .put("status", "online")
            .put("capabilities", JSONObject().put("localVolumes", true).put("microSD", true)))
    }

    fun runForever(onState: (String) -> Unit) {
        while (true) {
            try {
                if (nodeToken == null) register()
                heartbeat()
                onState("Connected to VPS control plane")
            } catch (e: Exception) {
                nodeToken = null
                onState("VPS connection retry: " + (e.message ?: "unknown error"))
            }
            Thread.sleep(15000)
        }
    }
}

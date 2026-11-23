package org.netgo.client

import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import org.json.JSONArray
import org.json.JSONObject
import org.netgo.runtime.mobile.Client

object NetGoRuntime {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    @Volatile var client: Client? = null
    val message = MutableStateFlow("正在准备客户端…")
    val profiles = MutableStateFlow<List<Pair<String, String>>>(emptyList())
    val hosts = MutableStateFlow<List<Pair<String, String>>>(emptyList())
    val status = MutableStateFlow(JSONObject("{\"state\":\"disconnected\"}"))
    @Volatile var selectedSite = ""
    private var polling: Job? = null
    suspend fun ready(): Client = withTimeout(15000) { while (client == null) delay(50); client!! }
    fun execute(action: suspend (Client) -> Unit) = scope.launch {
        try { action(ready()) } catch (error: Exception) { message.value = error.message ?: "操作失败" }
    }
    fun attach(value: Client) {
        client = value; polling?.cancel()
        polling = scope.launch {
            refreshProfiles(value)
            while (isActive) { try { status.value = JSONObject(value.status()) } catch (_: Exception) {}; delay(1000) }
        }
        message.value = "导入站点后使用已有账号连接"
    }
    fun refreshProfiles(value: Client) {
        val data = JSONArray(value.profiles())
        profiles.value = (0 until data.length()).map { data.getJSONObject(it).let { p -> p.getString("ID") to p.getString("Name") } }
        if (selectedSite.isEmpty() && profiles.value.isNotEmpty()) selectedSite = profiles.value.first().first
    }
    fun importProfile(text: String) = execute { it.importProfile(text); refreshProfiles(it); message.value = "站点已导入" }
    fun connect(site: String, alias: String, password: String) = execute { selectedSite = site; it.connect(site, alias, password, false) }
    fun disconnect() = execute { NativeRemote.stopCurrent(); it.disconnect(); hosts.value = emptyList() }
    fun refreshHosts() = execute {
        val data = JSONObject(it.remoteCall(selectedSite, "GET", "/netgo/v2/remote", "", "")).getJSONArray("hosts")
        hosts.value = (0 until data.length()).map { i -> data.getJSONObject(i).let { h -> h.getString("id") to h.getString("name") } }
    }
    fun detach(value: Client) { NativeRemote.stopCurrent(); polling?.cancel(); if (client === value) client = null; scope.launch { runCatching { value.close() } } }
}

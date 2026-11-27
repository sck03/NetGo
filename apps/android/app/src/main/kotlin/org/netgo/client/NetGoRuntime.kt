package org.netgo.client

import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.json.JSONArray
import org.json.JSONObject
import org.netgo.runtime.mobile.Client
import java.util.concurrent.atomic.AtomicLong

data class RemoteHost(val id: String, val name: String, val status: String, val capabilities: Int) {
    val ready get() = status == "ready" && capabilities and 1 != 0
    val label get() = name + " · " + when(status) { "ready" -> "可连接"; "busy" -> "使用中"; "offline" -> "离线"; "vpn_offline" -> "VPN 未连接"; "disabled" -> "已停用"; else -> status }
}
object NetGoRuntime {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    @Volatile var client: Client? = null
        private set
    val message = MutableStateFlow("正在准备客户端…")
    val profiles = MutableStateFlow<List<Pair<String, String>>>(emptyList())
    val hosts = MutableStateFlow<List<RemoteHost>>(emptyList())
    val status = MutableStateFlow(JSONObject().put("state", "disconnected"))
    private val connections = Mutex()
    private val epoch = AtomicLong()
    private val hostRequest = AtomicLong()
    @Volatile var selectedSite = ""
        set(value) { if (field != value) { field=value; disconnect() } }
    private var polling: Job? = null
    val running = MutableStateFlow(false)
    private val visible = MutableStateFlow(false)
    private val starting = AtomicLong()
    fun setVisible(value: Boolean) { visible.value = value }
    suspend fun ready(): Client = withTimeout(15000) { while (client == null) delay(50); client!! }
    fun execute(action: suspend (Client) -> Unit): Job {
        val version = epoch.get()
        return scope.launch {
            try { val value = ready(); if (version == epoch.get()) action(value) }
            catch (error: CancellationException) { throw error }
            catch (error: Exception) { if (version == epoch.get()) message.value = error.message ?: "操作失败" }
        }
    }
    @Synchronized fun attach(value: Client) {
        client = value; polling?.cancel()
        polling = scope.launch {
            var profilesRevision = -1L
            while (isActive && client === value) {
                combine(visible, running) { shown, active -> shown || active }.first { it }
                try {
                    val version=epoch.get()
                    val next = JSONObject(value.status())
                    if (client === value && version == epoch.get()) {
                        status.value = next
                        val state = next.optString("state")
                        if (starting.get() == 0L && (state == "disconnected" || state == "failed")) running.value = false
                        if (!next.optBoolean("remote_ready")) { hostRequest.incrementAndGet(); hosts.value = emptyList() }
                        val revision = next.optLong("profiles_revision")
                        if (profilesRevision != revision) {
                            refreshProfiles(value)
                            profilesRevision = revision
                        }
                    }
                } catch (error: CancellationException) { throw error }
                catch (_: Exception) { if (client === value) { hosts.value = emptyList(); status.value = JSONObject().put("state", "disconnected") } }
                delay(2000)
            }
        }
        message.value = "导入站点后使用已有账号连接"
    }
    fun refreshProfiles(value: Client) {
        val data = JSONArray(value.profiles())
        if (client !== value) return
        profiles.value = (0 until data.length()).map { data.getJSONObject(it).let { p -> p.getString("ID") to p.getString("Name") } }
        if (selectedSite.isEmpty() && profiles.value.isNotEmpty()) selectedSite = profiles.value.first().first
    }
    fun importProfile(text: String, approved: Boolean = false) = execute { if (approved) it.importProfileTrusted(text) else it.importProfile(text); refreshProfiles(it); message.value = "站点已导入" }
    fun connect(site: String, alias: String, password: String, internet: Boolean, replace: Boolean) {
        selectedSite = site
        val version = epoch.incrementAndGet()
        starting.set(version); running.value = true; hosts.value = emptyList()
        scope.launch {
            try {
                val value = ready()
                connections.withLock {
                    if (version != epoch.get() || client !== value) return@withLock
                    value.connectMode(site, alias, password, replace, internet)
                    if (version != epoch.get()) value.disconnect()
                }
            } catch (error: CancellationException) { throw error }
            catch (error: Exception) {
                if (version == epoch.get()) { running.value = false; message.value = error.message ?: "连接失败" }
            } finally { starting.compareAndSet(version, 0) }
        }
    }
    fun disconnect() {
        val version=epoch.incrementAndGet(); hostRequest.incrementAndGet(); hosts.value = emptyList(); NativeRemote.stopCurrent()
        // Cancellation must never wait for initialization or an ordinary UI job.
        starting.set(0); running.value = false
        val value = client ?: return
        scope.launch { connections.withLock { if (version == epoch.get() && client === value) runCatching { value.disconnect() } } }
    }
    fun refreshHosts() {
        val site = selectedSite; val version = epoch.get(); val request = hostRequest.incrementAndGet()
        hosts.value = emptyList()
        execute {
            try {
                val result = mutableListOf<RemoteHost>()
                var after = ""
                var pages = 0
                do {
                    check(version == epoch.get() && request == hostRequest.get() && client === it) { "remote_cancelled" }
                    val path = "/netgo/v2/remote" + if (after.isEmpty()) "" else "?after=$after"
                    val page = JSONObject(it.remoteCall(site, "GET", path, "", ""))
                    val data = page.getJSONArray("hosts")
                    for (index in 0 until data.length()) {
                        val host = data.getJSONObject(index)
                        if (host.optInt("available_capabilities") and 1 != 0) result.add(RemoteHost(host.getString("id"),host.getString("name"),host.optString("status"),host.optInt("available_capabilities")))
                    }
                    val next = page.getString("next_after")
                    check(next.isEmpty() || (next > after && Regex("[A-Za-z0-9_-]{1,128}").matches(next))) { "remote_invalid_page" }
                    pages++
                    check(next.isEmpty() || pages < 200) { "remote_inventory_too_large" }
                    after = next
                } while (after.isNotEmpty())
                if (version == epoch.get() && request == hostRequest.get() && client === it) { hosts.value = result; if(result.isEmpty()) message.value = "暂无已授权主机" }
            } catch (e: Exception) { if (request == hostRequest.get()) hosts.value = emptyList(); throw e }
        }
    }
    @Synchronized fun detach(value: Client) {
        if (client === value) {
            epoch.incrementAndGet(); starting.set(0); running.value=false; hostRequest.incrementAndGet(); NativeRemote.stopCurrent(); polling?.cancel(); polling=null; client=null
            hosts.value=emptyList(); status.value=JSONObject().put("state", "disconnected")
        }
        scope.launch { runCatching { value.close() } }
    }
}

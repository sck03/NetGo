package org.netgo.client

import android.app.Activity
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.view.MotionEvent
import android.view.SurfaceHolder
import android.view.SurfaceView
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import kotlinx.coroutines.launch
import org.json.JSONObject
import org.netgo.client.ui.theme.NetGoTheme

private data class ConnectionRequest(val site: String, val alias: String, val password: String, val internet: Boolean, val replace: Boolean)
class MainActivity : ComponentActivity() {
    private var pending: ConnectionRequest? = null
    private val permission = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        val request = pending; pending = null
        if (it.resultCode == Activity.RESULT_OK && request != null) startConnection(request)
        else NetGoRuntime.message.value = "VPN 授权已取消"
    }
    private fun startConnection(value: ConnectionRequest) {
        ContextCompat.startForegroundService(this, Intent(this, NetGoVpnService::class.java).setAction("connect")
            .putExtra("site", value.site).putExtra("alias", value.alias).putExtra("password", value.password).putExtra("internet", value.internet).putExtra("replace", value.replace))
    }
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState); startService(Intent(this, NetGoVpnService::class.java))
        setContent { NetGoTheme { Screen { site, alias, password, internet, replace ->
            val request = ConnectionRequest(site, alias, password, internet, replace); val approval = VpnService.prepare(this)
            if (approval == null) startConnection(request) else { pending = request; permission.launch(approval) }
        } } }
    }
    override fun onStop() { NativeRemote.stopCurrent(); super.onStop() }
}

@Composable private fun Screen(connect: (String, String, String, Boolean, Boolean) -> Unit) {
    val profiles by NetGoRuntime.profiles.collectAsState(); val hosts by NetGoRuntime.hosts.collectAsState()
    val status by NetGoRuntime.status.collectAsState(); val message by NetGoRuntime.message.collectAsState()
    var site by remember { mutableStateOf(NetGoRuntime.selectedSite) }; var alias by remember { mutableStateOf("") }; var password by remember { mutableStateOf("") }
    var select by remember { mutableStateOf(false) }; var target by remember { mutableStateOf("") }
    var internet by remember { mutableStateOf(false) }; var replace by remember { mutableStateOf(false) }
    var pendingImport by remember { mutableStateOf<String?>(null) }
    val context = androidx.compose.ui.platform.LocalContext.current
    LaunchedEffect(profiles) { if (site.isEmpty() && profiles.isNotEmpty()) { site = profiles.first().first; NetGoRuntime.selectedSite = site } }
    val importer = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) NetGoRuntime.scope.launch {
            try {
                val text = context.contentResolver.openInputStream(uri)!!.use { stream ->
                    val output = java.io.ByteArrayOutputStream(); val buffer = ByteArray(4096)
                    while (true) { val count = stream.read(buffer); if (count < 0) break; require(output.size() + count <= 65536); output.write(buffer, 0, count) }
                    output.toByteArray().toString(Charsets.UTF_8)
                }
                val next = JSONObject(text); val previous = org.json.JSONArray(NetGoRuntime.ready().profiles())
                val old = (0 until previous.length()).map { previous.getJSONObject(it) }.firstOrNull { it.getString("ID") == next.getString("ID") }
                if (old != null && (old.optString("Origin") != next.optString("Origin") || old.optString("CA") != next.optString("CA"))) pendingImport = text else NetGoRuntime.importProfile(text)
            } catch (_: Exception) { NetGoRuntime.message.value = "无法读取有效站点配置" }
        }
    }
    if (pendingImport != null) AlertDialog(onDismissRequest = { pendingImport = null }, title = { Text("确认新的站点身份") }, text = { Text("文件更改了站点地址或证书。请确认它来自你的服务器管理员后继续。") }, confirmButton = { TextButton(onClick = { pendingImport?.let { NetGoRuntime.importProfile(it, true) }; pendingImport = null }) { Text("确认导入") } }, dismissButton = { TextButton(onClick = { pendingImport = null }) { Text("取消") } })
    Surface(Modifier.fillMaxSize()) { Column(Modifier.safeDrawingPadding().padding(16.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("NetGo", style = MaterialTheme.typography.headlineMedium)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Box { OutlinedButton(onClick = { select = true }) { Text(profiles.firstOrNull { it.first == site }?.second ?: "选择站点") }
                DropdownMenu(select, onDismissRequest = { select = false }) { profiles.forEach { item -> DropdownMenuItem(text = { Text(item.second) }, onClick = { target = ""; site = item.first; NetGoRuntime.selectedSite = site; select = false }) } }
            }; OutlinedButton(onClick = { importer.launch(arrayOf("application/json", "text/plain")) }) { Text("导入站点") }
        }
        OutlinedTextField(alias, onValueChange = { alias = it }, label = { Text("账号") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        OutlinedTextField(password, onValueChange = { password = it }, label = { Text("密码") }, visualTransformation = PasswordVisualTransformation(), singleLine = true, modifier = Modifier.fillMaxWidth())
        Row { Checkbox(checked = replace, onCheckedChange = { replace = it }); Text("替换此账号的其他连接") }
        Row { Checkbox(checked = internet, onCheckedChange = { internet = it }); Text("通过旁路由访问互联网（需要账号授权）") }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = { connect(site, alias, password, internet, replace); password = "" }, enabled = site.isNotEmpty() && alias.isNotEmpty() && password.isNotEmpty()) { Text("连接网络") }
            OutlinedButton(onClick = { target = ""; NetGoRuntime.disconnect() }) { Text("断开") }
        }
        Text(status.optString("state", "disconnected") + "  " + status.optString("error")); Text(message); HorizontalDivider()
        OutlinedButton(onClick = { NetGoRuntime.selectedSite = site; NetGoRuntime.refreshHosts() }, enabled = status.optString("state") == "connected") { Text("更新远程主机") }
        hosts.forEach { host -> OutlinedButton(enabled = host.ready, onClick = { NativeRemote.stopCurrent(); target = host.id }) { Text(host.label) } }
        LaunchedEffect(site, status.optString("state")) { if (status.optString("state") != "connected") target = "" }
        status.optJSONObject("lease")?.optJSONArray("resources")?.let { values ->
            if (values.length() > 0) Text("已授权资源")
            for (index in 0 until values.length()) { val resource = values.getJSONObject(index); Text(resource.optString("name") + " · " + resource.optString("cidr")) }
        }
        if (target.isNotEmpty()) RemoteScreen(site, target) { target = "" }
    } }
}

@Composable private fun RemoteScreen(site: String, host: String, close: () -> Unit) {
    var remote by remember(site, host) { mutableStateOf<NativeRemote?>(null) }
    var input by remember { mutableStateOf(TextFieldValue()) }; var committed by remember { mutableStateOf("") }
    DisposableEffect(site, host) { onDispose { remote?.stop() } }
    AndroidView(modifier = Modifier.fillMaxWidth().aspectRatio(16f / 9f), factory = { context ->
        SurfaceView(context).apply {
            holder.addCallback(object : SurfaceHolder.Callback {
                override fun surfaceCreated(holder: SurfaceHolder) { NetGoRuntime.client?.let { remote = NativeRemote(site, host, holder.surface, it).apply { start() } } }
                override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {}
                override fun surfaceDestroyed(holder: SurfaceHolder) { remote?.stop() }
            })
            setOnTouchListener { view, event ->
                val x = (event.x / view.width * 65535).toInt().coerceIn(0, 65535); val y = (event.y / view.height * 65535).toInt().coerceIn(0, 65535)
                if (event.pointerCount > 1 || event.actionMasked == MotionEvent.ACTION_CANCEL) remote?.send(JSONObject().put("type", "release"))
                else { remote?.send(JSONObject().put("type", "move").put("x", x).put("y", y)); if (event.actionMasked == MotionEvent.ACTION_DOWN || event.actionMasked == MotionEvent.ACTION_UP) remote?.send(JSONObject().put("type", "button").put("button", 1).put("down", event.actionMasked == MotionEvent.ACTION_DOWN)) }; true
            }
        }
    })
    OutlinedTextField(input, onValueChange = { value ->
        if (value.text.length <= 4096) { input = value
            if (value.composition == null) {
                var common = 0; while (common < committed.length && common < value.text.length && committed[common] == value.text[common]) common++
                repeat(committed.codePointCount(common, committed.length)) { remote?.send(JSONObject().put("type", "key").put("code", 8).put("down", true)); remote?.send(JSONObject().put("type", "key").put("code", 8).put("down", false)) }
                val added = value.text.substring(common); if (added.isNotEmpty()) remote?.send(JSONObject().put("type", "text").put("text", added)); committed = value.text
            }
        }
    }, label = { Text("远程键盘输入") }, modifier = Modifier.fillMaxWidth())
    OutlinedButton(onClick = { remote?.stop(); close() }) { Text("结束远程桌面") }
}

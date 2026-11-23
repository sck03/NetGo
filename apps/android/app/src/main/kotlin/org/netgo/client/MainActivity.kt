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

class MainActivity : ComponentActivity() {
    private var pending: Triple<String, String, String>? = null
    private val permission = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        val request = pending; pending = null
        if (it.resultCode == Activity.RESULT_OK && request != null) startConnection(request)
        else NetGoRuntime.message.value = "VPN 授权已取消"
    }
    private fun startConnection(value: Triple<String, String, String>) {
        ContextCompat.startForegroundService(this, Intent(this, NetGoVpnService::class.java).setAction("connect")
            .putExtra("site", value.first).putExtra("alias", value.second).putExtra("password", value.third))
    }
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState); startService(Intent(this, NetGoVpnService::class.java))
        setContent { NetGoTheme { Screen { site, alias, password ->
            val request = Triple(site, alias, password); val approval = VpnService.prepare(this)
            if (approval == null) startConnection(request) else { pending = request; permission.launch(approval) }
        } } }
    }
    override fun onStop() { NativeRemote.stopCurrent(); super.onStop() }
}

@Composable private fun Screen(connect: (String, String, String) -> Unit) {
    val profiles by NetGoRuntime.profiles.collectAsState(); val hosts by NetGoRuntime.hosts.collectAsState()
    val status by NetGoRuntime.status.collectAsState(); val message by NetGoRuntime.message.collectAsState()
    var site by remember { mutableStateOf(NetGoRuntime.selectedSite) }; var alias by remember { mutableStateOf("") }; var password by remember { mutableStateOf("") }
    var select by remember { mutableStateOf(false) }; var target by remember { mutableStateOf("") }
    val context = androidx.compose.ui.platform.LocalContext.current
    LaunchedEffect(profiles) { if (site.isEmpty() && profiles.isNotEmpty()) { site = profiles.first().first; NetGoRuntime.selectedSite = site } }
    val importer = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) NetGoRuntime.scope.launch {
            try {
                val text = context.contentResolver.openInputStream(uri)!!.use { stream ->
                    val output = java.io.ByteArrayOutputStream(); val buffer = ByteArray(4096)
                    while (true) { val count = stream.read(buffer); if (count < 0) break; require(output.size() + count <= 65536); output.write(buffer, 0, count) }
                    output.toByteArray().toString(Charsets.UTF_8)
                }; NetGoRuntime.importProfile(text)
            } catch (_: Exception) { NetGoRuntime.message.value = "无法读取有效站点配置" }
        }
    }
    Surface(Modifier.fillMaxSize()) { Column(Modifier.safeDrawingPadding().padding(16.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("NetGo", style = MaterialTheme.typography.headlineMedium)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Box { OutlinedButton(onClick = { select = true }) { Text(profiles.firstOrNull { it.first == site }?.second ?: "选择站点") }
                DropdownMenu(select, onDismissRequest = { select = false }) { profiles.forEach { item -> DropdownMenuItem(text = { Text(item.second) }, onClick = { site = item.first; NetGoRuntime.selectedSite = site; select = false }) } }
            }; OutlinedButton(onClick = { importer.launch(arrayOf("application/json", "text/plain")) }) { Text("导入站点") }
        }
        OutlinedTextField(alias, onValueChange = { alias = it }, label = { Text("账号") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        OutlinedTextField(password, onValueChange = { password = it }, label = { Text("密码") }, visualTransformation = PasswordVisualTransformation(), singleLine = true, modifier = Modifier.fillMaxWidth())
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = { connect(site, alias, password); password = "" }, enabled = site.isNotEmpty() && alias.isNotEmpty() && password.isNotEmpty()) { Text("连接网络") }
            OutlinedButton(onClick = { target = ""; NetGoRuntime.disconnect() }) { Text("断开") }
        }
        Text(status.optString("state", "disconnected") + "  " + status.optString("error")); Text(message); HorizontalDivider()
        OutlinedButton(onClick = { NetGoRuntime.selectedSite = site; NetGoRuntime.refreshHosts() }, enabled = status.optString("state") == "connected") { Text("更新远程主机") }
        hosts.forEach { host -> OutlinedButton(onClick = { NativeRemote.stopCurrent(); target = host.first }) { Text(host.second) } }
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

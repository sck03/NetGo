package org.netgo.client

import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.Alignment
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import kotlinx.coroutines.launch
import org.json.JSONObject

@Composable internal fun RemoteScreen(site: String, host: String, viewOnly: Boolean, audio: Boolean, close: () -> Unit) {
    var remote by remember(site, host) { mutableStateOf<NativeRemote?>(null) }
    var videoRatio by remember(site, host) { mutableFloatStateOf(16f / 9f) }
    val displayHeight = LocalConfiguration.current.screenHeightDp.dp * 0.6f
    DisposableEffect(remote) {
        val session = remote
        videoRatio = session?.videoAspectRatio ?: (16f / 9f)
        session?.videoSizeChanged = { videoRatio = it }
        onDispose { session?.videoSizeChanged = null }
    }
    var input by remember(site, host) { mutableStateOf(TextFieldValue()) }
    val textLimit = remember { NativeRemote.textLimitNative() }
    val context=androidx.compose.ui.platform.LocalContext.current
    val scope=rememberCoroutineScope()
    var pendingTransfer by remember { mutableStateOf<JSONObject?>(null) }
    var uploadPath by remember { mutableStateOf<java.io.File?>(null) }
    var uploadOwner by remember { mutableStateOf<NativeRemote?>(null) }
    var importing by remember { mutableStateOf(false) }
    var downloaded by remember { mutableStateOf<java.io.File?>(null) }
    var downloadPending by remember { mutableStateOf(false) }
    var exporting by remember { mutableStateOf(false) }
    val downloads = remember(site, host) { RemoteDownloads(context.cacheDir) }
    var files by remember { mutableStateOf<List<String>>(emptyList()) }
    var showFiles by remember { mutableStateOf(false) }
    var capabilities by remember { mutableStateOf(0) }
    val activity = context as MainActivity
    val export=rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/octet-stream")) { uri ->
        activity.documentPickerActive = false
        val file=downloaded
        if(uri!=null && file!=null) scope.launch { exporting=true; downloads.exporting(); try { kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) { context.contentResolver.openOutputStream(uri,"w")!!.use { out -> file.inputStream().use { it.copyTo(out,32768) } };file.delete() };downloaded=null;NetGoRuntime.message.value="文件已导出" } catch(_:Exception) { NetGoRuntime.message.value="文件导出失败，可重试" } finally { downloads.exported(); exporting=false } }
    }
    val upload=rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        activity.documentPickerActive = false
        if (uri != null && !importing && uploadPath == null) scope.launch {
            importing = true
            try {
                stageRemoteUpload(context, uri) { file, name ->
                    uploadPath = file
                    pendingTransfer = JSONObject().put("action", "upload").put("path", file.absolutePath).put("name", name)
                }
            } catch (cancelled: kotlinx.coroutines.CancellationException) {
                throw cancelled
            } catch (_: Exception) {
                NetGoRuntime.message.value = "无法读取文件，单文件上限 1 GiB"
            } finally { importing = false }
        }
    }
    LaunchedEffect(remote,pendingTransfer) {
        val value=pendingTransfer;val session=remote
        if(value!=null && session!=null) {
            repeat(200) {
                if(session.capabilities!=0) {
                    if(session.command(value)) {
                        if(value.optString("action")=="upload") uploadOwner=session
                    } else { uploadPath?.delete();uploadPath=null }
                    pendingTransfer=null
                    return@LaunchedEffect
                }
                kotlinx.coroutines.delay(50)
            }
            pendingTransfer=null;uploadPath?.delete();uploadPath=null
            NetGoRuntime.message.value="传输未开始，连接尚未就绪，请重试"
        }
    }
    LaunchedEffect(remote) { val session=remote;capabilities=session?.capabilities ?: 0;session?.capabilitiesChanged={ capabilities=it };session?.finished={ downloads.finish(); downloadPending=false; capabilities=0; if(uploadOwner===session){uploadPath?.delete();uploadPath=null;uploadOwner=null} };session?.event={ value ->
        if(value.has("error"))NetGoRuntime.message.value="操作失败："+value.optString("error")
        else if(value.optString("action")=="clipboard.get") { val manager=context.getSystemService(android.content.ClipboardManager::class.java);manager.setPrimaryClip(android.content.ClipData.newPlainText("NetGo",value.optString("text")));NetGoRuntime.message.value="已复制远端剪贴板" }
        else if(value.has("files")){val list=value.getJSONArray("files");files=(0 until list.length()).map { list.getJSONObject(it).getString("name") }}
        else if(value.has("bytes"))NetGoRuntime.message.value="传输："+value.optLong("bytes")+" / "+value.optLong("size")+" 字节"
        if(value.optBoolean("done")){if(value.optString("action")=="upload" && uploadOwner===session){uploadPath?.delete();uploadPath=null;uploadOwner=null};if(value.optString("action")=="download"){downloadPending=false;if(!value.has("error"))downloaded=java.io.File(value.getString("path"))}}
    } }

    DisposableEffect(site, host) { onDispose { pendingTransfer=null;downloads.dispose();remote?.stop();if(uploadOwner==null){uploadPath?.delete();uploadPath=null} } }
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        AndroidView(modifier = Modifier.width(minOf(maxWidth, displayHeight * videoRatio)).aspectRatio(videoRatio).align(Alignment.Center), onRelease = { it.release() }, factory = { viewContext ->
            RemoteDisplay(viewContext, { activity.documentPickerActive }, { surface ->
                NetGoRuntime.client?.let { remote = NativeRemote(site, host, surface, it, 1 or 8 or 16 or 32 or (if (viewOnly) 0 else 2) or (if (audio) 4 else 0)).apply { downloads.started(); finished={ downloads.finish() }; start() } }
            }, { remote })
        })
    }
    OutlinedTextField(input, enabled = capabilities and 2 != 0, onValueChange = { value ->
        input = value
    }, isError = input.text.toByteArray(Charsets.UTF_8).size > textLimit,
       label = { Text("发送文字（每次最多 $textLimit UTF-8 字节）") }, modifier = Modifier.fillMaxWidth())
    OutlinedButton(enabled = capabilities and 2 != 0 && input.composition == null && input.text.isNotEmpty() && input.text.toByteArray(Charsets.UTF_8).size <= textLimit, onClick = {
        if (remote?.send(JSONObject().put("type", "text").put("text", input.text)) == true) input = TextFieldValue()
        else NetGoRuntime.message.value = "文字尚未发送，连接或输入队列繁忙，请重试"
    }) { Text("发送文字") }
    Row(horizontalArrangement=Arrangement.spacedBy(8.dp)) {
        listOf("退格" to 8, "回车" to 13).forEach { (label, code) ->
            OutlinedButton(enabled=capabilities and 2 != 0,onClick={
                if (remote?.key(code) != true) NetGoRuntime.message.value="按键尚未发送，请重试"
            }) { Text(label) }
        }
    }
    Row(horizontalArrangement=Arrangement.spacedBy(8.dp)) {
        OutlinedButton(enabled=capabilities and 8 != 0,onClick={val manager=context.getSystemService(android.content.ClipboardManager::class.java);val text=manager.primaryClip?.getItemAt(0)?.coerceToText(context)?.toString() ?: "";remote?.command(JSONObject().put("action","clipboard.set").put("text",text))}) { Text("发送剪贴板") }
        OutlinedButton(enabled=capabilities and 8 != 0,onClick={remote?.command(JSONObject().put("action","clipboard.get"))}) { Text("获取剪贴板") }
    }
    Row(horizontalArrangement=Arrangement.spacedBy(8.dp)) {
        OutlinedButton(enabled=capabilities and 16 != 0 && uploadPath==null && pendingTransfer==null,onClick={activity.openDocument { upload.launch(arrayOf("*/*")) }}) { Text("上传文件") }
        Box { OutlinedButton(enabled=capabilities and 32 != 0 && downloaded==null && !downloadPending,onClick={remote?.command(JSONObject().put("action","file.list"));showFiles=true}) { Text("下载文件") };DropdownMenu(showFiles,onDismissRequest={showFiles=false}) { files.forEach { name -> DropdownMenuItem(text={Text(name)},onClick={showFiles=false;runCatching { val file=downloads.destination(name);downloadPending=remote?.command(JSONObject().put("action","download").put("name",name).put("path",file.absolutePath))==true }.onFailure { NetGoRuntime.message.value="无法准备下载文件" }}) } } }
        OutlinedButton(onClick={remote?.command(JSONObject().put("action","cancel"));if(pendingTransfer!=null){uploadPath?.delete();uploadPath=null};pendingTransfer=null}) { Text("取消传输") }
    }
    downloaded?.let { file -> OutlinedButton(enabled=!exporting,onClick={activity.openDocument { export.launch(file.name) }}) { Text("导出已下载文件") }; OutlinedButton(enabled=!exporting,onClick={file.delete();downloaded=null}) { Text("丢弃下载") } }
    OutlinedButton(onClick = { remote?.stop(); close() }) { Text("结束远程桌面") }
}

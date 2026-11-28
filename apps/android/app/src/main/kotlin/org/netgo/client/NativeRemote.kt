package org.netgo.client

import android.view.Surface
import androidx.annotation.Keep
import org.json.JSONObject
import org.json.JSONArray
import org.netgo.runtime.mobile.Client
import org.netgo.runtime.mobile.RemoteActivity
import java.util.concurrent.atomic.AtomicReference

@Keep
class NativeRemote(private val site: String, private val host: String, private val surface: Surface, private val controlClient: Client, private val requestedCapabilities: Int) : RemoteActivity {
    @Volatile private var active = true
	val isActive: Boolean get() = active
	@Keep override fun controlActive(): Boolean = active
	@Volatile var presenting = true
    var capabilitiesChanged: ((Int) -> Unit)? = null
    var videoSizeChanged: ((Float) -> Unit)? = null
    @Volatile var videoAspectRatio = 16f / 9f; private set
    @Volatile var capabilities = 0; private set
    private var decoder: RemoteVideo? = null
    private var sound: RemoteAudio? = null
    @Volatile private var audioUnavailable = false
    private val main = android.os.Handler(android.os.Looper.getMainLooper())
    @Volatile private var handle = 0L
    var event: ((JSONObject)->Unit)? = null
    var finished: (() -> Unit)? = null
    private external fun commandNative(handle:Long,json:ByteArray):Boolean
    fun command(value:JSONObject):Boolean {
        val accepted = active && capabilities != 0 && handle != 0L && commandNative(handle,value.toString().toByteArray(Charsets.UTF_8))
        if (!accepted) NetGoRuntime.message.value = "操作尚未提交，连接不可用或任务队列已满"
        return accepted
    }
    @Keep fun onEvent(bytes:ByteArray) { if(active) runCatching { val value=JSONObject(bytes.toString(Charsets.UTF_8));android.os.Handler(android.os.Looper.getMainLooper()).post { if(active) event?.invoke(value) } } }
    private external fun startNative(site: String, host: String, capabilities: Int): Long
    private external fun cancelNative(handle: Long)
    private external fun inputNative(handle: Long, json: ByteArray): Boolean
    fun start() {
        handle = startNative(site, host, requestedCapabilities)
        if (handle == 0L) { onStatus("remote_client_failed"); onClosed() }
        else {
            current.set(this)
            if (!active) { cancelNative(handle); current.compareAndSet(this, null) }
        }
    }
    private fun sendInput(value: String): Boolean = active && capabilities and 2 != 0 && handle != 0L && inputNative(handle, value.toByteArray(Charsets.UTF_8))
    fun send(value: JSONObject): Boolean = sendInput(value.toString())
    fun key(code: Int): Boolean = sendInput(JSONArray().put(JSONObject().put("type","key").put("code",code).put("down",true)).put(JSONObject().put("type","key").put("code",code).put("down",false)).toString())
    fun stop() { active = false; if (handle != 0L) cancelNative(handle); current.compareAndSet(this, null) }
    // The native core owns retry policy. Preserve terminal authorization errors
    // instead of letting JNI clear them into an indistinguishable transport error.
    @Keep fun control(method: String, path: String, body: String, key: String): ByteArray = try {
        (if (method == "CONTEXT") controlClient.remoteContext(site) else controlClient.remoteCall(site, method, path, body, key, this)).toByteArray(Charsets.UTF_8)
    } catch (error: Exception) {
        JSONObject().put("error", error.message ?: "remote_invalid_control_response").toString().toByteArray(Charsets.UTF_8)
    }
    @Keep fun sign(message: ByteArray): ByteArray = controlClient.signPossession(site, message)
    @Keep fun onFormat(width: Int, height: Int, fps: Int, caps: Int): Boolean = try {
        if (!active || width !in 2..4096 || height !in 2..2160) false else {
            releaseMedia()
            audioUnavailable = false
            decoder = RemoteVideo(surface, width, height, fps, { active && presenting }, { onStatus("remote_decoder_failed"); stop() })
            if (caps and 4 != 0) sound = RemoteAudio { audioUnavailable = true; onStatus("音频设备不可用，继续显示画面") }
            videoAspectRatio = width.toFloat() / height
            capabilities = caps
            main.post { if (active) { videoSizeChanged?.invoke(videoAspectRatio); capabilitiesChanged?.invoke(caps) } }
            true
        }
    } catch (_: Exception) { releaseMedia(); false }
    @Keep fun onVideo(bytes: ByteArray, stamp: Long): Boolean = try {
        active && decoder?.write(bytes, stamp) == true
    } catch (_: Exception) { false }
    @Keep fun onAudio(bytes: ByteArray): Boolean = active && sound?.write(bytes) == true
    @Keep fun onStatus(code: String) { if (active) NetGoRuntime.message.value = when (code) { "remote_connected" -> if (audioUnavailable) "远程桌面已连接（声音不可用）" else "远程桌面已连接"; "remote_waiting_consent" -> "等待主机确认…"; "remote_host_busy" -> "对方正在使用远程桌面，请稍后刷新"; "remote_host_unavailable" -> "对方暂不可连接，请检查 VPN 和桌面共享"; "remote_view_forbidden" -> "当前账号没有观看权限"; else -> code } }
    private fun releaseMedia() {
        val codec = decoder; decoder = null
        val audio = sound; sound = null
        runCatching { codec?.close() }
        runCatching { audio?.close() }
    }
    @Keep fun onClosed() {
        active = false; capabilities = 0
        releaseMedia()
        surface.release()
        main.post {
            capabilitiesChanged?.invoke(0); finished?.invoke()
        }
        current.compareAndSet(this, null)
    }
    companion object {
        init { System.loadLibrary("netgo-remote-jni") }
        external fun textLimitNative(): Int
        private val current = AtomicReference<NativeRemote?>(null)
        fun stopCurrent() { current.getAndSet(null)?.stop() }
        fun releaseCurrentInput() { current.get()?.send(JSONObject().put("type", "release")) }
    }
}

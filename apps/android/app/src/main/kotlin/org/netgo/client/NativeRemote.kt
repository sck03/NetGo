package org.netgo.client

import android.media.*
import android.view.Surface
import androidx.annotation.Keep
import org.json.JSONObject
import org.json.JSONArray
import org.netgo.runtime.mobile.Client

@Keep
class NativeRemote(private val site: String, private val host: String, private val surface: Surface, private val controlClient: Client, private val requestedCapabilities: Int) {
    @Volatile private var active = true
	val isActive: Boolean get() = active
	@Volatile var presenting = true
	var capabilitiesChanged: ((Int) -> Unit)? = null
    @Volatile var capabilities = 0; private set
    private var decoder: MediaCodec? = null
    private var sound: AudioTrack? = null
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
    private var pendingAudio = ByteArray(0)
    private external fun startNative(site: String, host: String, capabilities: Int): Long
    private external fun cancelNative(handle: Long)
    private external fun inputNative(handle: Long, json: ByteArray): Boolean
    fun start() { handle = startNative(site, host, requestedCapabilities); current = this }
    private fun sendInput(value: String): Boolean = active && capabilities and 2 != 0 && handle != 0L && inputNative(handle, value.toByteArray(Charsets.UTF_8))
    fun send(value: JSONObject): Boolean = sendInput(value.toString())
    fun key(code: Int): Boolean = sendInput(JSONArray().put(JSONObject().put("type","key").put("code",code).put("down",true)).put(JSONObject().put("type","key").put("code",code).put("down",false)).toString())
    fun stop() { active = false; if (handle != 0L) cancelNative(handle); if (current === this) current = null }
    @Keep fun control(method: String, path: String, body: String, key: String): ByteArray =
        (if (method == "CONTEXT") controlClient.remoteContext(site) else controlClient.remoteCall(site, method, path, body, key)).toByteArray(Charsets.UTF_8)
    @Keep fun sign(message: ByteArray): ByteArray = controlClient.signPossession(site, message)
    @Keep fun onFormat(width: Int, height: Int, fps: Int, caps: Int): Boolean = try {
        if (!active || width !in 2..4096 || height !in 2..2160) false else {
            releaseMedia()
            val format = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, width, height).apply {
                setInteger(MediaFormat.KEY_MAX_INPUT_SIZE, 4 * 1024 * 1024)
                setInteger(MediaFormat.KEY_FRAME_RATE, fps)
                if (android.os.Build.VERSION.SDK_INT >= 30) setInteger(MediaFormat.KEY_LOW_LATENCY, 1)
            }
            decoder = MediaCodec.createDecoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
            decoder!!.configure(format, surface, null, 0)
            decoder!!.start()
            if (caps and 4 != 0) sound = AudioTrack.Builder()
                .setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_MEDIA).setContentType(AudioAttributes.CONTENT_TYPE_MOVIE).build())
                .setAudioFormat(AudioFormat.Builder().setSampleRate(48000).setChannelMask(AudioFormat.CHANNEL_OUT_STEREO).setEncoding(AudioFormat.ENCODING_PCM_16BIT).build())
                .setBufferSizeInBytes(19200).setTransferMode(AudioTrack.MODE_STREAM).build()
            sound?.play()
            capabilities = caps
            android.os.Handler(android.os.Looper.getMainLooper()).post { if (active) capabilitiesChanged?.invoke(caps) }
            true
        }
    } catch (_: Exception) { releaseMedia(); false }
    @Keep fun onVideo(bytes: ByteArray, stamp: Long): Boolean = try {
        if (!active) false else {
            val codec = decoder ?: error("decoder_unavailable")
            val drained = MediaCodec.BufferInfo()
            while (true) { val output=codec.dequeueOutputBuffer(drained,0); if(output<0) break; codec.releaseOutputBuffer(output,presenting) }
            var index = codec.dequeueInputBuffer(10000)
            var attempts=0
            while(index<0 && active && attempts++<2) {
                while(true) {val output=codec.dequeueOutputBuffer(drained,0);if(output<0)break;codec.releaseOutputBuffer(output,presenting)}
                index=codec.dequeueInputBuffer(10000)
            }
            // Never silently discard a reference frame; bound waiting and stop on overload.
            if (index < 0 || !active) error("decoder_backpressure")
            if (index >= 0) { codec.getInputBuffer(index)!!.apply { clear(); put(bytes) }; codec.queueInputBuffer(index, 0, bytes.size, stamp / 10, 0) }
            val info = MediaCodec.BufferInfo()
            while (true) { val output = codec.dequeueOutputBuffer(info, 0); if (output < 0) break; codec.releaseOutputBuffer(output, presenting) }
            true
        }
    } catch (_: Exception) { false }
    @Keep fun onAudio(bytes: ByteArray): Boolean = try {
        if (!active || bytes.size !in 4..3840 || bytes.size % 4 != 0) false else {
            val track=sound ?: error("audio_unavailable")
            if(pendingAudio.size + bytes.size > 19200) error("audio_backpressure")
            val packet=pendingAudio + bytes
            val written=track.write(packet,0,packet.size,AudioTrack.WRITE_NON_BLOCKING)
            if(written<0) error("audio_write_failed")
            pendingAudio=packet.copyOfRange(written,packet.size);true
        }
    } catch (_: Exception) { false }
    @Keep fun onStatus(code: String) { if (active) NetGoRuntime.message.value = when (code) { "remote_connected" -> "远程桌面已连接"; "remote_waiting_consent" -> "等待主机确认…"; "remote_host_busy" -> "对方正在使用远程桌面，请稍后刷新"; "remote_host_unavailable" -> "对方暂不可连接，请检查 VPN 和桌面共享"; "remote_view_forbidden" -> "当前账号没有观看权限"; else -> code } }
    private fun releaseMedia() {
        val codec = decoder; decoder = null
        val audio = sound; sound = null
        runCatching { codec?.stop() }
        runCatching { codec?.release() }
        runCatching { audio?.stop() }
        runCatching { audio?.release() }
        pendingAudio = ByteArray(0)
    }
    @Keep fun onClosed() {
        active = false; capabilities = 0
        releaseMedia()
        surface.release()
        android.os.Handler(android.os.Looper.getMainLooper()).post {
            capabilitiesChanged?.invoke(0); finished?.invoke()
        }
        if (current === this) current = null
    }
    companion object {
        init { System.loadLibrary("netgo-remote-jni") }
        external fun textLimitNative(): Int
        @Volatile private var current: NativeRemote? = null
        fun stopCurrent() { current?.stop() }
        fun releaseCurrentInput() { current?.send(JSONObject().put("type", "release")) }
    }
}

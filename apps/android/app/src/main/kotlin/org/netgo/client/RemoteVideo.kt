package org.netgo.client

import android.media.MediaCodec
import android.media.MediaFormat
import android.os.Handler
import android.os.HandlerThread
import android.os.SystemClock
import android.view.Surface
import java.util.ArrayDeque
import java.util.concurrent.TimeUnit
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

// Codec callbacks drain decoded frames independently of incoming packets. A
// static desktop's last frame is therefore presented without another packet.
internal class RemoteVideo(
    surface: Surface, width: Int, height: Int, fps: Int,
    private val presenting: () -> Boolean, private val failed: () -> Unit
) : AutoCloseable {
    private data class Packet(val bytes: ByteArray, val stamp: Long)
    private val lock = ReentrantLock()
    private val space = lock.newCondition()
    private val packets = ArrayDeque<Packet>()
    private val inputs = ArrayDeque<Int>()
    private val thread = HandlerThread("NetGoVideo").apply { start() }
    private val handler = Handler(thread.looper)
    @Volatile private var closed = false
    @Volatile private var broken = false
    private var codec: MediaCodec? = null

    init {
        try {
            val decoder = MediaCodec.createDecoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
            codec = decoder
            decoder.setCallback(object : MediaCodec.Callback() {
                override fun onInputBufferAvailable(codec: MediaCodec, index: Int) {
                    if (closed || broken) return
                    lock.withLock { inputs.addLast(index) }
                    pump()
                }
                override fun onOutputBufferAvailable(codec: MediaCodec, index: Int, info: MediaCodec.BufferInfo) {
                    if (closed || broken) return
                    try { codec.releaseOutputBuffer(index, presenting()) } catch (_: Exception) { fail() }
                }
                override fun onOutputFormatChanged(codec: MediaCodec, format: MediaFormat) {}
                override fun onError(codec: MediaCodec, error: MediaCodec.CodecException) { fail() }
            }, handler)
            val format = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, width, height).apply {
                setInteger(MediaFormat.KEY_MAX_INPUT_SIZE, 4 * 1024 * 1024)
                setInteger(MediaFormat.KEY_FRAME_RATE, fps)
                if (android.os.Build.VERSION.SDK_INT >= 30) setInteger(MediaFormat.KEY_LOW_LATENCY, 1)
            }
            decoder.configure(format, surface, null, 0)
            decoder.start()
        } catch (error: Exception) { close(); throw error }
    }
    fun write(bytes: ByteArray, stamp: Long): Boolean {
        if (bytes.isEmpty() || bytes.size > 4 * 1024 * 1024) return false
        val deadline = SystemClock.elapsedRealtime() + 40
        lock.withLock {
            while (!closed && !broken && packets.size >= 2) {
                val remaining = deadline - SystemClock.elapsedRealtime()
                if (remaining <= 0) return false
                space.await(remaining, TimeUnit.MILLISECONDS)
            }
            if (closed || broken) return false
            packets.addLast(Packet(bytes, stamp / 10))
        }
        return handler.post { pump() }
    }
    private fun pump() {
        try {
            while (!closed && !broken) {
                val next = lock.withLock {
                    if (inputs.isEmpty() || packets.isEmpty()) return
                    val pair = inputs.removeFirst() to packets.removeFirst()
                    space.signalAll()
                    pair
                }
                val decoder = codec ?: return
                val buffer = decoder.getInputBuffer(next.first) ?: error("decoder_unavailable")
                buffer.clear(); buffer.put(next.second.bytes)
                decoder.queueInputBuffer(next.first, 0, next.second.bytes.size, next.second.stamp, 0)
            }
        } catch (_: Exception) { fail() }
    }
    private fun fail() {
        lock.withLock {
            if (closed || broken) return
            broken = true; packets.clear(); space.signalAll()
        }
        failed()
    }
    override fun close() {
        lock.withLock {
            if (closed) return
            closed = true; packets.clear(); inputs.clear(); space.signalAll()
        }
        handler.post {
            val decoder = codec; codec = null
            runCatching { decoder?.stop() }
            runCatching { decoder?.release() }
            thread.quitSafely()
        }
        if (Thread.currentThread() !== thread) thread.join(2000)
    }
}

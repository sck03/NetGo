package org.netgo.client

import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioTrack

// Optional playback is independent of video and authorization. One bounded PCM
// buffer absorbs partial writes without allocating a new array for every packet.
internal class RemoteAudio(private val unavailable: () -> Unit) : AutoCloseable {
    private var track: AudioTrack? = null
    private val pending = ByteArray(19200)
    private var size = 0

    init {
        try {
            track = AudioTrack.Builder()
                .setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_MEDIA).setContentType(AudioAttributes.CONTENT_TYPE_MOVIE).build())
                .setAudioFormat(AudioFormat.Builder().setSampleRate(48000).setChannelMask(AudioFormat.CHANNEL_OUT_STEREO).setEncoding(AudioFormat.ENCODING_PCM_16BIT).build())
                .setBufferSizeInBytes(pending.size).setTransferMode(AudioTrack.MODE_STREAM).build()
            check(track?.state == AudioTrack.STATE_INITIALIZED)
            track!!.play()
        } catch (_: Exception) { disable() }
    }
    fun write(bytes: ByteArray): Boolean {
        if (bytes.size !in 4..3840 || bytes.size % 4 != 0) return false
        val output = track ?: return true
        try {
            // PCM packets are independent: drop queued audio under transient
            // pressure instead of disconnecting or growing playback latency.
            if (size + bytes.size > pending.size) size = 0
            bytes.copyInto(pending, size)
            size += bytes.size
            val written = output.write(pending, 0, size, AudioTrack.WRITE_NON_BLOCKING)
            check(written >= 0 && written <= size && written % 4 == 0)
            pending.copyInto(pending, 0, written, size)
            size -= written
        } catch (_: Exception) { disable() }
        return true
    }
    private fun disable() { close(); unavailable() }
    override fun close() {
        val output = track; track = null; size = 0
        runCatching { output?.stop() }
        runCatching { output?.release() }
    }
}

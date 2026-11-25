package org.netgo.client

import android.content.Context
import android.graphics.SurfaceTexture
import android.view.MotionEvent
import android.view.Surface
import android.view.TextureView
import org.json.JSONObject

// The native session owns the decoder; the view owns its presentation surface.
// A document picker temporarily detaches presentation without revoking consent.
internal class RemoteDisplay(
    context: Context,
    private val picking: () -> Boolean,
    private val start: (Surface) -> Unit,
    private val session: () -> NativeRemote?
) : TextureView(context), TextureView.SurfaceTextureListener {
    private var retained: SurfaceTexture? = null
    init {
        surfaceTextureListener = this
        setOnTouchListener { view, event ->
            val remote = session()
            val x = (event.x / view.width.coerceAtLeast(1) * 65535).toInt().coerceIn(0, 65535)
            val y = (event.y / view.height.coerceAtLeast(1) * 65535).toInt().coerceIn(0, 65535)
            if (event.pointerCount > 1 || event.actionMasked == MotionEvent.ACTION_CANCEL)
                remote?.send(JSONObject().put("type", "release"))
            else {
                remote?.send(JSONObject().put("type", "move").put("x", x).put("y", y))
                if (event.actionMasked == MotionEvent.ACTION_DOWN || event.actionMasked == MotionEvent.ACTION_UP)
                    remote?.send(JSONObject().put("type", "button").put("button", 1).put("down", event.actionMasked == MotionEvent.ACTION_DOWN))
            }
            true
        }
    }
    override fun onSurfaceTextureAvailable(texture: SurfaceTexture, width: Int, height: Int) {
        val saved = retained
        if (saved != null && saved !== texture) setSurfaceTexture(saved)
        retained = null
        if (session()?.isActive == true) session()?.presenting = true
        else start(Surface(saved ?: texture))
    }
    override fun onSurfaceTextureDestroyed(texture: SurfaceTexture): Boolean {
        session()?.presenting = false
        if (picking() && session()?.isActive == true) { retained = texture; return false }
        session()?.stop()
        return true
    }
    override fun onSurfaceTextureSizeChanged(texture: SurfaceTexture, width: Int, height: Int) {}
    override fun onSurfaceTextureUpdated(texture: SurfaceTexture) {}
    fun release() { session()?.stop(); retained?.release(); retained = null }
}

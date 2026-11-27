package org.netgo.client

import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import java.io.File
import java.util.UUID
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext

// The caller receives ownership only after the cancellable IO boundary. The
// outer finally also handles a result discarded by withContext cancellation.
internal suspend fun stageRemoteUpload(context: Context, uri: Uri, accept: (File, String) -> Unit) {
    val file = File(File(context.cacheDir, "remote-upload"), UUID.randomUUID().toString() + ".bin")
    var handedOff = false
    try {
        val name = withContext(Dispatchers.IO) {
            val name = context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use {
                if (it.moveToFirst()) it.getString(0) else null
            } ?: "upload.bin"
            require(name.isNotBlank() && name.length <= 180 && !name.contains('/') && !name.contains('\\'))
            require(file.parentFile!!.isDirectory || file.parentFile!!.mkdirs())
            context.contentResolver.openInputStream(uri)!!.use { source ->
                file.outputStream().use { out ->
                    val buffer = ByteArray(32768)
                    var total = 0L
                    while (true) {
                        currentCoroutineContext().ensureActive()
                        val count = source.read(buffer)
                        if (count < 0) break
                        total += count
                        require(total <= 1024L * 1024 * 1024)
                        out.write(buffer, 0, count)
                    }
                }
            }
            name
        }
        accept(file, name)
        handedOff = true
    } finally {
        if (!handedOff) file.delete()
    }
}

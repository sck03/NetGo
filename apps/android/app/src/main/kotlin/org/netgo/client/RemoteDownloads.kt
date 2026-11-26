package org.netgo.client

import java.io.File
import java.util.UUID

// One private directory per viewer. Native IO and the document exporter retain
// ownership until they finish, even after Compose disposes the screen.
internal class RemoteDownloads(cache: File) {
    private val folder = File(cache, "remote-download/" + UUID.randomUUID())
    private var disposed = false
    private var workers = 0
    private var exports = 0

    @Synchronized fun destination(name: String): File {
        require(name.isNotBlank() && name != "." && name != ".." && name.length <= 180 && !name.contains('/') && !name.contains('\\'))
        check(!disposed)
        val directory = File(folder, UUID.randomUUID().toString()).apply { check(mkdirs()) }
        return File(directory, name)
    }
    @Synchronized fun exporting() { exports++ }
    @Synchronized fun exported() { exports--; clean() }
    @Synchronized fun started() { workers++ }
    @Synchronized fun finish() { workers--; clean() }
    @Synchronized fun dispose() { disposed = true; clean() }
    private fun clean() { if (disposed && workers == 0 && exports == 0) folder.deleteRecursively() }
}

import Foundation

// Completed downloads are user files. Each transfer owns a unique directory;
// failed submissions remove it without touching earlier downloads of that name.
final class RemoteDownloads {
    func destination(_ name: String) throws -> URL {
        guard !name.isEmpty, !name.hasPrefix("."), !name.contains("/"),
              !name.contains("\\"), !name.contains(":"), !name.contains("\0"), name.utf8.count <= 180 else {
            throw NSError(domain:"NetGo",code:1,userInfo:[NSLocalizedDescriptionKey:"无效的远端文件名"])
        }
        let root = FileManager.default.urls(for:.documentDirectory,in:.userDomainMask)[0]
            .appendingPathComponent("NetGo Downloads",isDirectory:true)
            .appendingPathComponent(UUID().uuidString,isDirectory:true)
        try FileManager.default.createDirectory(at:root,withIntermediateDirectories:true)
        return root.appendingPathComponent(name)
    }
    func discard(_ url: URL) { try? FileManager.default.removeItem(at:url.deletingLastPathComponent()) }
}

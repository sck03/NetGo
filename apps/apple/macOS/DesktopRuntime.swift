import Foundation
import AppKit

final class DesktopRuntime {
    private var service: DesktopService?
    private var socket = ""
    private let connections = DesktopConnectionCoordinator()
    @MainActor func attach(_ model: ClientModel) {
        guard service == nil else { return }
        if CommandLine.arguments.contains("--smoke") { DispatchQueue.main.asyncAfter(deadline:.now()+2) { NSApplication.shared.terminate(nil) }; return }
        do {
            let root = FileManager.default.urls(for:.applicationSupportDirectory,in:.userDomainMask)[0].appendingPathComponent("NetGo",isDirectory:true)
            try FileManager.default.createDirectory(at:root,withIntermediateDirectories:true,attributes:[.posixPermissions:0o700])
            socket = root.appendingPathComponent("client.sock").path
            if socket.utf8.count >= 104 { throw NSError(domain:"NetGo",code:1,userInfo:[NSLocalizedDescriptionKey:"用户数据目录过长"]) }
            let helper = Bundle.main.resourceURL!.appendingPathComponent("bin/netgo-client-service")
            let service = DesktopService(helper: helper, root: root.path, socket: socket) { [weak model] message in
                Task { @MainActor in model?.message = message }
            }
            self.service = service
            service.start()
            let path = socket
            model.request = { method, route, body in
                try desktopIPC(path, method, route, body)
            }
            model.remoteRequest = { method, route, body, active in
                try desktopIPC(path, method, route, body, active: active, timeout: 4000)
            }
            let request = model.request
            model.importAction = { _, raw, approved in
                guard let request = request else { throw NSError(domain:"NetGo",code:1) }
                _ = try request("POST",approved ? "/desktop/import-trusted" : "/desktop/import",raw)
            }
            let completed: @MainActor (Error?) -> Void = { [weak model] error in
                if let error { model?.message = error.localizedDescription }
                else { model?.refresh() }
            }
            model.disconnectAction = { [weak self] in
                self?.connections.submit(.disconnect, operation: { _ = try request?("POST","/desktop/disconnect","{}") }, completion: completed)
            }
            model.restoreAction = { [weak self] in
                self?.connections.submit(.restore, operation: { _ = try request?("POST","/desktop/unattended/restore","") }, completion: completed)
            }
            model.disableUnattendedAction = { [weak self] in
                self?.connections.submit(.disableUnattended, operation: { _ = try request?("DELETE","/desktop/unattended","") }, completion: completed)
            }
            model.connectAction = { [weak self,weak model] site, alias, password, internet, replace in
                let remember=model?.unattended ?? false
                self?.connections.submit(.connect, prepare: DesktopRuntime.prepareNetwork, operation: {
                        guard let request else { return }
                        let body = String(decoding:try JSONSerialization.data(withJSONObject:["Site":site.id,"Alias":alias,"Password":password,"Replace":replace,"Internet":internet,"Remember":remember]),as:UTF8.self)
                        _ = try request("POST","/desktop/connect",body)
                }, completion: completed)
            }
            NotificationCenter.default.addObserver(forName:NSApplication.willTerminateNotification,object:nil,queue:.main) { [weak self] _ in self?.service?.stop() }
        } catch { model.message = error.localizedDescription }
    }
    private static func prepareNetwork() throws {
        if FileManager.default.fileExists(atPath:"/var/run/netgo/netbird.sock") { return }
        guard let resource = Bundle.main.resourceURL else { throw NSError(domain:"NetGo",code:1) }
        let script = resource.appendingPathComponent("maintenance/install-network.sh").path
        let quoted = "'" + script.replacingOccurrences(of:"'",with:"'\\''") + "' '" + resource.path.replacingOccurrences(of:"'",with:"'\\''") + "' " + String(getuid())
        let command = "do shell script \"" + ("/bin/bash " + quoted).replacingOccurrences(of:"\\",with:"\\\\").replacingOccurrences(of:"\"",with:"\\\"") + "\" with administrator privileges"
        let task = Process(); task.executableURL = URL(fileURLWithPath:"/usr/bin/osascript"); task.arguments = ["-e",command]
        task.standardOutput = FileHandle.nullDevice; task.standardError = FileHandle.nullDevice
        try task.run(); task.waitUntilExit()
        guard task.terminationStatus == 0 else { throw NSError(domain:"NetGo",code:2,userInfo:[NSLocalizedDescriptionKey:"网络组件安装未完成"]) }
    }
}

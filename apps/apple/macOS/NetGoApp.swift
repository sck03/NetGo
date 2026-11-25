import SwiftUI
import AppKit
import NetGoApple

@main struct NetGoApp: App {
    @StateObject private var model = ClientModel()
    private let runtime = DesktopRuntime()
    var body: some Scene {
        WindowGroup {
            ClientView(model:model).frame(minWidth:560,minHeight:560)
                .onAppear { runtime.attach(model) }
                .onDisappear { model.stopRemote() }
        }.defaultSize(width:640,height:700)
    }
}

final class DesktopRuntime {
    private var process: Process?
    private var socket = ""
    private let operationLock = NSLock()
    private let requestLock = NSLock()
    private var generation: UInt64 = 0
    private func advance() -> UInt64 {operationLock.lock();defer{operationLock.unlock()};generation &+= 1;return generation}
    private func current(_ value: UInt64) -> Bool {operationLock.lock();defer{operationLock.unlock()};return generation==value}
    @MainActor func attach(_ model: ClientModel) {
        guard process == nil else { return }
        if CommandLine.arguments.contains("--smoke") { DispatchQueue.main.asyncAfter(deadline:.now()+2) { NSApplication.shared.terminate(nil) }; return }
        do {
            let root = FileManager.default.urls(for:.applicationSupportDirectory,in:.userDomainMask)[0].appendingPathComponent("NetGo",isDirectory:true)
            try FileManager.default.createDirectory(at:root,withIntermediateDirectories:true,attributes:[.posixPermissions:0o700])
            socket = root.appendingPathComponent("client.sock").path
            if socket.utf8.count >= 104 { throw NSError(domain:"NetGo",code:1,userInfo:[NSLocalizedDescriptionKey:"用户数据目录过长"]) }
            let helper = Bundle.main.resourceURL!.appendingPathComponent("bin/netgo-client-service")
            let child = Process(); child.executableURL = helper
            child.arguments = ["--root",root.path,"--socket",socket,"--daemon","/var/run/netgo/netbird.sock"]
            child.standardOutput = FileHandle.nullDevice; child.standardError = FileHandle.nullDevice
            try child.run(); process = child
            let path = socket
            model.request = { method, route, body in
                var output = [CChar](repeating:0,count:1_048_576), size = 0
                let result = ng_apple_ipc(path,method,route,body,&output,output.count,&size)
                guard result == 0 else { throw NSError(domain:"NetGo",code:Int(result),userInfo:[NSLocalizedDescriptionKey:"连接服务暂不可用，请查看网络安装状态"]) }
                return String(decoding:output.prefix(size).map{UInt8(bitPattern:$0)},as:UTF8.self)
            }
            let request = model.request
            model.importAction = { _, raw, approved in
                guard let request = request else { throw NSError(domain:"NetGo",code:1) }
                _ = try request("POST",approved ? "/desktop/import-trusted" : "/desktop/import",raw)
            }
            model.disconnectAction = { [weak self] in
                guard let self=self else{return};let version=self.advance()
                DispatchQueue.global().async { self.requestLock.lock();defer{self.requestLock.unlock()};if self.current(version) { _ = try? request?("POST","/desktop/disconnect","{}") } }
            }
            model.connectAction = { [weak self,weak model] site, alias, password, internet, replace in
                guard let self=self else{return};let version=self.advance()
                DispatchQueue.global().async {
                    do {
                        try DesktopRuntime.prepareNetwork()
                        self.requestLock.lock();defer{self.requestLock.unlock()}
                        guard self.current(version), let request = request else { return }
                        let body = String(decoding:try JSONSerialization.data(withJSONObject:["Site":site.id,"Alias":alias,"Password":password,"Replace":replace,"Internet":internet]),as:UTF8.self)
                        _ = try request("POST","/desktop/connect",body)
                        if !self.current(version) { _ = try? request("POST","/desktop/disconnect","{}"); return }
                        Task { @MainActor in model?.refresh() }
                    } catch { Task { @MainActor in model?.message = error.localizedDescription } }
                }
            }
            NotificationCenter.default.addObserver(forName:NSApplication.willTerminateNotification,object:nil,queue:.main) { [weak self] _ in self?.process?.terminate() }
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

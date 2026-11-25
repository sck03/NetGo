import SwiftUI
import NetworkExtension

@main struct NetGoApp: App {
    @StateObject private var model = ClientModel()
    private let runtime = TunnelRuntime()
    var body: some Scene { WindowGroup { ClientView(model:model).onAppear { runtime.attach(model) } } }
}
final class TunnelRuntime {
    private var manager: NETunnelProviderManager?
    private let fence = NSLock()
    private var generation: UInt64 = 0
    private func advance() -> UInt64 { fence.lock(); defer {fence.unlock()}; generation &+= 1; return generation }
    private func current(_ value: UInt64) -> Bool { fence.lock(); defer {fence.unlock()}; return generation == value }
    @MainActor func attach(_ model: ClientModel) {
        model.connectAction = { [weak self,weak model] site,alias,password,internet,replace in self?.connect(site,alias:alias,password:password,internet:internet,replace:replace,model:model) }
        model.disconnectAction = { [weak self] in guard let self=self else{return}; _=self.advance(); self.fence.lock(); self.manager?.connection.stopVPNTunnel(); self.fence.unlock() }
        model.request = { [weak self] method,path,body in
            guard let session = self?.manager?.connection as? NETunnelProviderSession else { throw NSError(domain:"NetGo",code:1,userInfo:[NSLocalizedDescriptionKey:"VPN 未连接"]) }
            let request = try JSONSerialization.data(withJSONObject:["method":method,"path":path,"body":body])
            let signal = DispatchSemaphore(value:0), box = ResponseBox()
            try session.sendProviderMessage(request) { data in box.data = data; signal.signal() }
            guard signal.wait(timeout:.now()+22) == .success, let data = box.data else { throw NSError(domain:"NetGo",code:2,userInfo:[NSLocalizedDescriptionKey:"VPN 服务未响应"]) }
            if let object = try JSONSerialization.jsonObject(with:data) as? [String:Any], let error = object["error"] as? String { throw NSError(domain:"NetGo",code:3,userInfo:[NSLocalizedDescriptionKey:error]) }
            return String(decoding:data,as:UTF8.self)
        }
    }
    private func connect(_ site: SiteItem,alias: String,password: String,internet: Bool,replace: Bool,model: ClientModel?) {
        let version=advance()
        NETunnelProviderManager.loadAllFromPreferences { [weak self,weak model] managers,error in
            guard let self=self,self.current(version) else{return}
            if let error = error { Task { @MainActor in model?.message = error.localizedDescription }; return }
            let manager = managers?.first(where:{($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == "org.netgo.client.ios.tunnel"}) ?? NETunnelProviderManager()
            let config = NETunnelProviderProtocol(); config.providerBundleIdentifier = "org.netgo.client.ios.tunnel"; config.serverAddress = site.name
            manager.protocolConfiguration = config; manager.localizedDescription = "NetGo"; manager.isEnabled = true
            manager.saveToPreferences { error in
                guard self.current(version) else{return}
                if let error = error { Task { @MainActor in model?.message = error.localizedDescription }; return }
                manager.loadFromPreferences { error in
                    self.fence.lock(); defer {self.fence.unlock()}; guard self.generation==version else{return}
                    if let error = error { Task { @MainActor in model?.message = error.localizedDescription }; return }
                    self.manager = manager
                    do { try manager.connection.startVPNTunnel(options:["profile":site.raw as NSString,"alias":alias as NSString,"password":password as NSString,"internet":NSNumber(value:internet),"replace":NSNumber(value:replace),"import_id":site.importID as NSString,"import":NSNumber(value:site.needsImport),"trusted":NSNumber(value:site.trusted)]) }
                    catch { Task { @MainActor in model?.message = error.localizedDescription } }
                }
            }
        }
    }
}
private final class ResponseBox { var data: Data? }

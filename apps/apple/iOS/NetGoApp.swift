import SwiftUI
import NetworkExtension

@main struct NetGoApp: App {
    @StateObject private var model = ClientModel()
    private let runtime = TunnelRuntime()
    var body: some Scene { WindowGroup { ClientView(model:model).onAppear { runtime.attach(model) } } }
}
final class TunnelRuntime {
    private var manager: NETunnelProviderManager?
    @MainActor func attach(_ model: ClientModel) {
        model.connectAction = { [weak self,weak model] site,alias,password,internet in self?.connect(site,alias:alias,password:password,internet:internet,model:model) }
        model.disconnectAction = { [weak self] in self?.manager?.connection.stopVPNTunnel() }
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
    private func connect(_ site: SiteItem,alias: String,password: String,internet: Bool,model: ClientModel?) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self,weak model] managers,error in
            if let error = error { Task { @MainActor in model?.message = error.localizedDescription }; return }
            let manager = managers?.first(where:{($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == "org.netgo.client.ios.tunnel"}) ?? NETunnelProviderManager()
            let config = NETunnelProviderProtocol(); config.providerBundleIdentifier = "org.netgo.client.ios.tunnel"; config.serverAddress = site.name
            manager.protocolConfiguration = config; manager.localizedDescription = "NetGo"; manager.isEnabled = true
            manager.saveToPreferences { error in
                if let error = error { Task { @MainActor in model?.message = error.localizedDescription }; return }
                manager.loadFromPreferences { error in
                    if let error = error { Task { @MainActor in model?.message = error.localizedDescription }; return }
                    self?.manager = manager
                    do { try manager.connection.startVPNTunnel(options:["profile":site.raw as NSString,"alias":alias as NSString,"password":password as NSString,"internet":NSNumber(value:internet)]) }
                    catch { Task { @MainActor in model?.message = error.localizedDescription } }
                }
            }
        }
    }
}
private final class ResponseBox { var data: Data? }

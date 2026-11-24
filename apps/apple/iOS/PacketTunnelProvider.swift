import NetworkExtension
import Network
import NetGoApple

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let bridge = TunnelBridge()
    private let queue = DispatchQueue(label:"org.netgo.tunnel")
    private let monitor = NWPathMonitor()
    private var descriptor: Int32 = -1
    private var interfaceName = ""
    override func startTunnel(options: [String:NSObject]?,completionHandler: @escaping (Error?) -> Void) {
        guard let options = options, let profile = options["profile"] as? String, let alias = options["alias"] as? String, let password = options["password"] as? String else { completionHandler(NSError(domain:"NetGo",code:1,userInfo:[NSLocalizedDescriptionKey:"缺少站点或登录信息"])); return }
        let initial = NEPacketTunnelNetworkSettings(tunnelRemoteAddress:"127.0.0.1")
        initial.ipv4Settings = NEIPv4Settings(addresses:["192.0.2.1"],subnetMasks:["255.255.255.255"])
        initial.ipv4Settings?.includedRoutes = []; initial.mtu = 1280
        setTunnelNetworkSettings(initial) { [weak self] error in
            guard let self = self else { return }; if let error = error { completionHandler(error); return }
            var name = [CChar](repeating:0,count:32); self.descriptor = ng_apple_tunnel_descriptor(&name,name.count)
            guard self.descriptor >= 0 else { completionHandler(NSError(domain:"NetGo",code:2,userInfo:[NSLocalizedDescriptionKey:"无法创建 VPN 接口"])); return }
            self.interfaceName = String(cString:name)
            let root = FileManager.default.urls(for:.applicationSupportDirectory,in:.userDomainMask)[0].appendingPathComponent("NetGo",isDirectory:true)
            do { try FileManager.default.createDirectory(at:root,withIntermediateDirectories:true,attributes:[.posixPermissions:0o700]); var location = root; var values = URLResourceValues(); values.isExcludedFromBackup = true; try location.setResourceValues(values) } catch { completionHandler(error); return }
            self.bridge.applyNetwork = { [weak self] ip,ip6,dns,routes in self?.apply(ip,dns:dns,routes:routes) }
            self.monitor.pathUpdateHandler = { [weak self] path in self?.bridge.networkAvailable(path.status == .satisfied) }; self.monitor.start(queue:self.queue)
            self.queue.async {
                let result = self.bridge.startRoot(root.path,profile:profile,alias:alias,password:password,tunnelFD:self.descriptor,interfaceName:self.interfaceName)
                completionHandler(result.isEmpty ? nil : NSError(domain:"NetGo",code:3,userInfo:[NSLocalizedDescriptionKey:result]))
            }
        }
    }
    private func apply(_ address: String,dns: String,routes: String) {
        let parts = address.split(separator:"/"); guard parts.count == 2, let bits = Int(parts[1]), bits > 0, bits <= 32 else { return }
        let mask = UInt32.max << (32-bits), maskText = [24,16,8,0].map { String((mask >> $0) & 255) }.joined(separator:".")
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress:"127.0.0.1")
        settings.ipv4Settings = NEIPv4Settings(addresses:[String(parts[0])],subnetMasks:[maskText]); settings.mtu = 1280
        var values = [NEIPv4Route(destinationAddress:String(parts[0]),subnetMask:maskText)]
        for value in routes.split(separator:";") {
            let fields = value.split(separator:"/"); guard fields.count == 2, let prefix = Int(fields[1]), prefix > 0, prefix <= 32 else { continue }
            let mask = UInt32.max << (32-prefix), text = [24,16,8,0].map { String((mask >> $0) & 255) }.joined(separator:".")
            values.append(NEIPv4Route(destinationAddress:String(fields[0]),subnetMask:text))
        }
        settings.ipv4Settings?.includedRoutes = values
        if let data = dns.data(using:.utf8), let object = try? JSONSerialization.jsonObject(with:data) as? [String:Any], let server = object["ServerIP"] as? String, !server.isEmpty { settings.dnsSettings = NEDNSSettings(servers:[server]); settings.dnsSettings?.matchDomains = object["SearchDomains"] as? [String] ?? [] }
        setTunnelNetworkSettings(settings) { [weak self] error in if let error = error { self?.cancelTunnelWithError(error) } }
    }
    override func handleAppMessage(_ messageData: Data,completionHandler: ((Data?) -> Void)?) { queue.async { [weak self] in completionHandler?(self?.bridge.message(messageData)) } }
    override func stopTunnel(with reason: NEProviderStopReason,completionHandler: @escaping () -> Void) { monitor.cancel(); queue.async { [self] in bridge.stop(); if descriptor >= 0 { close(descriptor); descriptor = -1 }; completionHandler() } }
}

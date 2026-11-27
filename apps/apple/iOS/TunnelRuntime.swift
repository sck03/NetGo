import Foundation
import NetworkExtension
import SwiftUI

// Owns the saved system tunnel independently of any individual SwiftUI view.
final class TunnelRuntime: ObservableObject {
    private static let bundleID = "org.netgo.client.ios.tunnel"
    private let fence = NSLock()
    private var manager: NETunnelProviderManager?
    private var generation: UInt64 = 0
    private var loading = false
    private var observation: NSObjectProtocol?

    deinit { if let observation { NotificationCenter.default.removeObserver(observation) } }

    private func advance() -> UInt64 {
        fence.lock(); defer { fence.unlock() }
        generation &+= 1
        loading = false
        return generation
    }
    private func current(_ value: UInt64) -> Bool {
        fence.lock(); defer { fence.unlock() }
        return generation == value
    }
    private func snapshot() -> NETunnelProviderManager? {
        fence.lock(); defer { fence.unlock() }
        return manager
    }
    private static func saved(_ managers: [NETunnelProviderManager]?) -> NETunnelProviderManager? {
        managers?.first { ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == bundleID }
    }
    private func install(_ value: NETunnelProviderManager, version: UInt64, model: ClientModel?) {
        fence.lock()
        guard generation == version else { fence.unlock(); return }
        manager = value; loading = false
        fence.unlock()
        Task { @MainActor [weak self, weak model] in
            guard let self, self.current(version) else { return }
            if let observation = self.observation { NotificationCenter.default.removeObserver(observation) }
            self.observation = NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange, object: value.connection, queue: .main) { [weak model] _ in
                Task { @MainActor in model?.refresh() }
            }
            model?.refresh()
        }
    }
    @MainActor func attach(_ model: ClientModel) {
        model.connectAction = { [weak self, weak model] site, alias, password, internet, replace in
            self?.connect(site, alias: alias, password: password, internet: internet, replace: replace, model: model)
        }
        model.disconnectAction = { [weak self] in self?.disconnect() }
        model.request = { [weak self] method, path, body in
            guard let session = self?.snapshot()?.connection as? NETunnelProviderSession,
                  session.status == .connected || session.status == .reasserting else {
                throw NSError(domain: "NetGo", code: 1, userInfo: [NSLocalizedDescriptionKey: "remote_site_disconnected"])
            }
            let request = try JSONSerialization.data(withJSONObject: ["method": method, "path": path, "body": body])
            let signal = DispatchSemaphore(value: 0), response = TunnelResponse()
            try session.sendProviderMessage(request) { data in response.store(data); signal.signal() }
            guard signal.wait(timeout: .now() + 22) == .success, let data = response.load() else {
                throw NSError(domain: "NetGo", code: 2, userInfo: [NSLocalizedDescriptionKey: "ipc_failed"])
            }
            if let object = try JSONSerialization.jsonObject(with: data) as? [String: Any], let error = object["error"] as? String {
                throw NSError(domain: "NetGo", code: 3, userInfo: [NSLocalizedDescriptionKey: error])
            }
            return String(decoding: data, as: UTF8.self)
        }
        fence.lock()
        guard manager == nil && !loading else { fence.unlock(); return }
        loading = true; let version = generation
        fence.unlock()
        NETunnelProviderManager.loadAllFromPreferences { [weak self, weak model] managers, error in
            guard let self, self.current(version) else { return }
            self.fence.lock(); self.loading = false; self.fence.unlock()
            if let error { Task { @MainActor in model?.message = error.localizedDescription }; return }
            if let manager = Self.saved(managers) { self.install(manager, version: version, model: model) }
        }
    }
    private func disconnect() {
        let version = advance()
        if let manager = snapshot() { manager.connection.stopVPNTunnel(); return }
        // A stop pressed before preference loading finishes must still take effect.
        NETunnelProviderManager.loadAllFromPreferences { [weak self] managers, _ in
            guard let self, self.current(version) else { return }
            Self.saved(managers)?.connection.stopVPNTunnel()
        }
    }
    private func connect(_ site: SiteItem, alias: String, password: String, internet: Bool, replace: Bool, model: ClientModel?) {
        let version = advance()
        NETunnelProviderManager.loadAllFromPreferences { [weak self, weak model] managers, error in
            guard let self, self.current(version) else { return }
            if let error { Task { @MainActor in model?.message = error.localizedDescription }; return }
            let manager = Self.saved(managers) ?? NETunnelProviderManager()
            let config = NETunnelProviderProtocol()
            config.providerBundleIdentifier = Self.bundleID; config.serverAddress = site.name
            manager.protocolConfiguration = config; manager.localizedDescription = "NetGo"; manager.isEnabled = true
            manager.saveToPreferences { error in
                guard self.current(version) else { return }
                if let error { Task { @MainActor in model?.message = error.localizedDescription }; return }
                manager.loadFromPreferences { error in
                    guard self.current(version) else { return }
                    if let error { Task { @MainActor in model?.message = error.localizedDescription }; return }
                    self.install(manager, version: version, model: model)
                    self.fence.lock(); defer { self.fence.unlock() }
                    guard self.generation == version else { return }
                    do {
                        try manager.connection.startVPNTunnel(options: ["profile": site.raw as NSString, "alias": alias as NSString, "password": password as NSString, "internet": NSNumber(value: internet), "replace": NSNumber(value: replace), "import_id": site.importID as NSString, "import": NSNumber(value: site.needsImport), "trusted": NSNumber(value: site.trusted)])
                    } catch { Task { @MainActor in model?.message = error.localizedDescription } }
                }
            }
        }
    }
}

private final class TunnelResponse {
    private let lock = NSLock()
    private var data: Data?
    func store(_ value: Data?) { lock.lock(); data = value; lock.unlock() }
    func load() -> Data? { lock.lock(); defer { lock.unlock() }; return data }
}

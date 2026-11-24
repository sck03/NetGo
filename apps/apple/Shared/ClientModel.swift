import Foundation
import SwiftUI
import NetGoApple

struct SiteItem: Identifiable {
    let id: String
    let name: String
    let raw: String
}
struct HostItem: Identifiable { let id: String; let name: String }

@MainActor final class ClientModel: ObservableObject {
    @Published var sites: [SiteItem] = []
    @Published var hosts: [HostItem] = []
    @Published var selected = ""
    @Published var alias = ""
    @Published var password = ""
    @Published var message = "未连接"
    @Published var connected = false
    let viewer = RemoteViewer()
    var request: ((String,String,String) throws -> String)?
    var connectAction: ((SiteItem,String,String) -> Void)?
    var disconnectAction: (() -> Void)?
    private var timer: Timer?

    init() {
        sites = (UserDefaults.standard.stringArray(forKey:"sites") ?? []).compactMap { raw in guard let object = try? JSONSerialization.jsonObject(with:Data(raw.utf8)) as? [String:Any], let id = object["ID"] as? String else { return nil }; return SiteItem(id:id,name:object["Name"] as? String ?? id,raw:raw) }
        selected = sites.first?.id ?? ""
        viewer.changed = { [weak self] text in Task { @MainActor in self?.message = text } }
        timer = Timer.scheduledTimer(withTimeInterval: 2,repeats: true) { [weak self] _ in Task { @MainActor in self?.refresh() } }
    }
    func importSite(_ data: Data) {
        do {
            guard data.count <= 65536, let object = try JSONSerialization.jsonObject(with:data) as? [String:Any], let id = object["ID"] as? String, let name = object["Name"] as? String, object["Protocol"] as? Int == 2 else { throw NSError(domain:"NetGo",code:1,userInfo:[NSLocalizedDescriptionKey:"请选择有效的站点配置文件"]) }
            let raw = String(decoding:data,as:UTF8.self)
            if connectAction == nil { operation("POST","/desktop/import",raw) }
            sites.removeAll { $0.id == id }; sites.append(SiteItem(id:id,name:name,raw:raw)); selected = id; message = "站点已导入"
            UserDefaults.standard.set(sites.map(\.raw),forKey:"sites")
        } catch { message = error.localizedDescription }
    }
    func connect() {
        guard let site = sites.first(where:{$0.id == selected}), !alias.isEmpty, !password.isEmpty else { message = "请选择站点并填写账号密码"; return }
        let secret = password; password = ""; message = "连接中…"
        if let connectAction = connectAction { connectAction(site,alias,secret); return }
        let body = encode(["Site":site.id,"Alias":alias,"Password":secret,"Replace":false])
        operation("POST","/desktop/connect",body)
    }
    func disconnect() { viewer.stop(); if let disconnectAction = disconnectAction { disconnectAction() } else { operation("POST","/desktop/disconnect","{}") } }
    func refresh() {
        guard let request = request else { return }
        DispatchQueue.global(qos:.utility).async { [weak self] in
            do {
                let raw = try request("GET","/desktop/status","")
                let object = try JSONSerialization.jsonObject(with:Data(raw.utf8)) as? [String:Any] ?? [:]
                let state = object["state"] as? String ?? "disconnected"
                Task { @MainActor in self?.connected = state == "connected"; self?.message = state == "connected" ? "已连接" : (object["error"] as? String).flatMap{$0.isEmpty ? nil : $0} ?? "未连接" }
            } catch { Task { @MainActor in self?.connected = false } }
        }
    }
    func loadHosts() {
        let body = encode(["Site":selected,"Method":"GET","Path":"/remote/hosts","Body":NSNull(),"Idempotency":""])
        guard let request = request else { return }
        DispatchQueue.global().async { [weak self] in
            do {
                let raw = try request("POST","/desktop/remote/call",body)
                let object = try JSONSerialization.jsonObject(with:Data(raw.utf8))
                let values = (object as? [[String:Any]]) ?? ((object as? [String:Any])?["hosts"] as? [[String:Any]]) ?? []
                let hosts = values.compactMap { item -> HostItem? in guard let id = item["id"] as? String else { return nil }; return HostItem(id:id,name:(item["name"] as? String) ?? id) }
                Task { @MainActor in self?.hosts = hosts }
            } catch { Task { @MainActor in self?.message = error.localizedDescription } }
        }
    }
    func view(_ host: HostItem) {
        guard let request = request else { return }
        viewer.control = { [site=selected] method,_,body,_ in
            switch method {
            case "LOCAL_CONTEXT": return try request("GET","/desktop/remote/context?site=" + site,"")
            case "LOCAL_SIGN": return try request("POST","/desktop/remote/sign",body)
            default: return try request("POST","/desktop/remote/call",body)
            }
        }
        viewer.start(site:selected,host:host.id)
    }
    private func operation(_ method: String,_ path: String,_ body: String) {
        guard let request = request else { return }
        DispatchQueue.global().async { [weak self] in
            do { _ = try request(method,path,body); Task { @MainActor in self?.refresh() } }
            catch { Task { @MainActor in self?.message = error.localizedDescription } }
        }
    }
    private func encode(_ object: [String:Any]) -> String { String(decoding:(try? JSONSerialization.data(withJSONObject:object)) ?? Data(),as:UTF8.self) }
}

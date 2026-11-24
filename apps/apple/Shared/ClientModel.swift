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
	@Published var internet = false
	@Published var confirmTrust = false
	private var pendingImport: Data?
    let viewer = RemoteViewer()
    var request: ((String,String,String) throws -> String)?
    var importAction: ((String,String,Bool) throws -> Void)?
    var connectAction: ((SiteItem,String,String,Bool) -> Void)?
    var disconnectAction: (() -> Void)?
    private var timer: Timer?

    init() {
        sites = (UserDefaults.standard.stringArray(forKey:"sites") ?? []).compactMap { raw in guard let object = try? JSONSerialization.jsonObject(with:Data(raw.utf8)) as? [String:Any], let id = object["ID"] as? String else { return nil }; return SiteItem(id:id,name:object["Name"] as? String ?? id,raw:raw) }
        selected = sites.first?.id ?? ""
        viewer.changed = { [weak self] text in Task { @MainActor in self?.message = text } }
        timer = Timer.scheduledTimer(withTimeInterval: 2,repeats: true) { [weak self] _ in Task { @MainActor in self?.refresh() } }
    }
    func importSite(_ data: Data, approved: Bool = false) {
        do {
            guard data.count <= 65536, let object = try JSONSerialization.jsonObject(with:data) as? [String:Any], let id = object["ID"] as? String, let name = object["Name"] as? String, object["Protocol"] as? Int == 2 else { throw NSError(domain:"NetGo",code:1,userInfo:[NSLocalizedDescriptionKey:"请选择有效的站点配置文件"]) }
            let raw = String(decoding:data,as:UTF8.self)
            if !approved, let previous = sites.first(where:{$0.id == id}), let old = try? JSONSerialization.jsonObject(with:Data(previous.raw.utf8)) as? [String:Any], (old["Origin"] as? String != object["Origin"] as? String || old["CA"] as? String != object["CA"] as? String) { pendingImport = data; confirmTrust = true; return }
            let previous = sites.first(where:{$0.id == id})?.raw ?? ""
            if let old = try? JSONSerialization.jsonObject(with:Data(previous.utf8)) as? [String:Any] {
                let oldRevision = old["ConfigRevision"] as? UInt64 ?? 0
                let revision = object["ConfigRevision"] as? UInt64 ?? 0
                guard revision >= oldRevision, !approved || revision > oldRevision else { throw NSError(domain:"NetGo",code:1,userInfo:[NSLocalizedDescriptionKey:"配置版本过旧，请重新从服务器导出"]) }
                if oldRevision > 0 && revision == oldRevision {
                    var a = old, b = object; a.removeValue(forKey:"Name"); b.removeValue(forKey:"Name")
                    guard NSDictionary(dictionary:a).isEqual(to:b) else { throw NSError(domain:"NetGo",code:1,userInfo:[NSLocalizedDescriptionKey:"同一版本的配置内容不一致"]) }
                }
            }
            let item = SiteItem(id:id,name:name,raw:raw)
            if let action = importAction {
                message = "导入中…"
                DispatchQueue.global().async { [weak self] in
                    do { try action(previous,raw,approved); Task { @MainActor in self?.saveSite(item,message:"站点已导入") } }
                    catch { Task { @MainActor in self?.message = error.localizedDescription } }
                }
            } else { saveSite(item,message:"站点已保存，连接时验证") }
        } catch { message = error.localizedDescription }
    }
    private func saveSite(_ item: SiteItem,message: String) {
        sites.removeAll { $0.id == item.id }; sites.append(item); selected = item.id; self.message = message
        UserDefaults.standard.set(sites.map(\.raw),forKey:"sites")
    }
    func approveImport() { if let data = pendingImport { pendingImport = nil; importSite(data,approved:true) } }
    func cancelImport() { pendingImport = nil }
    func connect() {
        guard let site = sites.first(where:{$0.id == selected}), !alias.isEmpty, !password.isEmpty else { message = "请选择站点并填写账号密码"; return }
        let secret = password; password = ""; message = "连接中…"
        if let connectAction = connectAction { connectAction(site,alias,secret,internet); return }
        let body = encode(["Site":site.id,"Alias":alias,"Password":secret,"Replace":false,"Internet":internet])
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
        let body = encode(["Site":selected,"Method":"GET","Path":"/netgo/v2/remote","Body":NSNull(),"Idempotency":""])
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

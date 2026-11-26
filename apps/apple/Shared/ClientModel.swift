import Foundation
#if os(macOS)
import AppKit
import ServiceManagement
#else
import UIKit
#endif
import SwiftUI
import NetGoApple

struct SiteItem: Identifiable, Equatable {
    let id: String
    let name: String
    let raw: String
    var importID = ""
    var needsImport = false
    var trusted = false
}
struct HostItem: Identifiable { let id: String; let name: String; let status: String; let capabilities: Int
    var ready: Bool { status == "ready" && capabilities & 1 != 0 }
    var label: String { name + " · " + (["ready":"可连接","busy":"使用中","offline":"离线","vpn_offline":"VPN 未连接","disabled":"已停用"][status] ?? status) }
}

@MainActor final class ClientModel: ObservableObject {
    @Published var sites: [SiteItem] = []
    @Published var hosts: [HostItem] = []
    @Published var selected = "" { didSet { if selected != oldValue { if oldValue.isEmpty {invalidate()} else {disconnect()} } } }
    @Published var replaceSessions = false
    @Published var resources: [String] = []
    private var epoch: UInt64 = 0
    private var hostEpoch: UInt64 = 0
    private var refreshing = false
    private var profilesRevision: UInt64?
    private var connectionSummary = ""
    private func invalidate() { epoch &+= 1; hostEpoch &+= 1; hosts=[]; resources=[]; remoteReady=false; stopRemote() }
    private var remoteSuspended=false
    private var nextRemoteAttempt=Date.distantPast
        func stopRemote(suspend:Bool=true) { if suspend {remoteSuspended=true};viewer.stop()
#if os(macOS)
        host.stop()
#endif
    }
    @Published var alias = ""
    @Published var password = ""
    @Published var message = "未连接"
    @Published var connected = false
    @Published var remoteReady = false
	@Published var internet = false
	@Published var confirmTrust = false
	private var pendingImport: Data?
    let viewer = RemoteViewer()
    @Published var transferFiles:[String] = []
    @Published var remoteCapabilities: UInt32 = 0
    @Published var remoteViewOnly = false
    @Published var remoteAudio = true
    @Published var lastDownload:URL?
    private var selectedUpload:URL?
    private let downloads = RemoteDownloads()
    private var pendingDownload:URL?
#if os(macOS)
    let host = RemoteHost()
    @Published var unattended=UserDefaults.standard.bool(forKey:"remoteUnattended") { didSet {
        UserDefaults.standard.set(unattended,forKey:"remoteUnattended")
        do {if unattended {try SMAppService.mainApp.register()} else {try SMAppService.mainApp.unregister()}} catch {message="登录启动设置："+error.localizedDescription}
        if !unattended {stopRemote();disableUnattendedAction?()}
    }}
    func shareDesktop() {
        guard remoteReady,let request else { return };remoteSuspended=false;viewer.stop();let site=selected
        host.start(site:site,unattended:unattended) { method,_,body,_ in
            if method=="LOCAL_CONTEXT" { return try request("GET","/desktop/remote/context?site="+site,"") }
            return try request("POST","/desktop/remote/call",body)
        }
    }
#endif
    var request: ((String,String,String) throws -> String)?
    var importAction: ((String,String,Bool) throws -> Void)?
    var connectAction: ((SiteItem,String,String,Bool,Bool) -> Void)?
    var disconnectAction: (() -> Void)?
    var restoreAction: (() -> Void)?
    var disableUnattendedAction: (() -> Void)?
    private var timer: Timer?

    init() {
        sites = (UserDefaults.standard.stringArray(forKey:"sites") ?? []).compactMap { raw in guard let object = try? JSONSerialization.jsonObject(with:Data(raw.utf8)) as? [String:Any], let id = object["ID"] as? String else { return nil }; return SiteItem(id:id,name:object["Name"] as? String ?? id,raw:raw,importID:(UserDefaults.standard.dictionary(forKey:"siteImportIDs")?[id] as? String) ?? "",needsImport:UserDefaults.standard.stringArray(forKey:"pendingSites")?.contains(id) ?? false,trusted:UserDefaults.standard.stringArray(forKey:"trustedSites")?.contains(id) ?? false) }
        selected = UserDefaults.standard.string(forKey:"remoteSite") ?? sites.first?.id ?? ""
        viewer.changed = { [weak self] text in Task { @MainActor in self?.message = text } }
        viewer.event = { [weak self] value in Task { @MainActor in self?.exchangeEvent(value) } }
        viewer.permissionsChanged = { [weak self] bits in Task { @MainActor in self?.remoteCapabilities=bits } }
#if os(macOS)
        host.changed = { [weak self] text in Task { @MainActor in self?.message=text;if text.contains("系统设置") {self?.remoteSuspended=true} } }
#endif
        timer = Timer.scheduledTimer(withTimeInterval: 2,repeats: true) { [weak self] _ in Task { @MainActor in self?.refresh() } }
    }
    private var importGeneration: UInt64 = 0
    func importSite(from url: URL) {
        importGeneration &+= 1
        let version = importGeneration
        message = "读取站点文件…"
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let result = Result<Data, Error> {
                let allowed = url.startAccessingSecurityScopedResource()
                defer { if allowed { url.stopAccessingSecurityScopedResource() } }
                let file = try FileHandle(forReadingFrom: url)
                defer { try? file.close() }
                var data = Data()
                while let chunk = try file.read(upToCount: min(8192, 65537 - data.count)), !chunk.isEmpty {
                    data.append(chunk)
                    guard data.count <= 65536 else {
                        throw NSError(domain: "NetGo", code: 1, userInfo: [NSLocalizedDescriptionKey: "站点文件不能超过 64 KiB"])
                    }
                }
                return data
            }
            Task { @MainActor in
                guard let self = self, self.importGeneration == version else { return }
                switch result {
                case .success(let data): self.importSite(data)
                case .failure(let error): self.message = error.localizedDescription
                }
            }
        }
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
            let item = SiteItem(id:id,name:name,raw:raw,importID:UUID().uuidString,needsImport:importAction == nil,trusted:approved)
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
        profilesRevision = nil
        sites.removeAll { $0.id == item.id }; sites.append(item); selected = item.id; self.message = message
        persistSites()
    }
    private func persistSites() {
        UserDefaults.standard.set(sites.map(\.raw),forKey:"sites")
        UserDefaults.standard.set(Dictionary(uniqueKeysWithValues:sites.filter{$0.needsImport}.map{($0.id,$0.importID)}),forKey:"siteImportIDs")
        UserDefaults.standard.set(sites.filter{$0.needsImport}.map(\.id),forKey:"pendingSites")
        UserDefaults.standard.set(sites.filter{$0.trusted}.map(\.id),forKey:"trustedSites")
    }
    private func synchronizeProfiles(_ data: Data) -> Bool {
        guard let values = try? JSONSerialization.jsonObject(with:data) as? [[String:Any]] else { return false }
        var updated = sites
        for value in values {
            guard let id=value["ID"] as? String, let bytes=try? JSONSerialization.data(withJSONObject:value,options:.sortedKeys) else {continue}
            if let pending=sites.first(where:{$0.id==id && $0.needsImport}), let old=try? JSONSerialization.jsonObject(with:Data(pending.raw.utf8)) as? [String:Any] {
                if old["Origin"] as? String != value["Origin"] as? String || old["CA"] as? String != value["CA"] as? String || (old["ConfigRevision"] as? UInt64 ?? 0) > (value["ConfigRevision"] as? UInt64 ?? 0) {continue}
            }
            let item = SiteItem(id:id,name:value["Name"] as? String ?? id,raw:String(decoding:bytes,as:UTF8.self))
            if let index=updated.firstIndex(where:{$0.id==id}) { updated[index]=item } else { updated.append(item) }
        }
        if updated != sites { sites=updated; persistSites() }
        return true
    }
    func approveImport() { if let data = pendingImport { pendingImport = nil; importSite(data,approved:true) } }
    func cancelImport() { pendingImport = nil }
    func connect() {
        guard let site = sites.first(where:{$0.id == selected}), !alias.isEmpty, !password.isEmpty else { message = "请选择站点并填写账号密码"; return }
        invalidate();remoteSuspended=false
        UserDefaults.standard.set(selected,forKey:"remoteSite")
        let secret = password; password = ""; message = "连接中…"
        if let connectAction = connectAction { connectAction(site,alias,secret,internet,replaceSessions); return }
        let body = encode(["Site":site.id,"Alias":alias,"Password":secret,"Replace":replaceSessions,"Internet":internet])
        operation("POST","/desktop/connect",body)
    }
    func disconnect() { invalidate(); connected=false; if let disconnectAction = disconnectAction { disconnectAction() } else { operation("POST","/desktop/disconnect","{}") } }
    func refresh() {
        guard let request = request, !refreshing else { return }
        refreshing=true; let version=epoch; let knownProfiles=profilesRevision
        DispatchQueue.global(qos:.utility).async { [weak self] in
            do {
                let raw = try request("GET","/desktop/status","")
                let object = try JSONSerialization.jsonObject(with:Data(raw.utf8)) as? [String:Any] ?? [:]
                let revision = object["profiles_revision"] as? UInt64 ?? 0
                let profiles: String?
                if knownProfiles == revision { profiles=nil } else { profiles=try? request("GET","/desktop/profiles","") }
                Task { @MainActor in
                    guard let self=self else{return}; self.refreshing=false; guard version==self.epoch else{return}
                    self.connected=object["state"] as? String == "connected"
                    self.remoteReady=object["remote_ready"] as? Bool == true
                    let summary=self.connected ? "已连接" : ((object["error"] as? String).flatMap{$0.isEmpty ? nil : $0} ?? "未连接")
                    if summary != self.connectionSummary { self.connectionSummary=summary;self.message=summary }
                    self.resources=((object["lease"] as? [String:Any])?["resources"] as? [[String:Any]] ?? []).map{($0["name"] as? String ?? "") + " · " + ($0["cidr"] as? String ?? "")}
                    if !self.remoteReady { self.hostEpoch &+= 1; self.hosts=[]; self.resources=[]; self.stopRemote(suspend:false) }
#if os(macOS)
                    if self.unattended && !self.remoteSuspended && Date()>=self.nextRemoteAttempt {
                        if self.remoteReady,object["site_id"] as? String == self.selected,!self.host.isRunning {
                            self.nextRemoteAttempt=Date().addingTimeInterval(30);self.shareDesktop()
                        } else if !self.connected,["failed","disconnected"].contains(object["state"] as? String ?? "") {
                            self.nextRemoteAttempt=Date().addingTimeInterval(30)
                            self.restoreAction?()
                        }
                    }
#endif
                    if let profiles=profiles,self.synchronizeProfiles(Data(profiles.utf8)) {self.profilesRevision=revision}
                }
            } catch { Task { @MainActor in guard let self=self else{return};self.refreshing=false;if version==self.epoch {self.connected=false;self.remoteReady=false;self.hosts=[];self.resources=[];self.stopRemote(suspend:false)} } }
        }
    }
    func loadHosts() {
        guard remoteReady, let request=request else{return}
        hostEpoch &+= 1; let version=epoch, query=hostEpoch; hosts=[]
        let body=encode(["Site":selected,"Method":"GET","Path":"/netgo/v2/remote","Body":NSNull(),"Idempotency":""])
        DispatchQueue.global().async { [weak self] in
            do {
                let raw=try request("POST","/desktop/remote/call",body)
                let object=try JSONSerialization.jsonObject(with:Data(raw.utf8)) as? [String:Any] ?? [:]
                let hosts=(object["hosts"] as? [[String:Any]] ?? []).compactMap { item -> HostItem? in
                    guard let id=item["id"] as? String, let caps=item["available_capabilities"] as? Int, caps & 1 != 0 else{return nil}
                    return HostItem(id:id,name:item["name"] as? String ?? id,status:item["status"] as? String ?? "offline",capabilities:caps)
                }
                Task { @MainActor in guard let self=self,version==self.epoch,query==self.hostEpoch else{return};self.hosts=hosts;if hosts.isEmpty{self.message="暂无已授权主机"} }
            } catch { Task { @MainActor in guard let self=self,version==self.epoch,query==self.hostEpoch else{return};self.hosts=[];self.message=error.localizedDescription } }
        }
    }
    func view(_ host: HostItem) {
        guard host.ready, remoteReady, let request = request else { return }
        remoteSuspended=true
#if os(macOS)
        self.host.stop()
#endif
        viewer.control = { [site=selected] method,_,body,_ in
            switch method {
            case "LOCAL_CONTEXT": return try request("GET","/desktop/remote/context?site=" + site,"")
            case "LOCAL_SIGN": return try request("POST","/desktop/remote/sign",body)
            default: return try request("POST","/desktop/remote/call",body)
            }
        }
        viewer.start(site:selected,host:host.id,audio:remoteAudio,viewOnly:remoteViewOnly)
    }
    private func exchangeEvent(_ value:[String:Any]) {
        if let error=value["error"] as? String { message="操作失败："+error }
        else if value["action"] as? String == "clipboard.get",let text=value["text"] as? String {
#if os(macOS)
            NSPasteboard.general.clearContents();NSPasteboard.general.setString(text,forType:.string)
#else
            UIPasteboard.general.string=text
#endif
            message="已复制远端剪贴板"
        } else if let files=value["files"] as? [[String:Any]] { transferFiles=files.compactMap{$0["name"] as? String} }
        else if let bytes=value["bytes"] as? NSNumber,let size=value["size"] as? NSNumber { message="文件传输：\(bytes) / \(size) 字节" }
        if value["done"] as? Bool == true {
            if value["action"] as? String == "upload",value["path"] as? String == selectedUpload?.path {
                selectedUpload?.stopAccessingSecurityScopedResource();selectedUpload=nil
            }
            if value["action"] as? String == "download", let path=value["path"] as? String, path==pendingDownload?.path {
                if value["error"]==nil { lastDownload=pendingDownload }
                else if let url=pendingDownload { downloads.discard(url) }
                pendingDownload=nil
            }
        }
    }
    func sendClipboard() {
#if os(macOS)
        let text=NSPasteboard.general.string(forType:.string) ?? ""
#else
        let text=UIPasteboard.general.string ?? ""
#endif
        viewer.command(["action":"clipboard.set","text":text])
    }
    func upload(_ url:URL) { guard selectedUpload==nil else { message="请等待当前上传完成或取消";return };_ = url.startAccessingSecurityScopedResource();selectedUpload=url;if !viewer.command(["action":"upload","path":url.path,"name":url.lastPathComponent]) { url.stopAccessingSecurityScopedResource();selectedUpload=nil;message="远程连接不可用或任务队列已满" } }
    func download(_ name:String) {
        guard pendingDownload==nil else { message="请等待当前下载完成或取消";return }
        do {
            let url=try downloads.destination(name)
            if viewer.command(["action":"download","name":name,"path":url.path]) { pendingDownload=url }
            else { downloads.discard(url);message="远程连接不可用或任务队列已满" }
        } catch { message=error.localizedDescription }
    }
    func discardDownload() { if let url=lastDownload { downloads.discard(url);lastDownload=nil } }
    private func operation(_ method: String,_ path: String,_ body: String) {
        let version=epoch
        guard let request = request else { return }
        DispatchQueue.global().async { [weak self] in
            do { _ = try request(method,path,body); Task { @MainActor in if self?.epoch==version {self?.refresh()} } }
            catch { Task { @MainActor in if self?.epoch==version {self?.message = error.localizedDescription} } }
        }
    }
    private func encode(_ object: [String:Any]) -> String { String(decoding:(try? JSONSerialization.data(withJSONObject:object)) ?? Data(),as:UTF8.self) }
}

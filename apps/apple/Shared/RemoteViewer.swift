import Foundation
import NetGoApple

final class RemoteViewer {
    static let textLimit = Int(ng_remote_input_text_limit())
    let media = MediaPlayer()
    private let lock = NSLock()
    private var running = false
    private var generation: UInt64 = 0
    private var sessionGeneration: UInt64 = 0
    private func current(_ epoch:UInt64)->Bool {lock.lock();defer {lock.unlock()};return generation==epoch}
    private var width: UInt32 = 640, height: UInt32 = 360
    private let inputs = ng_remote_input_create()
    deinit { ng_remote_input_destroy(inputs) }
    private var client: OpaquePointer?
    private var sitePointer: UnsafeMutablePointer<CChar>?
    private var hostPointer: UnsafeMutablePointer<CChar>?
    private let queue = DispatchQueue(label: "org.netgo.viewer")
    var control: ((String,String,String,String) throws -> String)?
    private var sessionControl: ((String,String,String,String) throws -> String)?
    var changed: ((String) -> Void)?
    var event: (([String:Any]) -> Void)?
    var permissionsChanged: ((UInt32) -> Void)?
    private(set) var site = ""
    private(set) var capabilities: UInt32 = 0

    func start(site: String, host: String, audio: Bool = true, viewOnly: Bool = false) {
        stop(); let selectedControl = control
        lock.lock(); let requested = generation; lock.unlock()
        queue.async { [self] in
            lock.lock(); guard generation == requested else { lock.unlock(); return }; running = true; media.start(); ng_remote_input_clear(inputs); lock.unlock()
            self.site = site; sessionGeneration=requested;sessionControl = selectedControl
            sitePointer = strdup(site); hostPointer = strdup(host)
            var options = ng_remote_client_options()
            options.struct_size = UInt32(MemoryLayout<ng_remote_client_options>.size); options.abi_version = UInt32(NG_REMOTE_CLIENT_ABI)
            options.site_id = UnsafePointer(sitePointer); options.host_id = UnsafePointer(hostPointer)
            options.capabilities = 1 | 8 | 16 | 32 | (audio ? 4 : 0) | (viewOnly ? 0 : 2); options.context = Unmanaged.passUnretained(self).toOpaque()
            options.active = { ctx in let v = Unmanaged<RemoteViewer>.fromOpaque(ctx!).takeUnretainedValue(); v.lock.lock(); defer { v.lock.unlock() }; return v.running ? 1 : 0 }
            options.control = { ctx, method, path, body, key, out, capacity, length in
                let v = Unmanaged<RemoteViewer>.fromOpaque(ctx!).takeUnretainedValue()
                return remoteControlReply(output: out, capacity: capacity, length: length) {
                    try v.call(String(cString: method!),String(cString: path!),String(cString: body!),String(cString: key!))
                }
            }
            options.sign = { ctx, message, size, signature in
                let v = Unmanaged<RemoteViewer>.fromOpaque(ctx!).takeUnretainedValue()
                do {
                    let body = try v.json(["Site":v.site,"Message":Data(bytes:message!,count:size).base64url])
                    let result = try v.sessionControl?("LOCAL_SIGN","",body,"") ?? ""
                    guard let object = try JSONSerialization.jsonObject(with: Data(result.utf8)) as? [String:Any], let text = object["signature"] as? String, let data = Data(base64url:text), data.count == 64 else { return -1 }
                    data.withUnsafeBytes { memcpy(signature!,$0.baseAddress!,64) }; return 0
                } catch { return -1 }
            }
            options.input = { ctx, output, capacity, length in
                let v = Unmanaged<RemoteViewer>.fromOpaque(ctx!).takeUnretainedValue()
                return ng_remote_input_read(v.inputs, output, capacity, length)
            }
            options.format = { ctx, width, height, _, caps in let v = Unmanaged<RemoteViewer>.fromOpaque(ctx!).takeUnretainedValue(); v.lock.lock(); v.width = width; v.height = height; v.capabilities = caps; let epoch=v.sessionGeneration; v.lock.unlock(); DispatchQueue.main.async { if v.current(epoch) { v.permissionsChanged?(caps) } }; return 0 }
            options.video = { ctx, bytes, size, time in Unmanaged<RemoteViewer>.fromOpaque(ctx!).takeUnretainedValue().media.video(Data(bytes:bytes!,count:size),time:time) ? 0 : -1 }
            options.audio = { ctx, bytes, size in Unmanaged<RemoteViewer>.fromOpaque(ctx!).takeUnretainedValue().media.audio(Data(bytes:bytes!,count:size)) ? 0 : -1 }
            options.event = { ctx,json in let v=Unmanaged<RemoteViewer>.fromOpaque(ctx!).takeUnretainedValue();if let data=String(cString:json!).data(using:.utf8),let object=(try? JSONSerialization.jsonObject(with:data)) as? [String:Any] { let epoch=v.sessionGeneration;DispatchQueue.main.async { if v.current(epoch)||(["upload","download"].contains(object["action"] as? String ?? "")&&object["done"] as? Bool==true){v.event?(object)} } } }
            options.status = { ctx, code in let v = Unmanaged<RemoteViewer>.fromOpaque(ctx!).takeUnretainedValue(); let code = String(cString:code!); let messages = ["remote_connected":"远程桌面已连接", "remote_waiting_consent":"等待主机确认…", "remote_host_busy":"对方正在使用远程桌面，请稍后刷新", "remote_host_unavailable":"对方暂不可连接，请检查 VPN 和桌面共享", "remote_view_forbidden":"当前账号没有观看权限", "remote_disconnected":"远程桌面已结束"]; let epoch=v.sessionGeneration;DispatchQueue.main.async { if v.current(epoch){v.changed?(messages[code] ?? code)} } }
            var created: OpaquePointer?
            if ng_remote_client_create(&options,&created) == 0, let created = created {
                lock.lock(); client = created; if !running { ng_remote_client_cancel(created) }; lock.unlock()
                _ = ng_remote_client_run(created)
                lock.lock(); client = nil; lock.unlock(); ng_remote_client_destroy(created)
            }
            lock.lock(); client = nil; running = false; capabilities = 0; lock.unlock()
            let ended=sessionGeneration
            DispatchQueue.main.async { if self.current(ended) { self.permissionsChanged?(0) } }
            free(sitePointer); free(hostPointer); sitePointer = nil; hostPointer = nil; media.stop()
        }
    }
    private func call(_ method: String,_ path: String,_ body: String,_ key: String) throws -> String {
        guard let control = sessionControl else { throw NSError(domain:"NetGo",code:1) }
        if method == "CONTEXT" { return try control("LOCAL_CONTEXT","",try json(["Site":site]),"") }
        return try control("LOCAL_CALL","",try json(["Site":site,"Method":method,"Path":path,"Body":(try? JSONSerialization.jsonObject(with:Data(body.utf8))) ?? NSNull(),"Idempotency":key]),"")
    }
    private func json(_ value: Any) throws -> String { String(decoding:try JSONSerialization.data(withJSONObject:value),as:UTF8.self) }
    @discardableResult func input(_ event: Any) -> Bool {
        lock.lock(); defer { lock.unlock() }
        guard running, capabilities & 2 != 0, let data = try? JSONSerialization.data(withJSONObject:event) else { return false }
        return data.withUnsafeBytes { bytes in
            ng_remote_input_push(inputs, bytes.baseAddress?.assumingMemoryBound(to: CChar.self), bytes.count) == 0
        }
    }
    func key(_ code: Int) -> Bool {
        let events: [[String:Any]] = [["type":"key","code":code,"down":true],["type":"key","code":code,"down":false]]
        return input(events)
    }
    func pointer(x: CGFloat,y: CGFloat,viewWidth: CGFloat,viewHeight: CGFloat,down: Bool?,button: Int = 1) {
        lock.lock(); let w = CGFloat(width), h = CGFloat(height); lock.unlock()
        let scale = min(viewWidth/max(w,1),viewHeight/max(h,1)), left = (viewWidth-w*scale)/2, top = (viewHeight-h*scale)/2
        guard scale > 0 else { return }
        if down == true && (x < left || x >= left+w*scale || y < top || y >= top+h*scale) { return }
        input(["type":"move","x":UInt32(max(0,min(1,(x-left)/(w*scale)))*65535),"y":UInt32(max(0,min(1,(y-top)/(h*scale)))*65535)])
        if let down = down { input(["type":"button","button":button,"down":down]) }
    }
    @discardableResult func command(_ value:[String:Any]) -> Bool {
        let accepted: Bool = {
            lock.lock();defer { lock.unlock() }
            guard running,let client,let data=try? JSONSerialization.data(withJSONObject:value) else { return false }
            return data.withUnsafeBytes { bytes in ng_remote_client_command(client,bytes.baseAddress?.assumingMemoryBound(to:CChar.self),bytes.count)==0 }
        }()
        if !accepted { DispatchQueue.main.async { self.changed?("操作尚未提交，连接不可用或任务队列已满") } }
        return accepted
    }
    func stop() { lock.lock(); generation &+= 1; let ended=generation; running = false; capabilities=0; if let client = client { ng_remote_client_cancel(client) }; lock.unlock(); DispatchQueue.main.async { if self.current(ended) { self.permissionsChanged?(0) } }; media.stop() }
}

extension Data {
    var base64url: String { base64EncodedString().replacingOccurrences(of:"+",with:"-").replacingOccurrences(of:"/",with:"_").replacingOccurrences(of:"=",with:"") }
    init?(base64url: String) { var text = base64url.replacingOccurrences(of:"-",with:"+").replacingOccurrences(of:"_",with:"/"); text += String(repeating:"=",count:(4-text.count%4)%4); self.init(base64Encoded:text) }
}

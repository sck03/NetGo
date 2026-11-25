#if os(macOS)
import Foundation
import AppKit
import NetGoApple

final class RemoteHost {
    private let lock = NSLock()
    private let queue = DispatchQueue(label:"org.netgo.host")
    private var handle: OpaquePointer?
    private var running = false
    private var generation: UInt64 = 0
    private var media: UnsafeMutableRawPointer?
    private var control: ((String,String,String,String) throws -> String)?
    private var site = ""
    var changed: ((String) -> Void)?
    func start(site: String, unattended: Bool, control: @escaping (String,String,String,String) throws -> String) {
        stop()
        guard ng_apple_host_permissions() == 0 else { changed?("请在系统设置中允许 NetGo 屏幕录制与辅助功能权限，再重新开启共享"); return }
        lock.lock(); let epoch=generation; lock.unlock()
        queue.async { [self] in
            lock.lock(); guard generation==epoch else { lock.unlock(); return }; running=true; lock.unlock()
            self.site=site; self.control=control; media=ng_apple_host_media_create()
            guard media != nil else { lock.lock();running=false;lock.unlock();self.control=nil;report("无法创建屏幕采集器"); return }
            defer { ng_apple_host_media_destroy(media); media=nil; self.control=nil; lock.lock(); running=false; lock.unlock() }
            let directory=FileManager.default.urls(for:.applicationSupportDirectory,in:.userDomainMask)[0].appendingPathComponent("NetGo/Exchange",isDirectory:true)
            do { try FileManager.default.createDirectory(at:directory,withIntermediateDirectories:true,attributes:[.posixPermissions:0o700]) } catch { report("无法创建文件交换目录"); return }
            let sitePointer=strdup(site), name=strdup(Host.current().localizedName ?? "Mac"), platform=strdup("macos"), folder=strdup(directory.path)
            defer { free(sitePointer); free(name); free(platform); free(folder) }
            var o=ng_remote_host_options(); o.struct_size=UInt32(MemoryLayout<ng_remote_host_options>.size);o.abi_version=UInt32(NG_REMOTE_HOST_ABI);o.site_id=UnsafePointer(sitePointer);o.name=UnsafePointer(name);o.platform=UnsafePointer(platform);o.exchange_directory=UnsafePointer(folder);o.capabilities=59;o.unattended=unattended ? 1:0;o.context=Unmanaged.passUnretained(self).toOpaque()
            o.active = { ctx in let h=Unmanaged<RemoteHost>.fromOpaque(ctx!).takeUnretainedValue(); h.lock.lock(); defer { h.lock.unlock() };return h.running ? 1:0 }
            o.control = { ctx,method,path,body,_,output,capacity,length in
                let h=Unmanaged<RemoteHost>.fromOpaque(ctx!).takeUnretainedValue()
                do { guard let call=h.control else { return -1 };let verb=String(cString:method!);let value:String
                    if verb=="CONTEXT" { value=try call("LOCAL_CONTEXT","","","") }
                    else { let request:[String:Any] = ["Site":h.site,"Method":verb,"Path":String(cString:path!),"Body":(try? JSONSerialization.jsonObject(with:Data(String(cString:body!).utf8))) ?? NSNull(),"Idempotency":""];let payload=String(decoding:try JSONSerialization.data(withJSONObject:request),as:UTF8.self);value=try call("LOCAL_CALL","",payload,"") }
                    let data=Data(value.utf8);guard data.count<=capacity else { return -1 };data.withUnsafeBytes { if let p=$0.baseAddress { memcpy(output!,p,data.count) } };length!.pointee=data.count;return 0
                } catch { return -1 }
            }
            o.consent = { ctx,user,caps in
                let h=Unmanaged<RemoteHost>.fromOpaque(ctx!).takeUnretainedValue();let reply=HostConsent();let account=String(cString:user!)
                DispatchQueue.main.async { reply.lock.lock();let expired=reply.expired;reply.lock.unlock();guard !expired else { reply.done.signal();return };let alert=NSAlert();alert.messageText="是否允许远程连接？";alert.informativeText="站点账号 \(account) 申请访问桌面。权限：\(caps & 2 != 0 ? "键鼠控制，" : "")\(caps & 8 != 0 ? "剪贴板，" : "")\(caps & 48 != 0 ? "交换目录文件，" : "")屏幕查看。";alert.addButton(withTitle:"拒绝");alert.addButton(withTitle:"允许");reply.alert=alert
                    guard let window=NSApp.windows.first else { reply.done.signal();return };alert.beginSheetModal(for:window) { response in reply.lock.lock();reply.allowed=response == .alertSecondButtonReturn && !reply.expired;reply.lock.unlock();reply.done.signal() }
                }
                let deadline=Date().addingTimeInterval(15);while Date()<deadline { h.lock.lock();let live=h.running;h.lock.unlock();if !live { break };if reply.done.wait(timeout:.now()+0.1) == .success { reply.lock.lock();let allowed=reply.allowed;reply.lock.unlock();return allowed ? 1:0 } }
                reply.lock.lock();reply.expired=true;reply.lock.unlock();DispatchQueue.main.async { if let alert=reply.alert,let parent=alert.window.sheetParent { parent.endSheet(alert.window,returnCode:.alertFirstButtonReturn) } };return 0
            }
            o.open = { ctx,caps,w,h,fps in let host=Unmanaged<RemoteHost>.fromOpaque(ctx!).takeUnretainedValue();return ng_apple_host_media_open(host.media,caps,w,h,fps) }
            o.poll = { ctx,kind,out,capacity,length in let host=Unmanaged<RemoteHost>.fromOpaque(ctx!).takeUnretainedValue();return ng_apple_host_media_poll(host.media,kind,out,capacity,length) }
            o.input = { ctx,text,size in let host=Unmanaged<RemoteHost>.fromOpaque(ctx!).takeUnretainedValue();return ng_apple_host_media_input(host.media,text,size) }
            o.close = { ctx in let host=Unmanaged<RemoteHost>.fromOpaque(ctx!).takeUnretainedValue();ng_apple_host_media_close(host.media) }
            o.clipboard=ng_apple_host_clipboard
            o.status = { ctx,code in let host=Unmanaged<RemoteHost>.fromOpaque(ctx!).takeUnretainedValue();let value=String(cString:code!);let messages=["remote_host_waiting":"桌面共享已开启，等待本机确认连接","remote_host_unattended":"无人值守共享已开启，仅接受站点授权账号","remote_host_connected":"正在共享桌面（GPU 硬件编码）","remote_host_stopped":"桌面共享已停止"];host.report(messages[value] ?? value) }
            var created:OpaquePointer?;guard ng_remote_host_create(&o,&created)==0,let created else { report("无法启动桌面共享");return }
            lock.lock();handle=created;if !running { ng_remote_host_cancel(created) };lock.unlock();_ = ng_remote_host_run(created);lock.lock();handle=nil;lock.unlock();ng_remote_host_destroy(created)
        }
    }
    private func report(_ text:String) { DispatchQueue.main.async { [weak self] in self?.changed?(text) } }
    var isRunning:Bool {lock.lock();defer {lock.unlock()};return running}
    func stop() { lock.lock();generation &+= 1;running=false;if let handle { ng_remote_host_cancel(handle) };lock.unlock() }
}
private final class HostConsent {
    let lock=NSLock()
    let done=DispatchSemaphore(value:0)
    var allowed=false,expired=false
    var alert:NSAlert?
}
#endif

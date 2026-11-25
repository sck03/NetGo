import SwiftUI
import UniformTypeIdentifiers
#if os(macOS)
import AppKit
#else
import UIKit
#endif

struct ClientView: View {
    @ObservedObject var model: ClientModel
    @State private var importing = false
    @State private var text = ""
    @State private var uploading = false
    var body: some View {
        VStack(spacing: 12) {
            HStack { Text("NetGo").font(.title2.bold()); Spacer(); Button("导入站点") { uploading=false;importing = true } }
            Picker("站点",selection:$model.selected) { ForEach(model.sites) { Text($0.name).tag($0.id) } }
            TextField("账号",text:$model.alias).textFieldStyle(.roundedBorder)
            SecureField("密码",text:$model.password).textFieldStyle(.roundedBorder)
            Toggle("替换此账号的其他连接",isOn:$model.replaceSessions).disabled(model.connected)
            Toggle("通过旁路由访问互联网（需要账号授权）",isOn:$model.internet).disabled(model.connected)
            HStack { Button("连接") { model.connect() }.disabled(model.connected); Button("断开") { model.disconnect() }; Spacer(); Text(model.message).font(.caption) }
            ForEach(model.resources,id: \.self) { Text($0).font(.caption) }
            Divider()
            HStack { Text("远程桌面"); Spacer(); Button("刷新主机") { model.loadHosts() }.disabled(!model.connected) }
            ScrollView(.horizontal) { HStack { ForEach(model.hosts) { host in Button(host.label) { model.view(host) }.disabled(!host.ready || !model.connected) } } }
#if os(macOS)
            HStack { Toggle("无人值守：保存凭据并在登录后恢复（连接前开启）",isOn:$model.unattended);Button("共享此 Mac") { model.shareDesktop() }.disabled(!model.connected) }
#endif
            ScrollView(.horizontal) { HStack { Button("发送剪贴板") { model.sendClipboard() };Button("获取剪贴板") { model.viewer.command(["action":"clipboard.get"]) };Button("上传文件") { uploading=true;importing=true };Menu("下载文件") { Button("刷新列表") { model.viewer.command(["action":"file.list"]) };ForEach(model.transferFiles,id: \.self) { name in Button(name) { model.download(name) } } };Button("取消传输") { model.viewer.command(["action":"cancel"]) };if let url=model.lastDownload { ShareLink("导出已下载文件",item:url) } } }
            ScreenView(viewer:model.viewer).frame(minHeight:180).background(.black)
            HStack { TextField("发送文字",text:$text).textFieldStyle(.roundedBorder); Button("发送") { model.viewer.input(["type":"text","text":text]); text = "" }; Button("停止远控") { model.stopRemote() } }
        }
        .padding()
        .fileImporter(isPresented:$importing,allowedContentTypes:uploading ? [.item]:[.json],allowsMultipleSelection:false) { result in
            do {
                guard let url=try result.get().first else {return}
                if uploading {model.upload(url);return}
                let allowed = url.startAccessingSecurityScopedResource()
                defer { if allowed { url.stopAccessingSecurityScopedResource() } }
                model.importSite(try Data(contentsOf:url))
            } catch { model.message = error.localizedDescription }
        }
        .alert("确认新的站点身份",isPresented:$model.confirmTrust) {
            Button("取消",role:.cancel) { model.cancelImport() }
            Button("确认管理员提供的配置") { model.approveImport() }
        } message: { Text("导入文件更改了站点地址或证书。请确认文件来自你的服务器管理员后继续。") }
    }
}

#if os(macOS)
struct ScreenView: NSViewRepresentable {
    let viewer: RemoteViewer
    func makeNSView(context: Context) -> Screen { let value = Screen(); value.viewer = viewer; value.wantsLayer = true; value.layer?.addSublayer(viewer.media.layer); return value }
    func updateNSView(_ view: Screen,context: Context) { view.viewer = viewer }
    final class Screen: NSView {
        var viewer: RemoteViewer?
        override var acceptsFirstResponder: Bool { true }
        override func layout() { super.layout(); viewer?.media.layer.frame = bounds }
        override func mouseDown(with event: NSEvent) { window?.makeFirstResponder(self); send(event,down:true) }
        override func mouseUp(with event: NSEvent) { send(event,down:false) }
        override func mouseDragged(with event: NSEvent) { send(event,down:nil) }
        override func rightMouseDown(with event: NSEvent) { send(event,down:true,button:2) }
        override func rightMouseUp(with event: NSEvent) { send(event,down:false,button:2) }
        private func send(_ event: NSEvent,down: Bool?,button: Int = 1) {
            let p = convert(event.locationInWindow,from:nil)
            viewer?.pointer(x:p.x,y:bounds.height-p.y,viewWidth:bounds.width,viewHeight:bounds.height,down:down,button:button)
        }
        override func keyDown(with event: NSEvent) { if let text = event.characters, !text.isEmpty { viewer?.input(["type":"text","text":text]) } }
        override func resignFirstResponder() -> Bool { viewer?.input(["type":"release"]); return super.resignFirstResponder() }
    }
}
#else
struct ScreenView: UIViewRepresentable {
    let viewer: RemoteViewer
    func makeUIView(context: Context) -> Screen { let value = Screen(); value.viewer = viewer; value.layer.addSublayer(viewer.media.layer); return value }
    func updateUIView(_ view: Screen,context: Context) { view.viewer = viewer }
    final class Screen: UIView {
        var viewer: RemoteViewer?
        override func layoutSubviews() { super.layoutSubviews(); viewer?.media.layer.frame = bounds }
        override func touchesBegan(_ touches: Set<UITouch>,with event: UIEvent?) { send(touches,down:true) }
        override func touchesMoved(_ touches: Set<UITouch>,with event: UIEvent?) { send(touches,down:nil) }
        override func touchesEnded(_ touches: Set<UITouch>,with event: UIEvent?) { send(touches,down:false) }
        override func touchesCancelled(_ touches: Set<UITouch>,with event: UIEvent?) { viewer?.input(["type":"release"]) }
        private func send(_ touches: Set<UITouch>,down: Bool?) {
            guard let touch = touches.first else { return }; let point = touch.location(in:self)
            viewer?.pointer(x:point.x,y:point.y,viewWidth:bounds.width,viewHeight:bounds.height,down:down)
        }
    }
}
#endif

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
            HStack { Text("远程桌面"); Spacer(); Button("刷新主机") { model.loadHosts() }.disabled(!model.remoteReady) }
            HStack {
                Toggle("仅观看", isOn: $model.remoteViewOnly)
                Toggle("播放对方声音（主机支持时）", isOn: $model.remoteAudio)
            }.disabled(model.remoteCapabilities != 0)
            ScrollView(.horizontal) { HStack { ForEach(model.hosts) { host in Button(host.label) { model.view(host) }.disabled(!host.ready || !model.remoteReady) } } }
#if os(macOS)
            HStack { Toggle("无人值守：保存凭据并在登录后恢复（连接前开启）",isOn:$model.unattended);Button("共享此 Mac") { model.shareDesktop() }.disabled(!model.remoteReady) }
#endif
            ScrollView(.horizontal) {
                HStack {
                    Button("发送剪贴板") { model.sendClipboard() }.disabled(model.remoteCapabilities & 8 == 0)
                    Button("获取剪贴板") { model.viewer.command(["action":"clipboard.get"]) }.disabled(model.remoteCapabilities & 8 == 0)
                    Button("上传文件") { uploading=true;importing=true }.disabled(model.remoteCapabilities & 16 == 0)
                    Menu("下载文件") {
                        Button("刷新列表") { model.viewer.command(["action":"file.list"]) }
                        ForEach(model.transferFiles,id: \.self) { name in Button(name) { model.download(name) } }
                    }.disabled(model.remoteCapabilities & 32 == 0)
                    Button("取消传输") { model.viewer.command(["action":"cancel"]) }.disabled(model.remoteCapabilities == 0)
                    if let url=model.lastDownload { ShareLink("导出已下载文件",item:url); Button("删除此下载") { model.discardDownload() } }
                }
            }
            ScreenView(viewer:model.viewer).frame(minHeight:180).background(.black)
            HStack {
                TextField("发送文字",text:$text).textFieldStyle(.roundedBorder).disabled(model.remoteCapabilities & 2 == 0)
                Button("发送") {
                    if model.viewer.input(["type":"text","text":text]) { text = "" }
                    else { model.message = "文字尚未发送，连接或输入队列繁忙，请重试" }
                }.disabled(model.remoteCapabilities & 2 == 0 || text.isEmpty || text.utf8.count > RemoteViewer.textLimit)
                Button("停止远控") { model.stopRemote() }
            }
            Text("每次最多 \(RemoteViewer.textLimit) UTF-8 字节，超限请分段发送").font(.caption)
            HStack {
                Button("退格") { if !model.viewer.key(8) { model.message="按键尚未发送，请重试" } }
                Button("回车") { if !model.viewer.key(13) { model.message="按键尚未发送，请重试" } }
            }.disabled(model.remoteCapabilities & 2 == 0)
        }
        .padding()
        .fileImporter(isPresented:$importing,allowedContentTypes:uploading ? [.item]:[.json],allowsMultipleSelection:false) { result in
            do {
                guard let url=try result.get().first else {return}
                if uploading {model.upload(url);return}
                model.importSite(from: url)
            } catch { model.message = error.localizedDescription }
        }
        .alert("确认新的站点身份",isPresented:$model.confirmTrust) {
            Button("取消",role:.cancel) { model.cancelImport() }
            Button("确认管理员提供的配置") { model.approveImport() }
        } message: { Text("导入文件更改了站点地址或证书。请确认文件来自你的服务器管理员后继续。") }
    }
}

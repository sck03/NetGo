import SwiftUI
import AppKit
import NetGoApple

@main struct NetGoApp: App {
    @StateObject private var model = ClientModel()
    private let runtime = DesktopRuntime()
    var body: some Scene {
        WindowGroup {
            ClientView(model:model).frame(minWidth:560,minHeight:560)
                .onAppear { runtime.attach(model) }
                .onDisappear { if !model.unattended {model.stopRemote()} }
        }.defaultSize(width:640,height:700)
    }
}

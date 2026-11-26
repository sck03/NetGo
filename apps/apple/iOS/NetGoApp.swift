import SwiftUI

@main struct NetGoApp: App {
    @StateObject private var model = ClientModel()
    @StateObject private var runtime = TunnelRuntime()
    var body: some Scene {
        WindowGroup { ClientView(model: model).onAppear { runtime.attach(model) } }
    }
}

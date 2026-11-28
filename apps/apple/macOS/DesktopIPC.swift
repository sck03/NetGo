import Foundation
import NetGoApple

private final class IPCActivity {
    let active: RemoteActivity
    init(_ active: @escaping RemoteActivity) { self.active = active }
}

// Synchronous bridge on the caller's worker, with that session's cancellation.
func desktopIPC(_ socket: String, _ method: String, _ path: String, _ body: String,
                active: @escaping RemoteActivity = { true }, timeout: Int32 = 22000) throws -> String {
    var output = [CChar](repeating: 0, count: 4 << 20), size = 0
    let capacity = output.count
    let activity = IPCActivity(active)
    let result = withExtendedLifetime(activity) {
        ng_apple_ipc(socket, method, path, body, &output, capacity, &size, { context in
            guard let context else { return 0 }
            return Unmanaged<IPCActivity>.fromOpaque(context).takeUnretainedValue().active() ? 1 : 0
        }, Unmanaged.passUnretained(activity).toOpaque(), timeout)
    }
    let text = output.withUnsafeBytes { String(decoding: $0.prefix(size), as: UTF8.self) }
    guard result == 0 else {
        let response = try? ControlResponse.operationError(Data(text.utf8))
        let failures: [Int32: String] = [-5: "control_response_too_large", -6: "control_cancelled", -7: "control_timeout"]
        let code = result > 0 ? (response ?? "remote_invalid_control_response") : (failures[result] ?? "ipc_failed")
        throw NSError(domain: "NetGo", code: Int(result), userInfo: [NSLocalizedDescriptionKey: code])
    }
    return text
}

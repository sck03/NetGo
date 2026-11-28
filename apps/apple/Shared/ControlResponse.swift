import Foundation

enum ControlResponse {
    // A successful state response may carry a nonempty diagnostic error.
    // Operation failures are the standalone {"error": "code"} envelope.
    static func operationError(_ data: Data) throws -> String? {
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              object.count == 1, let code = object["error"] as? String else { return nil }
        return code.isEmpty ? "ipc_failed" : code
    }
}

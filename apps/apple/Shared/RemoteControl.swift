import Foundation

typealias RemoteActivity = () -> Bool
typealias RemoteRequest = (String, String, String, @escaping RemoteActivity) throws -> String
typealias RemoteControl = (String, String, String, String, @escaping RemoteActivity) throws -> String

// Shared by host and viewer: the native core decides which errors are retryable.
// Buffer failures remain ABI failures; backend errors travel as their JSON code.
func remoteControlReply(output: UnsafeMutablePointer<CChar>?, capacity: Int,
                        length: UnsafeMutablePointer<Int>?, call: () throws -> String) -> Int32 {
    let data: Data
    do {
        data = Data(try call().utf8)
    } catch {
        guard let encoded = try? JSONSerialization.data(withJSONObject: ["error": error.localizedDescription]) else { return -1 }
        data = encoded
    }
    guard let output, let length, !data.isEmpty, data.count <= capacity else { return -1 }
    data.withUnsafeBytes { bytes in _ = memcpy(output, bytes.baseAddress!, data.count) }
    length.pointee = data.count
    return 0
}

import Foundation

@main struct ControlResponseTests {
    static func main() throws {
        for response in [#"{"state":"connected","error":""}"#,
                         #"{"state":"reconnecting","error":"network_unavailable"}"#] {
            let error = try ControlResponse.operationError(Data(response.utf8))
            precondition(error == nil)
        }
        let cancelled = try ControlResponse.operationError(Data(#"{"error":"control_cancelled"}"#.utf8))
        let empty = try ControlResponse.operationError(Data(#"{"error":""}"#.utf8))
        precondition(cancelled == "control_cancelled")
        precondition(empty == "ipc_failed")
    }
}

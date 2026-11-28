import Foundation

@main struct TunnelRequestQueueTests {
    static func wait(_ signal: DispatchSemaphore) {
        precondition(signal.wait(timeout: .now() + 5) == .success, "tunnel request stalled")
    }
    static func expectError(_ data: Data?, _ code: String) {
        precondition(data != nil, "missing request result")
        let reply = try! JSONSerialization.jsonObject(with: data!) as! [String: String]
        precondition(reply["error"] == code, "unexpected request result")
    }
    static func main() {
        let queue = TunnelRequestQueue()
        precondition(!queue.isClosed, "new queue is closed")
        let cancelled = DispatchSemaphore(value: 0)
        queue.cancel("early")
        queue.submit(id: "early", { _ in preconditionFailure("cancelled work ran") }, completion: {
            expectError($0, "control_cancelled"); cancelled.signal()
        })
        wait(cancelled)

        let entered = DispatchSemaphore(value: 0), release = DispatchSemaphore(value: 0)
        let completed = DispatchSemaphore(value: 0)
        for id in ["running-1", "running-2"] {
            queue.submit(id: id, { active in
                precondition(active(), "new request was cancelled")
                entered.signal(); wait(release)
                precondition(!active(), "close did not cancel running request")
                return Data("{\"error\":\"control_cancelled\"}".utf8)
            }, completion: { expectError($0, "control_cancelled"); completed.signal() })
        }
        wait(entered); wait(entered)
        queue.submit(id: "queued", { _ in preconditionFailure("queued work ran after close") }, completion: {
            expectError($0, "control_cancelled"); completed.signal()
        })
        queue.close(); queue.close()
        precondition(queue.isClosed, "close state was lost")
        queue.cancel("late")
        let rejected = DispatchSemaphore(value: 0)
        queue.submit(id: "late", { _ in preconditionFailure("closed queue accepted work") }, completion: {
            expectError($0, "remote_site_disconnected"); rejected.signal()
        })
        wait(rejected)
        release.signal(); release.signal()
        for _ in 0..<3 { wait(completed) }
        print("PASS early cancellation, close during running/queued requests and rejection after close")
    }
}

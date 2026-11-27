import Foundation

// Network messages have a bounded executor independent of tunnel lifecycle work.
// Queued messages still receive a reply after stop, so callers do not time out.
final class TunnelRequestQueue {
    private let lock = NSLock()
    private let queue = OperationQueue()
    private var pending = 0
    private var closed = false

    init() {
        queue.maxConcurrentOperationCount = 2
        queue.name = "org.netgo.tunnel.requests"
    }

    var isClosed: Bool {
        lock.lock()
        defer { lock.unlock() }
        return closed
    }

    func close() {
        lock.lock()
        closed = true
        lock.unlock()
    }

    func submit(_ work: @escaping () -> Data?, completion: ((Data?) -> Void)?) {
        lock.lock()
        guard !closed && pending < 8 else {
            let code = closed ? "remote_site_disconnected" : "control_busy"
            lock.unlock()
            completion?(Data("{\"error\":\"\(code)\"}".utf8))
            return
        }
        pending += 1
        lock.unlock()
        queue.addOperation { [self] in
            let reply = isClosed ? Data("{\"error\":\"remote_site_disconnected\"}".utf8) : work()
            lock.lock()
            pending -= 1
            lock.unlock()
            completion?(reply)
        }
    }
}

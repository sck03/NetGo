import Foundation

private final class TunnelRequestActivity {
    private let lock = NSLock()
    private var running = true
    var active: Bool { lock.lock(); defer { lock.unlock() }; return running }
    func cancel() { lock.lock(); running = false; lock.unlock() }
}

// Cancellation bypasses the executor, including calls still queued behind IO.
// Bounded tombstones also cover a cancel message delivered before its request.
final class TunnelRequestQueue {
    private let lock = NSLock()
    private let queue = OperationQueue()
    private var calls: [String: TunnelRequestActivity] = [:]
    private var cancelled: [String: Date] = [:]
    private var closed = false

    var isClosed: Bool {
        lock.lock()
        defer { lock.unlock() }
        return closed
    }

    init() {
        queue.maxConcurrentOperationCount = 2
        queue.name = "org.netgo.tunnel.requests"
    }
    func close() {
        lock.lock(); defer { lock.unlock() }
        closed = true
        calls.values.forEach { $0.cancel() }
        cancelled.removeAll()
    }
    func cancel(_ id: String) {
        lock.lock(); defer { lock.unlock() }
        if let call = calls[id] { call.cancel(); return }
        guard !closed else { return }
        cancelled = cancelled.filter { $0.value > Date() }
        if cancelled.count >= 64, let oldest = cancelled.min(by: { $0.value < $1.value }) { cancelled.removeValue(forKey: oldest.key) }
        cancelled[id] = Date().addingTimeInterval(30)
    }
    func submit(id: String, _ work: @escaping (@escaping () -> Bool) -> Data?, completion: ((Data?) -> Void)?) {
        lock.lock()
        guard !closed && calls.count < 8 && calls[id] == nil else {
            let code = closed ? "remote_site_disconnected" : "remote_busy"
            lock.unlock(); completion?(Data("{\"error\":\"\(code)\"}".utf8)); return
        }
        let activity = TunnelRequestActivity()
        if let expiry = cancelled.removeValue(forKey: id), expiry > Date() { activity.cancel() }
        calls[id] = activity
        lock.unlock()
        queue.addOperation { [self] in
            let reply = activity.active ? work({ activity.active }) : Data("{\"error\":\"control_cancelled\"}".utf8)
            lock.lock(); calls.removeValue(forKey: id); lock.unlock()
            completion?(reply)
        }
    }
}

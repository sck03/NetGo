#if os(macOS)
import Foundation

// Every operation advances the same generation before entering the worker
// queue. A queued restore can never reconnect after a later disconnect.
final class DesktopConnectionCoordinator {
    enum Intent {
        case connect, restore, disconnect, disableUnattended
        var supersedable: Bool { self == .connect || self == .restore }
    }
    private let lock = NSLock()
    private let queue = DispatchQueue(label: "org.netgo.connection")
    private var generation: UInt64 = 0

    private func advance() -> UInt64 {
        lock.lock(); defer { lock.unlock() }
        generation &+= 1
        return generation
    }
    private func current(_ value: UInt64) -> Bool {
        lock.lock(); defer { lock.unlock() }
        return generation == value
    }
    func submit(_ intent: Intent, prepare: (() throws -> Void)? = nil,
                operation: @escaping () throws -> Void,
                completion: @escaping @MainActor (Error?) -> Void) {
        let version = advance()
        // Installation can wait for an OS prompt. It must never occupy the IPC
        // queue and delay a user's disconnect while that prompt remains open.
        if let prepare {
            DispatchQueue.global(qos: .userInitiated).async { [self] in
                guard current(version) else { return }
                do {
                    try prepare()
                    enqueue(intent, version: version, operation: operation, completion: completion)
                } catch { complete(version, error: error, completion: completion) }
            }
        } else { enqueue(intent, version: version, operation: operation, completion: completion) }
    }
    private func enqueue(_ intent: Intent, version: UInt64, operation: @escaping () throws -> Void,
                         completion: @escaping @MainActor (Error?) -> Void) {
        queue.async { [self] in
            guard !intent.supersedable || current(version) else { return }
            do { try operation(); complete(version, error: nil, completion: completion) }
            catch { complete(version, error: error, completion: completion) }
        }
    }
    private func complete(_ version: UInt64, error: Error?, completion: @escaping @MainActor (Error?) -> Void) {
        DispatchQueue.main.async { [self] in
            guard current(version) else { return }
            completion(error)
        }
    }
}
#endif

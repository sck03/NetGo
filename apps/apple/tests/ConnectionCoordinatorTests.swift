import Foundation

@main struct ConnectionCoordinatorTests {
    static func wait(_ signal: DispatchSemaphore) {
        precondition(signal.wait(timeout: .now() + 3) == .success, "connection worker stalled")
    }
    static func main() {
        let coordinator = DesktopConnectionCoordinator()
        let entered = DispatchSemaphore(value: 0), release = DispatchSemaphore(value: 0)
        let disconnected = DispatchSemaphore(value: 0)
        coordinator.submit(.restore, prepare: { entered.signal(); wait(release) }, operation: {
            preconditionFailure("cancelled restore reconnected")
        }, completion: { _ in preconditionFailure("stale restore completion") })
        wait(entered)
        coordinator.submit(.disconnect, operation: { disconnected.signal() }, completion: { _ in })
        wait(disconnected)
        release.signal()

        let inFlight = DispatchSemaphore(value: 0), unblock = DispatchSemaphore(value: 0)
        let orderedDisconnect = DispatchSemaphore(value: 0), connected = DispatchSemaphore(value: 0)
        coordinator.submit(.connect, operation: { inFlight.signal(); wait(unblock) }, completion: { _ in })
        wait(inFlight)
        coordinator.submit(.disconnect, operation: { orderedDisconnect.signal() }, completion: { _ in })
        coordinator.submit(.connect, operation: {
            precondition(orderedDisconnect.wait(timeout: .now()) == .success, "disconnect was skipped before reconnect")
            connected.signal()
        }, completion: { _ in })
        unblock.signal()
        wait(connected)
        print("PASS queued restore cancellation and disconnect ordering")
    }
}

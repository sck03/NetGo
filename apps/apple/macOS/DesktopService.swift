import Foundation

// Process ownership and retry policy stay off the UI thread. The service's
// per-user lock arbitrates startup if two app instances race to recover it.
final class DesktopService {
    private let queue = DispatchQueue(label: "org.netgo.service")
    private let helper: URL
    private let root: String
    private let socket: String
    private let failed: (String) -> Void
    private var timer: DispatchSourceTimer?
    private var process: Process?
    private var nextAttempt: TimeInterval = 0
    private var startedAt: TimeInterval = 0
    private var failures = 0

    init(helper: URL, root: String, socket: String, failed: @escaping (String) -> Void) {
        self.helper = helper; self.root = root; self.socket = socket; self.failed = failed
    }
    func start() {
        queue.async { [self] in
            guard timer == nil else { return }
            let timer = DispatchSource.makeTimerSource(queue: queue)
            timer.schedule(deadline: .now(), repeating: .seconds(1), leeway: .milliseconds(100))
            timer.setEventHandler { [weak self] in self?.check() }
            self.timer = timer
            timer.resume()
        }
    }
    func stop() {
        // Termination notification must complete before the app exits.
        queue.sync {
            timer?.cancel(); timer = nil
            if let process, process.isRunning { process.terminate() }
            process = nil
        }
    }
    private func check() {
        let now = ProcessInfo.processInfo.systemUptime
        if let process, process.isRunning {
            if now - startedAt >= 10 { failures = 0 }
            return
        }
        process = nil
        guard now >= nextAttempt else { return }
        // An existing live service belongs to its original owner.
        if (try? desktopIPC(socket, "GET", "/desktop/status", "", timeout: 500)) != nil {
            failures = 0; nextAttempt = now + 5
            return
        }
        nextAttempt = now + Double(1 << min(failures, 5))
        failures = min(failures + 1, 5)
        do {
            let child = Process()
            child.executableURL = helper
            child.arguments = ["--root", root, "--socket", socket, "--daemon", "/var/run/netgo/netbird.sock"]
            child.standardOutput = FileHandle.nullDevice; child.standardError = FileHandle.nullDevice
            try child.run()
            process = child; startedAt = now
        } catch { failed("连接服务启动失败，将自动重试：" + error.localizedDescription) }
    }
}

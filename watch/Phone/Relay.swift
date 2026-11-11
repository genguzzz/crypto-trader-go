// The phone side: it holds the owner key, talks to the ctg server and answers the watch.
//
//   watch ──sendMessage {op}──▶ Relay (this app, woken in the background by WatchConnectivity)
//                                 └─ Server (Api.swift) → https://ctg.gugenzzz.top/api/v1/… (X-CTG-Admin)
//   watch ◀── reply {snapshot | error} ──┘   also: application context, complication user info,
//                                            the app group copy for the widgets
//
// Requests are made here in Swift, not in a web view or JS runtime, so a message from the watch is
// answered even when the app was not running. The key never leaves the phone.
//
// Latency: the three reads run at the same time on one kept-alive connection; a snapshot younger than
// `fresh` answers the watch at once; concurrent asks share one fetch; an approve / reject is answered
// as soon as the server has, and the fresh state follows as a separate message.
import BackgroundTasks
import Foundation
import WatchConnectivity
import WidgetKit

final class Relay: NSObject, ObservableObject, WCSessionDelegate {
    static let shared = Relay()
    static let refreshTask = "top.gugenzzz.okxwatch.refresh"
    private static let fresh: TimeInterval = 4

    @Published var hasKey = !Keychain.read().isEmpty
    @Published var snapshot: Snapshot?
    @Published var lastError = ""
    @Published var watchState = "未连接"
    @Published var lastAsk = ""           // the last thing the watch asked for, and how long it took

    private let lock = NSLock()
    private var cached: (Snapshot, Date)?
    private var inflight: Task<Snapshot, Error>?

    func start() {
        if let (s, at) = SharedSnapshot.load() { cached = (s, at); snapshot = s }
        guard WCSession.isSupported() else { return }
        WCSession.default.delegate = self
        WCSession.default.activate()
    }

    func setKey(_ k: String) {
        Keychain.write(k.trimmingCharacters(in: .whitespacesAndNewlines))
        hasKey = !Keychain.read().isEmpty
    }

    // ---------- snapshot ----------

    /// The newest snapshot: the cached one when younger than `maxAge`, else one fetch shared by everyone asking.
    func snapshot(maxAge: TimeInterval) async throws -> Snapshot {
        lock.lock()
        if let (s, at) = cached, Date().timeIntervalSince(at) < maxAge { lock.unlock(); return s }
        let task: Task<Snapshot, Error>
        if let t = inflight {
            task = t
        } else {
            task = Task { try await Server.fetchSnapshot() }
            inflight = task
        }
        lock.unlock()
        do {
            let s = try await task.value
            finish(task, s)
            return s
        } catch {
            finish(task, nil)
            await MainActor.run { self.lastError = error.localizedDescription }
            throw error
        }
    }

    private func finish(_ task: Task<Snapshot, Error>, _ s: Snapshot?) {
        lock.lock()
        let first = inflight == task
        if first { inflight = nil }
        if let s, first { cached = (s, Date()) }
        lock.unlock()
        guard let s, first else { return }
        publish(s)
    }

    /// The UI, the widgets and the watch get a new snapshot.
    private func publish(_ s: Snapshot) {
        let before = snapshot
        DispatchQueue.main.async { self.snapshot = s; self.lastError = "" }
        SharedSnapshot.save(s)
        WidgetCenter.shared.reloadAllTimelines()
        let session = WCSession.default
        guard session.activationState == .activated, session.isPaired, session.isWatchAppInstalled else { return }
        let data = Wire.encode(s)
        try? session.updateApplicationContext([Wire.snapshot: data])
        // a change the watch face should show now: wakes the watch app in the background (limited daily budget)
        let changed = before.map { Set($0.pending.map(\.id)) != Set(s.pending.map(\.id)) || $0.positions.count != s.positions.count } ?? true
        if changed && session.isComplicationEnabled && session.remainingComplicationUserInfoTransfers > 0 {
            session.transferCurrentComplicationUserInfo([Wire.snapshot: data])
        }
    }

    /// Refresh now (the app came to the foreground, the user pulled, a background task).
    @discardableResult
    func refresh() async -> Snapshot? { try? await snapshot(maxAge: 0) }

    // ---------- background refresh ----------

    func registerBackground() {
        BGTaskScheduler.shared.register(forTaskWithIdentifier: Relay.refreshTask, using: nil) { task in
            self.scheduleBackground()
            let work = Task {
                let ok = await self.refresh() != nil
                task.setTaskCompleted(success: ok)
            }
            task.expirationHandler = { work.cancel() }
        }
    }

    func scheduleBackground() {
        let r = BGAppRefreshTaskRequest(identifier: Relay.refreshTask)
        r.earliestBeginDate = Date(timeIntervalSinceNow: 15 * 60)
        try? BGTaskScheduler.shared.submit(r)
    }

    // ---------- watch ----------

    func session(_ session: WCSession, activationDidCompleteWith state: WCSessionActivationState, error: Error?) {
        describe(session)
        Task { await refresh() }
    }
    func sessionDidBecomeInactive(_ session: WCSession) {}
    func sessionDidDeactivate(_ session: WCSession) { session.activate() }
    func sessionReachabilityDidChange(_ session: WCSession) { describe(session) }
    func sessionWatchStateDidChange(_ session: WCSession) { describe(session) }

    private func describe(_ s: WCSession) {
        let text = !s.isPaired ? "没有配对的手表" : !s.isWatchAppInstalled ? "手表上还没装 OKX 助手" : s.isReachable ? "手表已连接" : "手表 App 未在前台"
        DispatchQueue.main.async { self.watchState = text }
    }

    func session(_ session: WCSession, didReceiveMessage message: [String: Any], replyHandler: @escaping ([String: Any]) -> Void) {
        let op = message[Wire.op] as? String ?? ""
        let id = message[Wire.id] as? Int ?? 0
        let started = Date()
        Task {
            var reply: [String: Any] = [:]
            do {
                switch op {
                case "approve", "reject":
                    guard id > 0 else { throw ApiError(message: "缺少提议编号") }
                    reply[Wire.result] = try await Server.answer(id: id, approve: op == "approve")
                    // answer at once with the proposal gone; the fetched state follows as a message
                    if let (s, _) = lockedCache() {
                        var t = s
                        t.pending.removeAll { $0.id == id }
                        reply[Wire.snapshot] = Wire.encode(t)
                    }
                    Task { await self.pushFresh() }
                case "snapshot":
                    reply[Wire.snapshot] = Wire.encode(try await snapshot(maxAge: Relay.fresh))
                default:
                    throw ApiError(message: "未知请求 \(op)")
                }
            } catch {
                reply[Wire.error] = error.localizedDescription
                if reply[Wire.snapshot] == nil, let (s, _) = lockedCache() { reply[Wire.snapshot] = Wire.encode(s) }
            }
            replyHandler(reply)
            let ms = Int(Date().timeIntervalSince(started) * 1000)
            await MainActor.run {
                self.lastAsk = "\(op)\(id > 0 ? " #\(id)" : "") · \(ms) ms · \(clockText(Date().timeIntervalSince1970 * 1000))"
            }
        }
    }

    private func lockedCache() -> (Snapshot, Date)? {
        lock.lock()
        defer { lock.unlock() }
        return cached
    }

    /// Fetch and send to the watch right away when it is listening.
    private func pushFresh() async {
        guard let s = await refresh() else { return }
        let session = WCSession.default
        if session.isReachable { session.sendMessage([Wire.snapshot: Wire.encode(s)], replyHandler: nil, errorHandler: nil) }
    }
}

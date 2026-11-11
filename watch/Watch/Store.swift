// The watch side: it never talks to the server itself. It asks the phone (WatchConnectivity
// sendMessage, which wakes the companion app in the background) and shows the answer; when the
// phone cannot be reached it shows the last snapshot the phone left in the application context.
import Foundation
import WatchConnectivity
import WatchKit

final class Store: NSObject, ObservableObject, WCSessionDelegate {
    static let shared = Store()

    @Published var snapshot: Snapshot?
    @Published var receivedAt: Date?
    @Published var loading = false
    @Published var error = ""
    @Published var notice = ""          // the result of the last approve / reject
    @Published var answering: Int?      // the proposal being answered

    func start() {
        guard WCSession.isSupported() else { return }
        WCSession.default.delegate = self
        WCSession.default.activate()
    }

    /// Server time now, as far as the newest snapshot tells.
    var now: Double {
        guard let s = snapshot, let at = receivedAt else { return Date().timeIntervalSince1970 * 1000 }
        return s.ts + Date().timeIntervalSince(at) * 1000
    }

    private func take(_ s: Snapshot) {
        let before = Set(snapshot?.pending.map(\.id) ?? [])
        let fresh = s.pending.contains { !before.contains($0.id) }
        if snapshot != nil && fresh { WKInterfaceDevice.current().play(.notification) }
        snapshot = s
        receivedAt = Date()
    }

    private func send(_ msg: [String: Any], done: @escaping ([String: Any]?, String) -> Void) {
        let session = WCSession.default
        guard session.activationState == .activated else { done(nil, "正在连接手机…"); return }
        guard session.isReachable else { done(nil, "连不上手机（需要 iPhone 在附近并已解锁过）"); return }
        session.sendMessage(msg, replyHandler: { reply in
            DispatchQueue.main.async { done(reply, reply[Wire.error] as? String ?? "") }
        }, errorHandler: { err in
            DispatchQueue.main.async { done(nil, "手机没有应答：\(err.localizedDescription)") }
        })
    }

    func refresh() {
        guard !loading else { return }
        loading = true
        send([Wire.op: "snapshot"]) { reply, err in
            self.loading = false
            self.error = err
            if let s = Wire.decode(reply?[Wire.snapshot]) { self.take(s) }
        }
    }

    /// Approve or reject: the phone posts it; the server's verdict comes back with a fresh snapshot.
    func answer(_ p: Proposal, approve: Bool, done: @escaping (Bool) -> Void) {
        guard answering == nil else { return }
        answering = p.id
        send([Wire.op: approve ? "approve" : "reject", Wire.id: p.id]) { reply, err in
            self.answering = nil
            if let s = Wire.decode(reply?[Wire.snapshot]) { self.take(s) }
            let result = reply?[Wire.result] as? String ?? ""
            self.error = err
            self.notice = err.isEmpty ? "#\(p.id) \(result)" : ""
            WKInterfaceDevice.current().play(err.isEmpty ? .success : .failure)
            done(err.isEmpty)
        }
    }

    // ---------- WCSessionDelegate ----------

    func session(_ session: WCSession, activationDidCompleteWith state: WCSessionActivationState, error: Error?) {
        let ctx = session.receivedApplicationContext
        DispatchQueue.main.async {
            if self.snapshot == nil, let s = Wire.decode(ctx[Wire.snapshot]) { self.take(s) }
            self.refresh()
        }
    }

    func session(_ session: WCSession, didReceiveApplicationContext ctx: [String: Any]) {
        DispatchQueue.main.async {
            if let s = Wire.decode(ctx[Wire.snapshot]), s.ts >= (self.snapshot?.ts ?? 0) { self.take(s) }
        }
    }

    func sessionReachabilityDidChange(_ session: WCSession) {
        if session.isReachable { DispatchQueue.main.async { self.refresh() } }
    }
}

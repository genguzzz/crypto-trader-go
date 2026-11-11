// The watch side: it never talks to the server itself. It asks the phone (WatchConnectivity
// sendMessage, which wakes the companion app in the background) and shows the answer; when the
// phone cannot be reached it shows the last snapshot the phone left in the application context.
import Foundation
import WatchConnectivity
import WatchKit
import WidgetKit

final class Store: NSObject, ObservableObject, WCSessionDelegate {
    static let shared = Store()

    @Published var snapshot: Snapshot?
    @Published var receivedAt: Date?
    @Published var loading = false
    @Published var error = ""
    @Published var notice = ""          // the result of the last approve / reject
    @Published var answering: Int?      // the proposal being answered

    private var wantRefresh = false     // asked before the session was activated
    private var timer: Timer?
    private var demo = false

    func start() {
        #if DEBUG
        if ProcessInfo.processInfo.environment["OKX_DEMO"] != nil { demo = true; take(Store.demo); return }  // simulator screenshots
        #endif
        // the last state at once, while the phone is asked for a fresh one
        if let (s, at) = SharedSnapshot.load() { snapshot = s; receivedAt = at }
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
        let changed = snapshot != s
        snapshot = s
        receivedAt = Date()
        SharedSnapshot.save(s)
        if changed { WidgetCenter.shared.reloadAllTimelines() }
    }

    private func send(_ msg: [String: Any], done: @escaping ([String: Any]?, String) -> Void) {
        let session = WCSession.default
        guard session.isReachable else { done(nil, "连不上手机（需要 iPhone 在附近并已解锁过）"); return }
        session.sendMessage(msg, replyHandler: { reply in
            DispatchQueue.main.async { done(reply, reply[Wire.error] as? String ?? "") }
        }, errorHandler: { err in
            DispatchQueue.main.async { done(nil, "手机没有应答：\(err.localizedDescription)") }
        })
    }

    func refresh() {
        guard !demo, !loading else { return }
        guard WCSession.default.activationState == .activated else { wantRefresh = true; return }
        loading = true
        send([Wire.op: "snapshot"]) { reply, err in
            self.loading = false
            self.error = err
            if let s = Wire.decode(reply?[Wire.snapshot]) { self.take(s) }
        }
    }

    /// While the app is on screen it stays current by itself; it stops when the wrist drops.
    func setActive(_ active: Bool) {
        timer?.invalidate()
        timer = nil
        guard active else { return }
        refresh()
        timer = Timer.scheduledTimer(withTimeInterval: 15, repeats: true) { _ in self.refresh() }
    }

    /// Approve or reject: the phone posts it and answers with the server's verdict.
    func answer(_ p: Proposal, approve: Bool, done: @escaping (Bool) -> Void) {
        guard answering == nil else { return }
        answering = p.id
        notice = ""
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
            if let s = Wire.decode(ctx[Wire.snapshot]), s.ts > (self.snapshot?.ts ?? 0) { self.take(s) }
            if self.wantRefresh { self.wantRefresh = false; self.refresh() }
        }
    }

    private func incoming(_ d: [String: Any]) {
        DispatchQueue.main.async {
            if let s = Wire.decode(d[Wire.snapshot]), s.ts >= (self.snapshot?.ts ?? 0) { self.take(s) }
        }
    }

    func session(_ session: WCSession, didReceiveApplicationContext ctx: [String: Any]) { incoming(ctx) }
    func session(_ session: WCSession, didReceiveMessage message: [String: Any]) { incoming(message) }
    func session(_ session: WCSession, didReceiveUserInfo userInfo: [String: Any] = [:]) { incoming(userInfo) }

    func sessionReachabilityDidChange(_ session: WCSession) {
        if session.isReachable { DispatchQueue.main.async { self.refresh() } }
    }

    #if DEBUG
    /// Sample data for the simulator (run with SIMCTL_CHILD_OKX_DEMO=1).
    static var demo: Snapshot {
        let now = Date().timeIntervalSince1970 * 1000
        var s = Snapshot()
        s.ts = now
        s.mode = "live"
        s.pending = [Proposal(id: 12, kind: "open", instId: "ENA-USDT-SWAP", side: "long", source: "signal", trigger: "交易台 · 信号", marginMode: "isolated",
                              orderType: "limit", px: 0.412, marginUsdt: 30, leverage: 5, contracts: 364, notionalUsdt: 150, fraction: nil, refPx: 0.4205,
                              sl: 0.398, tp: 0.445, slPct: 3.4, tpPct: 8.0, rr: 2.36, rationale: "1 小时 +5.1%，量能 2.6 倍、OI +3.2%，回踩 0.412 企稳后做多。",
                              invalidation: "15m 收盘跌破 0.398", note: nil, tpsl: nil, expiresAt: now + 14 * 60_000, status: "pending", mode: "live", error: nil),
                     Proposal(id: 13, kind: "reduce", instId: "DOGE-USDT-SWAP", side: "short", source: "chat", trigger: nil, marginMode: "isolated",
                              orderType: "market", px: nil, marginUsdt: nil, leverage: nil, contracts: 750, notionalUsdt: 140, fraction: 0.5, refPx: 0.1872,
                              sl: nil, tp: nil, slPct: nil, tpPct: nil, rr: nil, rationale: "反弹到前高附近，先减一半锁定。", invalidation: nil, note: nil, tpsl: nil,
                              expiresAt: now + 2 * 60_000, status: "pending", mode: "live", error: nil),
                     Proposal(id: 14, kind: "open", instId: "PEPE-USDT-SWAP", side: "short", source: "signal", trigger: nil, marginMode: "isolated",
                              orderType: "market", px: nil, marginUsdt: 20, leverage: 3, contracts: 10, notionalUsdt: 60, fraction: nil, refPx: 0.00001234,
                              sl: 0.0000131, tp: 0.0000112, slPct: 6.2, tpPct: 9.2, rr: 1.48, rationale: "资金费率转正且 OI 背离。", invalidation: nil, note: nil, tpsl: nil,
                              expiresAt: now + 25 * 60_000, status: "pending", mode: "live", error: nil),
                     Proposal(id: 15, kind: "adjust", instId: "BTC-USDT-SWAP", side: "long", source: "chat", trigger: nil, marginMode: "cross",
                              orderType: nil, px: nil, marginUsdt: nil, leverage: nil, contracts: 0.12, notionalUsdt: nil, fraction: nil, refPx: 103480,
                              sl: 102500, tp: 107000, slPct: nil, tpPct: nil, rr: nil, rationale: "止损上移到保本上方，止盈放到前高。", invalidation: nil, note: nil,
                              tpsl: "止损 100800 → 102500，止盈 106000 → 107000", expiresAt: now + 10 * 60_000, status: "pending", mode: "live", error: nil),
                     Proposal(id: 16, kind: "cancel", instId: "ENA-USDT-SWAP", side: "long", source: "chat", trigger: nil, marginMode: "isolated",
                              orderType: "limit", px: 0.405, marginUsdt: nil, leverage: nil, contracts: 364, notionalUsdt: nil, fraction: nil, refPx: 0.4205,
                              sl: nil, tp: nil, slPct: nil, tpPct: nil, rr: nil, rationale: "回踩没到位，结构变了，撤单。", invalidation: nil, note: nil,
                              tpsl: "撤销限价买单 364 张 @0.405", expiresAt: now + 10 * 60_000, status: "pending", mode: "live", error: nil)]
        s.positionsEnabled = true
        s.positions = [Position(instId: "BTC-USDT-SWAP", side: "long", mgnMode: "cross", contracts: 0.12, notionalUsd: 12400, avgPx: 102350, markPx: 103480,
                                liqPx: 91200, lever: 8, upl: 135.6, uplRatio: 0.0875, margin: 1550, tp: [Trigger(px: 106000)], sl: [Trigger(px: 100800)]),
                       Position(instId: "DOGE-USDT-SWAP", side: "short", mgnMode: "isolated", contracts: 1500, notionalUsd: 277, avgPx: 0.1851, markPx: 0.1872,
                                liqPx: 0.2143, lever: 5, upl: -3.15, uplRatio: -0.0567, margin: 55, tp: nil, sl: [Trigger(px: 0.189)]),
                       Position(instId: "ETH-USDT-SWAP", side: "long", mgnMode: "cross", contracts: 2, notionalUsd: 7200, avgPx: 3512.4, markPx: 3540.1,
                                liqPx: nil, lever: 10, upl: 55.4, uplRatio: 0.077, margin: 720, tp: nil, sl: nil),
                       Position(instId: "SOL-USDT-SWAP", side: "short", mgnMode: "isolated", contracts: 30, notionalUsd: 4500, avgPx: 148.2, markPx: 149.9,
                                liqPx: 171.3, lever: 5, upl: -51, uplRatio: -0.057, margin: 900, tp: [Trigger(px: 139)], sl: [Trigger(px: 155)])]
        s.equity = Equity(totalEq: 2480.35, availEq: 870.2, upl: 132.45)
        s.desk = DeskStatus(enabled: true, running: nil,
                            lastRun: DeskRun(id: 7, label: "定时巡检", status: "done", ts: now - 50 * 60_000, decisions: 2, proposals: 1, summary: "ENA 回踩做多一笔（待审核），SOL 不做。"),
                            nextScheduleAt: now + 70 * 60_000, pending: 2, runs24h: 9, decisions24h: 14, lastError: nil)
        return s
    }
    #endif
}

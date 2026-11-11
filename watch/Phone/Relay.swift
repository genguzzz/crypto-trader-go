// The phone side: it holds the owner key, talks to the ctg server and answers the watch.
//
//   watch ──sendMessage {op}──▶ Relay (this app, woken in the background by WatchConnectivity)
//                                 └─ URLSession → https://ctg.gugenzzz.top/api/v1/… (X-CTG-Admin)
//   watch ◀── reply {snapshot | error} ──┘        also: updateApplicationContext(latest snapshot)
//
// Requests are made here in Swift, not in a web view or JS runtime, so a message from the watch is
// answered even when the app was not running. The key never leaves the phone.
import Foundation
import Security
import WatchConnectivity

enum Keychain {
    private static let service = "top.gugenzzz.okxwatch"
    private static let account = "ownerKey"

    static func read() -> String {
        let q: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                kSecAttrAccount as String: account, kSecReturnData as String: true]
        var out: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let d = out as? Data else { return "" }
        return String(data: d, encoding: .utf8) ?? ""
    }

    static func write(_ v: String) {
        let base: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                   kSecAttrAccount as String: account]
        SecItemDelete(base as CFDictionary)
        guard !v.isEmpty else { return }
        var add = base
        add[kSecValueData as String] = Data(v.utf8)
        // readable after the first unlock: the watch may ask while the phone is locked in a pocket
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(add as CFDictionary, nil)
    }
}

struct ApiError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

final class Relay: NSObject, ObservableObject, WCSessionDelegate {
    static let shared = Relay()
    static let origin = "https://ctg.gugenzzz.top"

    @Published var hasKey = !Keychain.read().isEmpty
    @Published var snapshot: Snapshot?
    @Published var lastError = ""
    @Published var watchState = "未连接"
    @Published var lastAsk = ""           // the last thing the watch asked for

    private let http: URLSession = {
        let c = URLSessionConfiguration.ephemeral
        c.timeoutIntervalForRequest = 15
        c.waitsForConnectivity = false
        return URLSession(configuration: c)
    }()

    func start() {
        guard WCSession.isSupported() else { return }
        WCSession.default.delegate = self
        WCSession.default.activate()
    }

    func setKey(_ k: String) {
        Keychain.write(k.trimmingCharacters(in: .whitespacesAndNewlines))
        hasKey = !Keychain.read().isEmpty
    }

    // ---------- server ----------

    private func call(_ path: String, method: String = "GET", body: [String: Any]? = nil) async throws -> Data {
        let key = Keychain.read()
        guard !key.isEmpty else { throw ApiError(message: "手机上还没有填访问密钥") }
        var r = URLRequest(url: URL(string: Relay.origin + path)!)
        r.httpMethod = method
        r.setValue(key, forHTTPHeaderField: "X-CTG-Admin")
        r.setValue("okxwatch/0.1", forHTTPHeaderField: "X-App-Version")
        if let body {
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
            r.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let (data, resp): (Data, URLResponse)
        do {
            (data, resp) = try await http.data(for: r)
        } catch {
            throw ApiError(message: "手机连不上服务器：\(error.localizedDescription)")
        }
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(code) else {
            let msg = ((try? JSONSerialization.jsonObject(with: data)) as? [String: Any])?["error"] as? String
            throw ApiError(message: code == 401 ? "访问密钥不对" : msg ?? "服务器返回 \(code)")
        }
        return data
    }

    private struct ProposalList: Codable { let proposals: [Proposal]; let now: Double; let mode: String }
    private struct Account: Codable { let enabled: Bool; let positions: [Position]?; let equity: Equity? }
    private struct Desk: Codable { let status: DeskStatus }

    /// One snapshot from three reads; a part that fails is reported in `problems`, the rest still shows.
    func fetchSnapshot() async throws -> Snapshot {
        var s = Snapshot()
        // the proposals decide whether the key and the network work at all: their error is the snapshot's
        let pl = try JSONDecoder().decode(ProposalList.self, from: try await call("/api/v1/realtrade/proposals?status=pending"))
        s.ts = pl.now
        s.mode = pl.mode
        s.pending = pl.proposals.filter { $0.status == "pending" }
        do {
            let a = try JSONDecoder().decode(Account.self, from: try await call("/api/v1/account/positions"))
            s.positionsEnabled = a.enabled
            s.positions = a.positions ?? []
            s.equity = a.equity
        } catch {
            s.problems.append("持仓：\(error.localizedDescription)")
        }
        do {
            s.desk = try JSONDecoder().decode(Desk.self, from: try await call("/api/v1/autopilot")).status
        } catch {
            s.problems.append("交易台：\(error.localizedDescription)")
        }
        return s
    }

    /// Refresh, publish to the UI and hand the watch the newest state for when it cannot ask.
    @discardableResult
    func refresh() async -> Snapshot? {
        do {
            let s = try await fetchSnapshot()
            await MainActor.run { self.snapshot = s; self.lastError = "" }
            push(s)
            return s
        } catch {
            await MainActor.run { self.lastError = error.localizedDescription }
            return nil
        }
    }

    private func push(_ s: Snapshot) {
        guard WCSession.default.activationState == .activated else { return }
        try? WCSession.default.updateApplicationContext([Wire.snapshot: Wire.encode(s)])
    }

    /// Approve or reject one proposal. The server re-validates and answers with the final state.
    func answer(id: Int, approve: Bool) async throws -> String {
        let body: [String: Any] = approve ? ["confirm": true, "via": "app"] : ["via": "app"]
        let data = try await call("/api/v1/realtrade/proposals/\(id)/\(approve ? "approve" : "reject")", method: "POST", body: body)
        let p = try? JSONDecoder().decode(Proposal.self, from: data)
        switch p?.status {
        case "executed": return p?.mode == "live" ? "已执行" : "已模拟执行（dry run）"
        case "rejected": return "已拒绝"
        case "failed": return "执行失败：\(p?.error ?? "")"
        case "invalidated": return "已作废：\(p?.error ?? "")"
        case let st?: return st
        default: return approve ? "已提交" : "已拒绝"
        }
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
        DispatchQueue.main.async { self.lastAsk = "\(op)\(id > 0 ? " #\(id)" : "") · \(clockText(Date().timeIntervalSince1970 * 1000))" }
        Task {
            var reply: [String: Any] = [:]
            do {
                switch op {
                case "approve", "reject":
                    guard id > 0 else { throw ApiError(message: "缺少提议编号") }
                    reply[Wire.result] = try await self.answer(id: id, approve: op == "approve")
                case "snapshot":
                    break
                default:
                    throw ApiError(message: "未知请求 \(op)")
                }
            } catch {
                reply[Wire.error] = error.localizedDescription
            }
            // whatever happened, the watch gets the state as it is now
            do {
                let s = try await self.fetchSnapshot()
                await MainActor.run { self.snapshot = s; self.lastError = "" }
                reply[Wire.snapshot] = Wire.encode(s)
            } catch {
                if reply[Wire.error] == nil { reply[Wire.error] = error.localizedDescription }
            }
            replyHandler(reply)
        }
    }
}

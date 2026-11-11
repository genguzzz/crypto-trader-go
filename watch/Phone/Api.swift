// The ctg server client, shared by the companion app and its home-screen widget. The owner key lives
// in the keychain under the app group, so the widget can read it too; it never goes to the watch.
import Foundation
import Security

enum Keychain {
    private static let service = "top.gugenzzz.okxwatch"
    private static let account = "ownerKey"

    private static func query(group: Bool) -> [String: Any] {
        var q: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                kSecAttrAccount as String: account]
        if group { q[kSecAttrAccessGroup as String] = SharedSnapshot.group }
        return q
    }

    static func read() -> String {
        var q = query(group: true)
        q[kSecReturnData as String] = true
        var out: AnyObject?
        if SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let d = out as? Data {
            return String(data: d, encoding: .utf8) ?? ""
        }
        // 0.1 kept the key in the app's own access group: move it to the shared one
        var old = query(group: false)
        old[kSecReturnData as String] = true
        guard SecItemCopyMatching(old as CFDictionary, &out) == errSecSuccess, let d = out as? Data,
              let v = String(data: d, encoding: .utf8), !v.isEmpty else { return "" }
        write(v)
        return v
    }

    static func write(_ v: String) {
        SecItemDelete(query(group: false) as CFDictionary)
        SecItemDelete(query(group: true) as CFDictionary)
        guard !v.isEmpty else { return }
        var add = query(group: true)
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

enum Server {
    static let origin = "https://ctg.gugenzzz.top"

    // one session for the process: its connection stays open between the watch's requests
    private static let http: URLSession = {
        let c = URLSessionConfiguration.default
        c.timeoutIntervalForRequest = 12
        c.waitsForConnectivity = false
        c.urlCache = nil
        c.httpMaximumConnectionsPerHost = 4
        return URLSession(configuration: c)
    }()

    static func call(_ path: String, method: String = "GET", body: [String: Any]? = nil) async throws -> Data {
        let key = Keychain.read()
        guard !key.isEmpty else { throw ApiError(message: "手机上还没有填访问密钥") }
        var r = URLRequest(url: URL(string: origin + path)!)
        r.httpMethod = method
        r.setValue(key, forHTTPHeaderField: "X-CTG-Admin")
        r.setValue("okxwatch/0.2", forHTTPHeaderField: "X-App-Version")
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

    private static func get<T: Decodable>(_ type: T.Type, _ path: String) async -> Result<T, Error> {
        do { return .success(try JSONDecoder().decode(T.self, from: try await call(path))) } catch { return .failure(error) }
    }

    /// One snapshot from three reads made at the same time; a part that fails is reported in
    /// `problems`, the rest still shows.
    static func fetchSnapshot() async throws -> Snapshot {
        async let pl = get(ProposalList.self, "/api/v1/realtrade/proposals?status=pending")
        async let acct = get(Account.self, "/api/v1/account/positions")
        async let desk = get(Desk.self, "/api/v1/autopilot")
        var s = Snapshot()
        // the proposals decide whether the key and the network work at all: their error is the snapshot's
        let p = try await pl.get()
        s.ts = p.now
        s.mode = p.mode
        s.pending = p.proposals.filter { $0.status == "pending" }
        switch await acct {
        case .success(let a):
            s.positionsEnabled = a.enabled
            s.positions = a.positions ?? []
            s.equity = a.equity
        case .failure(let e):
            s.problems.append("持仓：\(e.localizedDescription)")
        }
        switch await desk {
        case .success(let d): s.desk = d.status
        case .failure(let e): s.problems.append("交易台：\(e.localizedDescription)")
        }
        return s
    }

    /// Approve or reject one proposal. The server re-validates and answers with the final state.
    static func answer(id: Int, approve: Bool) async throws -> String {
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
}

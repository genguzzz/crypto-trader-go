// What the watch shows: one snapshot the phone builds from the ctg server (pending trade proposals,
// the owner's OKX positions, the trading desk's state). Field names follow the server's JSON
// (internal/realtrade, internal/okxacct, internal/autopilot); everything optional there is optional here.
import Foundation

struct Proposal: Codable, Identifiable, Hashable {
    let id: Int
    let kind: String            // open | add | reduce | close | adjust | cancel | amend
    let instId: String
    let side: String            // long | short
    let source: String?
    let trigger: String?
    let marginMode: String?
    let orderType: String?
    let px: Double?
    let marginUsdt: Double?
    let leverage: Int?
    let contracts: Double?
    let notionalUsdt: Double?
    let fraction: Double?
    let refPx: Double?
    let sl: Double?
    let tp: Double?
    let slPct: Double?
    let tpPct: Double?
    let rr: Double?
    let rationale: String?
    let invalidation: String?
    let note: String?
    let expiresAt: Double
    let status: String
    let mode: String?           // dryrun | live
    let error: String?
}

struct Trigger: Codable, Hashable {
    let px: Double
}

struct Position: Codable, Identifiable, Hashable {
    var id: String { instId + side }
    let instId: String
    let side: String
    let mgnMode: String?
    let contracts: Double
    let notionalUsd: Double?
    let avgPx: Double
    let markPx: Double?
    let liqPx: Double?
    let lever: Double?
    let upl: Double
    let uplRatio: Double
    let margin: Double?
    let tp: [Trigger]?
    let sl: [Trigger]?
}

struct Equity: Codable, Hashable {
    let totalEq: Double
    let availEq: Double?
    let upl: Double?
}

struct DeskRun: Codable, Hashable {
    let id: Int
    let label: String
    let status: String
    let ts: Double
    let decisions: Int?
    let proposals: Int?
    let summary: String?
}

struct DeskStatus: Codable, Hashable {
    let enabled: Bool
    let running: DeskRun?
    let lastRun: DeskRun?
    let nextScheduleAt: Double?
    let pending: Int?
    let runs24h: Int?
    let decisions24h: Int?
    let lastError: String?
}

/// Everything the watch shows, as of `ts` (server time, ms).
struct Snapshot: Codable, Hashable {
    var ts: Double = 0
    var mode: String = ""               // dryrun | live
    var pending: [Proposal] = []
    var positionsEnabled = false
    var positions: [Position] = []
    var equity: Equity?
    var desk: DeskStatus?               // nil: the server has no trading desk, or it could not be read
    var problems: [String] = []         // parts that could not be loaded
}

/// Messages between the watch and the phone (WatchConnectivity dictionaries).
enum Wire {
    static let op = "op"                // snapshot | approve | reject
    static let id = "id"
    static let snapshot = "snapshot"    // Data: JSON of Snapshot
    static let error = "error"          // String
    static let result = "result"        // String: what the server answered to approve / reject

    static func encode(_ s: Snapshot) -> Data { (try? JSONEncoder().encode(s)) ?? Data() }
    static func decode(_ d: Any?) -> Snapshot? {
        guard let d = d as? Data else { return nil }
        return try? JSONDecoder().decode(Snapshot.self, from: d)
    }
}

// ---------- formatting shared by both apps ----------

func coin(_ instId: String) -> String { instId.replacingOccurrences(of: "-USDT-SWAP", with: "") }

func px(_ v: Double?) -> String {
    guard let v, v != 0 else { return "—" }
    let a = abs(v)
    let digits = a >= 1000 ? 1 : a >= 100 ? 2 : a >= 1 ? 4 : a >= 0.01 ? 5 : 8
    let f = NumberFormatter()
    f.numberStyle = .decimal
    f.usesGroupingSeparator = false
    f.maximumFractionDigits = digits
    f.minimumFractionDigits = 0
    return f.string(from: NSNumber(value: v)) ?? "\(v)"
}

func usd(_ v: Double?, sign: Bool = false) -> String {
    guard let v else { return "—" }
    return String(format: sign ? "%+.2f" : "%.2f", v)
}

let kindText: [String: String] = ["open": "开仓", "add": "补仓", "reduce": "减仓", "close": "平仓", "adjust": "调整止盈止损",
                                  "cancel": "撤单", "amend": "改单"]

func sideText(_ s: String) -> String { s == "long" ? "多" : "空" }

/// "12 分钟" / "1 小时 5 分" until (or since) a server time in ms.
func minutesText(_ ms: Double, from now: Double) -> String {
    let m = Int((abs(ms - now) / 60_000).rounded())
    return m < 60 ? "\(m) 分钟" : m % 60 == 0 ? "\(m / 60) 小时" : "\(m / 60) 小时 \(m % 60) 分"
}

func clockText(_ ms: Double) -> String {
    let f = DateFormatter()
    f.dateFormat = "HH:mm"
    return f.string(from: Date(timeIntervalSince1970: ms / 1000))
}

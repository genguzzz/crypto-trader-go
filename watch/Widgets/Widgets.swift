// Widgets for the watch face / Smart Stack (watchOS) and the home and lock screens (iOS): pending
// proposals and the unrealized P&L. The watch widget shows the snapshot the watch app last received
// (the phone wakes it with complication user info when something changes); the iOS widget fetches
// its own with the shared owner key and falls back to the app's last copy.
import SwiftUI
import WidgetKit

let lime = Color(red: 0.737, green: 1.0, blue: 0.184)
let up = Color(red: 0.145, green: 0.655, blue: 0.314)
let down = Color(red: 0.792, green: 0.247, blue: 0.392)

struct Entry: TimelineEntry {
    let date: Date
    let snapshot: Snapshot?
    let receivedAt: Date?

    static let sample: Entry = {
        var s = Snapshot()
        s.ts = Date().timeIntervalSince1970 * 1000
        s.mode = "live"
        s.positionsEnabled = true
        s.positions = [Position(instId: "BTC-USDT-SWAP", side: "long", mgnMode: "cross", contracts: 0.12, notionalUsd: 12400, avgPx: 102350,
                                markPx: 103480, liqPx: nil, lever: 8, upl: 135.6, uplRatio: 0.0875, margin: 1550, tp: nil, sl: nil)]
        s.equity = Equity(totalEq: 2480.35, availEq: 870.2, upl: 135.6)
        return Entry(date: .now, snapshot: s, receivedAt: .now)
    }()
}

struct Provider: TimelineProvider {
    func placeholder(in context: Context) -> Entry { .sample }

    func getSnapshot(in context: Context, completion: @escaping (Entry) -> Void) {
        completion(context.isPreview ? .sample : stored())
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<Entry>) -> Void) {
        #if os(iOS)
        Task {
            var entry = stored()
            if let s = try? await Server.fetchSnapshot() {
                SharedSnapshot.save(s)
                entry = Entry(date: .now, snapshot: s, receivedAt: .now)
            }
            completion(Timeline(entries: [entry], policy: .after(.now.addingTimeInterval(15 * 60))))
        }
        #else
        completion(Timeline(entries: [stored()], policy: .after(.now.addingTimeInterval(30 * 60))))
        #endif
    }

    private func stored() -> Entry {
        let x = SharedSnapshot.load()
        return Entry(date: .now, snapshot: x?.0, receivedAt: x?.1)
    }
}

/// "+132.5" / "−1.2k"
func compact(_ v: Double, sign: Bool = true) -> String {
    let a = abs(v)
    let s = a >= 10_000 ? String(format: "%.0fk", a / 1000) : a >= 1000 ? String(format: "%.1fk", a / 1000) : a >= 100 ? String(format: "%.0f", a) : String(format: "%.1f", a)
    return (v < 0 ? "−" : sign ? "+" : "") + s
}

struct OKXWidgetView: View {
    @Environment(\.widgetFamily) private var family
    let entry: Entry

    private var pending: Int { entry.snapshot?.pending.count ?? 0 }
    private var upl: Double { entry.snapshot?.totalUpl ?? 0 }
    private var hasPositions: Bool { !(entry.snapshot?.positions.isEmpty ?? true) }
    private var uplColor: Color { upl >= 0 ? up : down }

    var body: some View {
        content
            .widgetURL(URL(string: pending > 0 ? "okxwatch://proposals" : "okxwatch://positions"))
            .containerBackground(for: .widget) { background }
    }

    @ViewBuilder private var background: some View {
        switch family {
        #if os(iOS)
        case .systemSmall, .systemMedium: Color.black
        #endif
        default: AccessoryWidgetBackground()
        }
    }

    @ViewBuilder private var content: some View {
        switch family {
        case .accessoryCircular: circular
        case .accessoryInline: Text(inline)
        case .accessoryRectangular: rectangular
        #if os(watchOS)
        case .accessoryCorner: corner
        #endif
        #if os(iOS)
        case .systemSmall: small
        case .systemMedium: medium
        #endif
        default: rectangular
        }
    }

    private var inline: String {
        guard entry.snapshot != nil else { return "OKX 无数据" }
        var s = pending > 0 ? "OKX 待审 \(pending)" : "OKX"
        if hasPositions { s += " · \(compact(upl)) U" }
        return s
    }

    private var circular: some View {
        VStack(spacing: 0) {
            if pending > 0 {
                Image(systemName: "bell.badge.fill").font(.system(size: 12)).foregroundStyle(lime)
                Text("\(pending)").font(.system(size: 20, weight: .bold, design: .rounded))
            } else {
                Text("OKX").font(.system(size: 10, weight: .semibold)).foregroundStyle(.secondary)
                Text(hasPositions ? compact(upl) : "—").font(.system(size: 15, weight: .bold, design: .rounded))
                    .foregroundStyle(hasPositions ? uplColor : .secondary).minimumScaleFactor(0.5)
            }
        }
        .widgetAccentable()
    }

    #if os(watchOS)
    private var corner: some View {
        Text(pending > 0 ? "\(pending)" : hasPositions ? compact(upl) : "—")
            .font(.system(size: 18, weight: .bold, design: .rounded))
            .foregroundStyle(pending > 0 ? lime : uplColor)
            .widgetLabel { Text(pending > 0 ? "待审核" : "未实现 U") }
    }
    #endif

    private var rectangular: some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(spacing: 4) {
                Text("OKX").font(.headline).widgetAccentable()
                if pending > 0 {
                    Text("\(pending) 待审核").font(.caption).fontWeight(.semibold).foregroundStyle(lime)
                }
                Spacer(minLength: 0)
            }
            if let s = entry.snapshot {
                if hasPositions {
                    Text("\(compact(upl)) U · \(s.positions.count) 仓").font(.system(.body, design: .rounded)).fontWeight(.semibold)
                        .foregroundStyle(uplColor)
                } else {
                    Text("没有持仓").font(.caption).foregroundStyle(.secondary)
                }
                if let p = s.pending.first {
                    Text("\(coin(p.instId)) \(kindText[p.kind] ?? p.kind)\(sideText(p.side))").font(.caption2).lineLimit(1)
                } else if let at = entry.receivedAt {
                    Text(at, style: .relative).font(.caption2).foregroundStyle(.secondary)
                }
            } else {
                Text("打开手表 App 获取").font(.caption2).foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    #if os(iOS)
    private var small: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("OKX").font(.caption).fontWeight(.bold).foregroundStyle(lime)
                Spacer()
                if pending > 0 {
                    Label("\(pending)", systemImage: "bell.badge.fill").font(.caption).fontWeight(.semibold).foregroundStyle(lime)
                }
            }
            Spacer(minLength: 0)
            if let e = entry.snapshot?.equity {
                Text("权益 U").font(.caption2).foregroundStyle(.secondary)
                Text(usd(e.totalEq)).font(.system(.title3, design: .rounded)).fontWeight(.semibold).foregroundStyle(.white)
                    .minimumScaleFactor(0.6).lineLimit(1)
            }
            Text(hasPositions ? "\(usd(upl, sign: true)) U" : "没有持仓").font(.footnote).fontWeight(.semibold)
                .foregroundStyle(hasPositions ? uplColor : .secondary).lineLimit(1).minimumScaleFactor(0.6)
            if let at = entry.receivedAt {
                Text(at, style: .time).font(.caption2).foregroundStyle(.secondary)
            }
        }
    }

    private var medium: some View {
        HStack(alignment: .top, spacing: 12) {
            small.frame(width: 120)
            VStack(alignment: .leading, spacing: 5) {
                if let p = entry.snapshot?.pending.first {
                    Text("待审核 #\(p.id) \(coin(p.instId)) \(kindText[p.kind] ?? p.kind)\(sideText(p.side))")
                        .font(.caption).fontWeight(.semibold).foregroundStyle(lime).lineLimit(1)
                }
                ForEach((entry.snapshot?.positions ?? []).prefix(3)) { p in
                    HStack {
                        Text(coin(p.instId)).font(.caption).fontWeight(.semibold).foregroundStyle(.white)
                        Text(sideText(p.side)).font(.caption2).foregroundStyle(p.side == "long" ? up : down)
                        Spacer()
                        Text(usd(p.upl, sign: true)).font(.caption).foregroundStyle(p.upl >= 0 ? up : down)
                    }
                }
                Spacer(minLength: 0)
            }
        }
    }
    #endif
}

@main
struct OKXWidgets: WidgetBundle {
    var body: some Widget { OKXWidget() }
}

struct OKXWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "OKXWidget", provider: Provider()) { OKXWidgetView(entry: $0) }
            .configurationDisplayName("OKX 助手")
            .description("待审核的提议和持仓的未实现盈亏")
            .supportedFamilies(families)
    }

    private var families: [WidgetFamily] {
        #if os(watchOS)
        [.accessoryCircular, .accessoryRectangular, .accessoryInline, .accessoryCorner]
        #else
        [.systemSmall, .systemMedium, .accessoryCircular, .accessoryRectangular, .accessoryInline]
        #endif
    }
}

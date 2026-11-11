// OKX 助手 on the wrist: three pages — proposals waiting for the owner (approve / reject), the open
// positions, and the trading desk. Approving is two taps on purpose: the detail, then a confirmation
// that names the order and says whether it is real.
import SwiftUI

let lime = Color(red: 0.737, green: 1.0, blue: 0.184)
let up = Color(red: 0.145, green: 0.655, blue: 0.314)
let down = Color(red: 0.792, green: 0.247, blue: 0.392)
let cyan = Color(red: 0.184, green: 0.878, blue: 1.0)

@main
struct WatchApp: App {
    @Environment(\.scenePhase) private var phase

    init() { Store.shared.start() }

    var body: some Scene {
        WindowGroup {
            RootView().environmentObject(Store.shared)
        }
        .onChange(of: phase) { _, p in Store.shared.setActive(p == .active) }
    }
}

enum Page: Hashable {
    case proposals, positions, desk

    var title: String {
        switch self {
        case .proposals: "待审核"
        case .positions: "持仓"
        case .desk: "交易台"
        }
    }

    /// okxwatch://proposals | positions | desk (the widgets open these)
    init?(url: URL) {
        switch url.host() {
        case "proposals": self = .proposals
        case "positions": self = .positions
        case "desk": self = .desk
        default: return nil
        }
    }
}

struct RootView: View {
    @EnvironmentObject var store: Store
    @State private var page = Page.proposals
    @State private var path = NavigationPath()

    var body: some View {
        // Pages side by side, each scrolling on its own: a vertical page view around scrolling lists
        // cannot tell a scroll from a page turn and stops between pages.
        NavigationStack(path: $path) {
            TabView(selection: $page) {
                ProposalsPage().tag(Page.proposals)
                PositionsPage().tag(Page.positions)
                DeskPage().tag(Page.desk)
            }
            .tabViewStyle(.page(indexDisplayMode: .always))
            .navigationTitle(page.title)
            .navigationBarTitleDisplayMode(.inline)
            .navigationDestination(for: Proposal.self) { ProposalDetail(p: $0) }
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button { store.refresh() } label: {
                        if store.loading { ProgressView() } else { Image(systemName: "arrow.clockwise") }
                    }
                    .disabled(store.loading)
                }
            }
        }
        .onOpenURL { url in
            guard let p = Page(url: url) else { return }
            path = NavigationPath()
            page = p
        }
        .onAppear {
            // with nothing to approve, open on the positions
            if store.snapshot.map({ $0.pending.isEmpty && !$0.positions.isEmpty }) == true { page = .positions }
        }
    }
}

/// The lines at the end of every page: how fresh the data is, or why there is none.
struct StatusLine: View {
    @EnvironmentObject var store: Store

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            if !store.notice.isEmpty { Text(store.notice).foregroundStyle(lime) }
            if !store.error.isEmpty { Text(store.error).foregroundStyle(.orange) }
            if let at = store.receivedAt {
                TimelineView(.periodic(from: .now, by: 15)) { ctx in
                    let age = Int(ctx.date.timeIntervalSince(at))
                    Text(store.loading ? "刷新中…" : age < 60 ? "刚刚更新" : "\(age / 60) 分钟前更新")
                        .foregroundStyle(age > 300 ? .orange : .secondary)
                }
            } else if store.error.isEmpty {
                Text(store.loading ? "正在向手机请求…" : "还没有数据").foregroundStyle(.secondary)
            }
            ForEach(store.snapshot?.problems ?? [], id: \.self) { Text($0).foregroundStyle(.orange) }
        }
        .font(.caption2)
        .frame(maxWidth: .infinity, alignment: .leading)
        .listRowBackground(Color.clear)
        .listRowInsets(EdgeInsets(top: 4, leading: 4, bottom: 0, trailing: 4))
    }
}

/// A small colored label: 开仓多, 实盘 …
struct Tag: View {
    let text: String
    let color: Color

    var body: some View {
        Text(text)
            .font(.system(size: 11, weight: .semibold))
            .foregroundStyle(color)
            .padding(.horizontal, 5)
            .padding(.vertical, 1)
            .background(color.opacity(0.18), in: Capsule())
            .lineLimit(1)
            .fixedSize()
    }
}

/// A page with nothing in it.
struct Empty: View {
    let text: String
    let icon: String

    var body: some View {
        VStack(spacing: 6) {
            Image(systemName: icon).font(.title2).foregroundStyle(.secondary)
            Text(text).font(.footnote).foregroundStyle(.secondary).multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 10)
        .listRowBackground(Color.clear)
    }
}

// ---------- proposals ----------

struct ProposalsPage: View {
    @EnvironmentObject var store: Store

    var body: some View {
        List {
            let pending = store.snapshot?.pending ?? []
            if store.snapshot != nil && pending.isEmpty {
                Empty(text: "没有待审核的提议", icon: "checkmark.circle")
            }
            ForEach(pending) { p in
                NavigationLink(value: p) { ProposalRow(p: p) }
            }
            StatusLine()
        }
    }
}

struct ProposalRow: View {
    @EnvironmentObject var store: Store
    let p: Proposal

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 4) {
                Text(coin(p.instId)).font(.system(.headline, design: .rounded)).lineLimit(1).minimumScaleFactor(0.7)
                Spacer(minLength: 2)
                Tag(text: p.mode == "live" ? "实盘" : "模拟", color: p.mode == "live" ? down : .orange)
            }
            Tag(text: (kindText[p.kind] ?? p.kind) + sideText(p.side), color: p.side == "long" ? up : down)
            Text(brief(p)).font(.caption2).foregroundStyle(.secondary).lineLimit(2).minimumScaleFactor(0.7)
            TimelineView(.periodic(from: .now, by: 20)) { _ in
                let left = p.expiresAt - store.now
                Label(left > 0 ? "还剩 \(minutesText(p.expiresAt, from: store.now))" : "已过期", systemImage: "timer")
                    .font(.caption2).foregroundStyle(left < 180_000 ? .orange : .secondary)
            }
        }
        .padding(.vertical, 2)
    }
}

/// One line under the coin: what the proposal does.
func brief(_ p: Proposal) -> String {
    let ord = p.orderType == "limit" ? "限 " + px(p.px) : "市价"
    switch p.kind {
    case "open", "add": return "\(usd(p.marginUsdt)) U × \(p.leverage ?? 0) · \(ord)"
    case "reduce": return "\(Int((p.fraction ?? 0) * 100))% · \(ord)"
    case "close": return "全部 · \(ord)"
    case "adjust": return "损 \(px(p.sl)) · 盈 \(px(p.tp))"
    default: return p.tpsl ?? ""
    }
}

struct ProposalDetail: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let p: Proposal
    @State private var confirming = false

    private var what: String {
        var s = "#\(p.id) \(coin(p.instId)) \(kindText[p.kind] ?? p.kind)\(sideText(p.side))"
        switch p.kind {
        case "open", "add": s += " \(usd(p.marginUsdt)) U × \(p.leverage ?? 0)"
        case "reduce": if let f = p.fraction { s += " \(Int(f * 100))%" }
        case "close": s += " 全部\(p.contracts.map { " \(px($0)) 张" } ?? "")"
        case "adjust": s += " 止损 \(px(p.sl)) / 止盈 \(px(p.tp))"
        default: break
        }
        if let t = p.tpsl, !t.isEmpty, p.kind == "cancel" || p.kind == "amend" { s += "\n" + t }
        return s
    }

    var body: some View {
        let live = p.mode == "live"
        let busy = store.answering == p.id
        List {
            Section {
                VStack(spacing: 5) {
                    row("操作", (kindText[p.kind] ?? p.kind) + sideText(p.side), p.side == "long" ? up : down)
                    if p.kind == "open" || p.kind == "add" {
                        row("保证金", "\(usd(p.marginUsdt)) U × \(p.leverage ?? 0)")
                        row("名义", "\(usd(p.notionalUsdt)) U")
                    }
                    if p.kind == "reduce", let f = p.fraction { row("比例", "\(Int(f * 100))%") }
                    if p.kind == "close" || p.kind == "reduce" || p.kind == "cancel", let c = p.contracts, c > 0 { row("张数", px(c)) }
                    row(p.orderType == "limit" ? "限价" : "参考价", px(p.orderType == "limit" ? p.px : p.refPx))
                    if (p.sl ?? 0) > 0 { row("止损", "\(px(p.sl)) −\(String(format: "%.1f", p.slPct ?? 0))%", down) }
                    if (p.tp ?? 0) > 0 { row("止盈", "\(px(p.tp)) +\(String(format: "%.1f", p.tpPct ?? 0))%", up) }
                    if let rr = p.rr, rr > 0 { row("盈亏比", String(format: "%.2f", rr)) }
                    row("模式", live ? "实盘 LIVE" : "模拟 DRY RUN", live ? down : .orange)
                    TimelineView(.periodic(from: .now, by: 20)) { _ in
                        let left = p.expiresAt - store.now
                        row("剩余", left > 0 ? minutesText(p.expiresAt, from: store.now) : "已过期", left < 180_000 ? .orange : .primary)
                    }
                }
                .padding(.vertical, 2)
            } header: {
                Text("#\(p.id) \(coin(p.instId))")
            }
            if let t = p.tpsl, !t.isEmpty {
                Section(p.kind == "cancel" || p.kind == "amend" ? "委托" : "止盈止损") { Text(t).font(.footnote) }
            }
            if let r = p.rationale, !r.isEmpty {
                Section("理由") { Text(r).font(.footnote) }
            }
            if let i = p.invalidation, !i.isEmpty {
                Section("失效条件") { Text(i).font(.footnote) }
            }
            if let n = p.note, !n.isEmpty {
                Section { Text(n).font(.footnote).foregroundStyle(.orange) }
            }
            Section {
                Button { confirming = true } label: {
                    Label(busy ? "提交中…" : "同意", systemImage: "checkmark").frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent).tint(up).disabled(busy)
                Button(role: .destructive) {
                    store.answer(p, approve: false) { ok in if ok { dismiss() } }
                } label: {
                    Label("拒绝", systemImage: "xmark").frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered).disabled(busy)
                if !store.error.isEmpty { Text(store.error).font(.footnote).foregroundStyle(.orange) }
            }
            .listRowBackground(Color.clear)
            .listRowInsets(EdgeInsets())
        }
        .navigationTitle(coin(p.instId))
        .confirmationDialog(live ? "实盘执行这笔交易？" : "模拟执行（dry run）？", isPresented: $confirming, titleVisibility: .visible) {
            Button(live ? "确认（实盘）" : "确认") {
                store.answer(p, approve: true) { ok in if ok { dismiss() } }
            }
            Button("取消", role: .cancel) {}
        } message: {
            Text(what + (live ? "\n会在 OKX 真实成交。" : "\n只记录和模拟，不会真实下单。"))
        }
    }

    private func row(_ label: String, _ value: String, _ color: Color = .primary) -> some View {
        HStack {
            Text(label).foregroundStyle(.secondary).fixedSize()
            Spacer(minLength: 4)
            Text(value).foregroundStyle(color).multilineTextAlignment(.trailing).lineLimit(1).minimumScaleFactor(0.6)
        }
        .font(.caption)
    }
}

// ---------- positions ----------

struct PositionsPage: View {
    @EnvironmentObject var store: Store

    var body: some View {
        List {
            if let s = store.snapshot {
                if let e = s.equity {
                    VStack(alignment: .leading, spacing: 1) {
                        Text("账户权益 U").font(.caption2).foregroundStyle(.secondary)
                        Text(usd(e.totalEq)).font(.system(.title3, design: .rounded)).fontWeight(.semibold)
                            .lineLimit(1).minimumScaleFactor(0.6)
                        let u = s.totalUpl
                        Text("未实现 \(usd(u, sign: true))").font(.caption2).foregroundStyle(u >= 0 ? up : down)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                if !s.positionsEnabled {
                    Text("服务器还没有接入 OKX 账户（只读）").font(.footnote).foregroundStyle(.secondary)
                } else if s.positions.isEmpty {
                    Empty(text: "没有持仓", icon: "tray")
                }
                ForEach(s.positions) { PositionRow(p: $0) }
            }
            StatusLine()
        }
    }
}

struct PositionRow: View {
    let p: Position

    var body: some View {
        let color = p.upl >= 0 ? up : down
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 4) {
                Text(coin(p.instId)).font(.system(.headline, design: .rounded)).lineLimit(1).minimumScaleFactor(0.7)
                Tag(text: sideText(p.side) + (p.lever.map { " \(Int($0))×" } ?? ""), color: p.side == "long" ? up : down)
                Spacer(minLength: 0)
            }
            HStack(alignment: .firstTextBaseline) {
                Text(usd(p.upl, sign: true)).font(.system(.body, design: .rounded)).fontWeight(.semibold).foregroundStyle(color)
                    .lineLimit(1).minimumScaleFactor(0.7)
                Spacer(minLength: 2)
                Text(String(format: "%+.1f%%", p.uplRatio * 100)).font(.caption2).foregroundStyle(color)
            }
            Text("\(px(p.avgPx)) → \(px(p.markPx))").font(.caption2).foregroundStyle(.secondary).lineLimit(1).minimumScaleFactor(0.7)
            if p.sl?.first != nil || p.tp?.first != nil || (p.liqPx ?? 0) > 0 {
                VStack(alignment: .leading, spacing: 0) {
                    HStack(spacing: 6) {
                        if let sl = p.sl?.first { Text("损 \(px(sl.px))").foregroundStyle(down) }
                        if let tp = p.tp?.first { Text("盈 \(px(tp.px))").foregroundStyle(up) }
                    }
                    if let l = p.liqPx, l > 0 { Text("强平 \(px(l))").foregroundStyle(.orange) }
                }
                .font(.system(size: 11)).lineLimit(1).minimumScaleFactor(0.7)
            }
        }
        .padding(.vertical, 2)
    }
}

// ---------- trading desk ----------

struct DeskPage: View {
    @EnvironmentObject var store: Store

    var body: some View {
        List {
            if let d = store.snapshot?.desk {
                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: 6) {
                        Circle().fill(!d.enabled ? .gray : d.running != nil ? lime : cyan).frame(width: 8, height: 8)
                        Text(!d.enabled ? "未启用" : d.running != nil ? "运行中" : "待命").font(.headline)
                    }
                    if let r = d.running { Text("#\(r.id) \(r.label)").font(.caption2).foregroundStyle(lime).lineLimit(2) }
                    if d.enabled, let next = d.nextScheduleAt, next > 0 {
                        metric("下次巡检", "\(clockText(next)) · \(minutesText(next, from: store.now))后")
                    }
                    if d.enabled {
                        HStack(alignment: .top) {
                            metric("待处理信号", "\(d.pending ?? 0)")
                            Spacer()
                            metric("24h 轮/决策", "\(d.runs24h ?? 0) / \(d.decisions24h ?? 0)")
                        }
                    }
                }
                if let e = d.lastError, !e.isEmpty { Text(e).font(.caption2).foregroundStyle(.orange) }
                if let r = d.lastRun {
                    Section {
                        VStack(alignment: .leading, spacing: 3) {
                            Text("\(clockText(r.ts)) · \(r.status == "done" ? "完成" : "未完成") · \(r.decisions ?? 0) 决策 · \(r.proposals ?? 0) 提议")
                                .font(.caption2).foregroundStyle(.secondary)
                            if let s = r.summary, !s.isEmpty { Text(s).font(.footnote) }
                        }
                    } header: {
                        Text("上一轮 #\(r.id) \(r.label)").lineLimit(1)
                    }
                }
            } else if store.snapshot != nil {
                Empty(text: "服务器上还没有交易台", icon: "cpu")
            }
            StatusLine()
        }
    }

    private func metric(_ label: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(label).font(.caption2).foregroundStyle(.secondary)
            Text(value).font(.footnote).lineLimit(1).minimumScaleFactor(0.7)
        }
    }
}

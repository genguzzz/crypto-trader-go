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
        .onChange(of: phase) { _, p in
            if p == .active { Store.shared.refresh() }
        }
    }
}

struct RootView: View {
    @EnvironmentObject var store: Store

    var body: some View {
        // one navigation stack around the pages (a stack per page inside a vertical TabView renders nothing)
        NavigationStack {
            TabView {
                ProposalsPage()
                PositionsPage()
                DeskPage()
            }
            .tabViewStyle(.verticalPage)
            .navigationDestination(for: Proposal.self) { ProposalDetail(p: $0) }
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button { store.refresh() } label: { Image(systemName: "arrow.clockwise") }.disabled(store.loading)
                }
            }
        }
    }
}

/// The line under every page: how fresh the data is, or why there is none.
struct StatusLine: View {
    @EnvironmentObject var store: Store

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            if !store.notice.isEmpty { Text(store.notice).foregroundStyle(lime) }
            if !store.error.isEmpty { Text(store.error).foregroundStyle(.orange) }
            if let at = store.receivedAt {
                TimelineView(.periodic(from: .now, by: 30)) { _ in
                    let age = Int(Date().timeIntervalSince(at))
                    Text(store.loading ? "刷新中…" : age < 60 ? "刚刚更新" : "\(age / 60) 分钟前更新")
                        .foregroundStyle(age > 300 ? .orange : .secondary)
                }
            } else if store.error.isEmpty {
                Text(store.loading ? "正在向手机请求…" : "还没有数据").foregroundStyle(.secondary)
            }
            ForEach(store.snapshot?.problems ?? [], id: \.self) { Text($0).foregroundStyle(.orange) }
        }
        .font(.footnote)
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
                Label("没有待审核的提议", systemImage: "checkmark.circle").foregroundStyle(.secondary)
            }
            ForEach(pending) { p in
                NavigationLink(value: p) { ProposalRow(p: p) }
            }
            StatusLine()
        }
        .navigationTitle("待审核")
    }
}

struct ProposalRow: View {
    @EnvironmentObject var store: Store
    let p: Proposal

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 5) {
                Text(coin(p.instId)).font(.headline)
                Text((kindText[p.kind] ?? p.kind) + sideText(p.side))
                    .font(.caption).fontWeight(.semibold)
                    .foregroundStyle(p.side == "long" ? up : down)
                Spacer()
                Text(p.mode == "live" ? "实盘" : "模拟").font(.caption2).foregroundStyle(p.mode == "live" ? down : .orange)
            }
            if p.kind == "open" || p.kind == "add" {
                Text("\(usd(p.marginUsdt)) U × \(p.leverage ?? 0) · \(p.orderType == "limit" ? "限价 " + px(p.px) : "市价")")
                    .font(.caption).foregroundStyle(.secondary)
            }
            TimelineView(.periodic(from: .now, by: 20)) { _ in
                let left = p.expiresAt - store.now
                Text(left > 0 ? "还剩 \(minutesText(p.expiresAt, from: store.now))" : "已过期")
                    .font(.caption2).foregroundStyle(left < 180_000 ? .orange : .secondary)
            }
        }
    }
}

struct ProposalDetail: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let p: Proposal
    @State private var confirming = false

    private var what: String {
        var s = "#\(p.id) \(coin(p.instId)) \(kindText[p.kind] ?? p.kind)\(sideText(p.side))"
        if p.kind == "open" || p.kind == "add" { s += " \(usd(p.marginUsdt)) U × \(p.leverage ?? 0)" }
        if p.kind == "reduce", let f = p.fraction { s += " \(Int(f * 100))%" }
        return s
    }

    var body: some View {
        let live = p.mode == "live"
        let busy = store.answering == p.id
        List {
            Section {
                row("操作", (kindText[p.kind] ?? p.kind) + sideText(p.side), p.side == "long" ? up : down)
                if p.kind == "open" || p.kind == "add" {
                    row("保证金", "\(usd(p.marginUsdt)) U × \(p.leverage ?? 0)")
                    row("名义", "\(usd(p.notionalUsdt)) U")
                }
                if p.kind == "reduce", let f = p.fraction { row("比例", "\(Int(f * 100))%") }
                row(p.orderType == "limit" ? "限价" : "参考价", px(p.orderType == "limit" ? p.px : p.refPx))
                if (p.sl ?? 0) > 0 { row("止损", "\(px(p.sl))  −\(String(format: "%.1f", p.slPct ?? 0))%", down) }
                if (p.tp ?? 0) > 0 { row("止盈", "\(px(p.tp))  +\(String(format: "%.1f", p.tpPct ?? 0))%", up) }
                if let rr = p.rr, rr > 0 { row("盈亏比", String(format: "%.2f", rr)) }
                row("模式", live ? "实盘 LIVE" : "模拟 DRY RUN", live ? down : .orange)
            } header: {
                Text("#\(p.id) \(coin(p.instId))")
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
                .tint(up).disabled(busy)
                Button(role: .destructive) {
                    store.answer(p, approve: false) { ok in if ok { dismiss() } }
                } label: {
                    Label("拒绝", systemImage: "xmark").frame(maxWidth: .infinity)
                }
                .disabled(busy)
                if !store.error.isEmpty { Text(store.error).font(.footnote).foregroundStyle(.orange) }
            }
        }
        .navigationTitle(coin(p.instId))
        .confirmationDialog(live ? "实盘执行这笔交易？" : "模拟执行（dry run）？", isPresented: $confirming, titleVisibility: .visible) {
            Button(live ? "确认下单" : "确认") {
                store.answer(p, approve: true) { ok in if ok { dismiss() } }
            }
            Button("取消", role: .cancel) {}
        } message: {
            Text(what + (live ? "\n会在 OKX 真实成交。" : "\n只记录和模拟，不会真实下单。"))
        }
    }

    private func row(_ label: String, _ value: String, _ color: Color = .primary) -> some View {
        HStack {
            Text(label).foregroundStyle(.secondary)
            Spacer()
            Text(value).foregroundStyle(color).multilineTextAlignment(.trailing)
        }
        .font(.footnote)
    }
}

// ---------- positions ----------

struct PositionsPage: View {
    @EnvironmentObject var store: Store

    var body: some View {
        List {
            if let s = store.snapshot {
                if let e = s.equity {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("账户权益").font(.caption2).foregroundStyle(.secondary)
                        Text("\(usd(e.totalEq)) U").font(.title3).fontWeight(.semibold)
                        if let u = e.upl {
                            Text("未实现 \(usd(u, sign: true)) U").font(.caption).foregroundStyle(u >= 0 ? up : down)
                        }
                    }
                }
                if !s.positionsEnabled {
                    Text("服务器还没有接入 OKX 账户（只读）").font(.footnote).foregroundStyle(.secondary)
                } else if s.positions.isEmpty {
                    Label("没有持仓", systemImage: "tray").foregroundStyle(.secondary)
                }
                ForEach(s.positions) { PositionRow(p: $0) }
            }
            StatusLine()
        }
        .navigationTitle("持仓")
    }
}

struct PositionRow: View {
    let p: Position

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 5) {
                Text(coin(p.instId)).font(.headline)
                Text(sideText(p.side) + (p.lever.map { " \(Int($0))×" } ?? ""))
                    .font(.caption).fontWeight(.semibold).foregroundStyle(p.side == "long" ? up : down)
                Spacer()
                Text(String(format: "%+.1f%%", p.uplRatio * 100)).font(.caption).foregroundStyle(p.upl >= 0 ? up : down)
            }
            Text("\(usd(p.upl, sign: true)) U").font(.body).fontWeight(.semibold).foregroundStyle(p.upl >= 0 ? up : down)
            Text("均价 \(px(p.avgPx)) → \(px(p.markPx))").font(.caption2).foregroundStyle(.secondary)
            HStack(spacing: 8) {
                if let sl = p.sl?.first { Text("损 \(px(sl.px))").foregroundStyle(down) }
                if let tp = p.tp?.first { Text("盈 \(px(tp.px))").foregroundStyle(up) }
                if let l = p.liqPx, l > 0 { Text("强平 \(px(l))").foregroundStyle(.orange) }
            }
            .font(.caption2)
        }
    }
}

// ---------- trading desk ----------

struct DeskPage: View {
    @EnvironmentObject var store: Store

    var body: some View {
        List {
            if let d = store.snapshot?.desk {
                HStack {
                    Circle().fill(d.enabled ? cyan : .gray).frame(width: 8, height: 8)
                    Text(!d.enabled ? "未启用" : d.running != nil ? "运行中" : "待命").font(.headline)
                }
                if let r = d.running { Text("#\(r.id) \(r.label)").font(.footnote).foregroundStyle(cyan) }
                if d.enabled, let next = d.nextScheduleAt, next > 0 {
                    row("下次巡检", "\(clockText(next))（\(minutesText(next, from: store.now))后）")
                }
                if d.enabled {
                    row("待处理信号", "\(d.pending ?? 0) 条")
                    row("24 小时", "\(d.runs24h ?? 0) 轮 · \(d.decisions24h ?? 0) 笔决策")
                }
                if let e = d.lastError, !e.isEmpty { Text(e).font(.footnote).foregroundStyle(.orange) }
                if let r = d.lastRun {
                    Section("上一轮 #\(r.id) \(r.label)") {
                        Text("\(clockText(r.ts)) · \(r.status == "done" ? "完成" : "未完成") · \(r.decisions ?? 0) 笔决策 · \(r.proposals ?? 0) 笔提议")
                            .font(.caption2).foregroundStyle(.secondary)
                        if let s = r.summary, !s.isEmpty { Text(s).font(.footnote) }
                    }
                }
            } else if store.snapshot != nil {
                Text("服务器上还没有交易台").font(.footnote).foregroundStyle(.secondary)
            }
            StatusLine()
        }
        .navigationTitle("交易台")
    }

    private func row(_ label: String, _ value: String) -> some View {
        HStack {
            Text(label).foregroundStyle(.secondary)
            Spacer()
            Text(value)
        }
        .font(.footnote)
    }
}

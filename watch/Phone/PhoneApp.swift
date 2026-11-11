// The companion app is deliberately small: enter the owner key once, see that the relay works.
// Everything else happens on the watch.
import SwiftUI

@main
struct PhoneApp: App {
    @Environment(\.scenePhase) private var phase

    init() {
        Relay.shared.registerBackground()
        Relay.shared.start()
    }

    var body: some Scene {
        WindowGroup {
            HomeView()
                .environmentObject(Relay.shared)
                .preferredColorScheme(.dark)
        }
        .onChange(of: phase) { _, p in
            if p == .active { Task { await Relay.shared.refresh() } }
            if p == .background { Relay.shared.scheduleBackground() }
        }
    }
}

struct HomeView: View {
    @EnvironmentObject var relay: Relay
    @State private var key = ""
    @State private var busy = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    LabeledContent("手表", value: relay.watchState)
                    if !relay.lastAsk.isEmpty { LabeledContent("手表上次请求", value: relay.lastAsk) }
                } header: {
                    Text("连接")
                } footer: {
                    Text("手表通过这台手机请求服务器：这个 App 不需要开着，手表发来请求时系统会在后台唤醒它。新提议的推送走 Bark。")
                }

                Section {
                    if relay.hasKey {
                        LabeledContent("访问密钥", value: "已保存在钥匙串")
                        Button("更换密钥", role: .destructive) { relay.setKey("") }
                    } else {
                        SecureField("所有者访问密钥（CTG_ADMIN_KEY）", text: $key)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                        Button("保存并测试") { save() }.disabled(key.trimmingCharacters(in: .whitespaces).isEmpty || busy)
                    }
                } header: {
                    Text("服务器 \(Server.origin.replacingOccurrences(of: "https://", with: ""))")
                } footer: {
                    Text("密钥只保存在这台手机的钥匙串里，不会发到手表。")
                }

                if !relay.lastError.isEmpty {
                    Section { Text(relay.lastError).foregroundStyle(.orange) }
                }

                if let s = relay.snapshot {
                    Section("现在（手表看到的内容）") {
                        LabeledContent("交易模式", value: s.mode == "live" ? "实盘 LIVE" : "模拟 DRY RUN")
                        LabeledContent("待审核提议", value: "\(s.pending.count) 笔")
                        LabeledContent("持仓", value: s.positionsEnabled ? "\(s.positions.count) 个" : "未接入")
                        if let e = s.equity { LabeledContent("账户权益", value: "\(usd(e.totalEq)) U") }
                        if let d = s.desk {
                            LabeledContent("交易台", value: !d.enabled ? "未启用" : d.running != nil ? "运行中" : "待命")
                        }
                        ForEach(s.problems, id: \.self) { Text($0).font(.footnote).foregroundStyle(.orange) }
                    }
                }

                Section {
                    Button(busy ? "刷新中…" : "立即刷新") {
                        busy = true
                        Task { await relay.refresh(); busy = false }
                    }.disabled(busy || !relay.hasKey)
                }
            }
            .navigationTitle("OKX 手表助手")
        }
    }

    private func save() {
        busy = true
        relay.setKey(key)
        key = ""
        Task {
            let ok = await relay.refresh() != nil
            if !ok && relay.lastError.contains("密钥") { relay.setKey("") }  // a wrong key is not kept
            busy = false
        }
    }
}

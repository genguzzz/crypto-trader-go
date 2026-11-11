// 交易: the web paper-trading area (web views/trading/*). A segmented sub-navigation like the web
// TradingLayout — 账户 · 策略库 · 待确认 (badge = open confirmations) · 信号 (the AI's signal
// subscriptions, count = active) · AI 决策 — over one pane each;
// account, backtest report, new account and strategy pages are pushed screens (app/trading/*).
// `?pane=` selects a pane from elsewhere (trade card → AI 决策).
import { Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { haptic } from "@/lib/device";
import { useOpenPrompts } from "@/lib/tradeLive";
import { colors, fs } from "@/lib/theme";
import { AccountsPane } from "@/components/trade/AccountsPane";
import { AiPane } from "@/components/trade/AiPane";
import { PromptsPane } from "@/components/trade/PromptsPane";
import { SignalsPane } from "@/components/trade/SignalsPane";
import { StrategiesPane } from "@/components/trade/StrategiesPane";
import { IconButton, Screen, TabHeader } from "@/components/ui";
import { usePendingProposals } from "@/lib/realtrade";
import { useActiveSignalCount } from "@/lib/signals";

type Pane = "accounts" | "strategies" | "prompts" | "signals" | "ai";
const PANES: { value: Pane; label: string; icon: keyof typeof Ionicons.glyphMap }[] = [
  { value: "accounts", label: "账户", icon: "wallet-outline" },
  { value: "strategies", label: "策略库", icon: "library-outline" },
  { value: "prompts", label: "待确认", icon: "file-tray-outline" },
  { value: "signals", label: "信号", icon: "flash-outline" },
  { value: "ai", label: "AI 决策", icon: "git-network-outline" },
];

export default function TradeTab() {
  const router = useRouter();
  const params = useLocalSearchParams<{ pane?: string }>();
  const pane: Pane = PANES.some((p) => p.value === params.pane) ? (params.pane as Pane) : "accounts";
  const prompts = useOpenPrompts() + usePendingProposals().length;
  const signals = useActiveSignalCount();

  return (
    <Screen>
      <TabHeader
        title="交易"
        sub={pane === "prompts" ? "模拟交易信号与 AI 实盘交易提议，在这里审核" : pane === "signals" ? "AI 在等的条件：成立时唤醒 AI 重新评估，交易仍需你审核" : "模拟交易：不连接真实账户、不下真实订单"}
        right={pane === "accounts" ? <IconButton name="add-circle-outline" size={26} color={colors.primary} label="新建账户" onPress={() => router.push("/trading/new")} /> : undefined}
      />
      <View style={{ flexDirection: "row", marginHorizontal: 12, marginBottom: 4, padding: 2, gap: 2, borderRadius: 8, borderWidth: 1, borderColor: colors.border, backgroundColor: colors.bg }}>
        {PANES.map((p) => {
          const on = p.value === pane;
          const badge = p.value === "prompts" ? prompts : 0;
          const count = p.value === "signals" ? signals : 0;
          return (
            <Pressable
              key={p.value}
              accessibilityRole="tab"
              accessibilityState={{ selected: on }}
              onPress={() => { if (!on) { void haptic("select"); router.setParams({ pane: p.value }); } }}
              style={{ flex: 1, height: 32, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 4, borderRadius: 6, backgroundColor: on ? colors.cardMuted : "transparent" }}
            >
              {badge || count ? null : <Ionicons name={p.icon} size={14} color={on ? colors.primary : colors.textFaint} />}
              <Text style={{ fontSize: fs.sm, fontWeight: "500", color: on ? colors.text : colors.textFaint }} numberOfLines={1}>{p.label}</Text>
              {badge ? (
                <View style={{ minWidth: 16, height: 16, paddingHorizontal: 4, borderRadius: 8, backgroundColor: colors.warn, alignItems: "center", justifyContent: "center" }}>
                  <Text style={{ fontSize: 10, fontWeight: "700", color: colors.onPrimary }}>{badge}</Text>
                </View>
              ) : count ? (
                <Text style={{ fontSize: 10, fontWeight: "600", color: colors.violet }}>{count}</Text>
              ) : null}
            </Pressable>
          );
        })}
      </View>
      <View style={{ flex: 1 }}>
        {pane === "accounts" ? <AccountsPane /> : pane === "strategies" ? <StrategiesPane /> : pane === "prompts" ? <PromptsPane /> : pane === "signals" ? <SignalsPane /> : <AiPane />}
      </View>
    </Screen>
  );
}

// 交易 → 待确认 (web views/trading/Prompts.vue + PromptCard): open confirmations, most urgent first,
// with a live countdown and the answer buttons; then the recently handled ones.
import { useEffect, useState } from "react";
import { ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { haptic } from "@/lib/device";
import { time } from "@/lib/fmt";
import { mutate, useQuery } from "@/lib/query";
import { interactApi, type Prompt } from "@/lib/tradeApi";
import { refreshPrompts, setOpenPrompts, useTradeEvents } from "@/lib/tradeLive";
import { modeLabel, promptDeadline } from "@/lib/tradeMeta";
import { colors, fs, radius, tnum } from "@/lib/theme";
import { Card, EmptyState, ErrorBox, Loading, PanelTitle, PillButton, Pull, TAB_BAR_SPACE, Tag, toastError, toast } from "../ui";
import { InfoTip, LTag, Muted } from "./common";
import { usePendingProposals, useRealtrade, useRecentProposals } from "@/lib/realtrade";
import { ProposalCard } from "../ai/ProposalCard";

interface PromptsData { open: Prompt[]; history: Prompt[] }

const byLabel = (by?: string) => (!by ? "" : by === "web" ? "网页" : by === "timeout" ? "超时默认" : by === "cancelled" ? "已撤回" : by.startsWith("telegram") ? "Telegram" : by === "app" ? "App" : by);

export function PromptsPane() {
  const q = useQuery<PromptsData>("trade:prompts", async () => {
    const [xs, all] = await Promise.all([interactApi.prompts("open"), interactApi.prompts("all", 50)]);
    setOpenPrompts(xs.length);
    return {
      open: xs.slice().sort((a, b) => promptDeadline(a.deadline) - promptDeadline(b.deadline)),
      history: all.filter((p) => p.state && p.state !== "open").slice(0, 20),
    };
  }, { interval: 5000, staleMs: 3000 });
  useTradeEvents(q.refresh, { interact: true });
  const [busy, setBusy] = useState<string | null>(null);
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  const answer = async (p: Prompt, key: string, label: string) => {
    void haptic(key === "skip" ? "light" : "medium");
    setBusy(p.id);
    try {
      await interactApi.answer(p.id, key);
      mutate<PromptsData>("trade:prompts", (d) => (d ? { ...d, open: d.open.filter((x) => x.id !== p.id) } : d));
      toast(`已${label}`, p.event.title);
      void refreshPrompts();
    } catch (e) {
      toastError("未能提交", e);
    } finally {
      setBusy(null);
      void q.refresh();
    }
  };
  const choiceLabel = (p: Prompt) => p.options.find((o) => o.key === p.choice)?.label || p.choice || "—";

  return (
    <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: TAB_BAR_SPACE }} refreshControl={<Pull onRefresh={q.refresh} />}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Text style={{ fontSize: fs.xl, fontWeight: "600", color: colors.text }}>待确认</Text>
        <InfoTip title="待确认" items={[
          "账户开启了人工确认时，符合条件的信号在下单前会停在这里，并同时推送到 Telegram（如已配置）。在任一处回答即可，另一处会同步关闭。",
          ["执行", "：按正常仓位下单（仍要经过风控）。"],
          ["半仓", "：仓位减半。"],
          ["跳过", "：不下单，信号记为“用户跳过”。"],
          "超过时限没有回答时按账户设置的“超时默认”处理。",
        ]} />
      </View>
      {q.error && !q.data ? <ErrorBox text={q.error} onRetry={() => void q.refresh()} /> : null}
      {!q.data && !q.error ? <Loading text="加载待确认" /> : null}
      <AiProposals />
      {q.data && !q.data.open.length ? <Card><EmptyState icon="file-tray-outline" text="没有需要确认的模拟交易信号" /></Card> : null}
      {(q.data?.open ?? []).map((p) => <PromptCard key={p.id} p={p} now={now} busy={busy === p.id} onAnswer={(k, l) => void answer(p, k, l)} />)}
      {q.data?.history.length ? (
        <Card>
          <PanelTitle title="最近处理" />
          {q.data.history.map((x, i) => (
            <View key={x.id} style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 7, borderTopWidth: i ? 1 : 0, borderColor: colors.border }}>
              <Text style={[{ fontSize: fs.xs, color: colors.textFaint, width: 70 }, tnum]}>{time(x.answeredAt || promptDeadline(x.deadline), true)}</Text>
              <Text style={{ flex: 1, fontSize: fs.sm, color: colors.text }} numberOfLines={1}>{x.event.title}{x.event.account ? <Text style={{ color: colors.textFaint }}> · {x.event.account}</Text> : null}</Text>
              <Tag text={x.state === "expired" && x.by !== "timeout" ? "已过期" : choiceLabel(x)} tone={x.state !== "expired" && (x.choice === "execute" || x.choice === "half") ? "good" : undefined} />
              <Text style={{ fontSize: fs.xs, color: colors.textFaint, width: 54, textAlign: "right" }}>{byLabel(x.by)}</Text>
            </View>
          ))}
        </Card>
      ) : null}
    </ScrollView>
  );
}

/** AI-proposed real OKX trades: decided here (or in Telegram / the chat; the first answer wins). */
function AiProposals() {
  const owner = useRealtrade((r) => r.owner);
  const mode = useRealtrade((r) => r.mode);
  const pending = usePendingProposals();
  const recent = useRecentProposals();
  if (!owner) return null;
  const done = (recent ?? []).filter((p) => p.status !== "pending").slice(0, 3);
  return (
    <View style={{ gap: 10 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text }}>AI 交易提议</Text>
        {mode ? <Tag text={mode === "dryrun" ? "DRY RUN" : "实盘"} tone={mode === "dryrun" ? "warn" : "bad"} /> : null}
        <InfoTip title="AI 交易提议" items={[
          "AI 在对话里或信号触发时提出的 OKX 真实操作：开仓、补仓、减仓、平仓、调整止盈止损；市价或限价，逐仓或全仓，开仓和补仓必带止盈止损。",
          "限价单挂在 OKX 上，到期未成交的部分由服务器自动撤单；执行结果会自动发回提议它的 AI 对话，让 AI 核对仓位并安排下一步。",
          "在这里、Telegram 交易机器人或 AI 对话里同意 / 拒绝都可以，先答复的为准，其他地方会同步。",
          "同意时会按最新价格和仓位重新检查偏移、止盈止损方向和各项上限，不满足就作废。",
          mode === "dryrun" ? "当前是 DRY RUN：同意后只生成并记录下单请求，不会真实下单。" : "当前是实盘：同意后立即在 OKX 执行。",
        ]} />
      </View>
      {pending.length ? pending.map((p) => <ProposalCard key={p.id} p={p} />) : <Card><EmptyState icon="sparkles-outline" text="没有待审核的 AI 交易提议" /></Card>}
      {done.map((p) => <ProposalCard key={p.id} p={p} />)}
    </View>
  );
}

function PromptCard({ p, now, busy, onAnswer }: { p: Prompt; now: number; busy?: boolean; onAnswer: (key: string, label: string) => void }) {
  const router = useRouter();
  const left = Math.max(0, Math.floor((promptDeadline(p.deadline) - now) / 1000));
  const leftText = left >= 60 ? `${Math.floor(left / 60)}分${String(left % 60).padStart(2, "0")}秒` : `${left}秒`;
  const defLabel = p.options.find((o) => o.key === p.default)?.label || p.default;
  return (
    <View style={{ gap: 8, padding: 14, backgroundColor: colors.card, borderRadius: radius.md, borderWidth: 1, borderColor: colors.borderStrong, borderLeftWidth: 3, borderLeftColor: colors.warn }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        {p.event.mode ? <LTag l={modeLabel[p.event.mode]} fallback={p.event.mode} /> : p.event.kind === "test" ? <Tag text="测试" /> : null}
        {p.event.accountId ? (
          <Text onPress={() => router.push({ pathname: "/trading/account/[id]", params: { id: String(p.event.accountId) } })} style={{ fontSize: fs.sm, color: colors.textSoft, fontWeight: "500" }}>{p.event.account}</Text>
        ) : null}
        <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(p.event.ts, true)}</Text>
        <View style={{ flex: 1 }} />
        <Ionicons name="timer-outline" size={14} color={left <= 15 ? colors.warn : colors.textSoft} />
        <Text style={[{ fontSize: fs.sm, fontWeight: "600", color: left <= 15 ? colors.warn : colors.textSoft }, tnum]}>{left ? leftText : "已超时"}</Text>
      </View>
      <Text style={{ fontSize: fs.xl, fontWeight: "600", color: colors.text }}>{p.event.title}</Text>
      {p.event.lines?.length ? (
        <View style={{ gap: 2, paddingLeft: 4 }}>
          {p.event.lines.map((l, i) => <Text key={i} style={{ fontSize: fs.sm, color: colors.textSoft, lineHeight: 18 }}>· {l}</Text>)}
        </View>
      ) : null}
      <Muted>超时默认：{defLabel}</Muted>
      <View style={{ flexDirection: "row", gap: 8 }}>
        {p.options.map((o) => (
          <PillButton key={o.key} small label={o.label} style={{ flex: 1 }} variant={o.key === "execute" ? "primary" : o.key === "skip" ? "ghost" : "soft"} disabled={busy || !left} onPress={() => onAnswer(o.key, o.label)} />
        ))}
      </View>
    </View>
  );
}

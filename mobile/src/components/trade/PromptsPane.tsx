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
      {q.data && !q.data.open.length ? <Card><EmptyState icon="file-tray-outline" text="没有需要确认的信号" /></Card> : null}
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

// 交易 → AI 决策 (web views/trading/AiLog.vue): AI status and today's numbers, parameter proposals
// (approve / reject), the decision log with the engine's verdict and a detail sheet, and the
// strategy experience document (重新提炼 = a paid model call: owner key).
import { useMemo, useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { haptic } from "@/lib/device";
import { ago, price, time } from "@/lib/fmt";
import { useQuery } from "@/lib/query";
import { aiApi, tradeApi, type Account, type AiStatus, type DecisionRecord, type MemoryDoc, type Proposal, type Signal, type StrategyEntry } from "@/lib/tradeApi";
import { useTradeEvents } from "@/lib/tradeLive";
import { actionLabel, aiState, outcomeLabel, type Lbl } from "@/lib/tradeMeta";
import { colors, fs, radius, tnum } from "@/lib/theme";
import { CoinIcon } from "../CoinIcon";
import { Card, ErrorBox, Loading, PanelTitle, PickerSheet, PillButton, Pull, Segmented, Sheet, TAB_BAR_SPACE, Tag, confirm, tap, toast, toastError } from "../ui";
import { Banner, InfoTip, KpiGrid, LTag, MoreButton, Muted, SmallButton, mono, toneText } from "./common";

type Verdict = Pick<Signal, "outcome" | "outcomeReason">;
interface AiData { decisions: DecisionRecord[]; proposals: Proposal[]; verdict: Record<number, Verdict> }
const kindLabel: Record<string, string> = { review: "审核", test: "测试", reflect: "提炼经验" };
const symOf = (id: string) => id.replace("-USDT-SWAP", "");

export function AiPane() {
  const router = useRouter();
  const [acc, setAcc] = useState(0);
  const [kind, setKind] = useState<"review" | "">("review");
  const [accPick, setAccPick] = useState(false);
  const [shown, setShown] = useState(30);
  const key = `trade:ai:${acc}:${kind}`;
  const q = useQuery<AiData>(key, async () => {
    const [d, p] = await Promise.all([aiApi.decisions({ accountId: acc, kind: kind || undefined, limit: 200 }), aiApi.proposals(undefined, 50)]);
    const ids = [...new Set(d.filter((x) => x.accountId && x.signalId).map((x) => x.accountId))];
    const lists = await Promise.all(ids.map((id) => tradeApi.signals(id, 500).catch(() => [] as Signal[])));
    const verdict: Record<number, Verdict> = {};
    for (const s of lists.flat()) if (s.aiDecisionId) verdict[s.aiDecisionId] = { outcome: s.outcome, outcomeReason: s.outcomeReason };
    return { decisions: d, proposals: p, verdict };
  }, { interval: 15_000, staleMs: 10_000 });
  const status = useQuery<AiStatus>("ai:status", () => aiApi.status(), { interval: 15_000, staleMs: 10_000 });
  const accounts = useQuery<Account[]>("trade:accountList", () => tradeApi.accounts(), { staleMs: 60_000 });
  useTradeEvents(q.refresh);

  const s = status.data;
  const st = s ? aiState(s) : null;
  const decisions = q.data?.decisions;
  const verdict = q.data?.verdict ?? {};
  const pending = (q.data?.proposals ?? []).filter((p) => p.state === "pending");
  const past = (q.data?.proposals ?? []).filter((p) => p.state !== "pending").slice(0, 10);
  const accName = (id: number) => accounts.data?.find((a) => a.id === id)?.name || (id ? `账户 #${id}` : "—");
  const stats = useMemo(() => {
    const xs = (decisions || []).filter((d) => d.kind === "review");
    const lat = xs.filter((d) => d.latencyMs > 0);
    return { n: xs.length, exec: xs.filter((d) => d.action === "execute").length, skip: xs.filter((d) => d.action === "skip").length, err: xs.filter((d) => d.error).length, lat: lat.length ? lat.reduce((a, d) => a + d.latencyMs, 0) / lat.length : 0 };
  }, [decisions]);

  const [detail, setDetail] = useState<number | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const decide = (p: Proposal, approve: boolean) => {
    const go = async () => {
      void haptic(approve ? "medium" : "light");
      setBusy(p.id);
      try {
        await aiApi.decide(p.id, approve);
        toast(approve ? "已批准" : "已拒绝", `${p.strategy} · 实例 #${p.instanceId}`);
        await q.refresh();
      } catch (e) { toastError("未能提交", e); } finally { setBusy(null); }
    };
    if (approve) confirm("批准参数建议", `批准后实例 #${p.instanceId}（${p.strategy}）生成新的配置版本并立即使用新参数。继续？`, "批准", () => void go());
    else void go();
  };
  const changes = (p: Proposal) => Object.entries(p.params || {}).map(([k, v]) => ({ k, from: p.current?.[k], to: v }));

  return (
    <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: TAB_BAR_SPACE }} refreshControl={<Pull onRefresh={() => Promise.all([q.refresh(), status.refresh()])} />}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Text style={{ fontSize: fs.xl, fontWeight: "600", color: colors.text }}>AI 决策</Text>
        <InfoTip title="AI 决策" items={[
          "执行方式为“AI 审核”的策略，每个信号都会先交给 AI：AI 看到信号、账户与持仓、行情摘要、该策略近期的决策结果和经验文档，回答执行或跳过，可以缩小仓位、调整止损止盈。",
          "AI 的“执行”之后仍要经过账户风控，所以“引擎”结果可能是“风控拒绝”。AI 不能放大仓位、不能绕过风控、不能直接改参数。",
          ["参数建议", "：AI 认为参数需要调整时提出建议，只有你批准后才生效（生成新的配置版本）。"],
          ["经验文档", "：定期让 AI 读取过去的决策和平仓结果，提炼成一份简短文档，之后的审核会带上它。"],
          "AI 只经代理访问 Anthropic；代理不可用时 AI 审核暂停（信号按跳过处理），不会直连。",
        ]} />
        {st ? <LTag l={st} /> : null}
        <View style={{ flex: 1 }} />
        <Pressable onPress={() => { tap(); setAccPick(true); }} style={{ flexDirection: "row", alignItems: "center", gap: 4, height: 30, paddingHorizontal: 10, borderRadius: 8, borderWidth: 1, borderColor: colors.borderStrong, maxWidth: 150 }}>
          <Text style={{ fontSize: fs.sm, color: colors.text }} numberOfLines={1}>{acc ? accName(acc) : "全部账户"}</Text>
          <Ionicons name="chevron-down" size={13} color={colors.textFaint} />
        </Pressable>
      </View>
      {st && st.c !== "good" ? <Banner tone={st.c === "bad" ? "bad" : "warn"} text={`${st.t}：${st.d}`} /> : null}
      {st && !s?.configured ? <SmallButton label="去授权（设置）" icon="settings-outline" onPress={() => router.push("/settings")} /> : null}

      <KpiGrid items={[
        { label: "今日调用", value: String(s?.callsToday ?? "—"), sub: s?.lastCallAt ? `最近 ${ago(s.lastCallAt)}前` : "尚未调用" },
        { label: "今日费用", value: `$${(s?.costToday ?? 0).toFixed(2)}`, sub: s?.kind === "oauth_token" ? "订阅 Token：按 API 价格估算" : "USD，UTC+8 当日" },
        { label: "审核（列表内）", value: String(stats.n), sub: `执行 ${stats.exec} · 跳过 ${stats.skip}${stats.err ? ` · 出错 ${stats.err}` : ""}` },
        { label: "平均耗时", value: stats.lat ? (stats.lat / 1000).toFixed(1) + "s" : "—", sub: `模型 ${s?.model || "—"}` },
      ]} />

      <Card>
        <PanelTitle title="参数建议" right={pending.length ? <Tag text={`${pending.length} 待处理`} tone="warn" /> : undefined} />
        {q.data && !q.data.proposals.length ? <Muted>还没有参数建议</Muted> : null}
        {pending.map((p) => (
          <View key={p.id} style={{ gap: 8, padding: 12, marginBottom: 10, borderRadius: radius.md, borderWidth: 1, borderColor: colors.borderStrong }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
              <Text style={{ fontWeight: "700", color: colors.text, fontSize: fs.md }}>{p.strategy}</Text>
              <Muted>实例 #{p.instanceId} · {accName(p.accountId)}</Muted>
              <View style={{ flex: 1 }} />
              <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(p.ts, true)}</Text>
            </View>
            {changes(p).map((c) => (
              <View key={c.k} style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
                <Text style={[mono, { color: colors.text, fontSize: fs.sm, minWidth: 90 }]}>{c.k}</Text>
                <Text style={[{ fontSize: fs.sm, color: colors.textSoft }, tnum]}>{c.from ?? "—"}</Text>
                <Text style={{ color: colors.textFaint }}>→</Text>
                <Text style={[{ fontSize: fs.sm, fontWeight: "700", color: colors.text }, tnum]}>{c.to}</Text>
              </View>
            ))}
            <Text style={{ fontSize: fs.sm, color: colors.text, lineHeight: 20 }}>{p.rationale}</Text>
            <View style={{ flexDirection: "row", gap: 8 }}>
              <PillButton small variant="ghost" icon="close" label="拒绝" style={{ flex: 1 }} disabled={busy === p.id} onPress={() => decide(p, false)} />
              <PillButton small icon="checkmark" label="批准" style={{ flex: 1 }} disabled={busy === p.id} onPress={() => decide(p, true)} />
            </View>
          </View>
        ))}
        {past.length ? (
          <>
            <Text style={{ fontSize: fs.xs, fontWeight: "600", color: colors.textFaint, marginTop: 4, marginBottom: 4 }}>已处理</Text>
            {past.map((p) => (
              <View key={p.id} style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 5 }}>
                <Tag text={p.state === "approved" ? "已批准" : "已拒绝"} tone={p.state === "approved" ? "good" : undefined} />
                <Text style={{ fontSize: fs.sm, color: colors.text }}>{p.strategy}</Text>
                <Text style={[mono, { flex: 1, color: colors.textFaint }]} numberOfLines={1}>{changes(p).map((c) => `${c.k}→${c.to}`).join(" ")}</Text>
              </View>
            ))}
          </>
        ) : null}
      </Card>

      <Card>
        <PanelTitle title="决策记录" right={<Segmented small value={kind} onChange={(v) => { setKind(v); setShown(30); }} options={[{ value: "review", label: "审核" }, { value: "", label: "全部" }]} />} />
        {q.error && !q.data ? <ErrorBox text={q.error} onRetry={() => void q.refresh()} /> : null}
        {!q.data && !q.error ? <Loading text="加载决策" /> : null}
        {decisions && !decisions.length ? <Muted style={{ paddingVertical: 12, textAlign: "center" }}>还没有 AI {kind ? "审核" : "调用"}记录</Muted> : null}
        {(decisions || []).slice(0, shown).map((d, i) => <DecisionRow key={d.id} d={d} first={i === 0} verdict={verdict[d.id]} accName={accName} onDetail={() => setDetail(d.id)} />)}
        {(decisions || []).length > shown ? <MoreButton label="显示更多" onPress={() => setShown((n) => n + 50)} /> : null}
      </Card>

      <MemoryCard available={!!s?.available} />

      <PickerSheet visible={accPick} onClose={() => setAccPick(false)} title="账户" value={String(acc)}
        options={[{ value: "0", label: "全部账户" }, ...(accounts.data ?? []).map((a) => ({ value: String(a.id), label: a.name, desc: a.mode === "backtest" ? "回测" : "实时" }))]}
        onPick={(v) => { setAcc(Number(v)); setShown(30); }} />
      <DecisionSheet id={detail} onClose={() => setDetail(null)} />
    </ScrollView>
  );
}

function DecisionRow({ d, first, verdict, accName, onDetail }: { d: DecisionRecord; first: boolean; verdict?: Verdict; accName: (id: number) => string; onDetail: () => void }) {
  const act = actionLabel[d.signalAction as keyof typeof actionLabel];
  const oc: Lbl | undefined = verdict ? outcomeLabel[verdict.outcome] : undefined;
  return (
    <Pressable onPress={() => { tap(); onDetail(); }} style={({ pressed }) => ({ gap: 4, paddingVertical: 10, borderTopWidth: first ? 0 : 1, borderColor: colors.border, backgroundColor: pressed ? colors.raised : "transparent" })}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
        {d.instId ? <><CoinIcon instId={d.instId} size={15} /><Text style={{ fontWeight: "700", color: colors.text, fontSize: fs.md }}>{symOf(d.instId)}</Text></> : null}
        {d.signalAction ? <Text style={{ fontWeight: "600", fontSize: fs.sm, color: toneText(act?.tone) }}>{act?.t || d.signalAction}</Text> : null}
        {d.kind !== "review" ? <Tag text={kindLabel[d.kind] || d.kind} /> : null}
        {d.error ? <Tag text="出错" tone="bad" /> : d.kind === "review" ? <Tag text={`AI ${d.action === "execute" ? "执行" : d.action === "skip" ? "跳过" : d.action || "—"}`} tone={d.action === "execute" ? "good" : undefined} /> : null}
        {d.action === "execute" && oc ? <LTag l={{ t: `引擎：${oc.t}`, c: oc.c }} /> : null}
        <View style={{ flex: 1 }} />
        <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(d.ts, true)}</Text>
      </View>
      <Text style={[{ fontSize: fs.xs, color: colors.textSoft }, tnum]}>
        {[d.accountId ? accName(d.accountId) : "", d.strategy, d.decision?.confidence != null ? `置信 ${(d.decision.confidence * 100).toFixed(0)}%` : "",
          d.action === "execute" && d.decision?.sizeScale ? `仓位 ×${d.decision.sizeScale.toFixed(2)}` : "", d.decision?.stopLoss ? `止损 ${price(d.decision.stopLoss)}` : "",
          d.decision?.takeProfit ? `止盈 ${price(d.decision.takeProfit)}` : ""].filter(Boolean).join("  ")}
      </Text>
      <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{d.model || "—"}{d.promptVersion ? ` · 提示词 ${d.promptVersion}` : ""} · ${d.costUsd.toFixed(3)} · {(d.latencyMs / 1000).toFixed(1)}s</Text>
      {d.decision?.rationale ? <Text style={{ fontSize: fs.sm, color: colors.text, lineHeight: 20 }} numberOfLines={6}>{d.decision.rationale}</Text> : null}
      {d.error ? <Text style={{ fontSize: fs.xs, color: colors.down }}>{d.error}</Text> : null}
      {d.action === "execute" && verdict?.outcomeReason && verdict.outcome !== "ordered" ? <Text style={{ fontSize: fs.xs, color: colors.down }}>{verdict.outcomeReason}</Text> : null}
      {d.outcomes?.map((o) => <Text key={o.ts} style={{ fontSize: fs.xs, color: colors.textSoft }}><Text style={{ color: colors.textFaint }}>{time(o.ts, true)}</Text> {o.result}</Text>)}
    </Pressable>
  );
}

function DecisionSheet({ id, onClose }: { id: number | null; onClose: () => void }) {
  const q = useQuery<DecisionRecord>(id ? `ai:decision:${id}` : null, () => aiApi.decision(id!), { staleMs: 10 * 60_000 });
  const d = id ? q.data : undefined;
  const pre = (x: unknown) => (
    <ScrollView horizontal style={{ backgroundColor: colors.bg, borderRadius: 8, borderWidth: 1, borderColor: colors.border }} contentContainerStyle={{ padding: 10 }}>
      <Text selectable style={[mono, { color: colors.textSoft, lineHeight: 17 }]}>{JSON.stringify(x, null, 2)}</Text>
    </ScrollView>
  );
  return (
    <Sheet visible={id != null} onClose={onClose} title={d ? `决策 #${d.id}` : "决策详情"} scroll>
      {q.error && !d ? <Text style={{ color: colors.down }}>{q.error}</Text> : !d ? <Loading text="读取" /> : (
        <View style={{ gap: 8, paddingBottom: 10 }}>
          <Muted>{time(d.ts, true)} · {kindLabel[d.kind] || d.kind} · {d.model} · 提示词 {d.promptVersion || "—"} · 经验 v{d.memoryVersion} · 请求 sha256 {d.requestSha256.slice(0, 12)}</Muted>
          <Text style={{ fontSize: fs.md, fontWeight: "600", color: colors.text }}>模型回答</Text>
          {pre(d.decision)}
          {d.request ? <><Text style={{ fontSize: fs.md, fontWeight: "600", color: colors.text, marginTop: 6 }}>发给模型的上下文</Text>{pre(d.request)}</> : null}
        </View>
      )}
    </Sheet>
  );
}

function MemoryCard({ available }: { available: boolean }) {
  const strategies = useQuery<StrategyEntry[]>("trade:strategies", () => tradeApi.strategies(), { staleMs: 60_000 });
  const names = (strategies.data ?? []).map((e) => e.name);
  const [pick, setPick] = useState("");
  const [open, setOpen] = useState(false);
  const [reflecting, setReflecting] = useState(false);
  const strat = pick || names[0] || "";
  const mem = useQuery<{ current: MemoryDoc | null; err?: string }>(strat ? `ai:memory:${strat}` : null, async () => {
    try { return { current: (await aiApi.memory(strat)).current }; } catch (e) {
      const m = e instanceof Error ? e.message : String(e);
      return { current: null, err: /not found/i.test(m) ? undefined : m };
    }
  }, { staleMs: 60_000 });
  const reflect = () => confirm("重新提炼", `让 AI 读取「${strat}」最近的决策与结果，提炼一份新的经验文档？会产生一次模型调用费用。`, "提炼", async () => {
    setReflecting(true);
    try { await aiApi.reflect(strat); toast("已提炼新的经验文档"); await mem.refresh(); } catch (e) { toastError("提炼失败", e); } finally { setReflecting(false); }
  });
  const m = mem.data?.current;
  return (
    <Card>
      <PanelTitle title="经验文档" right={names.length ? (
        <Pressable onPress={() => { tap(); setOpen(true); }} style={{ flexDirection: "row", alignItems: "center", gap: 4, height: 28, paddingHorizontal: 10, borderRadius: 8, borderWidth: 1, borderColor: colors.borderStrong, maxWidth: 170 }}>
          <Text style={{ fontSize: fs.sm, color: colors.text }} numberOfLines={1}>{strat}</Text>
          <Ionicons name="chevron-down" size={13} color={colors.textFaint} />
        </Pressable>
      ) : undefined} />
      <Muted style={{ marginBottom: 8 }}>AI 从某个策略过去的审核决策和平仓结果中提炼的经验，之后审核这个策略的信号时会一并参考。每次提炼生成一个新版本，旧版本保留。</Muted>
      {reflecting || (mem.loading && !mem.data) ? <Loading text={reflecting ? "AI 提炼中…" : "加载"} /> : m ? (
        <View style={{ gap: 6 }}>
          <Muted>v{m.version} · {time(m.ts, true)} · 读取 {m.decisions} 条决策{m.model ? ` · ${m.model}` : ""}</Muted>
          <Text selectable style={{ fontSize: fs.sm, color: colors.text, lineHeight: 21 }}>{m.doc}</Text>
        </View>
      ) : mem.data ? <Muted>这个策略还没有经验文档</Muted> : null}
      {mem.data?.err ? <Text style={{ fontSize: fs.sm, color: colors.down }}>{mem.data.err}</Text> : null}
      <View style={{ alignItems: "flex-end", marginTop: 8 }}>
        <SmallButton label="重新提炼" icon="sparkles-outline" disabled={!strat || reflecting || !available} onPress={reflect} />
      </View>
      <PickerSheet visible={open} onClose={() => setOpen(false)} title="策略" value={strat} options={names.map((n) => ({ value: n, label: n }))} onPick={setPick} />
    </Card>
  );
}

// An AI trade proposal (lib/realtrade.ts) as a card: what it does (开仓 / 补仓 / 减仓 / 平仓 / 调整止盈止损),
// coin, side, leverage, margin mode, order type (市价 / 限价 @price), size, entry / stop / target with R:R,
// the position it acts on (补仓: the new average and liquidation estimate; 减仓 / 平仓: how much of which
// position), what happens to the TP/SL, the reasoning, its status, a resting limit's state (挂单中 ·
// 已成交 x/y · 已撤单) and the outcome. While pending: a countdown and 同意 / 拒绝 (同意 asks once with the
// exact action). The coin or 查看 opens a floating K-line over the AI sheet (the web chart with the
// proposal's levels drawn, ChartView `proposal`). TradeBubble is the server's message in the chat after
// an approved proposal ran (role trade). Owner only.
import { useEffect, useState, useSyncExternalStore } from "react";
import { Platform, Pressable, ScrollView, Text, View, useWindowDimensions } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { ChartView } from "@/components/ChartView";
import { CoinIcon } from "@/components/CoinIcon";
import { PillButton, Tag, confirm, tap, toast, toastError } from "@/components/ui";
import type { StepItem } from "@/components/ai/AiSteps";
import { price, sym, time } from "@/lib/fmt";
import { answerProposal, kindLabel, mgnLabel, orderStateLabel, serverNow, statusLabel, statusTone, useProposal, useRealtrade, type Proposal } from "@/lib/realtrade";
import { colors, fs, tnum } from "@/lib/theme";

const mono = { fontFamily: Platform.select({ ios: "Menlo", default: "monospace" }) };
const VIA: Record<string, string> = { telegram: "Telegram", web: "网页", app: "App" };
const CLOSE: Record<string, string> = { tp: "止盈", sl: "止损", manual: "主动平仓", unfilled: "未成交", other: "平仓" };
const lots = (v?: number) => String(+(v ?? 0).toPrecision(6));
const u = (v: number) => (Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(2).replace(/\.?0+$/, ""));
const signed = (v: number) => (v > 0 ? "+" : v < 0 ? "−" : "") + Math.abs(v).toFixed(2);

function source(p: Proposal) {
  if (p.source === "chat") return "AI 对话";
  return `${p.source === "signal" ? "信号" : "定时"} · ${p.trigger || "—"}`;
}

function countdown(ms: number) {
  if (ms <= 0) return "已到期";
  const s = Math.ceil(ms / 1000);
  const p2 = (n: number) => String(n).padStart(2, "0");
  return s >= 3600 ? `${Math.floor(s / 3600)}:${p2(Math.floor(s / 60) % 60)}:${p2(s % 60)}` : `${Math.floor(s / 60)}:${p2(s % 60)}`;
}

/** Server time, ticking each second while `on`. */
function useNow(on: boolean) {
  const [now, setNow] = useState(serverNow);
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => setNow(serverNow()), 1000);
    return () => clearInterval(t);
  }, [on]);
  return now;
}

/** The action the owner agrees to, in words (the confirm alert). */
function orderText(p: Proposal) {
  const long = p.side === "long";
  const d = (v: number, up: boolean) => `${up ? "+" : "−"}${v.toFixed(2)}%`;
  const pos = `${sym(p.instId)} ${long ? "多单" : "空单"}`;
  const how = p.orderType === "limit" ? `限价 ${price(p.px)}（挂 ${p.orderMin ?? 60} 分钟，未成交部分到期自动撤单）` : "市价";
  let text: string;
  switch (p.kind) {
    case "reduce":
      text = `以${how}减仓 ${pos} ${Math.round((p.fraction ?? 0) * 100)}%：${lots(p.contracts)} 张（持仓 ${lots(p.posContracts)} 张）。`;
      break;
    case "close":
      text = `以${how}平掉 ${pos} 全部 ${lots(p.contracts)} 张。`;
      break;
    case "adjust":
      text = `把 ${pos} 的止损改为 ${price(p.sl)}（${d(p.slPct, !long)}），止盈改为 ${price(p.tp)}（${d(p.tpPct, long)}），覆盖整个仓位。`;
      break;
    default:
      text =
        `以${how}${p.kind === "add" ? "补仓" : "开"}${long ? "多" : "空"} ${sym(p.instId)}，保证金 ${u(p.marginUsdt)} U，杠杆 ${p.leverage}×（${mgnLabel(p)}，${lots(p.contracts)} 张，名义 ${u(p.notionalUsdt)} U），` +
        `止损 ${price(p.sl)}（${d(p.slPct, !long)}），止盈 ${price(p.tp)}（${d(p.tpPct, long)}）。`;
      if (p.kind === "add" && p.newAvgPx) text += `补后 ${lots((p.posContracts ?? 0) + p.contracts)} 张，均价约 ${price(p.newAvgPx)}${p.liqPx ? `，强平约 ${price(p.liqPx)}` : ""}。`;
  }
  if (p.tpsl && p.kind !== "open") text += `\n止盈止损：${p.tpsl}。`;
  return text + (p.mode === "dryrun" ? "\n\nDRY RUN：只生成、记录并模拟，不会真实下单。" : "\n\n将在 OKX 真实执行。");
}

function DryRun() {
  return (
    <View style={{ height: 20, paddingHorizontal: 6, borderRadius: 4, borderWidth: 1, borderStyle: "dashed", borderColor: colors.warn, justifyContent: "center" }}>
      <Text style={[mono, { fontSize: 10, fontWeight: "700", color: colors.warn }]}>DRY RUN</Text>
    </View>
  );
}

function Cell({ k, v, sub, color }: { k: string; v: string; sub?: string; color?: string }) {
  return (
    <View style={{ flex: 1, minWidth: 0, gap: 1 }}>
      <Text numberOfLines={1} style={{ fontSize: fs.xs, color: colors.textFaint }}>{k}</Text>
      <Text numberOfLines={1} style={[{ fontSize: fs.lg, fontWeight: "600", color: color ?? colors.text }, tnum]}>{v}</Text>
      {sub ? <Text numberOfLines={1} style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{sub}</Text> : null}
    </View>
  );
}

export function ProposalCard({ p, inChart }: { p: Proposal; inChart?: boolean }) {
  const pending = p.status === "pending";
  const now = useNow(pending);
  const [busy, setBusy] = useState<"" | "approve" | "reject">("");
  const [more, setMore] = useState(false);
  const long = p.side === "long";
  const dry = p.mode === "dryrun";
  const chart = () => { tap(); openProposalChart(p); };

  const answer = (action: "approve" | "reject") => {
    setBusy(action);
    answerProposal(p.id, action)
      .then(({ p: r, conflict }) => {
        if (conflict) toast("这笔提议已被处理", `当前状态：${statusLabel(r)}${r.via ? `（经 ${VIA[r.via] ?? r.via}）` : ""}`);
        else toast(action === "approve" ? (r.mode === "dryrun" ? "已同意（DRY RUN）" : "已同意，正在下单") : "已拒绝");
      })
      .catch((e) => toastError(action === "approve" ? "未能同意" : "未能拒绝", e))
      .finally(() => setBusy(""));
  };
  const kind = kindLabel(p);
  const risky = p.kind === "open" || p.kind === "add" || !p.kind;
  const approve = () => confirm(dry ? `同意${kind}（DRY RUN）` : `同意并真实${kind}`, orderText(p), dry ? "同意" : "同意并执行", () => answer("approve"), !dry);
  const reject = () => confirm("拒绝这笔提议", `${kind} ${sym(p.instId)} ${long ? "多单" : "空单"}，拒绝后不会执行。`, "拒绝", () => answer("reject"), true);
  const ordState = orderStateLabel(p);

  const answered = p.via && ["approved", "executing", "executed", "failed", "rejected"].includes(p.status);
  const longText = p.rationale.length > 90;
  return (
    <View style={{ marginVertical: inChart ? 0 : 10, borderRadius: 14, borderWidth: 1, borderColor: pending ? "rgba(247,166,0,0.4)" : colors.border, backgroundColor: colors.card, overflow: "hidden" }}>
      {/* head: coin · side · leverage … DRY RUN · status */}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 7, paddingHorizontal: 12, paddingTop: 11, paddingBottom: 9 }}>
        <Pressable disabled={inChart} onPress={chart} hitSlop={6} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 5, paddingLeft: 4, paddingRight: inChart ? 6 : 4, paddingVertical: 3, borderRadius: 8, backgroundColor: pressed ? "rgba(188,255,47,0.22)" : colors.primarySoft })}>
          <CoinIcon instId={p.instId} size={18} />
          <Text style={{ fontSize: fs.lg, fontWeight: "700", color: colors.primary }}>{sym(p.instId)}</Text>
          {inChart ? null : <Ionicons name="chevron-forward" size={13} color={colors.primary} />}
        </Pressable>
        {p.kind && p.kind !== "open" ? <Tag text={kind} tone="info" /> : null}
        <Tag text={long ? "做多" : "做空"} tone={long ? "good" : "bad"} />
        <Text style={[{ fontSize: fs.md, fontWeight: "600", color: colors.text }, tnum]}>{p.leverage}×</Text>
        <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>{mgnLabel(p)}</Text>
        <View style={{ flex: 1 }} />
        {dry ? <DryRun /> : null}
        <Tag text={statusLabel(p)} tone={statusTone(p.status)} />
      </View>

      {/* size and levels */}
      <View style={{ paddingHorizontal: 12, gap: 10 }}>
        <View style={{ flexDirection: "row", gap: 10 }}>
          {risky ? <Cell k="保证金" v={`${u(p.marginUsdt)} U`} sub={`${lots(p.contracts)} 张 · 名义 ${u(p.notionalUsdt)} U`} />
            : p.kind === "reduce" ? <Cell k="减仓" v={`${Math.round((p.fraction ?? 0) * 100)}%`} sub={`${lots(p.contracts)} / ${lots(p.posContracts)} 张`} />
            : p.kind === "close" ? <Cell k="平仓" v="全部" sub={`${lots(p.contracts)} 张`} />
            : <Cell k="仓位" v={`${lots(p.posContracts)} 张`} sub={`均价 ${price(p.posAvgPx)}`} />}
          {p.kind === "adjust" ? <Cell k="现价" v={price(p.refPx)} />
            : <Cell k={p.orderType === "limit" ? "限价" : "入场参考"} v={price(p.orderType === "limit" ? p.px : p.refPx)} sub={p.orderType === "limit" ? `现价 ${price(p.refPx)} · 挂 ${p.orderMin ?? 60} 分钟` : "市价"} />}
          {risky ? <Cell k="盈亏比" v={`1 : ${p.rr.toFixed(2)}`} /> : <Cell k="仓位" v={p.posId ? `#${p.posId}` : "你的"} sub={p.kind === "adjust" ? undefined : `持仓 ${lots(p.posContracts)} 张`} />}
        </View>
        {p.sl > 0 || p.tp > 0 ? (
          <View style={{ flexDirection: "row", gap: 10 }}>
            <Cell k="止损" v={price(p.sl)} sub={`${long ? "−" : "+"}${p.slPct.toFixed(2)}%`} color={colors.down} />
            <Cell k="止盈" v={price(p.tp)} sub={`${long ? "+" : "−"}${p.tpPct.toFixed(2)}%`} color={colors.up} />
            <Cell k="来源" v={source(p)} sub={`${time(p.created, true)} · #${p.id}`} />
          </View>
        ) : (
          <View style={{ flexDirection: "row", gap: 10 }}>
            <Cell k="来源" v={source(p)} sub={`${time(p.created, true)} · #${p.id}`} />
          </View>
        )}
        {p.kind === "add" && p.newAvgPx ? (
          <Text style={[{ fontSize: fs.sm, color: colors.textSoft }, tnum]}>
            {`补前 ${lots(p.posContracts)} 张 @${price(p.posAvgPx)} → 补后 ${lots((p.posContracts ?? 0) + p.contracts)} 张 · 均价 ≈ ${price(p.newAvgPx)}`}
            {p.liqPx ? ` · 强平 ≈ ${price(p.liqPx)}` : ""}
          </Text>
        ) : p.kind === "open" && p.liqPx ? (
          <Text style={[{ fontSize: fs.sm, color: colors.textFaint }, tnum]}>{`预估强平 ≈ ${price(p.liqPx)}`}</Text>
        ) : null}
        {p.tpsl && p.kind !== "open" ? (
          <Text style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textSoft }}>
            <Text style={{ color: colors.textFaint }}>止盈止损 </Text>
            {p.tpsl}
          </Text>
        ) : null}
        {p.note ? <Text style={{ fontSize: fs.sm, lineHeight: 18, color: colors.warn }}>{p.note}</Text> : null}
      </View>

      {/* reasoning */}
      <View style={{ paddingHorizontal: 12, paddingTop: 10, gap: 6 }}>
        {p.rationale ? (
          <Pressable disabled={!longText} onPress={() => { tap(); setMore(!more); }}>
            <Text selectable={more} numberOfLines={more || !longText ? undefined : 3} style={{ fontSize: fs.md, lineHeight: 20, color: colors.textSoft }}>{p.rationale}</Text>
            {longText ? <Text style={{ fontSize: fs.xs, color: colors.textDim, marginTop: 2 }}>{more ? "收起" : "展开全部"}</Text> : null}
          </Pressable>
        ) : null}
        {p.invalidation ? (
          <Text style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textFaint }}>
            <Text style={{ color: colors.warn }}>失效条件 </Text>
            {p.invalidation}
          </Text>
        ) : null}
      </View>

      {/* outcome */}
      {!pending && (answered || p.execPx || p.ordId || p.error || p.closedAt || ordState) ? (
        <View style={{ marginHorizontal: 12, marginTop: 10, paddingTop: 9, borderTopWidth: 1, borderColor: colors.border, gap: 4 }}>
          {answered ? (
            <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>
              {p.status === "rejected" ? "拒绝" : "同意"}{p.by ? `：${p.by}` : ""} · 经 {VIA[p.via!] ?? p.via} · {time(p.statusAt, true)}
            </Text>
          ) : null}
          {ordState ? (
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
              <Tag text={ordState} tone={p.ordState === "canceled" ? undefined : p.ordState === "filled" ? "good" : "info"} />
              {p.ordState === "live" || p.ordState === "partially_filled" ? (
                <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{`${time(p.orderUntil ?? 0, true)} 前未成交自动撤单`}</Text>
              ) : null}
            </View>
          ) : null}
          {(p.execPx || p.ordId) && !(p.orderType === "limit" && !p.fillPx) ? (
            <Text style={[{ fontSize: fs.sm, color: colors.text }, tnum]}>
              {p.kind === "adjust" ? (dry ? "模拟执行" : "已执行") : `${dry ? "模拟成交" : "成交"} ${price(p.fillPx || p.execPx)}`}
              {p.ordId ? <Text style={[mono, { fontSize: 11, color: colors.textFaint }]}>{`  ordId ${p.ordId}`}</Text> : null}
            </Text>
          ) : null}
          {p.closedAt ? (
            <Text style={[{ fontSize: fs.sm, color: colors.text }, tnum]}>
              {CLOSE[p.closeKind ?? "other"] ?? "平仓"} {price(p.closePx)} · {time(p.closedAt, true)}
              {p.pnlUsdt != null ? <Text style={{ fontWeight: "700", color: p.pnlUsdt >= 0 ? colors.up : colors.down }}>{`  ${signed(p.pnlUsdt)} U`}</Text> : null}
            </Text>
          ) : null}
          {p.error ? <Text selectable style={{ fontSize: fs.sm, lineHeight: 18, color: colors.warn }}>{p.error}</Text> : null}
        </View>
      ) : null}

      {/* actions */}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, paddingTop: 10, paddingBottom: 12 }}>
        {pending ? (
          <View style={{ flexDirection: "row", alignItems: "center", gap: 4 }}>
            <Ionicons name="time-outline" size={14} color={colors.warn} />
            <Text style={[{ fontSize: fs.sm, color: colors.warn }, tnum]}>{countdown(p.expiresAt - now)}</Text>
          </View>
        ) : null}
        <View style={{ flex: 1 }} />
        {inChart ? null : <PillButton small variant="soft" icon="stats-chart-outline" label="查看" onPress={() => openProposalChart(p)} />}
        {pending ? (
          <>
            <PillButton small variant="ghost" label="拒绝" loading={busy === "reject"} disabled={!!busy} onPress={reject} />
            <PillButton small label="同意" loading={busy === "approve"} disabled={!!busy || p.expiresAt <= now} onPress={approve} />
          </>
        ) : null}
      </View>
    </View>
  );
}

/** The server's message after an approved proposal ran or its position closed (chat role trade): the first
 *  line as the head (✅ 已成交 · 📌 挂出 · ⌛ 撤单 · ❌ 失败 · ⚠️ 未下单 · 📕 已结束), the position and its TP/SL,
 *  the instructions to the AI folded, and 查看提议 (the proposal's K-line) when the id is known. */
export function TradeBubble({ text, onSelect }: { text: string; onSelect: (t: string) => void }) {
  const [more, setMore] = useState(false);
  const lines = text.split("\n");
  const head = lines[0] ?? "";
  const id = Number(/#(\d+)/.exec(head)?.[1] ?? 0);
  // the instructions to the AI start at the first line that asks it to act
  const cut = lines.findIndex((l, i) => i > 0 && /^(请|这个仓位已结束|（这是)/.test(l));
  const body = (cut > 0 ? lines.slice(1, cut) : lines.slice(1)).join("\n");
  const rest = cut > 0 ? lines.slice(cut).join("\n") : "";
  const bad = /^[❌⚠]/.test(head);
  const tint = bad ? "rgba(247,166,0," : "rgba(188,255,47,";
  return (
    <Pressable onLongPress={() => onSelect(text)} style={{ marginTop: 14, marginBottom: 6, borderRadius: 14, borderWidth: 1, borderColor: tint + "0.4)", backgroundColor: tint + "0.06)", paddingHorizontal: 12, paddingVertical: 10, gap: 6 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Ionicons name="swap-vertical" size={15} color={bad ? colors.warn : colors.primary} />
        <Text style={{ flex: 1, fontSize: fs.lg, fontWeight: "700", color: colors.text }}>{head.replace(/^\S+\s*/, "")}</Text>
      </View>
      {body ? <Text selectable style={[{ fontSize: fs.md, lineHeight: 19, color: colors.textSoft }, tnum]}>{body}</Text> : null}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 12 }}>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>交易执行后自动通知 AI 核对</Text>
        <View style={{ flex: 1 }} />
        {rest ? (
          <Pressable hitSlop={6} onPress={() => { tap(); setMore(!more); }}>
            <Text style={{ fontSize: fs.xs, color: colors.textSoft }}>{more ? "收起指令" : "给 AI 的指令"}</Text>
          </Pressable>
        ) : null}
        {id ? <ProposalLink id={id} /> : null}
      </View>
      {more ? <Text selectable style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textFaint }}>{rest}</Text> : null}
    </Pressable>
  );
}

function ProposalLink({ id }: { id: number }) {
  const p = useProposal(id);
  if (!p) return null;
  return (
    <Pressable hitSlop={6} onPress={() => { tap(); openProposalChart(p); }}>
      <Text style={{ fontSize: fs.xs, color: colors.primary }}>查看提议</Text>
    </Pressable>
  );
}

/** A proposal by id (from a propose_trade step in the chat), live while shown. Nothing without the owner key. */
export function ProposalById({ id }: { id: number }) {
  const owner = useRealtrade((s) => s.owner);
  const p = useProposal(id);
  if (!owner || p === null) return null;
  if (!p) {
    return (
      <View style={{ marginVertical: 10, padding: 12, borderRadius: 14, borderWidth: 1, borderColor: colors.border, backgroundColor: colors.card }}>
        <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>正在加载交易提议 #{id}…</Text>
      </View>
    );
  }
  return <ProposalCard p={p} />;
}

/** Proposal ids created by the propose_trade calls of one step group (tool output JSON with a numeric proposalId). */
export function proposalIds(items: StepItem[]): number[] {
  const out: number[] = [];
  for (const s of items) {
    if (s.t !== "tool" || !s.name.endsWith("propose_trade") || !s.output) continue;
    let id = 0;
    try {
      const v = JSON.parse(s.output) as { proposalId?: unknown };
      if (typeof v?.proposalId === "number") id = v.proposalId;
    } catch {
      id = Number(/"proposalId"\s*:\s*(\d+)/.exec(s.output)?.[1] ?? 0);
    }
    if (id > 0 && !out.includes(id)) out.push(id);
  }
  return out;
}

// ---------- floating K-line ----------

let chartFor: Proposal | null = null;
const chartListeners = new Set<() => void>();
export function openProposalChart(p: Proposal | null) {
  chartFor = p;
  chartListeners.forEach((l) => l());
}

/** The floating K-line over the AI sheet (render once, above the chat and its panes). */
export function ProposalChartHost() {
  const picked = useSyncExternalStore((l) => { chartListeners.add(l); return () => void chartListeners.delete(l); }, () => chartFor);
  const live = useProposal(picked?.id ?? 0);
  const { height } = useWindowDimensions();
  const insets = useSafeAreaInsets();
  useEffect(() => () => openProposalChart(null), []); // closed with the sheet
  if (!picked) return null;
  const p = live ?? picked;
  const close = () => { tap(); openProposalChart(null); };
  return (
    <View style={{ position: "absolute", left: 0, right: 0, top: 0, bottom: 0, zIndex: 30, justifyContent: "center", paddingHorizontal: 8, paddingTop: 12, paddingBottom: insets.bottom + 12, backgroundColor: colors.backdrop }}>
      <Pressable accessibilityLabel="关闭" onPress={close} style={{ position: "absolute", left: 0, right: 0, top: 0, bottom: 0 }} />
      <View style={{ maxHeight: "100%", borderRadius: 16, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.bg, overflow: "hidden" }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingLeft: 14, paddingRight: 8, paddingVertical: 8 }}>
          <Text style={{ flex: 1, fontSize: fs.lg, fontWeight: "600", color: colors.text }}>交易提议 #{p.id} · K 线</Text>
          <Pressable hitSlop={10} accessibilityLabel="关闭" onPress={close} style={({ pressed }) => ({ width: 30, height: 30, borderRadius: 15, alignItems: "center", justifyContent: "center", backgroundColor: colors.cardMuted, opacity: pressed ? 0.7 : 1 })}>
            <Ionicons name="close" size={17} color={colors.textSoft} />
          </Pressable>
        </View>
        <ChartView id={p.instId} proposal={p.id} compact style={{ height: Math.round(Math.max(300, Math.min(480, height * 0.48))) }} />
        <ScrollView style={{ flexGrow: 0, flexShrink: 1 }} contentContainerStyle={{ padding: 8, gap: 8 }}>
          <ProposalCard p={p} inChart />
          {p.events?.length ? (
            <View style={{ paddingHorizontal: 6, gap: 6 }}>
              {p.events.map((e, i) => (
                <View key={i} style={{ flexDirection: "row", gap: 8 }}>
                  <Text style={[{ width: 74, fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(e.ts, true)}</Text>
                  <Text selectable style={{ flex: 1, fontSize: fs.sm, lineHeight: 18, color: colors.textSoft }}>
                    {e.text}
                    {e.by ? <Text style={{ color: colors.textFaint }}>{`  ${e.by}`}</Text> : null}
                  </Text>
                </View>
              ))}
            </View>
          ) : null}
        </ScrollView>
      </View>
    </View>
  );
}

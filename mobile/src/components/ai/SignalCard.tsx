// A signal subscription (lib/signals.ts) as a card, built to scan: ⚡ #id · coin · cadence · status in one
// row; the condition drawn, not written (a price ruler for a zone or level: the live price, the zone or
// level and the distance; chips for field / moverisk / time / alert / plan / script leaves); the plan it
// waits for as 入场 / 止损 / 目标 with the distance from the price (the linked AI plan, else numbers in the
// intent); the intent folded to two lines; subscriber, expiry, fires, 查看 and 取消 (asks first).
// Rendered under the subscribe_signal step in the chat (compact, no chart), in 交易 → 信号, and in the
// detail. The detail is chart first (a compact K-line with only the levels, ChartView `compact`), then
// the card, the fire history and folded sections (script, raw spec); everything scrolls. Inside the AI
// sheet it floats over the chat (SignalHost), like the proposal K-line; elsewhere it is a page sheet
// (SignalSheet).
import { useEffect, useState, useSyncExternalStore, type ReactNode } from "react";
import { Platform, Pressable, ScrollView, Text, View, useWindowDimensions } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { router } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { ChartView } from "@/components/ChartView";
import { CoinIcon } from "@/components/CoinIcon";
import { PillButton, Sheet, Tag, confirm, tap, toast, toastError } from "@/components/ui";
import type { StepItem } from "@/components/ai/AiSteps";
import { ai, openChat } from "@/lib/ai";
import { price, sym, time } from "@/lib/fmt";
import { useLivePrice } from "@/lib/market";
import { useQuery } from "@/lib/query";
import {
  PLAN_STATUS, STATUS_LABEL, cancelSignal, endedByText, intentLevels, leftText, linkedPlan, plansOf, sigNow, statusTone, useSignal, useSignals,
  type PlanLevels, type Signal, type WakeState,
} from "@/lib/signals";
import { colors, fs, tnum } from "@/lib/theme";
import { ScriptSection, evalText } from "./ScriptSignal";

const mono = { fontFamily: Platform.select({ ios: "Menlo", default: "monospace" }) };
const VIOLET_BG = "rgba(155,125,240,0.18)";
const VIOLET_LINE = "rgba(155,125,240,0.45)";

/** Server time, ticking every 30 s while `on` (expiry countdowns are in minutes). */
function useNow(on: boolean) {
  const [now, setNow] = useState(sigNow);
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => setNow(sigNow()), 30_000);
    return () => clearInterval(t);
  }, [on]);
  return now;
}

type Leaf = {
  type?: string; instId?: string; op?: string; level?: number; lo?: number; hi?: number; bar?: string; of?: Leaf[]; every?: string;
  field?: string; value?: number; side?: string; metric?: string; ruleId?: number; atMs?: number; at?: string; planId?: number; state?: string;
};

const EVERY: Record<string, string> = { tick: "实时", "1m": "每 1 分", "5m": "每 5 分", "15m": "每 15 分", "1H": "每小时" };
/** Whether a leaf reads the last trade price (then the default cadence is every tick). */
const readsLast = (l: Leaf): boolean => (l.type === "all" || l.type === "any" ? (l.of ?? []).some(readsLast) : l.type === "zone" || (l.type === "price" && !l.bar));
/** How often the server checks it (server Spec.Every); null for scripts (their tag names the timeframe). */
function cadence(s: Signal): string | null {
  if (s.script) return null;
  const sp = s.spec as Leaf;
  if (sp.every) return EVERY[sp.every] ?? sp.every;
  return readsLast(sp) ? "实时" : "每 1 分";
}

/** Signed distance from the price, "+1.24%". */
const away = (v: number, px?: number) => (px && v ? `${v >= px ? "+" : "−"}${(Math.abs(v / px - 1) * 100).toFixed(2)}%` : "");

// ---------- the condition, drawn ----------

/** A zone (lo < hi) or a level (lo = hi) against the live price: the track spans both with some room;
 *  the zone is a violet band, a level a violet tick, the price a lime dot joined to the nearest edge. */
function Ruler({ instId, lo, hi, op, bar, fallbackPx, active }: { instId: string; lo: number; hi: number; op?: string; bar?: string; fallbackPx?: number; active: boolean }) {
  const px = useLivePrice(instId) ?? fallbackPx;
  const zone = hi > lo;
  const inside = !!px && zone && px >= lo && px <= hi;
  const target = !px ? lo : zone ? (px > hi ? hi : lo) : lo;
  const dist = px && !inside ? (target / px - 1) * 100 : null;
  const vs = [lo, hi, ...(px ? [px] : [])];
  const min = Math.min(...vs);
  const max = Math.max(...vs);
  const pad = (max - min || max * 0.01) * 0.3;
  const pos = (v: number) => `${((v - (min - pad)) / (max - min + 2 * pad)) * 100}%` as const;
  const near = dist != null && Math.abs(dist) <= 0.5;
  const verb = zone ? "进入区间" : op === "below" ? "跌破" : "上破";
  const done = !active;
  return (
    <View style={{ gap: 6 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Ionicons name={zone ? "contract-outline" : op === "below" ? "trending-down" : "trending-up"} size={14} color={colors.violet} />
        <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>{verb}</Text>
        <Text style={[{ fontSize: fs.lg, fontWeight: "700", color: colors.text }, tnum]}>{zone ? `${price(lo)} – ${price(hi)}` : price(lo)}</Text>
        {bar ? <Tag text={`${bar} 收盘`} /> : null}
        <View style={{ flex: 1 }} />
        {done || !px ? null : inside ? (
          <Tag text="已在区间内" tone="warn" />
        ) : dist != null ? (
          <View style={{ flexDirection: "row", alignItems: "center", gap: 2, height: 20, paddingHorizontal: 6, borderRadius: 4, backgroundColor: near ? colors.warnBg : colors.cardMuted }}>
            <Ionicons name={dist > 0 ? "arrow-up" : "arrow-down"} size={11} color={near ? colors.warn : colors.textSoft} />
            <Text style={[{ fontSize: fs.xs, fontWeight: "600", color: near ? colors.warn : colors.textSoft }, tnum]}>还差 {Math.abs(dist).toFixed(2)}%</Text>
          </View>
        ) : null}
      </View>
      <View style={{ height: 18, justifyContent: "center" }}>
        <View style={{ height: 4, borderRadius: 2, backgroundColor: colors.cardMuted }} />
        {zone ? (
          <View style={{ position: "absolute", left: pos(lo), width: `${((hi - lo) / (max - min + 2 * pad)) * 100}%`, top: 3, height: 12, borderRadius: 3, backgroundColor: VIOLET_BG, borderWidth: 1, borderColor: colors.violet }} />
        ) : (
          <View style={{ position: "absolute", left: pos(lo), marginLeft: -1, top: 1, width: 2, height: 16, borderRadius: 1, backgroundColor: colors.violet }} />
        )}
        {px && !inside ? (
          <View style={{ position: "absolute", left: px < target ? pos(px) : pos(target), width: `${(Math.abs(target - px) / (max - min + 2 * pad)) * 100}%`, top: 8, height: 2, backgroundColor: "rgba(188,255,47,0.35)" }} />
        ) : null}
        {px ? <View style={{ position: "absolute", left: pos(px), marginLeft: -5, top: 4, width: 10, height: 10, borderRadius: 5, backgroundColor: colors.primary, borderWidth: 2, borderColor: colors.card }} /> : null}
      </View>
      {px ? (
        <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>
          现价 <Text style={{ color: colors.primary }}>{price(px)}</Text>
        </Text>
      ) : null}
    </View>
  );
}

function Chip({ icon, k, v }: { icon: keyof typeof Ionicons.glyphMap; k: string; v: string }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 5, alignSelf: "flex-start", maxWidth: "100%", paddingHorizontal: 8, paddingVertical: 4, borderRadius: 8, borderWidth: 1, borderColor: VIOLET_LINE, backgroundColor: "rgba(155,125,240,0.08)" }}>
      <Ionicons name={icon} size={12} color={colors.violet} />
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{k}</Text>
      <Text numberOfLines={1} style={[{ flexShrink: 1, fontSize: fs.sm, fontWeight: "600", color: colors.text }, tnum]}>{v}</Text>
    </View>
  );
}

const METRIC: Record<string, string> = { pTail: "P_tail", pBig3: "P_big3" };
const PLAN_STATE: Record<string, string> = { active: "入场", tp1: "到 TP1", done: "结束", stopped: "止损", invalid: "作废", missed: "错过", expired: "到期", settled: "了结" };
const n = (v?: number) => (v == null ? "—" : String(+v.toPrecision(6)));

function LeafView({ l, s }: { l: Leaf; s: Signal }) {
  const coin = l.instId && s.instIds.length > 1 ? `${sym(l.instId)} ` : "";
  const fallback = l.instId && s.progress?.instId === l.instId ? s.progress.px : l.instId === s.instIds[0] ? s.progress?.px : undefined;
  switch (l.type) {
    case "all":
    case "any":
      return (
        <View style={{ gap: 8 }}>
          <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{l.type === "all" ? "以下全部满足" : "任一满足"}</Text>
          {(l.of ?? []).map((c, i) => (
            <View key={i} style={{ paddingLeft: 10, borderLeftWidth: 2, borderColor: VIOLET_LINE }}>
              <LeafView l={c} s={s} />
            </View>
          ))}
        </View>
      );
    case "zone":
      return l.instId && l.lo != null && l.hi != null ? <Ruler instId={l.instId} lo={l.lo} hi={l.hi} fallbackPx={fallback} active={s.status === "active"} /> : null;
    case "price":
      return l.instId && l.level != null ? <Ruler instId={l.instId} lo={l.level} hi={l.level} op={l.op} bar={l.bar} fallbackPx={fallback} active={s.status === "active"} /> : null;
    case "field":
      return <Chip icon="analytics-outline" k={`${coin}因子`} v={`${l.field} ${l.op} ${n(l.value)}`} />;
    case "moverisk":
      return (
        <Chip icon="pulse-outline" k={`${coin}延续风险 · ${l.side === "top" ? "顶部" : "底部"}`}
          v={l.metric === "extended" ? (l.value ? "进入过度延伸" : "离开过度延伸") : `${METRIC[l.metric ?? ""] ?? l.metric} ${l.op} ${n(l.value)}`} />
      );
    case "time":
      return <Chip icon="time-outline" k="到点" v={l.atMs ? time(l.atMs, true) : l.at ?? "—"} />;
    case "alert":
      return <Chip icon="notifications-outline" k={`${coin}告警规则`} v={`#${l.ruleId}`} />;
    case "plan":
      return <Chip icon="flag-outline" k={`计划 #${l.planId}`} v={PLAN_STATE[l.state ?? ""] ?? l.state ?? ""} />;
    case "script":
      return <Chip icon="code-slash-outline" k={`脚本 · ${s.script?.timeframe ?? ""}`} v={s.script?.desc || s.script?.class || "Condition"} />;
    default:
      return null;
  }
}

// ---------- the plan it waits for ----------

function Cell({ k, v, sub, color }: { k: string; v: string; sub?: string; color?: string }) {
  return (
    <View style={{ flex: 1, minWidth: 0, gap: 1 }}>
      <Text numberOfLines={1} style={{ fontSize: fs.xs, color: colors.textFaint }}>{k}</Text>
      <Text numberOfLines={1} adjustsFontSizeToFit minimumFontScale={0.8} style={[{ fontSize: fs.md, fontWeight: "600", color: color ?? colors.text }, tnum]}>{v}</Text>
      {sub ? <Text numberOfLines={1} style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{sub}</Text> : null}
    </View>
  );
}

/** The linked AI plan's levels (else the intent's numbers), each with its distance from the price. */
function PlanGrid({ s }: { s: Signal }) {
  const inst = s.instIds[0] ?? "";
  const wants = !!inst && (s.kind === "chat" || !!s.planId);
  const { data } = useQuery(wants ? `signal-plans:${inst}` : null, () => plansOf(inst), { staleMs: 60_000 });
  const px = useLivePrice(inst) ?? s.progress?.px;
  const plan = linkedPlan(s, data);
  const lv: PlanLevels | null = plan ?? intentLevels(s.intent, px);
  if (!lv) return null;
  const long = lv.side === "long";
  const status = plan ? PLAN_STATUS[plan.status] : undefined;
  return (
    <View style={{ gap: 6, padding: 10, borderRadius: 10, backgroundColor: colors.raised }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Text style={{ fontSize: fs.sm, fontWeight: "600", color: colors.textSoft }}>{plan ? `AI 计划 #${plan.id}` : "计划"}</Text>
        <Tag text={long ? "做多" : "做空"} tone={long ? "good" : "bad"} />
        {plan?.leverage ? <Text style={[{ fontSize: fs.sm, color: colors.textSoft }, tnum]}>{plan.leverage}×</Text> : null}
        <View style={{ flex: 1 }} />
        {status ? <Tag text={status} tone="info" /> : plan ? null : <Text style={{ fontSize: fs.xs, color: colors.textDim }}>据说明</Text>}
      </View>
      <View style={{ flexDirection: "row", gap: 10 }}>
        {lv.entryLo ? (
          <Cell k="入场" v={lv.entryHi && lv.entryHi !== lv.entryLo ? `${price(lv.entryLo)}–${price(lv.entryHi)}` : price(lv.entryLo)} sub={away((lv.entryLo + (lv.entryHi || lv.entryLo)) / 2, px)} />
        ) : null}
        <Cell k="止损" v={price(lv.stop)} sub={away(lv.stop, px)} color={colors.down} />
        {lv.tp1 ? <Cell k="目标" v={price(lv.tp1)} sub={lv.tp2 ? `${away(lv.tp1, px)} · 2: ${price(lv.tp2)}` : away(lv.tp1, px)} color={colors.up} /> : null}
      </View>
    </View>
  );
}

// ---------- folded text ----------

/** Text folded to `lines` with 展开 / 收起 (long text only); never cut for good. */
function Folded({ label, text, lines = 2, color = colors.textSoft }: { label?: string; text: string; lines?: number; color?: string }) {
  const [open, setOpen] = useState(false);
  const long = text.length > lines * 28;
  return (
    <Pressable disabled={!long} onPress={() => { tap(); setOpen(!open); }} style={{ gap: 3 }}>
      <Text selectable={open} numberOfLines={open || !long ? undefined : lines} style={{ fontSize: fs.md, lineHeight: 19, color }}>
        {label ? <Text style={{ color: colors.violet }}>{label} </Text> : null}
        {text}
      </Text>
      {long ? <Text style={{ fontSize: fs.xs, color: colors.textDim }}>{open ? "收起" : "展开"}</Text> : null}
    </Pressable>
  );
}

/** A folded section of the detail. */
function Section({ title, children, open: initial = false }: { title: string; children: ReactNode; open?: boolean }) {
  const [open, setOpen] = useState(initial);
  return (
    <View style={{ borderRadius: 12, backgroundColor: colors.card, borderWidth: 1, borderColor: colors.border, overflow: "hidden" }}>
      <Pressable onPress={() => { tap(); setOpen(!open); }} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 6, paddingHorizontal: 12, paddingVertical: 11, opacity: pressed ? 0.7 : 1 })}>
        <Text style={{ flex: 1, fontSize: fs.md, fontWeight: "600", color: colors.textSoft }}>{title}</Text>
        <Ionicons name={open ? "chevron-up" : "chevron-down"} size={15} color={colors.textFaint} />
      </Pressable>
      {open ? <View style={{ paddingHorizontal: 12, paddingBottom: 12 }}>{children}</View> : null}
    </View>
  );
}

// ---------- the card ----------

export function SignalCard({ s, inDetail, showSubscriber, onOpen, onLeave }: { s: Signal; inDetail?: boolean; showSubscriber?: boolean; onOpen?: () => void; onLeave?: () => void }) {
  const active = s.status === "active";
  const caps = useSignals((x) => x.caps);
  const now = useNow(active);
  const [busy, setBusy] = useState(false);
  const open = onOpen ?? (() => openSignal(s));
  const every = cadence(s);

  const cancel = () =>
    confirm("取消这个信号订阅", `#${s.id} ${s.desc}\n取消后条件成立也不会再唤醒 AI。`, "取消订阅", () => {
      setBusy(true);
      cancelSignal(s.id)
        .then(({ s: r, ended }) => toast(ended ? "这个订阅已经结束" : "已取消订阅", ended ? STATUS_LABEL[r.status] : `#${r.id}`))
        .catch((e) => toastError("未能取消", e))
        .finally(() => setBusy(false));
    }, true);

  return (
    <View style={{ marginVertical: inDetail ? 0 : 8, borderRadius: 14, borderWidth: 1, borderColor: active ? VIOLET_LINE : colors.border, backgroundColor: colors.card, overflow: "hidden" }}>
      {/* head: ⚡ #id · coin · cadence · script … status */}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6, paddingHorizontal: 12, paddingTop: 11 }}>
        <Ionicons name="flash" size={14} color={active ? colors.violet : colors.textFaint} />
        <Text style={[{ fontSize: fs.sm, fontWeight: "600", color: colors.textSoft }, tnum]}>#{s.id}</Text>
        {s.instIds.slice(0, 2).map((id) => (
          <Pressable key={id} disabled={inDetail} onPress={() => { tap(); open(); }} hitSlop={6} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 4, paddingLeft: 3, paddingRight: 6, paddingVertical: 2, borderRadius: 8, backgroundColor: pressed ? "rgba(188,255,47,0.22)" : colors.primarySoft })}>
            <CoinIcon instId={id} size={15} />
            <Text style={{ fontSize: fs.md, fontWeight: "700", color: colors.primary }}>{sym(id)}</Text>
          </Pressable>
        ))}
        {every ? <Tag text={every} /> : null}
        {s.script ? <Tag text={`脚本 · ${s.script.timeframe}`} /> : null}
        <View style={{ flex: 1 }} />
        <Tag text={STATUS_LABEL[s.status] ?? s.status} tone={statusTone(s.status)} />
      </View>

      <View style={{ paddingHorizontal: 12, paddingTop: 8, gap: 10 }}>
        {/* one-line description, then the condition drawn */}
        <Text selectable={inDetail} numberOfLines={inDetail ? undefined : 1} style={{ fontSize: fs.sm, color: colors.textFaint }}>{s.desc}</Text>
        <LeafView l={s.spec as Leaf} s={s} />
        {s.script?.last && active ? (
          <Text numberOfLines={inDetail ? undefined : 1} style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>最近检查 {time(s.script.last.at, true)} · {s.script.last.fire == null ? "无结论" : s.script.last.fire ? "成立" : "未成立"}{evalText(s.script.last) ? ` · ${evalText(s.script.last)}` : ""}</Text>
        ) : null}
        {active && s.script?.errors ? (
          <Text numberOfLines={inDetail ? undefined : 2} style={{ fontSize: fs.sm, lineHeight: 18, color: colors.warn }}>
            脚本连续出错 {s.script.errors} 次（{s.script.errors >= (caps?.scripts?.maxErrors ?? 3) - 1 ? "再错一次就会停止" : "连续出错过多会停止"}）：{s.script.lastError}
          </Text>
        ) : null}
        <PlanGrid s={s} />
        {s.intent ? <Folded label="触发后" text={s.intent} /> : null}
        {!active && (s.reason || s.endedBy) ? (
          <Text style={[{ fontSize: fs.sm, lineHeight: 18, color: colors.textFaint }, tnum]}>
            {s.status === "fired" ? "已用完触发次数" : s.status === "expired" ? "到期未触发" : `${endedByText(s.endedBy)}取消${s.reason ? `：${s.reason}` : ""}`}
            {` · ${time(s.statusAt, true)}`}
          </Text>
        ) : null}
      </View>

      {/* footer: subscriber; expiry · fires … 查看 取消 */}
      <View style={{ marginTop: 10, paddingHorizontal: 12, paddingTop: 9, paddingBottom: 11, gap: 8, borderTopWidth: 1, borderColor: colors.border }}>
        {showSubscriber ? (
          <Pressable disabled={!s.chatId} hitSlop={4} onPress={() => { tap(); goToChat(s.chatId!, onLeave); }} style={{ flexDirection: "row", alignItems: "center", gap: 5 }}>
            <Ionicons name={s.kind === "chat" ? "chatbubble-ellipses-outline" : "person-outline"} size={13} color={s.chatId ? colors.primary : colors.textFaint} />
            <Text numberOfLines={1} style={{ flexShrink: 1, fontSize: fs.sm, color: s.chatId ? colors.primary : colors.textFaint }}>{s.subscriber}</Text>
            {s.chatId ? <Ionicons name="chevron-forward" size={12} color={colors.primary} /> : null}
          </Pressable>
        ) : null}
        <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 3 }}>
            <Ionicons name="time-outline" size={13} color={colors.textFaint} />
            <Text style={[{ fontSize: fs.sm, color: active ? colors.textSoft : colors.textFaint }, tnum]}>{active ? `剩 ${leftText(s.expiresAt - now)}` : time(s.created, true)}</Text>
          </View>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 3 }}>
            <Ionicons name="flash-outline" size={13} color={colors.textFaint} />
            <Text style={[{ fontSize: fs.sm, color: s.fireCount ? colors.text : colors.textFaint }, tnum]}>{s.fireCount}/{s.maxFires}</Text>
          </View>
          <View style={{ flex: 1 }} />
          {inDetail ? null : <PillButton small variant="soft" icon="stats-chart-outline" label="查看" onPress={open} />}
          {active ? <PillButton small variant="ghost" label="取消" loading={busy} disabled={busy} onPress={cancel} /> : null}
        </View>
      </View>
    </View>
  );
}

/** A subscription by id (from a subscribe_signal step in the chat), live while shown. Nothing without the owner key. */
export function SignalById({ id }: { id: number }) {
  const owner = useSignals((x) => x.owner);
  const s = useSignal(id);
  if (s === null) return null;
  if (!s) {
    if (!owner) return null;
    return (
      <View style={{ marginVertical: 8, padding: 12, borderRadius: 14, borderWidth: 1, borderColor: colors.border, backgroundColor: colors.card }}>
        <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>正在加载信号 #{id}…</Text>
      </View>
    );
  }
  return <SignalCard s={s} />;
}

/** Subscription ids created by the subscribe_signal calls of one step group (tool output JSON with a numeric signalId). */
export function signalIds(items: StepItem[]): number[] {
  const out: number[] = [];
  for (const it of items) {
    if (it.t !== "tool" || !it.name.endsWith("subscribe_signal") || it.name.endsWith("unsubscribe_signal") || !it.output) continue;
    let id = 0;
    try {
      const v = JSON.parse(it.output) as { signalId?: unknown };
      if (typeof v?.signalId === "number") id = v.signalId;
    } catch {
      id = Number(/"signalId"\s*:\s*(\d+)/.exec(it.output)?.[1] ?? 0);
    }
    if (id > 0 && !out.includes(id)) out.push(id);
  }
  return out;
}

/** Open a chat in the AI sheet (from 交易 → 信号, or another chat's signal). leave closes a page sheet first. */
export function goToChat(id: number, leave?: () => void) {
  openSignal(null);
  if (ai.chatId !== id) void openChat(id).catch((e) => toastError("未能打开对话", e));
  if (!leave) return router.navigate("/ai");
  leave();
  setTimeout(() => router.navigate("/ai"), 450); // after the page sheet is down
}

// ---------- detail ----------

const WAKE_SHORT: Record<WakeState, [string, "good" | "warn" | "bad" | undefined]> = {
  started: ["已唤醒", "good"], queued: ["排队中", "warn"], skipped: ["未唤醒", undefined], failed: ["唤醒失败", "bad"],
};

/** One fire: time · price … wake state · the chat it woke; the matched values on one line (tap for all). */
function FireRow({ f, onLeave }: { f: NonNullable<Signal["history"]>[number]; onLeave?: () => void }) {
  const [open, setOpen] = useState(false);
  const [label, tone] = WAKE_SHORT[f.wake] ?? [f.wake, undefined];
  return (
    <Pressable onPress={() => { tap(); setOpen(!open); }} style={{ gap: 3, paddingVertical: 8, borderTopWidth: 1, borderColor: colors.border }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Text style={[{ fontSize: fs.sm, color: colors.textSoft }, tnum]}>{time(f.ts, true)}</Text>
        {f.px ? <Text style={[{ fontSize: fs.sm, fontWeight: "600", color: colors.text }, tnum]}>{price(f.px)}</Text> : null}
        <View style={{ flex: 1 }} />
        <Tag text={label} tone={tone} />
        {f.wakeChat ? (
          <Pressable hitSlop={6} onPress={() => { tap(); goToChat(f.wakeChat!, onLeave); }} style={{ flexDirection: "row", alignItems: "center", gap: 2 }}>
            <Ionicons name="chatbubble-ellipses-outline" size={13} color={colors.primary} />
            <Text style={[{ fontSize: fs.sm, color: colors.primary }, tnum]}>#{f.wakeChat}</Text>
            <Ionicons name="chevron-forward" size={12} color={colors.primary} />
          </Pressable>
        ) : null}
      </View>
      {f.matched ? <Text selectable={open} numberOfLines={open ? undefined : 1} style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{f.matched}</Text> : null}
      {f.error ? <Text selectable numberOfLines={open ? undefined : 1} style={{ fontSize: fs.xs, color: colors.warn }}>{f.error}</Text> : null}
    </Pressable>
  );
}

/** Chart height in the detail: about half the screen, at least 300. */
export const detailChartHeight = (screen: number) => Math.round(Math.max(300, Math.min(520, screen * 0.48)));

/** The detail body: the compact K-line with the levels, the card, the fire history, folded script and spec. */
export function SignalDetail({ s, chartHeight, onLeave, onTouchActive }: { s: Signal; chartHeight: number; onLeave?: () => void; onTouchActive?: (on: boolean) => void }) {
  const inst = s.instIds[0];
  const fires = s.history ?? [];
  return (
    <View style={{ gap: 10 }}>
      {inst ? <ChartView id={inst} signal={s.id} compact onTouchActive={onTouchActive} style={{ height: chartHeight, borderRadius: 12 }} /> : null}
      <SignalCard s={s} inDetail showSubscriber onLeave={onLeave} />
      <View style={{ paddingHorizontal: 12, paddingTop: 10, paddingBottom: 4, borderRadius: 12, backgroundColor: colors.card, borderWidth: 1, borderColor: colors.border }}>
        <Text style={{ fontSize: fs.md, fontWeight: "600", color: colors.textSoft, paddingBottom: 8 }}>触发记录{fires.length ? ` · ${fires.length}` : ""}</Text>
        {fires.length ? fires.map((f) => <FireRow key={f.id} f={f} onLeave={onLeave} />) : <Text style={{ fontSize: fs.sm, color: colors.textFaint, paddingBottom: 8 }}>还没有触发过</Text>}
      </View>
      {s.script ? (
        <Section title={`脚本 · ${s.script.class}`}>
          <ScriptSection x={s.script} every={(s.spec as { every?: string }).every} bare />
        </Section>
      ) : null}
      <Section title="条件定义">
        <View style={{ gap: 6 }}>
          <Text selectable style={[mono, { fontSize: 11, lineHeight: 16, color: colors.textFaint }]}>{JSON.stringify(s.spec, (k, v) => (k === "code" || k === "script" ? "…" : v), 1)}</Text>
          <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>
            创建 {time(s.created, true)} · 到期 {time(s.expiresAt, true)} · 冷却 {s.cooldownMin} 分钟{s.planId ? ` · 关联计划 #${s.planId}` : ""}
          </Text>
        </View>
      </Section>
    </View>
  );
}

/** The detail as a page sheet (交易 → 信号). The page doesn't scroll while a finger is on the chart. */
export function SignalSheet({ id, onClose }: { id: number; onClose: () => void }) {
  const s = useSignal(id);
  const { height } = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const [lock, setLock] = useState(false);
  return (
    <Sheet visible={!!id} onClose={onClose} title={id ? `信号 #${id}` : ""} fill>
      <ScrollView scrollEnabled={!lock} style={{ flex: 1, marginHorizontal: -16 }} contentContainerStyle={{ paddingHorizontal: 12, paddingBottom: insets.bottom + 20 }} showsVerticalScrollIndicator={false}>
        {s ? <SignalDetail s={s} chartHeight={detailChartHeight(height)} onLeave={onClose} onTouchActive={setLock} /> : <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>加载中…</Text>}
      </ScrollView>
    </Sheet>
  );
}

// The detail floating over the AI sheet (a Modal pushed from the form sheet takes no touches).
let detailFor: Signal | null = null;
const detailListeners = new Set<() => void>();
export function openSignal(s: Signal | null) {
  detailFor = s;
  detailListeners.forEach((l) => l());
}

/** Render once in the AI sheet, above the chat and its panes. */
export function SignalHost() {
  const picked = useSyncExternalStore((l) => { detailListeners.add(l); return () => void detailListeners.delete(l); }, () => detailFor);
  const live = useSignal(picked?.id ?? 0);
  const { height } = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const [lock, setLock] = useState(false);
  useEffect(() => () => openSignal(null), []); // closed with the sheet
  if (!picked) return null;
  const s = live ?? picked;
  const close = () => { tap(); openSignal(null); };
  return (
    <View style={{ position: "absolute", left: 0, right: 0, top: 0, bottom: 0, zIndex: 30, justifyContent: "center", paddingHorizontal: 8, paddingTop: 12, paddingBottom: insets.bottom + 12, backgroundColor: colors.backdrop }}>
      <Pressable accessibilityLabel="关闭" onPress={close} style={{ position: "absolute", left: 0, right: 0, top: 0, bottom: 0 }} />
      <View style={{ maxHeight: "100%", borderRadius: 16, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.bg, overflow: "hidden" }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingLeft: 14, paddingRight: 8, paddingVertical: 8 }}>
          <Text style={{ flex: 1, fontSize: fs.lg, fontWeight: "600", color: colors.text }}>信号 #{s.id}</Text>
          <Pressable hitSlop={10} accessibilityLabel="关闭" onPress={close} style={({ pressed }) => ({ width: 30, height: 30, borderRadius: 15, alignItems: "center", justifyContent: "center", backgroundColor: colors.cardMuted, opacity: pressed ? 0.7 : 1 })}>
            <Ionicons name="close" size={17} color={colors.textSoft} />
          </Pressable>
        </View>
        <ScrollView scrollEnabled={!lock} style={{ flexGrow: 0, flexShrink: 1 }} contentContainerStyle={{ padding: 8, paddingBottom: 14 }}>
          <SignalDetail s={s} chartHeight={detailChartHeight(height)} onTouchActive={setLock} />
        </ScrollView>
      </View>
    </View>
  );
}

/** The ⚡ message the server put in the chat when a signal fired (not the owner's words). */
export function SignalBubble({ text, onSelect }: { text: string; onSelect: (t: string) => void }) {
  const [more, setMore] = useState(false);
  const lines = text.split("\n");
  const head = lines[0] ?? "";
  const id = Number(/#(\d+)/.exec(head)?.[1] ?? 0);
  // the agent's instructions follow a blank line: shown on demand
  const cut = lines.findIndex((l) => !l.trim());
  const body = (cut > 0 ? lines.slice(1, cut) : lines.slice(1)).join("\n");
  const rest = cut > 0 ? lines.slice(cut + 1).join("\n") : "";
  return (
    <Pressable onLongPress={() => onSelect(text)} style={{ marginTop: 14, marginBottom: 6, borderRadius: 14, borderWidth: 1, borderColor: "rgba(155,125,240,0.45)", backgroundColor: "rgba(155,125,240,0.08)", paddingHorizontal: 12, paddingVertical: 10, gap: 6 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Ionicons name="flash" size={15} color={colors.violet} />
        <Text style={{ flex: 1, fontSize: fs.lg, fontWeight: "700", color: colors.text }}>{head.replace(/^⚡\s*/, "")}</Text>
      </View>
      {body ? <Text selectable style={{ fontSize: fs.md, lineHeight: 19, color: colors.textSoft }}>{body}</Text> : null}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 12 }}>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>信号自动唤醒 AI 重新评估</Text>
        <View style={{ flex: 1 }} />
        {rest ? (
          <Pressable hitSlop={6} onPress={() => { tap(); setMore(!more); }}>
            <Text style={{ fontSize: fs.xs, color: colors.textSoft }}>{more ? "收起指令" : "给 AI 的指令"}</Text>
          </Pressable>
        ) : null}
        {id ? <SignalLink id={id} /> : null}
      </View>
      {more ? <Text selectable style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textFaint }}>{rest}</Text> : null}
    </Pressable>
  );
}

function SignalLink({ id }: { id: number }) {
  const s = useSignal(id);
  if (!s) return null;
  return (
    <Pressable hitSlop={6} onPress={() => { tap(); openSignal(s); }}>
      <Text style={{ fontSize: fs.xs, color: colors.primary }}>查看订阅</Text>
    </Pressable>
  );
}

// A signal subscription (lib/signals.ts) as a card: the condition, the coin, the live distance to firing
// (现价 139.2 · 距 140.2 还差 0.7%), expiry countdown, fires, status, and what the subscriber means to
// do. 查看 opens the detail (K-line with the levels drawn, the full spec, intent and fire history); 取消
// asks first. Rendered under the subscribe_signal step in the chat, in 交易 → 信号, and in the detail.
// Inside the AI sheet the detail floats over the chat (SignalHost), like the proposal K-line; elsewhere
// it is a page sheet (SignalSheet). Script signals add their timeframe tag, the newest check (as the
// progress line) and, in the detail, the code, replay and live checks (ScriptSignal.tsx).
import { useEffect, useState, useSyncExternalStore } from "react";
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
import {
  STATUS_LABEL, WAKE_LABEL, cancelSignal, endedByText, leftText, sigNow, statusTone, useSignal, useSignals, type Signal,
} from "@/lib/signals";
import { colors, fs, tnum } from "@/lib/theme";
import { ScriptSection } from "./ScriptSignal";

const mono = { fontFamily: Platform.select({ ios: "Menlo", default: "monospace" }) };

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

type Leaf = { type?: string; instId?: string; op?: string; level?: number; lo?: number; hi?: number; bar?: string; of?: Leaf[] };
/** The first price / zone condition: what "distance to firing" is measured against. */
function firstTarget(spec: Leaf): Leaf | null {
  if (spec.type === "all" || spec.type === "any") {
    for (const c of spec.of ?? []) {
      const t = firstTarget(c);
      if (t) return t;
    }
    return null;
  }
  return spec.type === "price" || spec.type === "zone" ? spec : null;
}

/** 现价 · 距目标 %, from the live price when the stream has it, else the server's reading. */
function useDistance(s: Signal): { text: string; pct: number | null } | null {
  const t = s.status === "active" ? firstTarget(s.spec as Leaf) : null;
  const live = useLivePrice(t?.instId ?? "");
  if (s.status !== "active") return null;
  const px = live ?? s.progress?.px;
  if (!t || !px) return s.progress ? { text: s.progress.text, pct: s.progress.pct >= 0 ? s.progress.pct : null } : null;
  const head = `现价 ${price(px)}`;
  if (t.type === "zone" && t.lo != null && t.hi != null) {
    if (px >= t.lo && px <= t.hi) return { text: `${head} · 已在区间内`, pct: 0 };
    const target = px > t.hi ? t.hi : t.lo;
    const pct = (Math.abs(target - px) / px) * 100;
    return { text: `${head} · 距 ${price(target)} 还差 ${pct.toFixed(2)}%`, pct };
  }
  if (t.level == null) return null;
  const pct = (Math.abs(t.level - px) / px) * 100;
  return { text: `${head} · 距 ${price(t.level)} 还差 ${pct.toFixed(2)}%${t.bar ? `（看 ${t.bar} 收盘）` : ""}`, pct };
}

/** Distance 0–3% as a closeness bar (full = at the trigger). */
function Closeness({ pct }: { pct: number }) {
  const f = Math.max(0.04, Math.min(1, 1 - pct / 3));
  const c = pct <= 0.5 ? colors.warn : colors.primary;
  return (
    <View style={{ height: 3, borderRadius: 2, backgroundColor: colors.cardMuted, overflow: "hidden" }}>
      <View style={{ width: `${f * 100}%`, height: 3, backgroundColor: c }} />
    </View>
  );
}

export function SignalCard({ s, inDetail, showSubscriber, onOpen, onLeave }: { s: Signal; inDetail?: boolean; showSubscriber?: boolean; onOpen?: () => void; onLeave?: () => void }) {
  const active = s.status === "active";
  const caps = useSignals((x) => x.caps);
  const now = useNow(active);
  const dist = useDistance(s);
  const [busy, setBusy] = useState(false);
  const [more, setMore] = useState(false);
  const open = onOpen ?? (() => openSignal(s));

  const cancel = () =>
    confirm("取消这个信号订阅", `#${s.id} ${s.desc}\n取消后条件成立也不会再唤醒 AI。`, "取消订阅", () => {
      setBusy(true);
      cancelSignal(s.id)
        .then(({ s: r, ended }) => toast(ended ? "这个订阅已经结束" : "已取消订阅", ended ? STATUS_LABEL[r.status] : `#${r.id}`))
        .catch((e) => toastError("未能取消", e))
        .finally(() => setBusy(false));
    }, true);

  const longIntent = s.intent.length > 80;
  return (
    <View style={{ marginVertical: inDetail ? 0 : 8, borderRadius: 14, borderWidth: 1, borderColor: active ? "rgba(155,125,240,0.45)" : colors.border, backgroundColor: colors.card, overflow: "hidden" }}>
      {/* head: ⚡ #id · coins … status */}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 7, paddingHorizontal: 12, paddingTop: 11 }}>
        <Ionicons name="flash" size={14} color={active ? colors.violet : colors.textFaint} />
        <Text style={[{ fontSize: fs.sm, fontWeight: "600", color: colors.textSoft }, tnum]}>信号 #{s.id}</Text>
        {s.instIds.slice(0, 2).map((id) => (
          <Pressable key={id} disabled={inDetail} onPress={() => { tap(); open(); }} hitSlop={6} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 4, paddingLeft: 3, paddingRight: 6, paddingVertical: 2, borderRadius: 8, backgroundColor: pressed ? "rgba(188,255,47,0.22)" : colors.primarySoft })}>
            <CoinIcon instId={id} size={15} />
            <Text style={{ fontSize: fs.md, fontWeight: "700", color: colors.primary }}>{sym(id)}</Text>
          </Pressable>
        ))}
        {s.script ? <Tag text={`脚本 · ${s.script.timeframe}`} /> : null}
        <View style={{ flex: 1 }} />
        <Tag text={STATUS_LABEL[s.status] ?? s.status} tone={statusTone(s.status)} />
      </View>

      {/* the condition and how close it is */}
      <View style={{ paddingHorizontal: 12, paddingTop: 8, gap: 6 }}>
        <Text selectable style={{ fontSize: fs.xl, fontWeight: "600", lineHeight: 22, color: colors.text }}>{s.desc}</Text>
        {dist ? (
          <View style={{ gap: 5 }}>
            <Text style={[{ fontSize: fs.md, color: colors.textSoft }, tnum]}>{dist.text}</Text>
            {dist.pct != null ? <Closeness pct={dist.pct} /> : null}
          </View>
        ) : null}
        {active && s.script?.errors ? (
          <Text numberOfLines={inDetail ? undefined : 2} style={{ fontSize: fs.sm, lineHeight: 18, color: colors.warn }}>
            脚本连续出错 {s.script.errors} 次（{s.script.errors >= (caps?.scripts?.maxErrors ?? 3) - 1 ? "再错一次就会停止" : "连续出错过多会停止"}）：{s.script.lastError}
          </Text>
        ) : null}
        {s.intent ? (
          <Pressable disabled={!longIntent} onPress={() => { tap(); setMore(!more); }}>
            <Text selectable={more} numberOfLines={more || !longIntent || inDetail ? undefined : 2} style={{ fontSize: fs.md, lineHeight: 19, color: colors.textFaint }}>
              <Text style={{ color: colors.violet }}>触发后 </Text>
              {s.intent}
            </Text>
          </Pressable>
        ) : null}
        {showSubscriber ? (
          <Pressable disabled={!s.chatId} hitSlop={4} onPress={() => { tap(); goToChat(s.chatId!, onLeave); }} style={{ flexDirection: "row", alignItems: "center", gap: 4, alignSelf: "flex-start" }}>
            <Ionicons name={s.kind === "chat" ? "chatbubble-ellipses-outline" : "person-outline"} size={13} color={s.chatId ? colors.primary : colors.textFaint} />
            <Text numberOfLines={1} style={{ fontSize: fs.sm, color: s.chatId ? colors.primary : colors.textFaint, maxWidth: 280 }}>{s.subscriber}</Text>
          </Pressable>
        ) : null}
        {!active && (s.reason || s.endedBy) ? (
          <Text style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textFaint }}>
            {s.status === "fired" ? "已用完触发次数" : s.status === "expired" ? "到期未触发" : `${endedByText(s.endedBy)}取消${s.reason ? `：${s.reason}` : ""}`}
            {` · ${time(s.statusAt, true)}`}
          </Text>
        ) : null}
      </View>

      {/* footer: expiry · fires … 查看 取消 */}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, paddingTop: 10, paddingBottom: 12 }}>
        <Text style={[{ fontSize: fs.sm, color: active ? colors.textSoft : colors.textFaint }, tnum]}>
          {active ? `剩 ${leftText(s.expiresAt - now)}` : time(s.created, true)}
          {` · 触发 ${s.fireCount}/${s.maxFires}`}
        </Text>
        <View style={{ flex: 1 }} />
        {inDetail ? null : <PillButton small variant="soft" icon="stats-chart-outline" label="查看" onPress={open} />}
        {active ? <PillButton small variant="ghost" label="取消" loading={busy} disabled={busy} onPress={cancel} /> : null}
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

/** The detail body: K-line with the levels, the card, the raw spec and the fire history. */
export function SignalDetail({ s, chartHeight, onLeave }: { s: Signal; chartHeight: number; onLeave?: () => void }) {
  const inst = s.instIds[0];
  return (
    <View style={{ gap: 10 }}>
      {inst ? <ChartView id={inst} signal={s.id} style={{ height: chartHeight, borderRadius: 12 }} /> : null}
      <SignalCard s={s} inDetail showSubscriber onLeave={onLeave} />
      {s.script ? <ScriptSection x={s.script} every={(s.spec as { every?: string }).every} /> : null}
      <View style={{ gap: 6, paddingHorizontal: 4 }}>
        <Text style={{ fontSize: fs.sm, fontWeight: "600", color: colors.textSoft }}>触发记录</Text>
        {!s.history?.length ? (
          <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>还没有触发过</Text>
        ) : (
          s.history.map((f) => (
            <View key={f.id} style={{ gap: 3, paddingVertical: 6, borderTopWidth: 1, borderColor: colors.border }}>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(f.ts, true)}</Text>
                {f.px ? <Text style={[{ fontSize: fs.xs, color: colors.textSoft }, tnum]}>{price(f.px)}</Text> : null}
                <View style={{ flex: 1 }} />
                <Tag text={WAKE_LABEL[f.wake] ?? f.wake} tone={f.wake === "started" ? "good" : f.wake === "queued" ? "warn" : f.wake === "failed" ? "bad" : undefined} />
              </View>
              <Text selectable style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textSoft }}>{f.matched}</Text>
              {f.error ? <Text selectable style={{ fontSize: fs.sm, color: colors.warn }}>{f.error}</Text> : null}
              {f.wakeChat ? (
                <Pressable hitSlop={4} onPress={() => { tap(); goToChat(f.wakeChat!, onLeave); }} style={{ flexDirection: "row", alignItems: "center", gap: 4, alignSelf: "flex-start" }}>
                  <Ionicons name="chatbubble-ellipses-outline" size={13} color={colors.primary} />
                  <Text style={{ fontSize: fs.sm, color: colors.primary }}>AI 的评估：对话 #{f.wakeChat}</Text>
                </Pressable>
              ) : null}
            </View>
          ))
        )}
      </View>
      <View style={{ gap: 4, paddingHorizontal: 4, paddingBottom: 6 }}>
        <Text style={{ fontSize: fs.sm, fontWeight: "600", color: colors.textSoft }}>条件定义</Text>
        <Text selectable style={[mono, { fontSize: 11, lineHeight: 16, color: colors.textFaint }]}>{JSON.stringify(s.spec, (k, v) => (k === "code" || k === "script" ? "…" : v), 1)}</Text>
        <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>
          创建 {time(s.created, true)} · 到期 {time(s.expiresAt, true)} · 冷却 {s.cooldownMin} 分钟{s.planId ? ` · 关联计划 #${s.planId}` : ""}
        </Text>
      </View>
    </View>
  );
}

/** The detail as a page sheet (交易 → 信号). */
export function SignalSheet({ id, onClose }: { id: number; onClose: () => void }) {
  const s = useSignal(id);
  const { height } = useWindowDimensions();
  return (
    <Sheet visible={!!id} onClose={onClose} title={id ? `信号 #${id}` : ""} scroll>
      {s ? <SignalDetail s={s} chartHeight={Math.round(Math.min(420, height * 0.42))} onLeave={onClose} /> : <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>加载中…</Text>}
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
        <ScrollView style={{ flexGrow: 0, flexShrink: 1 }} contentContainerStyle={{ padding: 8 }}>
          <SignalDetail s={s} chartHeight={Math.round(Math.min(400, height * 0.4))} />
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

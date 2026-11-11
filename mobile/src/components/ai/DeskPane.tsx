// The trading desk (交易台) in the AI sheet. The desk is one standing chat the server runs by itself, so
// it looks different from an ordinary conversation everywhere it shows:
//   DeskCard   — pinned at the top of the history list (cyan accent, pulse, next run), never deletable
//   DeskBar    — the strip under the header while its chat is on screen (state, next run, run now)
//   DeskBubble — a run's brief in the chat (what fired; the instructions for the agent fold away)
//   DeskPane   — 概览 / 决策 / 记忆 / 策略: status and record, the decision journal with what happened
//                afterwards, the long-term memory, and the schedule, pacing and scans
// Data: lib/desk.ts (server internal/autopilot).
import { useEffect, useState, type ReactNode } from "react";
import { Animated, Easing, KeyboardAvoidingView, Platform, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { colors, fs, radius } from "@/lib/theme";
import { sym, time } from "@/lib/fmt";
import { openChat } from "@/lib/ai";
import { useQuery } from "@/lib/query";
import {
  actionText, clockAt, convictionText, deskApi, inText, memoryKindText, refreshDesk, runDeskNow, saveDesk, strategyText, triggerText,
  useDesk, verdictText, type DeskBucket, type DeskConfig, type DeskDecision, type DeskMemory, type DeskOverview, type DeskRun, type DeskStrategy,
} from "@/lib/desk";
import { Chip, EmptyState, ErrorBox, Loading, PillButton, Pull, Segmented, Tag, confirm, tap, toast, toastError, styles } from "@/components/ui";
import { NumInput, Toggle } from "@/components/Form";

// the desk's own accent: everything of the standing session is cyan, ordinary chats stay lime
export const DESK = "#2fe0ff";
const DESK_SOFT = "rgba(47,224,255,0.08)";
const DESK_LINE = "rgba(47,224,255,0.4)";

/** A dot that breathes while the desk is on (a run in flight breathes faster). */
function Pulse({ on, fast, size = 8 }: { on: boolean; fast?: boolean; size?: number }) {
  const [a] = useState(() => new Animated.Value(0));
  useEffect(() => {
    if (!on) return;
    const l = Animated.loop(Animated.timing(a, { toValue: 1, duration: fast ? 900 : 2600, easing: Easing.inOut(Easing.quad), useNativeDriver: true }));
    l.start();
    return () => l.stop();
  }, [a, on, fast]);
  const c = on ? DESK : colors.textDim;
  return (
    <View style={{ width: size * 2, height: size * 2, alignItems: "center", justifyContent: "center" }}>
      {on ? <Animated.View style={{ position: "absolute", width: size * 2, height: size * 2, borderRadius: size, backgroundColor: c, opacity: a.interpolate({ inputRange: [0, 0.5, 1], outputRange: [0.35, 0, 0.35] }), transform: [{ scale: a.interpolate({ inputRange: [0, 0.5, 1], outputRange: [0.5, 1, 0.5] }) }] }} /> : null}
      <View style={{ width: size, height: size, borderRadius: size / 2, backgroundColor: c }} />
    </View>
  );
}

/** One line on what the desk is doing now. */
function stateLine(d: DeskOverview, short = false) {
  const s = d.status;
  if (!s.enabled) return "未启用";
  if (s.running) return `运行中 · ${s.running.label}`;
  const parts: string[] = [];
  if (s.nextScheduleAt) parts.push(short ? `下次 ${clockAt(s.nextScheduleAt)}` : `下次巡检 ${clockAt(s.nextScheduleAt)}（${inText(s.nextScheduleAt)}）`);
  if (s.pending) parts.push(`${s.pending} 条信号待处理`);
  return parts.join(" · ") || "待命";
}

/** The desk's entry at the top of the history list. */
export function DeskCard({ active, onOpenChat, onOpenDesk }: { active: boolean; onOpenChat: (id: number) => void; onOpenDesk: () => void }) {
  const { data, owner, error } = useDesk();
  if (!owner && !error) return null;
  if (!data) return error ? <Text style={[styles.note, { margin: 10 }]}>{error}</Text> : null;
  const s = data.status;
  const open = () => (s.chatId ? onOpenChat(s.chatId) : onOpenDesk());
  return (
    <Pressable onPress={() => { tap(); open(); }} style={({ pressed }) => ({ marginBottom: 10, borderRadius: 14, borderWidth: 1, borderColor: active ? DESK : DESK_LINE, backgroundColor: pressed ? "rgba(47,224,255,0.14)" : DESK_SOFT, padding: 12, gap: 8 })}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Pulse on={s.enabled} fast={!!s.running} />
        <Text style={{ flex: 1, fontSize: fs.xl, fontWeight: "700", color: colors.text }}>交易台 · 全自动</Text>
        <View style={{ paddingHorizontal: 7, height: 20, borderRadius: 10, justifyContent: "center", borderWidth: 1, borderColor: DESK_LINE }}>
          <Text style={{ fontSize: fs.xs, fontWeight: "600", color: DESK }}>常驻会话</Text>
        </View>
      </View>
      <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>{stateLine(data)}</Text>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
        <Text style={{ flex: 1, fontSize: fs.xs, color: colors.textFaint }}>
          {s.enabled ? `24 小时：${s.runs24h} 轮 · ${s.decisions24h} 笔决策${s.cost24hUsd ? ` · $${s.cost24hUsd.toFixed(2)}` : ""}` : "信号触发和定时巡检都在这一个会话里，你也可以直接对话"}
        </Text>
        <Pressable hitSlop={8} onPress={() => { tap(); onOpenDesk(); }} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 4, paddingHorizontal: 10, height: 28, borderRadius: 14, backgroundColor: pressed ? colors.cardMuted : colors.raised })}>
          <Ionicons name="options-outline" size={13} color={colors.text} />
          <Text style={{ fontSize: fs.sm, color: colors.text }}>{s.enabled ? "日志与设置" : "去启用"}</Text>
        </Pressable>
      </View>
    </Pressable>
  );
}

/** The strip under the header while the desk's chat is on screen. */
export function DeskBar({ onOpenDesk }: { onOpenDesk: () => void }) {
  const { data } = useDesk();
  const [busy, setBusy] = useState(false);
  const [, setTick] = useState(0);
  useEffect(() => { const t = setInterval(() => setTick((n) => n + 1), 30_000); return () => clearInterval(t); }, []); // the countdown
  if (!data) return null;
  const s = data.status;
  const run = () => {
    if (busy) return;
    setBusy(true);
    runDeskNow().catch((e) => toastError("未能运行", e)).finally(() => setBusy(false));
  };
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingLeft: 12, paddingRight: 8, paddingVertical: 7, borderBottomWidth: 1, borderColor: DESK_LINE, backgroundColor: DESK_SOFT }}>
      <Pulse on={s.enabled} fast={!!s.running} size={6} />
      <Pressable style={{ flex: 1, minWidth: 0 }} onPress={() => { tap(); onOpenDesk(); }}>
        <Text numberOfLines={1} style={{ fontSize: fs.sm, color: colors.text }}>{stateLine(data, true)}</Text>
        <Text numberOfLines={1} style={{ fontSize: fs.xs, color: s.lastError ? colors.warn : colors.textFaint, marginTop: 1 }}>
          {s.lastError ? `上次未能启动：${s.lastError}` : `常驻会话 · 24 小时 ${s.runs24h} 轮 · ${s.decisions24h} 笔决策${s.tradeMode ? ` · ${s.tradeMode === "live" ? "实盘" : "模拟"}` : ""}`}
        </Text>
      </Pressable>
      {s.enabled && !s.running ? (
        <Pressable disabled={busy} hitSlop={6} accessibilityLabel="立即运行一轮" onPress={() => { tap(); run(); }} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 4, paddingHorizontal: 10, height: 28, borderRadius: 14, borderWidth: 1, borderColor: DESK_LINE, opacity: busy ? 0.5 : pressed ? 0.7 : 1 })}>
          <Ionicons name="play" size={11} color={DESK} />
          <Text style={{ fontSize: fs.sm, color: DESK }}>运行</Text>
        </Pressable>
      ) : null}
      <Pressable hitSlop={6} accessibilityLabel="交易台日志与设置" onPress={() => { tap(); onOpenDesk(); }} style={({ pressed }) => ({ width: 30, height: 28, borderRadius: 8, alignItems: "center", justifyContent: "center", backgroundColor: pressed ? colors.cardMuted : undefined })}>
        <Ionicons name="options-outline" size={17} color={colors.textSoft} />
      </Pressable>
    </View>
  );
}

/** A run's brief in the chat: the head line and what fired; the rest (context and instructions for the agent) on demand. */
export function DeskBubble({ text, onSelect }: { text: string; onSelect: (t: string) => void }) {
  const [more, setMore] = useState(false);
  const lines = text.split("\n");
  const head = (lines[0] ?? "").replace(/^🤖\s*/, "");
  const sig = text.indexOf("【本次信号】");
  let fired: string[] = [];
  if (sig >= 0) {
    const block = text.slice(sig).split("\n\n")[0].split("\n");
    fired = block.slice(1).filter((l) => l.startsWith("· "));
  }
  const kind: keyof typeof Ionicons.glyphMap = head.includes("定时") ? "time-outline" : head.includes("手动") ? "play-outline" : "flash-outline";
  return (
    <Pressable onLongPress={() => onSelect(text)} style={{ marginTop: 14, marginBottom: 6, borderRadius: 14, borderWidth: 1, borderColor: DESK_LINE, backgroundColor: DESK_SOFT, paddingHorizontal: 12, paddingVertical: 10, gap: 6 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Ionicons name={kind} size={15} color={DESK} />
        <Text style={{ flex: 1, fontSize: fs.lg, fontWeight: "700", color: colors.text }}>{head}</Text>
      </View>
      {fired.length ? fired.map((l, i) => <Text key={i} selectable style={{ fontSize: fs.md, lineHeight: 19, color: colors.textSoft }}>{l}</Text>)
        : <Text style={{ fontSize: fs.md, color: colors.textSoft }}>{sig >= 0 ? "没有策略信号触发，例行巡检大盘、仓位和监控池。" : "例行巡检。"}</Text>}
      <View style={{ flexDirection: "row", alignItems: "center" }}>
        <Text style={{ flex: 1, fontSize: fs.xs, color: colors.textFaint }}>交易台自动运行 · 只提议，不下单</Text>
        <Pressable hitSlop={6} onPress={() => { tap(); setMore(!more); }}>
          <Text style={{ fontSize: fs.xs, color: colors.textSoft }}>{more ? "收起简报" : "完整简报"}</Text>
        </Pressable>
      </View>
      {more ? <Text selectable style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textFaint }}>{lines.slice(1).join("\n")}</Text> : null}
    </Pressable>
  );
}

// ---------- the pane ----------

type Tab = "overview" | "journal" | "memory" | "setup";

export function DeskPane({ onClose, header }: { onClose: () => void; header: (title: string, right?: ReactNode) => ReactNode }) {
  const { data, error, owner } = useDesk();
  const [tab, setTab] = useState<Tab>("overview");
  const openDeskChat = () => {
    const id = data?.status.chatId;
    if (!id) return;
    onClose();
    openChat(id).catch((e) => toastError("未能打开会话", e));
  };
  return (
    <View style={{ flex: 1, backgroundColor: colors.bg }}>
      {header("交易台", data?.status.tradeMode ? <Tag text={data.status.tradeMode === "live" ? "LIVE" : "DRY RUN"} tone={data.status.tradeMode === "live" ? "bad" : "warn"} /> : null)}
      {!data ? (
        error ? <ErrorBox text={error} onRetry={() => void refreshDesk()} /> : owner || !error ? <Loading text="加载交易台" /> : <EmptyState icon="key-outline" text="交易台只对所有者可见，在设置里填入所有者密钥。" />
      ) : (
        <>
          <Segmented value={tab} onChange={setTab} style={{ marginHorizontal: 12, marginTop: 10 }}
            options={[{ value: "overview", label: "概览" }, { value: "journal", label: "决策" }, { value: "memory", label: "记忆" }, { value: "setup", label: "策略" }]} />
          {tab === "overview" ? <Overview d={data} onOpenChat={openDeskChat} /> : tab === "journal" ? <Journal d={data} /> : tab === "memory" ? <Memory /> : <Setup d={data} />}
        </>
      )}
    </View>
  );
}

function Box({ children, accent }: { children: ReactNode; accent?: boolean }) {
  return <View style={{ borderRadius: 12, borderWidth: 1, borderColor: accent ? DESK_LINE : colors.border, backgroundColor: accent ? DESK_SOFT : colors.card, padding: 12, gap: 8 }}>{children}</View>;
}

const H = ({ children, right }: { children: ReactNode; right?: ReactNode }) => (
  <View style={{ flexDirection: "row", alignItems: "center", marginTop: 6 }}>
    <Text style={{ flex: 1, fontSize: fs.lg, fontWeight: "600", color: colors.text }}>{children}</Text>
    {right}
  </View>
);

function Metric({ label, value, tone }: { label: string; value: string; tone?: "up" | "down" }) {
  return (
    <View style={{ flexBasis: "30%", flexGrow: 1, gap: 2 }}>
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{label}</Text>
      <Text style={{ fontSize: fs.xl, fontWeight: "600", color: tone === "up" ? colors.up : tone === "down" ? colors.down : colors.text, fontVariant: ["tabular-nums"] }}>{value}</Text>
    </View>
  );
}

const pctText = (v?: number, d = 1) => (v == null ? "—" : `${v > 0 ? "+" : ""}${v.toFixed(d)}%`);
const toneOf = (v?: number) => (v == null || v === 0 ? undefined : v > 0 ? "up" : "down");

function Overview({ d, onOpenChat }: { d: DeskOverview; onOpenChat: () => void }) {
  const { rev } = useDesk();
  const s = d.status;
  const [busy, setBusy] = useState(false);
  const [days, setDays] = useState<"7" | "30" | "90">("30");
  const stats = useQuery(`desk:stats:${days}`, () => deskApi.stats(Number(days)), { staleMs: 60_000 });
  const runs = useQuery("desk:runs", () => deskApi.runs(20), { staleMs: 20_000 });
  const refreshRuns = runs.refresh;
  const refreshStats = stats.refresh;
  useEffect(() => { void refreshRuns(); void refreshStats(); }, [rev, refreshRuns, refreshStats]);
  const toggle = (on: boolean) => {
    const go = () => {
      setBusy(true);
      saveDesk({ ...d.config, enabled: on }).then(() => toast(on ? "交易台已启用" : "交易台已停用")).catch((e) => toastError("未能保存", e)).finally(() => setBusy(false));
    };
    if (on) confirm("启用交易台", `AI 会在策略信号触发时和每 ${d.config.schedule.everyHours} 小时整点前 ${d.config.schedule.leadMin} 分钟自动运行并消耗 token。它只能提交交易提议，每一笔仍需你批准。`, "启用", go);
    else go();
  };
  const st = stats.data;
  const dir = st?.directional;
  return (
    <ScrollView contentContainerStyle={{ padding: 12, paddingBottom: 40, gap: 10 }} refreshControl={<Pull onRefresh={() => Promise.all([refreshDesk(), stats.refresh(), runs.refresh()])} />}>
      <Box accent>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          <Pulse on={s.enabled} fast={!!s.running} />
          <View style={{ flex: 1 }}>
            <Text style={{ fontSize: fs.xl, fontWeight: "700", color: colors.text }}>{s.enabled ? "全自动运行中" : "未启用"}</Text>
            <Text style={{ fontSize: fs.sm, color: colors.textSoft, marginTop: 2 }}>{s.enabled ? stateLine(d) : "启用后，信号触发与定时巡检都会唤醒同一个常驻会话。"}</Text>
          </View>
          <Toggle value={s.enabled} disabled={busy} onChange={toggle} />
        </View>
        {s.lastError ? <Text style={{ fontSize: fs.sm, color: colors.warn }}>上次未能启动：{s.lastError}</Text> : null}
        {s.enabled ? (
          <View style={{ flexDirection: "row", gap: 8 }}>
            {s.chatId ? <PillButton small variant="soft" icon="chatbubble-ellipses-outline" label="打开会话" onPress={onOpenChat} style={{ flex: 1 }} /> : null}
            <PillButton small variant="soft" icon="play-outline" label={s.running ? "运行中" : "立即运行一轮"} disabled={!!s.running} style={{ flex: 1 }}
              onPress={() => void runDeskNow().then(() => toast("已开始一轮运行", "在交易台会话里可以看到过程")).catch((e) => toastError("未能运行", e))} />
          </View>
        ) : null}
      </Box>

      <H right={<Segmented small fit value={days} onChange={setDays} options={[{ value: "7", label: "7 天" }, { value: "30", label: "30 天" }, { value: "90", label: "90 天" }]} />}>战绩</H>
      {!st ? (stats.error ? <ErrorBox text={stats.error} onRetry={() => void stats.refresh()} /> : <Loading />) : (
        <Box>
          <View style={{ flexDirection: "row", flexWrap: "wrap", rowGap: 12 }}>
            <Metric label="运行轮数" value={String(st.runs)} />
            <Metric label="决策" value={String(st.decisions)} />
            <Metric label="AI 花费" value={`$${st.costUsd.toFixed(2)}`} />
            <Metric label="方向判断 4h 胜率" value={dir?.winRate4h == null ? "—" : `${Math.round(dir.winRate4h * 100)}%`} />
            <Metric label="4h 平均" value={pctText(dir?.avgRet4h, 2)} tone={toneOf(dir?.avgRet4h)} />
            <Metric label="24h 平均" value={pctText(dir?.avgRet24h, 2)} tone={toneOf(dir?.avgRet24h)} />
            <Metric label="提议 / 已执行" value={`${st.proposals.made} / ${st.proposals.executed}`} />
            <Metric label="已平仓胜 / 总" value={`${st.proposals.wins} / ${st.proposals.closed}`} />
            <Metric label="已实现盈亏" value={`${st.proposals.pnlUsdt > 0 ? "+" : ""}${st.proposals.pnlUsdt.toFixed(2)} U`} tone={toneOf(st.proposals.pnlUsdt)} />
          </View>
          <Text style={styles.note}>方向判断：所有带方向的决策（含观望和不做），按决策方向计算之后的涨跌；{dir?.followed ?? 0} 笔已有 4 小时结果。样本少时只作参考。</Text>
          <Buckets title="按决策" rows={st.byAction} name={(k) => actionText[k] ?? k} />
          <Buckets title="按把握" rows={st.byConviction} name={(k) => convictionText[k] ?? "未标"} />
          <Buckets title="按触发策略" rows={st.byStrategy} name={(k) => strategyText(k, d.strategies)} />
          <Buckets title="按触发方式" rows={st.byTrigger} name={(k) => triggerText[k] ?? k} />
        </Box>
      )}

      <H>最近运行</H>
      {!runs.data ? <Loading /> : !runs.data.runs.length ? <Text style={styles.note}>还没有运行过。</Text> : runs.data.runs.map((r) => <RunRow key={r.id} r={r} />)}
    </ScrollView>
  );
}

function Buckets({ title, rows, name }: { title: string; rows: DeskBucket[]; name: (key: string) => string }) {
  const shown = rows.filter((r) => r.n > 0);
  if (!shown.length) return null;
  return (
    <View style={{ gap: 4, marginTop: 4 }}>
      <View style={{ flexDirection: "row" }}>
        <Text style={{ flex: 1, fontSize: fs.xs, color: colors.textFaint }}>{title}</Text>
        <Text style={{ width: 44, textAlign: "right", fontSize: fs.xs, color: colors.textFaint }}>笔数</Text>
        <Text style={{ width: 56, textAlign: "right", fontSize: fs.xs, color: colors.textFaint }}>4h 胜率</Text>
        <Text style={{ width: 60, textAlign: "right", fontSize: fs.xs, color: colors.textFaint }}>4h 平均</Text>
      </View>
      {shown.map((r) => (
        <View key={r.key} style={{ flexDirection: "row" }}>
          <Text numberOfLines={1} style={{ flex: 1, fontSize: fs.sm, color: colors.textSoft }}>{name(r.key)}</Text>
          <Text style={{ width: 44, textAlign: "right", fontSize: fs.sm, color: colors.text, fontVariant: ["tabular-nums"] }}>{r.n}</Text>
          <Text style={{ width: 56, textAlign: "right", fontSize: fs.sm, color: colors.text, fontVariant: ["tabular-nums"] }}>{r.winRate4h == null ? "—" : `${Math.round(r.winRate4h * 100)}%`}</Text>
          <Text style={{ width: 60, textAlign: "right", fontSize: fs.sm, color: r.avgRet4h == null ? colors.textFaint : r.avgRet4h > 0 ? colors.up : r.avgRet4h < 0 ? colors.down : colors.text, fontVariant: ["tabular-nums"] }}>{pctText(r.avgRet4h, 2)}</Text>
        </View>
      ))}
    </View>
  );
}

function RunRow({ r }: { r: DeskRun }) {
  const [open, setOpen] = useState(false);
  const icon: keyof typeof Ionicons.glyphMap = r.trigger === "schedule" ? "time-outline" : r.trigger === "manual" ? "play-outline" : "flash-outline";
  return (
    <Pressable onPress={() => { tap(); setOpen(!open); }} style={{ borderRadius: 10, borderWidth: 1, borderColor: colors.border, backgroundColor: colors.card, paddingHorizontal: 10, paddingVertical: 8, gap: 4 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Ionicons name={icon} size={14} color={r.status === "failed" ? colors.warn : DESK} />
        <Text numberOfLines={1} style={{ flex: 1, fontSize: fs.md, color: colors.text }}>#{r.id} {r.label}</Text>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{time(r.ts, true)}</Text>
      </View>
      <Text style={{ fontSize: fs.xs, color: r.status === "failed" ? colors.warn : colors.textFaint }}>
        {r.status === "running" ? "运行中…" : r.status === "failed" ? `未完成：${r.error}` : `${r.decisions} 笔决策${r.proposals ? ` · ${r.proposals} 笔提议` : ""}${r.signals ? ` · ${r.signals} 条信号` : ""}${r.costUsd ? ` · $${r.costUsd.toFixed(3)}` : ""}${r.latencyMs ? ` · ${Math.round(r.latencyMs / 1000)}s` : ""}${r.newSession ? " · 新上下文" : ""}`}
      </Text>
      {open && r.summary ? <Text selectable style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textSoft }}>{r.summary}</Text> : null}
    </Pressable>
  );
}

// ---------- journal ----------

const actionTone: Record<string, "good" | "bad" | "warn" | "info" | undefined> = { open: "info", add: "info", reduce: "warn", close: "warn", adjust: "warn" };

function Journal({ d }: { d: DeskOverview }) {
  const { rev } = useDesk();
  const [filter, setFilter] = useState<"" | "trades" | "passes">("");
  const q = useQuery(`desk:journal:${filter}`, () => deskApi.journal({ action: filter, limit: 100 }), { staleMs: 20_000 });
  const refresh = q.refresh;
  useEffect(() => { void refresh(); }, [rev, refresh]);
  const list = q.data?.decisions;
  return (
    <ScrollView contentContainerStyle={{ padding: 12, paddingBottom: 40, gap: 8 }} refreshControl={<Pull onRefresh={q.refresh} />}>
      <View style={{ flexDirection: "row", gap: 8 }}>
        <Chip label="全部" active={filter === ""} onPress={() => setFilter("")} />
        <Chip label="交易" active={filter === "trades"} onPress={() => setFilter("trades")} />
        <Chip label="观望 / 不做" active={filter === "passes"} onPress={() => setFilter("passes")} />
      </View>
      <Text style={styles.note}>每一笔决策都带着当时的价格入库，之后自动记录 1 小时 / 4 小时 / 24 小时的涨跌（按决策方向计正负）和提议的结果，用来回溯。</Text>
      {!list ? (q.error ? <ErrorBox text={q.error} onRetry={() => void q.refresh()} /> : <Loading />) : !list.length ? <EmptyState icon="reader-outline" text="还没有决策记录" /> : list.map((x) => <DecisionRow key={x.id} x={x} strategies={d.strategies} />)}
    </ScrollView>
  );
}

function Ret({ label, v }: { label: string; v?: number }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "baseline", gap: 3 }}>
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{label}</Text>
      <Text style={{ fontSize: fs.sm, fontVariant: ["tabular-nums"], color: v == null ? colors.textDim : v > 0 ? colors.up : v < 0 ? colors.down : colors.text }}>{pctText(v)}</Text>
    </View>
  );
}

const proposalText: Record<string, string> = { pending: "待审核", approved: "已批准", executing: "执行中", executed: "已执行", rejected: "已拒绝", expired: "已过期", failed: "失败", invalidated: "已作废" };

function DecisionRow({ x, strategies }: { x: DeskDecision; strategies: DeskStrategy[] }) {
  const [open, setOpen] = useState(false);
  const px = (v?: number) => (v ? String(v) : "—");
  return (
    <Pressable onPress={() => { tap(); setOpen(!open); }} style={{ borderRadius: 12, borderWidth: 1, borderColor: colors.border, backgroundColor: colors.card, padding: 10, gap: 6 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Text style={{ fontSize: fs.lg, fontWeight: "700", color: colors.text }}>{sym(x.instId)}</Text>
        <Tag text={actionText[x.action] ?? x.action} tone={actionTone[x.action]} />
        {x.side ? <Tag text={x.side === "long" ? "多" : "空"} tone={x.side === "long" ? "good" : "bad"} /> : null}
        {x.conviction ? <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>把握 {convictionText[x.conviction]}</Text> : null}
        <View style={{ flex: 1 }} />
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{time(x.ts, true)}</Text>
      </View>
      <Text numberOfLines={open ? undefined : 2} style={{ fontSize: fs.md, lineHeight: 19, color: colors.textSoft }}>{x.thesis}</Text>
      <View style={{ flexDirection: "row", flexWrap: "wrap", alignItems: "center", columnGap: 12, rowGap: 4 }}>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>@{px(x.px)}</Text>
        <Ret label="1h" v={x.ret1h} />
        <Ret label="4h" v={x.ret4h} />
        <Ret label="24h" v={x.ret24h} />
        {x.proposalId ? <Text style={{ fontSize: fs.xs, color: x.proposalStatus === "executed" ? colors.primary : colors.textFaint }}>提议 #{x.proposalId} {proposalText[x.proposalStatus ?? "pending"] ?? x.proposalStatus}</Text> : null}
        {x.closedAt ? <Text style={{ fontSize: fs.xs, fontWeight: "600", color: (x.pnlUsdt ?? 0) >= 0 ? colors.up : colors.down }}>{(x.pnlUsdt ?? 0) >= 0 ? "+" : ""}{(x.pnlUsdt ?? 0).toFixed(2)} U</Text> : null}
        {x.verdict ? <Tag text={verdictText[x.verdict]} tone={x.verdict === "right" ? "good" : x.verdict === "wrong" ? "bad" : "warn"} /> : null}
      </View>
      {open ? (
        <View style={{ gap: 4, paddingTop: 4, borderTopWidth: 1, borderColor: colors.border }}>
          {x.entry || x.sl || x.tp ? <Text style={styles.note}>入场 {px(x.entry)} · 止损 {px(x.sl)} · 止盈 {px(x.tp)}</Text> : null}
          {x.invalidation ? <Text style={styles.note}>失效条件：{x.invalidation}</Text> : null}
          {x.mfe24h != null && x.mae24h != null ? <Text style={styles.note}>24 小时内最有利 {pctText(x.mfe24h)} · 最不利 {pctText(x.mae24h)}</Text> : null}
          <Text style={styles.note}>
            {x.trigger ? `触发：${triggerText[x.trigger] ?? x.trigger}` : "对话中记录"}
            {x.strategy ? ` · ${x.strategy.split(",").map((k) => strategyText(k, strategies)).join("、")}` : ""}
            {x.runId ? ` · 运行 #${x.runId}` : ""}
            {x.tags ? ` · ${x.tags}` : ""}
          </Text>
          {x.review ? <Text selectable style={{ fontSize: fs.sm, lineHeight: 18, color: colors.text }}>复盘：{x.review}</Text> : x.outcomeAt ? <Text style={styles.note}>结果已出，等待 AI 复盘。</Text> : <Text style={styles.note}>结果跟踪中。</Text>}
        </View>
      ) : null}
    </Pressable>
  );
}

// ---------- memory ----------

function Memory() {
  const { rev } = useDesk();
  const q = useQuery("desk:memory", () => deskApi.memory("all"), { staleMs: 20_000 });
  const refresh = q.refresh;
  useEffect(() => { void refresh(); }, [rev, refresh]);
  const [text, setText] = useState("");
  const [saving, setSaving] = useState(false);
  const save = () => {
    const t = text.trim();
    if (!t || saving) return;
    setSaving(true);
    deskApi.remember("rule", t).then(() => { setText(""); return q.refresh(); }).catch((e) => toastError("未能保存", e)).finally(() => setSaving(false));
  };
  const retire = (m: DeskMemory) => confirm("停用这条记忆", "之后的运行不再带上它（记录保留）。", "停用", () => void deskApi.retire(m.id, "主人停用").then(() => q.refresh()).catch((e) => toastError("未能停用", e)), true);
  const all = q.data?.memory;
  const active = all?.filter((m) => m.status === "active") ?? [];
  const retired = all?.filter((m) => m.status !== "active") ?? [];
  return (
    <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <ScrollView contentContainerStyle={{ padding: 12, paddingBottom: 40, gap: 8 }} keyboardShouldPersistTaps="handled" refreshControl={<Pull onRefresh={q.refresh} />}>
        <Text style={styles.note}>交易台的长期记忆：每一轮运行都会带给 AI，上下文重启也不会丢。AI 复盘后自己写入教训；你写的是规则，优先级最高。{q.data ? `（${active.length}/${q.data.max}）` : ""}</Text>
        <View style={{ flexDirection: "row", gap: 6 }}>
          <TextInput value={text} onChangeText={setText} placeholder="加一条规则，例如：只做监控池前 30，单笔保证金 ≤ 50 U" placeholderTextColor={colors.textDim} maxLength={300} returnKeyType="done" onSubmitEditing={save}
            style={{ flex: 1, height: 40, borderRadius: radius.md, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.raised, color: colors.text, paddingHorizontal: 12, fontSize: 16 }} />
          <Pressable accessibilityLabel="添加规则" disabled={!text.trim() || saving} onPress={() => { tap(); save(); }} style={({ pressed }) => ({ width: 40, height: 40, borderRadius: radius.md, alignItems: "center", justifyContent: "center", backgroundColor: DESK_SOFT, borderWidth: 1, borderColor: DESK_LINE, opacity: !text.trim() || saving ? 0.5 : pressed ? 0.7 : 1 })}>
            <Ionicons name="add" size={20} color={DESK} />
          </Pressable>
        </View>
        {!all ? (q.error ? <ErrorBox text={q.error} onRetry={() => void q.refresh()} /> : <Loading />) : !all.length ? <EmptyState icon="bulb-outline" text="还没有长期记忆" /> : null}
        {active.map((m) => (
          <View key={m.id} style={{ flexDirection: "row", alignItems: "flex-start", gap: 8, paddingVertical: 10, paddingLeft: 4, borderBottomWidth: 1, borderColor: colors.border }}>
            <View style={{ flex: 1, gap: 4 }}>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                <Tag text={memoryKindText[m.kind] ?? m.kind} tone={m.kind === "rule" ? "info" : undefined} />
                {m.instId ? <Text style={{ fontSize: fs.xs, color: colors.textSoft }}>{sym(m.instId)}</Text> : null}
                <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{m.by === "ai" ? "AI 记录" : "你添加"} · {time(m.updated, true)}{m.sourceDecision ? ` · 来自决策 #${m.sourceDecision}` : ""}</Text>
              </View>
              <Text selectable style={{ fontSize: fs.lg, lineHeight: 21, color: colors.text }}>{m.text}</Text>
            </View>
            <Pressable hitSlop={8} accessibilityLabel="停用" onPress={() => { tap(); retire(m); }} style={({ pressed }) => ({ width: 36, height: 36, borderRadius: 8, alignItems: "center", justifyContent: "center", backgroundColor: pressed ? colors.cardMuted : undefined })}>
              <Ionicons name="archive-outline" size={16} color={colors.textDim} />
            </Pressable>
          </View>
        ))}
        {retired.length ? <H>已停用</H> : null}
        {retired.map((m) => (
          <View key={m.id} style={{ paddingVertical: 6, paddingLeft: 4, gap: 2 }}>
            <Text style={{ fontSize: fs.md, color: colors.textFaint }}>{m.text}</Text>
            <Text style={{ fontSize: fs.xs, color: colors.textDim }}>{memoryKindText[m.kind] ?? m.kind}{m.retiredReason ? ` · ${m.retiredReason}` : ""}</Text>
          </View>
        ))}
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

// ---------- setup ----------

function SetRow({ label, desc, children }: { label: string; desc?: string; children: ReactNode }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 10, minHeight: 40 }}>
      <View style={{ flex: 1 }}>
        <Text style={{ fontSize: fs.md, color: colors.text }}>{label}</Text>
        {desc ? <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 1 }}>{desc}</Text> : null}
      </View>
      {children}
    </View>
  );
}

function Setup({ d }: { d: DeskOverview }) {
  const [c, setC] = useState<DeskConfig>(d.config);
  const [saving, setSaving] = useState(false);
  const [base, setBase] = useState(JSON.stringify(d.config));
  const server = JSON.stringify(d.config);
  if (server !== base && JSON.stringify(c) === base) { setBase(server); setC(d.config); } // follow the server while nothing is edited here
  const dirty = JSON.stringify(c) !== base;
  const up = (f: (x: DeskConfig) => void) => setC((prev) => { const next = JSON.parse(JSON.stringify(prev)) as DeskConfig; f(next); return next; });
  const save = () => {
    setSaving(true);
    saveDesk(c).then((r) => { setBase(JSON.stringify(r.config)); setC(r.config); toast("已保存", "策略扫描已按新参数重建"); }).catch((e) => toastError("未能保存", e)).finally(() => setSaving(false));
  };
  const lead = c.schedule.leadMin;
  const eh = c.schedule.everyHours;
  const first = `${String((24 - (lead ? 1 : 0)) % 24).padStart(2, "0")}:${String((60 - lead) % 60).padStart(2, "0")}`;
  return (
    <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <ScrollView contentContainerStyle={{ padding: 12, paddingBottom: 110, gap: 10 }} keyboardShouldPersistTaps="handled">
        <H>定时巡检</H>
        <Box>
          <SetRow label="按时间唤醒" desc="信号一直不触发时也会定时看盘"><Toggle value={c.schedule.enabled} onChange={(v) => up((x) => { x.schedule.enabled = v; })} /></SetRow>
          <SetRow label="间隔">
            <Segmented small fit value={String(eh)} onChange={(v) => up((x) => { x.schedule.everyHours = Number(v); })} options={d.options.everyHours.map((h) => ({ value: String(h), label: `${h} 小时` }))} />
          </SetRow>
          <SetRow label="整点前" desc="资金费在 00 / 08 / 16 点结算，4 小时线也在整点收盘"><NumInput width={96} decimal={false} unit="分钟" value={lead} onChange={(v) => up((x) => { x.schedule.leadMin = v; })} /></SetRow>
          <Text style={styles.note}>北京时间 {first} 起每 {eh} 小时一次，全天 {24 / eh} 轮。</Text>
        </Box>

        <H>信号节流</H>
        <Box>
          <SetRow label="策略信号唤醒" desc="关闭后只按时间巡检"><Toggle value={c.signals.enabled} onChange={(v) => up((x) => { x.signals.enabled = v; })} /></SetRow>
          <SetRow label="两轮最小间隔" desc="间隔内的信号攒在一起下一轮处理"><NumInput width={96} decimal={false} unit="分钟" value={c.signals.minGapMin} onChange={(v) => up((x) => { x.signals.minGapMin = v; })} /></SetRow>
          <SetRow label="每 24 小时信号轮数上限" desc="超出的信号并入下一次定时巡检"><NumInput width={96} decimal={false} unit="轮" value={c.signals.maxRunsDay} onChange={(v) => up((x) => { x.signals.maxRunsDay = v; })} /></SetRow>
          <SetRow label="信号过期" desc="等待超过这个时间的信号直接丢弃"><NumInput width={96} decimal={false} unit="分钟" value={c.signals.staleMin} onChange={(v) => up((x) => { x.signals.staleMin = v; })} /></SetRow>
        </Box>

        <H>策略扫描（全池）</H>
        {d.strategies.map((sv) => {
          const i = c.strategies.findIndex((x) => x.key === sv.key);
          const sc = i >= 0 ? c.strategies[i] : null;
          const on = sc ? sc.enabled : sv.enabled;
          return (
            <Box key={sv.key}>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                <View style={{ flex: 1 }}>
                  <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text }}>{sv.label}{sv.custom ? "（自定义）" : ""}</Text>
                  <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 2 }}>{sv.desc}</Text>
                </View>
                {sc ? <Toggle value={on} onChange={(v) => up((x) => { x.strategies[i].enabled = v; })} /> : <Tag text={on ? "开" : "关"} />}
              </View>
              {on && sc ? (
                <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                  {(sv.paramDefs ?? []).map((p) => (
                    <View key={p.key} style={{ flexBasis: "47%", flexGrow: 1, gap: 3 }}>
                      <Text numberOfLines={1} style={{ fontSize: fs.xs, color: colors.textFaint }}>{p.label}</Text>
                      <NumInput width={140} unit={p.unit} decimal={p.step < 1} value={sc.params[p.key]} invalid={!(sc.params[p.key] >= p.min && sc.params[p.key] <= p.max)} onChange={(v) => up((x) => { x.strategies[i].params[p.key] = v; })} />
                    </View>
                  ))}
                </View>
              ) : null}
              {sv.error ? <Text style={{ fontSize: fs.xs, color: colors.warn }}>扫描未运行：{sv.error}</Text> : null}
              {sv.scans.map((x) => (
                <Text key={x.signalId} style={{ fontSize: fs.xs, lineHeight: 16, color: colors.textFaint }}>#{x.signalId} {x.desc}{x.progress ? `\n${x.progress}` : ""}</Text>
              ))}
              {sv.enabled ? <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>过去 24 小时触发 {sv.fires24h} 个币次</Text> : null}
            </Box>
          );
        })}

        <H>运行方式</H>
        <Box>
          <SetRow label="模型">
            <Segmented small fit value={c.model} onChange={(v) => up((x) => { x.model = v; })} options={[{ value: "", label: "默认" }, ...d.options.models.map((m) => ({ value: m, label: m.includes("opus") ? "Opus" : "Sonnet" }))]} />
          </SetRow>
          <SetRow label="思考强度">
            <Segmented small fit value={c.effort} onChange={(v) => up((x) => { x.effort = v; })} options={[{ value: "", label: "默认" }, { value: "low", label: "低" }, { value: "medium", label: "中" }, { value: "high", label: "高" }]} />
          </SetRow>
          <SetRow label="每轮最多提议"><NumInput width={96} decimal={false} unit="笔" value={c.maxProposals} onChange={(v) => up((x) => { x.maxProposals = v; })} /></SetRow>
          <SetRow label="上下文重启" desc="每运行这么多轮换一段新上下文（决策日志和记忆会带过去），0 = 不重启"><NumInput width={96} decimal={false} unit="轮" value={c.sessionRuns} onChange={(v) => up((x) => { x.sessionRuns = v; })} /></SetRow>
          <Text style={{ fontSize: fs.md, color: colors.text, marginTop: 4 }}>长期指示</Text>
          <TextInput value={c.focus} onChangeText={(v) => up((x) => { x.focus = v; })} multiline maxLength={600} placeholder="每一轮都带给 AI 的话，例如：偏好顺势回踩，不做上线不满 7 天的币" placeholderTextColor={colors.textDim}
            style={{ minHeight: 70, borderRadius: radius.md, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.raised, color: colors.text, paddingHorizontal: 10, paddingVertical: 8, fontSize: 15, lineHeight: 20, textAlignVertical: "top" }} />
        </Box>
      </ScrollView>
      {dirty ? (
        <View style={{ position: "absolute", left: 0, right: 0, bottom: 0, flexDirection: "row", gap: 10, paddingHorizontal: 12, paddingTop: 10, paddingBottom: 26, borderTopWidth: 1, borderColor: colors.border, backgroundColor: "rgba(10,10,10,0.96)" }}>
          <PillButton variant="soft" label="撤销" onPress={() => setC(JSON.parse(base) as DeskConfig)} style={{ flex: 1 }} />
          <PillButton label="保存" loading={saving} onPress={save} style={{ flex: 2 }} />
        </View>
      ) : null}
    </KeyboardAvoidingView>
  );
}

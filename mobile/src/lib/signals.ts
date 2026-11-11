// Signal subscriptions (server internal/signals): the AI waits for a market condition instead of being
// asked again. A subscription holds the condition, who subscribed (an AI chat, or the owner) and what
// the subscriber means to do; when it fires the server appends a ⚡ 信号 message to that chat and runs
// the agent there. Owner only (every call carries the key; a 401 shows nothing). One global store like
// lib/realtrade.ts: the list by status while a pane shows it, and single subscriptions the chat
// renders, refetched on the stream event `signals` and on foreground. When a signal wakes the chat
// that is on screen, the chat follows the new turn.
import { useEffect, useSyncExternalStore } from "react";
import { AppState } from "react-native";
import { ApiError, req } from "./api";
import { getAdminKey } from "./adminKey";
import { ai, openChat } from "./ai";
import { onStreamEvent } from "./market";
import { logger } from "./log";
import type { Diagnostic } from "./tradeApi";

const log = logger("signals");

export type SignalStatus = "active" | "fired" | "expired" | "cancelled";
export type WakeState = "queued" | "started" | "skipped" | "failed";
export interface SignalLevel { instId: string; px: number; label: string }
export interface SignalProgress { instId?: string; px?: number; target?: number; pct: number; text: string }
export interface SignalFire { id: number; ts: number; px?: number; matched: string; wake: WakeState; wakeChat?: number; wakeAt?: number; error?: string }

// Script signals (server internal/signals/script.go): a Python ctg.Condition run on its timeframe's bar close.
/** One check of a script at a bar close; fire null = no answer (None, missing bars, an error). */
export interface ScriptEval { at: number; fire: boolean | null; values?: Record<string, number>; note?: string; ms: number; error?: string; missing?: string[] }
export interface ReplayFire { t: number; values?: Record<string, number>; note?: string }
export type ScriptProblem = "compile" | "error" | "slow" | "often" | "unknown" | "holds" | "nodata";
/** The script run over recent history: how often it would have fired. states = one char per close: 1 holds, 0 not, - unknown. */
export interface ScriptReplay {
  at: number; from: number; to: number; timeframe: string; bars: number; days: number; states: string;
  fires: ReplayFire[]; fireCount: number; perDay: number; unknown: number; errors: number; firstError?: string; firstErrorAt?: number;
  avgMs: number; maxMs: number; noFactors?: boolean; now?: ScriptEval; ok: boolean; problem?: ScriptProblem; reason?: string;
}
export interface ScriptMeta { class: string; desc: string; instIds: string[]; timeframe: string; lookback: number; hash: string }
/** A subscription's script: what it declares, its code, the replay it was accepted with and its live checks. */
export interface ScriptInfo extends ScriptMeta {
  code: string; replay?: ScriptReplay; last?: ScriptEval; checks: number; avgMs: number;
  /** consecutive errors; maxErrors in a row end the subscription */
  errors: number; lastError?: string; lastErrorAt?: number;
}
export interface ScriptLimits { maxBytes: number; budgetMs: number; maxAvgMs: number; maxFiresPerDay: number; maxErrors: number; replayDays: number; maxActive: number; activeScripts: number }
export interface ScriptTest { ok: boolean; problem?: ScriptProblem; reason?: string; diagnostics: Diagnostic[]; meta?: ScriptMeta; replay?: ScriptReplay; limits: ScriptLimits }
export interface Signal {
  id: number; created: number; updated: number;
  kind: "chat" | "owner"; chatId?: number;
  /** chat title when subscribed */
  label: string;
  /** "AI 对话 #12 · 标题" | "主人" */
  subscriber: string;
  instIds: string[];
  spec: Record<string, unknown>;
  /** the condition in Chinese */
  desc: string;
  intent: string;
  planId?: number;
  expiresAt: number; maxFires: number; cooldownMin: number; fireCount: number; lastFiredAt?: number;
  status: SignalStatus; statusAt: number;
  /** owner | chat:<id>[（主人要求）] | system | timeout */
  endedBy?: string; reason?: string;
  levels?: SignalLevel[];
  /** active only: distance to firing */
  progress?: SignalProgress;
  /** detail only */
  history?: SignalFire[];
  /** script signals */
  script?: ScriptInfo;
}
export interface SignalCaps { maxActive: number; perChat: number; wakesPerDay: number; wakesToday: number; active: number; maxDays: number; scripts?: ScriptLimits }
interface SignalList { signals: Signal[] | null; caps: SignalCaps; now: number }
export type SignalFilter = "active" | "fired" | "ended";
export interface SignalCreate { spec: Record<string, unknown>; intent: string; expiresHours: number; maxFires?: number; every?: string; code?: string }

/** An AI trade plan (server internal/aigate/plans.go; web charts/aiPlans.ts) a signal can be linked to. */
export interface SignalPlan {
  id: number; chatId: number; created: number; instId: string; side: "long" | "short";
  entryLo: number; entryHi: number; stop: number; tp1: number; tp2?: number; leverage?: number;
  invalidate?: string; expires: number; status: string;
}
export const PLAN_STATUS: Record<string, string> = { pending: "等待入场", active: "已入场", tp1: "已到 TP1" };
/** Open and recently settled plans of a coin (owner key). */
export const plansOf = (instId: string) => req<SignalPlan[]>(`/api/v1/ai/plans?instId=${encodeURIComponent(instId)}&recent=168`, quiet);

/** The plan a signal waits for: its planId (or a plan leaf's), else the subscribing chat's newest plan on
 *  that coin made by the time it subscribed (the same rule the compact chart draws with). */
export function linkedPlan(s: Signal, plans: SignalPlan[] | undefined): SignalPlan | null {
  if (!plans?.length) return null;
  const pid = s.planId || planLeafId(s.spec);
  if (pid) return plans.find((p) => p.id === pid) ?? null;
  if (s.kind !== "chat" || !s.chatId) return null;
  const mine = plans.filter((p) => p.chatId === s.chatId && p.created <= s.created + 10 * 60_000);
  return mine.sort((a, b) => b.created - a.created)[0] ?? null;
}
function planLeafId(spec: Record<string, unknown>): number {
  if (spec.type === "plan" && typeof spec.planId === "number") return spec.planId;
  for (const c of (spec.of as Record<string, unknown>[] | undefined) ?? []) {
    const id = planLeafId(c);
    if (id) return id;
  }
  return 0;
}

export type PlanLevels = Pick<SignalPlan, "side" | "entryLo" | "entryHi" | "stop" | "tp1" | "tp2">;
/** Entry / stop / target written in the intent (入场 a-b，止损 c，目标 d), when no plan is linked.
 *  Needs a stop and an entry or target; numbers more than 50% from the price are ignored. */
export function intentLevels(text: string, px?: number): PlanLevels | null {
  const N = String.raw`(\d+(?:\.\d+)?)`;
  const m = (re: string) => new RegExp(re).exec(text);
  const e = m(String.raw`(?:入场|进场|挂单)\s*[:：]?\s*${N}(?:\s*[-–~至到]\s*${N})?`);
  const sl = m(String.raw`止损\s*[:：]?\s*${N}`);
  const tp = m(String.raw`(?:目标|止盈|TP1?)\s*[:：]?\s*${N}(?:\s*[/、,，]\s*${N})?`);
  if (!sl || (!e && !tp)) return null;
  const v = (x?: string) => (x ? Number(x) : 0);
  const near = (x: number) => !x || !px || Math.abs(x / px - 1) < 0.5;
  const lo = e ? Math.min(v(e[1]), v(e[2]) || v(e[1])) : 0;
  const hi = e ? Math.max(v(e[1]), v(e[2]) || v(e[1])) : 0;
  const stop = v(sl[1]);
  const tp1 = tp ? v(tp[1]) : 0;
  const tp2 = tp ? v(tp[2]) : 0;
  if (![lo, hi, stop, tp1, tp2].every(near)) return null;
  const side = (lo ? stop < lo : stop < tp1) ? "long" : "short";
  return { side, entryLo: lo, entryHi: hi, stop, tp1, tp2: tp2 || undefined };
}

export const STATUS_LABEL: Record<SignalStatus, string> = { active: "等待触发", fired: "已触发", expired: "已过期", cancelled: "已取消" };
export const statusTone = (s: SignalStatus): "good" | "warn" | "info" | undefined => (s === "active" ? "info" : s === "fired" ? "good" : undefined);
export const WAKE_LABEL: Record<WakeState, string> = { queued: "排队中（对话正忙）", started: "已唤醒 AI", skipped: "未唤醒（超出每日上限）", failed: "唤醒失败" };

/** Who ended it, in words. */
export function endedByText(by?: string) {
  if (!by) return "";
  if (by === "owner") return "主人";
  if (by === "timeout") return "到期";
  if (by === "system") return "系统";
  const m = /^chat:(\d+)(.*)$/.exec(by);
  return m ? `AI 对话 #${m[1]}${m[2]}` : by;
}

const quiet = { quiet: true };
const base = "/api/v1/signals";
export const sigApi = {
  list: (status: SignalFilter | "all", limit = 100) => req<SignalList>(`${base}?status=${status}&limit=${limit}`, quiet),
  get: (id: number) => req<Signal>(`${base}/${id}`, quiet),
  /** the owner's own subscription: when it fires it starts a new chat with `intent` as the instructions */
  create: (b: SignalCreate) => req<Signal>(base, { method: "POST", body: b }),
  cancel: (id: number, reason = "主人取消") => req<Signal>(`${base}/${id}/cancel`, { method: "POST", body: { reason } }),
  /** compile + replay a script over recent history; nothing is stored */
  testScript: (code: string) => req<ScriptTest>(`${base}/test-script`, { method: "POST", body: { code } }),
  scriptTemplate: () => req<{ code: string; limits: ScriptLimits }>(`${base}/script-template`, quiet),
};

// ---------- store ----------

interface SigState {
  owner: boolean;
  /** server clock − device clock, for countdowns */
  skew: number;
  caps: SignalCaps | null;
  /** lists by filter, loaded while a pane shows them */
  lists: Partial<Record<SignalFilter, Signal[]>>;
  activeCount: number;
  /** single subscriptions (with history) the chat shows; null = gone (404) */
  byId: Record<number, Signal | null>;
}
let st: SigState = { owner: false, skew: 0, caps: null, lists: {}, activeCount: 0, byId: {} };
const listeners = new Set<() => void>();
const sub = (l: () => void) => { listeners.add(l); return () => void listeners.delete(l); };
const set = (patch: Partial<SigState>) => { st = { ...st, ...patch }; listeners.forEach((l) => l()); };

const watched = new Map<number, number>();
const listWatchers: Record<SignalFilter, number> = { active: 0, fired: 0, ended: 0 };

export const sigNow = () => Date.now() + st.skew;

let running = false;
let again = false;
export async function refreshSignals(): Promise<void> {
  if (!getAdminKey()) {
    if (st.owner) set({ owner: false, lists: {}, byId: {}, activeCount: 0 });
    return;
  }
  if (running) { again = true; return; }
  running = true;
  try {
    const ids = [...watched.keys()];
    const filters = (Object.keys(listWatchers) as SignalFilter[]).filter((f) => listWatchers[f] > 0 || f === "active");
    const [lists, one] = await Promise.all([
      Promise.all(filters.map((f) => sigApi.list(f))),
      Promise.all(ids.map((id) => sigApi.get(id).catch((e) => (e instanceof ApiError && e.status === 404 ? null : undefined)))),
    ]);
    const byId = { ...st.byId };
    ids.forEach((id, i) => { if (one[i] !== undefined) byId[id] = one[i]; });
    const next: SigState["lists"] = { ...st.lists };
    filters.forEach((f, i) => { next[f] = lists[i].signals ?? []; });
    const first = lists[0];
    set({ owner: true, skew: first?.now ? first.now - Date.now() : st.skew, caps: first?.caps ?? st.caps, lists: next, activeCount: next.active?.length ?? 0, byId });
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) set({ owner: false, lists: {}, byId: {}, activeCount: 0 });
    else log.warn("refresh failed", e); // offline: keep the last state
  } finally {
    running = false;
    if (again) { again = false; void refreshSignals(); }
  }
}

let kickTimer: ReturnType<typeof setTimeout> | null = null;
const kick = () => {
  if (kickTimer) return;
  kickTimer = setTimeout(() => { kickTimer = null; void refreshSignals(); }, 250);
};

let started = false;
function start() {
  if (started) return;
  started = true;
  onStreamEvent((type, data) => {
    if (type !== "signals") return;
    kick();
    // a signal woke the chat on screen: follow the new turn (it was idle, so nothing streams yet)
    try {
      const ev = JSON.parse(data) as { kind?: string; chatId?: number };
      if (ev.kind === "woken" && ev.chatId && ev.chatId === ai.chatId && !ai.running) {
        void openChat(ev.chatId).catch((e) => log.warn("follow woken chat failed", e));
      }
    } catch {
      // not JSON
    }
  });
  AppState.addEventListener("change", (s) => { if (s === "active") kick(); });
  // distances move with the price: refresh every 15 s while something active is on screen
  setInterval(() => {
    if (AppState.currentState !== "active" || !getAdminKey()) return;
    const live = [...watched.keys()].some((id) => st.byId[id]?.status === "active");
    if (live || listWatchers.active > 0) kick();
  }, 15_000);
}

export function useSignals<T>(pick: (s: SigState) => T): T {
  return useSyncExternalStore(sub, () => pick(st));
}

/** Subscriptions of one filter, loaded while mounted. */
export function useSignalList(filter: SignalFilter) {
  useEffect(() => {
    start();
    listWatchers[filter]++;
    kick();
    return () => void listWatchers[filter]--;
  }, [filter]);
  return useSignals((s) => s.lists[filter]);
}

/** Active count (badge); starts the store. */
export function useActiveSignalCount() {
  useEffect(() => { start(); kick(); }, []);
  return useSignals((s) => s.activeCount);
}

/** One subscription with its history, kept live while mounted (0 = none). */
export function useSignal(id: number) {
  useEffect(() => {
    if (!id) return;
    start();
    watched.set(id, (watched.get(id) ?? 0) + 1);
    kick();
    return () => {
      const n = (watched.get(id) ?? 1) - 1;
      if (n > 0) watched.set(id, n);
      else watched.delete(id);
    };
  }, [id]);
  return useSignals((s) => (id ? s.byId[id] : undefined));
}

/** Cancel (the caller asked to confirm). 409 = it ended meanwhile: the final state comes back with `ended`. */
export async function cancelSignal(id: number): Promise<{ s: Signal; ended: boolean }> {
  try {
    const s = await sigApi.cancel(id);
    set({ byId: { ...st.byId, [id]: s } });
    kick();
    return { s, ended: false };
  } catch (e) {
    if (!(e instanceof ApiError) || e.status !== 409) throw e;
    const s = await sigApi.get(id);
    set({ byId: { ...st.byId, [id]: s } });
    kick();
    return { s, ended: true };
  }
}

/** Create an owner subscription and refresh the lists. Errors carry the server's reason in Chinese (zhSignalError). */
export async function createSignal(b: SignalCreate): Promise<Signal> {
  try {
    const s = await sigApi.create(b);
    set({ byId: { ...st.byId, [s.id]: s } });
    kick();
    return s;
  } catch (e) {
    if (e instanceof ApiError && e.status === 400) throw new ApiError(zhSignalError(e.message), 400, e.code);
    throw e;
  }
}

// The server's validation messages are written for the agent (English); the owner reads them in Chinese.
const ZH: [RegExp, (m: RegExpMatchArray) => string][] = [
  [/condition already holds now \((.*)\)/, (m) => `条件已经成立（${m[1]}）。订阅只在条件从不成立变为成立时触发：现在就行动，或换下一个价位。`],
  [/no current data for: (.*?) — /, (m) => `暂时没有数据（${m[1]}），还无法监控`],
  [/more than 50% away from the current price (\S+)/, (m) => `离现价 ${m[1]} 超过 50%`],
  [/wider than 20%/, () => "区间宽度超过 20%，请改用价格突破/跌破"],
  [/time is already past/, () => "这个时间已经过了"],
  [/cannot read at/, () => "时间格式应为 2026-11-02 10:00（UTC+8）"],
  [/intent is required/, () => "请写清触发后让 AI 做什么（至少 6 个字）"],
  [/intent is too long \(≤ (\d+)/, (m) => `说明太长（最多 ${m[1]} 字）`],
  [/expiresHours must be between (\S+) and (\S+)/, (m) => `有效期需在 ${m[1]}–${m[2]} 小时之间`],
  [/maxFires must be between 1 and (\d+)/, (m) => `触发次数需在 1–${m[1]} 之间`],
  [/(\d+) subscriptions are active; the cap is (\d+)/, (m) => `已有 ${m[1]} 个活跃订阅，上限 ${m[2]} 个，先取消一个`],
  [/unknown instrument (\S+)/, (m) => `未知合约 ${m[1]}`],
  [/unknown field/, () => "不支持这个字段"],
  [/every tick is not allowed with a script/, () => "脚本信号不能选实时检查：脚本在它的 K 线收盘时运行"],
  [/(\d+) script signals are active; the cap is (\d+)/, (m) => `已有 ${m[1]} 个脚本信号，上限 ${m[2]} 个，先取消一个`],
  [/script signals need the strategy runner|script runner:/, () => "脚本执行环境（策略容器）暂时不可用，稍后再试"],
  [/script rejected \((\w+)\): (.*)/, (m) => `脚本未通过检验（${PROBLEM_LABEL[m[1] as ScriptProblem] ?? m[1]}）：${m[2]}`],
];

export const PROBLEM_LABEL: Record<ScriptProblem, string> = {
  compile: "代码有误", error: "回放中出错", slow: "太慢", often: "触发太频繁", unknown: "判断不了", holds: "条件已经成立", nodata: "没有数据",
};
export function zhSignalError(msg: string) {
  for (const [re, f] of ZH) {
    const m = msg.match(re);
    if (m) return f(m);
  }
  return msg;
}

/** "3 小时 12 分" until ms. */
export function leftText(ms: number) {
  if (ms <= 0) return "已到期";
  const m = Math.round(ms / 60_000);
  if (m >= 1440) return `${Math.floor(m / 1440)} 天 ${Math.floor((m % 1440) / 60)} 小时`;
  if (m >= 60) return `${Math.floor(m / 60)} 小时 ${m % 60} 分`;
  return `${Math.max(1, m)} 分钟`;
}

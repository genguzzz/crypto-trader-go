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

const log = logger("signals");

export type SignalStatus = "active" | "fired" | "expired" | "cancelled";
export type WakeState = "queued" | "started" | "skipped" | "failed";
export interface SignalLevel { instId: string; px: number; label: string }
export interface SignalProgress { instId?: string; px?: number; target?: number; pct: number; text: string }
export interface SignalFire { id: number; ts: number; px?: number; matched: string; wake: WakeState; wakeChat?: number; wakeAt?: number; error?: string }
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
}
export interface SignalCaps { maxActive: number; perChat: number; wakesPerDay: number; wakesToday: number; active: number; maxDays: number }
interface SignalList { signals: Signal[] | null; caps: SignalCaps; now: number }
export type SignalFilter = "active" | "fired" | "ended";
export interface SignalCreate { spec: Record<string, unknown>; intent: string; expiresHours: number; maxFires?: number }

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
];
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

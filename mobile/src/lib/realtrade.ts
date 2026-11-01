// AI-proposed actions on the owner's OKX account — open, add 补仓, reduce 减仓, close 平仓, adjust TP/SL;
// market or limit, isolated or cross — waiting for his approval (server internal/realtrade). The chat
// agent and signal runs call the MCP tool propose_trade; the owner approves or rejects from Telegram,
// the web or here (first answer wins). Owner only: every call carries the key, a 401 shows nothing.
// One global store: the open list (pending → orb badge), the recent list while the pane shows it,
// and single proposals the chat renders, refetched on the stream event `realtrade` and on foreground.
import { useEffect, useSyncExternalStore } from "react";
import { AppState } from "react-native";
import { ApiError, req } from "./api";
import { getAdminKey, useAdminKeySet } from "./adminKey";
import { onStreamEvent } from "./market";
import { logger } from "./log";

const log = logger("realtrade");

export type RtMode = "dryrun" | "live";
export type ProposalStatus = "pending" | "approved" | "executing" | "executed" | "failed" | "rejected" | "expired" | "invalidated";
export interface ProposalEvent { ts: number; kind: string; text: string; by?: string }
export type ProposalKind = "open" | "add" | "reduce" | "close" | "adjust";
export type OrdState = "live" | "partially_filled" | "filled" | "canceled";
export interface Proposal {
  id: number; created: number; updated: number;
  /** trigger = the trigger's label, '' for chat */
  source: "chat" | "signal" | "schedule"; trigger: string;
  chatId: number;
  /** open | add 补仓 | reduce 减仓 | close 平仓 | adjust 调整止盈止损 */
  kind: ProposalKind;
  /** the position's side */
  instId: string; side: "long" | "short";
  marginMode: "isolated" | "cross"; orderType: "market" | "limit"; px?: number;
  marginUsdt: number; leverage: number; contracts: number; notionalUsdt: number;
  /** reduce: share of the position */
  fraction?: number;
  /** slPct / tpPct: distance from the entry in %, positive; sl / tp 0 = none (reduce, close) */
  refPx: number; sl: number; tp: number; slPct: number; tpPct: number; rr: number;
  /** the AI position acted on (its open's id; 0 = the owner's own) and its size / average at creation */
  posId?: number; posContracts?: number; posAvgPx?: number;
  /** add: average once filled; estimated liquidation; what happens to the TP/SL; a check not made */
  newAvgPx?: number; liqPx?: number; tpsl?: string; note?: string;
  /** limit: minutes it rests, the time the server cancels the rest, its state on OKX, fills */
  orderMin?: number; orderUntil?: number; ordState?: OrdState; filled?: number; fillPx?: number;
  /** an open AI position now (executed open) */
  curContracts?: number; curAvgPx?: number; curSl?: number; curTp?: number;
  rationale: string; invalidation: string;
  expiresAt: number;
  status: ProposalStatus;
  mode: RtMode; statusAt: number; error?: string;
  by?: string; via?: string;
  execPx?: number; ordId?: string;
  request?: unknown; response?: unknown;
  closedAt?: number; closeKind?: "tp" | "sl" | "manual" | "unfilled" | "other"; closePx?: number; pnlUsdt?: number;
  events?: ProposalEvent[];
}
export interface RtTrigger {
  id: string; kind: "schedule" | "alert"; label: string; enabled: boolean; everyMin?: number; ruleIds?: number[];
  cooldownMin: number; prompt: string; lastFiredAt?: number; lastChatId?: number; lastError?: string;
}
export interface RtConfig {
  mode: RtMode;
  limits: { minLeverage?: number; maxLeverage: number; minMarginUsdt?: number; maxMarginUsdt: number; maxOpen: number; dailyLossUsdt: number; maxDriftPct: number; minRR: number; expiryMin: number; maxAdds?: number; limitMaxMin?: number };
  telegram: { enabled: boolean; state: string; bot?: string; lastError?: string };
  triggers: RtTrigger[] | null;
}
interface ProposalList { proposals: Proposal[] | null; now: number; mode: RtMode }

const STATUS: Record<ProposalStatus, string> = {
  pending: "待审核", approved: "已同意", executing: "执行中", executed: "已执行", failed: "失败",
  rejected: "已拒绝", expired: "已过期", invalidated: "已失效",
};
export const statusLabel = (p: Proposal) => (p.status === "executed" && p.mode === "dryrun" ? "已模拟执行" : STATUS[p.status] ?? p.status);
export const KIND: Record<ProposalKind, string> = { open: "开仓", add: "补仓", reduce: "减仓", close: "平仓", adjust: "调整止盈止损" };
export const kindLabel = (p: Proposal) => KIND[p.kind] ?? "开仓";
export const mgnLabel = (p: Proposal) => (p.marginMode === "cross" ? "全仓" : "逐仓");
/** A limit order's state on OKX: 挂单中 · 已成交 x/y · 已撤单 ('' for market orders and before execution). */
export function orderStateLabel(p: Proposal) {
  const n = (v?: number) => String(+(v ?? 0).toPrecision(6));
  switch (p.ordState) {
    case "live": return "挂单中";
    case "partially_filled": return `挂单中 · 已成交 ${n(p.filled)}/${n(p.contracts)}`;
    case "canceled": return p.filled ? `已撤单 · 成交 ${n(p.filled)}/${n(p.contracts)}` : "已撤单 · 未成交";
    case "filled": return p.orderType === "limit" ? `已成交 ${n(p.filled)}/${n(p.contracts)}` : "";
  }
  return "";
}
export const statusTone = (s: ProposalStatus): "good" | "bad" | "warn" | "info" | undefined =>
  s === "pending" ? "warn" : s === "approved" || s === "executing" ? "info" : s === "executed" ? "good" : s === "failed" ? "bad" : undefined;

const quiet = { quiet: true };
const base = "/api/v1/realtrade";
export const rtApi = {
  list: (status: "pending" | "open" | "all", limit = 0) => req<ProposalList>(`${base}/proposals?status=${status}${limit ? `&limit=${limit}` : ""}`, quiet),
  get: (id: number) => req<Proposal>(`${base}/proposals/${id}`, quiet),
  approve: (id: number) => req<Proposal>(`${base}/proposals/${id}/approve`, { method: "POST", body: { confirm: true, via: "app" } }),
  reject: (id: number) => req<Proposal>(`${base}/proposals/${id}/reject`, { method: "POST", body: { via: "app" } }),
  config: () => req<RtConfig>(`${base}/config`, quiet),
};

// ---------- store ----------

interface RtState {
  owner: boolean;
  mode: RtMode | "";
  /** server clock − device clock, for countdowns */
  skew: number;
  /** status=open (pending | approved | executing) */
  open: Proposal[];
  pending: Proposal[];
  /** status=all, loaded while the pane shows it */
  recent: Proposal[] | null;
  /** single proposals (with events) the chat shows; null = gone (404) */
  byId: Record<number, Proposal | null>;
}
let st: RtState = { owner: false, mode: "", skew: 0, open: [], pending: [], recent: null, byId: {} };
const listeners = new Set<() => void>();
const sub = (l: () => void) => { listeners.add(l); return () => void listeners.delete(l); };
const set = (patch: Partial<RtState>) => { st = { ...st, ...patch }; listeners.forEach((l) => l()); };

const watched = new Map<number, number>();
let recentWatchers = 0;

export const serverNow = () => Date.now() + st.skew;
const put = (p: Proposal) => set({ byId: { ...st.byId, [p.id]: p }, recent: st.recent?.map((x) => (x.id === p.id ? p : x)) ?? null });

let running = false;
let again = false;
export async function refreshProposals(): Promise<void> {
  if (!getAdminKey()) {
    if (st.owner) set({ owner: false, open: [], pending: [], recent: null });
    return;
  }
  if (running) { again = true; return; }
  running = true;
  try {
    const ids = [...watched.keys()];
    const [open, all, one] = await Promise.all([
      rtApi.list("open"),
      recentWatchers ? rtApi.list("all", 40) : Promise.resolve(null),
      Promise.all(ids.map((id) => rtApi.get(id).catch((e) => (e instanceof ApiError && e.status === 404 ? null : undefined)))),
    ]);
    const byId = { ...st.byId };
    ids.forEach((id, i) => { if (one[i] !== undefined) byId[id] = one[i]; });
    const list = open.proposals ?? [];
    set({
      owner: true, mode: open.mode, skew: open.now ? open.now - Date.now() : 0,
      open: list, pending: list.filter((p) => p.status === "pending"),
      recent: all ? all.proposals ?? [] : st.recent, byId,
    });
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) set({ owner: false, open: [], pending: [], recent: null });
    else log.warn("refresh failed", e); // offline: keep the last state
  } finally {
    running = false;
    if (again) { again = false; void refreshProposals(); }
  }
}

let kickTimer: ReturnType<typeof setTimeout> | null = null;
const kick = () => {
  if (kickTimer) return;
  kickTimer = setTimeout(() => { kickTimer = null; void refreshProposals(); }, 250);
};

let started = false;
function start() {
  if (started) return;
  started = true;
  onStreamEvent((type) => { if (type === "realtrade") kick(); });
  AppState.addEventListener("change", (s) => { if (s === "active") kick(); });
  // Fallback for a missed stream event (stream reconnecting, answered in Telegram meanwhile): while
  // something can still change — an open proposal, or one on screen that isn't final — poll every 5 s.
  setInterval(() => {
    if (AppState.currentState !== "active" || !getAdminKey()) return;
    if (st.open.length || [...watched.keys()].some((id) => live(st.byId[id]))) kick();
  }, 5000);
}

const OPEN = new Set(["pending", "approved", "executing"]);
/** Still changing: open, or a resting limit order. */
const live = (p?: Proposal | null) => !!p && (OPEN.has(p.status) || p.ordState === "live" || p.ordState === "partially_filled");

/** A slice of the store. The first owner-key-aware caller (the orb) starts the stream listener. */
export function useRealtrade<T>(pick: (s: RtState) => T): T {
  return useSyncExternalStore(sub, () => pick(st));
}

/** Pending proposals (orb badge, pane); starts the store and reloads when the owner key changes. */
export function usePendingProposals() {
  const owner = useAdminKeySet();
  useEffect(() => {
    start();
    kick();
  }, [owner]);
  return useRealtrade((s) => s.pending);
}

/** One proposal, kept live while mounted (0 = none). */
export function useProposal(id: number) {
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
  return useRealtrade((s) => (id ? s.byId[id] : undefined));
}

/** Recent proposals (all statuses), loaded while mounted. */
export function useRecentProposals() {
  useEffect(() => {
    start();
    recentWatchers++;
    kick();
    return () => void recentWatchers--;
  }, []);
  return useRealtrade((s) => s.recent);
}

/** Approve (confirm already asked) or reject. A proposal answered elsewhere first (409) comes back
 *  in its final state with `conflict`. */
export async function answerProposal(id: number, action: "approve" | "reject"): Promise<{ p: Proposal; conflict: boolean }> {
  try {
    const p = await (action === "approve" ? rtApi.approve(id) : rtApi.reject(id));
    put(p);
    kick();
    return { p, conflict: false };
  } catch (e) {
    if (!(e instanceof ApiError) || e.status !== 409) throw e;
    const p = await rtApi.get(id);
    put(p);
    kick();
    return { p, conflict: true };
  }
}

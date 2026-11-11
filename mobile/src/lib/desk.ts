// The trading desk (交易台): one standing AI chat the server runs by itself — on pool-wide strategy
// scans (batched and paced) and on a schedule a few minutes before the hour — with a journal of every
// decision, followed afterwards (price 1 h / 4 h / 24 h later, the proposal's fate), and a long-term
// memory. The desk only proposes; approval stays with the owner (lib/realtrade.ts).
// Server: internal/autopilot (docs/trading-design.md §9). The overview is one shared store, refetched
// on the stream event `autopilot` and on foreground.
import { useEffect, useSyncExternalStore } from "react";
import { AppState } from "react-native";
import { ApiError, req } from "./api";
import { time } from "./fmt";
import { getAdminKey, useAdminKeySet } from "./adminKey";
import { logger } from "./log";
import { onStreamEvent } from "./market";
import { ai, openChat } from "./ai";

const log = logger("desk");

export interface DeskRun {
  id: number; ts: number; trigger: "schedule" | "signal" | "manual"; label: string; chatId: number; status: "running" | "done" | "failed";
  error?: string; signals: number; instIds: string[]; newSession?: boolean; finishedAt?: number; summary?: string; costUsd?: number;
  tokens?: number; latencyMs?: number; decisions: number; proposals: number;
}
export interface DeskStatus {
  enabled: boolean; chatId: number; running?: DeskRun; lastRun?: DeskRun; nextScheduleAt?: number; pending: number; runs24h: number;
  signalRuns24h: number; cost24hUsd: number; decisions24h: number; tradeMode?: string; lastError?: string; now: number;
}
export interface DeskParamDef { key: string; label: string; unit: string; def: number; min: number; max: number; step: number }
export interface DeskScan { signalId: number; desc: string; cooldownMin: number; fireCount: number; lastFiredAt?: number; progress?: string }
export interface DeskStrategy {
  key: string; label: string; desc: string; custom?: boolean; enabled: boolean; params?: Record<string, number>; paramDefs?: DeskParamDef[];
  scans: DeskScan[]; error?: string; fires24h: number;
}
export interface DeskConfig {
  enabled: boolean;
  schedule: { enabled: boolean; everyHours: number; leadMin: number };
  signals: { enabled: boolean; minGapMin: number; maxRunsDay: number; batchSec: number; staleMin: number };
  strategies: { key: string; enabled: boolean; params: Record<string, number> }[];
  custom: { key: string; label: string; enabled: boolean; spec: unknown; cooldownMin: number }[];
  model: string; effort: string; sessionRuns: number; maxProposals: number; focus: string;
}
export interface DeskOverview {
  status: DeskStatus; config: DeskConfig; strategies: DeskStrategy[];
  options: { everyHours: number[]; models: string[]; efforts: string[]; actions: string[]; verdicts: string[]; memoryKinds: string[] };
}
export type DeskAction = "open" | "add" | "reduce" | "close" | "adjust" | "hold" | "watch" | "skip";
export interface DeskDecision {
  id: number; ts: number; runId?: number; instId: string; action: DeskAction; side?: "long" | "short"; conviction?: "low" | "medium" | "high";
  thesis: string; entry?: number; sl?: number; tp?: number; invalidation?: string; tags?: string; strategy?: string; trigger?: string;
  proposalId?: number; px: number; snapshot?: { fields?: Record<string, number>; btcPx?: number };
  px1h?: number; px4h?: number; px24h?: number; ret1h?: number; ret4h?: number; ret24h?: number; mfe24h?: number; mae24h?: number;
  outcomeAt?: number; proposalStatus?: string; pnlUsdt?: number; closedAt?: number; closeKind?: string;
  review?: string; verdict?: "right" | "wrong" | "mixed" | "na"; reviewedAt?: number;
}
export interface DeskMemory {
  id: number; ts: number; updated: number; kind: "rule" | "lesson" | "regime" | "coin"; instId?: string; text: string; by: "ai" | "owner";
  sourceDecision?: number; status: "active" | "retired"; retiredReason?: string;
}
export interface DeskBucket { key: string; n: number; followed: number; winRate4h?: number; avgRet1h?: number; avgRet4h?: number; avgRet24h?: number }
export interface DeskStats {
  days: number; decisions: number; runs: number; costUsd: number;
  proposals: { made: number; executed: number; rejected: number; expired: number; closed: number; wins: number; pnlUsdt: number };
  directional: DeskBucket; byAction: DeskBucket[]; byConviction: DeskBucket[]; byStrategy: DeskBucket[]; byTrigger: DeskBucket[];
  verdicts: Record<string, number>;
}

export const actionText: Record<string, string> = { open: "开仓", add: "加仓", reduce: "减仓", close: "平仓", adjust: "调止盈止损", hold: "持有", watch: "观望", skip: "不做" };
export const convictionText: Record<string, string> = { low: "低", medium: "中", high: "高" };
export const verdictText: Record<string, string> = { right: "判断正确", wrong: "判断错误", mixed: "部分正确", na: "无法判定" };
export const memoryKindText: Record<string, string> = { rule: "规则", lesson: "教训", regime: "市场状态", coin: "币种" };
export const triggerText: Record<string, string> = { schedule: "定时", signal: "信号", manual: "手动", "—": "对话中" };
export const strategyText = (key: string, list: DeskStrategy[] | undefined) => {
  const base = key.startsWith("custom:") ? key : key.split(":")[0];
  return list?.find((s) => s.key === base)?.label ?? (key === "—" ? "无信号" : key);
};
/** A desk run's message in its chat starts with this (internal/autopilot brief.go). */
export const isDeskBrief = (text: string) => text.startsWith("🤖 交易台");

const enc = encodeURIComponent;
const qs = (o: Record<string, string | number | undefined>) => {
  const p = Object.entries(o).filter(([, v]) => v !== undefined && v !== "" && v !== 0).map(([k, v]) => `${k}=${enc(String(v))}`).join("&");
  return p ? "?" + p : "";
};

export const deskApi = {
  overview: () => req<DeskOverview>("/api/v1/autopilot", { quiet: true }),
  save: (c: DeskConfig) => req<DeskOverview>("/api/v1/autopilot/config", { method: "PUT", body: c }),
  run: (note = "") => req<DeskRun>("/api/v1/autopilot/run", { method: "POST", body: { note } }),
  runs: (limit = 30) => req<{ runs: DeskRun[] }>(`/api/v1/autopilot/runs?limit=${limit}`),
  journal: (q: { instId?: string; action?: string; days?: number; before?: number; limit?: number } = {}) =>
    req<{ decisions: DeskDecision[] }>("/api/v1/autopilot/journal" + qs(q)),
  review: (id: number, verdict: string, review: string) => req<DeskDecision>(`/api/v1/autopilot/journal/${id}/review`, { method: "POST", body: { verdict, review } }),
  memory: (status = "all") => req<{ memory: DeskMemory[]; max: number }>(`/api/v1/autopilot/memory?status=${status}`),
  remember: (kind: string, text: string, instId = "") => req<DeskMemory>("/api/v1/autopilot/memory", { method: "POST", body: { kind, text, instId } }),
  retire: (id: number, reason = "") => req<{ ok: boolean }>(`/api/v1/autopilot/memory/${id}?reason=${enc(reason)}`, { method: "DELETE" }),
  stats: (days = 30) => req<DeskStats>(`/api/v1/autopilot/stats?days=${days}`),
};

// ---------- the shared overview ----------

interface DeskState { owner: boolean; data: DeskOverview | null; error: string; skew: number; rev: number }
let st: DeskState = { owner: false, data: null, error: "", skew: 0, rev: 0 };
const subs = new Set<() => void>();
const sub = (l: () => void) => { subs.add(l); return () => void subs.delete(l); };
const set = (p: Partial<DeskState>) => { st = { ...st, ...p }; subs.forEach((l) => l()); };

let busy = false;
let again = false;
export async function refreshDesk() {
  if (!getAdminKey()) { if (st.owner || st.data) set({ owner: false, data: null }); return; }
  if (busy) { again = true; return; }
  busy = true;
  try {
    const d = await deskApi.overview();
    set({ owner: true, data: d, error: "", skew: d.status.now - Date.now() });
  } catch (e) {
    if (e instanceof ApiError && (e.status === 401 || e.status === 404)) set({ owner: e.status !== 401, data: null, error: e.status === 404 ? "服务器还没有交易台功能（需要更新后端）" : "" });
    else { log.warn("refresh failed", e); set({ error: e instanceof Error ? e.message : String(e) }); }
  } finally {
    busy = false;
    if (again) { again = false; void refreshDesk(); }
  }
}

let timer: ReturnType<typeof setTimeout> | null = null;
const kick = () => {
  if (timer) return;
  timer = setTimeout(() => { timer = null; void refreshDesk(); }, 250);
};

let started = false;
function start() {
  if (started) return;
  started = true;
  onStreamEvent((type, data) => {
    if (type !== "autopilot") return;
    kick();
    set({ rev: st.rev + 1 }); // lists (journal, memory, runs) refetch on it
    // a run started in the desk's chat while it is on screen and idle: follow the new turn
    try {
      const ev = JSON.parse(data) as { kind?: string };
      const chat = st.data?.status.chatId;
      if (ev.kind === "run" && chat && chat === ai.chatId && !ai.running) void openChat(chat).catch((e) => log.warn("follow run failed", e));
    } catch {
      // not JSON
    }
  });
  AppState.addEventListener("change", (s) => { if (s === "active") kick(); });
}

/** The desk's overview (null until loaded, or when the server has no desk); starts the store. */
export function useDesk() {
  const keyed = useAdminKeySet(); // refetch when the owner key is entered or changed
  useEffect(() => { start(); kick(); }, [keyed]);
  return useSyncExternalStore(sub, () => st);
}

/** Server time now (the status carries the server clock; countdowns must not depend on the phone's). */
export const deskNow = () => Date.now() + st.skew;

export async function saveDesk(c: DeskConfig) {
  const d = await deskApi.save(c);
  set({ owner: true, data: d, error: "" });
  return d;
}

export async function runDeskNow(note = "") {
  const r = await deskApi.run(note);
  kick();
  return r;
}

/** "13:50" today, "明天 01:50" / "11-12 01:50" otherwise — in the app's display zone (lib/fmt.ts), like every other time. */
export function clockAt(ms: number) {
  const [day, hm] = time(ms, true).split(" ");
  if (day === time(deskNow(), true).split(" ")[0]) return hm;
  return day === time(deskNow() + 86_400_000, true).split(" ")[0] ? `明天 ${hm}` : `${day} ${hm}`;
}

/** "12 分钟后" / "1 小时 5 分后" / "现在". */
export function inText(ms: number) {
  const m = Math.round((ms - deskNow()) / 60_000);
  if (m <= 0) return "现在";
  return m < 60 ? `${m} 分钟后` : m % 60 ? `${Math.floor(m / 60)} 小时 ${m % 60} 分后` : `${m / 60} 小时后`;
}

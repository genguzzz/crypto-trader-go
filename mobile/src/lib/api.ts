// Client for the ctg REST API (same routes and types as web/src/api.ts). Every call carries a request
// id, the app version and the owner key; a 401 admin_required waits for KeyGate's new key and retries once.
import { Platform } from "react-native";
import * as Application from "expo-application";
import { getAdminKey, keyRejected, loadAdminKey } from "./adminKey";
import { logger } from "./log";
import { runningLabel } from "./updates";

const log = logger("api");

// The web preview is served same-origin by a local proxy (see CLAUDE.md); the app talks to production.
export const API_ORIGIN = Platform.OS === "web" ? "" : "https://ctg.gugenzzz.top";
// Pages the app shows in a WebView (the K-line chart) always come from production.
export const WEB_ORIGIN = "https://ctg.gugenzzz.top";
export const appVersion = `${Application.nativeApplicationVersion ?? "dev"} (${Application.nativeBuildVersion ?? "0"}) ${runningLabel()}`;

export class ApiError extends Error {
  status: number;
  code?: string;
  constructor(message: string, status: number, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

const rid = () => Math.random().toString(36).slice(2, 10);

type ReqOpts = { method?: string; body?: unknown; timeoutMs?: number; quiet?: boolean };

export async function req<T>(path: string, opts: ReqOpts = {}, retried = false): Promise<T> {
  await loadAdminKey();
  if (!getAdminKey()) await keyRejected(""); // no key yet: wait for KeyGate instead of sending a 401
  const id = rid();
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), opts.timeoutMs ?? 20_000);
  const t0 = Date.now();
  const admin = getAdminKey();
  let res: Response;
  let text = "";
  try {
    res = await fetch(API_ORIGIN + path, {
      method: opts.method ?? (opts.body !== undefined ? "POST" : "GET"),
      headers: {
        "x-request-id": id,
        "x-app-version": appVersion.replace(/[^\x20-\x7e]/g, "?"), // header values must be ASCII
        ...(admin ? { "x-ctg-admin": admin } : {}),
        ...(opts.body !== undefined ? { "content-type": "application/json" } : {}),
      },
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
      signal: ctrl.signal,
    });
    text = await res.text();
  } catch (e) {
    const aborted = (e as { name?: string }).name === "AbortError";
    log.warn("call failed", { path: path.split("?")[0], rid: id, aborted, err: e });
    throw new ApiError(aborted ? "请求超时" : "无法连接服务器", 0);
  } finally {
    clearTimeout(timer);
  }
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = text;
  }
  if (!opts.quiet || !res.ok) log.info("call", { path: path.split("?")[0], status: res.status, ms: Date.now() - t0, rid: id });
  if (!res.ok) {
    const body = (data && typeof data === "object" ? data : {}) as { error?: string; code?: string };
    if (res.status === 401 && body.code === "admin_required" && !retried) {
      await keyRejected(admin);
      return req<T>(path, opts, true);
    }
    if (res.status === 429 && body.code === "rate_limited") throw new ApiError("密钥错误次数过多，请稍后再试", 429, body.code);
    throw new ApiError(body.error || `服务器返回 ${res.status}`, res.status, body.code);
  }
  return data as T;
}

const enc = encodeURIComponent;
const json = (method: string, body: unknown): ReqOpts => ({ method, body });

// ---------- types (web/src/api.ts) ----------

export interface Field { key: string; label: string; unit: string; desc: string; replayable: boolean }
export interface Contribution { field: string; weight: number; pct: number; points: number }
export interface SideScore { score: number | null; contrib: Contribution[] | null; missing?: string[] }
export interface Score { long: SideScore; short: SideScore; gate: string }
export interface Row {
  instId: string; category: string; status: "live" | "stale" | "warming" | "new";
  active: boolean; rank?: number; turnover12h: number; provisional?: boolean; have: number;
  last: number; values: Record<string, number> | null; pct: Record<string, number> | null;
  scores?: Record<string, Score>; watch?: boolean; hasSpot: boolean; listTime: number; freshnessMs: number;
  eligible?: boolean; mcap?: number; oiUsd?: number;
}
export interface Removed { instId: string; reason: string; at: number; turnover12h: number }
export interface Coverage { eligible: number; ready: number; warming: number; stale: number; new: number; active: number }
export interface Snapshot {
  seq: number; asOfMinute: number; decisionTs: number; computedAt: number; provisional: boolean; phase: string;
  preview?: boolean; previewAt?: number;
  threshold: number; topN: number; coverage: Coverage; rows: Row[] | null; removed: Removed[] | null;
}
export interface Rule {
  id: string; name: string; version: number; description: string; preset: boolean;
  long: Record<string, number>; short: Record<string, number>; gates: { maxSpreadBps?: number };
}
/** Level = [price, usdt notional, contracts] */
export type BookLevel = [number, number, number];
export interface OrderBook { instId: string; ts: number; receivedAt: number; ctVal: number; mid: number; bestBid: number; bestAsk: number; spreadBps: number; bids: BookLevel[]; asks: BookLevel[]; levels: number; coverBid: number; coverAsk: number }
export interface Settings { threshold: number; topN: number; categories: string[]; defaultRule: string; storageCapMB: number }
export interface CatalogItem {
  instId: string; category: string; listTime: number; preMarket?: boolean; recorded: boolean;
  active?: boolean; rank?: number; watch?: boolean; last: number; open24h: number;
}
export interface SeriesPoint { t: number; v: number; u?: number }
export interface LiqBar { t: number; long: number; short: number; nl: number; ns: number }
export interface LiqEvent { t: number; long: boolean; px: number; usd: number }
export interface ExhaustionItem { key: string; label: string; weight: number; points: number }
export interface FadePlan { side: "long" | "short"; entryLo: number; entryHi: number; stop: number; tp1: number; tp2: number; rr: number }
/** One side of the validated move risk (server internal/market/moverisk.go, rev-study spec). */
export interface MoveRiskSide { inState: boolean; ext: number; pos30: number; pTail: number; pBig3: number; baseTail: number; baseBig3: number; extreme: number }
export interface MoveRisk { t: number; close: number; atr: number; top: MoveRiskSide; bottom: MoveRiskSide }
/** Top/bottom exhaustion checklist (a description; no direction power out of sample) + the move risk. */
export interface Exhaustion {
  instId: string; bar: string; t: number; last: number; top: number; bottom: number; atTop: number; atBottom: number;
  topItems: ExhaustionItem[]; bottomItems: ExhaustionItem[]; atr: number; ema20: number; high: number; low: number; plan?: FadePlan;
  /** shorting vetoed: crowded shorts (funding ≤ −0.05%/settlement, or negative and >15% above 1H EMA24) */
  squeeze?: boolean; squeezeWhy?: string;
  risk?: MoveRisk;
}
export interface Liquidations { bars: LiqBar[]; since: number; recent?: LiqEvent[]; err?: string; syncing?: boolean }
export interface Coin { id: string; symbol: string; name: string; rank: number; marketCap: number; fdv: number; circulating: number; total: number; max: number; updatedAt: string; fetchedAt: number }
export interface Meta { fields: Field[]; rules: Rule[]; settings: Settings; categories: Record<string, string> }
export interface Candle { t: number; o: number; h: number; l: number; c: number; q: number; x: boolean }
export interface InstrumentInfo {
  instId: string; category: string; spotId?: string; listTime: number; last: number; bid: number; ask: number; tickTs: number;
  spotLast?: number; fundingRate: number; fundingTime: number; fundingIntervalH: number;
  open24h?: number; turnover24h?: number; markPx?: number; indexPx?: number;
  oiCcy: number; oiUsd: number; oiTs: number; row?: Row;
  watch: boolean; eligible: boolean; tier: string; syncing: boolean; syncMsg?: string; coin?: Coin;
}
export interface ComparePoint { t: number; v: Record<string, number> }
/** The owner's open OKX position, read-only (server internal/okxacct; web components/charts/positions.ts). */
export interface PosTrigger { algoId: string; px: number; pxType?: string; kind: string; full: boolean; contracts?: number }
export interface Position {
  instId: string; posSide: string; side: "long" | "short"; mgnMode: string;
  contracts: number; coins?: number; notionalUsd?: number;
  avgPx: number; markPx?: number; liqPx?: number; lever?: number;
  upl: number; uplRatio: number; mgnRatio?: number; margin?: number;
  tp: PosTrigger[]; sl: PosTrigger[]; cTime: number; uTime: number;
}
export interface AccountPositions {
  enabled: boolean; ts?: number; positions: Position[];
  equity?: { totalEq: number; availEq?: number; upl?: number; mgnRatio?: number; notionalUsd?: number };
}

// ---------- endpoints ----------

export const api = {
  meta: () => req<Meta>("/api/v1/meta", { quiet: true }),
  universe: (scope = "active") => req<Snapshot>(`/api/v1/universe?scope=${scope}`, { quiet: true }),
  instrument: (id: string) => req<InstrumentInfo>(`/api/v1/instruments/${enc(id)}`, { quiet: true }),
  catalog: () => req<{ items: CatalogItem[] }>("/api/v1/catalog"),
  orderbook: (id: string) => req<OrderBook>(`/api/v1/orderbook?instId=${enc(id)}`, { quiet: true }),
  candles: (id: string, bar: string, limit = 300, market = "swap") =>
    req<Candle[]>(`/api/v1/candles?instId=${enc(id)}&bar=${bar}&limit=${limit}&market=${market}`, { quiet: true }),
  compare: (id: string, bar: string, limit = 300) => req<{ ids: string[]; points: ComparePoint[] | null }>(`/api/v1/compare?instId=${enc(id)}&bar=${bar}&limit=${limit}`),
  series: (id: string, field: "oi" | "funding", hours = 24, step = 0) =>
    req<SeriesPoint[] | null>(`/api/v1/series?instId=${enc(id)}&field=${field}&hours=${hours}${step > 0 ? `&step=${step}` : ""}`),
  exhaustion: (id: string, bar = "1H") => req<Exhaustion>(`/api/v1/exhaustion?instId=${enc(id)}&bar=${bar}`, { quiet: true }),
  liquidations: (id: string, bar: string, limit: number, recent = 0) =>
    req<Liquidations>(`/api/v1/liquidations?instId=${enc(id)}&bar=${bar}&limit=${limit}&recent=${recent}`),
  watch: (id: string, on: boolean) => req<{ ok: boolean }>(`/api/v1/watchlist/${enc(id)}`, { method: on ? "PUT" : "DELETE" }),
  status: () => req<Record<string, unknown>>("/api/v1/status"),
  // the owner's OKX positions (read-only)
  positions: (id?: string) => req<AccountPositions>(`/api/v1/account/positions${id ? `?instId=${enc(id)}` : ""}`, { quiet: true }),
  // native app plumbing (internal/api/mobile.go)
  events: (events: unknown[]) => req<{ ok: boolean }>("/api/v1/app/events", { ...json("POST", { events }), quiet: true }),
  uploadLog: (log: string, meta: Record<string, unknown>) => req<{ id: string }>("/api/v1/app/logs", { ...json("POST", { log, meta }), timeoutMs: 60_000 }),
};

/** Checks a key against a cheap authenticated endpoint (KeyGate). Not through req: no retry, no gate. */
export async function verifyKey(k: string): Promise<"ok" | "bad" | "limited" | string> {
  try {
    const r = await fetch(API_ORIGIN + "/api/v1/settings", { headers: { "x-ctg-admin": k, "x-request-id": rid() } });
    log.info("verify key", { status: r.status });
    if (r.ok) return "ok";
    if (r.status === 401) return "bad";
    if (r.status === 429) return "limited";
    return `服务器返回 ${r.status}`;
  } catch (e) {
    log.warn("verify key failed", e);
    return "无法连接服务器";
  }
}

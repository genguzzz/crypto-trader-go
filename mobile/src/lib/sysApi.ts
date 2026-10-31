// Endpoints and types for the drawer pages (rules + track record, alerts, system status, settings,
// AI authorization, interaction channel, Dev), ported from web/src/api.ts, web/src/tradeApi.ts and
// web/src/components/status/storage.ts. Kept apart from api.ts so the tab screens' client stays small.
import { api, req, type Field, type Rule, type Settings } from "./api";
import { load } from "./query";

const enc = encodeURIComponent;
const send = (method: string, body?: unknown) => ({ method, body });

// ---------- rules / track record ----------

export interface Stats {
  /** Metrics are null when not measured (no cross-sections for that side), never a fake 0. */
  horizonH: number; crossSections: number; medianNames: number; longN: number; shortN: number;
  longIC: number | null; longT: number | null; longHit: number | null; shortIC: number | null; shortT: number | null; shortHit: number | null;
  longTopExcessBps: number | null; longTopNetBps: number | null; shortTopExcessBps: number | null; shortTopNetBps: number | null;
  firstTs: number; lastTs: number; cumLongIC: { ts: number; v: number }[] | null; verdict: string;
}
export interface Track { ruleId: string; version: number; replayable: boolean; replayState: string; live: Stats[] | null; replay: Stats[] | null; liveSince: number }

export const weightsText = (w: Record<string, number>, fields?: Field[]) =>
  Object.entries(w).map(([k, v]) => `${fields?.find((f) => f.key === k)?.label || k} ${v > 0 ? "+" : ""}${v}`).join("，") || "—";
/** Reload rules/settings/fields after a write (the shared "meta" query). */
export const refreshMeta = () => load("meta", () => api.meta());
export const verdictTone = (v: string) => (v.startsWith("有初步") ? "good" : v.startsWith("方向相反") ? "bad" : "warn") as "good" | "bad" | "warn";

// ---------- alerts ----------

/** params by type: price_move {window,pct,dir}, score {ruleId,side,min}, funding {source,pctPerHour,dir}. */
export type AlertType = "price_move" | "score" | "funding";
export interface AlertRule {
  id: number; name: string; enabled: boolean; type: AlertType; params: Record<string, string | number>;
  scope: "active" | "watch" | "inst"; instId?: string; cooldownMin: number; channels: { telegram: boolean; webhook: boolean };
  createdAt: number; updatedAt: number; summary: string; lastFired?: number; error?: string;
}
/** Secrets come back masked (••••abcd); sending a masked/empty value back keeps the saved one. */
export interface AlertChannels {
  telegram: { configured: boolean; botToken: string; chatId: string };
  webhook: { configured: boolean; url: string; secret: string };
}
/** delivery: channel → "ok" | "pending" | "失败：…" */
export interface AlertEvent { id: number; ts: number; ruleId: number; ruleName: string; instId: string; type: AlertType; value: number; message: string; delivery: Record<string, string> | null }

// ---------- status / storage ----------

export interface WSStatus { name: string; connected: boolean; subscriptions: number; messages: number; reconnects: number; lastMsgAgoMs: number; subErrors: number }
export interface SystemStatus {
  phase: string; uptime: string; backfillDone: number; backfillTotal: number;
  restRequests: number; restErrors: number; restRateLimited: number; ws: WSStatus[] | null;
  coverage: { eligible: number; ready: number; warming: number; stale: number; new: number; active: number };
  asOfMinute: number; computedAt: number; replay: string; history: string; hourHistory?: string;
  db: Record<string, number> | null;
  config: { threshold: number; topN: number; storageCapMB: number; replayHours: number };
  mem?: { heapAllocMB: number; heapSysMB: number; sysMB: number; goroutines: number; numGC: number; insts: number; flowDecodeErrors: number };
}
export interface TableUsage { name: string; table: string; bytes: number; budget: number; ages: string[] | null; tight: boolean }
export interface Storage {
  at: number; tookMs: number; capBytes: number; fileBytes: number; freeBytes: number; usedBytes: number;
  deleted: Record<string, number>; tables: TableUsage[] | null; tiers: Record<string, number>; tierNames: string[]; error?: string;
}

export const bytes = (b?: number) => (b == null ? "—" : b >= 1 << 30 ? (b / (1 << 30)).toFixed(2) + " GB" : (b / (1 << 20)).toFixed(0) + " MB");
export const tableLabel: Record<string, string> = {
  candles_1m: "1m K 线", oi_samples: "持仓量样本", candles_1h: "1H K 线", funding_settled: "资金费（已结算）", funding_obs: "资金费（盘中观测）",
  orderflow_1m: "订单流（逐笔 1m / 足迹）", taker_volume: "主动买卖量（OKX 统计）", other: "规则 / 评分快照 / 其他",
  funding_samples: "资金费样本", score_snapshots: "评分快照",
};
/** Storage tiers, high → low priority; report ages are indexed none, base, viewed, pool, watch. */
export const tierCols: [number, string][] = [[4, "自选"], [3, "活跃池"], [2, "最近查看"], [1, "全池录制"], [0, "未跟踪"]];
/** Compact count: 1,581,359 → 158.1万. */
export function count(n?: number | null) {
  if (n == null) return "—";
  if (n >= 1e8) return (n / 1e8).toFixed(2) + "亿";
  if (n >= 1e4) return (n / 1e4).toFixed(1) + "万";
  return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

// ---------- AI authorization / interaction channel (web/src/tradeApi.ts) ----------

export interface AiStatus {
  configured: boolean; kind?: "api_key" | "oauth_token"; masked?: string;
  storage?: "encrypted" | "plain"; storageNote?: string; updatedAt?: number; credentialError?: string;
  model: string;
  workerOnline: boolean; workerReady: boolean;
  proxyOk: boolean; proxyError?: string;
  worker?: { version: string; sdk?: string; cli?: string; ready: boolean; proxyOk: boolean; proxyError?: string; connectedAt: number };
  available: boolean;
  lastCallAt?: number; lastError?: string;
  costToday: number; callsToday: number; pendingProposals: number;
}
export interface AiTestResult { ok: boolean; model?: string; reply?: string; costUsd: number; latencyMs: number; error?: string }

/** One-line AI availability (web components/trade/meta.ts aiState). */
export function aiState(s: AiStatus): { t: string; c: "good" | "bad" | "warn"; d: string } {
  if (s.workerOnline && !s.proxyOk) return { t: "代理不可用", c: "bad", d: `AI 只经代理访问 Anthropic，代理恢复前 AI 审核暂停（信号按跳过处理），不会直连${s.proxyError ? "。" + s.proxyError : ""}` };
  if (!s.configured) return { t: "未授权", c: "warn", d: "还没有 AI 凭证，AI 审核的信号会被跳过" };
  if (!s.workerOnline) return { t: "AI 进程离线", c: "bad", d: "AI 审核进程未连接，AI 审核的信号按跳过处理" };
  if (s.credentialError) return { t: "凭证失效", c: "bad", d: s.credentialError };
  if (!s.workerReady) return { t: "未就绪", c: "warn", d: "AI 进程已连接但尚未就绪，信号暂按跳过处理" };
  if (s.lastError) return { t: "最近调用失败", c: "warn", d: s.lastError };
  return { t: "正常", c: "good", d: "" };
}

/** internal/interact ConfigView. botToken / secret are masked; sending them back masked or empty keeps them. */
export interface InteractConfig {
  telegram: { enabled: boolean; botToken: string; configured: boolean; allowUserIds: number[]; clearToken?: boolean };
  webhook: { enabled: boolean; url: string; secret: string; configured: boolean; clearSecret?: boolean; kinds: string[]; backlog?: number };
  kinds: string[]; // empty = all kinds
  quiet: boolean;
  status?: {
    state: string; bot?: string; lastError?: string; lastPollAt?: number;
    recipients: number; backlog: number; strangers?: { id: number; name: string; at: number }[];
  };
  allKinds?: { kind: string; label: string }[];
}
export const eventKinds: { value: string; label: string }[] = [
  { value: "signal", label: "信号" }, { value: "order", label: "下单" }, { value: "fill", label: "成交" }, { value: "cancel", label: "撤单" },
  { value: "stop", label: "止损" }, { value: "target", label: "止盈" }, { value: "time", label: "持仓到期" }, { value: "liquidation", label: "强平" },
  { value: "reject", label: "风控拒绝" }, { value: "breaker", label: "熔断" }, { value: "instance", label: "实例状态" }, { value: "strategy", label: "策略消息" },
  { value: "ai", label: "AI 决策" }, { value: "daily", label: "每日汇总" },
];

// ---------- Dev (external API keys) ----------

export type KeyKind = "key" | "token";
export interface ApiKey {
  id: number; kind: KeyKind; name: string; prefix: string; ratePerMin: number; burst: number;
  createdAt: number; expiresAt: number; revokedAt: number; lastUsedAt: number; requests: number; limited: number;
  status: "active" | "expired" | "revoked"; remaining: number;
}
export interface DevConfig { enabled: boolean; globalPerMin: number }
export interface DevParam { name: string; in: "query" | "path"; type: string; desc: string; required?: boolean; enum?: string[]; default?: unknown; min?: number; max?: number }
export interface DevEndpoint { name: string; path: string; summary: string; desc: string; params: DevParam[] | null; cost: string }
export interface DevEndpoints { restBase: string; mcpPath: string; endpoints: DevEndpoint[]; limits: { defaultRatePerMin: number; maxRatePerMin: number; maxBurst: number } }
export interface DevLog { ts: number; keyId: number; keyName: string; prefix: string; via: "rest" | "mcp"; endpoint: string; status: number; ms: number; cost: number; error?: string }

// ---------- endpoints ----------

export const sysApi = {
  rules: () => req<Rule[]>("/api/v1/rules"),
  saveRule: (r: Partial<Rule>) => req<Rule>("/api/v1/rules", send("POST", r)),
  deleteRule: (id: string) => req<{ ok: boolean }>(`/api/v1/rules/${enc(id)}`, send("DELETE")),
  track: (id: string) => req<Track>(`/api/v1/rules/${enc(id)}/track`),

  status: () => req<SystemStatus>("/api/v1/status", { quiet: true }),
  storage: () => req<Storage | null>("/api/v1/storage", { quiet: true }),
  settings: () => req<Settings>("/api/v1/settings"),
  saveSettings: (s: Settings) => req<Settings>("/api/v1/settings", send("PUT", s)),
  turnovers: () => req<{ asOfMinute: number; rows: [string, string, number, number][] }>("/api/v1/turnovers"),

  alertRules: () => req<{ rules: AlertRule[] | null; types: { key: AlertType; label: string }[] }>("/api/v1/alerts/rules"),
  saveAlertRule: (r: Partial<AlertRule>) => req<AlertRule>(r.id ? `/api/v1/alerts/rules/${r.id}` : "/api/v1/alerts/rules", send(r.id ? "PUT" : "POST", r)),
  deleteAlertRule: (id: number) => req<{ ok: boolean }>(`/api/v1/alerts/rules/${id}`, send("DELETE")),
  alertChannels: () => req<AlertChannels>("/api/v1/alerts/channels"),
  saveAlertChannels: (c: AlertChannels) => req<AlertChannels>("/api/v1/alerts/channels", send("PUT", c)),
  clearAlertChannel: (ch: "telegram" | "webhook") => req<AlertChannels>(`/api/v1/alerts/channels/${ch}`, send("DELETE")),
  testAlertChannel: (ch: "telegram" | "webhook") => req<{ ok: boolean; error?: string }>(`/api/v1/alerts/channels/${ch}/test`, { ...send("POST"), timeoutMs: 40_000 }),
  alertHistory: (limit = 100) => req<AlertEvent[] | null>(`/api/v1/alerts/history?limit=${limit}`),

  aiStatus: () => req<AiStatus>("/api/v1/ai/status", { quiet: true }),
  aiSetCredential: (kind: "api_key" | "oauth_token", value: string) => req<AiStatus>("/api/v1/ai/credential", send("PUT", { kind, value })),
  aiRevoke: () => req<AiStatus>("/api/v1/ai/credential", send("DELETE")),
  aiSetModel: (model: string) => req<AiStatus>("/api/v1/ai/model", send("PUT", { model })),
  aiTest: () => req<AiTestResult>("/api/v1/ai/test", { ...send("POST", {}), timeoutMs: 120_000 }),

  interactConfig: () => req<InteractConfig>("/api/v1/interact/config", { quiet: true }),
  saveInteractConfig: (c: InteractConfig) => req<InteractConfig>("/api/v1/interact/config", send("PUT", c)),
  interactTest: () => req<{ ok: boolean; channels?: Record<string, string>; promptId?: string; error?: string }>("/api/v1/interact/test", { ...send("POST", {}), timeoutMs: 40_000 }),

  devConfig: () => req<DevConfig>("/api/v1/dev/config"),
  saveDevConfig: (c: DevConfig) => req<DevConfig>("/api/v1/dev/config", send("PUT", c)),
  devKeys: () => req<ApiKey[] | null>("/api/v1/dev/keys", { quiet: true }),
  createKey: (x: { kind: KeyKind; name: string; ratePerMin: number; burst: number; ttlHours: number }) => req<ApiKey & { secret: string }>("/api/v1/dev/keys", send("POST", x)),
  updateKey: (id: number, x: { name?: string; ratePerMin?: number; burst?: number; revoke?: boolean }) => req<ApiKey>(`/api/v1/dev/keys/${id}`, send("PATCH", x)),
  deleteKey: (id: number) => req<{ ok: boolean }>(`/api/v1/dev/keys/${id}`, send("DELETE")),
  devEndpoints: () => req<DevEndpoints>("/api/v1/dev/endpoints"),
  devLog: () => req<DevLog[] | null>("/api/v1/dev/log", { quiet: true }),
};

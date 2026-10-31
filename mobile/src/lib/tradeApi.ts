// Paper trading, confirmations and AI review endpoints (web/src/tradeApi.ts; types mirror
// internal/trade/contract.go). Writes that run code or spend money (user strategies, check, trial,
// AI memory reflect) need the owner key: `req` asks for it through AdminKeySheet on a 401.
import { req } from "./api";

// ---------- enums ----------
export type Mode = "live" | "backtest" | "real";
export type ExecMode = "auto" | "ai" | "record";
export type ConfirmMode = "off" | "open" | "all";
export type Action = "open_long" | "open_short" | "close" | "reduce";
export type InstanceState = "created" | "running" | "paused" | "faulted" | "stopped" | "done";
export type SignalOutcome = "pending" | "ordered" | "rejected" | "skipped_ai" | "skipped_user" | "expired" | "recorded";
export type OrderType = "market" | "limit" | "stop" | "target";
export type OrderState = "waiting_trigger" | "live" | "partially_filled" | "filled" | "cancelled" | "rejected";

// ---------- domain ----------
export interface Limits {
  maxPositions: number; maxLeverage: number; riskPct: number; maxInstPct: number; dailyLossPct: number;
  slippageBp: number; cooldownMin: number; takerFeeBp: number; makerFeeBp: number; requireStop: boolean;
  confirm: ConfirmMode; confirmTimeoutS: number; confirmDefault: "skip" | "execute";
}
export const defaultLimits = (): Limits => ({
  maxPositions: 5, maxLeverage: 5, riskPct: 0.01, maxInstPct: 0.3, dailyLossPct: 0.05, slippageBp: 10, cooldownMin: 30,
  takerFeeBp: 5, makerFeeBp: 2, requireStop: true, confirm: "off", confirmTimeoutS: 120, confirmDefault: "skip",
});
export interface BacktestStatus { state: "idle" | "running" | "done" | "failed"; progress: number; clock?: number; error?: string; startedAt?: number; finishedAt?: number }
export interface Account {
  id: number; name: string; mode: Mode; deposit: number; limits: Limits; archived: boolean; createdAt: number;
  from?: number; to?: number; backtest?: BacktestStatus;
  equity: number; cash: number; dayPnl: number; halted: boolean; openCount: number; instances: number;
}
export interface ScreenerQuery {
  scope?: "pool" | "market"; rule?: string; side?: "long" | "short" | "both"; top: number; minScore?: number;
  filter?: Record<string, [number, number]>; refresh?: string;
}
export interface Universe { static?: string[]; screener?: ScreenerQuery }
export type ParamType = "" | "float" | "int" | "bool" | "choice" | "timeframe";
export interface ParamSpec {
  name: string; type?: ParamType; default: number; min?: number; max?: number; step?: number;
  options?: string[]; labels?: string[]; group?: string; label?: string; desc?: string; aiTunable?: boolean;
}
export interface StrategyMeta { canLong?: boolean; canShort?: boolean; warmup?: number; schedule?: Record<string, string>; batch?: boolean; builtin?: boolean; sourceId?: number; draft?: boolean }
export interface StrategyVersion {
  id: string; name: string; file: string; description: string; timeframe: string; params: ParamSpec[];
  defaultUniverse: Universe; meta?: StrategyMeta; code?: string; createdAt: number;
}
export interface Source { id: number; name: string; code?: string; published?: string; dirty: boolean; createdAt: number; updatedAt: number }
export interface Diagnostic { line: number; col: number; endLine?: number; endCol?: number; severity: "error" | "warning"; message: string }
export interface CheckResult { diagnostics: Diagnostic[]; strategies: StrategyVersion[] }
export interface LogLine { ts: number; clock: number; level: "info" | "warn" | "error"; kind: "log" | "notify"; msg: string; sent?: boolean }
export interface Instance {
  id: number; accountId: number; versionId: string; name: string; params: Record<string, number>; universe: Universe;
  exec: ExecMode; state: InstanceState; stateReason?: string; status?: string; configVersion: number; heartbeat: number; createdAt: number;
}
export interface Signal {
  id: number; instanceId: number; accountId: number; instId: string; action: Action; strength: number;
  stopLoss?: number; takeProfit?: number; maxSlippageBp?: number; leverage?: number; reduceFrac?: number; ttlMs?: number;
  reason: string; ts: number; recvTs: number; price: number; outcome: SignalOutcome; outcomeReason?: string;
  configVersion: number; aiDecisionId?: number;
}
export interface Order {
  id: number; accountId: number; instanceId: number; signalId?: number; instId: string; side: "buy" | "sell"; posSide: "long" | "short";
  type: OrderType; qty: number; price?: number; triggerPx?: number; reduceOnly: boolean; ocoGroup?: string; leverage: number;
  state: OrderState; filled: number; avgPx?: number; reason?: string; createdAt: number; updatedAt: number;
}
export interface Fill {
  id: number; orderId: number; accountId: number; instanceId: number; instId: string; side: "buy" | "sell"; posSide: "long" | "short";
  qty: number; px: number; fee: number; slippageBp: number; realizedPnl: number; liquidation?: boolean; ts: number;
}
export interface Position {
  accountId: number; instanceId: number; instId: string; posSide: "long" | "short"; qty: number; ctVal: number; entryPx: number;
  leverage: number; margin: number; liqPx: number; markPx: number; unrealizedPnl: number; stopLoss?: number; takeProfit?: number; openedAt: number;
}
export interface LedgerEntry { id: number; accountId: number; kind: string; amount: number; instId?: string; refId?: number; ts: number }
export interface AccountDetail { account: Account; positions: Position[]; orders: Order[] }
export interface ReportInst { instId: string; trades: number; wins: number; pnl: number; fees: number; funding: number; turnover: number }
export interface Report {
  accountId: number; deposit: number; equity: number; gross: number; net: number; netPct: number; realized: number; unrealized: number;
  maxDrawdown: number; sharpe: number | null; sharpeBasis: string; winRate: number | null; profitFactor: number | null;
  turnover: number; fees: number; funding: number; liquidations: number; trades: number; openTrades: number;
  byInst: ReportInst[] | null; signals: Record<string, number> | null; outcomes: Record<string, number> | null;
  from?: number; to?: number;
}
export interface TradeFill { id: number; ts: number; side: "buy" | "sell"; qty: number; px: number; fee: number; pnl: number; kind: "open" | "add" | "reduce" | "close"; orderType: string }
export type ExitKind = "signal" | "stop" | "target" | "time" | "liquidation" | "manual" | "open";
export interface Trade {
  id: number; instanceId: number; instId: string; posSide: "long" | "short"; openTs: number; closeTs?: number;
  entryPx: number; exitPx?: number; qty: number; ctVal: number; notional: number; leverage: number; margin: number;
  pnl: number; fees: number; funding: number; net: number; returnPct: number; exitKind: ExitKind;
  openReason?: string; closeReason?: string; stopLoss?: number; takeProfit?: number; aiDecisionId?: number;
  fills: TradeFill[];
}
export interface StrategyEntry { name: string; latest: StrategyVersion; versions: StrategyVersion[] }
/** internal/trade CandleResp: rows = [ts, o, h, l, c, vol (contracts), volQuote (USDT)]. */
export interface AccountCandles { instId: string; bar: string; from: number; to: number; rows: [number, number, number, number, number, number, number][] }
/** 试运行: a throwaway backtest over the last `days` days (code, a stored version or a draft). */
export interface TrialInput { code?: string; versionId?: string; sourceId?: number; params?: Record<string, number>; universe?: Universe; days?: number }
export interface TrialResult {
  check?: CheckResult; error?: string; from: number; to: number; strategy: string; report?: Report;
  signals: Signal[]; trades: Trade[]; logs: LogLine[]; candles?: AccountCandles; seconds: number;
}

// ---------- interaction ----------
export interface TradeEvent { kind: string; accountId: number; account: string; mode: Mode; title: string; lines: string[]; ts: number; ref?: string }
export interface PromptOption { key: string; label: string }
export interface Prompt {
  id: string; event: TradeEvent; options: PromptOption[]; deadline: string | number; default: string;
  state?: "open" | "answered" | "expired"; choice?: string; by?: string; answeredAt?: number; createdAt?: number;
}

// ---------- AI ----------
export interface AiStatus {
  configured: boolean; kind?: "api_key" | "oauth_token"; masked?: string; model: string;
  workerOnline: boolean; workerReady: boolean; proxyOk: boolean; proxyError?: string; available: boolean;
  credentialError?: string; lastCallAt?: number; lastError?: string; costToday: number; callsToday: number; pendingProposals: number;
}
export interface DecisionBody { action?: string; sizeScale?: number; stopLoss?: number; takeProfit?: number; confidence?: number; rationale?: string; paramChange?: Record<string, number> }
export interface DecisionRecord {
  id: number; ts: number; kind: string; accountId: number; instanceId: number; signalId: number; strategy: string; instId: string; signalAction: string;
  requestSha256: string; memoryVersion: number; promptVersion: string; model: string; action: string;
  decision: DecisionBody | null; meta?: unknown; costUsd: number; latencyMs: number; error?: string;
  outcomes?: { ts: number; result: string }[]; request?: unknown;
}
export interface Proposal {
  id: number; ts: number; decisionId: number; accountId: number; instanceId: number; strategy: string;
  params: Record<string, number>; current: Record<string, number>; rationale: string;
  state: "pending" | "approved" | "rejected"; decidedAt?: number; note?: string;
}
export interface MemoryDoc { strategy: string; version: number; ts: number; doc: string; source: string; throughDecision: number; decisions: number; model?: string; costUsd: number; auditId?: number }

// ---------- normalisers ----------
const arr = <T,>(x: unknown, key?: string): T[] =>
  Array.isArray(x) ? (x as T[]) : key && x && Array.isArray((x as Record<string, unknown>)[key]) ? ((x as Record<string, unknown>)[key] as T[]) : [];

function normDetail(x: unknown): AccountDetail {
  const o = (x ?? {}) as { account?: Account; positions?: unknown; orders?: unknown; openOrders?: unknown };
  return { account: o.account ?? (x as Account), positions: arr<Position>(o.positions), orders: arr<Order>(o.orders ?? o.openOrders) };
}
function normStrategies(x: unknown): StrategyEntry[] {
  return arr<StrategyEntry & StrategyVersion>(x, "strategies").map((e) => {
    const latest: StrategyVersion = e.latest ?? e;
    const versions = (Array.isArray(e.versions) && e.versions.length ? [...e.versions] : [latest]).sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0));
    return { name: e.name ?? latest.name, latest, versions };
  });
}
const qs = (o: Record<string, string | number | undefined>) => {
  const s = Object.entries(o).filter(([, v]) => v !== undefined && v !== "").map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`).join("&");
  return s ? "?" + s : "";
};
const w = <T,>(path: string, method: string, body?: unknown) => req<T>(path, { method, body: body ?? {} });
const T = "/api/v1/trade";

export interface NewAccount { name: string; mode: Mode; deposit: number; limits: Limits; from?: number; to?: number }
export interface NewInstance { versionId: string; params: Record<string, number>; universe: Universe; exec: ExecMode }
export type InstanceAction = "start" | "pause" | "resume" | "stop";

export const tradeApi = {
  accounts: async () => arr<Account>(await req<unknown>(`${T}/accounts`), "accounts"),
  createAccount: (a: NewAccount) => w<Account>(`${T}/accounts`, "POST", a),
  account: async (id: number) => normDetail(await req<unknown>(`${T}/accounts/${id}`)),
  patchAccount: (id: number, p: { name?: string; limits?: Limits; archived?: boolean }) => w<Account>(`${T}/accounts/${id}`, "PATCH", p),
  deposit: (id: number, amount: number) => w<Account>(`${T}/accounts/${id}/deposit`, "POST", { amount }),
  equity: async (id: number, from?: number, to?: number) => arr<[number, number]>(await req<unknown>(`${T}/accounts/${id}/equity${qs({ from, to })}`)),
  fills: async (id: number, limit = 100) => arr<Fill>(await req<unknown>(`${T}/accounts/${id}/fills${qs({ limit })}`)),
  orders: async (id: number, limit = 100) => arr<Order>(await req<unknown>(`${T}/accounts/${id}/orders${qs({ limit })}`)),
  signals: async (id: number, limit = 100) => arr<Signal>(await req<unknown>(`${T}/accounts/${id}/signals${qs({ limit })}`)),
  ledger: async (id: number, limit = 100) => arr<LedgerEntry>(await req<unknown>(`${T}/accounts/${id}/ledger${qs({ limit })}`)),
  close: (id: number, instId?: string) => w<unknown>(`${T}/accounts/${id}/close`, "POST", instId ? { instId } : {}),
  runBacktest: (id: number) => w<Account>(`${T}/backtests/${id}/run`, "POST"),
  cancelBacktest: (id: number) => w<Account>(`${T}/backtests/${id}/cancel`, "POST"),
  report: (id: number) => req<Report>(`${T}/accounts/${id}/report`),
  trades: async (id: number) => arr<Trade>(await req<unknown>(`${T}/accounts/${id}/trades`)),
  strategies: async () => normStrategies(await req<unknown>(`${T}/strategies`)),
  strategy: (versionId: string) => req<StrategyVersion>(`${T}/strategies/${encodeURIComponent(versionId)}`),
  reloadStrategies: () => w<unknown>(`${T}/strategies/reload`, "POST"),
  sources: async () => arr<Source>(await req<unknown>(`${T}/sources`)),
  source: (id: number) => req<Source>(`${T}/sources/${id}`),
  createSource: (x: { code?: string; fromVersion?: string }) => w<Source>(`${T}/sources`, "POST", x),
  saveSource: (id: number, code: string) => w<Source>(`${T}/sources/${id}`, "PUT", { code }),
  deleteSource: (id: number) => req<unknown>(`${T}/sources/${id}`, { method: "DELETE" }),
  publishSource: (id: number) => w<{ version?: StrategyVersion; check: CheckResult }>(`${T}/sources/${id}/publish`, "POST"),
  check: (code: string) => w<CheckResult>(`${T}/check`, "POST", { code }),
  /** Runs up to 90 s on the server. */
  trial: async (x: TrialInput) => {
    const r = await req<TrialResult>(`${T}/trial`, { method: "POST", body: x, timeoutMs: 120_000 });
    return { ...r, signals: arr<Signal>(r.signals), trades: arr<Trade>(r.trades), logs: arr<LogLine>(r.logs) };
  },
  runLog: async (instanceId: number) => arr<LogLine>(await req<unknown>(`${T}/instances/${instanceId}/logs`)),
  instances: async (accountId: number) => arr<Instance>(await req<unknown>(`${T}/accounts/${accountId}/instances`), "instances"),
  createInstance: (accountId: number, x: NewInstance) => w<Instance>(`${T}/accounts/${accountId}/instances`, "POST", x),
  patchInstance: (id: number, p: Partial<Pick<Instance, "params" | "universe" | "exec" | "versionId">>) => w<Instance>(`${T}/instances/${id}`, "PATCH", p),
  instanceAction: (id: number, a: InstanceAction) => w<Instance>(`${T}/instances/${id}/${a}`, "POST"),
};

export const interactApi = {
  /** state: open | all; newest first for `all`. */
  prompts: async (state: "open" | "all" = "open", limit?: number) => arr<Prompt>(await req<unknown>(`/api/v1/interact/prompts${qs({ state, limit })}`, { quiet: true }), "prompts"),
  answer: (id: string, choice: string) => w<unknown>(`/api/v1/interact/prompts/${encodeURIComponent(id)}`, "POST", { choice }),
};

export const aiApi = {
  status: () => req<AiStatus>("/api/v1/ai/status", { quiet: true }),
  decisions: async (q: { accountId?: number; kind?: string; limit?: number } = {}) =>
    arr<DecisionRecord>(await req<unknown>(`/api/v1/ai/decisions${qs({ ...q, accountId: q.accountId || undefined })}`)),
  decision: (id: number) => req<DecisionRecord>(`/api/v1/ai/decisions/${id}`),
  proposals: async (state?: "pending" | "approved" | "rejected", limit?: number) => arr<Proposal>(await req<unknown>(`/api/v1/ai/proposals${qs({ state, limit })}`)),
  decide: (id: number, approve: boolean) => w<Proposal>(`/api/v1/ai/proposals/${id}`, "POST", { approve }),
  memory: (strategy: string) => req<{ strategy: string; current: MemoryDoc | null; versions: MemoryDoc[] | null }>(`/api/v1/ai/memory/${encodeURIComponent(strategy)}`),
  reflect: (strategy: string) => w<MemoryDoc>(`/api/v1/ai/memory/${encodeURIComponent(strategy)}/reflect`, "POST"),
};

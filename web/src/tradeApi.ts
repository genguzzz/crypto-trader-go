/**
 * Trading, interaction and AI endpoints (docs/trading-design.md §4). Types mirror the JSON field
 * names of internal/trade/contract.go; shapes the contract leaves open are marked "ASSUMPTION" and
 * normalised defensively so small backend differences don't break the pages.
 */
import { req } from './api'
import { askAdminKey, clearAdminKey, getAdminKey } from './composables/adminKey'

// ---------- enums (contract.go) ----------
export type Mode = 'live' | 'backtest' | 'real'
export type ExecMode = 'auto' | 'ai' | 'record'
export type ConfirmMode = 'off' | 'open' | 'all'
export type Action = 'open_long' | 'open_short' | 'close' | 'reduce'
export type InstanceState = 'created' | 'running' | 'paused' | 'faulted' | 'stopped' | 'done'
export type SignalOutcome = 'pending' | 'ordered' | 'rejected' | 'skipped_ai' | 'skipped_user' | 'expired' | 'recorded'
export type OrderType = 'market' | 'limit' | 'stop' | 'target'
export type OrderState = 'waiting_trigger' | 'live' | 'partially_filled' | 'filled' | 'cancelled' | 'rejected'

// ---------- domain (contract.go) ----------
export interface Limits {
  maxPositions: number; maxLeverage: number; riskPct: number; maxInstPct: number; dailyLossPct: number
  slippageBp: number; cooldownMin: number; takerFeeBp: number; makerFeeBp: number; requireStop: boolean
  confirm: ConfirmMode; confirmTimeoutS: number; confirmDefault: 'skip' | 'execute'
}
export const defaultLimits = (): Limits => ({
  maxPositions: 5, maxLeverage: 5, riskPct: 0.01, maxInstPct: 0.3, dailyLossPct: 0.05, slippageBp: 10, cooldownMin: 30,
  takerFeeBp: 5, makerFeeBp: 2, requireStop: true, confirm: 'off', confirmTimeoutS: 120, confirmDefault: 'skip',
})

export interface Account {
  id: number; name: string; mode: Mode; deposit: number; limits: Limits; archived: boolean; createdAt: number
  from?: number; to?: number
  backtest?: BacktestStatus // backtest accounts: state of the last run
  equity: number; cash: number; dayPnl: number; halted: boolean; openCount: number; instances: number
}
/** internal/trade BacktestStatus */
export interface BacktestStatus {
  state: 'idle' | 'running' | 'done' | 'failed'; progress: number; clock?: number; error?: string; startedAt?: number; finishedAt?: number
}
/** scope: pool (default, omitted) = active pool by a rule's score; market = every recorded crypto swap by 12h USDT turnover (no rule). */
export interface ScreenerQuery {
  scope?: 'pool' | 'market'; rule?: string; side?: 'long' | 'short' | 'both'; top: number; minScore?: number
  filter?: Record<string, [number, number]>; refresh?: string
}
export interface Universe { static?: string[]; screener?: ScreenerQuery }
/** Param types (SDK 2). Values are numbers: bool 0/1, choice / timeframe the option index. "" = float (SDK 1). */
export type ParamType = '' | 'float' | 'int' | 'bool' | 'choice' | 'timeframe'
export interface ParamSpec {
  name: string; type?: ParamType; default: number; min?: number; max?: number; step?: number
  options?: string[]; labels?: string[]; group?: string; label?: string; desc?: string; aiTunable?: boolean
}
/** internal/trade StrategyMeta. */
export interface StrategyMeta {
  canLong?: boolean; canShort?: boolean; warmup?: number; schedule?: Record<string, string>; batch?: boolean
  builtin?: boolean; sourceId?: number; draft?: boolean
}
export interface StrategyVersion {
  id: string; name: string; file: string; description: string; timeframe: string; params: ParamSpec[]
  defaultUniverse: Universe; meta?: StrategyMeta; code?: string; createdAt: number
}
/** internal/trade Source: a user strategy (draft code + last published version). */
export interface Source { id: number; name: string; code?: string; published?: string; dirty: boolean; createdAt: number; updatedAt: number }
export interface Diagnostic { line: number; col: number; endLine?: number; endCol?: number; severity: 'error' | 'warning'; message: string }
export interface CheckResult { diagnostics: Diagnostic[]; strategies: StrategyVersion[] }
/** internal/trade LogLine: one entry of an instance's run log. */
export interface LogLine { ts: number; clock: number; level: 'info' | 'warn' | 'error'; kind: 'log' | 'notify'; msg: string; sent?: boolean }
export interface TrialInput { code?: string; versionId?: string; sourceId?: number; params?: Record<string, number>; universe?: Universe; days?: number }
export interface TrialResult {
  check?: CheckResult; error?: string; from: number; to: number; strategy: string; report?: Report
  signals: Signal[]; trades: Trade[]; logs: LogLine[]; candles?: AccountCandles; seconds: number
}
export interface Instance {
  id: number; accountId: number; versionId: string; name: string; params: Record<string, number>; universe: Universe
  exec: ExecMode; state: InstanceState; stateReason?: string; status?: string; configVersion: number; heartbeat: number; createdAt: number
}
export interface Signal {
  id: number; instanceId: number; accountId: number; instId: string; action: Action; strength: number
  stopLoss?: number; takeProfit?: number; maxSlippageBp?: number; leverage?: number; reduceFrac?: number; ttlMs?: number
  reason: string; ts: number; recvTs: number; price: number; outcome: SignalOutcome; outcomeReason?: string
  configVersion: number; aiDecisionId?: number
}
export interface Order {
  id: number; accountId: number; instanceId: number; signalId?: number; instId: string; side: 'buy' | 'sell'; posSide: 'long' | 'short'
  type: OrderType; qty: number; price?: number; triggerPx?: number; reduceOnly: boolean; ocoGroup?: string; leverage: number
  state: OrderState; filled: number; avgPx?: number; reason?: string; createdAt: number; updatedAt: number
}
export interface Fill {
  id: number; orderId: number; accountId: number; instanceId: number; instId: string; side: 'buy' | 'sell'; posSide: 'long' | 'short'
  qty: number; px: number; fee: number; slippageBp: number; realizedPnl: number; liquidation?: boolean; ts: number
}
export interface Position {
  accountId: number; instanceId: number; instId: string; posSide: 'long' | 'short'; qty: number; ctVal: number; entryPx: number
  leverage: number; margin: number; liqPx: number; markPx: number; unrealizedPnl: number; stopLoss?: number; takeProfit?: number; openedAt: number
}
export interface LedgerEntry { id: number; accountId: number; kind: 'deposit' | 'fee' | 'funding' | 'pnl' | 'liquidation' | string; amount: number; instId?: string; refId?: number; ts: number }

/** GET trade/accounts/{id} (internal/trade AccountDetail). */
export interface AccountDetail { account: Account; positions: Position[]; orders: Order[] }

/** internal/trade InstReport: pnl = realized − fees + funding. */
export interface ReportInst { instId: string; trades: number; wins: number; pnl: number; fees: number; funding: number; turnover: number }
/**
 * internal/trade Report. USDT amounts; ratios as fractions. net = equity − deposits (incl. unrealized);
 * fees paid (positive); funding signed (positive = received). sharpe / winRate / profitFactor are
 * null when not measurable (too few days / no trades / no losers).
 */
export interface Report {
  accountId: number; deposit: number; equity: number; gross: number; net: number; netPct: number; realized: number; unrealized: number
  maxDrawdown: number; sharpe: number | null; sharpeBasis: string; winRate: number | null; profitFactor: number | null
  turnover: number; fees: number; funding: number; liquidations: number; trades: number; openTrades: number
  byInst: ReportInst[] | null
  signals: Record<string, number> | null // ordered, rejected, skipped, recorded, pending
  outcomes: Record<string, number> | null // per SignalOutcome
  from?: number; to?: number
}

/** internal/trade TradeFill: one fill of a round trip. kind: open | add | reduce | close. */
export interface TradeFill { id: number; ts: number; side: 'buy' | 'sell'; qty: number; px: number; fee: number; pnl: number; kind: 'open' | 'add' | 'reduce' | 'close'; orderType: string }
export type ExitKind = 'signal' | 'stop' | 'target' | 'time' | 'liquidation' | 'manual' | 'open'
/**
 * internal/trade Trade: one round trip of an instance in an instrument, flat to flat. closeTs 0 = still
 * open. notional / margin / pnl / fees / funding / net in USDT; returnPct = net / margin.
 */
export interface Trade {
  id: number; instanceId: number; instId: string; posSide: 'long' | 'short'; openTs: number; closeTs?: number
  entryPx: number; exitPx?: number; qty: number; ctVal: number; notional: number; leverage: number; margin: number
  pnl: number; fees: number; funding: number; net: number; returnPct: number; exitKind: ExitKind
  openReason?: string; closeReason?: string; stopLoss?: number; takeProfit?: number; aiDecisionId?: number
  fills: TradeFill[]
}
/** internal/trade CandleResp: rows = [ts, o, h, l, c, vol (contracts), volQuote (USDT)]. */
export interface AccountCandles { instId: string; bar: string; from: number; to: number; rows: [number, number, number, number, number, number, number][] }

/** GET trade/strategies: latest version per name + version history (newest first, without code). */
export interface StrategyEntry { name: string; latest: StrategyVersion; versions: StrategyVersion[] }

// ---------- interaction (contract.go Event / Prompt) ----------
export interface TradeEvent { kind: string; accountId: number; account: string; mode: Mode; title: string; lines: string[]; ts: number; ref?: string }
export interface PromptOption { key: string; label: string }
export interface Prompt {
  id: string; event: TradeEvent; options: PromptOption[]; deadline: string | number; default: string
  // internal/interact store record: deadline is ms there; contract.go's time.Time would be RFC 3339
  state?: 'open' | 'answered' | 'expired'; choice?: string; by?: string; answeredAt?: number; createdAt?: number
}
/** internal/interact ConfigView. botToken is masked; sending it back masked or empty keeps it. */
export interface InteractConfig {
  telegram: { enabled: boolean; botToken: string; configured: boolean; allowUserIds: number[]; clearToken?: boolean }
  /** secret is masked; sending it back masked or empty keeps it. kinds empty = the global list. */
  webhook: { enabled: boolean; url: string; secret: string; configured: boolean; clearSecret?: boolean; kinds: string[]; backlog?: number }
  kinds: string[] // empty = all kinds
  quiet: boolean
  status?: {
    state: 'disabled' | 'stopped' | 'starting' | 'ok' | 'error' | string; bot?: string; lastError?: string; lastPollAt?: number
    recipients: number; backlog: number; strangers?: { id: number; name: string; at: number }[]
  }
  allKinds?: { kind: string; label: string }[]
}

// ---------- AI (internal/aigate) ----------
export interface AiStatus {
  configured: boolean; kind?: 'api_key' | 'oauth_token'; masked?: string
  storage?: 'encrypted' | 'plain'; storageNote?: string; updatedAt?: number; credentialError?: string
  model: string
  workerOnline: boolean; workerReady: boolean
  proxyOk: boolean; proxyError?: string // the worker reaches Anthropic only through the proxy; never direct
  worker?: { version: string; sdk?: string; cli?: string; ready: boolean; proxyOk: boolean; proxyError?: string; connectedAt: number }
  available: boolean // configured && worker online && ready
  lastCallAt?: number; lastError?: string
  costToday: number; callsToday: number; pendingProposals: number
}
/** The model's answer as stored (worker payload: trade.Decision without id). */
export interface DecisionBody {
  action?: string; sizeScale?: number; stopLoss?: number; takeProfit?: number; confidence?: number
  rationale?: string; paramChange?: Record<string, number>
}
/** internal/aigate DecisionRecord (audit row). kind: review | test | reflect. */
export interface DecisionRecord {
  id: number; ts: number; kind: 'review' | 'test' | 'reflect' | string
  accountId: number; instanceId: number; signalId: number; strategy: string; instId: string; signalAction: string
  requestSha256: string; memoryVersion: number; promptVersion: string; model: string; action: string
  decision: DecisionBody | null; meta?: unknown; costUsd: number; latencyMs: number; error?: string
  outcomes?: { ts: number; result: string }[]
  request?: unknown // only from GET ai/decisions/{id}
}
/** internal/aigate Proposal: params = proposed values of the changed parameters, current = their values then. */
export interface Proposal {
  id: number; ts: number; decisionId: number; accountId: number; instanceId: number; strategy: string
  params: Record<string, number>; current: Record<string, number>; rationale: string
  state: 'pending' | 'approved' | 'rejected'; decidedAt?: number; note?: string
}
export interface AiTestResult { ok: boolean; model?: string; reply?: string; costUsd: number; latencyMs: number; error?: string }
/** internal/aigate MemoryDoc: the strategy experience document distilled from past decisions. */
export interface MemoryDoc {
  strategy: string; version: number; ts: number; doc: string; source: string; throughDecision: number; decisions: number
  model?: string; costUsd: number; auditId?: number
}

// ---------- normalisers (tolerate small shape differences) ----------
const arr = <T>(x: unknown, key?: string): T[] =>
  Array.isArray(x) ? (x as T[]) : key && x && Array.isArray((x as any)[key]) ? ((x as any)[key] as T[]) : []

function normDetail(x: any): AccountDetail {
  const account: Account = x?.account ?? x
  return { account, positions: arr<Position>(x?.positions), orders: arr<Order>(x?.orders ?? x?.openOrders) }
}
function normStrategies(x: unknown): StrategyEntry[] {
  return arr<any>(x, 'strategies').map(e => {
    const latest: StrategyVersion = e.latest ?? e
    const versions: StrategyVersion[] = (Array.isArray(e.versions) && e.versions.length ? [...e.versions] : [latest])
      .sort((a: StrategyVersion, b: StrategyVersion) => (b.createdAt || 0) - (a.createdAt || 0)) // newest first
    return { name: e.name ?? latest.name, latest, versions }
  })
}

/** Plain write: paper trading, confirmations and proposals need no owner key (internal/api/admin.go). */
const wreq = <T>(path: string, method: string, body?: unknown) =>
  req<T>(path, { method, body: body === undefined ? undefined : JSON.stringify(body), headers: body === undefined ? undefined : { 'Content-Type': 'application/json' } })

/**
 * Protected write (credentials, channel config, paid AI calls — internal/api/admin.go adminPaths):
 * sends X-CTG-Admin from this browser's owner key. A 401 with code admin_required opens the key
 * dialog and retries once with the entered key.
 */
export async function mreq<T>(path: string, method: string, body?: unknown): Promise<T> {
  const send = (key: string) => fetch(path, {
    method,
    body: body === undefined ? undefined : JSON.stringify(body),
    headers: { ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...(key ? { 'X-CTG-Admin': key } : {}) },
  })
  let r = await send(getAdminKey())
  let out: any = await r.json().catch(() => ({}))
  if (r.status === 401 && out?.code === 'admin_required') {
    const key = await askAdminKey(getAdminKey() ? '密钥不正确，请重新输入' : '')
    if (!key) throw new Error('需要管理员密钥')
    r = await send(key)
    out = await r.json().catch(() => ({}))
    if (r.status === 401 && out?.code === 'admin_required') clearAdminKey() // wrong key: don't keep it
  }
  if (!r.ok) throw Object.assign(new Error(out?.error || `HTTP ${r.status}`), { body: out })
  return out as T
}
const qs = (o: Record<string, string | number | undefined>) => {
  const s = Object.entries(o).filter(([, v]) => v !== undefined && v !== '').map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`).join('&')
  return s ? '?' + s : ''
}
const T = '/api/v1/trade'

export interface NewAccount { name: string; mode: Mode; deposit: number; limits: Limits; from?: number; to?: number }
export interface NewInstance { versionId: string; params: Record<string, number>; universe: Universe; exec: ExecMode }
export type InstanceAction = 'start' | 'pause' | 'resume' | 'stop'

export const tradeApi = {
  // accounts
  accounts: async () => arr<Account>(await req<unknown>(`${T}/accounts`), 'accounts'),
  createAccount: (a: NewAccount) => wreq<Account>(`${T}/accounts`, 'POST', a),
  account: async (id: number) => normDetail(await req<unknown>(`${T}/accounts/${id}`)),
  patchAccount: (id: number, p: { name?: string; limits?: Limits; archived?: boolean }) => wreq<Account>(`${T}/accounts/${id}`, 'PATCH', p),
  deposit: (id: number, amount: number) => wreq<Account>(`${T}/accounts/${id}/deposit`, 'POST', { amount }),
  equity: async (id: number, from?: number, to?: number) => arr<[number, number]>(await req<unknown>(`${T}/accounts/${id}/equity${qs({ from, to })}`)),
  fills: async (id: number, limit = 100, before?: number) => arr<Fill>(await req<unknown>(`${T}/accounts/${id}/fills${qs({ limit, before })}`)),
  orders: async (id: number, limit = 100, before?: number) => arr<Order>(await req<unknown>(`${T}/accounts/${id}/orders${qs({ limit, before })}`)),
  signals: async (id: number, limit = 100, before?: number) => arr<Signal>(await req<unknown>(`${T}/accounts/${id}/signals${qs({ limit, before })}`)),
  ledger: async (id: number, limit = 100, before?: number) => arr<LedgerEntry>(await req<unknown>(`${T}/accounts/${id}/ledger${qs({ limit, before })}`)),
  close: (id: number, instId?: string) => wreq<unknown>(`${T}/accounts/${id}/close`, 'POST', instId ? { instId } : {}),
  runBacktest: (id: number) => wreq<Account>(`${T}/backtests/${id}/run`, 'POST', {}),
  cancelBacktest: (id: number) => wreq<Account>(`${T}/backtests/${id}/cancel`, 'POST', {}),
  report: (id: number) => req<Report>(`${T}/accounts/${id}/report`),
  trades: async (id: number) => arr<Trade>(await req<unknown>(`${T}/accounts/${id}/trades`)),
  candles: (id: number, instId: string, bar?: string) => req<AccountCandles>(`${T}/accounts/${id}/candles${qs({ instId, bar })}`),
  // strategies
  strategies: async () => normStrategies(await req<unknown>(`${T}/strategies`)),
  strategy: (versionId: string) => req<StrategyVersion>(`${T}/strategies/${encodeURIComponent(versionId)}`),
  reloadStrategies: () => wreq<unknown>(`${T}/strategies/reload`, 'POST', {}),
  // user strategies (writes run code: owner key)
  sources: async () => arr<Source>(await req<unknown>(`${T}/sources`)),
  source: (id: number) => req<Source>(`${T}/sources/${id}`),
  createSource: (x: { code?: string; fromVersion?: string }) => mreq<Source>(`${T}/sources`, 'POST', x),
  saveSource: (id: number, code: string) => mreq<Source>(`${T}/sources/${id}`, 'PUT', { code }),
  deleteSource: (id: number) => mreq<unknown>(`${T}/sources/${id}`, 'DELETE'),
  /** On a failed check the thrown error carries body.check. */
  publishSource: (id: number) => mreq<{ version?: StrategyVersion; check: CheckResult }>(`${T}/sources/${id}/publish`, 'POST', {}),
  check: (code: string) => mreq<CheckResult>(`${T}/check`, 'POST', { code }),
  trial: (x: TrialInput) => mreq<TrialResult>(`${T}/trial`, 'POST', x),
  runLog: async (instanceId: number) => arr<LogLine>(await req<unknown>(`${T}/instances/${instanceId}/logs`)),
  // instances
  instances: async (accountId: number) => arr<Instance>(await req<unknown>(`${T}/accounts/${accountId}/instances`), 'instances'),
  createInstance: (accountId: number, x: NewInstance) => wreq<Instance>(`${T}/accounts/${accountId}/instances`, 'POST', x),
  patchInstance: (id: number, p: Partial<Pick<Instance, 'params' | 'universe' | 'exec' | 'versionId'>>) => wreq<Instance>(`${T}/instances/${id}`, 'PATCH', p),
  instanceAction: (id: number, a: InstanceAction) => wreq<Instance>(`${T}/instances/${id}/${a}`, 'POST', {}),
}

export const interactApi = {
  /** state: open | all (internal/interact web.go); newest first for `all`. */
  prompts: async (state: 'open' | 'all' = 'open', limit?: number) => arr<Prompt>(await req<unknown>(`/api/v1/interact/prompts${qs({ state, limit })}`), 'prompts'),
  answer: (id: string, choice: string) => wreq<unknown>(`/api/v1/interact/prompts/${encodeURIComponent(id)}`, 'POST', { choice }),
  config: () => req<InteractConfig>('/api/v1/interact/config'),
  saveConfig: (c: InteractConfig) => mreq<InteractConfig>('/api/v1/interact/config', 'PUT', c),
  test: () => mreq<{ ok: boolean; channels?: Record<string, string>; promptId?: string; error?: string }>('/api/v1/interact/test', 'POST', {}),
}

export const aiApi = {
  status: () => req<AiStatus>('/api/v1/ai/status'),
  setCredential: (kind: 'api_key' | 'oauth_token', value: string) => mreq<AiStatus>('/api/v1/ai/credential', 'PUT', { kind, value }),
  revoke: () => mreq<AiStatus>('/api/v1/ai/credential', 'DELETE'),
  setModel: (model: string) => mreq<AiStatus>('/api/v1/ai/model', 'PUT', { model }),
  test: () => mreq<AiTestResult>('/api/v1/ai/test', 'POST', {}),
  decisions: async (q: { accountId?: number; instanceId?: number; kind?: string; strategy?: string; limit?: number; before?: number } = {}) =>
    arr<DecisionRecord>(await req<unknown>(`/api/v1/ai/decisions${qs({ ...q, accountId: q.accountId || undefined })}`)),
  decision: (id: number) => req<DecisionRecord>(`/api/v1/ai/decisions/${id}`),
  proposals: async (state?: 'pending' | 'approved' | 'rejected', limit?: number) => arr<Proposal>(await req<unknown>(`/api/v1/ai/proposals${qs({ state, limit })}`)),
  decide: (id: number, approve: boolean) => wreq<Proposal>(`/api/v1/ai/proposals/${id}`, 'POST', { approve }),
  memory: (strategy: string, version?: number) =>
    req<{ strategy: string; current: MemoryDoc | null; versions: MemoryDoc[] | null }>(`/api/v1/ai/memory/${encodeURIComponent(strategy)}${qs({ version })}`),
  reflect: (strategy: string) => mreq<MemoryDoc>(`/api/v1/ai/memory/${encodeURIComponent(strategy)}/reflect`, 'POST', {}),
}

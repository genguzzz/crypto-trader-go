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
  equity: number; cash: number; dayPnl: number; halted: boolean; openCount: number; instances: number
}
export interface ScreenerQuery {
  rule?: string; side?: 'long' | 'short'; top: number; minScore?: number
  filter?: Record<string, [number, number]>; refresh?: string
}
export interface Universe { static?: string[]; screener?: ScreenerQuery }
export interface ParamSpec { name: string; default: number; min?: number; max?: number; step?: number; desc?: string; aiTunable?: boolean }
export interface StrategyVersion {
  id: string; name: string; file: string; description: string; timeframe: string; params: ParamSpec[]
  defaultUniverse: Universe; code?: string; createdAt: number
}
export interface Instance {
  id: number; accountId: number; versionId: string; name: string; params: Record<string, number>; universe: Universe
  exec: ExecMode; state: InstanceState; stateReason?: string; configVersion: number; heartbeat: number; createdAt: number
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

/** GET trade/accounts/{id}: "account + positions + open orders". ASSUMPTION: {account, positions, orders}. */
export interface AccountDetail { account: Account; positions: Position[]; orders: Order[] }

/** ASSUMPTION: per-instrument row of the report (contract names only `byInst[]`). */
export interface ReportInst { instId: string; trades: number; net: number; gross?: number; winRate?: number; fees?: number; funding?: number }
export interface Report {
  gross: number; net: number; maxDrawdown: number; sharpe: number; winRate: number; profitFactor: number; turnover: number
  fees: number; funding: number; trades: number; byInst: ReportInst[]; signals: { ordered: number; rejected: number; skipped: number }
}

/** GET trade/strategies: "latest version per name + versions list". ASSUMPTION: {name, latest, versions}. */
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
  kinds: string[] // empty = all kinds
  quiet: boolean
  status?: {
    state: 'disabled' | 'stopped' | 'starting' | 'ok' | 'error' | string; bot?: string; lastError?: string; lastPollAt?: number
    recipients: number; backlog: number; strangers?: { id: number; name: string; at: number }[]
  }
  allKinds?: { kind: string; label: string }[]
}

// ---------- AI ----------
export interface AiStatus {
  configured: boolean; kind?: 'api_key' | 'oauth_token'; masked?: string; workerOnline: boolean
  lastCallAt?: number; lastError?: string; costToday: number
}
export interface Decision {
  id: number; action: 'execute' | 'skip'; sizeScale: number; stopLoss?: number; takeProfit?: number; confidence: number
  rationale: string; paramChange?: Record<string, number>; model: string; costUsd: number; latencyMs: number
}
/** ASSUMPTION: a decision as listed by GET ai/decisions, with the signal context and the engine's verdict. */
export interface DecisionRecord extends Decision {
  ts: number; signalId: number; accountId: number; instanceId: number; instId: string; signalAction: Action
  outcome?: SignalOutcome; outcomeReason?: string
}
/** ASSUMPTION: parameter change proposal (from Decision.paramChange). */
export interface Proposal {
  id: number; ts: number; accountId: number; instanceId: number; instanceName?: string; decisionId?: number
  current: Record<string, number>; proposed: Record<string, number>; rationale: string
  state: 'open' | 'approved' | 'rejected'
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

/**
 * State-changing request: sends X-CTG-Admin from this browser's owner key. A 401 with code
 * admin_required opens the key dialog and retries once with the entered key.
 */
async function mreq<T>(path: string, method: string, body?: unknown): Promise<T> {
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
  if (!r.ok) throw new Error(out?.error || `HTTP ${r.status}`)
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
  createAccount: (a: NewAccount) => mreq<Account>(`${T}/accounts`, 'POST', a),
  account: async (id: number) => normDetail(await req<unknown>(`${T}/accounts/${id}`)),
  patchAccount: (id: number, p: { name?: string; limits?: Limits; archived?: boolean }) => mreq<Account>(`${T}/accounts/${id}`, 'PATCH', p),
  deposit: (id: number, amount: number) => mreq<Account>(`${T}/accounts/${id}/deposit`, 'POST', { amount }),
  equity: async (id: number, from?: number, to?: number) => arr<[number, number]>(await req<unknown>(`${T}/accounts/${id}/equity${qs({ from, to })}`)),
  fills: async (id: number, limit = 100, before?: number) => arr<Fill>(await req<unknown>(`${T}/accounts/${id}/fills${qs({ limit, before })}`)),
  orders: async (id: number, limit = 100, before?: number) => arr<Order>(await req<unknown>(`${T}/accounts/${id}/orders${qs({ limit, before })}`)),
  signals: async (id: number, limit = 100, before?: number) => arr<Signal>(await req<unknown>(`${T}/accounts/${id}/signals${qs({ limit, before })}`)),
  ledger: async (id: number, limit = 100, before?: number) => arr<LedgerEntry>(await req<unknown>(`${T}/accounts/${id}/ledger${qs({ limit, before })}`)),
  close: (id: number, instId?: string) => mreq<unknown>(`${T}/accounts/${id}/close`, 'POST', instId ? { instId } : {}),
  runBacktest: (id: number) => mreq<unknown>(`${T}/backtests/${id}/run`, 'POST', {}),
  report: (id: number) => req<Report>(`${T}/accounts/${id}/report`),
  // strategies
  strategies: async () => normStrategies(await req<unknown>(`${T}/strategies`)),
  strategy: (versionId: string) => req<StrategyVersion>(`${T}/strategies/${encodeURIComponent(versionId)}`),
  reloadStrategies: () => mreq<unknown>(`${T}/strategies/reload`, 'POST', {}),
  // instances
  instances: async (accountId: number) => arr<Instance>(await req<unknown>(`${T}/accounts/${accountId}/instances`), 'instances'),
  createInstance: (accountId: number, x: NewInstance) => mreq<Instance>(`${T}/accounts/${accountId}/instances`, 'POST', x),
  patchInstance: (id: number, p: Partial<Pick<Instance, 'params' | 'universe' | 'exec'>>) => mreq<Instance>(`${T}/instances/${id}`, 'PATCH', p),
  instanceAction: (id: number, a: InstanceAction) => mreq<Instance>(`${T}/instances/${id}/${a}`, 'POST', {}),
}

export const interactApi = {
  prompts: async (state = 'open') => arr<Prompt>(await req<unknown>(`/api/v1/interact/prompts${qs({ state })}`), 'prompts'),
  answer: (id: string, choice: string) => mreq<unknown>(`/api/v1/interact/prompts/${encodeURIComponent(id)}`, 'POST', { choice }),
  config: () => req<InteractConfig>('/api/v1/interact/config'),
  saveConfig: (c: InteractConfig) => mreq<InteractConfig>('/api/v1/interact/config', 'PUT', c),
  test: () => mreq<{ ok: boolean; channels?: Record<string, string>; promptId?: string; error?: string }>('/api/v1/interact/test', 'POST', {}),
}

export const aiApi = {
  status: () => req<AiStatus>('/api/v1/ai/status'),
  setCredential: (kind: 'api_key' | 'oauth_token', value: string) => mreq<AiStatus>('/api/v1/ai/credential', 'PUT', { kind, value }),
  revoke: () => mreq<unknown>('/api/v1/ai/credential', 'DELETE'),
  test: () => mreq<{ ok: boolean; error?: string; latencyMs?: number; model?: string }>('/api/v1/ai/test', 'POST', {}),
  decisions: async (accountId?: number, limit = 100) => arr<DecisionRecord>(await req<unknown>(`/api/v1/ai/decisions${qs({ accountId, limit })}`), 'decisions'),
  proposals: async () => arr<Proposal>(await req<unknown>('/api/v1/ai/proposals'), 'proposals'),
  decide: (id: number, approve: boolean) => mreq<Proposal>(`/api/v1/ai/proposals/${id}`, 'POST', { approve }),
}

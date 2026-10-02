export interface Field { key: string; label: string; unit: string; desc: string; replayable: boolean }
export interface Contribution { field: string; weight: number; pct: number; points: number }
export interface SideScore { score: number | null; contrib: Contribution[] | null; missing?: string[] }
export interface Score { long: SideScore; short: SideScore; gate: string }
export interface Row {
  instId: string; category: string; status: 'live' | 'stale' | 'warming' | 'new'
  active: boolean; rank?: number; turnover12h: number; provisional?: boolean; have: number
  last: number; values: Record<string, number> | null; pct: Record<string, number> | null
  scores?: Record<string, Score>; watch?: boolean; hasSpot: boolean; listTime: number; freshnessMs: number
  eligible?: boolean; mcap?: number; oiUsd?: number
}
export interface Removed { instId: string; reason: string; at: number; turnover12h: number }
export interface Coverage { eligible: number; ready: number; warming: number; stale: number; new: number; active: number }
export interface Snapshot {
  seq: number; asOfMinute: number; decisionTs: number; computedAt: number; provisional: boolean; phase: string
  preview?: boolean; previewAt?: number
  threshold: number; topN: number; coverage: Coverage; rows: Row[] | null; removed: Removed[] | null
}
export interface Rule {
  id: string; name: string; version: number; description: string; preset: boolean
  long: Record<string, number>; short: Record<string, number>; gates: { maxSpreadBps?: number }
}
/** Level = [price, usdt notional, contracts] */
export type BookLevel = [number, number, number]
export interface OrderBook { instId: string; ts: number; receivedAt: number; ctVal: number; mid: number; bestBid: number; bestAsk: number; spreadBps: number; bids: BookLevel[]; asks: BookLevel[]; levels: number; coverBid: number; coverAsk: number }
export interface Settings { threshold: number; topN: number; categories: string[]; defaultRule: string; storageCapMB: number }
/** One live OKX USDT-margined perpetual (search catalog; no history is fetched until opened). */
export interface CatalogItem {
  instId: string; category: string; listTime: number; preMarket?: boolean; recorded: boolean
  active?: boolean; rank?: number; watch?: boolean; last: number; open24h: number
}
/** Per-bar taker buy/sell USDT; src trades = recorded here (footprint levels available), rubik = OKX history. */
export interface FlowBar { t: number; buy: number; sell: number; n?: number; src: 'trades' | 'rubik'; levels?: [number, number, number][]; step?: number }
/** Series sample: v = value (OI in coins), u = USD value where known. */
export interface SeriesPoint { t: number; v: number; u?: number }
/** Liquidations per bar (USDT): long = long positions liquidated (forced sells). */
export interface LiqBar { t: number; long: number; short: number; nl: number; ns: number }
export interface LiqEvent { t: number; long: boolean; px: number; usd: number }
export interface Liquidations { bars: LiqBar[]; since: number; recent?: LiqEvent[]; err?: string; syncing?: boolean }
/** Estimated liquidation map: USDT per price bin (p = bin low); longBy / shortBy split it by tier in the order of `levers`. */
export interface LiqBin { p: number; long: number; short: number; longBy: number[]; shortBy: number[] }
export interface LiqMap { px: number; step: number; from: number; days: number; lever: number; levers: number[]; weights: number[]; fine: number; at: number; bins: LiqBin[] }
export interface OrderFlow { bars: FlowBar[] | null; recording: boolean; since: number }
export interface Coin { id: string; symbol: string; name: string; rank: number; marketCap: number; fdv: number; circulating: number; total: number; max: number; updatedAt: string; fetchedAt: number }
export interface TableUsage { name: string; table: string; bytes: number; budget: number; ages: string[]; tight: boolean }
export interface Storage {
  at: number; tookMs: number; capBytes: number; fileBytes: number; freeBytes: number; usedBytes: number
  deleted: Record<string, number>; tables: TableUsage[]; tiers: Record<string, number>; tierNames: string[]; error?: string
}
export interface Meta { fields: Field[]; rules: Rule[]; settings: Settings; categories: Record<string, string> }
export interface Candle { t: number; o: number; h: number; l: number; c: number; q: number; x: boolean }
export interface Stats {
  /** Metrics are null when not measured (no cross-sections for that side), never a fake 0. */
  horizonH: number; crossSections: number; medianNames: number; longN: number; shortN: number
  longIC: number | null; longT: number | null; longHit: number | null; shortIC: number | null; shortT: number | null; shortHit: number | null
  longTopExcessBps: number | null; longTopNetBps: number | null; shortTopExcessBps: number | null; shortTopNetBps: number | null
  firstTs: number; lastTs: number; cumLongIC: { ts: number; v: number }[] | null; verdict: string
}
export interface Track { ruleId: string; version: number; replayable: boolean; replayState: string; live: Stats[]; replay: Stats[]; liveSince: number }
export interface InstrumentInfo {
  instId: string; category: string; spotId?: string; listTime: number; last: number; bid: number; ask: number; tickTs: number
  spotLast?: number; fundingRate: number; fundingTime: number; fundingIntervalH: number
  open24h?: number; turnover24h?: number; markPx?: number; indexPx?: number
  oiCcy: number; oiUsd: number; oiTs: number; row?: Row
  watch: boolean; eligible: boolean; tier: string; syncing: boolean; syncMsg?: string; coin?: Coin
}

/** Monitoring (alerts). params depend on type: price_move {window,pct,dir}, score {ruleId,side,min}, funding {source,pctPerHour,dir}. */
export type AlertType = 'price_move' | 'score' | 'funding'
export interface AlertRule {
  id: number; name: string; enabled: boolean; type: AlertType; params: Record<string, any>
  scope: 'active' | 'watch' | 'inst'; instId?: string; cooldownMin: number; channels: { telegram: boolean; webhook: boolean }
  createdAt: number; updatedAt: number; summary: string; lastFired?: number; error?: string
}
/** Delivery channels as served by the API: secrets are masked (••••abcd); sending a masked/empty value back keeps it. */
export interface AlertChannels {
  telegram: { configured: boolean; botToken: string; chatId: string }
  webhook: { configured: boolean; url: string; secret: string }
}
/** delivery: channel → "ok" | "pending" | "失败：…" */
export interface AlertEvent { id: number; ts: number; ruleId: number; ruleName: string; instId: string; type: AlertType; value: number; message: string; delivery: Record<string, string> | null }

/** External access (Dev tab): keys are listed without secrets; the secret is returned once by createKey. */
export type KeyKind = 'key' | 'token'
export interface ApiKey {
  id: number; kind: KeyKind; name: string; prefix: string; ratePerMin: number; burst: number
  createdAt: number; expiresAt: number; revokedAt: number; lastUsedAt: number; requests: number; limited: number
  status: 'active' | 'expired' | 'revoked'; remaining: number
}
export interface DevConfig { enabled: boolean; globalPerMin: number }
export interface DevParam { name: string; in: 'query' | 'path'; type: 'string' | 'integer' | 'boolean'; desc: string; required?: boolean; enum?: string[]; default?: any; min?: number; max?: number }
export interface DevEndpoint { name: string; path: string; summary: string; desc: string; params: DevParam[] | null; cost: string }
export interface DevEndpoints { restBase: string; mcpPath: string; endpoints: DevEndpoint[]; limits: { defaultRatePerMin: number; maxRatePerMin: number; maxBurst: number } }
export interface DevLog { ts: number; keyId: number; keyName: string; prefix: string; via: 'rest' | 'mcp'; endpoint: string; status: number; ms: number; cost: number; error?: string }

export async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(path, init)
  const body = await r.json().catch(() => ({}))
  if (!r.ok) throw new Error((body as any).error || `HTTP ${r.status}`)
  return body as T
}

export const api = {
  meta: () => req<Meta>('/api/v1/meta'),
  universe: (scope = 'active') => req<Snapshot>(`/api/v1/universe?scope=${scope}`),
  instrument: (id: string) => req<InstrumentInfo>(`/api/v1/instruments/${encodeURIComponent(id)}`),
  catalog: () => req<{ items: CatalogItem[] }>('/api/v1/catalog'),
  storage: () => req<Storage | null>('/api/v1/storage'),
  orderflow: (id: string, bar: string, limit: number, levels: boolean) =>
    req<OrderFlow>(`/api/v1/orderflow?instId=${encodeURIComponent(id)}&bar=${bar}&limit=${limit}&levels=${levels ? 1 : 0}`),
  liquidations: (id: string, bar: string, limit: number, recent = 0) =>
    req<Liquidations>(`/api/v1/liquidations?instId=${encodeURIComponent(id)}&bar=${bar}&limit=${limit}&recent=${recent}`),
  liqmap: (id: string, days = 7) => req<LiqMap>(`/api/v1/liqmap?instId=${encodeURIComponent(id)}&days=${days}`),
  candles: (id: string, bar: string, limit = 300, market = 'swap') =>
    req<Candle[]>(`/api/v1/candles?instId=${encodeURIComponent(id)}&bar=${bar}&limit=${limit}&market=${market}`),
  compare: (id: string, bar: string, limit = 300) =>
    req<{ ids: string[]; points: { t: number; v: Record<string, number> }[] | null }>(`/api/v1/compare?instId=${encodeURIComponent(id)}&bar=${bar}&limit=${limit}`),
  series: (id: string, field: 'oi' | 'funding', hours = 24) =>
    req<SeriesPoint[] | null>(`/api/v1/series?instId=${encodeURIComponent(id)}&field=${field}&hours=${hours}`),
  rules: () => req<Rule[]>('/api/v1/rules'),
  saveRule: (r: Partial<Rule>) => req<Rule>('/api/v1/rules', { method: 'POST', body: JSON.stringify(r), headers: { 'Content-Type': 'application/json' } }),
  deleteRule: (id: string) => req<{ ok: boolean }>(`/api/v1/rules/${id}`, { method: 'DELETE' }),
  track: (id: string) => req<Track>(`/api/v1/rules/${id}/track`),
  watch: (id: string, on: boolean) => req<{ ok: boolean }>(`/api/v1/watchlist/${encodeURIComponent(id)}`, { method: on ? 'PUT' : 'DELETE' }),
  status: () => req<any>('/api/v1/status'),
  orderbook: (id: string) => req<OrderBook>(`/api/v1/orderbook?instId=${encodeURIComponent(id)}`),
  settings: () => req<Settings>('/api/v1/settings'),
  turnovers: () => req<{ asOfMinute: number; rows: [string, string, number, number][] }>('/api/v1/turnovers'),
  saveSettings: (x: Settings) => req<Settings>('/api/v1/settings', { method: 'PUT', body: JSON.stringify(x), headers: { 'Content-Type': 'application/json' } }),
  alertRules: () => req<{ rules: AlertRule[]; types: { key: AlertType; label: string }[] }>('/api/v1/alerts/rules'),
  saveAlertRule: (r: Partial<AlertRule>) =>
    req<AlertRule>(r.id ? `/api/v1/alerts/rules/${r.id}` : '/api/v1/alerts/rules', { method: r.id ? 'PUT' : 'POST', body: JSON.stringify(r), headers: { 'Content-Type': 'application/json' } }),
  deleteAlertRule: (id: number) => req<{ ok: boolean }>(`/api/v1/alerts/rules/${id}`, { method: 'DELETE' }),
  alertChannels: () => req<AlertChannels>('/api/v1/alerts/channels'),
  saveAlertChannels: (c: AlertChannels) => req<AlertChannels>('/api/v1/alerts/channels', { method: 'PUT', body: JSON.stringify(c), headers: { 'Content-Type': 'application/json' } }),
  clearAlertChannel: (ch: 'telegram' | 'webhook') => req<AlertChannels>(`/api/v1/alerts/channels/${ch}`, { method: 'DELETE' }),
  testAlertChannel: (ch: 'telegram' | 'webhook') => req<{ ok: boolean; error?: string }>(`/api/v1/alerts/channels/${ch}/test`, { method: 'POST' }),
  devConfig: () => req<DevConfig>('/api/v1/dev/config'),
  saveDevConfig: (c: DevConfig) => req<DevConfig>('/api/v1/dev/config', { method: 'PUT', body: JSON.stringify(c), headers: { 'Content-Type': 'application/json' } }),
  devKeys: () => req<ApiKey[]>('/api/v1/dev/keys'),
  createKey: (x: { kind: KeyKind; name: string; ratePerMin: number; burst: number; ttlHours: number }) =>
    req<ApiKey & { secret: string }>('/api/v1/dev/keys', { method: 'POST', body: JSON.stringify(x), headers: { 'Content-Type': 'application/json' } }),
  updateKey: (id: number, x: { name?: string; ratePerMin?: number; burst?: number; revoke?: boolean }) =>
    req<ApiKey>(`/api/v1/dev/keys/${id}`, { method: 'PATCH', body: JSON.stringify(x), headers: { 'Content-Type': 'application/json' } }),
  deleteKey: (id: number) => req<{ ok: boolean }>(`/api/v1/dev/keys/${id}`, { method: 'DELETE' }),
  devEndpoints: () => req<DevEndpoints>('/api/v1/dev/endpoints'),
  devLog: () => req<DevLog[]>('/api/v1/dev/log'),
  alertHistory: (limit = 100) => req<AlertEvent[]>(`/api/v1/alerts/history?limit=${limit}`),
}

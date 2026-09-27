export interface Field { key: string; label: string; unit: string; desc: string; replayable: boolean }
export interface Contribution { field: string; weight: number; pct: number; points: number }
export interface SideScore { score: number | null; contrib: Contribution[] | null; missing?: string[] }
export interface Score { long: SideScore; short: SideScore; gate: string }
export interface Row {
  instId: string; category: string; status: 'live' | 'stale' | 'warming' | 'new'
  active: boolean; rank?: number; turnover12h: number; provisional?: boolean; have: number
  last: number; values: Record<string, number> | null; pct: Record<string, number> | null
  scores?: Record<string, Score>; watch?: boolean; hasSpot: boolean; listTime: number; freshnessMs: number
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
export interface Settings { threshold: number; topN: number; categories: string[]; defaultRule: string }
export interface Meta { fields: Field[]; rules: Rule[]; settings: Settings; categories: Record<string, string> }
export interface Candle { t: number; o: number; h: number; l: number; c: number; q: number; x: boolean }
export interface Stats {
  horizonH: number; crossSections: number; medianNames: number
  longIC: number; longT: number; longHit: number; shortIC: number; shortT: number; shortHit: number
  longTopExcessBps: number; longTopNetBps: number; shortTopExcessBps: number; shortTopNetBps: number
  firstTs: number; lastTs: number; cumLongIC: { ts: number; v: number }[] | null; verdict: string
}
export interface Track { ruleId: string; version: number; replayable: boolean; replayState: string; live: Stats[]; replay: Stats[]; liveSince: number }
export interface InstrumentInfo {
  instId: string; category: string; spotId?: string; listTime: number; last: number; bid: number; ask: number; tickTs: number
  spotLast?: number; fundingRate: number; fundingTime: number; fundingIntervalH: number
  oiCcy: number; oiUsd: number; oiTs: number; row?: Row
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(path, init)
  const body = await r.json().catch(() => ({}))
  if (!r.ok) throw new Error((body as any).error || `HTTP ${r.status}`)
  return body as T
}

export const api = {
  meta: () => req<Meta>('/api/v1/meta'),
  universe: (scope = 'active') => req<Snapshot>(`/api/v1/universe?scope=${scope}`),
  instrument: (id: string) => req<InstrumentInfo>(`/api/v1/instruments/${encodeURIComponent(id)}`),
  candles: (id: string, bar: string, limit = 300, market = 'swap') =>
    req<Candle[]>(`/api/v1/candles?instId=${encodeURIComponent(id)}&bar=${bar}&limit=${limit}&market=${market}`),
  compare: (id: string, bar: string, limit = 300) =>
    req<{ ids: string[]; points: { t: number; v: Record<string, number> }[] | null }>(`/api/v1/compare?instId=${encodeURIComponent(id)}&bar=${bar}&limit=${limit}`),
  series: (id: string, field: 'oi' | 'funding', hours = 24) =>
    req<{ t: number; v: number; u?: number }[] | null>(`/api/v1/series?instId=${encodeURIComponent(id)}&field=${field}&hours=${hours}`),
  rules: () => req<Rule[]>('/api/v1/rules'),
  saveRule: (r: Partial<Rule>) => req<Rule>('/api/v1/rules', { method: 'POST', body: JSON.stringify(r), headers: { 'Content-Type': 'application/json' } }),
  deleteRule: (id: string) => req<{ ok: boolean }>(`/api/v1/rules/${id}`, { method: 'DELETE' }),
  track: (id: string) => req<Track>(`/api/v1/rules/${id}/track`),
  watch: (id: string, on: boolean) => req<{ ok: boolean }>(`/api/v1/watchlist/${encodeURIComponent(id)}`, { method: on ? 'PUT' : 'DELETE' }),
  status: () => req<any>('/api/v1/status'),
  settings: () => req<Settings>('/api/v1/settings'),
  saveSettings: (x: Settings) => req<Settings>('/api/v1/settings', { method: 'PUT', body: JSON.stringify(x), headers: { 'Content-Type': 'application/json' } }),
}

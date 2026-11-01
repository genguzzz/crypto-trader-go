/**
 * AI-proposed real OKX trades (server: internal/realtrade). The agent proposes a market order with
 * TP + SL on isolated margin; the owner approves or rejects (first answer from Telegram, web or app
 * wins). In `dryrun` mode an approved proposal builds and stores the OKX request but never sends it.
 * Every endpoint is owner-only (X-CTG-Admin): reads stay quiet without the key (null), writes ask
 * for it like the other protected actions (tradeApi mreq).
 */
import { getAdminKey } from './composables/adminKey'
import { mreq } from './tradeApi'

export type RtMode = 'dryrun' | 'live'
export type RtStatus = 'pending' | 'approved' | 'executing' | 'executed' | 'failed' | 'rejected' | 'expired' | 'invalidated'
export interface RtEvent { ts: number; kind: string; text: string; by?: string }
export interface TradeProposal {
  id: number; created: number; updated: number
  source: 'chat' | 'signal' | 'schedule'; trigger: string // trigger label, '' for chat
  chatId: number // 0 = none
  instId: string; side: 'long' | 'short'
  marginUsdt: number; leverage: number; contracts: number; notionalUsdt: number
  refPx: number; sl: number; tp: number; slPct: number; tpPct: number; rr: number // pct: distance from refPx in %, positive
  rationale: string; invalidation: string
  expiresAt: number
  status: RtStatus
  mode: RtMode; statusAt: number; error?: string
  by?: string; via?: string // who resolved it and through which channel (telegram | web | app | timeout)
  execPx?: number; ordId?: string
  request?: unknown; response?: unknown // the exact OKX request(s) [{method, path, body}] and response
  closedAt?: number; closeKind?: 'tp' | 'sl' | 'other'; closePx?: number; pnlUsdt?: number
  events?: RtEvent[]
}
export interface RtTrigger {
  id: string; kind: 'schedule' | 'alert'; label: string; enabled: boolean; everyMin?: number; ruleIds?: number[]
  cooldownMin: number; prompt: string; lastFiredAt?: number; lastChatId?: number; lastError?: string
}
export interface RtConfig {
  mode: RtMode
  limits: { maxLeverage: number; maxMarginUsdt: number; maxOpen: number; dailyLossUsdt: number; maxDriftPct: number; minRR: number; expiryMin: number }
  telegram: { enabled: boolean; state: string; bot?: string; lastError?: string }
  triggers: RtTrigger[]
}
export interface RtList { proposals: TradeProposal[]; now: number; mode: RtMode }

export const rtStatusText: Record<RtStatus, string> = {
  pending: '待审核', approved: '已同意', executing: '执行中', executed: '已执行', failed: '失败',
  rejected: '已拒绝', expired: '已过期', invalidated: '已失效',
}
export const statusLabel = (p: TradeProposal) => (p.status === 'executed' && p.mode === 'dryrun' ? '已模拟执行' : rtStatusText[p.status] || p.status)
/** .tag tone of a status: open = info, done well = good, failed = bad, the rest neutral. */
export const statusTone = (s: RtStatus) =>
  s === 'pending' ? 'warn' : s === 'approved' || s === 'executing' ? 'info' : s === 'executed' ? 'good' : s === 'failed' ? 'bad' : ''
export const viaText: Record<string, string> = { telegram: 'Telegram', web: '网页', app: 'App', timeout: '超时' }
export const isOpen = (p: TradeProposal) => p.status === 'pending' || p.status === 'approved' || p.status === 'executing'

const P = '/api/v1/realtrade'

/** Owner-only read: null without a key or when the key is refused (the caller shows nothing). */
async function oget<T>(path: string): Promise<T | null> {
  const key = getAdminKey()
  if (!key) return null
  const r = await fetch(path, { headers: { 'X-CTG-Admin': key } })
  if (r.status === 401 || r.status === 403) return null
  const body = await r.json().catch(() => ({}))
  if (!r.ok) throw new Error((body as any).error || `HTTP ${r.status}`)
  return body as T
}

/** An agent signal subscription as the chart needs it (server: internal/signals). */
export interface SignalSub {
  id: number; status: 'active' | 'fired' | 'expired' | 'cancelled'; desc: string; instIds: string[]
  chatId?: number; planId?: number
  levels?: { instId: string; px: number; label: string }[]
}
export const signalsApi = { get: (id: number) => oget<SignalSub>(`/api/v1/signals/${id}`) }

export const realtradeApi = {
  list: (q: { status?: 'pending' | 'open' | 'all'; chatId?: number; limit?: number } = {}) => {
    const s = Object.entries(q).filter(([, v]) => v !== undefined && v !== 0).map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`).join('&')
    return oget<RtList>(`${P}/proposals${s ? '?' + s : ''}`)
  },
  get: (id: number) => oget<TradeProposal>(`${P}/proposals/${id}`),
  /** 409 (no longer pending): the thrown error carries body.proposal with the final state. */
  approve: (id: number) => mreq<TradeProposal>(`${P}/proposals/${id}/approve`, 'POST', { confirm: true, via: 'web' }),
  reject: (id: number) => mreq<TradeProposal>(`${P}/proposals/${id}/reject`, 'POST', { via: 'web' }),
  config: () => oget<RtConfig>(`${P}/config`),
  saveTriggers: (ts: RtTrigger[]) =>
    mreq<RtConfig>(`${P}/triggers`, 'PUT', ts.map(t => ({ id: t.id, enabled: t.enabled, everyMin: t.everyMin, ruleIds: t.ruleIds, cooldownMin: t.cooldownMin, prompt: t.prompt }))),
}

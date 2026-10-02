import type { Account, Action, AiStatus, ConfirmMode, ExecMode, InstanceState, Limits, Mode, ParamSpec, OrderState, OrderType, SignalOutcome, Universe } from '../../tradeApi'

/** Tag class (style.css .tag variants) + Chinese label for every enum shown in the trading pages. */
export interface Lbl { t: string; c: '' | 'good' | 'warn' | 'bad' | 'info' }

export const modeLabel: Record<Mode, Lbl> = {
  live: { t: '实时模拟', c: 'info' },
  backtest: { t: '历史回测', c: '' },
  real: { t: '实盘', c: 'warn' },
}
export const execLabel: Record<ExecMode, { t: string; d: string }> = {
  auto: { t: '自动', d: '信号直接经风控与仓位计算下单' },
  ai: { t: 'AI 审核', d: '先由 AI 判断执行或跳过，再经风控' },
  record: { t: '只记录', d: '只记录信号，从不下单' },
}
export const confirmLabel: Record<ConfirmMode, string> = { off: '关闭', open: '仅开仓', all: '全部' }
export const stateLabel: Record<InstanceState, Lbl> = {
  created: { t: '未启动', c: '' },
  running: { t: '运行中', c: 'good' },
  paused: { t: '已暂停', c: 'warn' },
  faulted: { t: '故障', c: 'bad' },
  stopped: { t: '已停止', c: '' },
  done: { t: '回测完成', c: 'info' },
}
export const outcomeLabel: Record<SignalOutcome, Lbl> = {
  pending: { t: '等待中', c: 'warn' },
  ordered: { t: '已下单', c: 'good' },
  rejected: { t: '风控拒绝', c: 'bad' },
  skipped_ai: { t: 'AI 跳过', c: 'info' },
  skipped_user: { t: '用户跳过', c: '' },
  expired: { t: '过期', c: '' },
  recorded: { t: '只记录', c: '' },
}
export const actionLabel: Record<Action, { t: string; tone: '' | 'up' | 'down' }> = {
  open_long: { t: '开多', tone: 'up' },
  open_short: { t: '开空', tone: 'down' },
  close: { t: '平仓', tone: '' },
  reduce: { t: '减仓', tone: '' },
}
export const orderTypeLabel: Record<OrderType, string> = { market: '市价', limit: '限价', stop: '止损', target: '止盈' }
export const orderStateLabel: Record<OrderState, Lbl> = {
  waiting_trigger: { t: '待触发', c: '' },
  live: { t: '挂单中', c: 'info' },
  partially_filled: { t: '部分成交', c: 'warn' },
  filled: { t: '已成交', c: 'good' },
  cancelled: { t: '已撤销', c: '' },
  rejected: { t: '已拒绝', c: 'bad' },
}
export const ledgerLabel: Record<string, string> = { deposit: '入金', fee: '手续费', funding: '资金费', pnl: '已实现盈亏', liquidation: '强平' }
/** Event kinds the interaction layer can push (contract.go Event.Kind). */
export const eventKinds: { value: string; label: string }[] = [
  { value: 'signal', label: '信号' }, { value: 'order', label: '下单' }, { value: 'fill', label: '成交' }, { value: 'cancel', label: '撤单' },
  { value: 'stop', label: '止损' }, { value: 'target', label: '止盈' }, { value: 'time', label: '持仓到期' }, { value: 'liquidation', label: '强平' },
  { value: 'reject', label: '风控拒绝' }, { value: 'breaker', label: '熔断' }, { value: 'instance', label: '实例状态' }, { value: 'strategy', label: '策略消息' },
  { value: 'ai', label: 'AI 决策' }, { value: 'daily', label: '每日汇总' },
]

/** One-line AI availability (internal/aigate Status): label, tag tone, explanation. */
export function aiState(s: AiStatus): { t: string; c: Lbl['c']; d: string } {
  if (s.workerOnline && !s.proxyOk) return { t: '代理不可用', c: 'bad', d: `AI 只经代理访问 Anthropic，代理恢复前 AI 审核暂停（信号按跳过处理），不会直连${s.proxyError ? '。' + s.proxyError : ''}` }
  if (!s.configured) return { t: '未授权', c: 'warn', d: '还没有 AI 凭证，AI 审核的信号会被跳过' }
  if (!s.workerOnline) return { t: 'AI 进程离线', c: 'bad', d: 'AI 审核进程未连接，AI 审核的信号按跳过处理' }
  if (s.credentialError) return { t: '凭证失效', c: 'bad', d: s.credentialError }
  if (!s.workerReady) return { t: '未就绪', c: 'warn', d: 'AI 进程已连接但尚未就绪，信号暂按跳过处理' }
  if (s.lastError) return { t: '最近调用失败', c: 'warn', d: s.lastError }
  return { t: '正常', c: 'good', d: '' }
}

/** Overall account status for cards and headers. */
export function accountStatus(a: Account, running?: number, paused?: number): Lbl {
  if (a.archived) return { t: '已归档', c: '' }
  if (a.halted) return { t: '熔断', c: 'bad' }
  if (a.mode === 'backtest') {
    const b = a.backtest
    if (b?.state === 'running') return { t: `回测中 ${Math.round((b.progress || 0) * 100)}%`, c: 'warn' }
    if (b?.state === 'failed') return { t: b.error === '已取消' ? '已取消' : '回测失败', c: 'bad' }
    if (b?.state === 'done') return { t: '回测完成', c: 'info' }
    return { t: '未运行', c: '' }
  }
  if (running === 0) return paused ? { t: '已暂停', c: 'warn' } : { t: '未运行', c: '' }
  return { t: '运行中', c: 'good' }
}

// ---- numbers ----
const fin = (v?: number | null): v is number => v != null && isFinite(v)
/** USDT amount with thousands separators; `signed` adds + for gains. */
export function amt(v?: number | null, d = 2, signed = false) {
  if (!fin(v)) return '—'
  const s = Math.abs(v).toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d })
  return (v < 0 ? '-' : signed && v > 0 ? '+' : '') + s
}
/** Fraction → percent (0.012 → 1.20%). */
export function pct(v?: number | null, d = 2, signed = false) {
  if (!fin(v)) return '—'
  const p = v * 100
  return (signed && p > 0 ? '+' : '') + p.toFixed(d) + '%'
}
export const tone = (v?: number | null) => (!fin(v) || v === 0 ? '' : v > 0 ? 'up' : 'down')
export const sym = (id: string) => id.replace('-USDT-SWAP', '')
/** Contract quantity: integers plain, fractional lots up to 4 decimals. */
export const qty = (v?: number | null) => (!fin(v) ? '—' : Number.isInteger(v) ? String(v) : String(+v.toFixed(4)))

/** Short universe summary: "BTC, ETH" or "筛选 · 规则 多 前 20". */
export function universeText(u: Universe | undefined, ruleName?: (id: string) => string) {
  if (!u) return '—'
  if (u.screener) {
    const q = u.screener
    const r = q.rule ? (ruleName?.(q.rule) || q.rule) : '默认规则'
    return `筛选 · ${r} ${q.side === 'short' ? '空' : q.side === 'both' ? '多空' : '多'} 前 ${q.top}${q.minScore ? ` · ≥${q.minScore}` : ''} · 每 ${q.refresh || '1H'}`
  }
  const s = u.static || []
  if (!s.length) return '—'
  return s.length > 4 ? `${s.slice(0, 4).map(sym).join(', ')} 等 ${s.length} 个` : s.map(sym).join(', ')
}

/** Date (display zone handled by fmt.time) for backtest ranges: yyyy-mm-dd in UTC+8. */
export function day(ms?: number) {
  if (!ms) return '—'
  const d = new Date(ms + 8 * 3600_000)
  return `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, '0')}-${String(d.getUTCDate()).padStart(2, '0')}`
}
/** Client-side validation of account limits (the server validates again). Keys = Limits fields. */
export function limitErrors(l: Limits): Record<string, string> {
  const e: Record<string, string> = {}
  const between = (k: string, v: number, lo: number, hi: number, msg: string) => { if (!isFinite(v) || v < lo || v > hi) e[k] = msg }
  // ranges mirror internal/trade/accounts.go validLimits
  if (!Number.isInteger(l.maxPositions) || l.maxPositions < 1 || l.maxPositions > 100) e.maxPositions = '请输入 1–100 的整数'
  between('maxLeverage', l.maxLeverage, 1, 100, '请输入 1–100')
  between('riskPct', l.riskPct, 0.01, 5, '请输入 1%–500%')
  between('maxInstPct', l.maxInstPct, 0.01, 1, '请输入 1%–100%')
  between('dailyLossPct', l.dailyLossPct, 0, 1, '请输入 0–100%（0 = 不熔断）')
  between('slippageBp', l.slippageBp, 0.1, 500, '请输入 0.1–500 bp')
  if (!Number.isInteger(l.cooldownMin) || l.cooldownMin < 0 || l.cooldownMin > 10080) e.cooldownMin = '请输入 0–10080 的整数'
  between('takerFeeBp', l.takerFeeBp, 0, 100, '请输入 0–100 bp')
  between('makerFeeBp', l.makerFeeBp, 0, 100, '请输入 0–100 bp')
  if (l.confirm !== 'off' && (!Number.isInteger(l.confirmTimeoutS) || l.confirmTimeoutS < 10 || l.confirmTimeoutS > 3600)) e.confirmTimeoutS = '请输入 10–3600 秒'
  return e
}

/** Parse yyyy-mm-dd as a UTC+8 day start (the trading day boundary). */
export function parseDay(s: string) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s)
  return m ? Date.UTC(+m[1], +m[2] - 1, +m[3]) - 8 * 3600_000 : NaN
}

/** Strategy parameter validation against its ParamSpec (internal/trade/params.go checkParam). */
export function paramError(p: ParamSpec, v: number | undefined): string {
  if (v == null || !isFinite(v)) return '请输入数值'
  if (p.type === 'bool') return v === 0 || v === 1 ? '' : '需为开或关'
  if (p.type === 'choice' || p.type === 'timeframe') return Number.isInteger(v) && v >= 0 && v < (p.options?.length ?? 0) ? '' : '不是有效选项'
  if (p.type === 'int' && !Number.isInteger(v)) return '需为整数'
  if (p.min != null && v < p.min) return `不能小于 ${p.min}`
  if (p.max != null && v > p.max) return `不能大于 ${p.max}`
  return ''
}

/** A parameter value as text (option label, 开/关, or the number). */
export function paramText(p: ParamSpec, v: number | undefined): string {
  if (v == null) return '—'
  if (p.type === 'bool') return v ? '开' : '关'
  if (p.type === 'choice' || p.type === 'timeframe') return p.labels?.[v] ?? p.options?.[v] ?? String(v)
  return String(v)
}

/** Directions a strategy may open, from its meta (SDK 1 versions carry none: both). */
export function directionText(m?: { canLong?: boolean; canShort?: boolean }): string {
  if (!m || (m.canLong === undefined && m.canShort === undefined)) return '多 / 空'
  return [m.canLong && '多', m.canShort && '空'].filter(Boolean).join(' / ') || '—'
}

import type { Account, Action, ConfirmMode, ExecMode, InstanceState, Limits, Mode, ParamSpec, OrderState, OrderType, SignalOutcome, Universe } from '../../tradeApi'

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
  { value: 'stop', label: '止损' }, { value: 'target', label: '止盈' }, { value: 'liquidation', label: '强平' }, { value: 'reject', label: '风控拒绝' },
  { value: 'breaker', label: '熔断' }, { value: 'instance', label: '实例状态' }, { value: 'ai', label: 'AI 决策' }, { value: 'daily', label: '每日汇总' },
]

/** Overall account status for cards and headers. */
export function accountStatus(a: Account, running?: number): Lbl {
  if (a.archived) return { t: '已归档', c: '' }
  if (a.halted) return { t: '熔断', c: 'bad' }
  if (a.mode === 'backtest') return { t: '回测', c: '' }
  if (running === 0) return { t: '未运行', c: '' }
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
    return `筛选 · ${r} ${q.side === 'short' ? '空' : '多'} 前 ${q.top}${q.minScore ? ` · ≥${q.minScore}` : ''} · 每 ${q.refresh || '1H'}`
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
  if (!Number.isInteger(l.maxPositions) || l.maxPositions < 1 || l.maxPositions > 50) e.maxPositions = '请输入 1–50 的整数'
  between('maxLeverage', l.maxLeverage, 1, 100, '请输入 1–100')
  between('riskPct', l.riskPct, 0.0001, 0.1, '请输入 0.01%–10%')
  between('maxInstPct', l.maxInstPct, 0.01, 1, '请输入 1%–100%')
  between('dailyLossPct', l.dailyLossPct, 0.001, 1, '请输入 0.1%–100%')
  between('slippageBp', l.slippageBp, 0, 500, '请输入 0–500 bp')
  if (!Number.isInteger(l.cooldownMin) || l.cooldownMin < 0 || l.cooldownMin > 10080) e.cooldownMin = '请输入 0–10080 的整数'
  between('takerFeeBp', l.takerFeeBp, 0, 100, '请输入 0–100 bp')
  between('makerFeeBp', l.makerFeeBp, -10, 100, '请输入 -10–100 bp')
  if (l.confirm !== 'off' && (!Number.isInteger(l.confirmTimeoutS) || l.confirmTimeoutS < 10 || l.confirmTimeoutS > 3600)) e.confirmTimeoutS = '请输入 10–3600 秒'
  return e
}

/** Parse yyyy-mm-dd as a UTC+8 day start (the trading day boundary). */
export function parseDay(s: string) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s)
  return m ? Date.UTC(+m[1], +m[2] - 1, +m[3]) - 8 * 3600_000 : NaN
}

/** Strategy parameter validation against its ParamSpec. */
export function paramError(p: ParamSpec, v: number | undefined): string {
  if (v == null || !isFinite(v)) return '请输入数值'
  if (p.min != null && v < p.min) return `不能小于 ${p.min}`
  if (p.max != null && v > p.max) return `不能大于 ${p.max}`
  return ''
}

export function usd(v?: number | null) {
  if (v == null || !isFinite(v)) return '—'
  const a = Math.abs(v)
  if (a >= 1e8) return (v / 1e8).toFixed(2) + '亿'
  if (a >= 1e4) return (v / 1e4).toFixed(a >= 1e6 ? 0 : 1) + '万'
  return v.toFixed(0)
}
export function price(v?: number | null) {
  if (v == null || !isFinite(v) || v === 0) return '—'
  const a = Math.abs(v)
  const d = a >= 1000 ? 1 : a >= 10 ? 3 : a >= 1 ? 4 : a >= 0.01 ? 5 : 8
  return v.toFixed(d)
}
/** log return → percent string */
export function logPct(v?: number | null, d = 2) {
  if (v == null || !isFinite(v)) return '—'
  const p = (Math.exp(v) - 1) * 100
  return (p > 0 ? '+' : '') + p.toFixed(d) + '%'
}
export function num(v?: number | null, d = 2) {
  if (v == null || !isFinite(v)) return '—'
  return v.toFixed(d)
}
export function fundingH(v?: number | null) {
  if (v == null || !isFinite(v)) return '—'
  return (v * 100).toFixed(4) + '%'
}
export function fieldValue(key: string, v?: number | null) {
  if (v == null || !isFinite(v)) return '—'
  switch (key) {
    case 'turnover12h': return usd(v)
    case 'funding_hourly': case 'funding_pred_hourly': return fundingH(v) + '/h'
    case 'spread_bps': return v.toFixed(1) + 'bps'
    case 'trend_z_4h': return v.toFixed(2)
    case 'vol_surprise_4h': return '×' + Math.exp(v).toFixed(2)
    default: return logPct(v)
  }
}
export function time(ms?: number, withDate = false) {
  if (!ms) return '—'
  const d = new Date(ms)
  const hm = d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
  return withDate ? `${d.getMonth() + 1}-${d.getDate()} ${hm}` : hm
}
export function ago(ms: number) {
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000))
  if (s < 60) return s + 's'
  if (s < 3600) return Math.round(s / 60) + 'm'
  return Math.round(s / 3600) + 'h'
}
export function sign(v: number) { return v > 0 ? 'up' : v < 0 ? 'down' : '' }
export const catLabel: Record<string, string> = { '1': '加密', '3': '股票', '4': '商品' }
/** Chart time: seconds shifted so lightweight-charts (UTC) renders local wall time. */
export function chartTime(ms: number) { return Math.floor(ms / 1000 - new Date(ms).getTimezoneOffset() * 60) as any }

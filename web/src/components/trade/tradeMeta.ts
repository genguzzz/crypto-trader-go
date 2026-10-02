import type { Trade } from '../../tradeApi'
import type { Lbl } from './meta'

/** Exit kinds of a round trip (internal/trade Trade.exitKind). */
export const exitKindLabel: Record<string, Lbl> = {
  signal: { t: '信号平仓', c: '' },
  stop: { t: '止损', c: 'warn' },
  target: { t: '止盈', c: 'good' },
  time: { t: '持仓到期', c: '' },
  liquidation: { t: '强平', c: 'bad' },
  manual: { t: '手动', c: '' },
  open: { t: '持仓中', c: 'info' },
}

/** Holding time: "3天4小时" / "5小时12分" / "8分". */
export function holdText(t: Trade, now = Date.now()) {
  const ms = (t.closeTs || now) - t.openTs
  const m = Math.max(0, Math.round(ms / 60_000))
  const d = Math.floor(m / 1440), h = Math.floor((m % 1440) / 60), mm = m % 60
  if (d) return `${d}天${h ? h + '小时' : ''}`
  if (h) return `${h}小时${mm ? mm + '分' : ''}`
  return `${mm}分`
}

/** Per-instrument totals for chips and the contribution list, sorted by |net|. */
export function byInstrument(trades: Trade[]) {
  const m = new Map<string, { instId: string; n: number; wins: number; net: number; notional: number }>()
  for (const t of trades) {
    const x = m.get(t.instId) || { instId: t.instId, n: 0, wins: 0, net: 0, notional: 0 }
    x.n++; x.net += t.net; x.notional += t.notional
    if (t.closeTs && t.net > 0) x.wins++
    m.set(t.instId, x)
  }
  return [...m.values()].sort((a, b) => Math.abs(b.net) - Math.abs(a.net))
}

import type { BookLevel } from '../../api'

/** Cumulative depth point: offset from mid in %, cumulative USDT, cumulative base units. */
export interface DepthPoint { off: number; px: number; cum: number; base: number }

export function cumulative(levels: BookLevel[], mid: number, ctVal: number): DepthPoint[] {
  let cum = 0, base = 0
  return levels.map(([px, usdt, sz]) => {
    cum += usdt
    base += sz * ctVal
    return { off: (px / mid - 1) * 100, px, cum, base }
  })
}

export interface Sweep {
  side: 'buy' | 'sell'
  targetPct: number     // signed offset from mid
  targetPx: number
  usdt: number          // notional consumed to move price to target
  avgPx: number         // volume-weighted fill price
  slipPct: number       // avg fill vs mid (signed)
  levels: number
  beyondData: boolean   // target lies beyond the fetched book depth
}

/** Market order needed to push the price from mid to mid×(1+pct/100): sum of opposite-side levels up to there. */
export function sweepTo(pct: number, bids: DepthPoint[], asks: DepthPoint[], mid: number): Sweep {
  const buy = pct >= 0
  const side = buy ? asks : bids
  let last: DepthPoint | null = null, n = 0
  for (const p of side) {
    if (buy ? p.off <= pct : p.off >= pct) { last = p; n++ } else break
  }
  const usdt = last?.cum ?? 0
  const base = last?.base ?? 0
  const avgPx = base > 0 ? usdt / base : mid
  const edge = side[side.length - 1]
  return {
    side: buy ? 'buy' : 'sell', targetPct: pct, targetPx: mid * (1 + pct / 100), usdt, avgPx,
    slipPct: (avgPx / mid - 1) * 100, levels: n,
    beyondData: !!edge && (buy ? pct > edge.off : pct < edge.off),
  }
}

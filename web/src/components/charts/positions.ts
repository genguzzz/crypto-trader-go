import { ref, watch, type Ref } from 'vue'
import { LineStyle, type IPriceLine, type ISeriesApi } from 'lightweight-charts'
import { getAdminKey } from '../../composables/adminKey'
import { C } from '../../theme'

/**
 * The owner's open OKX position on the K-line (server: internal/okxacct, read-only). Entry (average
 * price, with side, size and live PnL), liquidation price and every resting TP / SL as price lines;
 * gone on the first refresh after the position closes. Owner only: without the key on this device
 * the server answers 401 and nothing is drawn (the chart never asks for the key).
 */
export interface PosTrigger { algoId: string; px: number; pxType?: string; kind: string; full: boolean; contracts?: number }
export interface Position {
  instId: string; posSide: string; side: 'long' | 'short'; mgnMode: string
  contracts: number; coins?: number; notionalUsd?: number
  avgPx: number; markPx?: number; liqPx?: number; lever?: number
  upl: number; uplRatio: number; mgnRatio?: number; margin?: number
  tp: PosTrigger[]; sl: PosTrigger[]; cTime: number; uTime: number
}
export interface AccountPositions {
  enabled: boolean; ts?: number; positions: Position[]
  equity?: { totalEq: number; availEq?: number; upl?: number; mgnRatio?: number; notionalUsd?: number }
}

/** "0.5 BTC" when the contract size is known, else "3 张". */
export const posSize = (p: Position) => {
  if (!p.coins) return `${+p.contracts.toPrecision(6)} 张`
  return `${+p.coins.toPrecision(4)} ${p.instId.split('-')[0]}`
}
const signed = (v: number, d = 2) => (v >= 0 ? '+' : '') + v.toFixed(d)

export function usePositions(series: () => ISeriesApi<'Candlestick'> | undefined, id: () => string, on: Ref<boolean>) {
  const positions = ref<Position[]>([])
  let lines: IPriceLine[] = []
  let off = false // the server has no account configured: stop asking

  async function refresh() {
    if (!on.value || off) { positions.value = []; draw(); return }
    const inst = id(), key = getAdminKey()
    try {
      const r = await fetch(`/api/v1/account/positions?instId=${encodeURIComponent(inst)}`, { headers: key ? { 'X-CTG-Admin': key } : {} })
      if (inst !== id()) return
      if (!r.ok) { if (r.status === 401) positions.value = []; draw(); return } // not the owner; other errors keep what is drawn
      const d = (await r.json()) as AccountPositions
      off = !d.enabled
      positions.value = d.positions || []
    } catch { /* keep what is drawn */ }
    draw()
  }

  function draw() {
    const s = series()
    if (!s) return
    for (const l of lines) s.removePriceLine(l)
    lines = []
    for (const p of positions.value) {
      const long = p.side === 'long', tag = long ? '持多' : '持空'
      const line = (price: number, color: string, title: string, style = LineStyle.Dashed, width: 1 | 2 = 1) =>
        lines.push(s.createPriceLine({ price, color, lineWidth: width, lineStyle: style, axisLabelVisible: true, title }))
      line(p.avgPx, long ? C.up : C.down, `${tag} ${posSize(p)} ${signed(p.upl)}U`, LineStyle.Solid, 2)
      if (p.liqPx) line(p.liqPx, C.amber, '强平', LineStyle.Dotted, 2)
      const part = (t: PosTrigger) => (t.full ? '' : ` ${+(t.contracts ?? 0).toPrecision(6)}张`)
      for (const t of p.tp) line(t.px, C.up, `止盈${part(t)}`)
      for (const t of p.sl) line(t.px, C.down, `止损${part(t)}`)
    }
  }

  /** Levels the price scale should span: entry, nearest TP and SL (the liquidation price only when
   *  there is no stop, since it is often far enough away to squash the candles). */
  const levels = () => positions.value.flatMap(p => {
    const xs = [p.avgPx]
    if (p.tp[0]) xs.push(p.tp[0].px)
    if (p.sl[0]) xs.push(p.sl[0].px)
    else if (p.liqPx) xs.push(p.liqPx)
    return xs
  })

  watch(on, v => { localStorage.setItem('ctg.chartPositions', v ? '1' : '0'); refresh() })
  return { positions, refresh, draw, levels }
}

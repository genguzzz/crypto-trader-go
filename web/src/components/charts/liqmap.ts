import type { ISeriesPrimitive, IPrimitivePaneView, IPrimitivePaneRenderer, SeriesAttachedParameter, Time, ISeriesApi, SeriesType } from 'lightweight-charts'
import type { CanvasRenderingTarget2D } from 'fancy-canvas'
import { alpha, liqTierColor } from '../../theme'
import type { LiqMap, LiqBin } from '../../api'

const ROW_PX = 3 // merge price bins until a row is at least this tall
const MAX_W = 0.24 // widest bar: share of the pane width
const MAX_W_PX = 200

/**
 * Estimated liquidation map drawn over the candles: a horizontal histogram against the right edge
 * of the price pane, each bar stacked by leverage tier (long liquidations below the price in a red
 * ramp, short above in a green ramp, brighter = higher leverage) plus the cumulative amount from
 * the last price outward (thin lines, own scale). Hidden tiers drop out of bars, rows and lines.
 */
type Row = { long: number; short: number; longBy: number[]; shortBy: number[] }
export class LiqMapOverlay implements ISeriesPrimitive<Time> {
  private m?: LiqMap
  private series?: ISeriesApi<SeriesType>
  private requestUpdate?: () => void
  private view: IPrimitivePaneView
  private cumLong: [number, number][] = [] // [price, cumulative USDT from the last price down to it], descending price
  private cumShort: [number, number][] = [] // ascending price
  private rowStep = 0 // price size of the rows last drawn
  private rowSums = new Map<number, Row>()
  private hoverPx: number | null = null
  private hidden = new Set<number>() // leverage tiers not drawn

  constructor() {
    const renderer: IPrimitivePaneRenderer = { draw: t => this.draw(t) }
    this.view = { zOrder: () => 'bottom', renderer: () => renderer }
  }
  attached(p: SeriesAttachedParameter<Time>) { this.series = p.series; this.requestUpdate = p.requestUpdate }
  detached() { this.series = this.requestUpdate = undefined }
  paneViews() { return [this.view] }

  setData(m: LiqMap) {
    this.m = m
    this.rebuild()
  }

  /** Leverage tiers to leave out (by leverage). */
  setHidden(levers: number[]) {
    this.hidden = new Set(levers)
    this.rebuild()
  }

  /** Visible amount of one side of a bin. */
  private val(b: LiqBin, long: boolean) {
    const by = long ? b.longBy : b.shortBy
    if (!by || !this.m) return long ? b.long : b.short
    let v = 0
    by.forEach((x, i) => { if (!this.hidden.has(this.m!.levers[i])) v += x })
    return v
  }

  private rebuild() {
    const m = this.m
    if (!m) return
    const bins = m.bins || []
    let a = 0
    this.cumLong = bins.filter(b => b.p < m.px).sort((x, y) => y.p - x.p).map(b => [b.p, (a += this.val(b, true))] as [number, number]).filter((x, i, xs) => i === 0 ? x[1] > 0 : x[1] > xs[i - 1][1])
    a = 0
    this.cumShort = bins.filter(b => b.p >= m.px).sort((x, y) => x.p - y.p).map(b => [b.p + m.step, (a += this.val(b, false))] as [number, number]).filter((x, i, xs) => i === 0 ? x[1] > 0 : x[1] > xs[i - 1][1])
    this.requestUpdate?.()
  }

  /** Highlight the row under the crosshair (null = none). */
  setHover(px: number | null) {
    if (px === this.hoverPx) return
    this.hoverPx = px
    this.requestUpdate?.()
  }

  /** The drawn row containing `px`: its price range and estimated amounts. */
  rowAt(px: number): { lo: number; hi: number; long: number; short: number; tiers: { lever: number; long: number; short: number }[] } | null {
    if (!this.rowStep || !this.m) return null
    const k = Math.floor(px / this.rowStep + 1e-9), r = this.rowSums.get(k)
    const tiers = this.m.levers.map((lever, i) => ({ lever, long: r?.longBy[i] ?? 0, short: r?.shortBy[i] ?? 0 }))
      .filter(t => !this.hidden.has(t.lever) && t.long + t.short > 0)
    return { lo: k * this.rowStep, hi: (k + 1) * this.rowStep, long: r?.long ?? 0, short: r?.short ?? 0, tiers }
  }

  /** Estimated USDT liquidated if price moved from the last price to `px` (longs below, shorts above). */
  cumAt(px: number): { long: boolean; usd: number } | null {
    if (!this.m) return null
    const long = px < this.m.px
    const xs = long ? this.cumLong : this.cumShort
    let v = 0
    for (const [p, c] of xs) {
      if (long ? p >= px : p <= px) v = c
      else break
    }
    return { long, usd: v }
  }

  private draw(target: CanvasRenderingTarget2D) {
    const m = this.m, s = this.series
    if (!m || !s || !m.bins?.length) return
    target.useMediaCoordinateSpace(({ context: ctx, mediaSize }) => {
      // full pane height: the bands below (volume, OI, liquidations) draw over the map where they meet
      this.paint(ctx, mediaSize.width, mediaSize.height, m, s)
    })
  }

  private paint(ctx: CanvasRenderingContext2D, W: number, H: number, m: LiqMap, s: ISeriesApi<SeriesType>) {
    {
      const y0 = s.priceToCoordinate(m.px), y1 = s.priceToCoordinate(m.px + m.step)
      if (y0 == null || y1 == null) return
      const binPx = Math.abs(y0 - y1)
      if (!(binPx > 0)) return
      const mult = Math.max(1, Math.ceil(ROW_PX / binPx)), rowStep = m.step * mult
      // regroup to display rows; only rows on screen set the scale
      const n = m.levers.length, rows = new Map<number, Row>()
      for (const b of m.bins) {
        const k = Math.floor(b.p / rowStep + 1e-9)
        let r = rows.get(k)
        if (!r) { r = { long: 0, short: 0, longBy: new Array(n).fill(0), shortBy: new Array(n).fill(0) }; rows.set(k, r) }
        for (let i = 0; i < n; i++) {
          if (this.hidden.has(m.levers[i])) continue
          const l = b.longBy?.[i] ?? (n === 1 ? b.long : 0), sh = b.shortBy?.[i] ?? (n === 1 ? b.short : 0)
          r.longBy[i] += l; r.shortBy[i] += sh; r.long += l; r.short += sh
        }
      }
      this.rowStep = rowStep
      this.rowSums = rows
      const drawn: { top: number; h: number; r: Row }[] = []
      let max = 0
      for (const [k, r] of rows) {
        const ya = s.priceToCoordinate(k * rowStep), yb = s.priceToCoordinate((k + 1) * rowStep)
        if (ya == null || yb == null) continue
        const top = Math.min(ya, yb), h = Math.max(1, Math.abs(ya - yb) - (mult * binPx >= 4 ? 1 : 0))
        if (top > H || top + h < 0) continue
        drawn.push({ top, h, r })
        max = Math.max(max, r.long + r.short)
      }
      if (!max) return
      const maxW = Math.min(MAX_W_PX, W * MAX_W)
      const hk = this.hoverPx != null ? Math.floor(this.hoverPx / rowStep + 1e-9) : null
      if (hk != null) { // hovered row: faint band across the map's width so the row being read is clear
        const ya = s.priceToCoordinate(hk * rowStep), yb = s.priceToCoordinate((hk + 1) * rowStep)
        if (ya != null && yb != null) {
          ctx.fillStyle = 'rgba(255,255,255,.08)'
          ctx.fillRect(W - maxW, Math.min(ya, yb), maxW, Math.max(1, Math.abs(ya - yb)))
        }
      }
      // stacked from the right edge: longs by tier (low leverage outermost-right), then shorts
      ctx.globalAlpha = 0.75
      for (const { top, h, r } of drawn) {
        let x = W
        for (const [by, long] of [[r.longBy, true], [r.shortBy, false]] as [number[], boolean][]) {
          for (let i = 0; i < n; i++) {
            const w = (by[i] / max) * maxW
            if (w <= 0) continue
            x -= w
            if (w > 0.3) { ctx.fillStyle = liqTierColor(m.levers[i], long); ctx.fillRect(x, top, w, h) }
          }
        }
      }
      ctx.globalAlpha = 1
      // cumulative from the last price outward, scaled to the larger side
      const cmax = Math.max(this.cumLong.at(-1)?.[1] ?? 0, this.cumShort.at(-1)?.[1] ?? 0)
      if (!cmax) return
      const line = (xs: [number, number][], color: string) => {
        ctx.beginPath()
        ctx.moveTo(W, y0)
        for (const [p, c] of xs) {
          const y = s.priceToCoordinate(p)
          if (y == null) continue
          ctx.lineTo(W - (c / cmax) * maxW, y)
        }
        ctx.strokeStyle = color
        ctx.lineWidth = 1
        ctx.stroke()
      }
      line(this.cumLong, alpha('down', 0.9))
      line(this.cumShort, alpha('up', 0.9))
    }
  }
}

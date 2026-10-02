import type { ISeriesPrimitive, IPrimitivePaneView, IPrimitivePaneRenderer, SeriesAttachedParameter, Time, ISeriesApi, SeriesType } from 'lightweight-charts'
import type { CanvasRenderingTarget2D } from 'fancy-canvas'
import { alpha, liqTierColor } from '../../theme'
import type { LiqMap, LiqBin } from '../../api'

const ROW_PX = 3 // a drawn row is at least this tall: price bins merge by a factor from MULTS
const MULTS = [1, 2, 5, 10, 20, 50, 100, 200, 500, 1000]
// Bar lengths are relative to the largest row within ±w of the last price, w the smallest window
// covering what is on screen. Steps instead of the exact visible range keep the bars still while
// the chart is dragged (autoscale moves the range every frame).
const WINDOWS = [0.01, 0.02, 0.05, 0.1, 0.2, 0.5]

/** Width of the map against the right edge of a price pane `paneW` px wide. */
export const liqMapWidth = (paneW: number) => Math.min(200, paneW * 0.24)

type Row = { long: number; short: number; longBy: number[]; shortBy: number[] }

/**
 * Estimated liquidation map drawn over the candles: a horizontal histogram against the right edge
 * of the price pane, each bar stacked by leverage tier (long liquidations below the price in a red
 * ramp, short above in a green ramp, brighter = higher leverage) plus the cumulative amount from
 * the last price outward (thin lines). Hidden tiers drop out of bars, rows, lines and readouts.
 */
export class LiqMapOverlay implements ISeriesPrimitive<Time> {
  private m?: LiqMap
  private series?: ISeriesApi<SeriesType>
  private requestUpdate?: () => void
  private view: IPrimitivePaneView
  private cumLong: [number, number][] = [] // [price, cumulative USDT from the last price down to it], descending price
  private cumShort: [number, number][] = [] // ascending price
  private mult = 0 // bins per drawn row (sticky, see pickMult)
  private rows = new Map<number, Row>() // row index (floor(p / (step·mult))) → amounts; rebuilt when mult/data/tiers change
  private rowsMult = 0
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

  /** Highlight the row under the crosshair (null = none). */
  setHover(px: number | null) {
    if (px === this.hoverPx) return
    this.hoverPx = px
    this.requestUpdate?.()
  }

  private visible(by: number[]) {
    let v = 0
    by.forEach((x, i) => { if (!this.hidden.has(this.m!.levers[i])) v += x })
    return v
  }

  private rebuild() {
    const m = this.m
    if (!m) return
    const cum = (bins: LiqBin[], long: boolean, edge: number): [number, number][] => {
      let a = 0
      const out: [number, number][] = []
      for (const b of bins) {
        const v = this.visible(long ? b.longBy : b.shortBy)
        if (v > 0) out.push([b.p + edge, (a += v)])
      }
      return out
    }
    const bins = m.bins || []
    this.cumLong = cum(bins.filter(b => b.p < m.px).reverse(), true, 0)
    this.cumShort = cum(bins.filter(b => b.p >= m.px), false, m.step)
    this.rowsMult = 0
    this.requestUpdate?.()
  }

  private rowsFor(mult: number) {
    if (this.rowsMult === mult) return this.rows
    const m = this.m!, n = m.levers.length, rowStep = m.step * mult, rows = new Map<number, Row>()
    for (const b of m.bins || []) {
      const k = Math.floor(b.p / rowStep + 1e-9)
      let r = rows.get(k)
      if (!r) { r = { long: 0, short: 0, longBy: new Array(n).fill(0), shortBy: new Array(n).fill(0) }; rows.set(k, r) }
      for (let i = 0; i < n; i++) {
        if (this.hidden.has(m.levers[i])) continue
        r.longBy[i] += b.longBy[i]; r.shortBy[i] += b.shortBy[i]; r.long += b.longBy[i]; r.short += b.shortBy[i]
      }
    }
    this.rows = rows
    this.rowsMult = mult
    return rows
  }

  /** Merge factor for bins `binPx` tall: kept while rows stay 2–9 px, so small zooms don't regroup. */
  private pickMult(binPx: number) {
    const h = this.mult * binPx
    if (this.mult && (h >= 2 || this.mult === MULTS.at(-1)) && (h <= 3 * ROW_PX || this.mult === 1)) return this.mult
    this.mult = MULTS.find(x => x * binPx >= ROW_PX) ?? MULTS.at(-1)!
    return this.mult
  }

  /** The drawn row containing `px`: its price range and estimated amounts. */
  rowAt(px: number): { lo: number; hi: number; long: number; short: number; tiers: { lever: number; long: number; short: number }[] } | null {
    if (!this.m || !this.rowsMult) return null
    const rowStep = this.m.step * this.rowsMult, k = Math.floor(px / rowStep + 1e-9), r = this.rows.get(k)
    const tiers = this.m.levers.map((lever, i) => ({ lever, long: r?.longBy[i] ?? 0, short: r?.shortBy[i] ?? 0 }))
      .filter(t => !this.hidden.has(t.lever) && t.long + t.short > 0)
    return { lo: k * rowStep, hi: (k + 1) * rowStep, long: r?.long ?? 0, short: r?.short ?? 0, tiers }
  }

  /** Estimated USDT liquidated if price moved from the last price to `px` (longs below, shorts above). */
  cumAt(px: number): { long: boolean; usd: number } | null {
    if (!this.m) return null
    const long = px < this.m.px
    let v = 0
    for (const [p, c] of long ? this.cumLong : this.cumShort) {
      if (long ? p >= px : p <= px) v = c
      else break
    }
    return { long, usd: v }
  }

  private draw(target: CanvasRenderingTarget2D) {
    const m = this.m, s = this.series
    if (!m || !s || !m.bins?.length) return
    // full pane height: the bands below (volume, OI, liquidations) draw over the map where they meet
    target.useMediaCoordinateSpace(({ context, mediaSize }) => this.paint(context, mediaSize.width, mediaSize.height, m, s))
  }

  private paint(ctx: CanvasRenderingContext2D, W: number, H: number, m: LiqMap, s: ISeriesApi<SeriesType>) {
    const y0 = s.priceToCoordinate(m.px), y1 = s.priceToCoordinate(m.px + m.step)
    const pTop = s.coordinateToPrice(0), pBot = s.coordinateToPrice(H)
    if (y0 == null || y1 == null || pTop == null || pBot == null) return
    const binPx = Math.abs(y0 - y1)
    if (!(binPx > 0)) return
    const mult = this.pickMult(binPx), rowStep = m.step * mult, rows = this.rowsFor(mult), n = m.levers.length
    const reach = Math.max(Math.abs(pTop / m.px - 1), Math.abs(pBot / m.px - 1))
    const win = WINDOWS.find(w => w >= reach) ?? WINDOWS.at(-1)!
    const drawn: { top: number; h: number; r: Row }[] = []
    let max = 0
    for (const [k, r] of rows) {
      if (Math.abs(((k + 0.5) * rowStep) / m.px - 1) <= win) max = Math.max(max, r.long + r.short)
      const ya = s.priceToCoordinate(k * rowStep), yb = s.priceToCoordinate((k + 1) * rowStep)
      if (ya == null || yb == null) continue
      const top = Math.min(ya, yb), h = Math.max(1, Math.abs(ya - yb) - (mult * binPx >= 4 ? 1 : 0))
      if (top <= H && top + h >= 0) drawn.push({ top, h, r })
    }
    if (!max) return
    const maxW = liqMapWidth(W)
    if (this.hoverPx != null) { // hovered row: faint band across the map's width so the row being read is clear
      const hk = Math.floor(this.hoverPx / rowStep + 1e-9)
      const ya = s.priceToCoordinate(hk * rowStep), yb = s.priceToCoordinate((hk + 1) * rowStep)
      if (ya != null && yb != null) {
        ctx.fillStyle = 'rgba(255,255,255,.08)'
        ctx.fillRect(W - maxW, Math.min(ya, yb), maxW, Math.max(1, Math.abs(ya - yb)))
      }
    }
    // stacked from the right edge by tier (low leverage outermost-right): longs, then shorts
    ctx.globalAlpha = 0.75
    for (const { top, h, r } of drawn) {
      let x = W
      for (const [by, long] of [[r.longBy, true], [r.shortBy, false]] as [number[], boolean][]) {
        for (let i = 0; i < n; i++) {
          const w = (by[i] / max) * maxW
          if (w <= 0) continue
          x -= w
          ctx.fillStyle = liqTierColor(m.levers[i], long)
          ctx.fillRect(x, top, w, h)
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
        if (y != null) ctx.lineTo(W - (c / cmax) * maxW, y)
      }
      ctx.strokeStyle = color
      ctx.lineWidth = 1
      ctx.stroke()
    }
    line(this.cumLong, alpha('down', 0.9))
    line(this.cumShort, alpha('up', 0.9))
  }
}

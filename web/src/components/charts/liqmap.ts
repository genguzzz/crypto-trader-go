import type { ISeriesPrimitive, IPrimitivePaneView, IPrimitivePaneRenderer, SeriesAttachedParameter, Time, ISeriesApi, SeriesType } from 'lightweight-charts'
import type { CanvasRenderingTarget2D } from 'fancy-canvas'
import { alpha } from '../../theme'
import type { LiqMap } from '../../api'

const ROW_PX = 3 // merge price bins until a row is at least this tall
const MAX_W = 0.24 // widest bar: share of the pane width
const MAX_W_PX = 200

/**
 * Estimated liquidation map drawn over the candles: a horizontal histogram against the right edge
 * of the price pane (long liquidations below the price in red, short above in green) plus the
 * cumulative amount from the last price outward (thin lines, own scale).
 */
export class LiqMapOverlay implements ISeriesPrimitive<Time> {
  private m?: LiqMap
  private series?: ISeriesApi<SeriesType>
  private requestUpdate?: () => void
  private view: IPrimitivePaneView
  private cumLong: [number, number][] = [] // [price, cumulative USDT from the last price down to it], descending price
  private cumShort: [number, number][] = [] // ascending price

  constructor() {
    const renderer: IPrimitivePaneRenderer = { draw: t => this.draw(t) }
    this.view = { zOrder: () => 'bottom', renderer: () => renderer }
  }
  attached(p: SeriesAttachedParameter<Time>) { this.series = p.series; this.requestUpdate = p.requestUpdate }
  detached() { this.series = this.requestUpdate = undefined }
  paneViews() { return [this.view] }

  setData(m: LiqMap) {
    this.m = m
    const bins = m.bins || []
    let a = 0
    this.cumLong = bins.filter(b => b.long > 0 && b.p < m.px).sort((x, y) => y.p - x.p).map(b => [b.p, (a += b.long)])
    a = 0
    this.cumShort = bins.filter(b => b.short > 0 && b.p >= m.px).sort((x, y) => x.p - y.p).map(b => [b.p + m.step, (a += b.short)])
    this.requestUpdate?.()
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
      const W = mediaSize.width
      // stay inside the candles' own area: the bands below (volume, OI, liquidations) share the pane
      const H = mediaSize.height * (1 - (s.priceScale().options().scaleMargins?.bottom ?? 0))
      ctx.save()
      ctx.beginPath()
      ctx.rect(0, 0, W, H)
      ctx.clip()
      this.paint(ctx, W, H, m, s)
      ctx.restore()
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
      const rows = new Map<number, { long: number; short: number }>()
      for (const b of m.bins) {
        const k = Math.floor(b.p / rowStep + 1e-9)
        const r = rows.get(k) ?? { long: 0, short: 0 }
        r.long += b.long; r.short += b.short
        rows.set(k, r)
      }
      const drawn: { top: number; h: number; r: { long: number; short: number } }[] = []
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
      for (const { top, h, r } of drawn) {
        const wl = (r.long / max) * maxW, ws = (r.short / max) * maxW
        if (wl > 0.3) { ctx.fillStyle = alpha('down', 0.5); ctx.fillRect(W - wl, top, wl, h) }
        if (ws > 0.3) { ctx.fillStyle = alpha('up', 0.5); ctx.fillRect(W - ws - wl, top, ws, h) }
      }
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

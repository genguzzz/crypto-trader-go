import type { IChartApi, ISeriesApi, ISeriesPrimitive, IPrimitivePaneView, IPrimitivePaneRenderer, SeriesAttachedParameter, Time, SeriesType } from 'lightweight-charts'
import type { CanvasRenderingTarget2D } from 'fancy-canvas'

/** [level low price, taker buy USDT, taker sell USDT] */
export type Level = [number, number, number]
export interface FootBar { levels: Level[]; step: number }

const short = (v: number) => (v >= 1e8 ? (v / 1e8).toFixed(1) + '亿' : v >= 1e4 ? (v / 1e4).toFixed(v >= 1e5 ? 0 : 1) + '万' : v.toFixed(0))

/**
 * Footprint (cluster) overlay for the candle series: per bar, per price level, taker sell on the
 * left half and taker buy on the right half, shaded by share of the bar's largest level; the
 * point of control (largest level) is outlined. Numbers appear once bars are wide enough.
 */
export class Footprint implements ISeriesPrimitive<Time> {
  private bars = new Map<number, FootBar>()
  private chart?: IChartApi
  private series?: ISeriesApi<SeriesType>
  private requestUpdate?: () => void
  private view: IPrimitivePaneView

  constructor() {
    const renderer: IPrimitivePaneRenderer = { draw: t => this.draw(t) }
    this.view = { zOrder: () => 'top', renderer: () => renderer }
  }

  attached(p: SeriesAttachedParameter<Time>) {
    this.chart = p.chart as IChartApi
    this.series = p.series
    this.requestUpdate = p.requestUpdate
  }
  detached() { this.chart = this.series = this.requestUpdate = undefined }
  paneViews() { return [this.view] }

  setData(bars: Map<number, FootBar>) {
    this.bars = bars
    this.requestUpdate?.()
  }

  private draw(target: CanvasRenderingTarget2D) {
    const chart = this.chart, series = this.series
    if (!chart || !series || !this.bars.size) return
    const ts = chart.timeScale()
    const spacing = ts.options().barSpacing
    const w = spacing * 0.92
    if (w < 6) return // too zoomed out to read anything
    target.useBitmapCoordinateSpace(({ context: ctx, horizontalPixelRatio: hr, verticalPixelRatio: vr, bitmapSize }) => {
      const showText = w >= 58
      ctx.font = `${Math.round(10 * vr)}px ui-monospace, SFMono-Regular, Menlo, monospace`
      ctx.textBaseline = 'middle'
      for (const [time, fb] of this.bars) {
        const x = ts.timeToCoordinate(time as Time)
        if (x == null || x < -w || x * hr > bitmapSize.width + w * hr) continue
        let max = 0, poc = -1
        fb.levels.forEach((l, i) => { const v = l[1] + l[2]; if (v > max) { max = v; poc = i } })
        if (max <= 0) continue
        const left = Math.round((x - w / 2) * hr), mid = Math.round(x * hr), right = Math.round((x + w / 2) * hr)
        fb.levels.forEach(([px, buy, sell], i) => {
          const yTop = series.priceToCoordinate(px + fb.step), yBot = series.priceToCoordinate(px)
          if (yTop == null || yBot == null) return
          const top = Math.round(yTop * vr), h = Math.max(1, Math.round(yBot * vr) - top - 1)
          ctx.fillStyle = `rgba(246, 70, 93, ${0.12 + 0.6 * (sell / max)})`
          ctx.fillRect(left, top, mid - left - 1, h)
          ctx.fillStyle = `rgba(14, 203, 129, ${0.12 + 0.6 * (buy / max)})`
          ctx.fillRect(mid, top, right - mid, h)
          if (i === poc) {
            ctx.strokeStyle = 'rgba(245, 197, 66, .9)'
            ctx.lineWidth = Math.max(1, hr)
            ctx.strokeRect(left + 0.5, top + 0.5, right - left - 1, h - 1)
          }
          if (showText && h >= 11 * vr) {
            // imbalance: one side ≥ 3× the other is emphasised
            ctx.fillStyle = sell > 3 * buy && sell > 0 ? '#ffb4bf' : 'rgba(234, 236, 239, .85)'
            ctx.textAlign = 'right'
            ctx.fillText(short(sell), mid - 3 * hr, top + h / 2)
            ctx.fillStyle = buy > 3 * sell && buy > 0 ? '#9ff0cf' : 'rgba(234, 236, 239, .85)'
            ctx.textAlign = 'left'
            ctx.fillText(short(buy), mid + 3 * hr, top + h / 2)
          }
        })
      }
    })
  }
}

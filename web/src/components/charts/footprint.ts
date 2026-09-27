import type { IChartApi, ISeriesApi, ISeriesPrimitive, IPrimitivePaneView, IPrimitivePaneRenderer, SeriesAttachedParameter, Time, SeriesType } from 'lightweight-charts'
import type { CanvasRenderingTarget2D } from 'fancy-canvas'

/** [level low price, taker buy USDT, taker sell USDT] */
export type Level = [number, number, number]
export interface FootBar { levels: Level[]; step: number; low: number; high: number }

const short = (v: number) => (v >= 1e8 ? (v / 1e8).toFixed(1) + '亿' : v >= 1e4 ? (v / 1e4).toFixed(v >= 1e5 ? 0 : 1) + '万' : v.toFixed(0))

// Conventions below follow common open-source/vendor footprint implementations (flowsurface,
// ATAS, Sierra Chart, orderflow-station) — algorithms and default numbers only, no code copied:
// diagonal imbalance at a fixed ratio with an adaptive volume floor, "naked" (unfilled) POC lines
// carried forward until a later bar's range covers them, and colour scaled to the visible range.
const IMBALANCE_RATIO = 3.0 // ATAS/orderflow-station convention: plain multiplier, not flowsurface's "1+pct" form
const STACK_MIN = 3 // consecutive same-side imbalanced levels to count as a "stacked" run
const NPOC_LOOKBACK = 80 // bars

interface BarCalc {
  time: number
  fb: FootBar
  poc: number // price of the point of control (largest buy+sell level)
  vah: number
  val: number
  imbBuy: Set<number> // level indices (px/step, rounded) flagged as buy-side imbalance
  imbSell: Set<number>
  stackBuy: Set<number>
  stackSell: Set<number>
}

/** POC = largest total level; ties keep the one nearest the bar's mid price (deterministic, unlike a bare HashMap scan). */
function pointOfControl(levels: Level[], mid: number): number {
  let bestPx = levels[0]?.[0] ?? 0, bestV = -1, bestDist = Infinity
  for (const [px, b, s] of levels) {
    const v = b + s, d = Math.abs(px - mid)
    if (v > bestV || (v === bestV && d < bestDist)) { bestV = v; bestPx = px; bestDist = d }
  }
  return bestPx
}

/** Value area (70%): expand from the POC one level at a time, taking the larger neighbour each step (ties go up). */
function valueArea(levels: Level[], step: number, poc: number) {
  const total = levels.reduce((s, [, b, se]) => s + b + se, 0)
  if (!total) return { vah: poc, val: poc }
  const byIdx = new Map(levels.map(l => [Math.round(l[0] / step), l[1] + l[2]]))
  const pocIdx = Math.round(poc / step)
  let lo = pocIdx, hi = pocIdx, acc = byIdx.get(pocIdx) ?? 0
  while (acc < total * 0.7) {
    const up = byIdx.get(hi + 1) ?? 0, down = byIdx.get(lo - 1) ?? 0
    if (up === 0 && down === 0) break
    if (up >= down) { hi++; acc += up } else { lo--; acc += down }
  }
  return { vah: hi * step, val: lo * step }
}

/** Diagonal imbalance: buy@P vs sell@(P−step), sell@P vs buy@(P+step); floor = adaptive minimum volume. */
function imbalances(levels: Level[], step: number) {
  const byIdx = new Map(levels.map(l => [Math.round(l[0] / step), l]))
  const totals = levels.map(([, b, s]) => b + s).sort((a, b) => a - b)
  const floor = totals[Math.floor(totals.length / 2)] ?? 0 // median level total this bar
  const buy = new Set<number>(), sell = new Set<number>()
  for (const [px, b] of levels) {
    const i = Math.round(px / step)
    const below = byIdx.get(i - 1)
    if (below && b >= floor && b >= IMBALANCE_RATIO * below[2]) buy.add(i)
  }
  for (const [px, , s] of levels) {
    const i = Math.round(px / step)
    const above = byIdx.get(i + 1)
    if (above && s >= floor && s >= IMBALANCE_RATIO * above[1]) sell.add(i)
  }
  const stackBuy = stackedRuns(buy), stackSell = stackedRuns(sell)
  return { buy, sell, stackBuy, stackSell }
}

function stackedRuns(idxs: Set<number>): Set<number> {
  const sorted = [...idxs].sort((a, b) => a - b)
  const out = new Set<number>()
  let runStart = 0
  for (let i = 0; i <= sorted.length; i++) {
    if (i === sorted.length || sorted[i] !== sorted[i - 1] + 1) {
      if (i - runStart >= STACK_MIN) for (let k = runStart; k < i; k++) out.add(sorted[k])
      runStart = i
    }
  }
  return out
}

/**
 * Footprint (cluster) overlay for the candle series: per bar, per price level, taker sell on the
 * left half and taker buy on the right half. Colour intensity scales to the largest level over the
 * visible range (not just the one bar) so bars are comparable at a glance. The POC (largest level)
 * is outlined; a "naked" POC — one no later bar's range has traded back through — is carried
 * forward as a dashed line until it is filled or falls out of the lookback window. A level flagged
 * as a diagonal imbalance (one side ≥3× the diagonal opposite, above a per-bar volume floor) gets a
 * coloured tick; 3+ consecutive imbalanced levels on the same side ("stacked") get a thicker one.
 */
export class Footprint implements ISeriesPrimitive<Time> {
  private calc: BarCalc[] = []
  private byTime = new Map<number, BarCalc>()
  private chart?: IChartApi
  private series?: ISeriesApi<SeriesType>
  private requestUpdate?: () => void
  private view: IPrimitivePaneView

  constructor() {
    const renderer: IPrimitivePaneRenderer = { draw: t => this.draw(t) }
    this.view = { zOrder: () => 'top', renderer: () => renderer }
  }

  attached(p: SeriesAttachedParameter<Time>) {
    console.log('[footprint] attached')
    this.chart = p.chart as IChartApi
    this.series = p.series
    this.requestUpdate = p.requestUpdate
  }
  detached() { this.chart = this.series = this.requestUpdate = undefined }
  paneViews() { return [this.view] }

  setData(bars: Map<number, FootBar>) {
    const calc: BarCalc[] = []
    for (const [time, fb] of [...bars].sort((a, b) => a[0] - b[0])) {
      if (!fb.levels.length) continue
      const poc = pointOfControl(fb.levels, (fb.low + fb.high) / 2)
      const { vah, val } = valueArea(fb.levels, fb.step, poc)
      const { buy, sell, stackBuy, stackSell } = imbalances(fb.levels, fb.step)
      calc.push({ time, fb, poc, vah, val, imbBuy: buy, imbSell: sell, stackBuy, stackSell })
    }
    this.calc = calc
    this.byTime = new Map(calc.map(c => [c.time, c]))
    console.log('[footprint] setData bars', bars.size, 'calc', calc.length)
    this.requestUpdate?.()
  }

  /** POC / VAH / VAL for the bar at `time`, for the legend (avoids redoing the math on the Vue side). */
  barStats(time: number) {
    const c = this.byTime.get(time)
    return c ? { poc: c.poc, vah: c.vah, val: c.val } : null
  }

  private draw(target: CanvasRenderingTarget2D) {
    console.log('[footprint] draw() called')
    const chart = this.chart, series = this.series
    if (!chart || !series || !this.calc.length) { console.log('[footprint] draw skip', !!chart, !!series, this.calc.length); return }
    const ts = chart.timeScale()
    const range = ts.getVisibleRange()
    const spacing = ts.options().barSpacing
    const w = spacing * 0.92
    if (w < 6) { console.log('[footprint] w too small', w); return }

    // colour scale: largest level total over bars currently in view (falls back to the single bar)
    const visible = range ? this.calc.filter(c => c.time >= (range.from as number) && c.time <= (range.to as number)) : this.calc
    let scaleMax = 0
    for (const c of visible.length ? visible : this.calc) for (const [, b, s] of c.fb.levels) scaleMax = Math.max(scaleMax, b, s)
    if (scaleMax <= 0) return

    target.useBitmapCoordinateSpace(({ context: ctx, horizontalPixelRatio: hr, verticalPixelRatio: vr, bitmapSize }) => {
      const showText = w >= 58
      ctx.font = `${Math.round(10 * vr)}px ui-monospace, SFMono-Regular, Menlo, monospace`
      ctx.textBaseline = 'middle'
      for (const c of this.calc) {
        const x = ts.timeToCoordinate(c.time as Time)
        if (x == null || x < -w || x * hr > bitmapSize.width + w * hr) continue
        const { fb } = c
        const left = Math.round((x - w / 2) * hr), mid = Math.round(x * hr), right = Math.round((x + w / 2) * hr)
        for (const [px, buy, sell] of fb.levels) {
          const yTop = series.priceToCoordinate(px + fb.step), yBot = series.priceToCoordinate(px)
          if (yTop == null || yBot == null) continue
          const top = Math.round(yTop * vr), h = Math.max(1, Math.round(yBot * vr) - top - 1)
          ctx.fillStyle = `rgba(246, 70, 93, ${0.1 + 0.65 * Math.min(1, sell / scaleMax)})`
          ctx.fillRect(left, top, mid - left - 1, h)
          ctx.fillStyle = `rgba(14, 203, 129, ${0.1 + 0.65 * Math.min(1, buy / scaleMax)})`
          ctx.fillRect(mid, top, right - mid, h)

          const i = Math.round(px / fb.step)
          if (c.stackBuy.has(i) || c.stackSell.has(i)) {
            ctx.strokeStyle = c.stackBuy.has(i) ? 'rgba(14, 203, 129, .95)' : 'rgba(246, 70, 93, .95)'
            ctx.lineWidth = Math.max(1.5, 1.5 * hr)
            ctx.strokeRect(left + 1, top + 1, right - left - 2, h - 2)
          } else if (c.imbBuy.has(i) || c.imbSell.has(i)) {
            ctx.fillStyle = c.imbBuy.has(i) ? 'rgba(14, 203, 129, .95)' : 'rgba(246, 70, 93, .95)'
            ctx.fillRect(c.imbBuy.has(i) ? right - 3 * hr : left, top, 3 * hr, h) // small tick on the flagged side
          }
          if (px === c.poc) {
            ctx.strokeStyle = 'rgba(245, 197, 66, .9)'
            ctx.lineWidth = Math.max(1, hr)
            ctx.strokeRect(left + 0.5, top + 0.5, right - left - 1, h - 1)
          }
          if (showText && h >= 11 * vr) {
            ctx.fillStyle = sell > 3 * buy && sell > 0 ? '#ffb4bf' : 'rgba(234, 236, 239, .85)'
            ctx.textAlign = 'right'
            ctx.fillText(short(sell), mid - 5 * hr, top + h / 2)
            ctx.fillStyle = buy > 3 * sell && buy > 0 ? '#9ff0cf' : 'rgba(234, 236, 239, .85)'
            ctx.textAlign = 'left'
            ctx.fillText(short(buy), mid + 5 * hr, top + h / 2)
          }
        }
      }
      this.drawNakedPOCs(ctx, ts, series, hr, vr, bitmapSize.width)
    })
  }

  /** A bar's POC is "naked" until a later bar's [low, high] range trades back through it; draw it as
   *  a dashed line out to that later bar (or the right edge, while still unfilled), newest first,
   *  limited to the last NPOC_LOOKBACK bars. */
  private drawNakedPOCs(ctx: CanvasRenderingContext2D, ts: ReturnType<IChartApi['timeScale']>, series: ISeriesApi<SeriesType>, hr: number, vr: number, widthPx: number) {
    const bars = this.calc.slice(-NPOC_LOOKBACK)
    ctx.lineWidth = Math.max(1, hr)
    ctx.setLineDash([4 * hr, 3 * hr])
    for (let i = 0; i < bars.length; i++) {
      const poc = bars[i].poc
      let filledAt = -1
      for (let j = i + 1; j < bars.length; j++) {
        if (poc >= bars[j].fb.low && poc <= bars[j].fb.high) { filledAt = j; break }
      }
      const y = series.priceToCoordinate(poc)
      const x0 = ts.timeToCoordinate(bars[i].time as Time)
      if (y == null || x0 == null) continue
      const x1 = filledAt >= 0 ? ts.timeToCoordinate(bars[filledAt].time as Time) : widthPx / hr
      if (x1 == null) continue
      ctx.strokeStyle = filledAt >= 0 ? 'rgba(245, 197, 66, .35)' : 'rgba(245, 197, 66, .8)'
      const yy = Math.round(y * vr)
      ctx.beginPath()
      ctx.moveTo(Math.round(x0 * hr), yy)
      ctx.lineTo(Math.round(x1 * hr), yy)
      ctx.stroke()
    }
    ctx.setLineDash([])
  }
}

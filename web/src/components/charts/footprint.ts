import type { IChartApi, ISeriesApi, ISeriesPrimitive, IPrimitivePaneView, IPrimitivePaneRenderer, SeriesAttachedParameter, Time, SeriesType } from 'lightweight-charts'
import type { CanvasRenderingTarget2D } from 'fancy-canvas'

/** [level low price, taker buy USDT, taker sell USDT] */
export type Level = [number, number, number]
export interface FootBar { levels: Level[]; step: number }
/** Every candle (not only footprint bars): decides whether a POC has been traded back through. */
export interface CandleRange { time: number; low: number; high: number }
export interface BarStats { poc: number; vah: number; val: number; step: number }

const short = (v: number) => (v >= 1e8 ? (v / 1e8).toFixed(1) + '亿' : v >= 1e4 ? (v / 1e4).toFixed(v >= 1e5 ? 0 : 1) + '万' : v.toFixed(0))

// Defaults follow the common footprint conventions (ATAS, Sierra Chart, flowsurface):
// diagonal imbalance at 3× above a per-bar volume floor, 3+ stacked levels, 70% value area,
// naked POC carried forward until a later candle trades through it.
const IMBALANCE_RATIO = 3
const STACK_MIN = 3
const VALUE_AREA = 0.7
const MIN_ROW_PX = 12 // regroup levels (×2, ×10, …) until a row is at least this tall
const MULTS = [1, 2, 10, 20, 100, 200, 1000] // each divides the next (same rule as the server)
const NAKED_MAX = 5 // only the most recent untested POCs: older ones turned the chart into a wall of lines

interface BarCalc {
  levels: Level[] // at the display step, ascending
  step: number
  poc: number // low price of the point-of-control level
  vah: number // top of the value area (inclusive level's high)
  val: number
  imbBuy: Set<number>
  imbSell: Set<number>
  stackBuy: Set<number>
  stackSell: Set<number>
}

const idx = (px: number, step: number) => Math.round(px / step)

function regroup(fb: FootBar, step: number): Level[] {
  if (step <= fb.step * (1 + 1e-9)) return fb.levels
  const m = new Map<number, Level>()
  for (const [px, b, s] of fb.levels) {
    const k = Math.floor(px / step + 1e-6)
    const l = m.get(k)
    if (l) { l[1] += b; l[2] += s } else m.set(k, [k * step, b, s])
  }
  return [...m.values()].sort((a, b) => a[0] - b[0])
}

function compute(levels: Level[], step: number): BarCalc {
  // POC: largest level total; ties → the level nearest the bar's volume-weighted centre
  const total = levels.reduce((a, [, b, s]) => a + b + s, 0)
  const centre = total ? levels.reduce((a, [px, b, s]) => a + px * (b + s), 0) / total : 0
  let pi = 0
  for (let i = 1; i < levels.length; i++) {
    const v = levels[i][1] + levels[i][2], best = levels[pi][1] + levels[pi][2]
    if (v > best || (v === best && Math.abs(levels[i][0] - centre) < Math.abs(levels[pi][0] - centre))) pi = i
  }
  // value area: grow from the POC, each time taking the larger adjacent level (ties go up)
  let lo = pi, hi = pi, acc = levels[pi][1] + levels[pi][2]
  while (acc < total * VALUE_AREA && (lo > 0 || hi < levels.length - 1)) {
    const up = hi < levels.length - 1 ? levels[hi + 1][1] + levels[hi + 1][2] : -1
    const dn = lo > 0 ? levels[lo - 1][1] + levels[lo - 1][2] : -1
    if (up >= dn) acc += levels[++hi][1] + levels[hi][2]
    else acc += levels[--lo][1] + levels[lo][2]
  }
  // diagonal imbalance: buy@P vs sell@P−1, sell@P vs buy@P+1; an empty opposite level inside the
  // bar counts as 0. A side must also reach the bar's median non-zero side volume (noise floor).
  const byI = new Map(levels.map(l => [idx(l[0], step), l]))
  const sides = levels.flatMap(([, b, s]) => [b, s]).filter(v => v > 0).sort((a, b) => a - b)
  const floor = sides[Math.floor(sides.length / 2)] ?? 0
  const iLo = idx(levels[0][0], step), iHi = idx(levels[levels.length - 1][0], step)
  const imbBuy = new Set<number>(), imbSell = new Set<number>()
  for (const [px, b, s] of levels) {
    const i = idx(px, step)
    if (i > iLo && b > 0 && b >= floor && b >= IMBALANCE_RATIO * (byI.get(i - 1)?.[2] ?? 0)) imbBuy.add(i)
    if (i < iHi && s > 0 && s >= floor && s >= IMBALANCE_RATIO * (byI.get(i + 1)?.[1] ?? 0)) imbSell.add(i)
  }
  return {
    levels, step, poc: levels[pi][0], val: levels[lo][0], vah: levels[hi][0] + step,
    imbBuy, imbSell, stackBuy: stacked(imbBuy), stackSell: stacked(imbSell),
  }
}

function stacked(xs: Set<number>): Set<number> {
  const out = new Set<number>()
  for (const i of xs) {
    if (xs.has(i - 1)) continue // start of a run
    let n = 0
    while (xs.has(i + n)) n++
    if (n >= STACK_MIN) for (let k = 0; k < n; k++) out.add(i + k)
  }
  return out
}

/**
 * Footprint (cluster) overlay for the candle series: per bar, per price level, taker sell on the
 * left half and taker buy on the right. Levels regroup automatically so rows stay readable at the
 * current vertical zoom. Colour intensity scales to the largest level in view (bars comparable).
 * Marks: POC (gold outline), value area (bar on the left edge), diagonal imbalance (tick on the
 * dominant side), stacked imbalance (outlined run), naked POC (dashed gold line to the right edge
 * until a later candle trades through it).
 */
export class Footprint implements ISeriesPrimitive<Time> {
  private bars: { time: number; fb: FootBar }[] = []
  private candles: CandleRange[] = []
  private cache = new Map<number, BarCalc>() // time → calc at `mult`
  private dispStep = 0 // level size drawn: the finest bar step × a MULTS entry
  private chart?: IChartApi
  private series?: ISeriesApi<SeriesType>
  private requestUpdate?: () => void
  private view: IPrimitivePaneView
  /** Called when the display level size changes (legend reads stats through barStats). */
  onRegroup?: () => void

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

  setData(bars: Map<number, FootBar>, candles: CandleRange[]) {
    this.bars = [...bars].filter(([, fb]) => fb.levels.length && fb.step > 0).sort((a, b) => a[0] - b[0]).map(([time, fb]) => ({ time, fb }))
    this.candles = candles
    this.cache.clear()
    this.requestUpdate?.()
  }

  private calc(time: number, fb: FootBar): BarCalc {
    let c = this.cache.get(time)
    if (!c) {
      const step = Math.max(this.dispStep, fb.step)
      c = compute(regroup(fb, step), step)
      this.cache.set(time, c)
    }
    return c
  }

  /** POC / value area / level size of the bar at `time`, at the level size currently drawn. */
  barStats(time: number): BarStats | null {
    const b = this.bars.find(x => x.time === time)
    if (!b) return null
    const c = this.calc(time, b.fb)
    return { poc: c.poc, vah: c.vah, val: c.val, step: c.step }
  }

  /** Smallest regrouping of the finest bar step whose row is ≥ MIN_ROW_PX tall. */
  private pickStep(series: ISeriesApi<SeriesType>): number {
    const step = Math.min(...this.bars.map(b => b.fb.step))
    const p = this.bars[this.bars.length - 1].fb.levels[0][0]
    const y0 = series.priceToCoordinate(p), y1 = series.priceToCoordinate(p + step)
    if (y0 == null || y1 == null) return this.dispStep || step
    const px = Math.abs(y0 - y1)
    return step * (MULTS.find(m => m * px >= MIN_ROW_PX) ?? MULTS[MULTS.length - 1])
  }

  private draw(target: CanvasRenderingTarget2D) {
    const chart = this.chart, series = this.series
    if (!chart || !series || !this.bars.length) return
    const ts = chart.timeScale()
    const w = ts.options().barSpacing * 0.9
    if (w < 6) return

    const step = this.pickStep(series)
    if (step !== this.dispStep) {
      this.dispStep = step
      this.cache.clear()
      queueMicrotask(() => this.onRegroup?.())
    }

    const range = ts.getVisibleRange()
    const inView = range ? this.bars.filter(b => b.time >= (range.from as number) && b.time <= (range.to as number)) : []
    if (!inView.length) {
      target.useBitmapCoordinateSpace(s => this.drawNakedPOCs(s.context, ts, series, s.horizontalPixelRatio, s.verticalPixelRatio, s.bitmapSize.width))
      return
    }
    let scaleMax = 0
    for (const b of inView) for (const [, x, y] of this.calc(b.time, b.fb).levels) scaleMax = Math.max(scaleMax, x, y)
    if (scaleMax <= 0) return
    const alpha = (v: number) => 0.08 + 0.72 * Math.sqrt(Math.min(1, v / scaleMax)) // sqrt: small levels stay visible

    target.useBitmapCoordinateSpace(({ context: ctx, horizontalPixelRatio: hr, verticalPixelRatio: vr, bitmapSize }) => {
      ctx.font = `${Math.round(10 * vr)}px ui-monospace, SFMono-Regular, Menlo, monospace`
      ctx.textBaseline = 'middle'
      for (const { time, fb } of inView) {
        const x = ts.timeToCoordinate(time as Time)
        if (x == null) continue
        const c = this.calc(time, fb)
        const left = Math.round((x - w / 2) * hr), mid = Math.round(x * hr), right = Math.round((x + w / 2) * hr)
        const showText = w >= 58
        for (const [px, buy, sell] of c.levels) {
          const yTop = series.priceToCoordinate(px + c.step), yBot = series.priceToCoordinate(px)
          if (yTop == null || yBot == null) continue
          const top = Math.round(Math.min(yTop, yBot) * vr), bot = Math.round(Math.max(yTop, yBot) * vr)
          if (bot < 0 || top > bitmapSize.height) continue
          const h = Math.max(1, bot - top - (bot - top > 3 ? 1 : 0)) // 1px gap between rows when there is room
          ctx.fillStyle = `rgba(246,70,93,${alpha(sell)})`
          ctx.fillRect(left, top, mid - left - 1, h)
          ctx.fillStyle = `rgba(14,203,129,${alpha(buy)})`
          ctx.fillRect(mid, top, right - mid, h)

          const i = idx(px, c.step)
          const sb = c.stackBuy.has(i), ss = c.stackSell.has(i)
          if (sb || ss) { // stacked imbalance: outline the dominant half
            ctx.strokeStyle = sb ? '#0ecb81' : '#f6465d'
            ctx.lineWidth = Math.max(1, Math.round(1.5 * hr))
            if (sb) ctx.strokeRect(mid + 0.5, top + 0.5, right - mid - 1, h - 1)
            if (ss) ctx.strokeRect(left + 0.5, top + 0.5, mid - left - 2, h - 1)
          }
          const tick = Math.max(2, Math.round(2 * hr))
          if (c.imbBuy.has(i)) { ctx.fillStyle = '#0ecb81'; ctx.fillRect(right - tick, top, tick, h) }
          if (c.imbSell.has(i)) { ctx.fillStyle = '#f6465d'; ctx.fillRect(left, top, tick, h) }
          if (px === c.poc) {
            ctx.strokeStyle = '#f5c542'
            ctx.lineWidth = Math.max(1, Math.round(hr))
            ctx.strokeRect(left + 0.5, top + 0.5, right - left - 1, h - 1)
          }
          if (showText && h >= 11 * vr) {
            ctx.textAlign = 'right'
            ctx.fillStyle = c.imbSell.has(i) ? '#ffb4bf' : 'rgba(234,236,239,.85)'
            ctx.fillText(short(sell), mid - 4 * hr, top + h / 2)
            ctx.textAlign = 'left'
            ctx.fillStyle = c.imbBuy.has(i) ? '#9ff0cf' : 'rgba(234,236,239,.85)'
            ctx.fillText(short(buy), mid + 4 * hr, top + h / 2)
          }
        }
        // value area: a thin bar just left of the cells
        const ya = series.priceToCoordinate(c.vah), yb = series.priceToCoordinate(c.val)
        if (ya != null && yb != null) {
          ctx.fillStyle = 'rgba(245,197,66,.55)'
          ctx.fillRect(left - Math.round(3 * hr), Math.round(ya * vr), Math.max(1, Math.round(1.5 * hr)), Math.round((yb - ya) * vr))
        }
      }
      this.drawNakedPOCs(ctx, ts, series, hr, vr, bitmapSize.width)
    })
  }

  /** Naked POC: a closed bar's POC that no later candle's [low, high] has traded through yet —
   *  drawn as a dashed line from that bar to the right edge. The forming (last) bar is skipped;
   *  only the NAKED_MAX most recent are drawn. */
  private drawNakedPOCs(ctx: CanvasRenderingContext2D, ts: ReturnType<IChartApi['timeScale']>, series: ISeriesApi<SeriesType>, hr: number, vr: number, widthPx: number) {
    const cs = this.candles
    if (!cs.length) return
    const pos = new Map(cs.map((c, i) => [c.time, i]))
    ctx.lineWidth = Math.max(1, Math.round(hr))
    ctx.setLineDash([4 * hr, 3 * hr])
    ctx.strokeStyle = 'rgba(245,197,66,.85)'
    const lastTime = cs[cs.length - 1].time
    let drawn = 0
    for (let k = this.bars.length - 1; k >= 0 && drawn < NAKED_MAX; k--) {
      const { time, fb } = this.bars[k]
      if (time >= lastTime) continue
      const c = this.calc(time, fb)
      const p = c.poc + c.step / 2
      const start = pos.get(time)
      if (start == null) continue
      let filled = false
      for (let j = start + 1; j < cs.length && !filled; j++) filled = cs[j].low <= p && cs[j].high >= p
      if (filled) continue
      const y = series.priceToCoordinate(p), x0 = ts.timeToCoordinate(time as Time)
      if (y == null) continue
      const yy = Math.round(y * vr) + 0.5
      ctx.beginPath()
      ctx.moveTo(x0 == null ? 0 : Math.round(x0 * hr), yy)
      ctx.lineTo(widthPx, yy)
      ctx.stroke()
      drawn++
    }
    ctx.setLineDash([])
  }
}

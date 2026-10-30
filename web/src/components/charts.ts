import { C } from '../theme'
import { createChart, ColorType, type DeepPartial, type ChartOptions, type IChartApi } from 'lightweight-charts'

export function baseChart(el: HTMLElement, extra: DeepPartial<ChartOptions> = {}) {
  const chart = createChart(el, {
    autoSize: true,
    layout: { background: { type: ColorType.Solid, color: C.bg }, textColor: C.text, fontSize: 11, attributionLogo: true },
    grid: { vertLines: { color: C.grid }, horzLines: { color: C.grid } },
    rightPriceScale: { borderColor: C.border },
    leftPriceScale: { borderColor: C.border },
    timeScale: { borderColor: C.border, timeVisible: true, secondsVisible: false, minBarSpacing: 0.2 },
    crosshair: { mode: 0 },
    ...extra,
    // the built-in pinch is replaced by naturalPinch below
    handleScale: { pinch: false, ...(typeof extra.handleScale === 'object' ? extra.handleScale : {}) },
  })
  naturalPinch(chart, el)
  return chart
}

/**
 * Two-finger zoom on the time axis that follows the fingers: the number of bars in view scales with
 * 1 / finger distance and the bar under the pinch midpoint stays under it (also when the fingers move,
 * so a pinch can pan at the same time). The library's own pinch grows bar spacing by
 * (Δscale × 5) / 10 per event, which made zooming out need many pinches.
 */
function naturalPinch(chart: IChartApi, el: HTMLElement) {
  let start: { dist: number; from: number; to: number; anchor: number; frac: number } | null = null
  const dist = (t: TouchList) => Math.hypot(t[0].clientX - t[1].clientX, t[0].clientY - t[1].clientY)
  const midX = (t: TouchList) => (t[0].clientX + t[1].clientX) / 2 - el.getBoundingClientRect().left
  const begin = (e: TouchEvent) => {
    const lr = chart.timeScale().getVisibleLogicalRange()
    const w = chart.timeScale().width()
    if (e.touches.length !== 2 || !lr || !w) { start = null; return }
    const frac = Math.max(0, Math.min(1, midX(e.touches) / w))
    start = { dist: Math.max(1, dist(e.touches)), from: lr.from, to: lr.to, frac, anchor: lr.from + (lr.to - lr.from) * frac }
  }
  el.addEventListener('touchstart', begin, { passive: true, capture: true })
  el.addEventListener('touchend', begin, { passive: true, capture: true })
  el.addEventListener('touchcancel', () => { start = null }, { passive: true, capture: true })
  el.addEventListener('touchmove', e => {
    if (!start || e.touches.length !== 2) return
    const w = chart.timeScale().width()
    const ratio = start.dist / Math.max(1, dist(e.touches)) // > 1: fingers closer → more bars
    const minBars = Math.max(2, w / 80), maxBars = w / (chart.timeScale().options().minBarSpacing || 0.2)
    const width = Math.max(minBars, Math.min(maxBars, (start.to - start.from) * ratio))
    const frac = Math.max(0, Math.min(1, midX(e.touches) / w))
    const from = start.anchor - width * frac
    chart.timeScale().setVisibleLogicalRange({ from, to: from + width })
  }, { passive: true, capture: true })
}

export function precisionFor(px: number) {
  const a = Math.abs(px)
  const d = a >= 1000 ? 1 : a >= 10 ? 3 : a >= 1 ? 4 : a >= 0.01 ? 5 : 8
  return { type: 'price' as const, precision: d, minMove: Math.pow(10, -d) }
}

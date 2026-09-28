import { C } from '../theme'
import { createChart, ColorType, type DeepPartial, type ChartOptions } from 'lightweight-charts'

export function baseChart(el: HTMLElement, extra: DeepPartial<ChartOptions> = {}) {
  return createChart(el, {
    autoSize: true,
    layout: { background: { type: ColorType.Solid, color: C.bg }, textColor: C.text, fontSize: 11, attributionLogo: true },
    grid: { vertLines: { color: C.grid }, horzLines: { color: C.grid } },
    rightPriceScale: { borderColor: C.border },
    leftPriceScale: { borderColor: C.border },
    timeScale: { borderColor: C.border, timeVisible: true, secondsVisible: false },
    crosshair: { mode: 0 },
    ...extra,
  })
}

export function precisionFor(px: number) {
  const a = Math.abs(px)
  const d = a >= 1000 ? 1 : a >= 10 ? 3 : a >= 1 ? 4 : a >= 0.01 ? 5 : 8
  return { type: 'price' as const, precision: d, minMove: Math.pow(10, -d) }
}

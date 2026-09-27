import { createChart, ColorType, type DeepPartial, type ChartOptions } from 'lightweight-charts'

export function baseChart(el: HTMLElement, extra: DeepPartial<ChartOptions> = {}) {
  return createChart(el, {
    autoSize: true,
    layout: { background: { type: ColorType.Solid, color: '#161920' }, textColor: '#8a93a3', fontSize: 11, attributionLogo: true },
    grid: { vertLines: { color: '#20242d' }, horzLines: { color: '#20242d' } },
    rightPriceScale: { borderColor: '#2a2f3a' },
    leftPriceScale: { borderColor: '#2a2f3a' },
    timeScale: { borderColor: '#2a2f3a', timeVisible: true, secondsVisible: false },
    crosshair: { mode: 0 },
    ...extra,
  })
}

export function precisionFor(px: number) {
  const a = Math.abs(px)
  const d = a >= 1000 ? 1 : a >= 10 ? 3 : a >= 1 ? 4 : a >= 0.01 ? 5 : 8
  return { type: 'price' as const, precision: d, minMove: Math.pow(10, -d) }
}

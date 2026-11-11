import { ref, watch } from 'vue'
import { LineStyle, type IPriceLine, type ISeriesApi } from 'lightweight-charts'
import { C } from '../../theme'
import { signalsApi, type SignalSub } from '../../realtradeApi'

/**
 * One agent signal subscription on the K-line (server: internal/signals), opened from the app's signal
 * card (/embed/chart/<id>?signal=<n>): its price and zone levels on this instrument as dotted violet
 * lines. Owner only: without the key nothing is drawn.
 */
export function useSignalLevels(series: () => ISeriesApi<'Candlestick'> | undefined, id: () => string, signalId: () => number | undefined) {
  const signal = ref<SignalSub | null>(null)
  let lines: IPriceLine[] = []

  async function refresh() {
    const sid = signalId()
    if (!sid) { signal.value = null; draw(); return }
    try {
      const s = await signalsApi.get(sid)
      if (sid !== signalId()) return
      signal.value = s && s.instIds.includes(id()) ? s : null
    } catch { /* keep what is drawn */ }
    draw()
  }

  function draw() {
    const s = series()
    if (!s) return
    for (const l of lines) s.removePriceLine(l)
    lines = []
    for (const l of mine()) {
      lines.push(s.createPriceLine({ price: l.px, color: C.violet, lineWidth: 2, lineStyle: LineStyle.Dotted, axisLabelVisible: true, title: `信号 ${l.label}` }))
    }
  }

  const mine = () => (signal.value?.levels ?? []).filter(l => l.instId === id())
  /** Levels the price scale should span (spanLevels). */
  const levels = () => mine().map(l => l.px)

  watch([signalId, id], refresh)
  return { signal, refresh, draw, levels }
}

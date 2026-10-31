import { ref, watch, type Ref } from 'vue'
import { createSeriesMarkers, LineStyle, type AutoscaleInfo, type IPriceLine, type ISeriesApi, type ISeriesMarkersPluginApi, type Time } from 'lightweight-charts'
import { chartTime } from '../../fmt'
import { C } from '../../theme'

/**
 * The AI's trade plans on the K-line (server: internal/aigate/plans.go). Only open plans are drawn —
 * entry zone, stop, TP1/TP2 as price lines and an "AI 空/多" marker where the plan was made; the
 * server follows each plan on 1m candles and closes it on stop, target, a stop break before entry,
 * TP1 before entry, its holding time running out, or a newer plan for the coin, so it disappears
 * here on the next refresh.
 */
export interface AiPlan {
  id: number; chatId: number; created: number; instId: string; side: 'long' | 'short'
  entryLo: number; entryHi: number; stop: number; tp1: number; tp2?: number; leverage?: number
  hold?: string; invalidate?: string; confidence?: string; expires: number
  status: 'pending' | 'active' | 'tp1'; statusAt?: number; enteredAt?: number
}

export const planStatusText: Record<string, string> = { pending: '等待入场', active: '已入场', tp1: '已到 TP1' }

const HIDE_KEY = 'ctg.aiPlansHidden'
const hidden = (): number[] => { try { return JSON.parse(localStorage.getItem(HIDE_KEY) || '[]') } catch { return [] } }

export function useAiPlans(series: () => ISeriesApi<'Candlestick'> | undefined, id: () => string, barMs: () => number, on: Ref<boolean>) {
  const plans = ref<AiPlan[]>([])
  let lines: IPriceLine[] = []
  let markers: ISeriesMarkersPluginApi<Time> | undefined
  let markersOf: ISeriesApi<'Candlestick'> | undefined

  async function refresh() {
    if (!on.value) { plans.value = []; draw(); return }
    const inst = id()
    try {
      const r = await fetch(`/api/v1/ai/plans?instId=${encodeURIComponent(inst)}`)
      if (!r.ok || inst !== id()) return
      const hide = hidden()
      plans.value = ((await r.json()) as AiPlan[]).filter(p => !hide.includes(p.id))
    } catch { /* keep what is drawn */ }
    draw()
  }

  function draw() {
    const s = series()
    if (!s) return
    for (const l of lines) s.removePriceLine(l)
    lines = []
    if (markersOf !== s) {
      markers = createSeriesMarkers(s, [])
      markersOf = s
      // keep the plan in view: the price scale also spans the entry zone, stop and TP1 (TP2 is often far
      // enough away to squash the candles, so it only shows when the candles reach it)
      s.applyOptions({
        autoscaleInfoProvider: (orig: () => AutoscaleInfo | null) => {
          const r = orig()
          const lv = plans.value.flatMap(p => [p.entryLo, p.entryHi, p.stop, p.status === 'tp1' && p.tp2 ? p.tp2 : p.tp1])
          if (!r || !r.priceRange || !lv.length) return r
          return { ...r, priceRange: { minValue: Math.min(r.priceRange.minValue, ...lv), maxValue: Math.max(r.priceRange.maxValue, ...lv) } }
        },
      })
    }
    const ms: { time: Time; position: 'aboveBar' | 'belowBar'; color: string; shape: 'arrowDown' | 'arrowUp'; text: string }[] = []
    for (const p of plans.value) {
      const short = p.side === 'short', tag = `AI${short ? '空' : '多'}`
      const line = (price: number, color: string, title: string, style = LineStyle.Dashed, width: 1 | 2 = 1) =>
        lines.push(s.createPriceLine({ price, color, lineWidth: width, lineStyle: style, axisLabelVisible: true, title }))
      const entryColor = p.status === 'pending' ? C.lime : 'rgba(188,255,47,.45)'
      if (p.entryHi !== p.entryLo) {
        line(p.entryHi, entryColor, `${tag} 入场`, p.status === 'pending' ? LineStyle.Dashed : LineStyle.Dotted)
        line(p.entryLo, entryColor, '', p.status === 'pending' ? LineStyle.Dashed : LineStyle.Dotted)
      } else line(p.entryLo, entryColor, `${tag} 入场`)
      line(p.stop, C.down, `${tag} 止损`, LineStyle.Solid, 2)
      if (p.status !== 'tp1') line(p.tp1, C.up, `${tag} TP1`)
      if (p.tp2) line(p.tp2, C.up, `${tag} TP2`)
      const b = barMs()
      ms.push({ time: chartTime(Math.floor(p.created / b) * b), position: short ? 'aboveBar' : 'belowBar', color: C.lime,
        shape: short ? 'arrowDown' : 'arrowUp', text: tag })
    }
    markers?.setMarkers(ms.sort((a, b) => (a.time as number) - (b.time as number)))
  }

  /** Hide a plan here (and close it on the server when the owner key is at hand). */
  async function dismiss(p: AiPlan, key?: string) {
    localStorage.setItem(HIDE_KEY, JSON.stringify([...hidden(), p.id].slice(-200)))
    plans.value = plans.value.filter(x => x.id !== p.id)
    draw()
    if (key) fetch(`/api/v1/ai/plans/${p.id}`, { method: 'DELETE', headers: { 'X-CTG-Admin': key } }).catch(() => {})
  }

  watch(on, v => { localStorage.setItem('ctg.chartAiPlans', v ? '1' : '0'); refresh() })
  return { plans, refresh, draw, dismiss }
}

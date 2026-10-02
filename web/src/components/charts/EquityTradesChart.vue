<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { AreaSeries, LineSeries, TrackingModeExitMode, type IChartApi, type ISeriesApi, type IPriceLine, type Logical, type MouseEventParams, type Time } from 'lightweight-charts'
import { baseChart } from '../charts'
import { C, alpha } from '../../theme'
import { chartTime, time } from '../../fmt'
import type { Trade } from '../../tradeApi'
import { EquityTradeMarks } from './tradeMarks'
import { amt, pct, tone } from '../trade/meta'

/**
 * Account equity (USDT) with trade bubbles (tradeMarks.ts), the deposit as a dashed baseline and a
 * drawdown band below. The legend at the top-left follows the crosshair (PC hover; phones: long-press
 * then drag) with time, equity, PnL vs deposit, drawdown and open positions at that moment.
 * Events: hover(ids, x, y) when the pointer is over a bubble or cluster (empty = none), pick(ids) on click/tap.
 */
const props = withDefaults(defineProps<{
  points: [number, number][]; deposit: number; trades: Trade[]; hoverId?: number; selectedId?: number
  dimmed?: Set<number> | null; height?: number; small?: boolean
}>(), { height: 360, hoverId: 0, selectedId: 0, dimmed: null })
const emit = defineEmits<{ hover: [ids: number[], x: number, y: number]; pick: [ids: number[]] }>()

const el = ref<HTMLElement>()
let chart: IChartApi | undefined
let eq: ISeriesApi<'Line'> | undefined
let dd: ISeriesApi<'Area'> | undefined
let base: IPriceLine | undefined
const marks = new EquityTradeMarks()
let fitted = false

// ---- data (deduped, strictly increasing chart seconds) ----
const data = computed(() => {
  const ts: number[] = [], ms: number[] = [], vs: number[] = [], dds: number[] = []
  let last = -Infinity, peak = -Infinity
  for (const [t, v] of props.points) {
    const s = chartTime(t)
    if (!(s > last) || !isFinite(v)) continue
    last = s; peak = Math.max(peak, v)
    ts.push(s); ms.push(t); vs.push(v); dds.push(peak > 0 ? v / peak - 1 : 0)
  }
  return { ts, ms, vs, dds }
})

// ---- crosshair legend ----
const cur = ref<number | null>(null) // index into data under the crosshair (null = latest)
const legend = computed(() => {
  const d = data.value
  const i = cur.value ?? d.vs.length - 1
  if (i < 0 || !d.vs.length) return null
  const v = d.vs[i], t = d.ms[i]
  const open = props.trades.filter(x => x.openTs <= t && (!x.closeTs || x.closeTs > t)).length
  return { t, v, pnl: v - props.deposit, pnlPct: props.deposit ? v / props.deposit - 1 : null, dd: d.dds[i], open, live: cur.value == null }
})

function render() {
  if (!chart || !eq) return
  const d = data.value
  eq.setData(d.ts.map((t, i) => ({ time: t as Time, value: d.vs[i] })))
  dd?.setData(d.ts.map((t, i) => ({ time: t as Time, value: d.dds[i] * 100 })))
  if (base) { eq.removePriceLine(base); base = undefined }
  if (props.deposit) base = eq.createPriceLine({ price: props.deposit, color: C.text, lineWidth: 1, lineStyle: 2, axisLabelVisible: true, title: '入金' })
  marks.setData(d.ts, d.vs, props.trades, chartTime)
  if (!fitted && d.ts.length) { chart.timeScale().fitContent(); fitted = true }
}

let hoverKey = ''
function onMove(p: MouseEventParams<Time>) {
  const d = data.value
  if (p.time == null || !p.point) { cur.value = null } else {
    let lo = 0, hi = d.ts.length - 1, r = -1
    const t = p.time as number
    while (lo <= hi) { const m = (lo + hi) >> 1; if (d.ts[m] <= t) { r = m; lo = m + 1 } else hi = m - 1 }
    cur.value = r < 0 ? 0 : r
  }
  const h = p.point ? marks.hit(p.point.x, p.point.y) : null
  if (el.value) el.value.style.cursor = h ? 'pointer' : ''
  const key = h ? h.ids.join(',') : ''
  if (key !== hoverKey) { hoverKey = key; emit('hover', h ? h.ids : [], h?.x ?? 0, h?.y ?? 0) }
}
function onClick(p: MouseEventParams<Time>) {
  const h = p.point ? marks.hit(p.point.x, p.point.y) : null
  if (h) emit('pick', h.ids)
}

/** Zoom the time axis to [fromMs, toMs] with some padding (cluster click). */
function zoomTo(fromMs: number, toMs: number) {
  if (!chart) return
  const d = data.value
  const lo = idxLogical(chartTime(fromMs)), hi = idxLogical(chartTime(toMs))
  const pad = Math.max(8, (hi - lo) * 0.35)
  chart.timeScale().setVisibleLogicalRange({ from: (lo - pad) as Logical, to: (hi + pad) as Logical })
  void d
}
function idxLogical(t: number) {
  const xs = data.value.ts
  let lo = 0, hi = xs.length - 1, r = 0
  while (lo <= hi) { const m = (lo + hi) >> 1; if (xs[m] <= t) { r = m; lo = m + 1 } else hi = m - 1 }
  return r
}
function fit() { chart?.timeScale().fitContent() }
defineExpose({ zoomTo, fit })

onMounted(() => {
  chart = baseChart(el.value!, {
    rightPriceScale: { borderColor: C.border, scaleMargins: { top: 0.16, bottom: 0.1 } },
    trackingMode: { exitMode: TrackingModeExitMode.OnTouchEnd },
    crosshair: { mode: 0, vertLine: { color: alpha('ink', 0.35), labelBackgroundColor: C.label }, horzLine: { color: alpha('ink', 0.2), labelBackgroundColor: C.label } },
  })
  eq = chart.addSeries(LineSeries, { color: C.lime, lineWidth: 2, priceFormat: { type: 'price', precision: 2, minMove: 0.01 }, lastValueVisible: true, priceLineVisible: false, crosshairMarkerRadius: 4 })
  dd = chart.addSeries(AreaSeries, {
    lineColor: C.down, topColor: alpha('down', 0.05), bottomColor: alpha('down', 0.35), lineWidth: 1, invertFilledArea: true,
    priceFormat: { type: 'price', precision: 1, minMove: 0.1 }, title: '回撤%', lastValueVisible: false, priceLineVisible: false, crosshairMarkerVisible: false,
  }, 1)
  chart.panes()[1]?.setStretchFactor(0.18)
  marks.setSmall(!!props.small)
  eq.attachPrimitive(marks)
  ;(el.value as any).__hits = () => marks.hitList() // UI tests (scripts/shot-report.mjs)
  chart.subscribeCrosshairMove(onMove)
  chart.subscribeClick(onClick)
  render()
})
onUnmounted(() => { chart?.unsubscribeCrosshairMove(onMove); chart?.unsubscribeClick(onClick); chart?.remove() })
watch(() => [props.points, props.deposit, props.trades], render)
watch(() => [props.hoverId, props.selectedId], () => marks.setHighlight(props.hoverId, props.selectedId))
watch(() => props.dimmed, d => marks.setDimmed(d ?? null))
</script>

<template>
  <div class="etc" :style="{ height: height + 'px' }">
    <div ref="el" class="cv"></div>
    <div v-if="legend" class="lg" :class="{ live: legend.live }">
      <span class="tm tnum">{{ time(legend.t, true) }}</span>
      <span class="it"><i>权益</i><b class="tnum">{{ amt(legend.v) }}</b></span>
      <span class="it"><i>盈亏</i><b class="tnum" :class="tone(legend.pnl)">{{ amt(legend.pnl, 2, true) }}<small v-if="legend.pnlPct != null"> {{ pct(legend.pnlPct, 2, true) }}</small></b></span>
      <span class="it"><i>回撤</i><b class="tnum" :class="legend.dd < 0 ? 'down' : ''">{{ pct(legend.dd, 2) }}</b></span>
      <span class="it"><i>持仓</i><b class="tnum">{{ legend.open }}</b></span>
    </div>
    <slot />
  </div>
</template>

<style scoped>
.etc { position: relative; width: 100%; min-width: 0; }
.cv { position: absolute; inset: 0; }
.lg {
  position: absolute; left: 8px; top: 6px; z-index: 3; display: flex; flex-wrap: wrap; align-items: baseline; gap: 2px 12px; max-width: calc(100% - 80px);
  padding: 4px 8px; border-radius: 6px; background: rgba(14, 14, 14, .82); font-size: var(--fs-xs); color: var(--text-3); pointer-events: none;
}
.lg .tm { color: var(--text-2); }
.lg.live .tm { color: var(--text-3); }
.it { display: inline-flex; align-items: baseline; gap: 4px; white-space: nowrap; }
.it i { font-style: normal; }
.it b { color: var(--text-1); font-weight: 600; font-size: var(--fs-sm); }
.it b small { font-size: var(--fs-xs); font-weight: 500; }
.it b.up { color: var(--up); } .it b.down { color: var(--down); }
</style>

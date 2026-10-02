<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { CandlestickSeries, TrackingModeExitMode, type IChartApi, type ISeriesApi, type IPriceLine, type Logical, type MouseEventParams, type Time } from 'lightweight-charts'
import { baseChart, precisionFor } from '../charts'
import { C, alpha } from '../../theme'
import { chartTime, price, time } from '../../fmt'
import type { AccountCandles, Trade } from '../../tradeApi'
import { CandleTradeMarks } from './tradeMarks'
import ResetViewButton from './ResetViewButton.vue'

/**
 * One instrument's candles over the account period with every fill of its trades (tradeMarks.ts),
 * holding spans shaded by result, and for the selected trade its entry / stop / target as price
 * lines. focus(trade) zooms the time axis to the trade with padding.
 */
const props = withDefaults(defineProps<{
  candles: AccountCandles | null; trades: Trade[]; hoverId?: number; selectedId?: number; dimmed?: Set<number> | null; height?: number; small?: boolean
}>(), { height: 360, hoverId: 0, selectedId: 0, dimmed: null })
const emit = defineEmits<{ hover: [ids: number[], x: number, y: number]; pick: [ids: number[]] }>()

const el = ref<HTMLElement>()
let chart: IChartApi | undefined
let cs: ISeriesApi<'Candlestick'> | undefined
const marks = new CandleTradeMarks()
let lines: IPriceLine[] = []

const rows = computed(() => {
  const out: { time: Time; open: number; high: number; low: number; close: number }[] = []
  let last = -Infinity
  for (const r of props.candles?.rows || []) {
    const s = chartTime(r[0])
    if (!(s > last)) continue
    last = s
    out.push({ time: s as Time, open: r[1], high: r[2], low: r[3], close: r[4] })
  }
  return out
})
const ohlc = ref<{ t: number; o: number; h: number; l: number; c: number } | null>(null)
const legend = computed(() => {
  if (ohlc.value) return ohlc.value
  const r = props.candles?.rows, x = r?.[r.length - 1]
  return x ? { t: x[0], o: x[1], h: x[2], l: x[3], c: x[4] } : null
})

let shownKey = ''
function render() {
  if (!chart || !cs) return
  const rs = rows.value
  cs.setData(rs)
  const px = rs[rs.length - 1]?.close
  if (px) cs.applyOptions({ priceFormat: precisionFor(px) })
  marks.setData(rs.map(r => r.time as number), props.trades, chartTime)
  setLines()
  // another instrument or bar size: drop the previous instrument's zoom / scroll / price scale
  const key = props.candles ? `${props.candles.instId}|${props.candles.bar}` : ''
  if (key && key !== shownKey) { shownKey = key; resetView() }
  requestAnimationFrame(() => { if (chart) axisW.value = chart.priceScale('right').width() })
}
const axisW = ref(60)
/** Default view: the selected trade with padding, else the whole period; prices auto-scaled. */
function resetView() {
  if (!chart) return
  chart.priceScale('right').applyOptions({ autoScale: true })
  const t = props.trades.find(x => x.id === props.selectedId)
  if (t) focus(t)
  else chart.timeScale().fitContent()
}
function setLines() {
  if (!cs) return
  for (const l of lines) cs.removePriceLine(l)
  lines = []
  const t = props.trades.find(x => x.id === props.selectedId)
  // the last-price tag would collide with the trade's tags: hide it while a trade is selected
  cs.applyOptions({ lastValueVisible: !t })
  if (!t) return
  // axis labels in priority order; a label within 16 px of a kept one is dropped (its line stays)
  const want = [
    { p: t.stopLoss, color: C.down, title: '止损', style: 2 },
    { p: t.takeProfit, color: C.up, title: '止盈', style: 2 },
    { p: t.entryPx, color: alpha('ink', 0.6), title: '开仓', style: 1 },
    { p: t.exitPx, color: alpha('lime', 0.7), title: '平仓', style: 1 },
  ].filter(x => x.p)
  const kept: number[] = []
  for (const x of want) {
    const y = cs.priceToCoordinate(x.p!)
    const show = y == null || kept.every(k => Math.abs(k - y) >= 16)
    if (show && y != null) kept.push(y)
    lines.push(cs.createPriceLine({ price: x.p!, color: x.color, lineWidth: 1, lineStyle: x.style, axisLabelVisible: show, title: show ? x.title : '' }))
  }
}

/** Zoom to a trade with padding on both sides (≥ 12 bars, 60% of its length). */
function focus(t: Trade) {
  if (!chart) return
  chart.priceScale('right').applyOptions({ autoScale: true })
  const [a, b] = marks.span(t)
  const pad = Math.max(12, (b - a) * 0.6)
  chart.timeScale().setVisibleLogicalRange({ from: (a - pad) as Logical, to: (b + pad) as Logical })
  // label collisions depend on the price scale after the zoom: recompute once it has settled
  setTimeout(setLines, 120)
}
function fit() { chart?.priceScale('right').applyOptions({ autoScale: true }); chart?.timeScale().fitContent() }
defineExpose({ focus, fit, resetView })

let hoverKey = ''
function onMove(p: MouseEventParams<Time>) {
  const d = p.seriesData && cs ? (p.seriesData.get(cs) as { open: number; high: number; low: number; close: number } | undefined) : undefined
  const i = p.time != null ? rows.value.findIndex(r => r.time === p.time) : -1
  ohlc.value = d && i >= 0 ? { t: props.candles!.rows[Math.min(i, props.candles!.rows.length - 1)][0], o: d.open, h: d.high, l: d.low, c: d.close } : null
  const h = p.point ? marks.hit(p.point.x, p.point.y) : null
  if (el.value) el.value.style.cursor = h ? 'pointer' : ''
  const key = h ? h.ids.join(',') : ''
  if (key !== hoverKey) { hoverKey = key; emit('hover', h ? h.ids : [], h?.x ?? 0, h?.y ?? 0) }
}
function onClick(p: MouseEventParams<Time>) {
  const h = p.point ? marks.hit(p.point.x, p.point.y) : null
  emit('pick', h ? h.ids : []) // blank chart area clears the selection
}

onMounted(() => {
  chart = baseChart(el.value!, {
    rightPriceScale: { borderColor: C.border, scaleMargins: { top: 0.12, bottom: 0.08 } },
    trackingMode: { exitMode: TrackingModeExitMode.OnTouchEnd },
    // fitContent must be able to show the whole period even on a 330 px phone plot (default min 0.5 px/bar)
    timeScale: { borderColor: C.border, timeVisible: true, secondsVisible: false, minBarSpacing: 0.05 },
    crosshair: { mode: 0, vertLine: { color: alpha('ink', 0.35), labelBackgroundColor: C.label }, horzLine: { color: alpha('ink', 0.2), labelBackgroundColor: C.label } },
  })
  cs = chart.addSeries(CandlestickSeries, { upColor: C.up, downColor: C.down, borderVisible: false, wickUpColor: C.up, wickDownColor: C.down, priceLineVisible: false })
  marks.setSmall(!!props.small)
  cs.attachPrimitive(marks)
  ;(el.value as any).__hits = () => marks.hitList() // UI tests (scripts/shot-report.mjs)
  chart.subscribeCrosshairMove(onMove)
  chart.subscribeClick(onClick)
  render()
  chart.timeScale().fitContent()
})
onUnmounted(() => { chart?.unsubscribeCrosshairMove(onMove); chart?.unsubscribeClick(onClick); chart?.remove() })
watch(() => [props.candles, props.trades], render)
watch(() => props.selectedId, setLines)
watch(() => [props.hoverId, props.selectedId], () => marks.setHighlight(props.hoverId, props.selectedId))
watch(() => props.dimmed, d => marks.setDimmed(d ?? null))
</script>

<template>
  <div class="tcc" :class="{ small }" :style="{ height: height + 'px' }">
    <div v-if="legend" class="lg">
      <span class="tm tnum">{{ time(legend.t, true) }}<small v-if="candles"> · {{ candles.bar }}</small></span>
      <span class="it"><i>开</i><b class="tnum">{{ price(legend.o) }}</b></span>
      <span class="it"><i>高</i><b class="tnum">{{ price(legend.h) }}</b></span>
      <span class="it"><i>低</i><b class="tnum">{{ price(legend.l) }}</b></span>
      <span class="it"><i>收</i><b class="tnum" :class="legend.c >= legend.o ? 'up' : 'down'">{{ price(legend.c) }}</b></span>
    </div>
    <div class="cvw"><div ref="el" class="cv"></div><ResetViewButton :right="axisW + 6" :bottom="34" title="复位：所选交易或整个区间、自动缩放" @reset="resetView" /></div>
    <slot />
  </div>
</template>

<style scoped>
.tcc { position: relative; width: 100%; min-width: 0; display: flex; flex-direction: column; }
.cvw { position: relative; flex: 1; min-height: 0; }
.cv { position: absolute; inset: 0; }
.small .lg { position: static; max-width: none; margin: 0 0 4px; padding: 2px 4px; background: none; gap: 0 10px; }
.lg {
  position: absolute; left: 8px; top: 6px; z-index: 3; display: flex; flex-wrap: wrap; align-items: baseline; gap: 2px 10px; max-width: calc(100% - 80px);
  padding: 4px 8px; border-radius: 6px; background: rgba(14, 14, 14, .82); font-size: var(--fs-xs); color: var(--text-3); pointer-events: none;
}
.lg .tm { color: var(--text-2); }
.it { display: inline-flex; align-items: baseline; gap: 3px; white-space: nowrap; }
.it i { font-style: normal; }
.it b { color: var(--text-1); font-weight: 600; font-size: var(--fs-sm); }
.it b.up { color: var(--up); } .it b.down { color: var(--down); }
</style>

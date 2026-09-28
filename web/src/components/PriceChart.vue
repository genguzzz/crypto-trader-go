<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { CandlestickSeries, LineSeries, HistogramSeries, AreaSeries, type IChartApi, type ISeriesApi, type MouseEventParams, type Time } from 'lightweight-charts'
import { api, type Candle, type FlowBar, type OrderFlow, type SeriesPoint } from '../api'
import { chartTime, price as fmtPrice, usd, time as fmtTime } from '../fmt'
import { baseChart, precisionFor } from './charts'
import { Footprint, type FootBar } from './charts/footprint'
import { C, alpha } from '../theme'
import { LocateFixed, Maximize2, Minimize2 } from 'lucide-vue-next'
import FilterChip from './ui/FilterChip.vue'
import Loading from './ui/Loading.vue'

/** fill: take the parent's full height (workbench); otherwise fixed chart heights (stacked page). */
const props = defineProps<{ id: string; hasSpot: boolean; hasOi: boolean; fill?: boolean }>()
const emit = defineEmits<{ live: [price: number] }>()
const bar = ref(localStorage.getItem('bar') || '15m')
type Layer = 'k' | 'delta' | 'foot'
const layer = ref<Layer>((localStorage.getItem('ctg.chartLayer') as Layer) || 'k')
const withSpot = ref(localStorage.getItem('ctg.chartSpot') === '1')
const withOIL = ref(localStorage.getItem('ctg.chartOIL') === '1') // OI level area: off by default
const withOID = ref(localStorage.getItem('ctg.chartOID') !== '0') // OI change bars: on by default
const err = ref('')
const loading = ref(false)
const el = ref<HTMLElement>()
let chart: IChartApi | undefined
let candles: ISeriesApi<'Candlestick'> | undefined
let vol: ISeriesApi<'Histogram'> | undefined
let spot: ISeriesApi<'Line'> | undefined
let delta: ISeriesApi<'Histogram'> | undefined
let oiL: ISeriesApi<'Area'> | undefined
let oiD: ISeriesApi<'Histogram'> | undefined
let cvd: ISeriesApi<'Line'> | undefined
let foot: Footprint | undefined
let timer: number | undefined
let hotTimer: number | undefined

const depth: Record<string, number> = { '1m': 1440, '5m': 2016, '15m': 672, '1H': 720, '4H': 180 }
const barMs: Record<string, number> = { '1m': 60e3, '5m': 300e3, '15m': 900e3, '1H': 3600e3, '4H': 14400e3 }
const volColor = (c: Candle) => (c.c >= c.o ? alpha('up', 0.35) : alpha('down', 0.35))

// ---- data kept for the legend ----
const bars = ref<Candle[]>([])
const flow = ref<Map<number, FlowBar>>(new Map())
const spotByT = ref<Map<number, number>>(new Map())
const oiByT = ref<Map<number, { v: number; d?: number }>>(new Map()) // bar open → OI (USD, last sample) and change vs the previous bar
const recording = ref(false)
const hover = ref<number | null>(null) // index into bars
const axisLabel = ref<{ top: number; px: number; pct: number } | null>(null)
const axisWidth = ref(60)
const footVer = ref(0) // bumped when footprint data or its level size changes (legend reads it)
const spacing = ref(6) // current bar spacing (px): the footprint needs room to draw

const shown = computed(() => {
  const xs = bars.value
  if (!xs.length) return null
  const i = hover.value ?? xs.length - 1
  const c = xs[i], prev = i > 0 ? xs[i - 1].c : c.o
  const poc = layer.value === 'foot' && footVer.value >= 0 ? foot?.barStats(chartTime(c.t) as number) ?? null : null
  return { c, chg: prev ? c.c / prev - 1 : 0, amp: prev ? (c.h - c.l) / prev : 0, f: flow.value.get(c.t), poc, oi: oiByT.value.get(c.t), spot: spotByT.value.get(c.t), live: hover.value == null }
})
const pct = (v: number, d = 2) => (v > 0 ? '+' : '') + (v * 100).toFixed(d) + '%'
const tone = (v: number) => (v > 0 ? 'up' : v < 0 ? 'down' : '')

// ---- loading: every source in parallel, newest request wins, results cached per view ----
interface Loaded { cs: Candle[]; of?: OrderFlow; ss?: Candle[]; oi?: SeriesPoint[] }
const cache = new Map<string, Loaded>() // switching back to a period/layer paints at once, then refreshes
const cacheKey = () => [bar.value, layer.value, withSpot.value && props.hasSpot, withOIL.value || withOID.value].join('|')
let seq = 0

async function fetchAll(): Promise<Loaded> {
  const b = bar.value, n = depth[b] || 400, lay = layer.value
  const hours = Math.min(24 * 30, Math.ceil((n * barMs[b]) / 3600e3) + 1)
  const [cs, of, ss, oi] = await Promise.all([
    api.candles(props.id, b, n),
    lay !== 'k' ? api.orderflow(props.id, b, n, lay === 'foot') : undefined,
    withSpot.value && props.hasSpot ? api.candles(props.id, b, Math.min(n, 1000), 'spot') : undefined,
    (withOIL.value || withOID.value) && props.hasOi ? api.series(props.id, 'oi', hours) : undefined,
  ])
  return { cs, of, ss, oi: oi || undefined }
}

/**
 * mode: 'fit' = reset to the latest bars (first open); 'anchor' = user changed period/layer/overlay:
 * keep what is on screen (right-edge time, bars in view, manual price range); 'keep' = periodic refresh.
 */
async function load(mode: 'fit' | 'anchor' | 'keep' = 'keep') {
  const my = ++seq, key = cacheKey()
  const view = mode === 'anchor' ? captureView() : null
  const hit = mode !== 'keep' ? cache.get(key) : undefined
  if (hit) apply(hit, mode, view)
  else if (mode !== 'keep') loading.value = true
  try {
    const d = await fetchAll()
    if (my !== seq || !chart) return // a newer request superseded this one
    cache.set(key, d)
    apply(d, hit ? 'keep' : mode, view)
    err.value = ''
  } catch (e: any) {
    if (my === seq) err.value = e.message
  } finally {
    if (my === seq) loading.value = false
  }
}

function apply(d: Loaded, mode: 'fit' | 'anchor' | 'keep', view: View | null) {
  const cs = d.cs
  if (!chart || !cs.length) return
  bars.value = cs
  candles!.applyOptions({ priceFormat: precisionFor(cs[cs.length - 1].c) })
  candles!.setData(cs.map(c => ({ time: chartTime(c.t), open: c.o, high: c.h, low: c.l, close: c.c })))
  vol!.setData(cs.map(c => ({ time: chartTime(c.t), value: c.q, color: volColor(c) })))
  applyFlow(cs, d.of)
  applySpot(d.ss)
  applyOI(cs, d.oi)
  if (mode === 'fit') resetView()
  else if (mode === 'anchor' && view) restoreView(view)
  requestAnimationFrame(() => { if (chart) axisWidth.value = chart.priceScale('right').width() })
}

// ---- view anchor: what the user is looking at, independent of the bar period ----
interface View { rightT: number; frac: number; width: number; live: boolean; rightOffset: number; price: { from: number; to: number } | null }
function captureView(): View | null {
  const xs = bars.value
  const lr = chart?.timeScale().getVisibleLogicalRange()
  if (!chart || !lr || !xs.length) return null
  const n = xs.length, i = Math.max(0, Math.min(n - 1, Math.floor(lr.to)))
  const ps = candles!.priceScale()
  return {
    rightT: xs[i].t, frac: lr.to - i, width: lr.to - lr.from, live: lr.to >= n - 1, rightOffset: lr.to - (n - 1),
    price: ps.options().autoScale ? null : ps.getVisibleRange(),
  }
}
function restoreView(v: View) {
  const xs = bars.value, ts = chart!.timeScale(), n = xs.length
  let to: number
  if (v.live) to = n - 1 + v.rightOffset // following the latest bar: stay on it
  else {
    let lo = 0, hi = n - 1, j = 0 // last bar opening at/before the old right edge
    while (lo <= hi) { const m = (lo + hi) >> 1; if (xs[m].t <= v.rightT) { j = m; lo = m + 1 } else hi = m - 1 }
    to = j + v.frac
  }
  ts.setVisibleLogicalRange({ from: to - v.width, to }) // same number of bars in view → same spacing
  if (v.price) candles!.priceScale().setVisibleRange(v.price)
}

// ---- spot: same price axis as the contract; the gap is the basis ----
function applySpot(ss?: Candle[]) {
  if (!chart) return
  if (!ss) {
    if (spot) { chart.removeSeries(spot); spot = undefined }
    spotByT.value = new Map()
    return
  }
  spot ??= chart.addSeries(LineSeries, { color: C.gold, lineWidth: 1, priceScaleId: 'right', title: '现货', lastValueVisible: false, crosshairMarkerVisible: false })
  spot.applyOptions({ priceFormat: precisionFor(ss[ss.length - 1]?.c || 1) })
  spot.setData(ss.map(c => ({ time: chartTime(c.t), value: c.c })))
  spotByT.value = new Map(ss.map(c => [c.t, c.c]))
}

// ---- OI: level (area) and per-bar change (bars, like volume), each in its own band above volume ----
const BAND = 0.14 // height of each OI band; volume keeps the bottom 18%
// Top margin of the candles: clear the legend overlay (2–3 lines on phones) so the latest bars
// aren't hidden under it; re-measured whenever the legend or the chart resizes.
const legendEl = ref<HTMLElement>()
function topMargin() {
  const h = chart?.panes()[0]?.getHeight() || el.value?.clientHeight || 0
  const lh = legendEl.value?.offsetHeight ?? 0
  return h ? Math.min(0.35, Math.max(0.08, (lh + 12) / h)) : 0.08
}
let legendRO: ResizeObserver | undefined
onMounted(() => {
  legendRO = new ResizeObserver(() => layoutBands())
  if (el.value) legendRO.observe(el.value)
})
watch(legendEl, (n, o) => { if (o) legendRO?.unobserve(o); if (n) legendRO?.observe(n) })
onUnmounted(() => legendRO?.disconnect())
function layoutBands() {
  if (!chart || !candles) return
  let bottom = 0.2
  if (oiD) { chart.priceScale('oid').applyOptions({ scaleMargins: { top: 1 - bottom - BAND, bottom } }); bottom += BAND }
  if (oiL) { chart.priceScale('oil').applyOptions({ scaleMargins: { top: 1 - bottom - BAND, bottom } }); bottom += BAND }
  candles.priceScale().applyOptions({ scaleMargins: { top: topMargin(), bottom: bottom + (oiD || oiL ? 0.02 : 0) } })
}
function applyOI(cs: Candle[], pts?: SeriesPoint[]) {
  if (!chart) return
  const wantL = !!pts && withOIL.value, wantD = !!pts && withOID.value
  if (!wantL && oiL) { chart.removeSeries(oiL); oiL = undefined }
  if (!wantD && oiD) { chart.removeSeries(oiD); oiD = undefined }
  if (!pts) { oiByT.value = new Map(); layoutBands(); return }
  const step = barMs[bar.value], from = cs[0].t
  const last = new Map<number, number>()
  for (const p of pts) last.set(Math.floor(p.t / step) * step, p.u ?? p.v) // ascending: last sample in each bar wins
  const m = new Map<number, { v: number; d?: number }>()
  let prev: number | undefined, prevT = 0
  for (const [t, v] of last) {
    // change only between adjacent bars: a bar after a gap (hourly-only history on small periods)
    // would otherwise carry several bars' worth of change
    if (t >= from) m.set(t, { v, d: prev !== undefined && t - prevT === step ? v - prev : undefined })
    prev = v
    prevT = t
  }
  if (wantL) oiL ??= chart.addSeries(AreaSeries, { lineColor: C.oi, lineWidth: 1, topColor: alpha('oi', 0.28), bottomColor: alpha('oi', 0.02), priceScaleId: 'oil', priceFormat: { type: 'volume' }, lastValueVisible: false, priceLineVisible: false, crosshairMarkerVisible: false })
  if (wantD) oiD ??= chart.addSeries(HistogramSeries, { priceScaleId: 'oid', priceFormat: { type: 'volume' }, lastValueVisible: false, priceLineVisible: false, base: 0 })
  oiL?.setData([...m].map(([t, x]) => ({ time: chartTime(t), value: x.v })))
  oiD?.setData([...m].filter(([, x]) => x.d !== undefined).map(([t, x]) => ({ time: chartTime(t), value: x.d!, color: x.d! >= 0 ? alpha('oi', 0.75) : alpha('oiDown', 0.75) })))
  oiByT.value = m
  layoutBands()
}

// ---- hot data: while this chart is on screen, the forming bars refresh every second ----
let hotBusy = false
async function hot() {
  if (hotBusy || document.hidden || !chart || !bars.value.length) return
  hotBusy = true
  const b = bar.value
  try {
    const cs = await api.candles(props.id, b, 10)
    if (b !== bar.value || !chart || !bars.value.length) return
    const xs = bars.value.slice()
    let last = xs[xs.length - 1]
    for (const c of cs) {
      if (c.t < last.t) continue
      if (c.t === last.t) xs[xs.length - 1] = c
      else xs.push(c)
      last = c
      candles!.update({ time: chartTime(c.t), open: c.o, high: c.h, low: c.l, close: c.c })
      vol!.update({ time: chartTime(c.t), value: c.q, color: volColor(c) })
    }
    bars.value = xs
    emit('live', last.c)
    if (spot && withSpot.value && props.hasSpot) { // keep the spot line's forming bar in step
      const ss = await api.candles(props.id, b, 10, 'spot')
      if (b !== bar.value || !spot) return
      const m = new Map(spotByT.value)
      const lastT = [...m.keys()].pop() ?? 0
      for (const c of ss) {
        if (c.t < lastT) continue
        spot.update({ time: chartTime(c.t), value: c.c })
        m.set(c.t, c.c)
      }
      spotByT.value = m
    }
  } catch { /* the 10 s full reload reports errors */ } finally {
    hotBusy = false
  }
}

// ---- reset: latest bars at a readable width, every price scale back to auto ----
function resetView() {
  if (!chart) return
  const ts = chart.timeScale(), n = bars.value.length, w = el.value?.clientWidth ?? 600
  chart.panes().forEach((_, i) => {
    for (const id of ['right', 'left']) chart!.priceScale(id, i).applyOptions({ autoScale: true })
  })
  chart.priceScale('vol').applyOptions({ autoScale: true })
  if (!n) return
  // footprint wants wide bars for its numbers; candles ~7px so bodies stay visible
  const px = layer.value === 'foot' ? Math.max(40, Math.min(72, w / 10)) : 7
  const show = Math.min(n, Math.max(layer.value === 'foot' ? 8 : 40, Math.round(w / px)))
  ts.setVisibleLogicalRange({ from: n - show, to: n + 2 })
}

// ---- order flow layer: delta histogram + CVD in a second pane; footprint over the candles ----
function applyFlow(cs: Candle[], of?: OrderFlow) {
  if (!chart) return
  const wantFoot = layer.value === 'foot' && !!of
  if (foot && !wantFoot) {
    candles!.detachPrimitive(foot)
    candles!.applyOptions({ upColor: C.up, downColor: C.down })
    vol!.applyOptions({ visible: true })
    foot = undefined
  }
  if (!of) {
    if (delta) chart.removeSeries(delta)
    if (cvd) chart.removeSeries(cvd)
    delta = cvd = undefined
    if (chart.panes().length > 1) chart.removePane(1)
    flow.value = new Map()
    return
  }
  recording.value = of.recording
  const m = new Map<number, FlowBar>()
  for (const b of of.bars || []) m.set(b.t, b)
  flow.value = m
  if (!delta) {
    delta = chart.addSeries(HistogramSeries, { priceFormat: { type: 'volume' }, title: 'Delta', lastValueVisible: false, priceLineVisible: false }, 1)
    cvd = chart.addSeries(LineSeries, { color: C.gold, lineWidth: 1, priceScaleId: 'left', priceFormat: { type: 'volume' }, title: 'CVD', lastValueVisible: false, priceLineVisible: false }, 1)
    chart.priceScale('left', 1).applyOptions({ visible: true, borderColor: C.border })
    chart.panes()[1]?.setHeight(110)
  }
  let cum = 0
  const dd: { time: Time; value: number; color: string }[] = [], cc: { time: Time; value: number }[] = []
  for (const c of cs) {
    const f = m.get(c.t)
    if (!f) continue
    const d = f.buy - f.sell
    cum += d
    const a = f.src === 'trades' ? 0.85 : 0.5 // rubik history drawn lighter
    dd.push({ time: chartTime(c.t), value: d, color: d >= 0 ? alpha('up', a) : alpha('down', a) })
    cc.push({ time: chartTime(c.t), value: cum })
  }
  delta!.setData(dd)
  cvd!.setData(cc)
  if (wantFoot) {
    if (!foot) {
      foot = new Footprint()
      foot.onRegroup = () => footVer.value++
      candles!.attachPrimitive(foot)
      candles!.applyOptions({ upColor: alpha('up', 0.18), downColor: alpha('down', 0.18) })
    }
    vol!.applyOptions({ visible: false }) // footprint already shows volume per level; the bars would cover it
    const fm = new Map<number, FootBar>()
    for (const b of of.bars || []) if (b.levels?.length && b.step) fm.set(chartTime(b.t) as number, { levels: b.levels, step: b.step })
    foot.setData(fm, cs.map(c => ({ time: chartTime(c.t) as number, low: c.l, high: c.h })))
    footVer.value++
  }
}

// ---- crosshair: legend follows the hovered bar; price axis label shows distance from last price ----
function onCrosshair(p: MouseEventParams<Time>) {
  if (!p.time || !p.point) {
    hover.value = null
    axisLabel.value = null
    return
  }
  const t = p.time as number
  const xs = bars.value
  let lo = 0, hi = xs.length - 1, found = -1
  while (lo <= hi) { // bars are ascending by chart time
    const mid = (lo + hi) >> 1, v = chartTime(xs[mid].t) as number
    if (v === t) { found = mid; break }
    if (v < t) lo = mid + 1; else hi = mid - 1
  }
  hover.value = found >= 0 ? found : null
  const pane0 = chart!.panes()[0]?.getHeight() ?? 0
  if ((p as any).paneIndex === 0 || ((p as any).paneIndex == null && p.point.y < pane0)) {
    const px = candles!.coordinateToPrice(p.point.y)
    const last = xs[xs.length - 1]?.c
    axisWidth.value = chart!.priceScale('right').width()
    axisLabel.value = px != null && last ? { top: p.point.y, px, pct: px / last - 1 } : null
  } else axisLabel.value = null
}

onMounted(() => {
  chart = baseChart(el.value!, { crosshair: { mode: 0, horzLine: { labelVisible: false } } })
  candles = chart.addSeries(CandlestickSeries, { upColor: C.up, downColor: C.down, borderVisible: false, wickUpColor: C.up, wickDownColor: C.down })
  vol = chart.addSeries(HistogramSeries, { priceScaleId: 'vol', priceFormat: { type: 'volume' }, lastValueVisible: false, priceLineVisible: false })
  chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.82, bottom: 0 } })
  layoutBands()
  chart.subscribeCrosshairMove(onCrosshair)
  chart.timeScale().subscribeVisibleLogicalRangeChange(() => { spacing.value = chart?.timeScale().options().barSpacing ?? 6 })
  load('fit')
  timer = window.setInterval(() => load(), 10_000)
  hotTimer = window.setInterval(hot, 1000)
})
onUnmounted(() => { clearInterval(timer); clearInterval(hotTimer); chart?.remove() })
watch(bar, v => { localStorage.setItem('bar', v); load('anchor') })
watch(layer, v => { localStorage.setItem('ctg.chartLayer', v); load('anchor') })
watch(withSpot, v => { localStorage.setItem('ctg.chartSpot', v ? '1' : '0'); load('anchor') })
watch(withOIL, v => { localStorage.setItem('ctg.chartOIL', v ? '1' : '0'); load('anchor') })
watch(withOID, v => { localStorage.setItem('ctg.chartOID', v ? '1' : '0'); load('anchor') })
// ---- full screen: native Fullscreen API where the element supports it; otherwise (iPhone Safari)
// a fixed overlay covering the viewport. Esc / the button leaves either way.
const root = ref<HTMLElement>()
const full = ref(false)
const pseudo = ref(false)
async function toggleFull() {
  if (full.value) {
    if (document.fullscreenElement) await document.exitFullscreen().catch(() => {})
    pseudo.value = full.value = false
    return
  }
  const el = root.value as any
  if (el?.requestFullscreen) {
    try { await el.requestFullscreen(); full.value = true; return } catch { /* fall through */ }
  }
  pseudo.value = full.value = true
}
const onFsChange = () => { if (!document.fullscreenElement && !pseudo.value) full.value = false }
const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape' && pseudo.value) pseudo.value = full.value = false }
onMounted(() => { document.addEventListener('fullscreenchange', onFsChange); window.addEventListener('keydown', onKey) })
onUnmounted(() => { document.removeEventListener('fullscreenchange', onFsChange); window.removeEventListener('keydown', onKey) })
watch(full, () => requestAnimationFrame(() => { if (chart) axisWidth.value = chart.priceScale('right').width() }))

const pxDigits = computed(() => precisionFor(bars.value[bars.value.length - 1]?.c || 1).precision)
</script>

<template>
  <div ref="root" class="panel pc" :class="{ fill: fill || full, full, pseudo }">
    <h3>
      <span>合约 K 线 <InfoTip>
        <b>数据来源</b>：OKX 永续 1m K 线由本机录制（实时 WS + 启动回填）；进过监控池的币额外回补 7 天 1m；1H/4H 来自 1H K 线，保留 30 天。5m/15m/4H 由服务端按 UTC 对齐聚合。<br />
        <b>十字光标</b>：左上角显示所指 K 线的开高低收、涨跌幅（对上一根收盘）、振幅与成交额；右侧价格标签显示光标价位<b>距现价</b>的百分比。手机上长按后拖动。<br />
        <b>订单流</b>：主动买入（taker buy）与主动卖出的 USDT 成交额。<b>Delta</b> = 买 − 卖（下方柱），<b>CVD</b> = 区间内 Delta 累计（黄线）。本机逐笔录制的 K 线颜色较深；更早的由 OKX 统计的 5m/1H 主动买卖量补充（颜色较浅，1m 周期没有）。<br />
        <b>足迹</b>：每根 K 线内每个价位的主动卖（左，红）/ 主动买（右，绿），颜色深浅按<b>当前可见范围</b>内最大的单价位成交额换算（跨 K 线可比）。价位档会随纵向缩放自动合并（×2、×10…），左上角显示当前每档大小；拖动右侧价格轴可纵向放大看更细的档位，横向放大到够宽时显示数字。有足迹的 K 线来自逐笔成交：活跃池、自选、正在查看的合约实时录制；打开详情页时自动回补保留期内（查看/活跃 3 天、自选 7 天）缺失的分钟——整天的用 OKX 官方每日逐笔成交文件（按北京时间分天，次日发布），当天尚未发布的部分用 OKX 历史成交接口补最近一段。只有一整根 K 线的每分钟都齐全才画足迹。<br />
        <b>POC / 价值区</b>：黄框为该根 K 线成交最多的价位（Point of Control）；每根 K 线左侧的细金线是价值区（从 POC 向两侧扩展、覆盖 70% 成交额的价格区间），数值见左上角。<b>未回补 POC</b>：已收盘 K 线的 POC 之后没有任何 K 线的最高–最低价穿过它，就用金色虚线画到最右侧（只画最近 5 条），被穿过后不再显示——常用来观察价格是否会回来“回补”。<br />
        <b>怎么读</b>：① 先看 POC 在 K 线的上部还是下部：收阳且 POC 在上部 = 高位有人接、偏强；收阳但 POC 在下部、上影线处成交稀薄 = 上涨没人跟。② 在区间高点出现大量主动买却涨不上去（吸收）、或新高处成交突然变少（衰竭），是常见的反转线索。③ 堆叠失衡所在价位常成为之后的支撑/阻力。④ 这些都是“发生了什么”的描述，不是预测，要结合更大周期和成本判断。<br />
        <b>失衡</b>（对角比较）：某价位的主动买 ≥ 低一档主动卖的 3 倍，在右侧标绿色竖条、数字变绿；主动卖 ≥ 高一档主动买的 3 倍，在左侧标红色竖条。量太小（低于该 K 线各价位单边成交额中位数）的不算。连续 3 档及以上同方向失衡为<b>堆叠失衡</b>，该侧加描边，常被当作主动方强势的信号，不是必然。<br />
        <b>成交额柱</b>：底部柱为该根 K 线的 USDT 成交额，绿涨红跌。缺失分钟显示为空档；最后一根为未收盘 K 线。<br />
        <b>现货 / OI</b>：现货为 OKX 同名 USDT 现货收盘价（黄线），和合约 K 线共用右侧价格轴，缩放、拖动都跟随 K 线，两者的间距就是基差；<b>OI Δ</b>（默认开）为持仓量变化柱：每根 K 线相对上一根的增减，蓝增橙减，和成交额柱一样画在 K 线下方的独立区域；<b>OI</b> 为持仓量走势（面积图），在变化柱上方再占一条。左上角显示持仓量与变化（近期逐分钟采样；更早只有 1 小时采样，小周期在那段没有变化柱）。没有对应数据时按钮置灰。<br />
        <b>刷新</b>：打开详情页时最新几根 K 线每秒刷新（服务端 OKX WebSocket 推送的未收盘 K 线），整段数据和订单流每 10 秒刷新；页面切到后台时暂停。<br />
        <b>时间</b>：按设置页选择的时区显示（默认 UTC+8）。右下角 ◎ 或双击图表回到最新并自动缩放。
      </InfoTip></span>
      <span class="bars">
        <Segmented v-model="layer" size="sm" aria-label="图层" :options="[{ value: 'k', label: 'K 线' }, { value: 'delta', label: 'Delta' }, { value: 'foot', label: '足迹' }]" />
        <Segmented v-model="bar" size="sm" aria-label="K 线周期" :options="[{ value: '1m', label: '1m' }, { value: '5m', label: '5m' }, { value: '15m', label: '15m' }, { value: '1H', label: '1H' }, { value: '4H', label: '4H' }]" />
      </span>
      <span class="ovl">
        <FilterChip v-model="withSpot" label="现货" :disabled="!hasSpot" :title="hasSpot ? '叠加 OKX 同名 USDT 现货收盘价（黄线，与 K 线同一价格轴）' : '没有 OKX 同名 USDT 现货'" />
        <FilterChip v-model="withOID" label="OI Δ" :disabled="!hasOi" :title="hasOi ? '在 K 线下方显示每根 K 线的持仓量变化柱（蓝增橙减）' : '没有持仓量数据'" />
        <FilterChip v-model="withOIL" label="OI" :disabled="!hasOi" :title="hasOi ? '在 K 线下方显示持仓量走势（面积图）' : '没有持仓量数据'" />
        <button type="button" class="fs-btn" :title="full ? '退出全屏（Esc）' : '全屏'" :aria-label="full ? '退出全屏' : '全屏'" @click="toggleFull">
          <Minimize2 v-if="full" :size="15" /><Maximize2 v-else :size="15" />
        </button>
      </span>
    </h3>
    <div class="wrap" :class="{ tall: layer !== 'k' }">
      <div ref="el" class="chart" @dblclick="resetView"></div>
      <Loading overlay :show="loading" />
      <div v-if="layer === 'foot' && spacing < 20 && bars.length" class="foot-hint">横向放大（滚轮 / 双指）到足够宽才显示足迹</div>
      <button type="button" class="reset" title="回到最新、自动缩放（PC 也可双击图表）" aria-label="重置视图" :style="{ right: axisWidth + 6 + 'px' }" @click="resetView"><LocateFixed :size="15" /></button>
      <div ref="legendEl" class="legend tnum" v-if="shown">
        <span class="muted">{{ fmtTime(shown.c.t, true) }}</span>
        <span>开 <b>{{ fmtPrice(shown.c.o) }}</b></span><span>高 <b>{{ fmtPrice(shown.c.h) }}</b></span>
        <span>低 <b>{{ fmtPrice(shown.c.l) }}</b></span><span>收 <b :class="tone(shown.c.c - shown.c.o)">{{ fmtPrice(shown.c.c) }}</b></span>
        <span>涨跌 <b :class="tone(shown.chg)">{{ pct(shown.chg) }}</b></span><span>振幅 <b>{{ pct(shown.amp) }}</b></span>
        <span>额 <b>{{ usd(shown.c.q) }}</b></span>
        <span v-if="withSpot && shown.spot != null">现货 <b class="spot">{{ fmtPrice(shown.spot) }}</b> <small class="muted">基差 {{ pct(shown.c.c / shown.spot - 1, 3) }}</small></span>
        <span v-if="(withOID || withOIL) && shown.oi">OI <b class="oi">{{ usd(shown.oi.v) }}</b> <small v-if="shown.oi.d !== undefined" :class="tone(shown.oi.d)">{{ (shown.oi.d >= 0 ? '+' : '−') + usd(Math.abs(shown.oi.d)) }}（{{ pct(shown.oi.d / (shown.oi.v - shown.oi.d)) }}）</small></span>
        <template v-if="layer !== 'k'">
          <br />
          <template v-if="shown.f">
            <span>主动买 <b class="up">{{ usd(shown.f.buy) }}</b></span><span>主动卖 <b class="down">{{ usd(shown.f.sell) }}</b></span>
            <span>Delta <b :class="tone(shown.f.buy - shown.f.sell)">{{ (shown.f.buy >= shown.f.sell ? '+' : '−') + usd(Math.abs(shown.f.buy - shown.f.sell)) }}</b></span>
            <span class="muted">{{ shown.f.src === 'trades' ? '本机逐笔' : 'OKX 统计' }}</span>
            <template v-if="shown.poc">
              <span>POC <b class="poc">{{ fmtPrice(shown.poc.poc) }}</b></span>
              <span>价值区 <b>{{ fmtPrice(shown.poc.val) }}–{{ fmtPrice(shown.poc.vah) }}</b></span>
              <span class="muted">每档 {{ +shown.poc.step.toPrecision(6) }}</span>
            </template>
          </template>
          <span v-else class="muted">这根 K 线没有订单流数据{{ bar === '1m' ? '（1m 只有本机逐笔录制）' : '' }}</span>
          <span v-if="!recording" class="muted">· 未在录制逐笔</span>
          <template v-if="layer === 'foot'">
            <br />
            <span class="key"><i class="k-cell"><em class="s"></em><em class="b"></em></i>左卖右买</span>
            <span class="key"><i class="k-poc"></i>POC 成交最多价位</span>
            <span class="key"><i class="k-va"></i>价值区 70%</span>
            <span class="key"><i class="k-naked"></i>未回补 POC</span>
          </template>
        </template>
      </div>
      <div v-if="axisLabel" class="axis-lbl tnum" :style="{ top: axisLabel.top - 16 + 'px', width: axisWidth + 'px' }">
        <b>{{ axisLabel.px.toFixed(pxDigits) }}</b>
        <span :class="tone(axisLabel.pct)">{{ pct(axisLabel.pct) }}</span>
      </div>
    </div>
    <div class="note" v-if="err">图表数据错误：{{ err }}</div>
  </div>
</template>

<style scoped>
.wrap { position: relative; }
/* fill mode: header on top, the chart takes the rest (workbench panel, full screen) */
.pc.fill { display: flex; flex-direction: column; height: 100%; min-height: 0; }
.pc.fill .wrap { flex: 1; min-height: 0; }
.pc.fill .chart, .pc.fill .wrap.tall .chart { height: 100%; }
.pc.full { background: var(--bg-1); padding: 10px 12px; border-radius: 0; }
.pc.pseudo { position: fixed; inset: 0; z-index: 100; padding-bottom: calc(10px + env(safe-area-inset-bottom)); }
.fs-btn { all: unset; box-sizing: border-box; display: inline-grid; place-items: center; width: 28px; height: 24px; border-radius: 6px; color: var(--text-2); cursor: pointer; border: 1px solid var(--line-2); }
@media (hover: hover) { .fs-btn:hover { color: var(--text-1); border-color: var(--accent); } }
.fs-btn:focus-visible { outline: 2px solid var(--accent); }
.reset { all: unset; box-sizing: border-box; position: absolute; bottom: 34px; z-index: 4; display: grid; place-items: center; width: 28px; height: 28px; border-radius: 6px;
  background: rgba(14, 14, 14, .85); border: 1px solid var(--line-2); color: var(--text-2); cursor: pointer; }
@media (hover: hover) { .reset:hover { color: var(--text-1); border-color: var(--accent); } }
.reset:active { background: var(--bg-3); }
.reset:focus-visible { outline: 2px solid var(--accent); }
.chart { height: 340px; }
.wrap.tall .chart { height: 450px; }
h3 { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 6px; }
.bars { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.ovl { display: flex; gap: 6px; align-items: center; }
.ovl :deep(.fchip) { height: 24px; padding: 0 10px; font-size: var(--fs-xs); font-weight: 500; }
.legend { position: absolute; left: 6px; top: 4px; max-width: calc(100% - 76px); z-index: 3; pointer-events: none; padding: 0 4px; border-radius: 4px; background: rgba(14, 14, 14, .78); font-size: var(--fs-xs); color: var(--text-3); line-height: 1.6; }
.legend span { margin-right: 8px; white-space: nowrap; }
.legend b { color: var(--text-1); font-weight: 500; }
.legend b.up { color: var(--up); } .legend b.down { color: var(--down); } .legend b.poc { color: var(--gold); } .legend b.oi { color: var(--info); } .legend small.up { color: var(--up); } .legend small.down { color: var(--down); } .legend b.spot { color: var(--gold); }
.legend .key { display: inline-flex; align-items: center; gap: 4px; }
.key i { display: inline-block; flex: none; }
.k-cell { display: inline-flex !important; width: 14px; height: 9px; } .k-cell em { flex: 1; } .k-cell .s { background: var(--down); } .k-cell .b { background: var(--up); }
.k-poc { width: 12px; height: 8px; border: 1px solid var(--gold); }
.k-va { width: 2px; height: 10px; background: var(--gold); opacity: .7; }
.k-naked { width: 14px; border-top: 1px dashed var(--gold); }
.foot-hint { position: absolute; left: 50%; bottom: 48px; transform: translateX(-50%); z-index: 3; pointer-events: none; padding: 4px 10px; border-radius: 6px; background: rgba(14, 14, 14, .85); border: 1px solid var(--line-2); color: var(--text-2); font-size: var(--fs-xs); white-space: nowrap; }
.axis-lbl { position: absolute; right: 0; z-index: 3; pointer-events: none; display: grid; justify-items: center; padding: 1px 0; background: var(--bg-3); border-radius: 2px; font-size: 10px; line-height: 15px; color: var(--text-1); }
.axis-lbl b { font-weight: 500; }
.axis-lbl .up { color: var(--up); } .axis-lbl .down { color: var(--down); }
@media (max-width: 900px) {
  .legend { max-width: calc(100% - 70px); font-size: 10px; }
  .chart { height: 320px; } .wrap.tall .chart { height: 420px; }
  /* two rows: title + spot toggle, then the layer/period switches */
  h3 { row-gap: 8px; } h3 > span:first-child { flex: 1; } .bars { order: 3; width: 100%; flex-wrap: nowrap; }
}
</style>

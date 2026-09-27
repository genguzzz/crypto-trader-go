<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { CandlestickSeries, LineSeries, HistogramSeries, type IChartApi, type ISeriesApi, type MouseEventParams, type Time } from 'lightweight-charts'
import { api, type Candle, type FlowBar } from '../api'
import { chartTime, price as fmtPrice, usd, time as fmtTime } from '../fmt'
import { baseChart, precisionFor } from './charts'
import { Footprint, type FootBar } from './charts/footprint'

const props = defineProps<{ id: string; hasSpot: boolean }>()
const bar = ref(localStorage.getItem('bar') || '15m')
type Layer = 'k' | 'delta' | 'foot'
const layer = ref<Layer>((localStorage.getItem('ctg.chartLayer') as Layer) || 'k')
const withSpot = ref(false)
const err = ref('')
const el = ref<HTMLElement>()
let chart: IChartApi | undefined
let candles: ISeriesApi<'Candlestick'> | undefined
let vol: ISeriesApi<'Histogram'> | undefined
let spot: ISeriesApi<'Line'> | undefined
let delta: ISeriesApi<'Histogram'> | undefined
let cvd: ISeriesApi<'Line'> | undefined
let foot: Footprint | undefined
let timer: number | undefined

const UP = '#26a69a', DOWN = '#ef5350'
const depth: Record<string, number> = { '1m': 1440, '5m': 2016, '15m': 672, '1H': 720, '4H': 180 }

// ---- data kept for the legend ----
const bars = ref<Candle[]>([])
const flow = ref<Map<number, FlowBar>>(new Map())
const recording = ref(false)
const hover = ref<number | null>(null) // index into bars
const axisLabel = ref<{ top: number; px: number; pct: number } | null>(null)
const axisWidth = ref(60)

const shown = computed(() => {
  const xs = bars.value
  if (!xs.length) return null
  const i = hover.value ?? xs.length - 1
  const c = xs[i], prev = i > 0 ? xs[i - 1].c : c.o
  return { c, chg: prev ? c.c / prev - 1 : 0, amp: prev ? (c.h - c.l) / prev : 0, f: flow.value.get(c.t), live: hover.value == null }
})
const pct = (v: number, d = 2) => (v > 0 ? '+' : '') + (v * 100).toFixed(d) + '%'
const tone = (v: number) => (v > 0 ? 'up' : v < 0 ? 'down' : '')

async function load(fit = false) {
  try {
    const cs = await api.candles(props.id, bar.value, depth[bar.value] || 400)
    if (!chart || !cs.length) return
    bars.value = cs
    candles!.applyOptions({ priceFormat: precisionFor(cs[cs.length - 1].c) })
    candles!.setData(cs.map(c => ({ time: chartTime(c.t), open: c.o, high: c.h, low: c.l, close: c.c })))
    vol!.setData(cs.map(c => ({ time: chartTime(c.t), value: c.q, color: c.c >= c.o ? 'rgba(38,166,154,0.35)' : 'rgba(239,83,80,0.35)' })))
    await loadFlow(cs)
    if (withSpot.value && props.hasSpot) {
      const ss = await api.candles(props.id, bar.value, Math.min(depth[bar.value] || 400, 1000), 'spot')
      if (!spot) {
        spot = chart.addSeries(LineSeries, { color: '#f5c542', lineWidth: 1, priceScaleId: 'left', title: '现货' })
        chart.applyOptions({ leftPriceScale: { visible: true } })
      }
      spot.applyOptions({ priceFormat: precisionFor(ss[ss.length - 1]?.c || 1) })
      spot.setData(ss.map(c => ({ time: chartTime(c.t), value: c.c })))
    } else if (spot) {
      chart.removeSeries(spot)
      spot = undefined
      chart.applyOptions({ leftPriceScale: { visible: false } })
    }
    if (fit) {
      if (layer.value === 'foot') {
        chart.timeScale().applyOptions({ barSpacing: 72 })
        chart.timeScale().scrollToRealTime()
      } else chart.timeScale().fitContent()
    }
    err.value = ''
  } catch (e: any) {
    err.value = e.message
  }
}

// ---- order flow layer: delta histogram + CVD in a second pane; footprint over the candles ----
async function loadFlow(cs: Candle[]) {
  if (layer.value === 'k' || !chart) return
  const of = await api.orderflow(props.id, bar.value, depth[bar.value] || 400, layer.value === 'foot')
  recording.value = of.recording
  const m = new Map<number, FlowBar>()
  for (const b of of.bars || []) m.set(b.t, b)
  flow.value = m
  if (!delta) {
    delta = chart.addSeries(HistogramSeries, { priceFormat: { type: 'volume' }, title: 'Delta', lastValueVisible: false, priceLineVisible: false }, 1)
    cvd = chart.addSeries(LineSeries, { color: '#f5c542', lineWidth: 1, priceScaleId: 'left', priceFormat: { type: 'volume' }, title: 'CVD', lastValueVisible: false, priceLineVisible: false }, 1)
    chart.priceScale('left', 1).applyOptions({ visible: true, borderColor: '#2a2f3a' })
    chart.panes()[1]?.setHeight(110)
  }
  let cum = 0
  const dd: { time: Time; value: number; color: string }[] = [], cc: { time: Time; value: number }[] = []
  for (const c of cs) {
    const f = m.get(c.t)
    if (!f) continue
    const d = f.buy - f.sell
    cum += d
    const alpha = f.src === 'trades' ? 0.85 : 0.5 // rubik history drawn lighter
    dd.push({ time: chartTime(c.t), value: d, color: d >= 0 ? `rgba(38,166,154,${alpha})` : `rgba(239,83,80,${alpha})` })
    cc.push({ time: chartTime(c.t), value: cum })
  }
  delta!.setData(dd)
  cvd!.setData(cc)
  if (layer.value === 'foot') {
    if (!foot) {
      foot = new Footprint()
      candles!.attachPrimitive(foot)
      candles!.applyOptions({ upColor: 'rgba(38,166,154,.18)', downColor: 'rgba(239,83,80,.18)' })
    }
    const fm = new Map<number, FootBar>()
    for (const b of of.bars || []) if (b.levels?.length && b.step) fm.set(chartTime(b.t) as number, { levels: b.levels, step: b.step })
    foot.setData(fm)
  }
}

function clearFlow() {
  if (!chart) return
  if (foot) {
    candles!.detachPrimitive(foot)
    candles!.applyOptions({ upColor: UP, downColor: DOWN })
    foot = undefined
  }
  if (layer.value === 'k') {
    if (delta) chart.removeSeries(delta)
    if (cvd) chart.removeSeries(cvd)
    delta = cvd = undefined
    if (chart.panes().length > 1) chart.removePane(1)
    flow.value = new Map()
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
  candles = chart.addSeries(CandlestickSeries, { upColor: UP, downColor: DOWN, borderVisible: false, wickUpColor: UP, wickDownColor: DOWN })
  vol = chart.addSeries(HistogramSeries, { priceScaleId: 'vol', priceFormat: { type: 'volume' }, lastValueVisible: false, priceLineVisible: false })
  chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.82, bottom: 0 } })
  chart.subscribeCrosshairMove(onCrosshair)
  load(true)
  timer = window.setInterval(() => load(), 10_000)
})
onUnmounted(() => { clearInterval(timer); chart?.remove() })
watch(bar, v => { localStorage.setItem('bar', v); load(true) })
watch(withSpot, () => load())
watch(layer, (v, old) => {
  localStorage.setItem('ctg.chartLayer', v)
  if (old === 'foot' || v === 'k') clearFlow()
  load(v === 'foot' || old === 'foot')
})
const pxDigits = computed(() => precisionFor(bars.value[bars.value.length - 1]?.c || 1).precision)
</script>

<template>
  <div class="panel">
    <h3>
      <span>合约 K 线 <InfoTip>
        <b>数据来源</b>：OKX 永续 1m K 线由本机录制（实时 WS + 启动回填）；进过监控池的币额外回补 7 天 1m；1H/4H 来自 1H K 线，保留 30 天。5m/15m/4H 由服务端按 UTC 对齐聚合。<br />
        <b>十字光标</b>：左上角显示所指 K 线的开高低收、涨跌幅（对上一根收盘）、振幅与成交额；右侧价格标签显示光标价位<b>距现价</b>的百分比。手机上长按后拖动。<br />
        <b>订单流</b>：主动买入（taker buy）与主动卖出的 USDT 成交额。<b>Delta</b> = 买 − 卖（下方柱），<b>CVD</b> = 区间内 Delta 累计（黄线）。本机逐笔录制的 K 线颜色较深；更早的由 OKX 统计的 5m/1H 主动买卖量补充（颜色较浅，1m 周期没有）。<br />
        <b>足迹</b>：每根 K 线内每个价位的主动卖（左，红）/ 主动买（右，绿），黄框为成交最多的价位（POC），一侧 ≥3 倍另一侧时数字高亮。只有本机录制了逐笔成交的 K 线有足迹（活跃池、自选、正在查看的合约，从开始录制起）；放大 K 线可显示数字。<br />
        <b>成交额柱</b>：底部柱为该根 K 线的 USDT 成交额，绿涨红跌。缺失分钟显示为空档；最后一根为未收盘 K 线。<br />
        <b>叠加现货</b>：OKX 同名 USDT 现货收盘价（黄线），使用左侧独立价格轴。
      </InfoTip></span>
      <span class="bars">
        <Segmented v-model="layer" size="sm" aria-label="图层" :options="[{ value: 'k', label: 'K 线' }, { value: 'delta', label: 'Delta' }, { value: 'foot', label: '足迹' }]" />
        <Segmented v-model="bar" size="sm" aria-label="K 线周期" :options="[{ value: '1m', label: '1m' }, { value: '5m', label: '5m' }, { value: '15m', label: '15m' }, { value: '1H', label: '1H' }, { value: '4H', label: '4H' }]" />
        <label class="spot" :class="{ muted: !hasSpot }" :title="hasSpot ? 'OKX 同名 USDT 现货，左侧独立价格轴' : '无 OKX USDT 现货映射'">
          <input type="checkbox" v-model="withSpot" :disabled="!hasSpot" /> 叠加现货
        </label>
      </span>
    </h3>
    <div class="wrap" :class="{ tall: layer !== 'k' }">
      <div ref="el" class="chart"></div>
      <div class="legend tnum" v-if="shown">
        <span class="muted">{{ fmtTime(shown.c.t, true) }}</span>
        <span>开 <b>{{ fmtPrice(shown.c.o) }}</b></span><span>高 <b>{{ fmtPrice(shown.c.h) }}</b></span>
        <span>低 <b>{{ fmtPrice(shown.c.l) }}</b></span><span>收 <b :class="tone(shown.c.c - shown.c.o)">{{ fmtPrice(shown.c.c) }}</b></span>
        <span>涨跌 <b :class="tone(shown.chg)">{{ pct(shown.chg) }}</b></span><span>振幅 <b>{{ pct(shown.amp) }}</b></span>
        <span>额 <b>{{ usd(shown.c.q) }}</b></span>
        <template v-if="layer !== 'k'">
          <br />
          <template v-if="shown.f">
            <span>主动买 <b class="up">{{ usd(shown.f.buy) }}</b></span><span>主动卖 <b class="down">{{ usd(shown.f.sell) }}</b></span>
            <span>Delta <b :class="tone(shown.f.buy - shown.f.sell)">{{ (shown.f.buy >= shown.f.sell ? '+' : '−') + usd(Math.abs(shown.f.buy - shown.f.sell)) }}</b></span>
            <span class="muted">{{ shown.f.src === 'trades' ? '本机逐笔' : 'OKX 统计' }}</span>
          </template>
          <span v-else class="muted">这根 K 线没有订单流数据{{ bar === '1m' ? '（1m 只有本机逐笔录制）' : '' }}</span>
          <span v-if="!recording" class="muted">· 未在录制逐笔</span>
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
.chart { height: 340px; }
.wrap.tall .chart { height: 450px; }
h3 { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 6px; }
.bars { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.spot { font-weight: 400; font-size: 12px; margin-left: 2px; display: flex; align-items: center; gap: 4px; }
.legend { position: absolute; left: 6px; top: 4px; right: 70px; z-index: 3; pointer-events: none; font-size: var(--fs-xs); color: var(--text-3); line-height: 1.6; }
.legend span { margin-right: 8px; white-space: nowrap; }
.legend b { color: var(--text-1); font-weight: 500; }
.legend b.up { color: var(--up); } .legend b.down { color: var(--down); }
.axis-lbl { position: absolute; right: 0; z-index: 3; pointer-events: none; display: grid; justify-items: center; padding: 1px 0; background: #2b3242; border-radius: 2px; font-size: 10px; line-height: 15px; color: var(--text-1); }
.axis-lbl b { font-weight: 500; }
.axis-lbl .up { color: var(--up); } .axis-lbl .down { color: var(--down); }
@media (max-width: 900px) { .legend { right: 64px; font-size: 10px; } }
</style>

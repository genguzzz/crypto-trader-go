<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { CandlestickSeries, LineSeries, HistogramSeries, type IChartApi, type ISeriesApi, type MouseEventParams, type Time } from 'lightweight-charts'
import { api, type Candle, type FlowBar } from '../api'
import { chartTime, price as fmtPrice, usd, time as fmtTime } from '../fmt'
import { baseChart, precisionFor } from './charts'
import { Footprint, type FootBar } from './charts/footprint'
import { LocateFixed } from 'lucide-vue-next'
import FilterChip from './ui/FilterChip.vue'

const props = defineProps<{ id: string; hasSpot: boolean; hasOi: boolean }>()
const emit = defineEmits<{ live: [price: number] }>()
const bar = ref(localStorage.getItem('bar') || '15m')
type Layer = 'k' | 'delta' | 'foot'
const layer = ref<Layer>((localStorage.getItem('ctg.chartLayer') as Layer) || 'k')
const withSpot = ref(localStorage.getItem('ctg.chartSpot') === '1')
const withOI = ref(localStorage.getItem('ctg.chartOI') === '1')
const err = ref('')
const el = ref<HTMLElement>()
let chart: IChartApi | undefined
let candles: ISeriesApi<'Candlestick'> | undefined
let vol: ISeriesApi<'Histogram'> | undefined
let spot: ISeriesApi<'Line'> | undefined
let delta: ISeriesApi<'Histogram'> | undefined
let oi: ISeriesApi<'Line'> | undefined
let cvd: ISeriesApi<'Line'> | undefined
let foot: Footprint | undefined
let timer: number | undefined
let hotTimer: number | undefined

const UP = '#26a69a', DOWN = '#ef5350'
const depth: Record<string, number> = { '1m': 1440, '5m': 2016, '15m': 672, '1H': 720, '4H': 180 }
const barMs: Record<string, number> = { '1m': 60e3, '5m': 300e3, '15m': 900e3, '1H': 3600e3, '4H': 14400e3 }

// ---- data kept for the legend ----
const bars = ref<Candle[]>([])
const flow = ref<Map<number, FlowBar>>(new Map())
const oiByT = ref<Map<number, number>>(new Map()) // bar open → OI (USD) at the bar's last sample
const recording = ref(false)
const hover = ref<number | null>(null) // index into bars
const axisLabel = ref<{ top: number; px: number; pct: number } | null>(null)
const axisWidth = ref(60)
const footVer = ref(0) // bumped when footprint data or its level size changes (legend reads it)

const shown = computed(() => {
  const xs = bars.value
  if (!xs.length) return null
  const i = hover.value ?? xs.length - 1
  const c = xs[i], prev = i > 0 ? xs[i - 1].c : c.o
  const poc = layer.value === 'foot' && footVer.value >= 0 ? foot?.barStats(chartTime(c.t) as number) ?? null : null
  return { c, chg: prev ? c.c / prev - 1 : 0, amp: prev ? (c.h - c.l) / prev : 0, f: flow.value.get(c.t), poc, oi: oiByT.value.get(c.t), live: hover.value == null }
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
    await loadOI(cs)
    if (fit) resetView()
    requestAnimationFrame(() => { if (chart) axisWidth.value = chart.priceScale('right').width() })
    err.value = ''
  } catch (e: any) {
    err.value = e.message
  }
}

// ---- OI overlay: own hidden scale in the price pane; legend shows the value ----
async function loadOI(cs: Candle[]) {
  if (!chart) return
  if (!withOI.value || !props.hasOi) {
    if (oi) { chart.removeSeries(oi); oi = undefined }
    oiByT.value = new Map()
    return
  }
  const step = barMs[bar.value], from = cs[0].t
  const hours = Math.min(24 * 30, Math.ceil((Date.now() - from) / 3600e3) + 1)
  const pts = (await api.series(props.id, 'oi', hours)) || []
  const m = new Map<number, number>()
  for (const p of pts) if (p.t >= from) m.set(Math.floor(p.t / step) * step, p.u ?? p.v) // ascending: last sample wins
  if (!oi) {
    oi = chart.addSeries(LineSeries, { color: '#7aa7ff', lineWidth: 2, priceScaleId: 'oi', priceFormat: { type: 'volume' }, lastValueVisible: false, priceLineVisible: false, crosshairMarkerVisible: false })
    chart.priceScale('oi').applyOptions({ scaleMargins: { top: 0.08, bottom: 0.3 } })
  }
  oi.setData([...m].map(([t, v]) => ({ time: chartTime(t), value: v })))
  oiByT.value = m
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
      vol!.update({ time: chartTime(c.t), value: c.q, color: c.c >= c.o ? 'rgba(38,166,154,0.35)' : 'rgba(239,83,80,0.35)' })
    }
    bars.value = xs
    emit('live', last.c)
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
  if (layer.value === 'foot') {
    // wide enough for numbers on PC; on a phone favour more bars (pinch to zoom in for numbers)
    ts.applyOptions({ barSpacing: Math.max(40, Math.min(72, w / 10)) })
    ts.scrollToRealTime()
  } else if (n) {
    const show = Math.min(n, Math.max(40, Math.round(w / 7))) // ~7px per candle: bodies stay visible
    ts.setVisibleLogicalRange({ from: n - show, to: n + 2 })
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
      foot.onRegroup = () => footVer.value++
      candles!.attachPrimitive(foot)
      candles!.applyOptions({ upColor: 'rgba(38,166,154,.18)', downColor: 'rgba(239,83,80,.18)' })
    }
    vol?.applyOptions({ visible: false }) // footprint already shows volume per level; the bars would cover it
    const fm = new Map<number, FootBar>()
    for (const b of of.bars || []) if (b.levels?.length && b.step) fm.set(chartTime(b.t) as number, { levels: b.levels, step: b.step })
    foot.setData(fm, cs.map(c => ({ time: chartTime(c.t) as number, low: c.l, high: c.h })))
    footVer.value++
  }
}

function clearFlow() {
  if (!chart) return
  if (foot) {
    candles!.detachPrimitive(foot)
    candles!.applyOptions({ upColor: UP, downColor: DOWN })
    vol?.applyOptions({ visible: true })
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
  hotTimer = window.setInterval(hot, 1000)
})
onUnmounted(() => { clearInterval(timer); clearInterval(hotTimer); chart?.remove() })
watch(bar, v => { localStorage.setItem('bar', v); load(true) })
watch(withSpot, v => { localStorage.setItem('ctg.chartSpot', v ? '1' : '0'); load() })
watch(withOI, v => { localStorage.setItem('ctg.chartOI', v ? '1' : '0'); load() })
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
        <b>足迹</b>：每根 K 线内每个价位的主动卖（左，红）/ 主动买（右，绿），颜色深浅按<b>当前可见范围</b>内最大的单价位成交额换算（跨 K 线可比）。价位档会随纵向缩放自动合并（×2、×10…），左上角显示当前每档大小；拖动右侧价格轴可纵向放大看更细的档位，横向放大到够宽时显示数字。有足迹的 K 线来自逐笔成交：活跃池、自选、正在查看的合约实时录制；打开详情页时自动回补保留期内（查看/活跃 3 天、自选 7 天）缺失的分钟——整天的用 OKX 官方每日逐笔成交文件（按北京时间分天，次日发布），当天尚未发布的部分用 OKX 历史成交接口补最近一段。只有一整根 K 线的每分钟都齐全才画足迹。<br />
        <b>POC / 价值区</b>：黄框为该根 K 线成交最多的价位（Point of Control）；每根 K 线左侧的细金线是价值区（从 POC 向两侧扩展、覆盖 70% 成交额的价格区间），数值见左上角。<b>未回补 POC</b>：已收盘 K 线的 POC 之后没有任何 K 线的最高–最低价穿过它，就用金色虚线画到最右侧，被穿过后不再显示——常用来观察价格是否会回来“回补”。<br />
        <b>失衡</b>（对角比较）：某价位的主动买 ≥ 低一档主动卖的 3 倍，在右侧标绿色竖条、数字变绿；主动卖 ≥ 高一档主动买的 3 倍，在左侧标红色竖条。量太小（低于该 K 线各价位单边成交额中位数）的不算。连续 3 档及以上同方向失衡为<b>堆叠失衡</b>，该侧加描边，常被当作主动方强势的信号，不是必然。<br />
        <b>成交额柱</b>：底部柱为该根 K 线的 USDT 成交额，绿涨红跌。缺失分钟显示为空档；最后一根为未收盘 K 线。<br />
        <b>现货 / OI</b>：叠加 OKX 同名 USDT 现货收盘价（黄线，左侧独立价格轴）或持仓量（蓝线，独立缩放，数值见左上角；更早的是 1 小时采样，近期逐分钟）。没有对应数据时按钮置灰。<br />
        <b>刷新</b>：打开详情页时最新几根 K 线每秒刷新（服务端 OKX WebSocket 推送的未收盘 K 线），整段数据和订单流每 10 秒刷新；页面切到后台时暂停。<br />
        <b>时间</b>：按设置页选择的时区显示（默认 UTC+8）。右下角 ◎ 或双击图表回到最新并自动缩放。
      </InfoTip></span>
      <span class="bars">
        <Segmented v-model="layer" size="sm" aria-label="图层" :options="[{ value: 'k', label: 'K 线' }, { value: 'delta', label: 'Delta' }, { value: 'foot', label: '足迹' }]" />
        <Segmented v-model="bar" size="sm" aria-label="K 线周期" :options="[{ value: '1m', label: '1m' }, { value: '5m', label: '5m' }, { value: '15m', label: '15m' }, { value: '1H', label: '1H' }, { value: '4H', label: '4H' }]" />
      </span>
      <span class="ovl">
        <FilterChip v-model="withSpot" label="现货" :disabled="!hasSpot" :title="hasSpot ? '叠加 OKX 同名 USDT 现货收盘价（黄线，左侧价格轴）' : '没有 OKX 同名 USDT 现货'" />
        <FilterChip v-model="withOI" label="OI" :disabled="!hasOi" :title="hasOi ? '叠加持仓量（蓝线，独立缩放，数值见左上角）' : '没有持仓量数据'" />
      </span>
    </h3>
    <div class="wrap" :class="{ tall: layer !== 'k' }">
      <div ref="el" class="chart" @dblclick="resetView"></div>
      <button type="button" class="reset" title="回到最新、自动缩放（PC 也可双击图表）" aria-label="重置视图" :style="{ right: axisWidth + 6 + 'px' }" @click="resetView"><LocateFixed :size="15" /></button>
      <div class="legend tnum" v-if="shown">
        <span class="muted">{{ fmtTime(shown.c.t, true) }}</span>
        <span>开 <b>{{ fmtPrice(shown.c.o) }}</b></span><span>高 <b>{{ fmtPrice(shown.c.h) }}</b></span>
        <span>低 <b>{{ fmtPrice(shown.c.l) }}</b></span><span>收 <b :class="tone(shown.c.c - shown.c.o)">{{ fmtPrice(shown.c.c) }}</b></span>
        <span>涨跌 <b :class="tone(shown.chg)">{{ pct(shown.chg) }}</b></span><span>振幅 <b>{{ pct(shown.amp) }}</b></span>
        <span>额 <b>{{ usd(shown.c.q) }}</b></span>
        <span v-if="withOI && shown.oi != null">OI <b class="oi">{{ usd(shown.oi) }}</b></span>
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
.reset { all: unset; box-sizing: border-box; position: absolute; bottom: 34px; z-index: 4; display: grid; place-items: center; width: 28px; height: 28px; border-radius: 6px;
  background: rgba(17, 21, 28, .8); border: 1px solid var(--line-2); color: var(--text-2); cursor: pointer; }
@media (hover: hover) { .reset:hover { color: var(--text-1); border-color: var(--accent); } }
.reset:active { background: var(--bg-3); }
.reset:focus-visible { outline: 2px solid var(--accent); }
.chart { height: 340px; }
.wrap.tall .chart { height: 450px; }
h3 { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 6px; }
.bars { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.ovl { display: flex; gap: 6px; align-items: center; }
.ovl :deep(.fchip) { height: 24px; padding: 0 10px; font-size: var(--fs-xs); font-weight: 500; }
.legend { position: absolute; left: 6px; top: 4px; max-width: calc(100% - 76px); z-index: 3; pointer-events: none; padding: 0 4px; border-radius: 4px; background: rgba(17, 21, 28, .75); font-size: var(--fs-xs); color: var(--text-3); line-height: 1.6; }
.legend span { margin-right: 8px; white-space: nowrap; }
.legend b { color: var(--text-1); font-weight: 500; }
.legend b.up { color: var(--up); } .legend b.down { color: var(--down); } .legend b.poc { color: #f5c542; } .legend b.oi { color: #7aa7ff; }
.axis-lbl { position: absolute; right: 0; z-index: 3; pointer-events: none; display: grid; justify-items: center; padding: 1px 0; background: #2b3242; border-radius: 2px; font-size: 10px; line-height: 15px; color: var(--text-1); }
.axis-lbl b { font-weight: 500; }
.axis-lbl .up { color: var(--up); } .axis-lbl .down { color: var(--down); }
@media (max-width: 900px) {
  .legend { max-width: calc(100% - 70px); font-size: 10px; }
  .chart { height: 320px; } .wrap.tall .chart { height: 420px; }
  /* two rows: title + spot toggle, then the layer/period switches */
  h3 { row-gap: 8px; } h3 > span:first-child { flex: 1; } .bars { order: 3; width: 100%; flex-wrap: nowrap; }
}
</style>

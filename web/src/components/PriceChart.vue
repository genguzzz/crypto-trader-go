<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { CandlestickSeries, LineSeries, HistogramSeries, type IChartApi, type ISeriesApi } from 'lightweight-charts'
import { api } from '../api'
import { chartTime } from '../fmt'
import { baseChart, precisionFor } from './charts'

const props = defineProps<{ id: string; hasSpot: boolean }>()
const bar = ref(localStorage.getItem('bar') || '15m')
const withSpot = ref(false)
const err = ref('')
const el = ref<HTMLElement>()
let chart: IChartApi | undefined
let candles: ISeriesApi<'Candlestick'> | undefined
let vol: ISeriesApi<'Histogram'> | undefined
let spot: ISeriesApi<'Line'> | undefined
let timer: number | undefined

async function load(fit = false) {
  try {
    const depth: Record<string, number> = { '1m': 1440, '5m': 2016, '15m': 672, '1H': 720, '4H': 180 }
    const cs = await api.candles(props.id, bar.value, depth[bar.value] || 400)
    if (!chart || !cs.length) return
    candles!.applyOptions({ priceFormat: precisionFor(cs[cs.length - 1].c) })
    candles!.setData(cs.map(c => ({ time: chartTime(c.t), open: c.o, high: c.h, low: c.l, close: c.c })))
    vol!.setData(cs.map(c => ({ time: chartTime(c.t), value: c.q, color: c.c >= c.o ? 'rgba(38,166,154,0.35)' : 'rgba(239,83,80,0.35)' })))
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
    if (fit) chart.timeScale().fitContent()
    err.value = ''
  } catch (e: any) {
    err.value = e.message
  }
}

onMounted(() => {
  chart = baseChart(el.value!)
  candles = chart.addSeries(CandlestickSeries, { upColor: '#26a69a', downColor: '#ef5350', borderVisible: false, wickUpColor: '#26a69a', wickDownColor: '#ef5350' })
  vol = chart.addSeries(HistogramSeries, { priceScaleId: 'vol', priceFormat: { type: 'volume' } })
  chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.82, bottom: 0 } })
  load(true)
  timer = window.setInterval(() => load(), 10_000)
})
onUnmounted(() => { clearInterval(timer); chart?.remove() })
watch(bar, v => { localStorage.setItem('bar', v); load(true) })
watch(withSpot, () => load())
</script>

<template>
  <div class="panel">
    <h3>
      合约 K 线（UTC 对齐，本地时间显示）
      <span class="bars">
        <button v-for="b in ['1m', '5m', '15m', '1H', '4H']" :key="b" :class="{ on: bar === b }" @click="bar = b">{{ b }}</button>
        <label class="spot" :class="{ muted: !hasSpot }" :title="hasSpot ? 'OKX 同名 USDT 现货，左侧独立价格轴' : '无 OKX USDT 现货映射'">
          <input type="checkbox" v-model="withSpot" :disabled="!hasSpot" /> 叠加现货
        </label>
      </span>
    </h3>
    <div ref="el" class="chart"></div>
    <div class="note" v-if="err">图表数据错误：{{ err }}</div>
    <div class="note">柱底为 USDT 成交额；缺失分钟显示为空档，不补价。1m/5m/15m 来自本机录制（进过池的币回补 7 天 1m），1H/4H 保留 30 天。</div>
  </div>
</template>

<style scoped>
.chart { height: 340px; }
h3 { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 6px; }
.bars { display: flex; gap: 4px; align-items: center; }
.bars button { padding: 1px 8px; font-size: 12px; }
.spot { font-weight: 400; font-size: 12px; margin-left: 6px; display: flex; align-items: center; gap: 4px; }
</style>

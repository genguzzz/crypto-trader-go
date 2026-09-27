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
      <span>合约 K 线 <InfoTip>
        <b>数据来源</b>：OKX 永续 1m K 线由本机录制（实时 WS + 启动回填）；进过监控池的币额外回补 7 天 1m；1H/4H 来自 1H K 线，保留 30 天。5m/15m/4H 由服务端按 UTC 对齐聚合。<br />
        <b>时间</b>：K 线按 UTC 切分，坐标轴显示你的本地时间。<br />
        <b>成交额柱</b>：底部柱为该根 K 线的 USDT 成交额（volCcyQuote），绿涨红跌。<br />
        <b>缺口</b>：缺失分钟显示为空档，不用前价补齐，避免把“没数据”画成“没波动”。最后一根为未收盘 K 线，会继续变化。<br />
        <b>叠加现货</b>：OKX 同名 USDT 现货收盘价（黄线），使用左侧独立价格轴；没有同名现货的合约不可叠加。
      </InfoTip></span>
      <span class="bars">
        <button v-for="b in ['1m', '5m', '15m', '1H', '4H']" :key="b" :class="{ on: bar === b }" @click="bar = b">{{ b }}</button>
        <label class="spot" :class="{ muted: !hasSpot }" :title="hasSpot ? 'OKX 同名 USDT 现货，左侧独立价格轴' : '无 OKX USDT 现货映射'">
          <input type="checkbox" v-model="withSpot" :disabled="!hasSpot" /> 叠加现货
        </label>
      </span>
    </h3>
    <div ref="el" class="chart"></div>
    <div class="note" v-if="err">图表数据错误：{{ err }}</div>
  </div>
</template>

<style scoped>
.chart { height: 340px; }
h3 { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 6px; }
.bars { display: flex; gap: 4px; align-items: center; }
.bars button { padding: 1px 8px; font-size: 12px; }
.spot { font-weight: 400; font-size: 12px; margin-left: 6px; display: flex; align-items: center; gap: 4px; }
</style>

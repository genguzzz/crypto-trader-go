<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { LineSeries, type IChartApi } from 'lightweight-charts'
import { api } from '../api'
import { chartTime } from '../fmt'
import { baseChart } from './charts'

const props = defineProps<{ id: string }>()
const bar = ref('15m')
const el = ref<HTMLElement>()
const legend = ref<{ id: string; color: string; last: number }[]>([])
let chart: IChartApi | undefined
const colors = ['#5b8def', '#f5a623', '#9b7df0']

async function load() {
  const r = await api.compare(props.id, bar.value, 300)
  if (!chart) return
  chart.remove()
  chart = baseChart(el.value!)
  legend.value = []
  r.ids.forEach((id, i) => {
    const s = chart!.addSeries(LineSeries, { color: colors[i], lineWidth: i === 0 ? 2 : 1, priceFormat: { type: 'price', precision: 2, minMove: 0.01 } })
    const pts = (r.points || []).map(p => ({ time: chartTime(p.t), value: p.v[id] }))
    s.setData(pts)
    legend.value.push({ id: id.replace('-USDT-SWAP', ''), color: colors[i], last: pts[pts.length - 1]?.value ?? 100 })
  })
  chart.timeScale().fitContent()
}
onMounted(() => { chart = baseChart(el.value!); load() })
onUnmounted(() => chart?.remove())
watch(bar, load)
</script>

<template>
  <div class="panel">
    <h3>
      <span>相对走势 <InfoTip>
        把该币、BTC、ETH 的永续收盘价都换算成<b>以共同起点 = 100</b> 的指数：值 105 表示自起点上涨 5%。<br />
        这样可以直接比较强弱，而不会因为价格量级不同（BTC 8 万 vs 小币 0.01）产生误读。<br />
        BTC/ETH 只是“大盘代理”，不是全市场指数。只画三者都有数据的时间点。
      </InfoTip></span>
      <Segmented v-model="bar" size="sm" aria-label="周期" :options="[{ value: '5m', label: '5m' }, { value: '15m', label: '15m' }, { value: '1H', label: '1H' }, { value: '4H', label: '4H' }]" />
    </h3>
    <div class="legend">
      <span v-for="l in legend" :key="l.id"><i :style="{ background: l.color }"></i>{{ l.id }} {{ l.last.toFixed(2) }}</span>
    </div>
    <div ref="el" class="chart"></div>
  </div>
</template>

<style scoped>
.chart { height: 200px; }
h3 { display: flex; justify-content: space-between; align-items: center; }
.bars { display: flex; gap: 4px; }
.legend { display: flex; gap: 12px; font-size: 12px; margin-bottom: 4px; }
.legend i { display: inline-block; width: 10px; height: 3px; margin-right: 4px; vertical-align: middle; }
</style>

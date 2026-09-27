<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { HistogramSeries, type IChartApi, type ISeriesApi } from 'lightweight-charts'
import { api } from '../../api'
import { chartTime } from '../../fmt'
import { baseChart } from '../charts'

/** Hourly USDT turnover bars; the last `windowH` hours (the 12h pool window) are highlighted. */
const props = withDefaults(defineProps<{ id: string; hours?: number; windowH?: number; height?: number }>(), { hours: 72, windowH: 12, height: 180 })
const el = ref<HTMLElement>()
let chart: IChartApi | undefined
let s: ISeriesApi<'Histogram'> | undefined

async function load() {
  const cs = await api.candles(props.id, '1H', props.hours)
  const cut = cs.length - props.windowH
  s?.setData(cs.map((c, i) => ({ time: chartTime(c.t), value: c.q, color: i >= cut ? '#4c8dff' : 'rgba(164,172,185,0.35)' })))
  chart?.timeScale().fitContent()
}
onMounted(() => {
  chart = baseChart(el.value!)
  s = chart.addSeries(HistogramSeries, { priceFormat: { type: 'volume' } })
  load()
})
onUnmounted(() => chart?.remove())
watch(() => [props.id, props.hours], load)
</script>

<template><div ref="el" :style="{ height: height + 'px' }"></div></template>

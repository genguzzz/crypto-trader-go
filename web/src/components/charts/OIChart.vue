<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { LineSeries, type IChartApi, type ISeriesApi } from 'lightweight-charts'
import { api } from '../../api'
import { chartTime } from '../../fmt'
import { baseChart } from '../charts'

/** Open interest over time; unit coin (oiCcy, default) or USD. Shared by the OI panel and OI detail. */
const props = withDefaults(defineProps<{ id: string; hours?: number; height?: number; unit?: 'ccy' | 'usd' }>(), { hours: 24 * 7, height: 160, unit: 'ccy' })
const emit = defineEmits<{ loaded: [n: number] }>()
const el = ref<HTMLElement>()
let chart: IChartApi | undefined
let s: ISeriesApi<'Line'> | undefined

async function load() {
  const pts = (await api.series(props.id, 'oi', props.hours)) || []
  const data: { time: any; value: number }[] = []
  for (const p of pts) {
    const t = chartTime(p.t), v = props.unit === 'usd' ? p.u || 0 : p.v
    if (!data.length || t > data[data.length - 1].time) data.push({ time: t, value: v })
  }
  s?.setData(data)
  chart?.timeScale().fitContent()
  emit('loaded', data.length)
}
onMounted(() => {
  chart = baseChart(el.value!)
  s = chart.addSeries(LineSeries, { color: '#9b7df0', lineWidth: 2, priceFormat: { type: 'volume' } })
  load()
})
onUnmounted(() => chart?.remove())
watch(() => [props.id, props.hours, props.unit], load)
</script>

<template><div ref="el" :style="{ height: height + 'px' }"></div></template>

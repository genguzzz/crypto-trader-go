<script setup lang="ts">
import { useBusy } from '../../composables/busy'
import Loading from '../ui/Loading.vue'
import { C } from '../../theme'
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { HistogramSeries, type IChartApi, type ISeriesApi } from 'lightweight-charts'
import { api } from '../../api'
import { chartTime } from '../../fmt'
import { baseChart } from '../charts'

/** Settled funding rates (per settlement, %). Shared by the funding panel and funding detail. */
const props = withDefaults(defineProps<{ id: string; days?: number; height?: number }>(), { days: 14, height: 160 })
const emit = defineEmits<{ loaded: [rates: { t: number; v: number }[]] }>()
const el = ref<HTMLElement>()
let chart: IChartApi | undefined
let s: ISeriesApi<'Histogram'> | undefined

const { busy, track } = useBusy()
const load = track(loadData)
async function loadData() {
  const pts = (await api.series(props.id, 'funding', props.days * 24)) || []
  const data: { time: any; value: number; color: string }[] = []
  for (const p of pts) {
    const t = chartTime(p.t)
    if (!data.length || t > data[data.length - 1].time) data.push({ time: t, value: p.v * 100, color: p.v >= 0 ? C.up : C.down })
  }
  s?.setData(data)
  chart?.timeScale().fitContent()
  emit('loaded', pts)
}
onMounted(() => {
  chart = baseChart(el.value!)
  s = chart.addSeries(HistogramSeries, { priceFormat: { type: 'percent', precision: 4, minMove: 0.0001 } })
  load()
})
onUnmounted(() => chart?.remove())
watch(() => [props.id, props.days], load)
</script>

<template><div class="lwrap"><div ref="el" :style="{ height: height + 'px' }"></div><Loading overlay :show="busy" /></div></template>

<style scoped>
.lwrap { position: relative; }
</style>

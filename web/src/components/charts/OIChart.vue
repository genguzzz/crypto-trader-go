<script setup lang="ts">
import { useBusy } from '../../composables/busy'
import Loading from '../ui/Loading.vue'
import { C } from '../../theme'
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

const { busy, track } = useBusy()
const load = track(loadData)
async function loadData() {
  // recorded per minute; ≤ ~2000 points is plenty for this width (7 days → 5 min, 1 day → as recorded)
  const min = Math.ceil((props.hours * 60) / 2016)
  const pts = (await api.series(props.id, 'oi', props.hours, min > 1 ? min * 60e3 : 0)) || []
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
  s = chart.addSeries(LineSeries, { color: C.violet, lineWidth: 2, priceFormat: { type: 'volume' } })
  load()
})
onUnmounted(() => chart?.remove())
watch(() => [props.id, props.hours, props.unit], load)
</script>

<template><div class="lwrap"><div ref="el" :style="{ height: height + 'px' }"></div><Loading overlay :show="busy" /></div></template>

<style scoped>
.lwrap { position: relative; }
</style>

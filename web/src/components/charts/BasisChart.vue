<script setup lang="ts">
import { useBusy } from '../../composables/busy'
import Loading from '../ui/Loading.vue'
import { C } from '../../theme'
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { LineSeries, type IChartApi, type ISeriesApi } from 'lightweight-charts'
import { api } from '../../api'
import { chartTime } from '../../fmt'
import { baseChart } from '../charts'

/** Perp vs OKX spot basis over time: (perp close / spot close − 1) in bps, per bar. */
const props = withDefaults(defineProps<{ id: string; bar?: string; limit?: number; height?: number }>(), { bar: '15m', limit: 400, height: 180 })
const el = ref<HTMLElement>()
const err = ref('')
let chart: IChartApi | undefined
let s: ISeriesApi<'Line'> | undefined

const { busy, track } = useBusy()
const load = track(loadData)
async function loadData() {
  try {
    const [perp, spot] = await Promise.all([api.candles(props.id, props.bar, props.limit), api.candles(props.id, props.bar, props.limit, 'spot')])
    const sp = new Map(spot.map(c => [c.t, c.c]))
    const data = perp.filter(c => sp.has(c.t)).map(c => ({ time: chartTime(c.t), value: (c.c / sp.get(c.t)! - 1) * 1e4 }))
    s?.setData(data)
    chart?.timeScale().fitContent()
    err.value = ''
  } catch (e: any) { err.value = e.message }
}
onMounted(() => {
  chart = baseChart(el.value!)
  s = chart.addSeries(LineSeries, { color: C.amber, lineWidth: 2, priceFormat: { type: 'price', precision: 1, minMove: 0.1 } })
  s.createPriceLine({ price: 0, color: C.text, lineWidth: 1, lineStyle: 2, axisLabelVisible: false, title: '' })
  load()
})
onUnmounted(() => chart?.remove())
watch(() => [props.id, props.bar], load)
</script>

<template>
  <div>
    <div class="lwrap"><div ref="el" :style="{ height: height + 'px' }"></div><Loading overlay :show="busy" /></div>
    <div v-if="err" class="note">{{ err }}</div>
  </div>
</template>

<style scoped>
.lwrap { position: relative; }
</style>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { AreaSeries, LineSeries, type IChartApi, type ISeriesApi, type IPriceLine } from 'lightweight-charts'
import { baseChart } from '../charts'
import { C, alpha } from '../../theme'
import { chartTime } from '../../fmt'

/**
 * Account equity curve (USDT) with the deposit as a dashed baseline; `drawdown` adds a lower pane
 * with the running drawdown from the equity peak (%). Data refreshes keep the visible range.
 */
const props = withDefaults(defineProps<{ points: [number, number][]; deposit?: number; drawdown?: boolean; height?: number }>(), { height: 260 })
const el = ref<HTMLElement>()
let chart: IChartApi | undefined
let eq: ISeriesApi<'Line'> | undefined
let dd: ISeriesApi<'Area'> | undefined
let base: IPriceLine | undefined
let fitted = false

function dedupe(pts: [number, number][]) {
  // lightweight-charts needs strictly increasing times (seconds)
  const out: { time: any; value: number }[] = []
  let last = -Infinity
  for (const [t, v] of pts) {
    const s = chartTime(t)
    if (s > last && isFinite(v)) { out.push({ time: s, value: v }); last = s }
  }
  return out
}

function render() {
  if (!chart || !eq) return
  const data = dedupe(props.points)
  eq.setData(data)
  if (base) { eq.removePriceLine(base); base = undefined }
  if (props.deposit) base = eq.createPriceLine({ price: props.deposit, color: C.text, lineWidth: 1, lineStyle: 2, axisLabelVisible: true, title: '入金' })
  if (dd) {
    let peak = -Infinity
    dd.setData(data.map(p => { peak = Math.max(peak, p.value); return { time: p.time, value: peak > 0 ? (p.value / peak - 1) * 100 : 0 } }))
  }
  if (!fitted && data.length) { chart.timeScale().fitContent(); fitted = true }
}

onMounted(() => {
  chart = baseChart(el.value!, { rightPriceScale: { borderColor: C.border, scaleMargins: { top: 0.1, bottom: 0.08 } } })
  eq = chart.addSeries(LineSeries, { color: C.lime, lineWidth: 2, priceFormat: { type: 'price', precision: 2, minMove: 0.01 }, lastValueVisible: true })
  if (props.drawdown) {
    dd = chart.addSeries(AreaSeries, {
      lineColor: C.down, topColor: alpha('down', 0.05), bottomColor: alpha('down', 0.35), lineWidth: 1, invertFilledArea: true,
      priceFormat: { type: 'price', precision: 2, minMove: 0.01 }, title: '回撤%',
    }, 1)
    chart.panes()[1]?.setStretchFactor(0.35)
  }
  render()
})
onUnmounted(() => chart?.remove())
watch(() => [props.points, props.deposit], render)
</script>

<template>
  <div ref="el" class="eqc" :style="{ height: height + 'px' }"></div>
</template>

<style scoped>
.eqc { width: 100%; min-width: 0; }
</style>

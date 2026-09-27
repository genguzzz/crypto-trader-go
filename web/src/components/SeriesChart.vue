<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { LineSeries, HistogramSeries, type IChartApi } from 'lightweight-charts'
import { api } from '../api'
import { chartTime } from '../fmt'
import { baseChart } from './charts'

const props = defineProps<{ id: string }>()
const oiEl = ref<HTMLElement>()
const fEl = ref<HTMLElement>()
const oiNote = ref('')
const fNote = ref('')
let c1: IChartApi | undefined, c2: IChartApi | undefined

onMounted(async () => {
  c1 = baseChart(oiEl.value!)
  c2 = baseChart(fEl.value!)
  const [oi, f] = await Promise.all([api.series(props.id, 'oi', 24 * 7), api.series(props.id, 'funding', 24 * 14)])
  const s1 = c1.addSeries(LineSeries, { color: '#9b7df0', lineWidth: 2, priceFormat: { type: 'volume' } })
  const oiPts = (oi || []).map(p => ({ time: chartTime(p.t), value: p.v }))
  s1.setData(dedupe(oiPts))
  oiNote.value = oiPts.length ? `${oiPts.length} 个样本（rubik 1H 历史 + 5m 近 8h + 每分钟录制）` : '暂无 OI 记录（只对可能进池的合约回补历史）'
  c1.timeScale().fitContent()
  const s2 = c2.addSeries(HistogramSeries, { priceFormat: { type: 'percent', precision: 4, minMove: 0.0001 } })
  const fp = (f || []).map(p => ({ time: chartTime(p.t), value: p.v * 100, color: p.v >= 0 ? '#26a69a' : '#ef5350' }))
  s2.setData(dedupe(fp))
  fNote.value = fp.length ? `${fp.length} 次已结算（单次费率，%）` : '暂无已结算资金费记录'
  c2.timeScale().fitContent()
})
onUnmounted(() => { c1?.remove(); c2?.remove() })

function dedupe<T extends { time: any }>(xs: T[]) {
  const out: T[] = []
  for (const x of xs) if (!out.length || x.time > out[out.length - 1].time) out.push(x)
  return out
}
</script>

<template>
  <div class="grid">
    <div class="panel">
      <h3>持仓量 oiCcy（币本位，7 天）</h3>
      <div ref="oiEl" class="chart"></div>
      <div class="note">{{ oiNote }} · OI 是多空双方共同未平仓，不代表方向</div>
    </div>
    <div class="panel">
      <h3>资金费（已结算，近 14 天）</h3>
      <div ref="fEl" class="chart"></div>
      <div class="note">{{ fNote }} · 4h/8h 结算间隔不同，比较时看“/小时”字段</div>
    </div>
  </div>
</template>

<style scoped>
.grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.chart { height: 160px; }
@media (max-width: 900px) { .grid { grid-template-columns: 1fr; } }
</style>

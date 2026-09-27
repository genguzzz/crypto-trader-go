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
      <h3>持仓量 OI（币本位，7 天） <InfoTip>
        <b>口径</b>：oiCcy = 以标的币数量计的未平仓量。用币数而不是美元，是为了排除“价格上涨导致美元持仓变大”的假象。<br />
        <b>数据</b>：OKX rubik 1H 历史 + 近 8 小时 5m + 本机每分钟录制。{{ oiNote }}<br />
        <b>注意</b>：OI 是多空双方共同的未平仓合约，OI 增加只说明“新开仓多于平仓”，<b>不能</b>说明是多头还是空头在加仓。
      </InfoTip></h3>
      <div ref="oiEl" class="chart"></div>
    </div>
    <div class="panel">
      <h3>资金费（已结算，近 14 天） <InfoTip>
        每根柱是一次<b>已结算</b>的资金费率（单次，%）。正值 = 多头付给空头，负值 = 空头付给多头。<br />
        OKX 不同合约结算间隔不同（4h 或 8h），跨币比较请看事实面板里的“资金费/小时”。{{ fNote }}
      </InfoTip></h3>
      <div ref="fEl" class="chart"></div>
    </div>
  </div>
</template>

<style scoped>
.grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.chart { height: 160px; }
@media (max-width: 900px) { .grid { grid-template-columns: 1fr; } }
</style>

<script setup lang="ts">
import { computed, ref } from 'vue'
import KpiRow from '../ui/KpiRow.vue'
import OIChart from '../charts/OIChart.vue'
import { usd, logPct } from '../../fmt'
import type { DetailCtx } from './types'
const props = defineProps<{ ctx: DetailCtx }>()
const unit = ref<'ccy' | 'usd'>('ccy')
const days = ref(7)
const tone = (v?: number) => (v == null ? '' : v > 0 ? 'up' : v < 0 ? 'down' : '') as '' | 'up' | 'down'
const items = computed(() => {
  const v = props.ctx.row?.values, i = props.ctx.info
  return [
    { label: '持仓量（USD）', value: usd(i.oiUsd), sub: '名义价值' },
    { label: '持仓量（币）', value: usd(i.oiCcy), sub: '币本位 oiCcy' },
    { label: '1h 变化', value: logPct(v?.oi_chg_1h), tone: tone(v?.oi_chg_1h), sub: '币本位' },
    { label: '4h 变化', value: logPct(v?.oi_chg_4h), tone: tone(v?.oi_chg_4h), sub: '币本位' },
    { label: 'OI / 12h 成交额', value: props.ctx.row?.turnover12h ? (i.oiUsd / props.ctx.row.turnover12h).toFixed(2) : '—', sub: '越高 = 持仓相对成交越重' },
  ]
})
</script>

<template>
  <div class="dv">
    <KpiRow :items="items" />
    <div class="ch">
      <div class="bar">
        <span class="t">持仓量走势 <InfoTip>OI 是多空双方共同的未平仓合约数。币本位变化排除了价格涨跌对美元持仓的影响；OI 上升只说明新开仓多于平仓，<b>不代表</b>多头或空头哪一方在加仓。</InfoTip></span>
        <span class="seg">
          <button :class="{ on: unit === 'ccy' }" @click="unit = 'ccy'">币</button><button :class="{ on: unit === 'usd' }" @click="unit = 'usd'">USD</button>
        </span>
        <span class="seg">
          <button v-for="d in [1, 3, 7]" :key="d" :class="{ on: days === d }" @click="days = d">{{ d }}天</button>
        </span>
      </div>
      <OIChart :id="ctx.id" :unit="unit" :hours="days * 24" :height="240" />
    </div>
  </div>
</template>

<style scoped>
.dv { display: grid; gap: 14px; }
.bar { display: flex; gap: 8px; align-items: center; margin-bottom: 6px; flex-wrap: wrap; } .bar .t { font-weight: 600; margin-right: auto; }
.seg { display: inline-flex; } .seg button { border-radius: 0; margin-left: -1px; padding: 2px 10px; font-size: var(--fs-sm); }
.seg button:first-child { border-radius: 6px 0 0 6px; } .seg button:last-child { border-radius: 0 6px 6px 0; }
</style>

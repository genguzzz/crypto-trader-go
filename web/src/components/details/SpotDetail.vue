<script setup lang="ts">
import { computed, ref } from 'vue'
import KpiRow from '../ui/KpiRow.vue'
import BasisChart from '../charts/BasisChart.vue'
import { price, fieldValue } from '../../fmt'
import { store } from '../../store'
import type { DetailCtx } from './types'
const props = defineProps<{ ctx: DetailCtx }>()
const bar = ref('15m')
const perp = computed(() => store.prices[props.ctx.id]?.[0] ?? props.ctx.info.last)
const items = computed(() => {
  const b = props.ctx.row?.values?.basis
  return [
    { label: 'OKX 现货', value: price(props.ctx.info.spotLast), sub: props.ctx.info.spotId || '' },
    { label: '永续', value: price(perp.value), sub: props.ctx.id },
    { label: '基差', value: fieldValue('basis', b), tone: (b == null ? '' : b > 0 ? 'up' : 'down') as '' | 'up' | 'down', sub: '永续相对现货' },
    { label: '基差（bps）', value: b != null ? ((Math.exp(b) - 1) * 1e4).toFixed(1) : '—', sub: '1 bps = 0.01%' },
  ]
})
</script>

<template>
  <div class="dv" v-if="ctx.info.spotId">
    <KpiRow :items="items" />
    <div>
      <div class="bar">
        <span class="t">期现基差走势（bps） <InfoTip>基差 = 永续收盘 ÷ 同时刻 OKX 同名 USDT 现货收盘 − 1。正值 = 永续比现货贵（多头情绪/资金费偏正时常见），负值相反。只是 OKX 一家现货，不代表全市场。</InfoTip></span>
        <Segmented v-model="bar" size="sm" aria-label="周期" :options="[{ value: '5m', label: '5m' }, { value: '15m', label: '15m' }, { value: '1H', label: '1H' }]" />
      </div>
      <BasisChart :id="ctx.id" :bar="bar" :height="220" />
    </div>
  </div>
  <div v-else class="note">该合约没有 OKX 同名 USDT 现货（股票/商品永续或未上现货的币），无法计算基差。</div>
</template>

<style scoped>
.dv { display: grid; gap: 14px; }
.bar { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; } .bar .t { font-weight: 600; margin-right: auto; }
</style>

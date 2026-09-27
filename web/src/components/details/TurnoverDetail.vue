<script setup lang="ts">
import { computed } from 'vue'
import KpiRow from '../ui/KpiRow.vue'
import TurnoverChart from '../charts/TurnoverChart.vue'
import { store } from '../../store'
import { usd } from '../../fmt'
import type { DetailCtx } from './types'
const props = defineProps<{ ctx: DetailCtx }>()
const items = computed(() => {
  const r = props.ctx.row, th = store.snap?.threshold || 0
  const vs = r?.values?.vol_surprise_4h
  return [
    { label: '12h 成交额', value: usd(r?.turnover12h) + ' USDT', sub: r?.active ? `池内第 ${r.rank} 名` : '未进监控池' },
    { label: '相对阈值', value: th && r?.turnover12h ? '×' + (r.turnover12h / th).toFixed(2) : '—', sub: `阈值 ${usd(th)} USDT` },
    { label: '4h 放量', value: vs != null ? '×' + Math.exp(vs).toFixed(2) : '—', sub: '对 7 日 4h 中位数' },
    { label: '池内分位', value: r?.pct?.turnover12h != null ? r.pct.turnover12h.toFixed(0) : '—', sub: '0 最低 · 100 最高' },
  ]
})
</script>

<template>
  <div class="dv">
    <KpiRow :items="items" />
    <div class="ch">
      <span class="t">每小时 USDT 成交额（72h，蓝色 = 当前 12h 窗口）
        <InfoTip>12h 成交额 = 最近 720 根已确认 1m K 线 volCcyQuote 之和，严格大于阈值才进监控池。这里按小时汇总展示，便于看成交是否集中在某一段。</InfoTip></span>
      <TurnoverChart :id="ctx.id" :height="220" />
    </div>
  </div>
</template>

<style scoped>
.dv { display: grid; gap: 14px; } .ch .t { display: block; font-weight: 600; margin-bottom: 6px; }
</style>

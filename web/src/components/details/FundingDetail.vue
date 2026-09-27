<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import KpiRow from '../ui/KpiRow.vue'
import FundingChart from '../charts/FundingChart.vue'
import { time } from '../../fmt'
import type { DetailCtx } from './types'
const props = defineProps<{ ctx: DetailCtx }>()
const now = ref(Date.now())
let t: number | undefined
onMounted(() => (t = window.setInterval(() => (now.value = Date.now()), 1000)))
onUnmounted(() => clearInterval(t))
const hist = ref<{ t: number; v: number }[]>([])
const pct = (v?: number | null, d = 4) => (v == null || !isFinite(v) ? '—' : (v * 100).toFixed(d) + '%')
const tone = (v?: number | null) => (v == null ? '' : v > 0 ? 'up' : v < 0 ? 'down' : '') as '' | 'up' | 'down'
const items = computed(() => {
  const i = props.ctx.info, iv = i.fundingIntervalH || 8, v = props.ctx.row?.values
  const s = Math.max(0, Math.floor((i.fundingTime - now.value) / 1000))
  const cd = `${String(Math.floor(s / 3600)).padStart(2, '0')}:${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
  const avg7 = hist.value.filter(x => x.t > now.value - 7 * 864e5)
  const mean = avg7.length ? avg7.reduce((a, b) => a + b.v, 0) / avg7.length : null
  return [
    { label: '本期费率', value: pct(i.fundingRate), tone: tone(i.fundingRate), sub: `${iv}h 结算 · ${time(i.fundingTime)}` },
    { label: '距结算', value: cd, sub: '倒计时' },
    { label: '折合每小时', value: pct(i.fundingRate / iv, 5), tone: tone(i.fundingRate), sub: '跨币比较用' },
    { label: '年化', value: pct((i.fundingRate * 24 * 365) / iv, 1), tone: tone(i.fundingRate), sub: '按本期费率简单年化' },
    { label: '上期已结算', value: pct(v?.funding_hourly != null ? v.funding_hourly * iv : null), tone: tone(v?.funding_hourly), sub: '评分用这一项' },
    { label: '7 日均值', value: pct(mean), tone: tone(mean), sub: `${avg7.length} 次结算` },
  ]
})
</script>

<template>
  <div class="dv">
    <KpiRow :items="items" />
    <div>
      <span class="t">已结算资金费（30 天，单次 %）
        <InfoTip>正值 = 多头付给空头，负值 = 空头付给多头。OKX 各合约结算间隔不同（4h/8h），比较时看“折合每小时”。年化只是按本期费率简单换算，不代表未来。</InfoTip></span>
      <FundingChart :id="ctx.id" :days="30" :height="220" @loaded="r => (hist = r)" />
    </div>
  </div>
</template>

<style scoped>.dv { display: grid; gap: 14px; } .t { display: block; font-weight: 600; margin-bottom: 6px; }</style>

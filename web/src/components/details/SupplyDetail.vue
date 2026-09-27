<script setup lang="ts">
import { computed } from 'vue'
import KpiRow from '../ui/KpiRow.vue'
import { usd, time } from '../../fmt'
import type { DetailCtx } from './types'
const props = defineProps<{ ctx: DetailCtx }>()
const c = computed(() => props.ctx.info.coin)
const qty = (v?: number) => (v ? usd(v) : '—')
const ratio = (a?: number, b?: number) => (a && b ? ((a / b) * 100).toFixed(1) + '%' : '—')
const items = computed(() => {
  const x = c.value!
  return [
    { label: '总市值', value: usd(x.marketCap), sub: x.rank ? `CoinGecko 第 ${x.rank} 名` : '' },
    { label: '完全稀释估值 FDV', value: usd(x.fdv), sub: x.fdv && x.marketCap ? `市值 / FDV ${ratio(x.marketCap, x.fdv)}` : '' },
    { label: '已流通 / 总量', value: ratio(x.circulating, x.total), sub: `${qty(x.circulating)} / ${qty(x.total)} 枚` },
    { label: '已流通 / 最大供应', value: x.max ? ratio(x.circulating, x.max) : '无上限', sub: x.max ? `最大 ${qty(x.max)} 枚` : '最大供应未设定' },
  ]
})
</script>

<template>
  <div class="dv" v-if="c">
    <KpiRow :items="items" />
    <p class="note">
      <b>未解锁</b>约为 总量 − 流通量（{{ c.total && c.circulating ? usd(c.total - c.circulating) + ' 枚，' + ratio(c.total - c.circulating, c.total) : '—' }}），按现价折合约 {{ c.total && c.circulating && c.marketCap ? usd((c.total - c.circulating) * (c.marketCap / c.circulating)) + ' USD' : '—' }}。
      <InfoTip>数据来自 CoinGecko 公共接口（{{ c.name }} / {{ c.id }}），约每 2 小时刷新，最近更新 {{ c.updatedAt ? time(Date.parse(c.updatedAt), true) : '—' }}。按 OKX 币种符号匹配，同名币取市值最大的一个，极少数小币可能对错。解锁比例 = 已流通 ÷ 总量；具体解锁日程（下一次解锁时间与数量）CoinGecko 免费接口不提供。只用于展示，不参与监控池与评分。</InfoTip>
    </p>
  </div>
  <div v-else class="note">{{ ctx.info.category === '1' ? '没有找到该币的市值数据（CoinGecko 前 1500 名之外或符号不匹配），或数据还在加载。' : '股票 / 商品类永续没有代币市值与解锁数据。' }}</div>
</template>

<style scoped>
.dv { display: grid; gap: 12px; }
</style>

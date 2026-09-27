<script setup lang="ts">
import { computed, ref } from 'vue'
import { Activity } from 'lucide-vue-next'
import { api } from '../../api'
import { usePolling } from '../../composables/polling'
import { cumulative, sweepTo } from './depth'
import { usd, price, ago } from '../../fmt'
import DepthChart from '../charts/DepthChart.vue'

/** Live order book (REST snapshot every second): summary, USDT depth chart, depth-at-% table, top-10 ladder. */
const props = withDefaults(defineProps<{ id: string; compact?: boolean }>(), { compact: false })
const { data: book, error } = usePolling(() => api.orderbook(props.id), 1000)

const ranges = [0.5, 1, 2, 5]
const pick = ref(Number(localStorage.getItem('ctg.depthRange')) || 2)
function setRange(r: number) { pick.value = r; localStorage.setItem('ctg.depthRange', String(r)) }
// OKX publishes at most 5000 levels per side; a range is usable when the book reaches half of it
const cover = computed(() => (book.value?.levels ? Math.min(book.value.coverBid, book.value.coverAsk) : Infinity))
const usable = (r: number) => cover.value >= r * 0.5
const range = computed(() => (usable(pick.value) ? pick.value : [...ranges].reverse().find(usable) ?? ranges[0]))
const rangeOpts = computed(() => ranges.map(r => ({ value: r, label: `±${r}%`, disabled: !usable(r), title: usable(r) ? undefined : `盘口数据只覆盖约 ±${cover.value.toFixed(2)}%` })))

const bids = computed(() => (book.value ? cumulative(book.value.bids, book.value.mid, book.value.ctVal) : []))
const asks = computed(() => (book.value ? cumulative(book.value.asks, book.value.mid, book.value.ctVal) : []))
const table = computed(() => {
  const b = book.value
  if (!b) return []
  return [0.1, 0.5, 1, 2, 5].map(p => {
    const up = sweepTo(p, bids.value, asks.value, b.mid)
    const dn = sweepTo(-p, bids.value, asks.value, b.mid)
    return { p, buy: up, sell: dn, ratio: up.usdt + dn.usdt > 0 ? dn.usdt / (up.usdt + dn.usdt) : 0.5 }
  })
})
const depth1 = computed(() => table.value.find(r => r.p === 1))
const ladderN = computed(() => (props.compact ? 6 : 10))
const ladderAsks = computed(() => (book.value?.asks.slice(0, ladderN.value) || []).map((l, i) => ({ l, cum: asks.value[i]?.cum || 0 })).reverse())
const ladderBids = computed(() => (book.value?.bids.slice(0, ladderN.value) || []).map((l, i) => ({ l, cum: bids.value[i]?.cum || 0 })))
const ladderMax = computed(() => Math.max(ladderAsks.value[0]?.cum || 0, ladderBids.value[ladderBids.value.length - 1]?.cum || 0, 1))
</script>

<template>
  <div class="ob" :class="{ compact }">
    <div v-if="error && !book" class="note">盘口获取失败：{{ error }}</div>
    <template v-if="book">
      <div class="summary">
        <div><span class="l">买一</span><b class="up tnum">{{ price(book.bestBid) }}</b></div>
        <div><span class="l">卖一</span><b class="down tnum">{{ price(book.bestAsk) }}</b></div>
        <div><span class="l">中间价</span><b class="tnum">{{ price(book.mid) }}</b></div>
        <div><span class="l">点差</span><b class="tnum">{{ book.spreadBps.toFixed(1) }} bps</b></div>
        <div v-if="depth1" class="imb">
          <span class="l">±1% 深度 买 {{ usd(depth1.sell.usdt) }} / 卖 {{ usd(depth1.buy.usdt) }}</span>
          <span class="bar"><i :style="{ width: depth1.ratio * 100 + '%' }"></i></span>
        </div>
        <span class="live"><Activity :size="12" /> {{ ago(book.ts) }} 前 · 每秒刷新</span>
      </div>

      <div class="depth-head">
        <span class="t">深度图 <small>USDT 累计 · 悬浮查看推动价格所需资金<template v-if="book.levels"> · 数据覆盖 −{{ book.coverBid.toFixed(2) }}% / +{{ book.coverAsk.toFixed(2) }}%（{{ book.levels }} 档）</template></small></span>
        <Segmented :model-value="range" size="sm" aria-label="深度范围" :options="rangeOpts" @update:model-value="setRange" />
      </div>
      <DepthChart :bids="bids" :asks="asks" :mid="book.mid" :range="range" :height="compact ? 200 : 260" />

      <div class="grid2">
        <table class="dtab">
          <thead><tr><th>价格偏离</th><th class="num">买入推高需</th><th class="num">卖出压低需</th><th class="num">均价滑点 买/卖</th></tr></thead>
          <tbody>
            <tr v-for="r in table" :key="r.p">
              <td>±{{ r.p }}%</td>
              <td class="num up">{{ usd(r.buy.usdt) }}<span v-if="r.buy.beyondData" class="gt" title="超出 OKX 公开盘口深度（每侧最多 5000 档），实际需要更多">+</span></td>
              <td class="num down">{{ usd(r.sell.usdt) }}<span v-if="r.sell.beyondData" class="gt" title="超出 OKX 公开盘口深度（每侧最多 5000 档），实际需要更多">+</span></td>
              <td class="num sub">{{ r.buy.slipPct.toFixed(2) }}% / {{ r.sell.slipPct.toFixed(2) }}%</td>
            </tr>
          </tbody>
        </table>

        <div class="ladder">
          <div class="lh"><span>价格</span><span class="num">USDT</span><span class="num">累计</span></div>
          <div v-for="(x, i) in ladderAsks" :key="'a' + i" class="lr ask">
            <i :style="{ width: (x.cum / ladderMax) * 100 + '%' }"></i>
            <span class="down tnum">{{ price(x.l[0]) }}</span><span class="num">{{ usd(x.l[1]) }}</span><span class="num sub">{{ usd(x.cum) }}</span>
          </div>
          <div class="mid tnum">{{ price(book.mid) }} <small>{{ book.spreadBps.toFixed(1) }} bps</small></div>
          <div v-for="(x, i) in ladderBids" :key="'b' + i" class="lr bid">
            <i :style="{ width: (x.cum / ladderMax) * 100 + '%' }"></i>
            <span class="up tnum">{{ price(x.l[0]) }}</span><span class="num">{{ usd(x.l[1]) }}</span><span class="num sub">{{ usd(x.cum) }}</span>
          </div>
        </div>
      </div>
    </template>
    <div v-else-if="!error" class="note">加载盘口…</div>
  </div>
</template>

<style scoped>
.ob { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; min-width: 0; }
.summary { display: flex; flex-wrap: wrap; gap: 8px 20px; align-items: flex-end; }
.summary > div { display: grid; }
.summary .l { font-size: var(--fs-xs); color: var(--text-3); }
.summary b { font-size: var(--fs-lg); font-weight: 600; }
.imb { min-width: 160px; flex: 1; }
.imb .bar { display: block; height: 4px; margin-top: 6px; background: var(--down); border-radius: 2px; overflow: hidden; }
.imb .bar i { display: block; height: 100%; background: var(--up); }
.live { margin-left: auto; font-size: var(--fs-xs); color: var(--text-3); display: inline-flex; align-items: center; gap: 4px; }
.depth-head { display: flex; justify-content: space-between; align-items: center; gap: 8px; flex-wrap: wrap; }
.depth-head .t { font-weight: 600; } .depth-head small { font-weight: 400; color: var(--text-3); margin-left: 6px; }
.grid2 { display: grid; grid-template-columns: 1.2fr 1fr; gap: 16px; align-items: start; }
.dtab td { height: 30px; } .dtab .gt { color: var(--warn); margin-left: 1px; }
.ladder { font-size: var(--fs-sm); }
.lh, .lr { display: grid; grid-template-columns: 1.1fr 1fr 1fr; gap: 8px; padding: 0 6px; }
.lh { color: var(--text-3); font-size: var(--fs-xs); padding-bottom: 4px; }
.lr { position: relative; height: 22px; align-items: center; }
.lr i { position: absolute; right: 0; top: 2px; bottom: 2px; border-radius: 2px; pointer-events: none; }
.lr.ask i { background: var(--down-bg); } .lr.bid i { background: var(--up-bg); }
.lr span { position: relative; }
.mid { text-align: center; font-weight: 700; font-size: var(--fs-lg); padding: 4px 0; border-top: 1px solid var(--line); border-bottom: 1px solid var(--line); margin: 2px 0; }
.mid small { font-size: var(--fs-xs); color: var(--text-3); font-weight: 400; }
@media (max-width: 900px) { .grid2 { grid-template-columns: 1fr; } }
.compact .grid2 { grid-template-columns: 1fr 1fr; }
@media (max-width: 900px) { .compact .grid2 { grid-template-columns: 1fr; } }
</style>

<script setup lang="ts">
import { computed } from 'vue'
import type { Order } from '../../tradeApi'
import { time, price } from '../../fmt'
import CoinIcon from '../CoinIcon.vue'
import { orderStateLabel, orderTypeLabel, qty, sym } from './meta'

/** Orders (open or history), newest first. Stop / target orders show their trigger price. */
const props = defineProps<{ orders: Order[] | null; empty?: string }>()
const rows = computed(() => (props.orders || []).slice().sort((a, b) => b.createdAt - a.createdAt || b.id - a.id))
const dir = (o: Order) => (o.reduceOnly ? (o.posSide === 'long' ? '平多' : '平空') : o.posSide === 'long' ? '开多' : '开空')
</script>

<template>
  <div class="table-scroll">
    <table v-if="rows.length" class="t">
      <thead><tr><th>时间</th><th>合约</th><th>类型</th><th>方向</th><th class="num">触发/价格</th><th class="num">数量<small>张</small></th><th class="num">已成交</th><th class="num">均价</th><th>状态</th></tr></thead>
      <tbody>
        <tr v-for="o in rows" :key="o.id">
          <td class="tm tnum">{{ time(o.createdAt, true) }}</td>
          <td><span class="inst"><CoinIcon :inst-id="o.instId" :size="14" />{{ sym(o.instId) }}</span></td>
          <td>{{ orderTypeLabel[o.type] || o.type }}<small v-if="o.leverage"> {{ o.leverage }}x</small></td>
          <td :class="o.side === 'buy' ? 'up' : 'down'">{{ dir(o) }}</td>
          <td class="num">{{ o.triggerPx ? price(o.triggerPx) : price(o.price) }}</td>
          <td class="num">{{ qty(o.qty) }}</td>
          <td class="num sub">{{ qty(o.filled) }}</td>
          <td class="num sub">{{ price(o.avgPx) }}</td>
          <td><span class="tag" :class="orderStateLabel[o.state]?.c" :title="o.reason">{{ orderStateLabel[o.state]?.t || o.state }}</span><small v-if="o.reason" class="rs">{{ o.reason }}</small></td>
        </tr>
      </tbody>
    </table>
    <p v-else class="muted small">{{ orders ? empty || '没有委托' : '加载中…' }}</p>
  </div>
</template>

<style scoped>
.t td { font-size: var(--fs-sm); font-variant-numeric: tabular-nums; }
.t small { margin-left: 2px; color: var(--text-3); font-size: 10px; }
.tm { color: var(--text-3); }
.inst { display: inline-flex; align-items: center; gap: 6px; font-weight: 600; }
.sub { color: var(--text-2); }
.rs { margin-left: 6px; }
.small { font-size: var(--fs-sm); }
</style>

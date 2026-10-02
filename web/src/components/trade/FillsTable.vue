<script setup lang="ts">
import { computed } from 'vue'
import type { Fill, Instance } from '../../tradeApi'
import { time, price } from '../../fmt'
import CoinIcon from '../CoinIcon.vue'
import { amt, qty, sym, tone } from './meta'

/** Fills (newest first): side, price, quantity, fee, slippage, realized PnL. `closes` = only closing fills. */
const props = defineProps<{ fills: Fill[] | null; instances?: Instance[]; closes?: boolean }>()
const rows = computed(() => (props.fills || []).filter(f => !props.closes || f.realizedPnl !== 0 || f.liquidation).slice().sort((a, b) => b.ts - a.ts || b.id - a.id))
const instName = (id: number) => props.instances?.find(i => i.id === id)?.name || (id ? `#${id}` : '手动')
// opening long = buy long, closing long = sell long, etc.
const what = (f: Fill) => (f.liquidation ? '强平' : (f.side === 'buy') === (f.posSide === 'long') ? (f.posSide === 'long' ? '开多' : '开空') : f.posSide === 'long' ? '平多' : '平空')
const whatTone = (f: Fill) => (f.liquidation ? 'down' : f.side === 'buy' ? 'up' : 'down')
</script>

<template>
  <div class="table-scroll">
    <table v-if="rows.length" class="t">
      <thead><tr><th>时间</th><th>合约</th><th>方向</th><th class="num">成交价</th><th class="num">数量<small>张</small></th><th class="num">已实现<small>USDT</small></th><th class="num">手续费</th><th class="num">滑点</th><th>策略</th></tr></thead>
      <tbody>
        <tr v-for="f in rows" :key="f.id">
          <td class="tm tnum">{{ time(f.ts, true) }}</td>
          <td><span class="inst"><CoinIcon :inst-id="f.instId" :size="14" />{{ sym(f.instId) }}</span></td>
          <td :class="whatTone(f)">{{ what(f) }}</td>
          <td class="num">{{ price(f.px) }}</td>
          <td class="num">{{ qty(f.qty) }}</td>
          <td class="num" :class="tone(f.realizedPnl)">{{ f.realizedPnl ? amt(f.realizedPnl, 2, true) : '—' }}</td>
          <td class="num sub">{{ amt(f.fee, 3) }}</td>
          <td class="num sub">{{ f.slippageBp.toFixed(1) }}<small>bp</small></td>
          <td class="sub">{{ instName(f.instanceId) }}</td>
        </tr>
      </tbody>
    </table>
    <p v-else class="muted small">{{ fills ? (closes ? '还没有平仓交易' : '还没有成交') : '加载中…' }}</p>
  </div>
</template>

<style scoped>
.t td { font-size: var(--fs-sm); font-variant-numeric: tabular-nums; }
.t small { margin-left: 2px; color: var(--text-3); font-size: 10px; }
.tm { color: var(--text-3); }
.inst { display: inline-flex; align-items: center; gap: 6px; font-weight: 600; }
.sub { color: var(--text-2); }
.small { font-size: var(--fs-sm); }
</style>

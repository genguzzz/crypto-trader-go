<script setup lang="ts">
import { computed } from 'vue'
import { ChevronRight } from 'lucide-vue-next'
import CoinIcon from '../CoinIcon.vue'
import { amt, pct, sym, tone } from './meta'

/** Contribution of each instrument to the account result: diverging bars around zero, sorted by |net|. */
export interface Contribution { instId: string; net: number; trades: number; wins: number; fees?: number; turnover?: number }
const props = defineProps<{ rows: Contribution[] }>()
const emit = defineEmits<{ open: [instId: string] }>()
const list = computed(() => [...props.rows].sort((a, b) => Math.abs(b.net) - Math.abs(a.net)))
const max = computed(() => Math.max(1e-9, ...props.rows.map(r => Math.abs(r.net))))
const total = computed(() => props.rows.reduce((s, r) => s + r.net, 0))
</script>

<template>
  <div class="ic">
    <button v-for="r in list" :key="r.instId" type="button" class="row" @click="emit('open', r.instId)">
      <span class="nm"><CoinIcon :inst-id="r.instId" :size="16" /><b>{{ sym(r.instId) }}</b></span>
      <span class="bar"><span class="axis"></span><i :class="r.net >= 0 ? 'pos' : 'neg'" :style="{ width: (Math.abs(r.net) / max) * 50 + '%' }"></i></span>
      <span class="v tnum" :class="tone(r.net)">{{ amt(r.net, 2, true) }}</span>
      <span class="m tnum">{{ r.trades }} 笔 · {{ r.trades ? pct(r.wins / r.trades, 0) : '—' }}</span>
      <ChevronRight :size="14" class="go" />
    </button>
    <div v-if="list.length > 1" class="tot"><span>合计</span><b class="tnum" :class="tone(total)">{{ amt(total, 2, true) }} <small>USDT</small></b></div>
    <p v-if="!list.length" class="muted small">没有交易</p>
  </div>
</template>

<style scoped>
.ic { display: grid; grid-template-columns: minmax(0, 1fr); }
.row {
  all: unset; box-sizing: border-box; display: grid; grid-template-columns: 84px minmax(0, 1fr) 92px 92px 14px; align-items: center; gap: 10px;
  padding: 7px 4px; border-bottom: 1px solid var(--line); cursor: pointer; border-radius: 4px;
}
@media (hover: hover) { .row:hover { background: var(--bg-2); } .row:hover .go { color: var(--accent); } }
.row:focus-visible { outline: 2px solid var(--accent); }
.nm { display: inline-flex; align-items: center; gap: 6px; min-width: 0; }
.nm b { font-weight: 600; overflow: hidden; text-overflow: ellipsis; }
.bar { position: relative; height: 10px; }
.axis { position: absolute; left: 50%; top: -3px; bottom: -3px; width: 1px; background: var(--line-2); }
.bar i { position: absolute; top: 0; bottom: 0; border-radius: 2px; }
.bar i.pos { left: 50%; background: var(--up); }
.bar i.neg { right: 50%; background: var(--down); }
.v { text-align: right; font-weight: 600; font-size: var(--fs-sm); }
.m { text-align: right; font-size: var(--fs-xs); color: var(--text-3); white-space: nowrap; }
.go { color: var(--text-3); }
.tot { display: flex; justify-content: space-between; padding: 8px 4px 0; font-size: var(--fs-sm); color: var(--text-3); }
.tot small { font-size: var(--fs-xs); color: var(--text-3); font-weight: 400; }
.small { font-size: var(--fs-sm); }
@media (max-width: 520px) { .row { grid-template-columns: 70px minmax(0, 1fr) 78px 14px; } .m { display: none; } }
</style>

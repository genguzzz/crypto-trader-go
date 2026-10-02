<script setup lang="ts">
import { computed } from 'vue'
import { ChevronRight } from 'lucide-vue-next'
import type { Account } from '../../tradeApi'
import { accountStatus, amt, day, modeLabel, pct, tone } from './meta'

/** Account summary card for the accounts list: equity is the focus, PnL and counts below. */
const props = defineProps<{ a: Account; running?: number }>()
const st = computed(() => accountStatus(props.a, props.running))
const total = computed(() => props.a.equity - props.a.deposit)
const totalPct = computed(() => (props.a.deposit > 0 ? total.value / props.a.deposit : null))
const to = computed(() => (props.a.mode === 'backtest' ? `/trading/backtests/${props.a.id}` : `/trading/accounts/${props.a.id}`))
</script>

<template>
  <RouterLink :to="to" class="ac" :class="{ arch: a.archived, halted: a.halted }">
    <div class="top">
      <span class="tag" :class="modeLabel[a.mode].c">{{ modeLabel[a.mode].t }}</span>
      <b class="nm">{{ a.name }}</b>
      <span class="tag st" :class="st.c">{{ st.t }}</span>
      <ChevronRight :size="15" class="go" />
    </div>
    <div class="eq">
      <span class="lbl">权益</span>
      <span class="v tnum">{{ amt(a.equity) }} <small>USDT</small></span>
    </div>
    <div class="row">
      <div v-if="a.mode !== 'backtest'" class="m"><span class="lbl">今日盈亏</span><span class="tnum" :class="tone(a.dayPnl)">{{ amt(a.dayPnl, 2, true) }}</span></div>
      <div class="m"><span class="lbl">累计收益</span><span class="tnum" :class="tone(total)">{{ pct(totalPct, 2, true) }}</span></div>
      <div class="m"><span class="lbl">持仓</span><span class="tnum">{{ a.openCount }}</span></div>
      <div class="m"><span class="lbl">{{ running != null ? '运行策略' : '策略' }}</span><span class="tnum">{{ running != null ? `${running}/${a.instances}` : a.instances }}</span></div>
    </div>
    <div class="foot">
      <template v-if="a.mode === 'backtest'">{{ day(a.from) }} → {{ day(a.to ? a.to - 1 : undefined) }}</template>
      <template v-else>入金 {{ amt(a.deposit, 0) }} USDT</template>
    </div>
  </RouterLink>
</template>

<style scoped>
.ac { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; padding: 14px 16px; background: var(--bg-1); border: 1px solid var(--line); border-radius: var(--radius); color: var(--text-1); text-decoration: none !important; transition: border-color .12s, background .12s; min-width: 0; }
@media (hover: hover) { .ac:hover { opacity: 1; border-color: var(--line-2); background: var(--bg-2); } .ac:hover .go { color: var(--accent); transform: translateX(2px); } }
.ac.arch { opacity: .6; }
.ac.halted { border-color: var(--down-bg); }
.top { display: flex; align-items: center; gap: 8px; min-width: 0; }
.nm { font-size: var(--fs-lg); font-weight: 600; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.st { margin-left: auto; }
.go { color: var(--text-3); flex: none; transition: transform .12s, color .12s; }
.eq { display: grid; gap: 2px; }
.lbl { font-size: var(--fs-xs); color: var(--text-3); }
.eq .v { font-size: var(--fs-2xl); font-weight: 700; line-height: 1.2; }
.eq small { font-size: var(--fs-xs); font-weight: 500; color: var(--text-3); }
.row { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; }
.m { display: grid; gap: 1px; min-width: 0; font-size: var(--fs-md); color: var(--text-2); font-weight: 500; }
.m > span:last-child { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.m .up { color: var(--up); } .m .down { color: var(--down); }
.foot { font-size: var(--fs-xs); color: var(--text-3); border-top: 1px solid var(--line); padding-top: 8px; }
</style>

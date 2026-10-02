<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Instance, Signal } from '../../tradeApi'
import { time, price } from '../../fmt'
import CoinIcon from '../CoinIcon.vue'
import { actionLabel, outcomeLabel, sym } from './meta'

/** Signal stream: newest first, with where each signal ended up (outcome chip) and why. */
const props = defineProps<{ signals: Signal[] | null; instances?: Instance[]; limit?: number }>()
const filter = ref<'all' | 'ordered' | 'not'>('all')
const shown = ref(props.limit ?? 20)
const instName = (id: number) => props.instances?.find(i => i.id === id)?.name
const list = computed(() => (props.signals || [])
  .filter(s => filter.value === 'all' || (filter.value === 'ordered') === (s.outcome === 'ordered'))
  .slice().sort((a, b) => b.ts - a.ts || b.id - a.id))
</script>

<template>
  <div class="sl">
    <Segmented v-model="filter" size="sm" aria-label="信号筛选" :options="[{ value: 'all', label: '全部' }, { value: 'ordered', label: '已下单' }, { value: 'not', label: '未执行' }]" />
    <ul v-if="list.length" class="ss">
      <li v-for="s in list.slice(0, shown)" :key="s.id" class="s">
        <div class="r1">
          <CoinIcon :inst-id="s.instId" :size="15" />
          <b class="sym">{{ sym(s.instId) }}</b>
          <span class="act" :class="actionLabel[s.action]?.tone">{{ actionLabel[s.action]?.t || s.action }}</span>
          <span class="px tnum">@ {{ price(s.price) }}</span>
          <span class="tag oc" :class="outcomeLabel[s.outcome]?.c">{{ outcomeLabel[s.outcome]?.t || s.outcome }}</span>
        </div>
        <div class="r2">
          <span class="tm tnum" :title="`决策 ${time(s.ts, true)} · 收到 ${time(s.recvTs, true)}`">{{ time(s.ts, true) }}</span>
          <span v-if="instName(s.instanceId)" class="in">{{ instName(s.instanceId) }}<small> · v{{ s.configVersion }}</small></span>
          <span v-if="s.stopLoss" class="lv tnum">止损 {{ price(s.stopLoss) }}</span>
          <span v-if="s.takeProfit" class="lv tnum">止盈 {{ price(s.takeProfit) }}</span>
          <span v-if="s.strength != null && s.strength !== 1" class="lv tnum">强度 {{ s.strength.toFixed(2) }}</span>
        </div>
        <div v-if="s.reason" class="why"><span class="k">策略</span>{{ s.reason }}</div>
        <div v-if="s.outcomeReason" class="why" :class="{ bad: s.outcome === 'rejected' }"><span class="k">结果</span>{{ s.outcomeReason }}</div>
      </li>
    </ul>
    <p v-else class="muted small">{{ signals ? '还没有信号' : '加载中…' }}</p>
    <button v-if="list.length > shown" type="button" class="more" @click="shown += 40">显示更多（共 {{ list.length }} 条）</button>
  </div>
</template>

<style scoped>
.sl { display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; }
.sl > .seg { justify-self: start; }
.ss { list-style: none; margin: 0; padding: 0; }
.s { padding: 9px 0; border-top: 1px solid var(--line); display: grid; gap: 3px; min-width: 0; }
.s:first-child { border-top: none; }
.r1 { display: flex; align-items: center; gap: 6px; min-width: 0; }
.sym { font-weight: 600; }
.act { font-weight: 600; font-size: var(--fs-sm); color: var(--text-2); }
.act.up { color: var(--up); } .act.down { color: var(--down); }
.px { color: var(--text-2); font-size: var(--fs-sm); }
.oc { margin-left: auto; }
.r2 { display: flex; flex-wrap: wrap; gap: 2px 10px; font-size: var(--fs-xs); color: var(--text-3); }
.in { color: var(--text-2); }
.in small { color: var(--text-3); }
.why { font-size: var(--fs-sm); color: var(--text-2); overflow-wrap: anywhere; }
.why.bad { color: var(--down); }
.why .k { display: inline-block; margin-right: 6px; font-size: var(--fs-xs); color: var(--text-3); }
.more { display: block; margin: 4px auto 0; background: transparent; color: var(--text-2); }
.small { font-size: var(--fs-sm); }
</style>

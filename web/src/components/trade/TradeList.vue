<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { Trade } from '../../tradeApi'
import { time, price } from '../../fmt'
import CoinIcon from '../CoinIcon.vue'
import { amt, pct, sym, tone } from './meta'
import { exitKindLabel, holdText } from './tradeMeta'

/**
 * Round trips under the chart, synced both ways with the chart nodes: hovering a row highlights its
 * node, a node picked on the chart scrolls its row into view (inside the list, never the page).
 * Filters narrow the list and fade the other nodes on the chart (emitted as `visible`).
 */
export interface TradeFilter { inst: string; side: 'all' | 'long' | 'short'; result: 'all' | 'win' | 'loss' | 'open'; exit: string; sort: 'time' | 'net' | 'ret' } // 'all' = no filter (Reka items can't use '')
const props = defineProps<{ trades: Trade[]; hoverId: number; selectedId: number }>()
const f = defineModel<TradeFilter>('filter', { required: true })
const emit = defineEmits<{ hover: [id: number]; select: [id: number] }>()

const insts = computed(() => [...new Set(props.trades.map(t => t.instId))].sort())
const rows = computed(() => {
  const x = f.value
  const out = props.trades.filter(t =>
    (x.inst === 'all' || t.instId === x.inst) && (x.side === 'all' || t.posSide === x.side) && (x.exit === 'all' || t.exitKind === x.exit) &&
    (x.result === 'all' || (x.result === 'open' ? !t.closeTs : x.result === 'win' ? !!t.closeTs && t.net > 0 : !!t.closeTs && t.net <= 0)))
  const key = x.sort === 'net' ? (t: Trade) => t.net : x.sort === 'ret' ? (t: Trade) => t.returnPct : (t: Trade) => t.openTs
  return out.sort((a, b) => key(b) - key(a) || b.id - a.id)
})
const sum = computed(() => ({ n: rows.value.length, net: rows.value.reduce((s, t) => s + t.net, 0), wins: rows.value.filter(t => t.closeTs && t.net > 0).length, closed: rows.value.filter(t => t.closeTs).length }))

const box = ref<HTMLElement>()
watch(() => props.selectedId, async id => {
  await nextTick()
  const row = box.value?.querySelector<HTMLElement>(`[data-id="${id}"]`)
  const c = box.value
  if (!row || !c) return
  const top = row.offsetTop - c.clientHeight / 2 + row.offsetHeight / 2
  c.scrollTo({ top: Math.max(0, top), behavior: 'smooth' })
})
const instOpts = computed(() => [{ value: 'all', label: '全部合约' }, ...insts.value.map(i => ({ value: i, label: sym(i) }))])
const exitOpts = [{ value: 'all', label: '全部离场' }, ...Object.entries(exitKindLabel).map(([k, v]) => ({ value: k, label: v.t }))]
const sortOpts = [{ value: 'time', label: '按时间' }, { value: 'net', label: '按净盈亏' }, { value: 'ret', label: '按收益率' }]
</script>

<template>
  <div class="tl">
    <div class="fb">
      <USelect v-model="f.inst" :options="instOpts" size="sm" width="128px" aria-label="合约" />
      <Segmented v-model="f.side" size="sm" aria-label="方向" :options="[{ value: 'all', label: '全部' }, { value: 'long', label: '多', tone: 'up' }, { value: 'short', label: '空', tone: 'down' }]" />
      <Segmented v-model="f.result" size="sm" aria-label="结果" :options="[{ value: 'all', label: '全部' }, { value: 'win', label: '盈利' }, { value: 'loss', label: '亏损' }, { value: 'open', label: '持仓' }]" />
      <USelect v-model="f.exit" :options="exitOpts" size="sm" width="112px" aria-label="离场方式" />
      <USelect v-model="f.sort" :options="sortOpts" size="sm" width="104px" aria-label="排序" />
      <span class="sm tnum">{{ sum.n }} 笔 · 胜率 {{ sum.closed ? pct(sum.wins / sum.closed, 0) : '—' }} · <b :class="tone(sum.net)">{{ amt(sum.net, 2, true) }}</b></span>
    </div>
    <div ref="box" class="box">
      <table v-if="rows.length" class="t">
        <thead><tr><th>开仓时间</th><th>合约</th><th>方向</th><th class="num">开仓 → 平仓</th><th class="num">持有</th><th class="num">名义<small>USDT</small></th><th class="num">净盈亏</th><th class="num">收益率</th><th>离场</th></tr></thead>
        <tbody>
          <tr v-for="t in rows" :key="t.id" :data-id="t.id" :class="{ hov: t.id === hoverId, sel: t.id === selectedId }"
            @mouseenter="emit('hover', t.id)" @mouseleave="emit('hover', 0)" @click="emit('select', t.id)">
            <td class="tm tnum">{{ time(t.openTs, true) }}</td>
            <td><span class="inst"><CoinIcon :inst-id="t.instId" :size="14" />{{ sym(t.instId) }}</span></td>
            <td :class="t.posSide === 'long' ? 'up' : 'down'">{{ t.posSide === 'long' ? '多' : '空' }} <small>{{ t.leverage }}x</small></td>
            <td class="num">{{ price(t.entryPx) }} <span class="muted">→</span> {{ t.exitPx ? price(t.exitPx) : '—' }}</td>
            <td class="num sub">{{ holdText(t) }}</td>
            <td class="num sub">{{ amt(t.notional, 0) }}</td>
            <td class="num" :class="tone(t.net)"><b>{{ amt(t.net, 2, true) }}</b></td>
            <td class="num" :class="tone(t.returnPct)">{{ pct(t.returnPct, 1, true) }}</td>
            <td><span class="tag" :class="exitKindLabel[t.exitKind]?.c">{{ exitKindLabel[t.exitKind]?.t || t.exitKind }}</span></td>
          </tr>
        </tbody>
      </table>
      <p v-else class="muted small">{{ trades.length ? '没有符合筛选的交易' : '还没有交易' }}</p>
    </div>
  </div>
</template>

<style scoped>
.tl { display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; min-width: 0; }
.fb { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.fb .sm { margin-left: auto; font-size: var(--fs-xs); color: var(--text-3); white-space: nowrap; }
.fb .sm b { font-weight: 600; }
.fb .sm .up { color: var(--up); } .fb .sm .down { color: var(--down); }
.box { max-height: 360px; overflow: auto; position: relative; border-top: 1px solid var(--line); -webkit-overflow-scrolling: touch; }
.t thead th { position: sticky; top: 0; z-index: 1; background: var(--bg-1); }
.t td { font-size: var(--fs-sm); font-variant-numeric: tabular-nums; cursor: pointer; }
.t small { color: var(--text-3); font-size: 10px; }
.t th small { margin-left: 2px; }
.tm { color: var(--text-3); }
.inst { display: inline-flex; align-items: center; gap: 6px; font-weight: 600; }
.sub { color: var(--text-2); }
tr.hov td { background: var(--bg-2); }
tr.sel td { background: var(--accent-bg-solid); }
tr.sel td:first-child { box-shadow: 2px 0 0 var(--accent) inset; }
.small { font-size: var(--fs-sm); padding: 8px 0; }
@media (max-width: 900px) { .fb .sm { margin-left: 0; width: 100%; } .box { max-height: 320px; } }
</style>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { X, Plus } from 'lucide-vue-next'
import { api, type CatalogItem } from '../../api'
import type { Universe } from '../../tradeApi'
import { ruleOptions, store } from '../../store'
import SearchInput from '../ui/SearchInput.vue'
import NumInput from '../ui/NumInput.vue'
import CoinIcon from '../CoinIcon.vue'
import { sym } from './meta'

/** Instrument universe of a strategy instance: a fixed list (catalog search) or a screener query. */
const u = defineModel<Universe>({ required: true })
const kind = computed<'static' | 'screener'>({
  get: () => (u.value.screener ? 'screener' : 'static'),
  set: k => {
    if (k === 'screener') u.value = { screener: u.value.screener ?? { rule: store.ruleId || undefined, side: 'long', top: 20, refresh: '1H' } }
    else u.value = { static: u.value.static ?? [] }
  },
})

// ---- static list: search the live catalog (every OKX USDT perpetual) ----
const catalog = ref<CatalogItem[]>([])
let loaded = false
const q = ref('')
watch(q, v => {
  if (v && !loaded) { loaded = true; api.catalog().then(x => (catalog.value = x.items)).catch(() => (loaded = false)) }
})
const picked = computed(() => u.value.static ?? [])
const results = computed(() => {
  const s = q.value.trim().toLowerCase()
  if (!s) return []
  const have = new Set(picked.value)
  return catalog.value
    .filter(c => !have.has(c.instId) && c.instId.toLowerCase().includes(s))
    .sort((a, b) => Number(!a.instId.toLowerCase().startsWith(s)) - Number(!b.instId.toLowerCase().startsWith(s)) || (a.rank ?? 999) - (b.rank ?? 999))
    .slice(0, 8)
})
function add(id: string) { u.value = { static: [...picked.value, id] }; q.value = '' }
function remove(id: string) { u.value = { static: picked.value.filter(x => x !== id) } }
const majors = ['BTC-USDT-SWAP', 'ETH-USDT-SWAP', 'SOL-USDT-SWAP']

const sc = computed(() => u.value.screener!)
</script>

<template>
  <div class="up">
    <Segmented v-model="kind" size="sm" aria-label="标的来源" :options="[{ value: 'static', label: '固定列表' }, { value: 'screener', label: '规则筛选' }]" />

    <template v-if="kind === 'static'">
      <div class="picked">
        <span v-for="id in picked" :key="id" class="pc"><CoinIcon :inst-id="id" :size="14" />{{ sym(id) }}
          <button type="button" class="x" :aria-label="`移除 ${id}`" @click="remove(id)"><X :size="12" /></button></span>
        <span v-if="!picked.length" class="muted small">还没有选择合约</span>
      </div>
      <SearchInput v-model="q" placeholder="搜索合约，如 BTC" :shortcut="false" class="srch" />
      <div v-if="q" class="res">
        <button v-for="c in results" :key="c.instId" type="button" class="ri" @click="add(c.instId)">
          <CoinIcon :inst-id="c.instId" :size="16" /><b>{{ sym(c.instId) }}</b><span class="muted">{{ c.instId }}</span>
          <span v-if="c.rank" class="tag">池内 #{{ c.rank }}</span><Plus :size="14" class="pl" />
        </button>
        <div v-if="!results.length" class="muted small">{{ catalog.length ? '没有匹配的合约' : '加载合约列表…' }}</div>
      </div>
      <div v-else-if="!picked.length" class="quick">
        <span class="muted small">常用</span>
        <button v-for="id in majors" :key="id" type="button" class="fchip" @click="add(id)">{{ sym(id) }}</button>
      </div>
    </template>

    <div v-else class="scr">
      <label class="f"><span>评分规则</span>
        <USelect :model-value="sc.rule || ''" :options="ruleOptions()" aria-label="筛选规则" @update:model-value="v => (sc.rule = String(v))" /></label>
      <label class="f"><span>方向</span>
        <Segmented :model-value="sc.side || 'long'" size="sm" aria-label="方向" :options="[{ value: 'long', label: '多头分', tone: 'up' }, { value: 'short', label: '空头分', tone: 'down' }]" @update:model-value="v => (sc.side = v as 'long' | 'short')" /></label>
      <label class="f"><span>取前 N</span><NumInput v-model="sc.top" unit="个" :min="1" :max="100" :step="1" /></label>
      <label class="f"><span>最低分（可选）</span><NumInput :model-value="sc.minScore ?? NaN" unit="分" :min="0" :max="100" @update:model-value="v => (sc.minScore = isFinite(v) ? v : undefined)" /></label>
      <label class="f"><span>重新筛选</span>
        <Segmented :model-value="sc.refresh || '1H'" size="sm" aria-label="刷新间隔" :options="['15m', '1H', '4H', '1D'].map(v => ({ value: v, label: v }))" @update:model-value="v => (sc.refresh = v)" /></label>
    </div>
  </div>
</template>

<style scoped>
.up { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; min-width: 0; }
.up > .seg { justify-self: start; }
.picked { display: flex; flex-wrap: wrap; gap: 6px; min-height: 26px; align-items: center; }
.pc { display: inline-flex; align-items: center; gap: 5px; height: 26px; padding: 0 4px 0 8px; border-radius: 13px; background: var(--bg-3); font-size: var(--fs-sm); font-weight: 600; }
.x { display: inline-flex; padding: 3px; border: none; background: none; color: var(--text-3); border-radius: 50%; }
@media (hover: hover) { .x:hover { color: var(--text-1); background: var(--bg-2); } }
.srch { width: 100%; }
.res { display: grid; gap: 2px; background: var(--bg-0); border: 1px solid var(--line); border-radius: 8px; padding: 4px; }
.ri { all: unset; box-sizing: border-box; display: flex; align-items: center; gap: 8px; padding: 6px 8px; border-radius: 6px; cursor: pointer; font-size: var(--fs-sm); min-width: 0; }
.ri .muted { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: var(--fs-xs); }
.ri .pl { margin-left: auto; color: var(--text-3); flex: none; }
@media (hover: hover) { .ri:hover { background: var(--bg-2); } }
.quick { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.small { font-size: var(--fs-xs); }
.scr { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 10px 16px; }
.f { display: grid; gap: 4px; min-width: 0; justify-items: start; }
.f > span { font-size: var(--fs-xs); color: var(--text-3); }
.f :deep(.sel-trigger) { width: 100%; }
</style>

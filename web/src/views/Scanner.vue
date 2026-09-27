<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { store, setRule, fieldLabel } from '../store'
import { api, type Row } from '../api'
import { usd, price, logPct, fundingH, catLabel, time, sign } from '../fmt'
import InstrumentDetail from '../components/InstrumentDetail.vue'
import ScoreChip from '../components/ScoreChip.vue'
import CoinIcon from '../components/CoinIcon.vue'
import { useDesktop } from '../composables/device'

const route = useRoute()
const router = useRouter()
const narrow = ref(window.innerWidth < 900)
const onResize = () => (narrow.value = window.innerWidth < 900)
onMounted(() => window.addEventListener('resize', onResize))
onUnmounted(() => window.removeEventListener('resize', onResize))

// PC only: drag the divider between list and detail; width saved locally.
const desktop = useDesktop()
const listW = ref(Number(localStorage.getItem('ctg.listWidth')) || 0)
const gridStyle = computed(() => (desktop.value && listW.value ? { gridTemplateColumns: `${listW.value}px 6px 1fr` } : {}))
function startResize(e: MouseEvent) {
  e.preventDefault()
  const startX = e.clientX
  const startW = (e.target as HTMLElement).previousElementSibling!.getBoundingClientRect().width
  const onMove = (ev: MouseEvent) => {
    listW.value = Math.max(520, Math.min(window.innerWidth - 480, startW + ev.clientX - startX))
  }
  const onUp = () => {
    localStorage.setItem('ctg.listWidth', String(Math.round(listW.value)))
    window.removeEventListener('mousemove', onMove)
    window.removeEventListener('mouseup', onUp)
    document.body.style.cursor = ''
  }
  document.body.style.cursor = 'col-resize'
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', onUp)
}
function resetWidth() { listW.value = 0; localStorage.removeItem('ctg.listWidth') }

const dir = ref<'all' | 'long' | 'short'>((localStorage.getItem('dir') as any) || 'all')
const cats = ref<Record<string, boolean>>({ '1': true, '3': true, '4': true })
const onlyWatch = ref(false)
const onlyPass = ref(false)
const q = ref('')
const sortKey = ref(localStorage.getItem('sortKey') || 'score')
const sortDesc = ref(true)
const showRemoved = ref(false)
watch(dir, v => localStorage.setItem('dir', v))
watch(sortKey, v => localStorage.setItem('sortKey', v))

const selected = computed(() => (route.query.sel as string) || '')
const rule = computed(() => store.meta?.rules.find(r => r.id === store.ruleId))

function scoreOf(r: Row, side: 'long' | 'short') {
  return r.scores?.[store.ruleId]?.[side]?.score ?? null
}
function dirScore(r: Row) {
  const l = scoreOf(r, 'long'), s = scoreOf(r, 'short')
  if (dir.value === 'long') return l
  if (dir.value === 'short') return s
  if (l == null && s == null) return null
  return Math.max(l ?? -1, s ?? -1)
}
function sortVal(r: Row): number | null {
  switch (sortKey.value) {
    case 'score': return dirScore(r)
    case 'turnover12h': return r.turnover12h || null
    case 'long': return scoreOf(r, 'long')
    case 'short': return scoreOf(r, 'short')
    default: return r.values?.[sortKey.value] ?? null
  }
}
const rows = computed(() => {
  const xs = (store.snap?.rows || []).filter(r => {
    if (!cats.value[r.category]) return false
    if (onlyWatch.value && !r.watch) return false
    if (q.value && !r.instId.toLowerCase().includes(q.value.toLowerCase())) return false
    if (onlyPass.value && r.scores?.[store.ruleId]?.gate !== 'PASS') return false
    return true
  })
  // unknown sorts last regardless of direction; ties by 12h turnover then id (stable)
  return xs.sort((a, b) => {
    const va = sortVal(a), vb = sortVal(b)
    if (va == null && vb == null) return b.turnover12h - a.turnover12h
    if (va == null) return 1
    if (vb == null) return -1
    if (va !== vb) return sortDesc.value ? vb - va : va - vb
    return b.turnover12h - a.turnover12h || a.instId.localeCompare(b.instId)
  })
})
function sortBy(k: string) {
  if (sortKey.value === k) sortDesc.value = !sortDesc.value
  else { sortKey.value = k; sortDesc.value = true }
}
function open(r: Row) {
  if (narrow.value) router.push(`/instrument/${r.instId}`)
  else router.replace({ query: { ...route.query, sel: r.instId } })
}
async function toggleWatch(r: Row) {
  await api.watch(r.instId, !r.watch)
}
function livePrice(r: Row) {
  return store.prices[r.instId]?.[0] ?? r.last
}
const sym = (id: string) => id.replace('-USDT-SWAP', '')
watch(() => store.snap, s => {
  if (!narrow.value && !selected.value && s?.rows?.length) router.replace({ query: { sel: s.rows[0].instId } })
}, { immediate: true })
const cols = [
  ['ret_4h', '4h'], ['excess_btc_4h', '相对BTC'], ['oi_chg_4h', 'OIΔ4h'], ['vol_surprise_4h', '放量'], ['funding_hourly', '费/h'],
]
</script>

<template>
  <div class="scanner" :class="{ narrow }" :style="gridStyle">
    <section class="list">
      <div class="controls">
        <div class="row">
          <select :value="store.ruleId" @change="setRule(($event.target as HTMLSelectElement).value)" title="评分规则（未验证，见规则与战绩）">
            <option v-for="r in store.meta?.rules || []" :key="r.id" :value="r.id">{{ r.name }} v{{ r.version }}</option>
          </select>
          <span class="seg">
            <button :class="{ on: dir === 'all' }" @click="dir = 'all'">全部</button>
            <button :class="{ on: dir === 'long' }" @click="dir = 'long'">做多</button>
            <button :class="{ on: dir === 'short' }" @click="dir = 'short'">做空</button>
          </span>
          <input v-model="q" placeholder="搜索" class="search" />
        </div>
        <div class="row">
          <span v-for="(l, c) in catLabel" :key="c" class="chip" :class="{ on: cats[c] }" @click="cats[c] = !cats[c]">{{ l }}</span>
          <span class="chip" :class="{ on: onlyWatch }" @click="onlyWatch = !onlyWatch">★ 自选</span>
          <span class="chip" :class="{ on: onlyPass }" @click="onlyPass = !onlyPass" title="只看通过点差门槛的">门槛通过</span>
          <select v-model="sortKey" class="hide-lg">
            <option value="score">按方向分</option><option value="long">多头分</option><option value="short">空头分</option>
            <option value="turnover12h">12h 成交额</option>
            <option v-for="f in store.meta?.fields.filter(f => f.key !== 'turnover12h') || []" :key="f.key" :value="f.key">{{ f.label }}</option>
          </select>
          <span class="chip" v-if="store.snap?.removed?.length" :class="{ on: showRemoved }" @click="showRemoved = !showRemoved">最近移出 {{ store.snap.removed.length }}</span>
        </div>
        <div class="rule-line" v-if="rule">
          <span class="muted">当前规则</span> <b>{{ rule.name }}</b> <span class="tag warn">未验证</span>
          <InfoTip label="评分原理"><ScoringExplainer /></InfoTip>
          <RouterLink :to="`/rules/${rule.id}`">战绩 →</RouterLink>
        </div>
      </div>
      <div class="removed" v-if="showRemoved">
        <div v-for="r in store.snap?.removed || []" :key="r.instId" class="rm">
          <span>{{ sym(r.instId) }}</span><span class="muted">{{ r.reason }}</span><span class="muted">{{ time(r.at) }}</span>
        </div>
      </div>

      <!-- desktop table -->
      <div class="tablewrap" v-if="!narrow">
        <table>
          <thead>
            <tr>
              <th></th>
              <th>合约</th>
              <th class="num sortable" @click="sortBy('long')">多{{ sortKey === 'long' ? (sortDesc ? '↓' : '↑') : '' }}</th>
              <th class="num sortable" @click="sortBy('short')">空{{ sortKey === 'short' ? (sortDesc ? '↓' : '↑') : '' }}</th>
              <th class="num">价格</th>
              <th v-for="[k, l] in cols" :key="k" class="num sortable" :title="fieldLabel(k)" @click="sortBy(k)">{{ l }}{{ sortKey === k ? (sortDesc ? '↓' : '↑') : '' }}</th>
              <th class="num sortable" @click="sortBy('turnover12h')">12h额{{ sortKey === 'turnover12h' ? (sortDesc ? '↓' : '↑') : '' }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in rows" :key="r.instId" :class="{ sel: r.instId === selected, inactive: !r.active }" @click="open(r)">
              <td @click.stop="toggleWatch(r)" class="star" :class="{ on: r.watch }">{{ r.watch ? '★' : '☆' }}</td>
              <td>
                <span class="dot" :class="r.status === 'live' ? 'ok' : r.status === 'stale' ? 'bad' : 'warn'" :title="r.status"></span>
                <CoinIcon :inst-id="r.instId" class="ic" />
                <b class="symb">{{ sym(r.instId) }}</b>
                <span class="tag" v-if="r.category !== '1'">{{ catLabel[r.category] }}</span>
                <span class="tag" v-if="!r.active">非活跃</span>
                <span class="tag bad" v-if="r.scores?.[store.ruleId]?.gate?.startsWith('BLOCKED')" :title="r.scores?.[store.ruleId]?.gate">门槛</span>
              </td>
              <td class="num"><ScoreChip :v="scoreOf(r, 'long')" side="long" /></td>
              <td class="num"><ScoreChip :v="scoreOf(r, 'short')" side="short" /></td>
              <td class="num px">{{ price(livePrice(r)) }}</td>
              <td v-for="[k] in cols" :key="k" class="num" :class="k === 'funding_hourly' || k === 'vol_surprise_4h' ? 'sec' : sign(r.values?.[k] ?? 0)">
                {{ k === 'funding_hourly' ? fundingH(r.values?.[k]) : k === 'vol_surprise_4h' ? (r.values?.[k] != null ? '×' + Math.exp(r.values[k]).toFixed(2) : '—') : logPct(r.values?.[k]) }}
              </td>
              <td class="num sec">{{ usd(r.turnover12h) }}<span v-if="r.provisional" class="muted">*</span></td>
            </tr>
          </tbody>
        </table>
        <div class="note pad" v-if="!rows.length">暂无数据：{{ store.snap?.phase === 'live' ? '当前筛选下没有合约' : '正在建池…' }}</div>
      </div>

      <!-- mobile cards -->
      <div v-else class="cards">
        <div v-for="r in rows" :key="r.instId" class="card" @click="open(r)">
          <div class="l1">
            <span class="dot" :class="r.status === 'live' ? 'ok' : r.status === 'stale' ? 'bad' : 'warn'"></span>
            <CoinIcon :inst-id="r.instId" :size="22" />
            <b>{{ sym(r.instId) }}</b>
            <span class="tag" v-if="r.category !== '1'">{{ catLabel[r.category] }}</span>
            <span class="star" :class="{ on: r.watch }" @click.stop="toggleWatch(r)">{{ r.watch ? '★' : '☆' }}</span>
            <span class="px num">{{ price(livePrice(r)) }}</span>
          </div>
          <div class="l2">
            <span>多 <ScoreChip :v="scoreOf(r, 'long')" side="long" /></span>
            <span>空 <ScoreChip :v="scoreOf(r, 'short')" side="short" /></span>
            <span :class="sign(r.values?.ret_4h ?? 0)">4h {{ logPct(r.values?.ret_4h) }}</span>
            <span class="muted">{{ usd(r.turnover12h) }}</span>
          </div>
        </div>
      </div>
    </section>

    <div v-if="!narrow && desktop" class="divider" title="拖动调整列表宽度；双击恢复默认" @mousedown="startResize" @dblclick="resetWidth"></div>
    <section class="detail" v-if="!narrow">
      <InstrumentDetail v-if="selected" :id="selected" :key="selected" />
      <div v-else class="note pad">选择左侧合约查看详情</div>
    </section>
  </div>
</template>

<style scoped>
.scanner { display: grid; grid-template-columns: minmax(760px, 50%) 6px 1fr; height: calc(100vh - 45px); }
.scanner.narrow { display: block; height: auto; }
.divider { cursor: col-resize; background: var(--line); opacity: 0.5; transition: background 0.15s; }
.divider:hover { background: var(--accent); opacity: 1; }
.list { display: flex; flex-direction: column; min-height: 0; }
.controls { padding: 12px 14px; display: grid; gap: 10px; border-bottom: 1px solid var(--line); background: var(--bg-1); }
.controls .row { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
.seg { display: inline-flex; }
.seg button { border-radius: 0; margin-left: -1px; }
.seg button:first-child { border-radius: 6px 0 0 6px; }
.seg button:last-child { border-radius: 0 6px 6px 0; }
.search { width: 110px; }
.tablewrap { overflow: auto; flex: 1; }
thead th { position: sticky; top: 0; background: var(--panel); z-index: 1; }
.sortable { cursor: pointer; user-select: none; }
tbody td, thead th { padding: 0 6px; }
tbody td:last-child, thead th:last-child { padding-right: 12px; }
thead th { height: 32px; border-bottom: 1px solid var(--line-2); }
tbody tr { cursor: pointer; height: 36px; }
tbody td { border-bottom-color: rgba(34, 40, 52, 0.6); }
tbody tr:hover { background: var(--bg-2); }
tbody tr.sel { background: var(--accent-bg); box-shadow: inset 2px 0 0 var(--accent); }
tbody tr.inactive { opacity: 0.6; }
.symb { font-weight: 600; font-size: var(--fs-md); color: var(--text-1); }
td.px { color: var(--text-1); font-weight: 500; }
td.sec { color: var(--text-2); }
td.up { color: var(--up); } td.down { color: var(--down); }
.ic { margin: 0 4px 0 2px; }
.star { color: var(--muted); cursor: pointer; width: 18px; }
.star.on { color: #f5c542; }
.detail { overflow: auto; min-height: 0; }
.pad { padding: 16px; }
.removed { padding: 6px 12px; border-bottom: 1px solid var(--line); display: grid; gap: 2px; font-size: 12px; }
.rm { display: grid; grid-template-columns: 90px 1fr 50px; }
.cards { display: grid; }
.card { padding: 8px 12px; border-bottom: 1px solid var(--line); display: grid; gap: 4px; }
.card .l1 { display: flex; gap: 6px; align-items: center; }
.card .px { margin-left: auto; font-weight: 600; }
.card .l2 { display: flex; gap: 12px; align-items: center; font-size: 12px; }
.hide-lg { display: none; }
.rule-line { display: flex; align-items: center; gap: 6px; font-size: var(--fs-sm); flex-wrap: wrap; }
.narrow .hide-lg { display: inline-block; }
</style>

<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { store, setRule, fieldLabel, ruleOptions } from '../store'
import { Sigma, TrendingUp, TrendingDown, ArrowDownWideNarrow, Star, ShieldCheck, LogOut, LineChart, X } from 'lucide-vue-next'
import { PopoverRoot, PopoverTrigger, PopoverPortal, PopoverContent } from 'reka-ui'
import SearchInput from '../components/ui/SearchInput.vue'
import FilterChip from '../components/ui/FilterChip.vue'
import ChipGroup from '../components/ui/ChipGroup.vue'
import { api, type Row, type CatalogItem } from '../api'
import { usd, price, logPct, fundingH, catLabel, time, sign } from '../fmt'
import InstrumentDetail from '../components/InstrumentDetail.vue'
import ScoreChip from '../components/ScoreChip.vue'
import CoinIcon from '../components/CoinIcon.vue'
import { useDesktop } from '../composables/device'
import { useLayoutEntry } from '../composables/layout'

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
useLayoutEntry('ctg.listWidth', '列表 / 详情宽度', () => listW.value > 0, resetWidth)

const dir = ref<'all' | 'long' | 'short'>((localStorage.getItem('dir') as any) || 'all')
const catSel = ref<string[]>(JSON.parse(localStorage.getItem('ctg.cats') || '["1","3","4"]'))
watch(catSel, v => localStorage.setItem('ctg.cats', JSON.stringify(v)))
// list state survives opening a detail page and coming back (filters/sort: localStorage; search: this tab only)
const onlyWatch = ref(localStorage.getItem('ctg.onlyWatch') === '1')
const onlyPass = ref(localStorage.getItem('ctg.onlyPass') === '1')
const q = ref(sessionStorage.getItem('ctg.q') || '')
const sortKey = ref(localStorage.getItem('sortKey') || 'score')
const sortDesc = ref(localStorage.getItem('ctg.sortDesc') !== '0')
watch(dir, v => localStorage.setItem('dir', v))
watch(sortKey, v => localStorage.setItem('sortKey', v))
watch(onlyWatch, v => localStorage.setItem('ctg.onlyWatch', v ? '1' : '0'))
watch(onlyPass, v => localStorage.setItem('ctg.onlyPass', v ? '1' : '0'))
watch(sortDesc, v => localStorage.setItem('ctg.sortDesc', v ? '1' : '0'))
watch(q, v => sessionStorage.setItem('ctg.q', v))

const catOptions = computed(() => Object.entries(catLabel).map(([c, l]) => ({ value: c, label: l, count: (store.snap?.rows || []).filter(r => r.category === c).length })))
const watchCount = computed(() => (store.snap?.rows || []).filter(r => r.watch).length)
const filtered = computed(() => !!q.value || onlyWatch.value || onlyPass.value || catSel.value.length < 3)
function clearFilters() { q.value = ''; onlyWatch.value = false; onlyPass.value = false; catSel.value = ['1', '3', '4'] }
const ruleModel = computed({ get: () => store.ruleId, set: (v: string) => setRule(v) })
const sortOptions = computed(() => [
  { value: 'score', label: '按方向分' }, { value: 'long', label: '多头分' }, { value: 'short', label: '空头分' }, { value: 'turnover12h', label: '12h 成交额' },
  ...(store.meta?.fields.filter(f => f.key !== 'turnover12h').map(f => ({ value: f.key, label: f.label })) || []),
])
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
// The list re-sorts on every snapshot (10s preview scores) — while a finger/mouse is down or the
// page is scrolling, keep the previous order so the row under the pointer can't move away before
// the click lands (missed / wrong taps on mobile).
const holding = ref(false)
let holdTimer: number | undefined
let lastOrder: string[] = []
function hold() { holding.value = true; clearTimeout(holdTimer) }
function release(delay = 700) { clearTimeout(holdTimer); holdTimer = window.setTimeout(() => (holding.value = false), delay) }
const onScroll = () => { hold(); release(400) }
onMounted(() => window.addEventListener('scroll', onScroll, { passive: true, capture: true }))
onUnmounted(() => { window.removeEventListener('scroll', onScroll, { capture: true }); clearTimeout(holdTimer) })

const rows = computed(() => {
  const xs = sortedRows.value
  if (holding.value && lastOrder.length) {
    const byId = new Map(xs.map(r => [r.instId, r]))
    const kept = lastOrder.map(id => byId.get(id)).filter((r): r is Row => !!r)
    const known = new Set(lastOrder)
    return kept.concat(xs.filter(r => !known.has(r.instId)))
  }
  lastOrder = xs.map(r => r.instId)
  return xs
})
const sortedRows = computed(() => {
  const xs = (store.snap?.rows || []).filter(r => {
    if (!catSel.value.includes(r.category)) return false
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
function open(r: { instId: string }) {
  if (narrow.value) router.push(`/instrument/${r.instId}`)
  else router.replace({ query: { ...route.query, sel: r.instId } })
}
async function toggleWatch(r: Row) {
  await api.watch(r.instId, !r.watch)
}

// Search also covers every live OKX USDT perpetual (catalog: metadata + 30s ticker, no history).
// Matches outside the current list are shown below it; opening one downloads its history on demand.
const catalog = ref<CatalogItem[]>([])
let catalogAt = 0
async function loadCatalog() {
  if (Date.now() - catalogAt < 60_000) return
  catalogAt = Date.now()
  try { catalog.value = (await api.catalog()).items } catch { catalogAt = 0 }
}
watch(q, v => { if (v) loadCatalog() })
const others = computed(() => {
  const s = q.value.trim().toLowerCase()
  if (!s || onlyWatch.value || onlyPass.value) return []
  const listed = new Set((store.snap?.rows || []).map(r => r.instId))
  return catalog.value
    .filter(c => !listed.has(c.instId) && catSel.value.includes(c.category) && c.instId.toLowerCase().includes(s))
    .sort((a, b) => Number(a.instId.toLowerCase().startsWith(s) ? 0 : 1) - Number(b.instId.toLowerCase().startsWith(s) ? 0 : 1) || a.instId.localeCompare(b.instId))
    .slice(0, 50)
})
const chg24 = (c: CatalogItem) => (c.last > 0 && c.open24h > 0 ? Math.log(c.last / c.open24h) : null)
async function watchOther(c: CatalogItem) {
  await api.watch(c.instId, true)
  catalogAt = 0
  loadCatalog()
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
    <section class="list" @pointerdown="hold()" @pointerup="release()" @pointercancel="release()">
      <div class="controls">
        <div class="row r1">
          <SearchInput v-model="q" placeholder="搜索合约" class="grow" />
          <Segmented v-model="dir" aria-label="方向" :options="[{ value: 'all', label: '全部' }, { value: 'long', label: '做多', icon: TrendingUp, tone: 'up' }, { value: 'short', label: '做空', icon: TrendingDown, tone: 'down' }]" />
          <span class="rule">
            <USelect v-model="ruleModel" :options="ruleOptions()" :icon="Sigma" width="230px" aria-label="评分规则" />
            <InfoTip><ScoringExplainer /></InfoTip>
            <RouterLink v-if="rule" :to="`/rules/${rule.id}`" class="icon-link" title="查看该规则战绩"><LineChart :size="15" /></RouterLink>
          </span>
        </div>
        <div class="row r2">
          <ChipGroup v-model="catSel" aria-label="类别" at-least-one :options="catOptions" />
          <span class="vsep"></span>
          <FilterChip v-model="onlyWatch" label="自选" :icon="Star" :count="watchCount" />
          <FilterChip v-model="onlyPass" label="门槛通过" :icon="ShieldCheck" title="只看当前规则点差门槛通过的合约" />
          <span class="hide-lg"><USelect v-model="sortKey" size="sm" :options="sortOptions" :icon="ArrowDownWideNarrow" aria-label="排序" /></span>
          <span class="spacer"></span>
          <PopoverRoot v-if="store.snap?.removed?.length">
            <PopoverTrigger class="fchip removed-btn"><LogOut :size="13" class="fc-ic" /><span>最近移出</span><span class="fc-n">{{ store.snap.removed.length }}</span></PopoverTrigger>
            <PopoverPortal>
              <PopoverContent class="lm-pop removed-pop" side="bottom" align="end" :side-offset="6">
                <div class="lm-title">最近 30 分钟移出监控池</div>
                <div v-for="r in store.snap?.removed || []" :key="r.instId" class="rm">
                  <CoinIcon :inst-id="r.instId" :size="16" /><b>{{ sym(r.instId) }}</b><span class="muted">{{ r.reason }}</span><span class="muted tnum">{{ time(r.at) }}</span>
                </div>
              </PopoverContent>
            </PopoverPortal>
          </PopoverRoot>
          <span class="count tnum">{{ rows.length }}<span class="muted"> / {{ store.snap?.rows?.length || 0 }}</span></span>
          <button v-if="filtered" class="clear" @click="clearFilters"><X :size="12" /> 清除</button>
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
        <div class="note pad" v-if="!rows.length && !others.length">暂无数据：{{ store.snap?.phase === 'live' ? '当前筛选下没有合约' : '正在建池…' }}</div>
        <template v-if="others.length">
          <div class="oh">OKX 其他合约 <span class="muted tnum">{{ others.length }}</span>
            <InfoTip>不在当前列表里的 OKX USDT 永续（全部类别，含盘前）。列表只保存合约信息和 30 秒一次的行情；<b>点开后</b>才按需下载近 7 天 1m、30 天 1H、持仓量与资金费历史，并在查看期间实时录制。加自选后长期保留（数据保留规则见“设置 → 数据存储”）。</InfoTip>
          </div>
          <table class="others">
            <tbody>
              <tr v-for="c in others" :key="c.instId" :class="{ sel: c.instId === selected }" @click="open(c)">
                <td @click.stop="watchOther(c)" class="star" title="加入自选">☆</td>
                <td>
                  <CoinIcon :inst-id="c.instId" class="ic" />
                  <b class="symb">{{ sym(c.instId) }}</b>
                  <span class="tag" v-if="c.category !== '1'">{{ catLabel[c.category] || c.category }}</span>
                  <span class="tag warn" v-if="c.preMarket">盘前</span>
                  <span class="tag" v-if="!c.recorded" title="不在全池录制范围（类别未选或盘前），点开后按需下载">未录制</span>
                  <span class="tag" v-else title="全池录制中，但不在活跃池">池外</span>
                </td>
                <td class="num px">{{ price(c.last) }}</td>
                <td class="num" :class="sign(chg24(c) ?? 0)"><small class="muted">24h </small>{{ logPct(chg24(c)) }}</td>
              </tr>
            </tbody>
          </table>
        </template>
      </div>

      <!-- mobile cards -->
      <div v-else class="cards">
        <div v-for="r in rows" :key="r.instId" class="card pressable" @click="open(r)">
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
        <div class="note pad" v-if="!rows.length && !others.length">暂无数据：{{ store.snap?.phase === 'live' ? '当前筛选下没有合约' : '正在建池…' }}</div>
        <template v-if="others.length">
          <div class="oh">OKX 其他合约 <span class="muted tnum">{{ others.length }}</span>
            <InfoTip>不在当前列表里的 OKX USDT 永续。点开后才按需下载近 7 天历史；加自选后长期保留。</InfoTip>
          </div>
          <div v-for="c in others" :key="c.instId" class="card pressable" @click="open(c)">
            <div class="l1">
              <CoinIcon :inst-id="c.instId" :size="22" />
              <b>{{ sym(c.instId) }}</b>
              <span class="tag" v-if="c.category !== '1'">{{ catLabel[c.category] || c.category }}</span>
              <span class="tag warn" v-if="c.preMarket">盘前</span>
              <span class="tag" v-if="!c.recorded">未录制</span>
              <span class="star" @click.stop="watchOther(c)">☆</span>
              <span class="px num">{{ price(c.last) }}</span>
            </div>
            <div class="l2"><span :class="sign(chg24(c) ?? 0)">24h {{ logPct(chg24(c)) }}</span></div>
          </div>
        </template>
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
.scanner { display: grid; grid-template-columns: minmax(760px, 50%) 6px 1fr; height: 100%; }
.scanner.narrow { display: block; height: auto; }
.divider { cursor: col-resize; background: var(--line); opacity: 0.5; transition: background 0.15s; }
@media (hover: hover) { .divider:hover { background: var(--accent); opacity: 1; } }
.list { display: flex; flex-direction: column; min-height: 0; }
.controls { padding: 12px 14px 10px; display: grid; gap: 10px; border-bottom: 1px solid var(--line); background: var(--bg-1); }
.controls .row { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
.grow { flex: 1; }
.rule { display: inline-flex; align-items: center; gap: 2px; }
.icon-link { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: 6px; color: var(--text-3); }
@media (hover: hover) { .icon-link:hover { background: var(--bg-2); color: var(--accent); text-decoration: none; } }
.vsep { width: 1px; height: 18px; background: var(--line-2); margin: 0 2px; }
.spacer { flex: 1; }
.count { font-size: var(--fs-sm); color: var(--text-1); font-weight: 600; }
.clear { display: inline-flex; align-items: center; gap: 3px; height: 24px; padding: 0 8px; border: none; background: transparent; color: var(--text-3); font-size: var(--fs-xs); }
@media (hover: hover) { .clear:hover { color: var(--text-1); background: var(--bg-2); } }
.tablewrap { overflow: auto; flex: 1; }
thead th { position: sticky; top: 0; background: var(--panel); z-index: 1; }
.sortable { cursor: pointer; user-select: none; }
tbody td, thead th { padding: 0 6px; }
tbody td:last-child, thead th:last-child { padding-right: 12px; }
thead th { height: 32px; border-bottom: 1px solid var(--line-2); }
tbody tr { cursor: pointer; height: 36px; }
tbody td { border-bottom-color: rgba(34, 40, 52, 0.6); }
@media (hover: hover) { tbody tr:hover { background: var(--bg-2); } }
tbody tr.sel { background: var(--accent-bg); box-shadow: inset 2px 0 0 var(--accent); }
tbody tr.inactive { opacity: 0.6; }
.symb { font-weight: 600; font-size: var(--fs-md); color: var(--text-1); }
td.px { color: var(--text-1); font-weight: 500; }
td.sec { color: var(--text-2); }
td.up { color: var(--up); } td.down { color: var(--down); }
.ic { margin: 0 4px 0 2px; }
.star { color: var(--muted); cursor: pointer; width: 18px; }
.card .star { padding: 6px 10px; margin: -6px -4px; font-size: 16px; } /* finger-sized target */
.star.on { color: #f5c542; }
.detail { overflow: auto; min-height: 0; }
.pad { padding: 16px; }

.cards { display: grid; }
.card { padding: 8px 12px; border-bottom: 1px solid var(--line); display: grid; gap: 4px; }
.card .l1 { display: flex; gap: 6px; align-items: center; }
.card .px { margin-left: auto; font-weight: 600; }
.card .l2 { display: flex; gap: 12px; align-items: center; font-size: 12px; }
.oh { display: flex; align-items: center; gap: 6px; padding: 14px 12px 6px; font-size: var(--fs-sm); font-weight: 600; color: var(--text-2); border-top: 1px solid var(--line-2); }
table.others { width: 100%; }
table.others td:first-child { width: 24px; }
.hide-lg { display: none; }
.narrow .hide-lg { display: inline-block; }
</style>

<style>
.removed-pop { width: 320px; }
.removed-pop .rm { display: grid; grid-template-columns: 18px 70px 1fr auto; gap: 8px; align-items: center; padding: 5px 6px; font-size: var(--fs-sm); border-radius: 6px; }
@media (hover: hover) { .removed-pop .rm:hover { background: var(--bg-3); } }
.removed-btn[data-state="open"] { border-style: solid; border-color: var(--accent); }
</style>

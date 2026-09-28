<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { store, setRule, fieldLabel, ruleOptions } from '../store'
import { Sigma, TrendingUp, TrendingDown, ArrowDownWideNarrow, ArrowUpNarrowWide, Star, ShieldCheck, LogOut, LineChart, X } from 'lucide-vue-next'
import { PopoverRoot, PopoverTrigger, PopoverPortal, PopoverContent } from 'reka-ui'
import SearchInput from '../components/ui/SearchInput.vue'
import FilterChip from '../components/ui/FilterChip.vue'
import ChipGroup from '../components/ui/ChipGroup.vue'
import { api, type Row, type CatalogItem } from '../api'
import { usd, price, logPct, fundingH, fundingClass, catLabel, time, sign, oiFrac } from '../fmt'
import Loading from '../components/ui/Loading.vue'
import InstrumentWorkbench from '../components/InstrumentWorkbench.vue'
import { SplitterGroup, SplitterPanel, SplitterResizeHandle } from 'reka-ui'
import ScoreChip from '../components/ScoreChip.vue'
import CoinIcon from '../components/CoinIcon.vue'

const route = useRoute()
const router = useRouter()
const narrow = ref(window.innerWidth < 900)
const onResize = () => (narrow.value = window.innerWidth < 900)
onMounted(() => window.addEventListener('resize', onResize))
onUnmounted(() => window.removeEventListener('resize', onResize))

// PC: list and workbench side by side (Reka Splitter; size saved, list collapsible from the
// instrument bar). The list's columns follow its own width: narrow = market list, wide = screener.
const listPanel = ref<InstanceType<typeof SplitterPanel> | null>(null)
const listOpen = ref(true)
function toggleList() {
  const p = listPanel.value as any
  if (!p) return
  if (p.isCollapsed) p.expand()
  else p.collapse()
}
const listEl = ref<HTMLElement>()
const listW = ref(1000)
let ro: ResizeObserver | undefined
watch(listEl, el => {
  ro?.disconnect()
  if (el) { ro = new ResizeObserver(es => (listW.value = es[0].contentRect.width)); ro.observe(el) }
}, { immediate: true })
onUnmounted(() => ro?.disconnect())
const splitProps = { direction: 'horizontal', autoSaveId: 'ctg.scanner.split' }
const listProps = {
  id: 'sc-list', order: 1, defaultSize: 23, minSize: 15, maxSize: 70, collapsible: true, collapsedSize: 0,
  onCollapse: () => (listOpen.value = false), onExpand: () => (listOpen.value = true),
}

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
  { value: 'score', label: '按方向分' }, { value: 'long', label: '多头分' }, { value: 'short', label: '空头分' }, { value: 'mcap', label: '总市值' }, { value: 'oi', label: '持仓量' }, { value: 'turnover12h', label: '12h 成交额' },
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
    case 'mcap': return r.mcap || null
    case 'oi': return r.oiUsd || null
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
const allCols: [string, string][] = [
  ['ret_4h', '4h'], ['excess_btc_4h', '相对BTC'], ['oi_chg_4h', 'OIΔ4h'], ['vol_surprise_4h', '放量'],
]
// OI bar: drawn in the list's visible frame (not the wider scrolling table), so the pinned cells
// and the scrolled cells line up — --vw = visible width, --sx = horizontal scroll offset.
const wrapEl = ref<HTMLElement>()
let wro: ResizeObserver | undefined
watch(wrapEl, el => {
  wro?.disconnect()
  if (!el) return
  wro = new ResizeObserver(() => el.style.setProperty('--vw', el.clientWidth + 'px'))
  wro.observe(el)
}, { immediate: true })
onUnmounted(() => wro?.disconnect())
function onWrapScroll(e: Event) {
  const el = e.target as HTMLElement
  el.style.setProperty('--sx', el.scrollLeft + 'px')
}
// every column is always there; a narrow list scrolls sideways with the symbol column pinned
const cols = allCols
</script>

<template>
  <component :is="narrow ? 'div' : SplitterGroup" class="scanner" :class="{ narrow }" v-bind="narrow ? {} : splitProps">
    <component :is="narrow ? 'div' : SplitterPanel" ref="listPanel" class="list-wrap" v-bind="narrow ? {} : listProps">
    <section ref="listEl" class="list" :class="{ slim: listW < 560 }" @pointerdown="hold()" @pointerup="release()" @pointercancel="release()">
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
          <span class="sortctl">
            <USelect v-model="sortKey" size="sm" :options="sortOptions" aria-label="排序字段" />
            <button class="sortdir" :title="sortDesc ? '从大到小（点击切换）' : '从小到大（点击切换）'" :aria-label="sortDesc ? '降序' : '升序'" @click="sortDesc = !sortDesc">
              <component :is="sortDesc ? ArrowDownWideNarrow : ArrowUpNarrowWide" :size="14" />
            </button>
          </span>
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
      <div class="tablewrap" v-if="!narrow" ref="wrapEl" @scroll.passive="onWrapScroll">
        <table>
          <thead>
            <tr>
              <th class="pin p0"></th>
              <th class="pin p1">合约</th>
              <th class="num sortable" @click="sortBy('long')">多{{ sortKey === 'long' ? (sortDesc ? '↓' : '↑') : '' }}</th>
              <th class="num sortable" @click="sortBy('short')">空{{ sortKey === 'short' ? (sortDesc ? '↓' : '↑') : '' }}</th>
              <th class="num sortable" :title="fieldLabel('funding_hourly')" @click="sortBy('funding_hourly')">费/h{{ sortKey === 'funding_hourly' ? (sortDesc ? '↓' : '↑') : '' }}</th>
              <th class="num">价格</th>
              <th v-for="[k, l] in cols" :key="k" class="num sortable" :title="fieldLabel(k)" @click="sortBy(k)">{{ l }}{{ sortKey === k ? (sortDesc ? '↓' : '↑') : '' }}</th>
              <th class="num sortable" @click="sortBy('mcap')">总市值{{ sortKey === 'mcap' ? (sortDesc ? '↓' : '↑') : '' }}</th>
              <th class="num sortable" @click="sortBy('turnover12h')">12h额{{ sortKey === 'turnover12h' ? (sortDesc ? '↓' : '↑') : '' }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in rows" :key="r.instId" :class="{ sel: r.instId === selected, inactive: !r.active }" :style="{ '--oi': oiFrac(r.oiUsd) }" @click="open(r)">
              <td @click.stop="toggleWatch(r)" class="star pin p0" :class="{ on: r.watch }">{{ r.watch ? '★' : '☆' }}</td>
              <td class="pin p1" :title="r.oiUsd ? `持仓量 ${usd(r.oiUsd)} USDT` : undefined">
                <span class="dot" :class="r.status === 'live' ? 'ok' : r.status === 'stale' ? 'bad' : 'warn'" :title="r.status"></span>
                <CoinIcon :inst-id="r.instId" class="ic" />
                <b class="symb">{{ sym(r.instId) }}</b>
                <span class="tag" v-if="r.category !== '1'">{{ catLabel[r.category] }}</span>
                <span class="tag" v-if="!r.active">非活跃</span>
                <span class="tag bad" v-if="r.scores?.[store.ruleId]?.gate?.startsWith('BLOCKED')" :title="r.scores?.[store.ruleId]?.gate">门槛</span>
              </td>
              <td class="num"><ScoreChip :v="scoreOf(r, 'long')" side="long" /></td>
              <td class="num"><ScoreChip :v="scoreOf(r, 'short')" side="short" /></td>
              <td class="num fund"><span :class="fundingClass(r.values?.funding_hourly, 1)">{{ fundingH(r.values?.funding_hourly) }}</span></td>
              <td class="num px">{{ price(livePrice(r)) }}</td>
              <td v-for="[k] in cols" :key="k" class="num" :class="k === 'vol_surprise_4h' ? 'sec' : sign(r.values?.[k] ?? 0)">
                {{ k === 'vol_surprise_4h' ? (r.values?.[k] != null ? '×' + Math.exp(r.values[k]).toFixed(2) : '—') : logPct(r.values?.[k]) }}
              </td>
              <td class="num sec">{{ r.mcap ? usd(r.mcap) : '—' }}</td>
              <td class="num sec">{{ usd(r.turnover12h) }}<span v-if="r.provisional" class="muted">*</span></td>
            </tr>
          </tbody>
        </table>
        <Loading v-if="!store.snap" text="加载行情" />
        <div class="note pad" v-else-if="!rows.length && !others.length">{{ store.snap.phase === 'live' ? '当前筛选下没有合约' : '正在建池…' }}</div>
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
        <div v-for="r in rows" :key="r.instId" class="card pressable oi-card" :style="{ '--oi': oiFrac(r.oiUsd) }" @click="open(r)">
          <div class="l1">
            <span class="dot" :class="r.status === 'live' ? 'ok' : r.status === 'stale' ? 'bad' : 'warn'"></span>
            <CoinIcon :inst-id="r.instId" :size="22" />
            <b>{{ sym(r.instId) }}</b>
            <span class="tag" v-if="r.category !== '1'">{{ catLabel[r.category] }}</span>
            <span class="star" :class="{ on: r.watch }" @click.stop="toggleWatch(r)">{{ r.watch ? '★' : '☆' }}</span>
            <span class="fund num" title="资金费（已结算，折合每小时）"><small>费/h</small> <span :class="fundingClass(r.values?.funding_hourly, 1)">{{ fundingH(r.values?.funding_hourly) }}</span></span>
            <span class="px num">{{ price(livePrice(r)) }}</span>
          </div>
          <div class="l2">
            <span>多 <ScoreChip :v="scoreOf(r, 'long')" side="long" /></span>
            <span>空 <ScoreChip :v="scoreOf(r, 'short')" side="short" /></span>
            <span :class="sign(r.values?.ret_4h ?? 0)">4h {{ logPct(r.values?.ret_4h) }}</span>
            <span class="muted" title="12h 成交额">{{ usd(r.turnover12h) }}</span>
            <span v-if="r.mcap" class="muted" title="总市值">市值 {{ usd(r.mcap) }}</span>
          </div>
        </div>
        <Loading v-if="!store.snap" text="加载行情" />
        <div class="note pad" v-else-if="!rows.length && !others.length">{{ store.snap.phase === 'live' ? '当前筛选下没有合约' : '正在建池…' }}</div>
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

    </component>
    <template v-if="!narrow">
      <SplitterResizeHandle class="rh" />
      <SplitterPanel id="sc-work" :order="2" :min-size="30" class="work">
        <InstrumentWorkbench v-if="selected" :id="selected" :key="selected" with-list :list-open="listOpen" @toggle-list="toggleList" />
        <div v-else class="note pad">选择左侧合约查看详情</div>
      </SplitterPanel>
    </template>
  </component>
</template>

<style scoped>
.scanner { height: 100%; }
.scanner.narrow { display: block; height: auto; }
.list-wrap, .work { min-width: 0; min-height: 0; overflow: hidden; }
.narrow .list-wrap { overflow: visible; }
.rh { width: 1px; background: var(--line); position: relative; transition: background .12s; }
.rh::after { content: ''; position: absolute; inset: 0 -3px; }
.rh[data-state="hover"], .rh[data-state="drag"] { background: var(--accent); }
.list { display: flex; flex-direction: column; min-height: 0; height: 100%; }
.narrow .list { height: auto; }
/* narrow list: controls stack, rule picker takes the full row */
.slim .controls { padding: 10px; gap: 8px; }
.slim .rule { width: 100%; } .slim .rule :deep(.sel-trigger) { flex: 1; width: auto !important; }
.controls { padding: 12px 14px 10px; display: grid; gap: 10px; border-bottom: 1px solid var(--line); background: var(--bg-1); }
.controls .row { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
.grow { flex: 1; }
.rule { display: inline-flex; align-items: center; gap: 2px; }
.icon-link { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: 6px; color: var(--text-3); }
@media (hover: hover) { .icon-link:hover { background: var(--bg-2); color: var(--accent); text-decoration: none; } }
.vsep { width: 1px; height: 18px; background: var(--line-2); margin: 0 2px; }
.spacer { flex: 1; }
.sortctl { display: inline-flex; align-items: center; }
.sortctl :deep(.sel-trigger) { border-top-right-radius: 0; border-bottom-right-radius: 0; }
.sortdir { display: inline-flex; align-items: center; justify-content: center; width: 26px; height: 26px; padding: 0; margin-left: -1px; border: 1px solid var(--line-2); border-radius: 0 8px 8px 0; background: var(--bg-2); color: var(--text-2); cursor: pointer; }
@media (hover: hover) { .sortdir:hover { color: var(--accent); border-color: var(--text-3); } }
.count { font-size: var(--fs-sm); color: var(--text-1); font-weight: 600; }
.clear { display: inline-flex; align-items: center; gap: 3px; height: 24px; padding: 0 8px; border: none; background: transparent; color: var(--text-3); font-size: var(--fs-xs); }
@media (hover: hover) { .clear:hover { color: var(--text-1); background: var(--bg-2); } }
.tablewrap { overflow: auto; flex: 1; overscroll-behavior-x: contain; }
.tablewrap > table:first-child { min-width: 760px; } /* all columns; narrower lists scroll sideways */
/* pinned star + symbol columns while scrolling sideways */
.pin { position: sticky; z-index: 2; background: var(--bg-1); }
thead .pin { z-index: 3; }
.p0 { left: 0; width: 24px; min-width: 24px; }
.p1 { left: 24px; box-shadow: 1px 0 0 var(--line); }
@media (hover: hover) { tbody tr:hover .pin { background-color: var(--bg-2); } }
tbody tr.sel .pin { background-color: var(--accent-bg-solid); }
/* OI bar (fmt.oiFrac): row background filled from the left, --oi = 0..1 of the visible width */
.tablewrap > table:first-child tbody tr {
  --oiw: calc(var(--vw, 400px) * var(--oi, 0));
  background-image: linear-gradient(var(--oi-bar), var(--oi-bar)); background-repeat: no-repeat;
  background-size: var(--oiw) 100%; background-position: var(--sx, 0px) 0;
}
tbody .pin { background-color: var(--bg-0); background-image: linear-gradient(var(--oi-bar), var(--oi-bar)); background-repeat: no-repeat; }
tbody .p0 { background-size: min(var(--oiw, 0px), 100%) 100%; }
tbody .p1 { background-size: clamp(0px, calc(var(--oiw, 0px) - 24px), 100%) 100%; }
thead th { position: sticky; top: 0; background: var(--bg-1); z-index: 1; }
.sortable { cursor: pointer; user-select: none; }
tbody td, thead th { padding: 0 6px; }
tbody td:last-child, thead th:last-child { padding-right: 12px; }
thead th { height: 32px; border-bottom: 1px solid var(--line-2); }
tbody tr { cursor: pointer; height: 36px; }
tbody td { border-bottom-color: var(--line); }
@media (hover: hover) { tbody tr:hover { background-color: var(--bg-2); } }
tbody tr.sel { background-color: var(--accent-bg); box-shadow: inset 2px 0 0 var(--accent); }
tbody tr.inactive { opacity: 0.6; }
.symb { font-weight: 600; font-size: var(--fs-md); color: var(--text-1); }
td.px { color: var(--text-1); font-weight: 500; }
td.sec, td.fund { color: var(--text-2); }
td.up { color: var(--up); } td.down { color: var(--down); }
.ic { margin: 0 4px 0 2px; }
.star { color: var(--text-3); cursor: pointer; width: 18px; }
.card .star { padding: 6px 10px; margin: -6px -4px; font-size: 16px; } /* finger-sized target */
.star.on { color: var(--gold); }
.pad { padding: 16px; }

.cards { display: grid; }
.card { padding: 8px 12px; border-bottom: 1px solid var(--line); display: grid; gap: 4px; }
.card .l1 { display: flex; gap: 6px; align-items: center; }
.card .fund { margin-left: auto; font-size: 12px; color: var(--text-2); white-space: nowrap; }
.card .fund small { color: var(--text-3); font-size: var(--fs-xs); }
.card .px { margin-left: auto; font-weight: 600; }
.card .fund + .px { margin-left: 10px; }
.oi-card { background-image: linear-gradient(var(--oi-bar), var(--oi-bar)); background-repeat: no-repeat; background-size: calc(var(--oi, 0) * 100%) 100%; }
.card .l2 { display: flex; gap: 12px; align-items: center; font-size: 12px; }
.oh { display: flex; align-items: center; gap: 6px; padding: 14px 12px 6px; font-size: var(--fs-sm); font-weight: 600; color: var(--text-2); border-top: 1px solid var(--line-2); }
table.others { width: 100%; }
table.others td:first-child { width: 24px; }
</style>

<style>
.removed-pop { width: 320px; }
.removed-pop .rm { display: grid; grid-template-columns: 18px 70px 1fr auto; gap: 8px; align-items: center; padding: 5px 6px; font-size: var(--fs-sm); border-radius: 6px; }
@media (hover: hover) { .removed-pop .rm:hover { background: var(--bg-3); } }
.removed-btn[data-state="open"] { border-style: solid; border-color: var(--accent); }
</style>

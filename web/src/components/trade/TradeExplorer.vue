<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { LineChart, CandlestickChart, ChevronLeft, ChevronRight, Maximize2, Minimize2, X } from 'lucide-vue-next'
import { tradeApi, type AccountCandles, type Trade } from '../../tradeApi'
import { useDesktop } from '../../composables/device'
import { useFullscreen } from '../../composables/fullscreen'
import EquityTradesChart from '../charts/EquityTradesChart.vue'
import TradeCandleChart from '../charts/TradeCandleChart.vue'
import Loading from '../ui/Loading.vue'
import CoinIcon from '../CoinIcon.vue'
import TradeCard from './TradeCard.vue'
import TradeList, { type TradeFilter } from './TradeList.vue'
import { amt, pct, sym, tone } from './meta'
import { byInstrument } from './tradeMeta'

/**
 * Account performance explorer, shared by the backtest report and the live account page.
 * 组合视角: equity with trade bubbles; 币对视角: one instrument's candles with its trades.
 * Selecting a trade anywhere (bubble click on PC, row click, prev/next, ← →) opens it in 币对视角,
 * zoomed to the trade. The list below is synced both ways and its filters fade other nodes.
 */
const props = withDefaults(defineProps<{ accountId: number; points: [number, number][]; deposit: number; reloadKey?: number; height?: number }>(), { reloadKey: 0 })
const desktop = useDesktop()
const narrow = ref(window.innerWidth <= 900)
const vh = ref(window.innerHeight)
const onResize = () => { narrow.value = window.innerWidth <= 900; vh.value = window.innerHeight }
const root = ref<HTMLElement>()
const { full, pseudo, toggle: toggleFull } = useFullscreen(root)
watch(full, () => requestAnimationFrame(onResize))
const h = computed(() => {
  if (full.value) return Math.max(260, vh.value - (narrow.value ? 130 : 150))
  return props.height ?? (narrow.value ? 300 : 400)
})

const view = ref<'equity' | 'inst'>('equity')
const trades = shallowRef<Trade[]>([])
const loaded = ref(false)
const err = ref('')
const inst = ref('')
const candles = shallowRef<AccountCandles | null>(null)
const candleCache = new Map<string, AccountCandles>() // key instId|bar
const TF: [string, number][] = [['1m', 6e4], ['5m', 3e5], ['15m', 9e5], ['1H', 3.6e6], ['4H', 1.44e7], ['1D', 8.64e7]]
/**
 * Candle size for showing `viewMs` of time: about one candle per 8 px of chart width, but never so
 * fine that the whole account period (the endpoint always returns all of it) exceeds 4500 candles.
 */
function pickBar(viewMs: number) {
  const pts = props.points
  const period = pts.length > 1 ? pts[pts.length - 1][0] - pts[0][0] : viewMs
  const want = Math.max(30, ((narrow.value ? window.innerWidth - 40 : window.innerWidth * 0.62) || 600) / 8)
  let best = '1H', bestD = Infinity
  for (const [k, ms] of TF) {
    if (period * 1.25 / ms > 4500) continue
    const d = Math.abs(Math.log(viewMs / ms / want))
    if (d < bestD) { best = k; bestD = d }
  }
  return best
}
const barFor = (t: Trade | null) => {
  if (!t) { const pts = props.points; return pickBar(pts.length > 1 ? pts[pts.length - 1][0] - pts[0][0] : 864e5) }
  return pickBar(Math.max(3 * 3.6e6, ((t.closeTs || Date.now()) - t.openTs) * 2.2))
}
const candleBusy = ref(false)
const candleErr = ref('')
const selectedId = ref(0)
const hoverIds = ref<number[]>([])
const hoverAt = ref({ x: 0, y: 0 })
const rowHover = ref(0)
const pinned = ref(false) // phone: card pinned under the chart after a tap
const filter = ref<TradeFilter>({ inst: 'all', side: 'all', result: 'all', exit: 'all', sort: 'time' })
const eqChart = ref<InstanceType<typeof EquityTradesChart> | null>(null)
const kChart = ref<InstanceType<typeof TradeCandleChart> | null>(null)

async function load() {
  try {
    trades.value = (await tradeApi.trades(props.accountId)).slice().sort((a, b) => a.openTs - b.openTs || a.id - b.id)
    err.value = ''
    if (!inst.value && trades.value.length) inst.value = byInstrument(trades.value)[0].instId
  } catch (e: any) {
    err.value = e.message
  } finally {
    loaded.value = true
  }
}
let wantKey = ''
async function loadCandles(id: string, force = false) {
  if (!id) return
  const sel = selected.value?.instId === id ? selected.value : null
  const bar = barFor(sel)
  const key = `${id}|${bar}`
  wantKey = key // a slower response for an older request must not replace a newer one
  const hit = candleCache.get(key)
  if (hit && !force) { candles.value = hit; return }
  candleBusy.value = true
  try {
    const c = await tradeApi.candles(props.accountId, id, bar)
    candleCache.set(key, c)
    if (wantKey === key) { candles.value = c; candleErr.value = '' }
  } catch (e: any) {
    candleErr.value = e.message
  } finally {
    candleBusy.value = false
  }
}
onMounted(() => { load(); window.addEventListener('keydown', onKey); window.addEventListener('resize', onResize) })
/** Clear the selection (blank chart click / tap, Esc). */
function deselect() { selectedId.value = 0; pinned.value = false; hoverIds.value = [] }
onUnmounted(() => { window.removeEventListener('keydown', onKey); window.removeEventListener('resize', onResize) })
watch(() => props.reloadKey, () => { load(); if (view.value === 'inst' && inst.value) loadCandles(inst.value, true) })
watch(() => props.accountId, () => { candleCache.clear(); inst.value = ''; selectedId.value = 0; load() })
watch(inst, id => { if (view.value === 'inst') loadCandles(id) })
watch(view, v => { if (v === 'inst') loadCandles(inst.value) })

// ---- derived ----
const insts = computed(() => byInstrument(trades.value))
const instTrades = computed(() => trades.value.filter(t => t.instId === inst.value))
const byId = computed(() => new Map(trades.value.map(t => [t.id, t])))
const selected = computed(() => byId.value.get(selectedId.value) ?? null)
const navIdx = computed(() => trades.value.findIndex(t => t.id === selectedId.value))
const filterOn = computed(() => [filter.value.inst, filter.value.side, filter.value.result, filter.value.exit].some(v => v !== 'all'))
const visible = computed(() => {
  if (!filterOn.value) return null
  const x = filter.value
  return new Set(trades.value.filter(t =>
    (x.inst === 'all' || t.instId === x.inst) && (x.side === 'all' || t.posSide === x.side) && (x.exit === 'all' || t.exitKind === x.exit) &&
    (x.result === 'all' || (x.result === 'open' ? !t.closeTs : x.result === 'win' ? !!t.closeTs && t.net > 0 : !!t.closeTs && t.net <= 0))).map(t => t.id))
})
const hoverTrade = computed(() => (hoverIds.value.length === 1 ? byId.value.get(hoverIds.value[0]) ?? null : null))
const hoverCluster = computed(() => {
  if (hoverIds.value.length < 2) return null
  const ts = hoverIds.value.map(id => byId.value.get(id)).filter(Boolean) as Trade[]
  const closed = ts.filter(t => t.closeTs)
  return { n: ts.length, net: ts.reduce((s, t) => s + t.net, 0), wins: closed.filter(t => t.net > 0).length, closed: closed.length, insts: [...new Set(ts.map(t => sym(t.instId)))].slice(0, 6) }
})
const highlightId = computed(() => (hoverIds.value.length === 1 ? hoverIds.value[0] : rowHover.value))

// ---- actions ----
/** Open a trade in 币对视角, zoomed to it. */
async function openTrade(id: number) {
  const t = byId.value.get(id)
  if (!t) return
  selectedId.value = id
  hoverIds.value = []
  pinned.value = false
  if (inst.value !== t.instId) inst.value = t.instId
  view.value = 'inst'
  await loadCandles(t.instId)
  await nextTick()
  kChart.value?.focus(t)
}
/** Show one instrument over the whole period (no trade selected); the chart resets its view. */
async function openInst(id: string) {
  selectedId.value = 0
  if (inst.value !== id) inst.value = id
  view.value = 'inst'
  await loadCandles(id)
  await nextTick()
  kChart.value?.resetView()
}
function step(d: number) {
  if (!trades.value.length) return
  const i = navIdx.value < 0 ? (d > 0 ? -1 : trades.value.length) : navIdx.value
  const j = Math.min(trades.value.length - 1, Math.max(0, i + d))
  openTrade(trades.value[j].id)
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && selectedId.value && root.value?.offsetParent) { deselect(); return }
  if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
  const t = e.target as HTMLElement
  if (/INPUT|TEXTAREA|SELECT/.test(t.tagName) || t.isContentEditable || t.closest('[role="slider"],[role="listbox"],[role="dialog"]')) return
  if (!trades.value.length || !root.value?.offsetParent) return
  e.preventDefault()
  step(e.key === 'ArrowRight' ? 1 : -1)
}
function onEqHover(ids: number[], x: number, y: number) { hoverIds.value = ids; hoverAt.value = { x, y } }
function onEqPick(ids: number[]) {
  if (!ids.length) { deselect(); return }
  if (ids.length > 1) {
    const ts = ids.map(id => byId.value.get(id)!).filter(Boolean)
    eqChart.value?.zoomTo(Math.min(...ts.map(t => t.openTs)), Math.max(...ts.map(t => t.closeTs || t.openTs)))
    hoverIds.value = []
    return
  }
  if (desktop.value) openTrade(ids[0])
  else { selectedId.value = ids[0]; pinned.value = true }
}
function onKHover(ids: number[], x: number, y: number) { hoverIds.value = ids.length === 1 ? ids : []; hoverAt.value = { x, y } }
function onKPick(ids: number[]) { if (!ids.length) deselect(); else if (ids.length === 1) openTrade(ids[0]); else openTrade(ids[0]) }
const cardPos = computed(() => {
  const W = root.value?.querySelector('.chart')?.clientWidth ?? 800
  const left = hoverAt.value.x + 20 + 320 > W ? Math.max(4, hoverAt.value.x - 20 - 320) : hoverAt.value.x + 20
  const top = Math.max(4, Math.min(h.value - 230, hoverAt.value.y - 90))
  return { left: left + 'px', top: top + 'px' }
})

// phone: swipe the card to step through trades
let sx = 0
const onTouchStart = (e: TouchEvent) => { sx = e.touches[0].clientX }
const onTouchEnd = (e: TouchEvent) => { const dx = e.changedTouches[0].clientX - sx; if (Math.abs(dx) > 60) step(dx < 0 ? 1 : -1) }

defineExpose({ openInst, openTrade })
</script>

<template>
  <div ref="root" class="tx" :class="{ full, 'fs-pseudo': pseudo }">
    <div class="bar">
      <Segmented v-model="view" aria-label="视角" :options="[{ value: 'equity', label: '组合视角', icon: LineChart }, { value: 'inst', label: '币对视角', icon: CandlestickChart, disabled: !trades.length }]" />
      <InfoTip>
        <b>组合视角</b>：账户权益曲线，每笔交易是一个币种气泡，画在平仓时刻（盈利在曲线上方、亏损在下方；持仓中的画在开仓时刻）。气泡面积 ∝ 名义价值；外圈颜色 = 结果（绿盈红亏），虚线圈 = 开仓 / 持仓中；角标：止 = 止损，盈 = 止盈，爆 = 强平，手 = 手动。挨得太近的气泡合并成一组并显示笔数，点击放大展开。<br />
        <b>币对视角</b>：所选合约的 K 线，每次成交按成交价和时间标出（▲ 买入 ▼ 卖出，虚线圈 = 开仓），持仓区间按盈亏着色，所选交易的止损 / 止盈 / 开平仓价画成价格线。<br />
        <b>操作</b>：PC 悬停查看、点击气泡在 K 线中打开；← → 切换上一笔 / 下一笔。手机长按图表后拖动查看任意时点，点气泡看详情，左右滑动详情卡切换。滚轮 / 双指缩放，拖动平移，双击坐标轴复原。
      </InfoTip>
      <span class="sp"></span>
      <slot name="tools" />
      <template v-if="view === 'inst' && trades.length">
        <button type="button" class="ib" aria-label="上一笔" title="上一笔（←）" @click="step(-1)"><ChevronLeft :size="15" /></button>
        <span class="nv tnum">{{ navIdx >= 0 ? navIdx + 1 : '—' }}/{{ trades.length }}</span>
        <button type="button" class="ib" aria-label="下一笔" title="下一笔（→）" @click="step(1)"><ChevronRight :size="15" /></button>
      </template>
      <button type="button" class="ib" :aria-label="full ? '退出全屏' : '全屏'" :title="full ? '退出全屏（Esc）' : '全屏'" @click="toggleFull">
        <Minimize2 v-if="full" :size="14" /><Maximize2 v-else :size="14" />
      </button>
      <button v-if="pseudo" type="button" class="ib close" aria-label="关闭全屏" @click="toggleFull"><X :size="16" /></button>
    </div>

    <div v-if="view === 'inst' && insts.length" class="chips" role="tablist" aria-label="交易过的合约">
      <button v-for="c in insts" :key="c.instId" type="button" role="tab" class="chip2" :class="{ on: c.instId === inst }" :aria-selected="c.instId === inst" @click="openInst(c.instId)">
        <CoinIcon :inst-id="c.instId" :size="16" /><b>{{ sym(c.instId) }}</b>
        <span class="tnum" :class="tone(c.net)">{{ amt(c.net, 0, true) }}</span><span class="n tnum">{{ c.n }} 笔</span>
      </button>
    </div>

    <div class="stage" :class="{ split: view === 'inst' && !narrow }">
      <div class="chart" :style="{ height: h + 'px' }">
        <EquityTradesChart v-show="view === 'equity'" ref="eqChart" :points="points" :deposit="deposit" :trades="trades" :hover-id="highlightId" :selected-id="selectedId"
          :dimmed="visible" :height="h" :small="narrow" @hover="onEqHover" @pick="onEqPick" />
        <template v-if="view === 'inst'">
          <TradeCandleChart ref="kChart" :candles="candles" :trades="instTrades" :hover-id="highlightId" :selected-id="selectedId" :dimmed="visible" :height="h" :small="narrow" @hover="onKHover" @pick="onKPick" />
          <Loading overlay :show="candleBusy" />
          <p v-if="candleErr && !candles" class="cerr down">K 线读取失败：{{ candleErr }}</p>
        </template>
        <div v-if="desktop && view === 'equity' && (hoverTrade || hoverCluster)" class="hcard" :style="cardPos">
          <TradeCard v-if="hoverTrade" :t="hoverTrade" fills />
          <div v-else-if="hoverCluster" class="cl">
            <b>{{ hoverCluster.n }} 笔交易</b>
            <span class="tnum" :class="tone(hoverCluster.net)">{{ amt(hoverCluster.net, 2, true) }} USDT</span>
            <span class="muted">胜率 {{ hoverCluster.closed ? pct(hoverCluster.wins / hoverCluster.closed, 0) : '—' }} · {{ hoverCluster.insts.join(' ') }}</span>
          </div>
          <div class="hint">{{ hoverTrade ? '点击在 K 线中查看' : '点击放大展开' }}</div>
        </div>
        <div v-if="loaded && !trades.length && view === 'equity'" class="empty-tag">还没有交易</div>
      </div>
      <aside v-if="view === 'inst' && !narrow" class="side">
        <TradeCard v-if="selected" :t="selected" fills :nav="{ i: navIdx, n: trades.length }" @prev="step(-1)" @next="step(1)" />
        <p v-else class="muted small">点击 K 线上的标记或下方列表查看一笔交易</p>
      </aside>
    </div>

    <!-- phone: the selected trade under the chart; swipe to step -->
    <div v-if="narrow && selected && (view === 'inst' || pinned)" class="pcard" @touchstart.passive="onTouchStart" @touchend.passive="onTouchEnd">
      <TradeCard :t="selected" fills :kline="view === 'equity'" :nav="{ i: navIdx, n: trades.length }" :closable="view === 'equity'"
        @kline="openTrade(selected.id)" @prev="step(-1)" @next="step(1)" @close="pinned = false; selectedId = 0" />
    </div>

    <p v-if="err" class="down small">{{ err }}</p>
    <TradeList v-if="trades.length && !full" v-model:filter="filter" :trades="trades" :hover-id="highlightId" :selected-id="selectedId" @hover="id => (rowHover = id)" @select="openTrade" />
  </div>
</template>

<style scoped>
.tx { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; min-width: 0; align-content: start; }
.tx.full:not(.fs-pseudo) { padding: 12px 16px; }
.ib.close { border-color: var(--line-2); color: var(--text-1); }
.bar { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.sp { flex: 1; }
.ib { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; padding: 0; background: transparent; color: var(--text-2); }
.nv { font-size: var(--fs-xs); color: var(--text-3); min-width: 40px; text-align: center; }
.chips { display: flex; gap: 6px; overflow-x: auto; padding-bottom: 2px; scrollbar-width: thin; -webkit-overflow-scrolling: touch; }
.chip2 {
  all: unset; box-sizing: border-box; flex: none; display: inline-flex; align-items: center; gap: 6px; height: 30px; padding: 0 10px; border-radius: 15px;
  border: 1px solid var(--line-2); font-size: var(--fs-sm); color: var(--text-2); cursor: pointer; white-space: nowrap; transition: border-color .12s, background .12s;
}
@media (hover: hover) { .chip2:hover { border-color: var(--text-3); } }
.chip2.on { border-color: var(--accent); background: var(--accent-bg); color: var(--text-1); }
.chip2:focus-visible { outline: 2px solid var(--accent); }
.chip2 b { color: var(--text-1); font-weight: 600; }
.chip2 .up { color: var(--up); } .chip2 .down { color: var(--down); }
.chip2 .n { font-size: var(--fs-xs); color: var(--text-3); }
.stage { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; }
.stage.split { grid-template-columns: minmax(0, 1fr) 340px; }
.chart { position: relative; min-width: 0; }
.side { min-width: 0; padding: 12px; background: var(--bg-2); border-radius: var(--radius); max-height: 100%; overflow: auto; align-self: start; }
.hcard {
  position: absolute; z-index: 8; width: 320px; padding: 12px 14px; background: var(--bg-2); border: 1px solid var(--line-2); border-radius: 10px;
  box-shadow: 0 16px 48px rgba(0, 0, 0, .55); pointer-events: none;
}
.hcard .hint { margin-top: 8px; font-size: var(--fs-xs); color: var(--accent); }
.cl { display: grid; gap: 4px; font-size: var(--fs-sm); }
.cl b { font-size: var(--fs-lg); }
.cl .up { color: var(--up); } .cl .down { color: var(--down); }
.pcard { padding: 12px; background: var(--bg-2); border-radius: var(--radius); border: 1px solid var(--line-2); }
.cerr { position: absolute; left: 12px; bottom: 12px; font-size: var(--fs-sm); }
.empty-tag { position: absolute; right: 70px; top: 10px; font-size: var(--fs-xs); color: var(--text-3); }
.small { font-size: var(--fs-sm); }
</style>

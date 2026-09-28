<script setup lang="ts">
import Loading from './ui/Loading.vue'
import { computed, markRaw, ref, watch, type Component } from 'vue'
import { SplitterGroup, SplitterPanel, SplitterResizeHandle } from 'reka-ui'
import { BookOpenText, Gauge, GitCompareArrows, ChartColumn, LayoutGrid, PanelLeftClose, PanelLeftOpen, ChevronLeft, Star } from 'lucide-vue-next'
import { price, usd, fieldValue, catLabel, logPct, num } from '../fmt'
import { useInstrument, chgClass, statusText } from '../composables/instrument'
import { useLayoutEntry } from '../composables/layout'
import PriceChart from './PriceChart.vue'
import CompareChart from './CompareChart.vue'
import SeriesChart from './SeriesChart.vue'
import CoinIcon from './CoinIcon.vue'
import StatCard from './ui/StatCard.vue'
import StatDetailDialog from './details/StatDetailDialog.vue'
import ScoreDetail from './details/ScoreDetail.vue'
import OrderBookView from './market/OrderBookView.vue'
import { detailIcon } from './details/registry'
import InfoTip from './InfoTip.vue'

/**
 * PC instrument workspace (TradingView / OKX style): an instrument bar on top, the K-line chart as
 * the main area, and an IDE-like side bar on the right whose panels are switched from the activity
 * bar at the far right. Every divider drags (Reka UI Splitter); sizes and open panels are saved.
 */
const props = defineProps<{ id: string; listOpen?: boolean; withList?: boolean }>()
const emit = defineEmits<{ 'toggle-list': [] }>()
const { info, err, row, base, live, chartPx, score, ruleName, spread, countdown, toggleWatch } = useInstrument(() => props.id)
const detailKey = ref<string | null>(null)
const openDetail = (k: string) => (detailKey.value = k)
const detailCtx = computed(() => ({ id: props.id, info: info.value!, row: row.value }))
const ret24 = computed(() => row.value?.values?.ret_24h)
const ret4 = computed(() => row.value?.values?.ret_4h)
const sc = (side: 'long' | 'short') => score.value?.[side].score ?? null

// ---- side panels: toggled from the activity bar; the open set and its order are saved ----
interface SideDef { key: string; label: string; icon: Component }
const sides: SideDef[] = [
  { key: 'book', label: '盘口', icon: markRaw(BookOpenText) },
  { key: 'score', label: '评分', icon: markRaw(Gauge) },
  { key: 'compare', label: '相对走势', icon: markRaw(GitCompareArrows) },
  { key: 'series', label: 'OI / 资金费', icon: markRaw(ChartColumn) },
  { key: 'info', label: '概况', icon: markRaw(LayoutGrid) },
]
const DEFAULT_OPEN = ['book', 'score']
const PANELS_KEY = 'ctg.wb.panels'
function loadOpen(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(PANELS_KEY) || 'null')
    if (Array.isArray(v)) return v.filter(k => sides.some(s => s.key === k))
  } catch { /* default */ }
  return [...DEFAULT_OPEN]
}
const open = ref<string[]>(loadOpen())
watch(open, v => { try { localStorage.setItem(PANELS_KEY, JSON.stringify(v)) } catch { /* ignore */ } }, { deep: true })
const openSides = computed(() => sides.filter(s => open.value.includes(s.key)).sort((a, b) => open.value.indexOf(a.key) - open.value.indexOf(b.key)))
function toggleSide(k: string) {
  open.value = open.value.includes(k) ? open.value.filter(x => x !== k) : [...open.value, k]
}
const sideKey = computed(() => 'ctg.wb.side.' + open.value.join('+')) // one saved split per panel set
useLayoutEntry('ctg.wb', '工作台面板与比例', () => open.value.join() !== DEFAULT_OPEN.join() || Object.keys(localStorage).some(k => k.startsWith('ctg.wb.')), () => {
  for (const k of Object.keys(localStorage)) if (k.startsWith('ctg.wb.') || k.startsWith('ctg.scanner.split')) localStorage.removeItem(k)
  open.value = [...DEFAULT_OPEN]
  location.reload() // splitter sizes are read on mount
})
</script>

<template>
  <div class="wb" v-if="info">
    <!-- instrument bar -->
    <header class="ibar">
      <button v-if="withList" type="button" class="ib-btn" :title="listOpen ? '收起合约列表' : '展开合约列表'" :aria-label="listOpen ? '收起合约列表' : '展开合约列表'" @click="emit('toggle-list')">
        <PanelLeftClose v-if="listOpen" :size="17" /><PanelLeftOpen v-else :size="17" />
      </button>
      <button v-else type="button" class="ib-btn" title="返回" aria-label="返回" @click="$router.back()"><ChevronLeft :size="18" /></button>
      <div class="sym">
        <CoinIcon :inst-id="info.instId" :size="24" />
        <div class="sym-t">
          <b>{{ base }}<small>/USDT</small></b>
          <span class="sym-m">
            <span class="dot" :class="row?.status === 'live' ? 'ok' : 'warn'" :title="statusText[row?.status || ''] || '未知'"></span>
            永续 · {{ catLabel[info.category] || info.category }}
            <span v-if="row?.active" class="rk">#{{ row.rank }}</span>
            <span v-if="info.syncing" class="rk" :title="info.syncMsg">同步中</span>
          </span>
        </div>
      </div>
      <div class="pxb">
        <span class="px tnum" :class="chgClass(ret24)">{{ price(live) }}</span>
        <span class="chgs tnum">
          <span :class="chgClass(ret24)"><small>24h</small>{{ logPct(ret24) }}</span>
          <span :class="chgClass(ret4)"><small>4h</small>{{ logPct(ret4) }}</span>
        </span>
      </div>
      <div class="kvs tnum">
        <button type="button" class="kv" @click="openDetail('score')">
          <span class="k">评分 · {{ ruleName }}</span>
          <span class="v"><small>多</small><b :class="(sc('long') ?? 0) >= 50 ? 'up' : ''">{{ sc('long')?.toFixed(0) ?? '—' }}</b><small>空</small><b :class="(sc('short') ?? 0) >= 50 ? 'down' : ''">{{ sc('short')?.toFixed(0) ?? '—' }}</b></span>
        </button>
        <button type="button" class="kv" @click="openDetail('funding')">
          <span class="k">资金费 {{ info.fundingIntervalH || '?' }}h · {{ countdown }}</span>
          <span class="v"><b :class="chgClass(info.fundingRate)">{{ (info.fundingRate * 100).toFixed(4) }}%</b></span>
        </button>
        <button type="button" class="kv" @click="openDetail('oi')">
          <span class="k">持仓量 · 4h</span>
          <span class="v"><b>{{ usd(info.oiUsd) }}</b><em :class="chgClass(row?.values?.oi_chg_4h)">{{ logPct(row?.values?.oi_chg_4h) }}</em></span>
        </button>
        <button type="button" class="kv" @click="openDetail('turnover')">
          <span class="k">12h 成交额</span>
          <span class="v"><b>{{ usd(row?.turnover12h) }}</b><em>{{ row?.values?.vol_surprise_4h != null ? '×' + Math.exp(row.values.vol_surprise_4h).toFixed(2) : '' }}</em></span>
        </button>
        <button type="button" class="kv hide-md" @click="openDetail('book')">
          <span class="k">点差</span>
          <span class="v"><b>{{ num(spread, 1) }}</b><em>bps</em></span>
        </button>
        <button v-if="info.spotId" type="button" class="kv hide-md" @click="openDetail('spot')">
          <span class="k">现货 · 基差</span>
          <span class="v"><b>{{ price(info.spotLast) }}</b><em>{{ fieldValue('basis', row?.values?.basis) }}</em></span>
        </button>
      </div>
      <button class="star" :class="{ on: info.watch }" :aria-pressed="info.watch" :title="info.watch ? '取消自选' : '加入自选'" @click="toggleWatch">
        <Star :size="16" :fill="info.watch ? 'currentColor' : 'none'" />
      </button>
    </header>

    <div class="body">
      <SplitterGroup direction="horizontal" auto-save-id="ctg.wb.main" class="sg">
        <SplitterPanel id="wb-chart" :order="1" :min-size="35" class="sp">
          <PriceChart :id="id" fill :has-spot="!!info.spotId" :has-oi="info.oiUsd > 0" @live="p => (chartPx = p)" />
        </SplitterPanel>
        <template v-if="openSides.length">
          <SplitterResizeHandle class="rh col" />
          <SplitterPanel id="wb-side" :order="2" :default-size="27" :min-size="16" :max-size="60" class="sp">
            <SplitterGroup :key="sideKey" direction="vertical" :auto-save-id="sideKey" class="sg">
              <template v-for="(s, i) in openSides" :key="s.key">
                <SplitterResizeHandle v-if="i > 0" class="rh row" />
                <SplitterPanel :id="'wb-' + s.key" :order="i + 1" :min-size="10" class="sp">
                  <section class="side">
                    <div v-if="s.key === 'book' || s.key === 'score' || s.key === 'info'" class="side-h">
                      <component :is="s.icon" :size="14" /><span>{{ s.label }}</span>
                      <InfoTip v-if="s.key === 'book'">OKX 永续盘口（books-full，每侧最多 5000 档，每秒刷新），金额为 USDT。深度图上悬浮查看把价格推到某个偏离需要多少资金。</InfoTip>
                    </div>
                    <div class="side-b">
                      <OrderBookView v-if="s.key === 'book'" :id="id" compact />
                      <ScoreDetail v-else-if="s.key === 'score'" :ctx="detailCtx" />
                      <CompareChart v-else-if="s.key === 'compare'" :id="id" />
                      <SeriesChart v-else-if="s.key === 'series'" :id="id" />
                      <div v-else-if="s.key === 'info'" class="cards">
                        <StatCard label="总市值" :icon="detailIcon('supply')" @open="openDetail('supply')">
                          <template #value>{{ info.coin ? usd(info.coin.marketCap) : '—' }}<small v-if="info.coin"> USD</small></template>
                          <template #sub>{{ info.coin && info.coin.total ? '已流通 ' + ((info.coin.circulating / info.coin.total) * 100).toFixed(1) + '%' : info.category === '1' ? '无市值数据' : '非代币' }}</template>
                        </StatCard>
                        <StatCard label="买一 / 卖一" :icon="detailIcon('book')" @open="openDetail('book')">
                          <template #value>{{ price(info.bid) }} <span class="muted">/</span> {{ price(info.ask) }}</template>
                          <template #sub>点差 {{ num(spread, 1) }} bps</template>
                        </StatCard>
                        <StatCard label="OKX 现货" :icon="detailIcon('spot')" @open="openDetail('spot')">
                          <template #value>{{ info.spotId ? price(info.spotLast) : '—' }}</template>
                          <template #sub>{{ info.spotId ? '基差 ' + fieldValue('basis', row?.values?.basis) : '无同名 USDT 现货' }}</template>
                        </StatCard>
                        <StatCard label="新闻 / 解锁" :clickable="false">
                          <template #value><span class="muted">未接入</span></template>
                          <template #sub>不代表没有事件或风险</template>
                        </StatCard>
                      </div>
                    </div>
                  </section>
                </SplitterPanel>
              </template>
            </SplitterGroup>
          </SplitterPanel>
        </template>
      </SplitterGroup>

      <!-- activity bar: switch side panels -->
      <nav class="act" aria-label="侧边面板">
        <button v-for="s in sides" :key="s.key" type="button" class="act-b" :class="{ on: open.includes(s.key) }" :aria-pressed="open.includes(s.key)" :title="(open.includes(s.key) ? '关闭 ' : '打开 ') + s.label" @click="toggleSide(s.key)">
          <component :is="s.icon" :size="17" />
          <span>{{ s.label.split(' ')[0] }}</span>
        </button>
      </nav>
    </div>
    <StatDetailDialog v-model:active="detailKey" :ctx="detailCtx" :title="`${base}/USDT 永续`" />
  </div>
  <div v-else-if="err" class="note pad">{{ err }}</div>
  <Loading v-else text="加载合约" />
</template>

<style scoped>
.wb { display: flex; flex-direction: column; height: 100%; min-height: 0; background: var(--bg-0); }
.pad { padding: 16px; }

/* instrument bar */
.ibar { flex: none; display: flex; align-items: center; gap: 14px; height: 52px; padding: 0 10px 0 6px; border-bottom: 1px solid var(--line); background: var(--bg-1); min-width: 0; }
.ib-btn { all: unset; box-sizing: border-box; display: inline-grid; place-items: center; width: 30px; height: 30px; border-radius: 6px; color: var(--text-3); cursor: pointer; flex: none; }
@media (hover: hover) { .ib-btn:hover { background: var(--bg-3); color: var(--text-1); } }
.sym { display: flex; align-items: center; gap: 8px; flex: none; }
.sym-t { display: grid; line-height: 1.15; }
.sym-t b { font-size: var(--fs-lg); color: var(--text-1); font-weight: 700; }
.sym-t b small { font-size: var(--fs-sm); color: var(--text-3); font-weight: 500; }
.sym-m { display: inline-flex; align-items: center; gap: 5px; font-size: var(--fs-xs); color: var(--text-3); white-space: nowrap; }
.rk { color: var(--accent); }
.pxb { display: grid; line-height: 1.15; flex: none; min-width: 110px; }
.px { font-size: var(--fs-xl); font-weight: 700; color: var(--text-1); }
.chgs { display: inline-flex; gap: 8px; font-size: var(--fs-xs); font-weight: 600; color: var(--text-2); }
.chgs small { font-weight: 500; color: var(--text-3); margin-right: 3px; }
.up { color: var(--up) !important; } .down { color: var(--down) !important; }
.kvs { display: flex; gap: 2px; min-width: 0; overflow: hidden; flex: 1; }
.kv { all: unset; box-sizing: border-box; display: grid; line-height: 1.25; padding: 4px 10px; border-radius: 6px; cursor: pointer; min-width: 0; flex: none; }
@media (hover: hover) { .kv:hover { background: var(--bg-3); } }
.kv:focus-visible { outline: 2px solid var(--accent); }
.kv .k { font-size: var(--fs-xs); color: var(--text-3); white-space: nowrap; max-width: 220px; overflow: hidden; text-overflow: ellipsis; }
.kv .v { display: inline-flex; align-items: baseline; gap: 5px; white-space: nowrap; font-size: var(--fs-sm); }
.kv .v b { color: var(--text-1); font-weight: 600; }
.kv .v small { color: var(--text-3); font-size: var(--fs-xs); }
.kv .v em { font-style: normal; color: var(--text-3); font-size: var(--fs-xs); }
.star { all: unset; box-sizing: border-box; display: inline-grid; place-items: center; width: 30px; height: 30px; border-radius: 6px; color: var(--text-3); cursor: pointer; flex: none; }
.star.on { color: var(--gold); }
@media (hover: hover) { .star:hover { background: var(--bg-3); } }
@media (max-width: 1400px) { .hide-md { display: none; } }

/* body: splitters + activity bar */
.body { flex: 1; min-height: 0; display: flex; }
.sg { flex: 1; min-width: 0; min-height: 0; }
.sp { min-width: 0; min-height: 0; overflow: hidden; }
.sp > :deep(.panel.pc) { margin: 0; height: 100%; border-radius: 0; border: 0; }
.rh { background: var(--line); position: relative; transition: background .12s; }
.rh.col { width: 1px; } .rh.row { height: 1px; }
.rh::after { content: ''; position: absolute; inset: -3px; } /* wider grab area */
.rh[data-state="hover"], .rh[data-state="drag"] { background: var(--accent); }

.side { display: flex; flex-direction: column; height: 100%; min-height: 0; background: var(--bg-1); }
.side-h { flex: none; display: flex; align-items: center; gap: 6px; height: 34px; padding: 0 12px; font-weight: 600; font-size: var(--fs-sm); color: var(--text-1); border-bottom: 1px solid var(--line); }
.side-h > :first-child { color: var(--text-3); }
.side-b { flex: 1; min-height: 0; overflow: auto; padding: 10px 12px; container-type: inline-size; }
/* components that bring their own card chrome sit flat inside the side panel */
.side-b > :deep(.panel), .side-b > :deep(.grid > .panel) { background: transparent; border: 0; padding: 0; margin: 0; box-shadow: none; }
.side-b > :deep(.grid) { gap: 16px; }
.cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: 4px; }

.act { flex: none; width: 52px; display: flex; flex-direction: column; gap: 2px; padding: 6px 4px; border-left: 1px solid var(--line); background: var(--bg-1); }
.act-b { all: unset; box-sizing: border-box; display: grid; justify-items: center; gap: 3px; padding: 7px 0 5px; border-radius: 8px; color: var(--text-3); cursor: pointer; font-size: 10px; position: relative; }
@media (hover: hover) { .act-b:hover { background: var(--bg-3); color: var(--text-1); } }
.act-b.on { color: var(--text-1); background: var(--bg-3); }
.act-b.on::before { content: ''; position: absolute; left: -4px; top: 8px; bottom: 8px; width: 2px; border-radius: 2px; background: var(--accent); }
.act-b:focus-visible { outline: 2px solid var(--accent); }
</style>

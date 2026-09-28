<script setup lang="ts">
import Loading from './ui/Loading.vue'
import { computed, ref } from 'vue'
import { ChevronLeft, Star } from 'lucide-vue-next'
import { price, usd, fieldValue, catLabel, ago, num, logPct, fundingClass } from '../fmt'
import { useInstrument, chgClass, statusText } from '../composables/instrument'
import PriceChart from './PriceChart.vue'
import CompareChart from './CompareChart.vue'
import SeriesChart from './SeriesChart.vue'
import CoinIcon from './CoinIcon.vue'
import StatCard from './ui/StatCard.vue'
import StatDetailDialog from './details/StatDetailDialog.vue'
import OrderBookView from './market/OrderBookView.vue'
import { detailIcon } from './details/registry'
import InfoTip from './InfoTip.vue'

/** Stacked detail page (phones and narrow windows); PC uses InstrumentWorkbench. */
const props = defineProps<{ id: string }>()
const detailKey = ref<string | null>(null)
const openDetail = (k: string) => (detailKey.value = k)
const { info, err, row, base, live, chartPx, score, ruleName, spread, countdown, toggleWatch } = useInstrument(() => props.id)
const detailCtx = computed(() => ({ id: props.id, info: info.value!, row: row.value }))
const ret24 = computed(() => row.value?.values?.ret_24h)
const ret4 = computed(() => row.value?.values?.ret_4h)
const scoreText = (side: 'long' | 'short') => score.value?.[side].score?.toFixed(0) ?? '—'
const scoreTone = (side: 'long' | 'short') => ((score.value?.[side].score ?? 0) >= 50 ? (side === 'long' ? 'up' : 'down') : 'sc-n')
</script>

<template>
  <div class="detail" v-if="info">
    <div class="stack">
      <div class="head panel">
        <div class="h-top">
          <button class="icon-btn" aria-label="返回" title="返回" @click="$router.back()"><ChevronLeft :size="20" /></button>
          <CoinIcon :inst-id="info.instId" :size="28" />
          <div class="h-name">
            <div class="h-sym">{{ base }}<span class="h-quote">/USDT</span></div>
            <div class="h-meta">永续 · {{ catLabel[info.category] || info.category }}<template v-if="info.tickTs"> · {{ ago(info.tickTs) }}前</template></div>
          </div>
          <div class="h-tags">
            <span class="tag" :class="row?.status === 'live' ? 'good' : 'warn'" :title="statusText[row?.status || ''] || '未知'"><span class="dot" :class="row?.status === 'live' ? 'ok' : 'warn'"></span><span class="st-txt">{{ statusText[row?.status || ''] || '未知' }}</span></span>
            <span class="tag info" v-if="row?.active">活跃 #{{ row.rank }}</span>
            <span class="tag" v-else-if="info.eligible" title="未达 12h 阈值或数据不全">非活跃</span>
            <span class="tag" v-else title="不在全池录制范围（类别未选或盘前）：查看期间实时录制，加自选后长期保留">未录制</span>
            <span class="tag info" v-if="info.syncing" :title="info.syncMsg">同步中</span>
          </div>
          <button class="star" :class="{ on: info.watch }" :aria-pressed="info.watch" :title="info.watch ? '取消自选' : '加入自选'" @click="toggleWatch">
            <Star :size="18" :fill="info.watch ? 'currentColor' : 'none'" /><span class="star-txt">{{ info.watch ? '已自选' : '自选' }}</span>
          </button>
        </div>

        <div class="h-price">
          <div class="hp-main">
            <span class="px tnum" :class="chgClass(ret24)">{{ price(live) }}</span>
            <span class="hp-chg">
              <span class="chg" :class="chgClass(ret24)"><small>24h</small>{{ logPct(ret24) }}</span>
              <span class="chg" :class="chgClass(ret4)"><small>4h</small>{{ logPct(ret4) }}</span>
            </span>
          </div>
          <!-- key numbers; each opens its detail -->
          <div class="kv tnum">
            <button type="button" @click="openDetail('funding')">
              <span class="k">资金费 {{ info.fundingIntervalH || '?' }}h</span>
              <b :class="fundingClass(info.fundingRate, info.fundingIntervalH)">{{ (info.fundingRate * 100).toFixed(4) }}%</b>
              <span class="kx">{{ countdown }}</span>
            </button>
            <button type="button" @click="openDetail('oi')">
              <span class="k">持仓量</span><b>{{ usd(info.oiUsd) }}</b>
              <span class="kx" :class="chgClass(row?.values?.oi_chg_4h)">{{ logPct(row?.values?.oi_chg_4h) }}</span>
            </button>
            <button type="button" @click="openDetail('turnover')">
              <span class="k">12h 额</span><b>{{ usd(row?.turnover12h) }}</b>
              <span class="kx">{{ row?.values?.vol_surprise_4h != null ? '×' + Math.exp(row.values.vol_surprise_4h).toFixed(2) : '—' }}</span>
            </button>
          </div>
        </div>

        <!-- one row; scrolls sideways on narrow screens, most important first -->
        <div class="cards">
          <StatCard label="评分" :icon="detailIcon('score')" @open="openDetail('score')">
            <template #value>
              <span class="sc-l">多</span><span :class="scoreTone('long')">{{ scoreText('long') }}</span>
              <span class="sep"> · </span>
              <span class="sc-l">空</span><span :class="scoreTone('short')">{{ scoreText('short') }}</span>
            </template>
            <template #sub>{{ score ? (score.gate === 'PASS' ? ruleName : score.gate.replace('BLOCKED:', '门槛：')) : '预热中' }}</template>
          </StatCard>
          <StatCard label="总市值" :icon="detailIcon('supply')" @open="openDetail('supply')">
            <template #value>{{ info.coin ? usd(info.coin.marketCap) : '—' }}<small v-if="info.coin"> USD</small></template>
            <template #sub>{{ info.coin && info.coin.total ? '已流通 ' + ((info.coin.circulating / info.coin.total) * 100).toFixed(1) + '%' : info.category === '1' ? '无市值数据' : '非代币' }}</template>
          </StatCard>
          <StatCard label="买一 / 卖一" :icon="detailIcon('book')" @open="openDetail('book')">
            <template #value>{{ price(info.bid) }} <span class="sep">/</span> {{ price(info.ask) }}</template>
            <template #sub>点差 {{ num(spread, 1) }} bps</template>
          </StatCard>
          <StatCard label="OKX 现货" :icon="detailIcon('spot')" @open="openDetail('spot')">
            <template #value>{{ info.spotId ? price(info.spotLast) : '—' }}</template>
            <template #sub>{{ info.spotId ? '基差 ' + fieldValue('basis', row?.values?.basis) : '无同名 USDT 现货' }}</template>
          </StatCard>
        </div>
      </div>
      <PriceChart :id="id" :has-spot="!!info.spotId" :has-oi="info.oiUsd > 0" @live="p => (chartPx = p)" />
      <div class="panel">
        <h3>实时盘口 <InfoTip>OKX 永续盘口（books-full，每侧最多 5000 档——OKX 公开的最深盘口；BTC 约覆盖 ±1%，小币可达几十 %；每秒刷新；前 400 档逐档，更深的按价格合并），金额为 USDT。深度图上悬浮/拖动查看把价格推到某个偏离需要多少资金；点击上方“买一 / 卖一”可放大查看。</InfoTip></h3>
        <OrderBookView :id="id" compact />
      </div>
      <CompareChart :id="id" />
      <SeriesChart :id="id" />
      <div class="panel">
        <h3>新闻 / 解锁 / 链上持有人 <span class="tag">未接入</span> <InfoTip>
          计划在 I4 阶段接入可靠来源（项目公告、解锁日程、链上地址分布），并保留发布时间与修订。<br />
          现在显示“未接入”——<b>不代表没有事件或没有风险</b>。股票/商品永续本身没有链上数据。
        </InfoTip></h3>
      </div>
    </div>
    <StatDetailDialog v-model:active="detailKey" :ctx="detailCtx" :title="`${base}/USDT 永续`" />
  </div>
  <div v-else-if="err" class="note pad">{{ err }}</div>
  <Loading v-else text="加载合约" />
</template>

<style scoped>
.detail { padding: 12px; }
.stack { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; }
.head { padding: 12px 16px; }
.h-top { display: flex; gap: 8px; align-items: center; min-width: 0; }
.icon-btn { all: unset; box-sizing: border-box; display: inline-grid; place-items: center; width: 32px; height: 32px; margin-left: -8px; border-radius: 8px; color: var(--text-2); cursor: pointer; flex: none; }
@media (hover: hover) { .icon-btn:hover { background: var(--bg-2); color: var(--text-1); } }
.h-name { display: grid; line-height: 1.2; min-width: 0; }
.h-sym { font-size: var(--fs-xl); font-weight: 700; color: var(--text-1); letter-spacing: .2px; white-space: nowrap; }
.h-quote { font-size: var(--fs-md); font-weight: 500; color: var(--text-3); margin-left: 1px; }
.h-meta { font-size: var(--fs-xs); color: var(--text-3); white-space: nowrap; }
.h-tags { display: flex; gap: 6px; align-items: center; min-width: 0; overflow: hidden; }
.h-tags .tag { flex: none; }
.star { display: inline-flex; align-items: center; gap: 6px; margin-left: auto; flex: none; padding: 5px 12px; color: var(--text-2); }
.star.on { color: var(--gold); border-color: rgba(245, 197, 66, .45); }
.h-price { display: flex; align-items: center; gap: 16px; margin: 10px 0 8px; }
.hp-main { flex: 1; min-width: 0; display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
.hp-chg { display: inline-flex; gap: 6px; }
.px { font-size: var(--fs-3xl); font-weight: 700; line-height: 1; letter-spacing: -.3px; color: var(--text-1); }
.chg { font-size: var(--fs-sm); font-weight: 600; padding: 1px 6px; border-radius: 4px; background: var(--bg-3); color: var(--text-2); font-variant-numeric: tabular-nums; }
.chg.up { background: var(--up-bg); } .chg.down { background: var(--down-bg); }
.chg small { font-size: var(--fs-xs); font-weight: 500; opacity: .75; margin-right: 3px; }
.kv { display: flex; gap: 4px; flex: none; }
.kv button { all: unset; box-sizing: border-box; display: grid; grid-template-columns: auto auto; column-gap: 8px; align-items: baseline; padding: 4px 10px; border-radius: 6px; cursor: pointer; font-size: var(--fs-xs); }
@media (hover: hover) { .kv button:hover { background: var(--bg-2); } }
.kv button:active { background: var(--bg-2); }
.kv button:focus-visible { outline: 2px solid var(--accent); }
.kv .k { color: var(--text-3); grid-column: 1 / -1; white-space: nowrap; }
.kv b { color: var(--text-1); font-weight: 600; font-size: var(--fs-md); white-space: nowrap; }
.kv b.up, .kv .kx.up { color: var(--up); } .kv b.down, .kv .kx.down { color: var(--down); }
.kv .kx { color: var(--text-3); white-space: nowrap; }
.cards { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 4px; border-top: 1px solid var(--line); padding-top: 6px; margin: 0 -12px; }
.sc-l { font-size: var(--fs-xs); font-weight: 500; color: var(--text-3); margin-right: 3px; }
.sc-n { color: var(--text-2); }
.up { color: var(--up); } .down { color: var(--down); }
.pad { padding: 16px; }
@media (max-width: 900px) {
  .detail { padding: 8px; }
  .head { padding: 8px 12px 6px; }
  .h-sym { font-size: var(--fs-lg); }
  .h-tags .st-txt { display: none; } /* status shows as the dot only; full text in the title */
  .h-tags .tag:has(.st-txt) { padding: 2px 5px; }
  .star { padding: 0; width: 34px; height: 34px; justify-content: center; border-radius: 8px; }
  .star-txt { display: none; }
  .h-price { align-items: flex-start; gap: 8px; margin: 8px 0 6px; }
  .hp-main { flex-direction: column; align-items: flex-start; gap: 6px; }
  .px { font-size: 26px; }
  /* right column: three compact rows "label  value  extra" */
  .kv { flex-direction: column; gap: 0; }
  .kv button { grid-template-columns: 52px auto; column-gap: 6px; padding: 2px 4px; justify-content: end; }
  .kv .k { grid-column: auto; text-align: right; }
  .kv b { font-size: var(--fs-sm); text-align: right; }
  .kv .kx { display: none; }
  /* cards: one sideways-scrolling row, ~2.5 visible so the scroll is discoverable */
  .cards { display: flex; overflow-x: auto; scroll-snap-type: x mandatory; scrollbar-width: none; margin: 0 -12px; padding: 6px 4px 0; }
  .cards::-webkit-scrollbar { display: none; }
  .cards > :deep(.card) { flex: 0 0 40%; scroll-snap-align: start; padding: 6px 8px; }
  .cards :deep(.v) { font-size: var(--fs-md); }
}
</style>

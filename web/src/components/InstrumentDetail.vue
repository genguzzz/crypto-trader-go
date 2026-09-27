<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api, type InstrumentInfo } from '../api'
import { store, setRule, fieldLabel, ruleOptions } from '../store'
import { price, usd, fieldValue, catLabel, time, ago, num, logPct } from '../fmt'
import PriceChart from './PriceChart.vue'
import CompareChart from './CompareChart.vue'
import SeriesChart from './SeriesChart.vue'
import ScoreChip from './ScoreChip.vue'
import CoinIcon from './CoinIcon.vue'
import DraggablePanels from './DraggablePanels.vue'
import StatCard from './ui/StatCard.vue'
import StatDetailDialog from './details/StatDetailDialog.vue'
import OrderBookView from './market/OrderBookView.vue'
import { detailIcon } from './details/registry'
import Modal from './Modal.vue'
import InfoTip from './InfoTip.vue'
import ScoringExplainer from './ScoringExplainer.vue'

const props = defineProps<{ id: string }>()
const ruleModel = computed({ get: () => store.ruleId, set: (v: string) => setRule(v) })
const panelIds = ['head', 'score', 'price', 'book', 'compare', 'series', 'events']
const detailKey = ref<string | null>(null)
const openDetail = (k: string) => (detailKey.value = k)
const detailCtx = computed(() => ({ id: props.id, info: info.value!, row: row.value }))
const showFacts = ref(false)
const panelLabels: Record<string, string> = { head: '行情', score: '评分拆解', book: '实时盘口', price: 'K 线', compare: '相对走势', series: 'OI/资金费', events: '新闻/解锁' }
const info = ref<InstrumentInfo | null>(null)
const err = ref('')
let timer: number | undefined
const nowMs = ref(Date.now())
let clock: number | undefined
onMounted(() => { clock = window.setInterval(() => (nowMs.value = Date.now()), 1000) })
onUnmounted(() => clearInterval(clock))
const base = computed(() => props.id.split('-')[0])
const ret24 = computed(() => row.value?.values?.ret_24h)
const ret4 = computed(() => row.value?.values?.ret_4h)
const chgClass = (v?: number | null) => (v == null || v === 0 ? '' : v > 0 ? 'up' : 'down')
const countdown = computed(() => {
  const ft = info.value?.fundingTime
  if (!ft) return '—'
  const s = Math.max(0, Math.floor((ft - nowMs.value) / 1000))
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), x = s % 60
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(x).padStart(2, '0')}`
})

async function load() {
  try {
    info.value = await api.instrument(props.id)
    err.value = ''
  } catch (e: any) {
    err.value = e.message
  }
}
onMounted(() => { load(); timer = window.setInterval(load, 5000) })
onUnmounted(() => clearInterval(timer))

const row = computed(() => info.value?.row)
const score = computed(() => row.value?.scores?.[store.ruleId])
const live = computed(() => store.prices[props.id]?.[0] ?? info.value?.last)
const spread = computed(() => {
  const i = info.value
  if (!i || !i.bid || !i.ask) return null
  return ((i.ask - i.bid) / ((i.ask + i.bid) / 2)) * 1e4
})
const statusText: Record<string, string> = { live: '实时', stale: '数据缺口，补洞中', warming: '预热中', new: '上市不足 12h' }
async function toggleWatch() {
  if (!info.value) return
  await api.watch(props.id, !info.value.watch)
  load()
}
</script>

<template>
  <div class="detail" v-if="info">
    <DraggablePanels storage-key="ctg.detailLayout.v2" :ids="panelIds" :labels="panelLabels" layout-label="详情面板顺序">
      <template #head>
      <div class="head panel">
        <div class="h-top">
          <CoinIcon :inst-id="info.instId" :size="36" />
          <div class="h-name">
            <div class="h-sym">{{ base }}<span class="h-quote">/USDT</span></div>
            <div class="h-meta">永续 · {{ catLabel[info.category] || info.category }}</div>
          </div>
          <span class="tag" :class="row?.status === 'live' ? 'good' : 'warn'"><span class="dot" :class="row?.status === 'live' ? 'ok' : 'warn'"></span>{{ statusText[row?.status || ''] || '未知' }}</span>
          <span class="tag info" v-if="row?.active">活跃 #{{ row.rank }}</span>
          <span class="tag" v-else-if="info.eligible" title="未达 12h 阈值或数据不全">非活跃</span>
          <span class="tag" v-else title="不在全池录制范围（类别未选或盘前）：查看期间实时录制，加自选后长期保留">未录制</span>
          <span class="tag info" v-if="info.syncing" :title="info.syncMsg">同步历史中</span>
          <button class="watch" :class="{ on: info.watch }" @click="toggleWatch">{{ info.watch ? '★ 已自选' : '☆ 自选' }}</button>
        </div>

        <div class="h-price">
          <span class="px tnum" :class="chgClass(ret24)">{{ price(live) }}</span>
          <span class="chg" :class="chgClass(ret24)"><small>24h</small>{{ logPct(ret24) }}</span>
          <span class="chg" :class="chgClass(ret4)"><small>4h</small>{{ logPct(ret4) }}</span>
          <span class="fresh">{{ info.tickTs ? ago(info.tickTs) + ' 前更新' : '' }}</span>
        </div>

        <div class="stats">
          <StatCard label="买一 / 卖一" :icon="detailIcon('book')" @open="openDetail('book')">
            <template #value>{{ price(info.bid) }} <span class="sep">/</span> {{ price(info.ask) }}</template>
            <template #sub>点差 {{ num(spread, 1) }} bps · 看盘口深度</template>
          </StatCard>
          <StatCard label="12h 成交额" :icon="detailIcon('turnover')" @open="openDetail('turnover')">
            <template #value>{{ usd(row?.turnover12h) }}<small> USDT</small></template>
            <template #sub>4h 放量 {{ row?.values?.vol_surprise_4h != null ? '×' + Math.exp(row.values.vol_surprise_4h).toFixed(2) : '—' }}</template>
          </StatCard>
          <StatCard label="持仓量 OI" :icon="detailIcon('oi')" @open="openDetail('oi')">
            <template #value>{{ usd(info.oiUsd) }}<small> USD</small></template>
            <template #sub>4h <span :class="chgClass(row?.values?.oi_chg_4h)">{{ logPct(row?.values?.oi_chg_4h) }}</span> · {{ usd(info.oiCcy) }} 币</template>
          </StatCard>
          <StatCard :label="`资金费（${info.fundingIntervalH || '?'}h）`" :icon="detailIcon('funding')" :tone="chgClass(info.fundingRate)" @open="openDetail('funding')">
            <template #value>{{ (info.fundingRate * 100).toFixed(4) }}%</template>
            <template #sub>距结算 {{ countdown }}</template>
          </StatCard>
          <StatCard label="OKX 现货" :icon="detailIcon('spot')" @open="openDetail('spot')">
            <template #value>{{ info.spotId ? price(info.spotLast) : '—' }}</template>
            <template #sub>{{ info.spotId ? '基差 ' + fieldValue('basis', row?.values?.basis) : '无同名 USDT 现货' }}</template>
          </StatCard>
        </div>
      </div>
      </template>
      <template #score>
      <div class="panel">
        <h3>
            评分拆解
            <InfoTip label="原理"><ScoringExplainer /></InfoTip>
            <USelect v-model="ruleModel" :options="ruleOptions()" size="sm" width="240px" aria-label="评分规则" />
            <button class="small facts-btn" @click="showFacts = true">全部字段 ⤢</button>
            <RouterLink :to="`/rules/${store.ruleId}`" class="small">战绩 →</RouterLink>
          </h3>
        <div v-if="score" class="sides">
          <div v-for="side in (['long', 'short'] as const)" :key="side" class="side">
            <div class="sh">
              <span class="sl">{{ side === 'long' ? '多头分' : '空头分' }}</span>
              <span class="sv" :class="score[side].score != null && score[side].score! >= 50 ? side : ''">{{ score[side].score != null ? score[side].score!.toFixed(0) : '—' }}</span>
              <span class="tag bad" v-if="score.gate !== 'PASS'">{{ score.gate.replace('BLOCKED:', '门槛：') }}</span>
            </div>
            <div v-for="c in score[side].contrib || []" :key="c.field" class="contrib">
              <span class="f" :title="c.field">{{ fieldLabel(c.field) }}</span>
              <span class="muted v">{{ fieldValue(c.field, row?.values?.[c.field]) }}</span>
              <span class="muted p">分位 {{ c.pct.toFixed(0) }}</span>
              <span class="barwrap"><span class="bar" :class="c.points >= 0 ? 'pos' : 'neg'" :style="{ width: Math.min(100, Math.abs(c.points) * 2) + '%' }"></span></span>
              <span class="pts num" :class="c.points >= 0 ? 'up' : 'down'">{{ c.points >= 0 ? '+' : '' }}{{ c.points.toFixed(1) }}</span>
            </div>
            <div v-if="score[side].missing?.length" class="note">缺少：{{ score[side].missing!.map(fieldLabel).join('、') }} → 未评分（不是 0）</div>
          </div>
        </div>
        <div v-else class="note">暂无评分（数据预热中或缺少输入）</div>
      </div>
      </template>
      <template #book>
        <div class="panel">
          <h3>实时盘口 <InfoTip>OKX 永续盘口（每侧 400 档，每秒刷新），金额为 USDT。深度图上悬浮/拖动查看把价格推到某个偏离需要多少资金；点击上方“买一 / 卖一”可放大查看。</InfoTip></h3>
          <OrderBookView :id="id" compact />
        </div>
      </template>
      <template #price>
      <PriceChart :id="id" :has-spot="!!info.spotId" />
      </template>
      <template #compare>
      <CompareChart :id="id" />
      </template>
      <template #series>
      <SeriesChart :id="id" />
      </template>
      <template #events>
      <div class="panel">
        <h3>新闻 / 解锁 / 链上持有人 <span class="tag">未接入</span> <InfoTip>
          计划在 I4 阶段接入可靠来源（项目公告、解锁日程、链上地址分布），并保留发布时间与修订。<br />
          现在显示“未接入”——<b>不代表没有事件或没有风险</b>。股票/商品永续本身没有链上数据。
        </InfoTip></h3>
      </div>
      </template>
    </DraggablePanels>
    <StatDetailDialog v-model:active="detailKey" :ctx="detailCtx" :title="`${base}/USDT 永续`" />
    <Modal :open="showFacts" :title="`${base} 事实面板（全部字段）`" wide @close="showFacts = false">
      <div class="modal-hint"><InfoTip label="字段说明">
        <b>数值</b>：只使用决策时刻已经可见的数据；盘中每 10 秒按未收盘分钟重算（会变动），战绩只记录分钟确认值。<br />
        <b>池内分位</b>：该值在当前活跃池里的位置，0 = 最低，100 = 最高；规则打分用的就是它。<br />
        <b>不可回放</b>：历史回放中没有该字段（点差、基差、预测资金费等只在实时有），含这些字段的规则只能积累上线后战绩。<br />
        缺失显示“—”，表示未知，不当作 0。
      </InfoTip></div>
      <table>
            <thead><tr><th>字段</th><th class="num">数值</th><th class="num">池内分位</th><th class="hide-sm">定义</th></tr></thead>
            <tbody>
              <tr v-for="f in store.meta?.fields || []" :key="f.key">
                <td>{{ f.label }}<span class="tag" v-if="!f.replayable" title="历史回放中没有此字段">不可回放</span></td>
                <td class="num">{{ fieldValue(f.key, row?.values?.[f.key]) }}</td>
                <td class="num">{{ row?.pct?.[f.key] != null ? row.pct[f.key].toFixed(0) : '—' }}</td>
                <td class="muted hide-sm wrap">{{ f.desc }}</td>
              </tr>
            </tbody>
          </table>
    </Modal>
  </div>
  <div v-else class="note pad">{{ err || '加载中…' }}</div>
</template>

<style scoped>
.detail { padding: 12px 12px 12px 16px; }
.head { padding: 16px 18px 14px; }
.h-top { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.h-name { display: grid; line-height: 1.2; margin-right: 4px; }
.h-sym { font-size: var(--fs-2xl); font-weight: 700; color: var(--text-1); letter-spacing: .2px; }
.h-quote { font-size: var(--fs-lg); font-weight: 500; color: var(--text-3); margin-left: 2px; }
.h-meta { font-size: var(--fs-xs); color: var(--text-3); }
.watch { margin-left: auto; }
.h-price { display: flex; align-items: baseline; gap: 10px; margin: 14px 0 16px; flex-wrap: wrap; }
.px { font-size: var(--fs-3xl); font-weight: 700; line-height: 1; letter-spacing: -.3px; color: var(--text-1); }
.chg { font-size: var(--fs-md); font-weight: 600; padding: 2px 8px; border-radius: 4px; background: var(--bg-3); color: var(--text-2); font-variant-numeric: tabular-nums; }
.chg.up { background: var(--up-bg); } .chg.down { background: var(--down-bg); }
.chg small { font-size: var(--fs-xs); font-weight: 500; opacity: .75; margin-right: 4px; }
.fresh { margin-left: auto; font-size: var(--fs-xs); color: var(--text-3); }
.stats { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); border-top: 1px solid var(--line); padding-top: 8px; gap: 4px; margin: 0 -12px; }
.stat { padding: 0 14px; border-left: 1px solid var(--line); min-width: 0; }
.stat:first-child { padding-left: 0; border-left: none; }
.stat .l { font-size: var(--fs-xs); color: var(--text-3); margin-bottom: 4px; white-space: nowrap; }
.stat .v { font-size: var(--fs-lg); font-weight: 600; color: var(--text-1); white-space: nowrap; }
.stat .v.up { color: var(--up); } .stat .v.down { color: var(--down); }
.stat .v small { font-size: var(--fs-xs); font-weight: 500; color: var(--text-3); }
.stat .v .sep { color: var(--text-3); font-weight: 400; }
.stat .s { font-size: var(--fs-xs); color: var(--text-2); margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
h3 { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.small { font-size: 12px; font-weight: 400; }
.facts-btn { margin-left: auto; padding: 2px 10px; }
.modal-hint { margin-bottom: 8px; }
.sides { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.side .sh { display: flex; gap: 10px; align-items: baseline; margin-bottom: 8px; padding-bottom: 8px; border-bottom: 1px solid var(--line); }
.sl { font-size: var(--fs-sm); color: var(--text-3); }
.sv { font-size: var(--fs-3xl); font-weight: 700; line-height: 1; color: var(--text-2); font-variant-numeric: tabular-nums; }
.sv.long { color: var(--up); } .sv.short { color: var(--down); }
.contrib { display: grid; grid-template-columns: 96px 72px 56px 1fr 48px; gap: 8px; align-items: center; font-size: var(--fs-sm); padding: 3px 0; }
.contrib .f { color: var(--text-1); } .contrib .v { color: var(--text-2); font-variant-numeric: tabular-nums; } .contrib .p { color: var(--text-3); }
.pts { font-weight: 600; }
.barwrap { height: 4px; background: var(--bg-3); border-radius: 2px; overflow: hidden; }
.bar { display: block; height: 100%; }
.bar.pos { background: var(--up); } .bar.neg { background: var(--down); }
.wrap { white-space: normal; }
.pad { padding: 16px; }
@media (max-width: 900px) {
  .sides { grid-template-columns: 1fr; } .detail { padding: 8px; }
  .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 4px; margin: 0 -8px; }

  .px { font-size: 26px; }
}
</style>

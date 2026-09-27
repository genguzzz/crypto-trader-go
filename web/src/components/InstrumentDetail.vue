<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api, type InstrumentInfo } from '../api'
import { store, setRule, fieldLabel } from '../store'
import { price, usd, fieldValue, catLabel, time, ago, num } from '../fmt'
import PriceChart from './PriceChart.vue'
import CompareChart from './CompareChart.vue'
import SeriesChart from './SeriesChart.vue'
import ScoreChip from './ScoreChip.vue'
import CoinIcon from './CoinIcon.vue'

const props = defineProps<{ id: string }>()
const info = ref<InstrumentInfo | null>(null)
const err = ref('')
let timer: number | undefined

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
  if (!row.value) return
  await api.watch(props.id, !row.value.watch)
  load()
}
</script>

<template>
  <div class="detail" v-if="info">
    <div class="head panel">
      <div class="title">
        <CoinIcon :inst-id="info.instId" :size="32" />
        <h2>{{ info.instId }}</h2>
        <span class="tag">{{ catLabel[info.category] || info.category }}</span>
        <span class="tag" :class="row?.status === 'live' ? 'good' : 'warn'">{{ statusText[row?.status || ''] || '未知' }}</span>
        <span class="tag" v-if="row?.active">活跃 #{{ row.rank }}</span>
        <span class="tag" v-else>非活跃（未达 12h 阈值或数据不全）</span>
        <button class="watch" @click="toggleWatch">{{ row?.watch ? '★ 已自选' : '☆ 加自选' }}</button>
      </div>
      <div class="kv">
        <div><span class="muted">最新</span><b class="big">{{ price(live) }}</b></div>
        <div><span class="muted">买/卖</span>{{ price(info.bid) }} / {{ price(info.ask) }}<span class="muted"> ({{ num(spread, 1) }}bps)</span></div>
        <div><span class="muted">12h 成交额</span>{{ usd(row?.turnover12h) }} USDT</div>
        <div><span class="muted">OI</span>{{ usd(info.oiUsd) }} USD<span class="muted"> · {{ usd(info.oiCcy) }} 币</span></div>
        <div><span class="muted">资金费</span>{{ (info.fundingRate * 100).toFixed(4) }}%<span class="muted"> / {{ info.fundingIntervalH || '?' }}h · 结算 {{ time(info.fundingTime) }}</span></div>
        <div><span class="muted">现货</span>{{ info.spotId ? price(info.spotLast) : '无 OKX USDT 现货映射' }}</div>
        <div><span class="muted">更新</span>{{ info.tickTs ? ago(info.tickTs) + ' 前' : '—' }}</div>
      </div>
    </div>

    <div class="panel">
      <h3>
        评分拆解
        <select :value="store.ruleId" @change="setRule(($event.target as HTMLSelectElement).value)">
          <option v-for="r in store.meta?.rules || []" :key="r.id" :value="r.id">{{ r.name }} v{{ r.version }}</option>
        </select>
        <RouterLink :to="`/rules/${store.ruleId}`" class="small">战绩 →</RouterLink>
      </h3>
      <div v-if="score" class="sides">
        <div v-for="side in (['long', 'short'] as const)" :key="side" class="side">
          <div class="sh">
            {{ side === 'long' ? '多头' : '空头' }} <ScoreChip :v="score[side].score" :side="side" />
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
      <div class="note">分数 = 50 + 各字段在“当前活跃池”内分位（映射到 ±1）× 权重。未经样本外验证，不是胜率/下单建议；看战绩判断是否有用。</div>
    </div>

    <PriceChart :id="id" :has-spot="!!info.spotId" />
    <CompareChart :id="id" />
    <SeriesChart :id="id" />

    <div class="panel">
      <h3>事实面板（全部字段）<span class="tag" v-if="store.snap?.preview">盘中每 10 秒重算</span></h3>
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
    </div>

    <div class="panel">
      <h3>新闻 / 解锁 / 链上持有人</h3>
      <div class="note">未接入（计划 I4）。此处显示“未知”——<b>不代表没有事件或没有风险</b>。股票/商品永续本身没有链上数据。</div>
    </div>
  </div>
  <div v-else class="note pad">{{ err || '加载中…' }}</div>
</template>

<style scoped>
.detail { padding: 12px; display: grid; gap: 12px; }
.head .title { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.head h2 { margin: 0; font-size: 18px; }
.watch { margin-left: auto; }
.kv { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: 4px 16px; margin-top: 8px; }
.kv > div { display: flex; gap: 6px; align-items: baseline; }
.big { font-size: 18px; }
h3 { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.small { font-size: 12px; font-weight: 400; }
.sides { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.side .sh { font-weight: 600; margin-bottom: 6px; display: flex; gap: 6px; align-items: center; }
.contrib { display: grid; grid-template-columns: 90px 70px 56px 1fr 44px; gap: 6px; align-items: center; font-size: 12px; padding: 2px 0; }
.barwrap { height: 6px; background: var(--panel2); border-radius: 3px; overflow: hidden; }
.bar { display: block; height: 100%; }
.bar.pos { background: var(--up); } .bar.neg { background: var(--down); }
.wrap { white-space: normal; }
.pad { padding: 16px; }
@media (max-width: 900px) { .sides { grid-template-columns: 1fr; } .detail { padding: 8px; } }
</style>

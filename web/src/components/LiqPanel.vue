<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { HistogramSeries, type IChartApi, type ISeriesApi } from 'lightweight-charts'
import { api, type LiqEvent, type Liquidations } from '../api'
import { chartTime, usd, price, time as fmtTime } from '../fmt'
import { baseChart } from './charts'
import { alpha } from '../theme'
import Loading from './ui/Loading.vue'

/** Recorded liquidations of one contract: long/short totals, bars per period, latest orders. */
const props = withDefaults(defineProps<{ id: string; height?: number }>(), { height: 160 })
const period = ref(localStorage.getItem('ctg.liqPeriod') || '1H')
const depth: Record<string, number> = { '15m': 96, '1H': 72, '4H': 90 }
const barMs: Record<string, number> = { '15m': 900e3, '1H': 3600e3, '4H': 14400e3 }
const el = ref<HTMLElement>()
let chart: IChartApi | undefined
let sS: ISeriesApi<'Histogram'> | undefined
let sL: ISeriesApi<'Histogram'> | undefined
let timer: number | undefined
const busy = ref(false)
const err = ref('')
const fine = ref<Liquidations | null>(null) // 5m bars over 24h: window totals
const recent = ref<LiqEvent[]>([])
const since = ref(0)

const totals = computed(() => {
  const now = Date.now()
  return [1, 4, 24].map(h => {
    const from = now - h * 3600e3
    let long = 0, short = 0
    for (const b of fine.value?.bars || []) if (b.t + 300e3 > from) { long += b.long; short += b.short }
    return { h, long, short, partial: !!since.value && since.value > from }
  })
})
const bigCut = computed(() => { // top ~10% of the latest orders by size are highlighted
  const xs = recent.value.map(x => x.usd).sort((a, b) => b - a)
  return xs[Math.floor(xs.length / 10)] ?? Infinity
})

let seq = 0
async function load(fit = false) {
  const my = ++seq, p = period.value
  if (fit) busy.value = true
  try {
    const [bars, f] = await Promise.all([api.liquidations(props.id, p, depth[p], 20), api.liquidations(props.id, '5m', 289)])
    if (my !== seq || !chart) return
    fine.value = f
    recent.value = bars.recent || []
    since.value = bars.since
    err.value = bars.err ? '同步失败：' + bars.err : ''
    const step = barMs[p], m = new Map(bars.bars.map(b => [b.t, b]))
    const start = bars.since ? Math.floor(bars.since / step) * step : Infinity
    const now = Math.floor(Date.now() / step) * step
    const ss: { time: any; value: number }[] = [], ll: { time: any; value: number }[] = []
    for (let t = Math.max(start, now - (depth[p] - 1) * step); t <= now; t += step) {
      const b = m.get(t)
      ss.push({ time: chartTime(t), value: b?.short ?? 0 })
      ll.push({ time: chartTime(t), value: -(b?.long ?? 0) })
    }
    sS!.setData(ss)
    sL!.setData(ll)
    if (fit) chart.timeScale().fitContent()
  } catch (e: any) {
    if (my === seq) err.value = e.message
  } finally {
    if (my === seq) busy.value = false
  }
}
onMounted(() => {
  chart = baseChart(el.value!)
  const o = { priceFormat: { type: 'volume' as const }, lastValueVisible: false, priceLineVisible: false, base: 0 }
  sS = chart.addSeries(HistogramSeries, { ...o, color: alpha('up', 0.8) })
  sL = chart.addSeries(HistogramSeries, { ...o, color: alpha('down', 0.8) })
  load(true)
  timer = window.setInterval(() => { if (!document.hidden) load() }, 30_000)
})
onUnmounted(() => { clearInterval(timer); chart?.remove() })
watch(period, v => { localStorage.setItem('ctg.liqPeriod', v); load(true) })
watch(() => props.id, () => load(true))
</script>

<template>
  <div class="panel">
    <h3>
      <span>强平 <InfoTip>
        <b>数据</b>：OKX 公共接口的逐笔强平单（liquidation-orders，已成交，不需要账户），金额 = 破产价 × 张数 × 面值（USDT）。<br />
        <b>多 / 空</b>：多 = 多单被强平（强制卖出，图中红柱向下），空 = 空单被强平（强制买入，绿柱向上）。<br />
        <b>覆盖</b>：OKX 只提供最近约 24 小时；活跃池、自选、最近查看的合约每分钟同步一次，本机保留 30 天。没有记录的时段不画柱；统计窗口早于记录起点时标“不完整”。<br />
        <b>最新强平</b>：最近 20 笔，金额在其中前 10% 的加粗。
      </InfoTip></span>
      <Segmented v-model="period" size="sm" aria-label="强平周期" :options="[{ value: '15m', label: '15m' }, { value: '1H', label: '1H' }, { value: '4H', label: '4H' }]" />
    </h3>
    <div class="tot tnum">
      <div v-for="x in totals" :key="x.h" class="t">
        <span class="k">近 {{ x.h }} 小时<em v-if="x.partial" title="记录起点晚于窗口开始">（不完整）</em></span>
        <span class="v"><b class="down">{{ usd(x.long) }}</b> <small>/</small> <b class="up">{{ usd(x.short) }}</b></span>
        <span class="bar" v-if="x.long + x.short > 0"><i class="l" :style="{ width: (x.long / (x.long + x.short)) * 100 + '%' }"></i><i class="s"></i></span>
      </div>
    </div>
    <div class="lwrap"><div ref="el" :style="{ height: height + 'px' }"></div><Loading overlay :show="busy" /></div>
    <div class="note" v-if="err">{{ err }}</div>
    <div class="list tnum" v-if="recent.length">
      <div class="row hd"><span>时间</span><span>方向</span><span>价格</span><span>金额</span></div>
      <div v-for="(x, i) in recent" :key="i" class="row" :class="{ big: x.usd >= bigCut }">
        <span class="muted">{{ fmtTime(x.t) }}</span>
        <span :class="x.long ? 'down' : 'up'">{{ x.long ? '多单' : '空单' }}</span>
        <span>{{ price(x.px) }}</span>
        <span>{{ usd(x.usd) }}</span>
      </div>
    </div>
    <div class="muted empty" v-else-if="!busy">最近 24 小时没有强平记录</div>
  </div>
</template>

<style scoped>
h3 { display: flex; justify-content: space-between; align-items: center; gap: 6px; }
.lwrap { position: relative; }
.tot { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin-bottom: 8px; }
.t { display: grid; gap: 2px; min-width: 0; }
.t .k { font-size: var(--fs-xs); color: var(--text-3); white-space: nowrap; }
.t .k em { font-style: normal; color: var(--warn); }
.t .v { font-size: var(--fs-sm); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.t .v b { font-weight: 600; } .t .v small { color: var(--text-3); }
.bar { display: flex; height: 3px; border-radius: 2px; overflow: hidden; background: var(--up); }
.bar .l { background: var(--down); } .bar .s { flex: 1; }
.up { color: var(--up); } .down { color: var(--down); }
.list { margin-top: 8px; font-size: var(--fs-xs); }
.row { display: grid; grid-template-columns: 44px 40px 1fr 1fr; gap: 6px; padding: 3px 0; border-bottom: 1px solid var(--line); }
.row > :nth-child(n+3) { text-align: right; }
.row.hd { color: var(--text-3); }
.row.big { font-weight: 600; color: var(--text-1); }
.row:not(.big):not(.hd) { color: var(--text-2); }
.empty { font-size: var(--fs-xs); padding: 8px 0; }
</style>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef } from 'vue'
import { useRoute } from 'vue-router'
import TradeCandleChart from '../components/charts/TradeCandleChart.vue'
import { tradeApi, type AccountCandles, type Trade } from '../tradeApi'

/**
 * One instrument's candles with an account's trades marked (the web 币对视角 of TradeExplorer), bare,
 * for the native iOS app's WebView: /embed/trades/:accountId?inst=BTC-USDT-SWAP&trade=<id>.
 * The app selects a trade with window.ctgSelect(id) (0 = none, whole period); taps on marks post
 * {type:'pick', ids} to window.ReactNativeWebView.
 */
const props = defineProps<{ accountId: string }>()
const route = useRoute()
const inst = typeof route.query.inst === 'string' ? route.query.inst : ''
const aid = Number(props.accountId)

const trades = shallowRef<Trade[]>([])
const candles = shallowRef<AccountCandles | null>(null)
const selectedId = ref(Number(route.query.trade) || 0)
const err = ref('')
const busy = ref(true)
const chart = ref<InstanceType<typeof TradeCandleChart> | null>(null)
const h = ref(window.innerHeight)
const onResize = () => (h.value = window.innerHeight)

// candle size: ~1 candle per 8 px over the shown span, never more than 4500 for the whole period (TradeExplorer.pickBar)
const TF: [string, number][] = [['1m', 6e4], ['5m', 3e5], ['15m', 9e5], ['1H', 3.6e6], ['4H', 1.44e7], ['1D', 8.64e7]]
function pickBar(viewMs: number, period: number) {
  const want = Math.max(30, (window.innerWidth - 40 || 600) / 8)
  let best = '1H', bestD = Infinity
  for (const [k, ms] of TF) {
    if ((period * 1.25) / ms > 4500) continue
    const d = Math.abs(Math.log(viewMs / ms / want))
    if (d < bestD) { best = k; bestD = d }
  }
  return best
}

const mine = computed(() => trades.value.filter(t => t.instId === inst))
let period = 0
let barKey = ''
async function loadCandles() {
  const t = mine.value.find(x => x.id === selectedId.value)
  const bar = pickBar(t ? Math.max(3 * 3.6e6, ((t.closeTs || Date.now()) - t.openTs) * 2.2) : period, period)
  if (bar === barKey && candles.value) return
  barKey = bar
  busy.value = true
  try { candles.value = await tradeApi.candles(aid, inst, bar); err.value = '' } catch (e: any) { err.value = e.message } finally { busy.value = false }
}

type Bridge = { postMessage: (s: string) => void }
const post = (msg: Record<string, unknown>) => (window as unknown as { ReactNativeWebView?: Bridge }).ReactNativeWebView?.postMessage(JSON.stringify(msg))

async function select(id: number) {
  selectedId.value = id
  await loadCandles()
  await nextTick()
  const t = mine.value.find(x => x.id === id)
  if (t) chart.value?.focus(t)
  else chart.value?.resetView()
}
function onPick(ids: number[]) {
  post({ type: 'pick', ids })
  if (ids.length) select(ids[0])
}

onMounted(async () => {
  window.addEventListener('resize', onResize)
  ;(window as unknown as { ctgSelect: (id: number) => void }).ctgSelect = id => { select(Number(id) || 0) }
  try {
    const [ts, d] = await Promise.all([tradeApi.trades(aid), tradeApi.account(aid)])
    trades.value = ts.slice().sort((a, b) => a.openTs - b.openTs || a.id - b.id)
    const a = d.account
    const from = a.from || a.createdAt || trades.value[0]?.openTs || Date.now() - 864e5
    period = Math.max(864e5 / 4, (a.to || Date.now()) - from)
    await loadCandles()
    await nextTick()
    if (selectedId.value) { const t = mine.value.find(x => x.id === selectedId.value); if (t) chart.value?.focus(t) }
  } catch (e: any) {
    err.value = e.message
    busy.value = false
  }
})
onUnmounted(() => window.removeEventListener('resize', onResize))
</script>

<template>
  <div class="embed">
    <TradeCandleChart v-if="candles" ref="chart" :candles="candles" :trades="mine" :selected-id="selectedId" :height="h - 8" small @pick="onPick" />
    <div v-if="err" class="note pad">{{ err }}</div>
    <div v-else-if="busy && !candles" class="note pad">加载中…</div>
  </div>
</template>

<style>
html, body { background: #0e0e0e; overscroll-behavior: none; }
</style>
<style scoped>
.embed { height: 100vh; height: 100dvh; padding: 4px 6px; box-sizing: border-box; }
.pad { padding: 16px; }
</style>

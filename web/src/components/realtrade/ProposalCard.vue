<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ChevronRight, ChartCandlestick, CircleAlert } from 'lucide-vue-next'
import Modal from '../Modal.vue'
import CoinIcon from '../CoinIcon.vue'
import PriceChart from '../PriceChart.vue'
import Loading from '../ui/Loading.vue'
import { api } from '../../api'
import { price, time } from '../../fmt'
import { store } from '../../store'
import { ai } from '../../composables/ai'
import { useDesktop } from '../../composables/device'
import { rt, rtNow, fetchProposal, remember, timeLeft } from '../../composables/realtrade'
import { kindText, mgnText, orderState, orderText, realtradeApi, statusLabel, statusTone, viaText, type TradeProposal } from '../../realtradeApi'

/**
 * One AI trade proposal — 开仓 / 补仓 / 减仓 / 平仓 / 调整止盈止损, market or limit, isolated or cross:
 * what would be ordered (side, leverage, margin, entry, stop, target, R:R; the position it acts on),
 * why, its state and — once resolved — the execution result. While pending: approve (after an
 * explicit confirm step) or reject. The coin / "K 线" opens the K-line with the proposal's levels.
 * live: refetch on every SSE `realtrade` (cards in the chat); list cards are refreshed by their list.
 * compact: one summary row that expands to the full card on tap.
 */
const props = defineProps<{ id: number; live?: boolean; compact?: boolean }>()
const desktop = useDesktop()
const router = useRouter()

const p = computed<TradeProposal | undefined>(() => rt.byId[props.id])
const failed = ref(false)
async function load() { failed.value = !(await fetchProposal(props.id)) && !p.value }
onMounted(() => { if (!p.value || props.live) load() })
watch(() => store.realtradeSeq, () => { if (props.live) load() })
// the countdown ran out: the server expires it shortly after
const left = computed(() => (p.value?.status === 'pending' ? timeLeft(p.value) : ''))
watch(() => p.value?.status === 'pending' && p.value.expiresAt <= rtNow.value, gone => { if (gone) window.setTimeout(load, 2000) })

const open = ref(!props.compact)
const sym = computed(() => p.value?.instId.split('-')[0] ?? '')
const long = computed(() => p.value?.side === 'long')
const sideText = computed(() => (long.value ? '做多' : '做空'))
const u = (v?: number) => (v == null ? '—' : String(+v.toFixed(2)))
const signedU = (v: number) => (v >= 0 ? '+' : '') + v.toFixed(2)
const lots = (v: number) => String(+v.toPrecision(6))
const sourceText = computed(() => {
  const x = p.value
  if (!x) return ''
  return x.source === 'chat' ? 'AI 对话' : `${x.source === 'signal' ? '信号' : '定时'}${x.trigger ? ' · ' + x.trigger : ''}`
})
const closeText = (k?: string) => (k === 'tp' ? '止盈平仓' : k === 'sl' ? '止损平仓' : k === 'manual' ? '主动平仓' : k === 'unfilled' ? '未成交' : '平仓')
const kind = computed(() => p.value?.kind ?? 'open')
const risky = computed(() => kind.value === 'open' || kind.value === 'add')
/** The action in one sentence (the confirm step). */
const confirmMain = computed(() => {
  const x = p.value
  if (!x) return ''
  const pos = `${sym.value} ${sideText.value}`
  switch (kind.value) {
    case 'reduce': return `以${orderText(x)}减仓 ${pos} ${Math.round((x.fraction ?? 0) * 100)}%（${lots(x.contracts)} 张，持仓 ${lots(x.posContracts ?? 0)} 张）。`
    case 'close': return `以${orderText(x)}平掉 ${pos} 全部 ${lots(x.contracts)} 张。`
    case 'adjust': return `把 ${pos} 的止损改为 ${price(x.sl)}、止盈改为 ${price(x.tp)}（覆盖整个仓位）。`
  }
  return `以${orderText(x)}${kindText[kind.value]}${long.value ? '多' : '空'} ${sym.value}，保证金 ${u(x.marginUsdt)} U，杠杆 ${x.leverage}×（${mgnText(x)}，约 ${lots(x.contracts)} 张，名义 ${u(x.notionalUsdt)} U），` +
    `止损 ${price(x.sl)}（${long.value ? '−' : '+'}${x.slPct.toFixed(2)}%），止盈 ${price(x.tp)}（${long.value ? '+' : '−'}${x.tpPct.toFixed(2)}%）。`
})

// ---- rationale: clamped until expanded ----
const whyOpen = ref(false)

// ---- approve / reject ----
const confirming = ref(false)
const acting = ref<'' | 'approve' | 'reject'>('')
const msg = ref('')
async function act(kind: 'approve' | 'reject') {
  if (!p.value || acting.value) return
  acting.value = kind
  msg.value = ''
  try {
    remember(await (kind === 'approve' ? realtradeApi.approve(p.value.id) : realtradeApi.reject(p.value.id)))
    confirming.value = false
  } catch (e: any) {
    const final = e?.body?.proposal as TradeProposal | undefined
    if (final) { // someone answered first, or it expired: show what it became
      remember(final)
      confirming.value = false
      msg.value = `已被处理：${statusLabel(final)}${final.via ? '（' + (viaText[final.via] || final.via) + '）' : ''}`
    } else msg.value = e?.message || String(e)
  } finally {
    acting.value = ''
  }
}

// ---- K-line popup ----
const chartOpen = ref(false)
const inst = ref<{ hasSpot: boolean; hasOi: boolean } | null>(null)
async function showChart() {
  chartOpen.value = true
  if (inst.value || !p.value) return
  try {
    const i = await api.instrument(p.value.instId)
    inst.value = { hasSpot: !!i.spotId, hasOi: i.oiUsd > 0 }
  } catch { inst.value = { hasSpot: false, hasOi: false } }
}
function openPage() {
  if (!p.value) return
  chartOpen.value = false
  if (!desktop.value) ai.open = false
  router.push({ path: `/instrument/${p.value.instId}`, query: { proposal: String(p.value.id) } })
}

// ---- record: events and the exact OKX request / response ----
const recOpen = ref(false)
async function toggleRecord() {
  recOpen.value = !recOpen.value
  if (recOpen.value && !p.value?.events) await load()
}
const json = (v: unknown) => JSON.stringify(v, null, 2)
</script>

<template>
  <div v-if="p" class="pc" :class="{ compact: compact && !open, slim: compact }">
    <!-- compact: one row; tap to expand -->
    <button v-if="compact" type="button" class="row" :aria-expanded="open" @click="open = !open">
      <CoinIcon :inst-id="p.instId" :size="18" />
      <b class="sym">{{ sym }}</b>
      <span :class="long ? 'up' : 'down'">{{ sideText }}</span>
      <span class="muted tnum">{{ kindText[kind] }} · {{ p.leverage }}x{{ risky ? ` · ${u(p.marginUsdt)}U` : '' }}</span>
      <span class="sp"></span>
      <span v-if="p.pnlUsdt != null" class="tnum" :class="p.pnlUsdt >= 0 ? 'up' : 'down'">{{ signedU(p.pnlUsdt) }}U</span>
      <span v-else-if="p.status === 'pending'" class="muted tnum">{{ left }}</span>
      <span class="tag" :class="statusTone(p.status)">{{ statusLabel(p) }}</span>
      <span class="muted tnum">{{ time(p.created, true) }}</span>
      <ChevronRight :size="14" class="chev" :class="{ on: open }" />
    </button>

    <template v-if="open">
      <div v-if="!compact" class="hd">
        <button type="button" class="coin" :title="`查看 ${sym} K 线`" @click="showChart">
          <CoinIcon :inst-id="p.instId" :size="22" /><b>{{ sym }}</b><small>永续</small>
        </button>
        <span class="tag">{{ kindText[kind] }}</span>
        <span class="tag" :class="long ? 'good' : 'bad'">{{ sideText }} {{ p.leverage }}x</span>
        <span class="muted">{{ mgnText(p) }} · {{ orderText(p) }}</span>
        <span class="sp"></span>
        <span v-if="p.mode === 'dryrun'" class="tag warn" title="模拟：只生成并记录下单请求，不会发往 OKX">DRY RUN</span>
        <span class="tag" :class="statusTone(p.status)">{{ statusLabel(p) }}</span>
      </div>

      <div v-if="risky" class="size tnum">
        <span class="k">保证金</span>
        <span><b class="big">{{ u(p.marginUsdt) }}</b> <small>USDT</small></span>
        <span class="muted">{{ mgnText(p) }} · {{ lots(p.contracts) }} 张 · 名义 {{ u(p.notionalUsdt) }} U</span>
      </div>
      <div v-else class="size tnum">
        <span class="k">{{ kindText[kind] }}</span>
        <span v-if="kind === 'reduce'"><b class="big">{{ Math.round((p.fraction ?? 0) * 100) }}%</b> <small>{{ lots(p.contracts) }} 张</small></span>
        <span v-else-if="kind === 'close'"><b class="big">全部</b> <small>{{ lots(p.contracts) }} 张</small></span>
        <span class="muted">持仓 {{ lots(p.posContracts ?? 0) }} 张 @{{ price(p.posAvgPx) }}{{ p.posId ? ` · 仓位 #${p.posId}` : ' · 你的仓位' }}</span>
      </div>
      <div v-if="kind === 'add' && p.newAvgPx" class="inv tnum"><span class="k">补后</span>{{ lots((p.posContracts ?? 0) + p.contracts) }} 张 · 均价 ≈ {{ price(p.newAvgPx) }}<template v-if="p.liqPx"> · 强平 ≈ {{ price(p.liqPx) }}</template></div>

      <div class="lv tnum">
        <div v-if="kind !== 'adjust'"><span class="k">{{ p.fillPx || p.execPx ? '成交价' : p.orderType === 'limit' ? '限价' : '参考价' }}</span><b>{{ price(p.fillPx || (p.orderType === 'limit' ? p.px : p.execPx) || p.refPx) }}</b><small class="muted">参考 {{ price(p.refPx) }}</small></div>
        <div v-if="p.sl > 0"><span class="k">止损</span><b class="down">{{ price(p.sl) }}</b><small class="down">{{ long ? '−' : '+' }}{{ p.slPct.toFixed(2) }}%</small></div>
        <div v-if="p.tp > 0"><span class="k">止盈</span><b class="up">{{ price(p.tp) }}</b><small class="up">{{ long ? '+' : '−' }}{{ p.tpPct.toFixed(2) }}%</small></div>
        <div v-if="risky"><span class="k">盈亏比</span><b>{{ p.rr.toFixed(2) }}</b><small class="muted">R:R</small></div>
      </div>
      <div v-if="p.tpsl" class="inv"><span class="k">止盈止损</span>{{ p.tpsl }}</div>
      <div v-if="p.note" class="inv"><span class="k">注意</span>{{ p.note }}</div>

      <div v-if="p.rationale" class="why" :class="{ clamp: !whyOpen }" @click="whyOpen = !whyOpen">{{ p.rationale }}</div>
      <button v-if="p.rationale && p.rationale.length > 90" type="button" class="link" @click="whyOpen = !whyOpen">{{ whyOpen ? '收起' : '展开理由' }}</button>
      <div v-if="p.invalidation" class="inv"><span class="k">失效条件</span>{{ p.invalidation }}</div>

      <!-- result -->
      <div v-if="p.execPx || p.ordId || p.error || p.closedAt || p.ordState" class="res tnum">
        <span v-if="orderState(p)" class="tag info">{{ orderState(p) }}</span>
        <span v-if="p.fillPx || p.execPx">成交 <b>{{ price(p.fillPx || p.execPx) }}</b></span>
        <span v-if="p.ordId" class="muted">订单 {{ p.ordId }}</span>
        <span v-if="p.closedAt">{{ closeText(p.closeKind) }} <b>{{ price(p.closePx) }}</b></span>
        <span v-if="p.pnlUsdt != null">盈亏 <b :class="p.pnlUsdt >= 0 ? 'up' : 'down'">{{ signedU(p.pnlUsdt) }} U</b></span>
        <span v-if="p.error" class="err"><CircleAlert :size="13" /> {{ p.error }}</span>
      </div>

      <div class="meta">
        <span>{{ sourceText }}</span>
        <span>#{{ p.id }} · {{ time(p.created, true) }}</span>
        <span v-if="p.status === 'pending'" class="cd tnum">剩 {{ left }}</span>
        <span v-else-if="p.by || p.via">{{ p.via === 'timeout' ? '超时' : (p.by || '') + (p.via ? ' · ' + (viaText[p.via] || p.via) : '') }} · {{ time(p.statusAt, true) }}</span>
      </div>

      <div v-if="msg" class="msg">{{ msg }}</div>
      <div class="acts">
        <button type="button" class="ghost" @click="toggleRecord">{{ recOpen ? '收起记录' : '记录' }}</button>
        <button type="button" class="ghost" @click="showChart"><ChartCandlestick :size="14" /> K 线</button>
        <span class="sp"></span>
        <template v-if="p.status === 'pending'">
          <button type="button" :disabled="!!acting" @click="act('reject')">{{ acting === 'reject' ? '…' : '拒绝' }}</button>
          <button type="button" class="primary" :disabled="!!acting" @click="confirming = true">同意</button>
        </template>
      </div>

      <div v-if="recOpen" class="rec">
        <Loading v-if="!p.events" />
        <div v-for="(e, i) in p.events || []" :key="i" class="ev"><span class="muted tnum">{{ time(e.ts, true) }}</span><span>{{ e.text }}<small v-if="e.by" class="muted"> · {{ e.by }}</small></span></div>
        <template v-if="p.request"><span class="k">OKX 请求{{ p.mode === 'dryrun' ? '（未发送）' : '' }}</span><pre>{{ json(p.request) }}</pre></template>
        <template v-if="p.response"><span class="k">OKX 返回</span><pre>{{ json(p.response) }}</pre></template>
      </div>
    </template>

    <Modal :open="confirming" :title="`确认同意：${kindText[kind]}`" @close="confirming = false">
      <div class="cf">
        <p class="cf-main">{{ confirmMain }}</p>
        <p v-if="p.tpsl" class="muted">止盈止损：{{ p.tpsl }}</p>
        <p v-if="risky" class="muted">{{ p.orderType === 'limit' ? `限价单挂 ${p.orderMin} 分钟，未成交部分到期自动撤单` : `按市价成交，成交价可能与参考价 ${price(p.refPx)} 不同` }}；止损触发时这笔预计亏损约 {{ u(p.notionalUsdt * p.slPct / 100) }} U（不含手续费和滑点）。</p>
        <p v-if="p.mode === 'dryrun'" class="tag warn cf-mode">DRY RUN：只生成并记录下单请求，不会真实下单</p>
        <p v-else class="tag bad cf-mode">实盘：会在 OKX 真实下单</p>
        <div v-if="p.status === 'pending'" class="cd tnum">剩 {{ left }} 过期</div>
        <div v-if="msg" class="msg">{{ msg }}</div>
        <div class="acts">
          <button type="button" @click="confirming = false">取消</button>
          <button type="button" class="primary" :disabled="!!acting || p.status !== 'pending'" @click="act('approve')">{{ acting === 'approve' ? '提交中…' : '确认同意' }}</button>
        </div>
      </div>
    </Modal>

    <Modal :open="chartOpen" :title="`${sym} · 提案 #${p.id}`" wide @close="chartOpen = false">
      <div class="chart-box">
        <PriceChart v-if="inst" :id="p.instId" :has-spot="inst.hasSpot" :has-oi="inst.hasOi" :proposal="p.id" />
        <Loading v-else />
        <button type="button" class="link" @click="openPage">在详情页打开 ›</button>
      </div>
    </Modal>
  </div>
  <div v-else-if="failed" class="pc muted gone">交易提案 #{{ id }} 无法加载</div>
</template>

<style scoped>
.pc { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; margin: 6px 0 10px; padding: 12px; border-radius: var(--radius); background: var(--bg-1); border: 1px solid var(--line-2); }
.pc.slim { margin: 0 0 6px; }
.pc.compact { padding: 0; gap: 0; }
.gone { font-size: var(--fs-sm); }
.row { all: unset; box-sizing: border-box; display: flex; align-items: center; gap: 6px; min-width: 0; padding: 9px 10px; cursor: pointer; font-size: var(--fs-sm); }
.pc:not(.compact) .row { padding: 0 0 8px; border-bottom: 1px solid var(--line); }
.row .sym { flex: none; }
.row .muted { white-space: nowrap; }
.chev { flex: none; color: var(--text-3); transition: transform .2s; }
.chev.on { transform: rotate(90deg); }
.sp { flex: 1; }
.hd { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.coin { all: unset; display: inline-flex; align-items: center; gap: 6px; padding: 2px 10px 2px 2px; border-radius: 999px; cursor: pointer; background: var(--bg-2); border: 1px solid var(--line-2); }
.coin b { font-size: var(--fs-lg); }
.coin small { font-size: var(--fs-xs); color: var(--text-3); }
@media (hover: hover) { .coin:hover { border-color: var(--accent); } }
.k { display: block; font-size: var(--fs-xs); color: var(--text-3); }
.size { display: grid; gap: 1px; }
.size .big { font-size: var(--fs-2xl); font-weight: 700; }
.size small, .lv small { font-size: var(--fs-xs); }
.size .muted { font-size: var(--fs-xs); }
.lv { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; }
.lv > div { display: grid; min-width: 0; }
.lv b { font-size: var(--fs-md); font-weight: 600; overflow: hidden; text-overflow: ellipsis; }
@media (max-width: 420px) { .lv { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
.why { font-size: var(--fs-sm); line-height: 1.6; color: var(--text-2); white-space: pre-wrap; word-break: break-word; cursor: pointer; }
.why.clamp { display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
.link { all: unset; justify-self: start; font-size: var(--fs-xs); color: var(--accent); cursor: pointer; }
.inv { font-size: var(--fs-sm); color: var(--text-2); word-break: break-word; }
.res { display: flex; flex-wrap: wrap; gap: 4px 12px; padding: 8px 10px; border-radius: var(--radius-sm); background: var(--bg-2); font-size: var(--fs-sm); }
.err { display: inline-flex; align-items: center; gap: 4px; color: var(--down); word-break: break-word; }
.meta { display: flex; flex-wrap: wrap; gap: 2px 10px; font-size: var(--fs-xs); color: var(--text-3); }
.cd { color: var(--warn); }
.msg { font-size: var(--fs-sm); color: var(--warn); }
.acts { display: flex; align-items: center; gap: 8px; }
.acts button { height: 32px; padding: 0 14px; display: inline-flex; align-items: center; gap: 4px; }
.acts .ghost { padding: 0 8px; background: transparent; border-color: transparent; color: var(--text-2); font-size: var(--fs-sm); }
button:disabled { opacity: .45; cursor: default; }
.rec { display: grid; grid-template-columns: minmax(0, 1fr); gap: 6px; font-size: var(--fs-xs); }
.ev { display: flex; gap: 8px; }
.ev span:last-child { min-width: 0; word-break: break-word; color: var(--text-2); }
pre { margin: 0; max-height: 220px; overflow: auto; padding: 8px 10px; border-radius: var(--radius-sm); background: var(--bg-0); border: 1px solid var(--line);
  font: 11px/1.5 ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-2); white-space: pre-wrap; word-break: break-all; }
.cf { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; }
.cf p { margin: 0; line-height: 1.7; }
.cf-main { font-size: var(--fs-lg); }
.cf .muted { font-size: var(--fs-sm); }
.cf-mode { justify-self: start; font-size: var(--fs-sm); }
.cf .acts { justify-content: flex-end; }
.chart-box { display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; }
.chart-box .link { justify-self: end; font-size: var(--fs-sm); }
</style>

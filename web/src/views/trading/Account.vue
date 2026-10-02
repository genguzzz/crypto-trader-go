<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ChevronLeft, ArrowDownToLine, SlidersHorizontal, XCircle, Archive, ArchiveRestore, Plus, FileBarChart } from 'lucide-vue-next'
import { tradeApi, type AccountDetail, type Fill, type Instance, type InstanceAction, type LedgerEntry, type Order, type Position, type Signal } from '../../tradeApi'
import { useTradeLive } from '../../composables/tradeLive'
import { time, price } from '../../fmt'
import EquityChart from '../../components/charts/EquityChart.vue'
import SignalList from '../../components/trade/SignalList.vue'
import InstanceCard from '../../components/trade/InstanceCard.vue'
import InstanceEditor from '../../components/trade/InstanceEditor.vue'
import AccountSettingsModal from '../../components/trade/AccountSettingsModal.vue'
import FillsTable from '../../components/trade/FillsTable.vue'
import OrdersTable from '../../components/trade/OrdersTable.vue'
import KpiRow, { type Kpi } from '../../components/ui/KpiRow.vue'
import Loading from '../../components/ui/Loading.vue'
import NumInput from '../../components/ui/NumInput.vue'
import Modal from '../../components/Modal.vue'
import CoinIcon from '../../components/CoinIcon.vue'
import { accountStatus, amt, ledgerLabel, modeLabel, pct, qty, sym, tone } from '../../components/trade/meta'

const props = defineProps<{ id: string }>()
const router = useRouter()
const aid = computed(() => Number(props.id))

const d = ref<AccountDetail | null>(null)
const insts = ref<Instance[] | null>(null)
const eq = ref<[number, number][]>([])
const signals = ref<Signal[] | null>(null)
const fills = ref<Fill[] | null>(null)
const history = ref<Order[] | null>(null)
const ledger = ref<LedgerEntry[] | null>(null)
const error = ref('')
const tab = ref<'pos' | 'open' | 'orders' | 'fills' | 'ledger'>('pos')
const range = ref<number>(0) // days; 0 = all

const DAY = 86_400_000
async function load() {
  try {
    const from = range.value ? Date.now() - range.value * DAY : undefined
    const [a, i, e, s] = await Promise.all([tradeApi.account(aid.value), tradeApi.instances(aid.value), tradeApi.equity(aid.value, from), tradeApi.signals(aid.value, 200)])
    d.value = a; insts.value = i; eq.value = e; signals.value = s
    error.value = ''
    loadTab()
  } catch (e: any) {
    error.value = e.message
  }
}
async function loadTab() {
  try {
    if (tab.value === 'fills') fills.value = await tradeApi.fills(aid.value, 200)
    else if (tab.value === 'orders') history.value = await tradeApi.orders(aid.value, 200)
    else if (tab.value === 'ledger') ledger.value = await tradeApi.ledger(aid.value, 200)
  } catch (e: any) {
    error.value = e.message
  }
}
useTradeLive(load, { accountId: () => aid.value })
watch(tab, loadTab)
watch(range, () => tradeApi.equity(aid.value, range.value ? Date.now() - range.value * DAY : undefined).then(x => (eq.value = x)).catch(() => {}))
watch(aid, () => { d.value = null; insts.value = null; signals.value = null; load() })

const a = computed(() => d.value?.account)
const isBacktest = computed(() => a.value?.mode === 'backtest')
const running = computed(() => insts.value?.filter(i => i.state === 'running').length)
const st = computed(() => (a.value ? accountStatus(a.value, running.value) : null))
const positions = computed(() => d.value?.positions || [])
const upnl = computed(() => positions.value.reduce((s, p) => s + p.unrealizedPnl, 0))
const kpis = computed<Kpi[]>(() => {
  const x = a.value
  if (!x) return []
  const total = x.equity - x.deposit
  return [
    { label: '权益 USDT', value: amt(x.equity), sub: `入金 ${amt(x.deposit, 0)}` },
    { label: '累计收益', value: pct(x.deposit ? total / x.deposit : null, 2, true), sub: amt(total, 2, true) + ' USDT', tone: tone(total) },
    ...(isBacktest.value ? [] : [{ label: '今日盈亏', value: amt(x.dayPnl, 2, true), sub: x.halted ? '已熔断，今日停止开仓' : `熔断线 -${pct(x.limits.dailyLossPct, 1)}`, tone: tone(x.dayPnl) } as Kpi]),
    { label: '未实现盈亏', value: amt(upnl.value, 2, true), sub: `${positions.value.length} 个持仓`, tone: tone(upnl.value) },
    { label: '可用', value: amt(x.cash), sub: `保证金 ${amt(positions.value.reduce((s, p) => s + p.margin, 0))}` },
  ]
})
const posPct = (p: Position) => (p.margin ? p.unrealizedPnl / p.margin : null)
const liqDist = (p: Position) => (p.markPx && p.liqPx ? Math.abs(p.markPx - p.liqPx) / p.markPx : null)
const instName = (id: number) => insts.value?.find(i => i.id === id)?.name || (id ? `#${id}` : '手动')

// ---- actions ----
const busy = ref(false)
const msg = ref('')
async function run<T>(f: () => Promise<T>, ok?: string) {
  busy.value = true
  msg.value = ''
  try { await f(); if (ok) msg.value = ok; await load() } catch (e: any) { error.value = e.message } finally { busy.value = false }
}
const closeOne = (p: Position) => { if (confirm(`按市价平掉 ${p.instId} ${p.posSide === 'long' ? '多' : '空'}仓 ${qty(p.qty)} 张？`)) run(() => tradeApi.close(aid.value, p.instId), '已提交平仓') }
const closeAll = () => { if (confirm(`按市价平掉全部 ${positions.value.length} 个持仓？`)) run(() => tradeApi.close(aid.value), '已提交全部平仓') }
const act = (i: Instance, x: InstanceAction) => {
  if (x === 'stop' && !confirm(`停止「${i.name}」？已有持仓保留，可以手动平仓。`)) return
  run(() => tradeApi.instanceAction(i.id, x))
}
async function archive(on: boolean) {
  if (on && !confirm(`归档「${a.value?.name}」？归档前请先停止策略、平掉持仓；归档后账户只读。`)) return
  await run(() => tradeApi.patchAccount(aid.value, { archived: on }))
  if (on) router.push('/trading')
}

const depOpen = ref(false)
const depAmt = ref(1000)
const depErr = computed(() => (depAmt.value > 0 && depAmt.value <= 1e9 ? '' : '请输入正数'))
async function deposit() {
  if (depErr.value) return
  depOpen.value = false
  await run(() => tradeApi.deposit(aid.value, depAmt.value), `已入金 ${amt(depAmt.value, 0)} USDT`)
}
const setOpen = ref(false)
const editInst = ref<Instance | null>(null)
const instOpen = ref(false)
function openInst(i: Instance | null) { editInst.value = i; instOpen.value = true }
function onInstSaved() { instOpen.value = false; load() }
</script>

<template>
  <div class="page acc">
    <div v-if="error" class="banner">{{ error }}</div>
    <Loading v-if="!a && !error" text="加载账户" />

    <template v-if="a">
      <header class="hd">
        <RouterLink to="/trading" class="back" aria-label="返回账户列表"><ChevronLeft :size="18" /></RouterLink>
        <h1>{{ a.name }}</h1>
        <span class="tag" :class="modeLabel[a.mode].c">{{ modeLabel[a.mode].t }}</span>
        <span v-if="st" class="tag" :class="st.c">{{ st.t }}</span>
        <InfoTip>
          <b>权益</b> = 现金 + 持仓保证金 + 未实现盈亏（按标记价）。<b>可用</b> = 不被保证金占用的现金。<br />
          <b>熔断</b>：当日（UTC+8）亏损达到“日亏损熔断”后当天不再接受开仓信号，平仓与止损照常；次日自动解除。<br />
          所有成交都是模拟：实时账户按当时盘口逐档成交（受滑点容忍限制），止损止盈按市价触发。
        </InfoTip>
        <span class="acts">
          <RouterLink v-if="isBacktest" :to="`/trading/backtests/${a.id}`" class="btn"><FileBarChart :size="14" />回测报告</RouterLink>
          <template v-if="!isBacktest && !a.archived">
            <button type="button" :disabled="busy" @click="depOpen = true"><ArrowDownToLine :size="14" />入金</button>
            <button type="button" :disabled="busy" @click="setOpen = true"><SlidersHorizontal :size="14" />风控</button>
            <button type="button" :disabled="busy || !positions.length" @click="closeAll"><XCircle :size="14" />全部平仓</button>
          </template>
          <button v-if="!a.archived" type="button" class="ghost" :disabled="busy" title="归档" @click="archive(true)"><Archive :size="14" /><span class="hide-sm">归档</span></button>
          <button v-else type="button" :disabled="busy" @click="archive(false)"><ArchiveRestore :size="14" />取消归档</button>
        </span>
      </header>
      <div v-if="msg" class="ok-msg">{{ msg }}</div>

      <KpiRow :items="kpis" />

      <div class="cols">
        <div class="main">
          <div class="panel">
            <h3 class="h3f">权益曲线 <InfoTip>每分钟一个点（回测为每根 K 线），超过 2000 点时降采样。虚线是累计入金：曲线在它上方即盈利。</InfoTip>
              <span class="sp"></span>
              <Segmented v-if="!isBacktest" v-model="range" size="sm" aria-label="区间" :options="[{ value: 1, label: '1D' }, { value: 7, label: '7D' }, { value: 30, label: '30D' }, { value: 0, label: '全部' }]" />
            </h3>
            <EquityChart v-if="eq.length > 1" :points="eq" :deposit="a.deposit" :height="240" />
            <p v-else class="muted small">数据点不足，运行一段时间后显示</p>
          </div>

          <div class="panel">
            <div class="tabs">
              <Segmented v-model="tab" size="sm" aria-label="明细" :options="[
                { value: 'pos', label: `持仓 ${positions.length}` }, { value: 'open', label: `委托 ${d?.orders.length ?? 0}` },
                { value: 'orders', label: '历史' }, { value: 'fills', label: '成交' }, { value: 'ledger', label: '流水' }]" />
              <InfoTip>
                <b>持仓</b>：逐仓。强平价按 0.5% 维持保证金率估算；标记价穿过强平价时整仓按强平价平掉，保证金损失。<br />
                <b>委托</b>：未完成的委托，包括等待触发的止损、止盈（OCO：一个成交另一个自动撤销）。<br />
                <b>流水</b>：入金、手续费、资金费、已实现盈亏、强平，每一笔资金变动。
              </InfoTip>
            </div>

            <div v-if="tab === 'pos'" class="table-scroll">
              <table v-if="positions.length" class="t">
                <thead><tr><th>合约</th><th class="num">数量<small>张</small></th><th class="num">开仓价</th><th class="num">标记价</th><th class="num">强平价</th><th class="num">止损 / 止盈</th><th class="num">未实现<small>USDT</small></th><th class="num">保证金</th><th>策略</th><th></th></tr></thead>
                <tbody>
                  <tr v-for="p in positions" :key="p.instId + p.posSide + p.instanceId">
                    <td><span class="inst"><CoinIcon :inst-id="p.instId" :size="15" /><b>{{ sym(p.instId) }}</b><span class="tag" :class="p.posSide === 'long' ? 'good' : 'bad'">{{ p.posSide === 'long' ? '多' : '空' }} {{ p.leverage }}x</span></span></td>
                    <td class="num">{{ qty(p.qty) }}</td>
                    <td class="num">{{ price(p.entryPx) }}</td>
                    <td class="num">{{ price(p.markPx) }}</td>
                    <td class="num"><span class="warn-t">{{ price(p.liqPx) }}</span><small class="d">{{ pct(liqDist(p), 1) }}</small></td>
                    <td class="num"><span class="down">{{ price(p.stopLoss) }}</span> / <span class="up">{{ price(p.takeProfit) }}</span></td>
                    <td class="num" :class="tone(p.unrealizedPnl)"><b>{{ amt(p.unrealizedPnl, 2, true) }}</b><small class="d">{{ pct(posPct(p), 1, true) }}</small></td>
                    <td class="num sub">{{ amt(p.margin) }}</td>
                    <td class="sub">{{ instName(p.instanceId) }}<small class="d">{{ time(p.openedAt, true) }}</small></td>
                    <td><button v-if="!isBacktest && !a.archived" type="button" class="sm" :disabled="busy" @click="closeOne(p)">平仓</button></td>
                  </tr>
                </tbody>
              </table>
              <p v-else class="muted small">没有持仓</p>
            </div>
            <OrdersTable v-else-if="tab === 'open'" :orders="d?.orders || []" empty="没有未完成的委托" />
            <OrdersTable v-else-if="tab === 'orders'" :orders="history" />
            <FillsTable v-else-if="tab === 'fills'" :fills="fills" :instances="insts || []" />
            <div v-else class="table-scroll">
              <table v-if="ledger?.length" class="t">
                <thead><tr><th>时间</th><th>类型</th><th>合约</th><th class="num">金额<small>USDT</small></th></tr></thead>
                <tbody>
                  <tr v-for="l in ledger.slice().sort((x, y) => y.ts - x.ts)" :key="l.id">
                    <td class="tm">{{ time(l.ts, true) }}</td><td>{{ ledgerLabel[l.kind] || l.kind }}</td>
                    <td>{{ l.instId ? sym(l.instId) : '—' }}</td><td class="num" :class="tone(l.amount)">{{ amt(l.amount, 4, true) }}</td>
                  </tr>
                </tbody>
              </table>
              <p v-else class="muted small">{{ ledger ? '没有资金流水' : '加载中…' }}</p>
            </div>
          </div>
        </div>

        <aside class="side">
          <div class="panel">
            <h3 class="h3f">策略实例 <InfoTip>
              <b>状态</b>：未启动 / 运行中 / 已暂停（不处理新 K 线，持仓与止损照常）/ 故障（策略进程超时或连续出错，自动重启 3 次后停止）/ 已停止。<br />
              <b>修改参数</b>会生成新的配置版本（v2、v3…），之后的信号记新版本，历史信号保留原版本，便于对比。<br />
              停止策略不会平仓；需要时请手动平仓。
            </InfoTip>
              <span class="sp"></span>
              <button v-if="!isBacktest && !a.archived" type="button" class="sm" @click="openInst(null)"><Plus :size="13" />添加</button>
            </h3>
            <InstanceCard v-for="i in insts || []" :key="i.id" :inst="i" :busy="busy" :readonly="isBacktest || a.archived" @act="x => act(i, x)" @edit="openInst(i)" />
            <p v-if="insts && !insts.length" class="muted small">还没有策略。点“添加”把策略挂到这个账户。</p>
          </div>
          <div class="panel">
            <h3>信号 <InfoTip>
              策略发出的每个信号和它的去向：<b>已下单</b>、<b>风控拒绝</b>（原因见“结果”）、<b>AI 跳过</b>、<b>用户跳过</b>（人工确认时选了跳过）、<b>过期</b>（等待确认或 AI 超过有效期）、<b>只记录</b>（只记录模式）。<br />
              时间是信号对应 K 线的收盘时间（交易所时间）；悬停可看本地收到时间。
            </InfoTip></h3>
            <SignalList :signals="signals" :instances="insts || []" />
          </div>
        </aside>
      </div>

      <Modal :open="depOpen" title="入金" @close="depOpen = false">
        <div class="dep">
          <div class="muted small">模拟入金，立即计入可用资金和累计入金（收益率按累计入金计算）。</div>
          <div class="line">
            <Segmented :model-value="depAmt" size="sm" aria-label="金额预设" :options="[1000, 5000, 10000].map(v => ({ value: v, label: amt(v, 0) }))" @update:model-value="v => (depAmt = v)" />
            <NumInput v-model="depAmt" unit="USDT" :min="1" width="96px" aria-label="入金金额" />
          </div>
          <div v-if="depErr" class="down small">{{ depErr }}</div>
          <div class="ft"><button type="button" @click="depOpen = false">取消</button><button type="button" class="primary" :disabled="!!depErr || busy" @click="deposit">确认入金</button></div>
        </div>
      </Modal>
      <AccountSettingsModal :open="setOpen" :account="a" @close="setOpen = false" @saved="setOpen = false; load()" />
      <InstanceEditor :open="instOpen" :account-id="a.id" :inst="editInst" start-on-create @close="instOpen = false" @saved="onInstSaved" />
    </template>
  </div>
</template>

<style scoped>
.banner { border-radius: var(--radius-sm); }
.hd { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; min-width: 0; }
.back { display: inline-flex; color: var(--text-3); margin-left: -4px; }
.hd h1 { margin: 0; font-size: var(--fs-2xl); font-weight: 600; min-width: 0; overflow-wrap: anywhere; }
.acts { margin-left: auto; display: inline-flex; gap: 6px; flex-wrap: wrap; }
.acts button, .btn { display: inline-flex; align-items: center; gap: 4px; height: 30px; padding: 0 10px; font-size: var(--fs-sm); }
.btn { border: 1px solid var(--line-2); border-radius: var(--radius-sm); background: var(--bg-3); color: var(--text-1); text-decoration: none !important; }
button.ghost { background: transparent; color: var(--text-3); }
button:disabled { opacity: .45; cursor: default; }
.ok-msg { font-size: var(--fs-sm); color: var(--up); }

.cols { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; align-items: start; }
.main, .side { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; min-width: 0; }
@media (min-width: 1100px) { .cols { grid-template-columns: minmax(0, 1fr) 400px; } }
.h3f { display: flex; align-items: center; gap: 4px; }
.sp { flex: 1; }
button.sm { display: inline-flex; align-items: center; gap: 3px; height: 26px; padding: 0 8px; font-size: var(--fs-sm); }
.tabs { display: flex; align-items: center; gap: 6px; margin-bottom: 10px; max-width: 100%; }
.tabs .seg { min-width: 0; overflow-x: auto; scrollbar-width: none; }

.t td { font-size: var(--fs-sm); font-variant-numeric: tabular-nums; }
.t small { margin-left: 2px; color: var(--text-3); font-size: 10px; }
.t small.d { display: block; margin: 0; }
.t td.num small.d { text-align: right; }
.inst { display: inline-flex; align-items: center; gap: 6px; }
.sub { color: var(--text-2); }
.tm { color: var(--text-3); }
.warn-t { color: var(--warn); }
.small { font-size: var(--fs-sm); }

.dep { display: grid; gap: 12px; }
.line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.ft { display: flex; justify-content: flex-end; gap: 8px; }
.ft button { height: 32px; padding: 0 16px; }
@media (max-width: 900px) {
  .acts { margin-left: 0; width: 100%; }
}
</style>

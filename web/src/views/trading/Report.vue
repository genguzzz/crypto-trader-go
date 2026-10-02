<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronLeft, Radio, RotateCw, ListTree } from 'lucide-vue-next'
import { tradeApi, type AccountDetail, type Fill, type Instance, type Report } from '../../tradeApi'
import { useTradeLive } from '../../composables/tradeLive'
import { store } from '../../store'
import EquityChart from '../../components/charts/EquityChart.vue'
import FillsTable from '../../components/trade/FillsTable.vue'
import Loading from '../../components/ui/Loading.vue'
import CoinIcon from '../../components/CoinIcon.vue'
import { amt, day, execLabel, pct, stateLabel, sym, tone, universeText } from '../../components/trade/meta'

const props = defineProps<{ id: string }>()
const aid = computed(() => Number(props.id))
const d = ref<AccountDetail | null>(null)
const rep = ref<Report | null>(null)
const insts = ref<Instance[] | null>(null)
const eq = ref<[number, number][]>([])
const fills = ref<Fill[] | null>(null)
const error = ref('')
const repErr = ref('')

async function load() {
  try {
    const [a, i, e, f] = await Promise.all([tradeApi.account(aid.value), tradeApi.instances(aid.value), tradeApi.equity(aid.value), tradeApi.fills(aid.value, 500)])
    d.value = a; insts.value = i; eq.value = e; fills.value = f
    error.value = ''
  } catch (e: any) {
    error.value = e.message
  }
  try { rep.value = await tradeApi.report(aid.value); repErr.value = '' } catch (e: any) { repErr.value = e.message }
}
useTradeLive(load, { accountId: () => aid.value })

const a = computed(() => d.value?.account)
const running = computed(() => (insts.value || []).some(i => i.state === 'running' || i.state === 'created'))
const faulted = computed(() => (insts.value || []).filter(i => i.state === 'faulted' || i.state === 'stopped'))
const progress = computed(() => {
  // replay clock ≈ the last equity point between from and to
  const x = a.value
  const last = eq.value[eq.value.length - 1]?.[0]
  if (!x?.from || !x.to || !last) return null
  return Math.max(0, Math.min(1, (last - x.from) / (x.to - x.from)))
})
const ruleName = (id: string) => store.meta?.rules.find(r => r.id === id)?.name || id

interface M { l: string; v: string; s?: string; t?: string; big?: boolean }
const metrics = computed<M[]>(() => {
  const r = rep.value, x = a.value
  if (!r || !x) return []
  const total = r.signals.ordered + r.signals.rejected + r.signals.skipped
  return [
    { l: '净收益', v: pct(x.deposit ? r.net / x.deposit : null, 2, true), s: `${amt(r.net, 2, true)} USDT`, t: tone(r.net), big: true },
    { l: '最大回撤', v: pct(-Math.abs(r.maxDrawdown), 2), s: '自权益高点', t: r.maxDrawdown ? 'down' : '', big: true },
    { l: '夏普', v: isFinite(r.sharpe) ? r.sharpe.toFixed(2) : '—', s: '日收益年化', big: true },
    { l: '胜率', v: pct(r.winRate, 1), s: `${r.trades} 笔交易`, big: true },
    { l: '盈亏比', v: isFinite(r.profitFactor) ? r.profitFactor.toFixed(2) : '—', s: '总盈利 ÷ 总亏损' },
    { l: '毛收益', v: amt(r.gross, 2, true), s: '扣费前 USDT', t: tone(r.gross) },
    { l: '手续费', v: amt(-Math.abs(r.fees), 2), s: 'USDT', t: r.fees ? 'down' : '' },
    { l: '资金费', v: amt(r.funding, 2, true), s: 'USDT，正 = 收到', t: tone(r.funding) },
    { l: '成交额', v: amt(r.turnover, 0), s: `换手 ${x.deposit ? (r.turnover / x.deposit).toFixed(1) : '—'} 倍` },
    { l: '信号', v: String(total), s: `下单 ${r.signals.ordered} · 拒绝 ${r.signals.rejected} · 跳过 ${r.signals.skipped}` },
  ]
})
const byInst = computed(() => (rep.value?.byInst || []).slice().sort((x, y) => y.net - x.net))

const busy = ref(false)
async function rerun() {
  if (!confirm('用相同配置重新运行回测？当前结果会被覆盖。')) return
  busy.value = true
  try { await tradeApi.runBacktest(aid.value); await load() } catch (e: any) { error.value = e.message } finally { busy.value = false }
}
</script>

<template>
  <div class="page rp">
    <div v-if="error" class="banner">{{ error }}</div>
    <Loading v-if="!a && !error" text="加载回测" />

    <template v-if="a">
      <header class="hd">
        <RouterLink to="/trading" class="back" aria-label="返回账户列表"><ChevronLeft :size="18" /></RouterLink>
        <h1>{{ a.name }}</h1>
        <span class="tag">回测报告</span>
        <span class="tag" :class="running ? 'warn' : faulted.length ? 'bad' : 'good'">{{ running ? '回测中' : faulted.length ? '异常结束' : '已完成' }}</span>
        <InfoTip>
          <b>回测方式</b>：按历史 K 线重放，信号在下一根 K 线开盘价成交（± 半个点差 + 1 跳），同一根 K 线内止损优先于止盈；手续费、资金费（真实结算记录）都计入。回测不等待人工确认。<br />
          <b>注意</b>：回测只说明这组参数在这段历史上的表现，不代表未来；参数调得越多，越容易过拟合。用“实时模拟”在样本外观察一段时间再下结论。
        </InfoTip>
        <span class="acts">
          <RouterLink :to="`/trading/accounts/${a.id}`" class="btn"><ListTree :size="14" />信号与委托</RouterLink>
          <button type="button" :disabled="busy || running" @click="rerun"><RotateCw :size="14" />重新运行</button>
          <RouterLink :to="`/trading/new?from=${a.id}`" class="btn primary"><Radio :size="14" />用同参数开实时账户</RouterLink>
        </span>
      </header>
      <div class="sub-h">
        <span class="tnum">{{ day(a.from) }} → {{ day(a.to ? a.to - 1 : undefined) }}</span>
        <span>· 入金 {{ amt(a.deposit, 0) }} USDT</span>
        <span>· 期末权益 <b class="tnum">{{ amt(a.equity) }}</b></span>
      </div>

      <div v-if="running" class="panel run">
        <Loading :text="progress != null ? `回测中 ${(progress * 100).toFixed(0)}%` : '回测中…'" />
      </div>
      <div v-for="i in faulted" :key="i.id" class="banner">{{ i.name }}：{{ stateLabel[i.state].t }}{{ i.stateReason ? '，' + i.stateReason : '' }}</div>

      <div v-if="metrics.length" class="mg">
        <div v-for="m in metrics" :key="m.l" class="m" :class="{ big: m.big }">
          <span class="l">{{ m.l }}</span>
          <span class="v tnum" :class="m.t">{{ m.v }}</span>
          <span v-if="m.s" class="s">{{ m.s }}</span>
        </div>
      </div>
      <p v-else-if="repErr && !running" class="muted small">报告暂不可用：{{ repErr }}</p>

      <div class="panel">
        <h3>权益与回撤 <InfoTip>上：账户权益（USDT），虚线为入金。下：相对此前最高权益的回撤（%）。</InfoTip></h3>
        <EquityChart v-if="eq.length > 1" :points="eq" :deposit="a.deposit" drawdown :height="320" />
        <p v-else class="muted small">还没有权益数据</p>
      </div>

      <div class="cols">
        <div class="panel">
          <h3>按合约</h3>
          <div class="table-scroll">
            <table v-if="byInst.length" class="t">
              <thead><tr><th>合约</th><th class="num">交易</th><th class="num">胜率</th><th class="num">净收益<small>USDT</small></th><th class="num">手续费</th><th class="num">资金费</th></tr></thead>
              <tbody>
                <tr v-for="r in byInst" :key="r.instId">
                  <td><span class="inst"><CoinIcon :inst-id="r.instId" :size="14" /><b>{{ sym(r.instId) }}</b></span></td>
                  <td class="num">{{ r.trades }}</td>
                  <td class="num">{{ pct(r.winRate, 0) }}</td>
                  <td class="num" :class="tone(r.net)"><b>{{ amt(r.net, 2, true) }}</b></td>
                  <td class="num sub">{{ amt(r.fees) }}</td>
                  <td class="num" :class="tone(r.funding)">{{ amt(r.funding, 2, true) }}</td>
                </tr>
              </tbody>
            </table>
            <p v-else class="muted small">没有交易</p>
          </div>
        </div>
        <div class="panel">
          <h3>策略配置</h3>
          <div v-for="i in insts || []" :key="i.id" class="cfg">
            <div><b>{{ i.name }}</b> <span class="tag">{{ execLabel[i.exec]?.t }}</span> <span class="tag" :class="stateLabel[i.state]?.c">{{ stateLabel[i.state]?.t }}</span></div>
            <div class="muted small">{{ universeText(i.universe, ruleName) }}</div>
            <div class="mono">{{ Object.entries(i.params).map(([k, v]) => `${k}=${v}`).join('  ') }}</div>
          </div>
          <div class="cfg muted small">风控：最多 {{ a.limits.maxPositions }} 仓 · {{ a.limits.maxLeverage }}x · 单笔 {{ pct(a.limits.riskPct) }} · 熔断 {{ pct(a.limits.dailyLossPct, 1) }} · 费率 {{ a.limits.takerFeeBp }}/{{ a.limits.makerFeeBp }} bp</div>
        </div>
      </div>

      <div class="panel">
        <h3>平仓交易 <span class="muted cnt">{{ (fills || []).filter(f => f.realizedPnl !== 0 || f.liquidation).length }}</span></h3>
        <FillsTable :fills="fills" :instances="insts || []" closes />
      </div>
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
.btn.primary { background: var(--accent); border-color: var(--accent); color: var(--on-accent); font-weight: 600; }
button:disabled { opacity: .45; cursor: default; }
.sub-h { display: flex; gap: 6px; flex-wrap: wrap; font-size: var(--fs-sm); color: var(--text-3); margin-top: -4px; }
.sub-h b { color: var(--text-1); }
.run { padding: 0; }

.mg { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); /* 10 metrics: 2 full rows, no empty cells */ gap: 1px; background: var(--line); border: 1px solid var(--line); border-radius: var(--radius); overflow: hidden; }
.m { display: grid; gap: 2px; align-content: start; padding: 10px 14px; background: var(--bg-1); min-width: 0; }
.m .l { font-size: var(--fs-xs); color: var(--text-3); }
.m .v { font-size: var(--fs-xl); font-weight: 600; color: var(--text-1); white-space: nowrap; }
.m.big .v { font-size: var(--fs-2xl); font-weight: 700; }
.m .v.up { color: var(--up); } .m .v.down { color: var(--down); }
.m .s { font-size: var(--fs-xs); color: var(--text-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.cols { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; align-items: start; }
@media (min-width: 1100px) { .cols { grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr); } }
.t td { font-size: var(--fs-sm); font-variant-numeric: tabular-nums; }
.t small { margin-left: 2px; color: var(--text-3); font-size: 10px; }
.inst { display: inline-flex; align-items: center; gap: 6px; }
.sub { color: var(--text-2); }
.cfg { display: grid; gap: 3px; padding: 8px 0; border-top: 1px solid var(--line); }
.cfg:first-of-type { border-top: none; padding-top: 0; }
.mono { font: var(--fs-xs) ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-3); overflow-wrap: anywhere; }
.cnt { font-weight: 400; font-size: var(--fs-sm); }
.small { font-size: var(--fs-sm); }
@media (max-width: 900px) {
  .acts { margin-left: 0; width: 100%; }
  .mg { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .m.big .v { font-size: var(--fs-xl); }
}
</style>

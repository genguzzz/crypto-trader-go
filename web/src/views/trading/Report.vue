<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronLeft, Radio, RotateCw, ListTree, Play, Square } from 'lucide-vue-next'
import { tradeApi, type AccountDetail, type Instance, type Report } from '../../tradeApi'
import { useTradeLive } from '../../composables/tradeLive'
import { store } from '../../store'
import TradeExplorer from '../../components/trade/TradeExplorer.vue'
import InstContribution from '../../components/trade/InstContribution.vue'
import Loading from '../../components/ui/Loading.vue'
import { amt, day, execLabel, pct, stateLabel, tone, universeText } from '../../components/trade/meta'
import { time } from '../../fmt'
import Meter from '../../components/ui/Meter.vue'

const props = defineProps<{ id: string }>()
const aid = computed(() => Number(props.id))
const d = ref<AccountDetail | null>(null)
const rep = ref<Report | null>(null)
const insts = ref<Instance[] | null>(null)
const eq = ref<[number, number][]>([])
const explorer = ref<InstanceType<typeof TradeExplorer> | null>(null)
const reloadKey = ref(0)
const error = ref('')
const repErr = ref('')

async function load() {
  try {
    const [a, i, e] = await Promise.all([tradeApi.account(aid.value), tradeApi.instances(aid.value), tradeApi.equity(aid.value)])
    // trades / candles change only while the backtest runs; refresh them then and once it ends
    if (d.value && (a.account.backtest?.state === 'running' || d.value.account.backtest?.state !== a.account.backtest?.state)) reloadKey.value++
    d.value = a; insts.value = i; eq.value = e
    error.value = ''
  } catch (e: any) {
    error.value = e.message
  }
  try { rep.value = await tradeApi.report(aid.value); repErr.value = '' } catch (e: any) { repErr.value = e.message }
}
useTradeLive(load, { accountId: () => aid.value })

const a = computed(() => d.value?.account)
const bt = computed(() => a.value?.backtest)
const running = computed(() => bt.value?.state === 'running')
const btTag = computed(() => {
  switch (bt.value?.state) {
    case 'running': return { t: `回测中 ${((bt.value.progress || 0) * 100).toFixed(0)}%`, c: 'warn' }
    case 'done': return { t: '已完成', c: 'good' }
    case 'failed': return { t: bt.value.error === '已取消' ? '已取消' : '失败', c: 'bad' }
    default: return { t: '尚未运行', c: '' }
  }
})
const ruleName = (id: string) => store.meta?.rules.find(r => r.id === id)?.name || id
const ratio = (v: number | null | undefined, d = 2) => (v == null || !isFinite(v) ? '—' : v.toFixed(d))

interface M { l: string; v: string; s?: string; t?: string; big?: boolean; tip?: string }
const metrics = computed<M[]>(() => {
  const r = rep.value, x = a.value
  if (!r || !x) return []
  const sg = r.signals || {}
  const total = Object.values(sg).reduce((s, n) => s + n, 0)
  return [
    { l: '净收益', v: pct(r.netPct, 2, true), s: `${amt(r.net, 2, true)} USDT`, t: tone(r.net), big: true },
    { l: '最大回撤', v: pct(-Math.abs(r.maxDrawdown), 2), s: '自权益高点', t: r.maxDrawdown ? 'down' : '', big: true },
    { l: '夏普', v: ratio(r.sharpe), s: r.sharpe == null ? '日收益不足 5 天' : ({ daily: '日收益年化' } as Record<string, string>)[r.sharpeBasis] || r.sharpeBasis || '日收益年化', big: true },
    { l: '胜率', v: r.winRate == null ? '—' : pct(r.winRate, 1), s: `${r.trades} 笔已平${r.openTrades ? ` · ${r.openTrades} 笔持仓中` : ''}`, big: true },
    { l: '盈亏比', v: ratio(r.profitFactor), s: r.profitFactor == null && r.trades ? '没有亏损交易' : '总盈利 ÷ 总亏损' },
    { l: '已实现', v: amt(r.realized, 2, true), s: `未实现 ${amt(r.unrealized, 2, true)}${r.liquidations ? ` · 强平 ${r.liquidations}` : ''}`, t: tone(r.realized) },
    { l: '手续费', v: amt(-Math.abs(r.fees), 2), s: `毛收益 ${amt(r.gross, 2, true)}`, t: r.fees ? 'down' : '' },
    { l: '资金费', v: amt(r.funding, 2, true), s: 'USDT，正 = 收到', t: tone(r.funding) },
    { l: '成交额', v: amt(r.turnover, 0), s: `换手 ${r.deposit ? (r.turnover / r.deposit).toFixed(1) : '—'} 倍` },
    { l: '信号', v: String(total), s: `下单 ${sg.ordered ?? 0} · 拒绝 ${sg.rejected ?? 0} · 跳过 ${sg.skipped ?? 0}` },
  ]
})
const contrib = computed(() => (rep.value?.byInst || []).map(r => ({ instId: r.instId, net: r.pnl, trades: r.trades, wins: r.wins, fees: r.fees, turnover: r.turnover })))

const busy = ref(false)
async function run() {
  if (bt.value?.state !== 'idle' && bt.value && !confirm('用相同配置重新运行回测？当前结果会被清空重算。')) return
  busy.value = true
  try { await tradeApi.runBacktest(aid.value); await load() } catch (e: any) { error.value = e.message } finally { busy.value = false }
}
async function cancel() {
  busy.value = true
  try { await tradeApi.cancelBacktest(aid.value); await load() } catch (e: any) { error.value = e.message } finally { busy.value = false }
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
        <span class="tag" :class="btTag.c">{{ btTag.t }}</span>
        <InfoTip>
          <b>回测方式</b>：按历史 K 线重放，信号在下一根 K 线开盘价成交（± 半个点差 + 1 跳），同一根 K 线内止损优先于止盈；手续费、资金费（真实结算记录）都计入。回测不等待人工确认。<br />
          <b>指标</b>：净收益 = 期末权益 − 入金（含未平仓的未实现盈亏）；毛收益 = 净收益 + 手续费 − 资金费。一笔交易 = 同一策略在同一合约从开仓到完全平仓，盈亏 = 已实现 − 手续费；胜率、盈亏比按交易计算，没有交易时为“—”。夏普 = 每日（UTC+8）权益收益的均值 ÷ 标准差 × √365，不足 5 个交易日不计算。<br />
          <b>注意</b>：回测只说明这组参数在这段历史上的表现，不代表未来；参数调得越多，越容易过拟合。用“实时模拟”在样本外观察一段时间再下结论。
        </InfoTip>
        <span class="acts">
          <RouterLink :to="`/trading/accounts/${a.id}`" class="btn"><ListTree :size="14" />信号与委托</RouterLink>
          <button v-if="running" type="button" :disabled="busy" @click="cancel"><Square :size="12" />取消</button>
          <button v-else type="button" :disabled="busy" @click="run"><template v-if="bt?.state === 'idle' || !bt"><Play :size="14" />开始回测</template><template v-else><RotateCw :size="14" />重新运行</template></button>
          <RouterLink :to="`/trading/new?from=${a.id}`" class="btn primary"><Radio :size="14" />用同参数开实时账户</RouterLink>
        </span>
      </header>
      <div class="sub-h">
        <span class="tnum">{{ day(a.from) }} → {{ day(a.to ? a.to - 1 : undefined) }}</span>
        <span>· 入金 {{ amt(a.deposit, 0) }} USDT</span>
        <span>· 期末权益 <b class="tnum">{{ amt(a.equity) }}</b></span>
      </div>

      <div v-if="running" class="panel run">
        <div class="rl"><span>回测中 <b class="tnum">{{ ((bt?.progress || 0) * 100).toFixed(0) }}%</b></span><span class="muted tnum">已重放到 {{ bt?.clock ? time(bt.clock, true) : '—' }}</span></div>
        <Meter :value="bt?.progress || 0" :max="1" tone="ok" />
      </div>
      <div v-else-if="bt?.state === 'failed'" class="banner">回测{{ bt.error === '已取消' ? '已取消' : '失败' }}{{ bt.error && bt.error !== '已取消' ? '：' + bt.error : '' }}</div>
      <div v-else-if="bt?.state === 'idle'" class="panel run"><span class="muted">尚未运行。点“开始回测”按当前配置重放 {{ day(a.from) }} → {{ day(a.to ? a.to - 1 : undefined) }}。</span></div>

      <div v-if="metrics.length" class="mg">
        <div v-for="m in metrics" :key="m.l" class="m" :class="{ big: m.big }">
          <span class="l">{{ m.l }}</span>
          <span class="v tnum" :class="m.t">{{ m.v }}</span>
          <span v-if="m.s" class="s">{{ m.s }}</span>
        </div>
      </div>
      <p v-else-if="repErr && !running" class="muted small">报告暂不可用：{{ repErr }}</p>

      <div class="panel">
        <TradeExplorer v-if="eq.length > 1" ref="explorer" :account-id="a.id" :points="eq" :deposit="a.deposit" :reload-key="reloadKey" />
        <p v-else class="muted small">还没有权益数据</p>
      </div>

      <div class="cols">
        <div class="panel">
          <h3>各合约贡献 <InfoTip>每个合约的盈亏 = 已实现 − 手续费 + 资金费（USDT），条形长度按绝对值比例；胜率 = 盈利交易 ÷ 交易数。点一行在币对视角中查看该合约的全部交易。</InfoTip></h3>
          <InstContribution :rows="contrib" @open="id => explorer?.openInst(id)" />
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
.run { display: grid; gap: 8px; }
.rl { display: flex; justify-content: space-between; gap: 8px; flex-wrap: wrap; font-size: var(--fs-sm); }

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

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Check, X, BrainCircuit, Settings2 } from 'lucide-vue-next'
import { aiApi, tradeApi, type Account, type AiStatus, type DecisionRecord, type Proposal } from '../../tradeApi'
import { useTradeLive } from '../../composables/tradeLive'
import { time, price, ago } from '../../fmt'
import CoinIcon from '../../components/CoinIcon.vue'
import Loading from '../../components/ui/Loading.vue'
import { actionLabel, outcomeLabel, sym } from '../../components/trade/meta'

const decisions = ref<DecisionRecord[] | null>(null)
const proposals = ref<Proposal[] | null>(null)
const status = ref<AiStatus | null>(null)
const accounts = ref<Account[]>([])
const acc = ref<number>(0)
const error = ref('')

async function load() {
  try {
    const [d, p] = await Promise.all([aiApi.decisions(acc.value || undefined, 200), aiApi.proposals()])
    decisions.value = d
    proposals.value = p
    error.value = ''
  } catch (e: any) {
    error.value = e.message
  }
  aiApi.status().then(s => (status.value = s)).catch(() => {})
}
useTradeLive(load, { pollMs: 15_000 })
tradeApi.accounts().then(x => (accounts.value = x)).catch(() => {})
watch(acc, load)

const accOpts = computed(() => [{ value: 0, label: '全部账户' }, ...accounts.value.map(a => ({ value: a.id, label: a.name, desc: a.mode === 'backtest' ? '回测' : '实时' }))])
const accName = (id: number) => accounts.value.find(a => a.id === id)?.name || `#${id}`
const openProps = computed(() => (proposals.value || []).filter(p => p.state === 'open'))
const pastProps = computed(() => (proposals.value || []).filter(p => p.state !== 'open').slice(0, 10))
const stats = computed(() => {
  const xs = decisions.value || []
  const exec = xs.filter(d => d.action === 'execute').length
  const ordered = xs.filter(d => d.action === 'execute' && d.outcome === 'ordered').length
  return { n: xs.length, exec, skip: xs.length - exec, ordered, cost: xs.reduce((s, d) => s + (d.costUsd || 0), 0), lat: xs.length ? xs.reduce((s, d) => s + d.latencyMs, 0) / xs.length : 0 }
})
const shown = ref(30)
const aiState = computed(() => {
  const s = status.value
  if (!s) return null
  if (!s.configured) return { t: '未授权', c: '' }
  if (s.lastError) return { t: '失效', c: 'bad' }
  return { t: s.workerOnline ? '正常' : 'AI 进程离线', c: s.workerOnline ? 'good' : 'warn' }
})

const busy = ref<number | null>(null)
async function decide(p: Proposal, approve: boolean) {
  if (approve && !confirm(`批准后「${p.instanceName || '#' + p.instanceId}」生成新的配置版本并立即使用新参数。继续？`)) return
  busy.value = p.id
  try { await aiApi.decide(p.id, approve); await load() } catch (e: any) { error.value = e.message } finally { busy.value = null }
}
const changes = (p: Proposal) => Object.entries(p.proposed).map(([k, v]) => ({ k, from: p.current?.[k], to: v }))
</script>

<template>
  <div class="page ai">
    <div class="ph">
      <h1>AI 决策</h1>
      <InfoTip>
        执行方式为“AI 审核”的策略，每个信号都会先交给 AI：AI 看到信号、账户与持仓、行情摘要和该策略近期的决策结果，回答<b>执行</b>或<b>跳过</b>，可以缩小仓位、收紧止损止盈。<br />
        AI 的“执行”之后仍要经过账户风控，所以“引擎结果”可能是“风控拒绝”。AI 不能放大仓位、不能绕过风控、不能直接改参数。<br />
        <b>参数建议</b>：AI 认为参数需要调整时会提出建议，只有你批准后才生效（生成新的配置版本）。
      </InfoTip>
      <span v-if="aiState" class="tag" :class="aiState.c">{{ aiState.t }}</span>
      <span class="sp"></span>
      <USelect v-model="acc" :options="accOpts" size="sm" width="180px" aria-label="账户" />
      <RouterLink to="/settings#ai" class="lnk"><Settings2 :size="14" />AI 授权</RouterLink>
    </div>
    <div v-if="error" class="banner">{{ error }}</div>

    <div class="kp">
      <div><span class="l">决策</span><b class="tnum">{{ stats.n }}</b><span class="s">执行 {{ stats.exec }} · 跳过 {{ stats.skip }}</span></div>
      <div><span class="l">执行后成交</span><b class="tnum">{{ stats.ordered }}</b><span class="s">其余被风控拒绝或过期</span></div>
      <div><span class="l">今日费用</span><b class="tnum">${{ (status?.costToday ?? 0).toFixed(2) }}</b><span class="s">列表合计 ${{ stats.cost.toFixed(2) }}</span></div>
      <div><span class="l">平均耗时</span><b class="tnum">{{ stats.lat ? (stats.lat / 1000).toFixed(1) + 's' : '—' }}</b><span class="s">{{ status?.lastCallAt ? `最近 ${ago(status.lastCallAt)}前` : '尚未调用' }}</span></div>
    </div>

    <div class="cols">
      <div class="panel">
        <h3>决策记录</h3>
        <Loading v-if="!decisions && !error" text="加载决策" />
        <div v-else-if="decisions && !decisions.length" class="empty"><BrainCircuit :size="20" class="muted" /><span class="muted">还没有 AI 决策</span></div>
        <ul class="ds">
          <li v-for="d in (decisions || []).slice(0, shown)" :key="d.id" class="d">
            <div class="r1">
              <CoinIcon :inst-id="d.instId" :size="15" /><b>{{ sym(d.instId) }}</b>
              <span class="act" :class="actionLabel[d.signalAction]?.tone">{{ actionLabel[d.signalAction]?.t || d.signalAction }}</span>
              <span class="tag" :class="d.action === 'execute' ? 'good' : ''">AI {{ d.action === 'execute' ? '执行' : '跳过' }}</span>
              <span v-if="d.action === 'execute' && d.outcome" class="tag" :class="outcomeLabel[d.outcome]?.c" :title="d.outcomeReason">引擎：{{ outcomeLabel[d.outcome]?.t || d.outcome }}</span>
              <span class="tm tnum">{{ time(d.ts, true) }}</span>
            </div>
            <div class="r2">
              <span>{{ accName(d.accountId) }}</span>
              <span>置信 {{ (d.confidence * 100).toFixed(0) }}%</span>
              <span v-if="d.action === 'execute'">仓位 ×{{ d.sizeScale.toFixed(2) }}</span>
              <span v-if="d.stopLoss">止损 {{ price(d.stopLoss) }}</span>
              <span v-if="d.takeProfit">止盈 {{ price(d.takeProfit) }}</span>
              <span class="cost">{{ d.model }} · ${{ d.costUsd.toFixed(3) }} · {{ (d.latencyMs / 1000).toFixed(1) }}s</span>
            </div>
            <p class="why">{{ d.rationale }}</p>
            <div v-if="d.outcomeReason && d.outcome !== 'ordered'" class="eng">{{ d.outcomeReason }}</div>
          </li>
        </ul>
        <button v-if="(decisions || []).length > shown" type="button" class="more" @click="shown += 50">显示更多</button>
      </div>

      <div class="panel">
        <h3>参数建议 <span v-if="openProps.length" class="tag warn">{{ openProps.length }} 待处理</span></h3>
        <p v-if="proposals && !proposals.length" class="muted small">还没有参数建议</p>
        <div v-for="p in openProps" :key="p.id" class="prop">
          <div class="r1"><b>{{ p.instanceName || `实例 #${p.instanceId}` }}</b><span class="muted small">{{ accName(p.accountId) }}</span><span class="tm tnum">{{ time(p.ts, true) }}</span></div>
          <table class="ch">
            <tbody><tr v-for="c in changes(p)" :key="c.k"><td><code>{{ c.k }}</code></td><td class="num sub">{{ c.from ?? '—' }}</td><td class="ar">→</td><td class="num"><b>{{ c.to }}</b></td></tr></tbody>
          </table>
          <p class="why">{{ p.rationale }}</p>
          <div class="pa">
            <button type="button" class="ghost" :disabled="busy === p.id" @click="decide(p, false)"><X :size="14" />拒绝</button>
            <button type="button" class="primary" :disabled="busy === p.id" @click="decide(p, true)"><Check :size="14" />批准</button>
          </div>
        </div>
        <template v-if="pastProps.length">
          <div class="past-h">已处理</div>
          <div v-for="p in pastProps" :key="p.id" class="past">
            <span class="tag" :class="p.state === 'approved' ? 'good' : ''">{{ p.state === 'approved' ? '已批准' : '已拒绝' }}</span>
            <span class="pn">{{ p.instanceName || `#${p.instanceId}` }}</span>
            <span class="muted mono">{{ changes(p).map(c => `${c.k}→${c.to}`).join(' ') }}</span>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ph { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.ph h1 { margin: 0; font-size: var(--fs-2xl); font-weight: 600; }
.sp { flex: 1; }
.lnk { display: inline-flex; align-items: center; gap: 4px; font-size: var(--fs-sm); color: var(--text-2); }
.banner { border-radius: var(--radius-sm); }
.kp { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 150px), 1fr)); gap: 10px; }
.kp > div { display: grid; gap: 1px; padding: 10px 14px; background: var(--bg-1); border: 1px solid var(--line); border-radius: var(--radius); min-width: 0; }
.kp .l { font-size: var(--fs-xs); color: var(--text-3); }
.kp b { font-size: var(--fs-xl); }
.kp .s { font-size: var(--fs-xs); color: var(--text-2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.cols { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; align-items: start; }
@media (min-width: 1100px) { .cols { grid-template-columns: minmax(0, 1fr) 400px; } }
.empty { display: flex; align-items: center; justify-content: center; gap: 8px; padding: 24px 0; }
.ds { list-style: none; margin: 0; padding: 0; }
.d { display: grid; gap: 4px; padding: 10px 0; border-top: 1px solid var(--line); min-width: 0; }
.d:first-child { border-top: none; padding-top: 0; }
.r1 { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; min-width: 0; }
.act { font-weight: 600; font-size: var(--fs-sm); }
.act.up { color: var(--up); } .act.down { color: var(--down); }
.tm { margin-left: auto; font-size: var(--fs-xs); color: var(--text-3); }
.r2 { display: flex; flex-wrap: wrap; gap: 2px 10px; font-size: var(--fs-xs); color: var(--text-2); }
.cost { color: var(--text-3); }
.why { margin: 0; font-size: var(--fs-sm); color: var(--text-1); line-height: 1.6; overflow-wrap: anywhere; }
.eng { font-size: var(--fs-xs); color: var(--down); }
.more { display: block; margin: 8px auto 0; background: transparent; color: var(--text-2); }
.prop { display: grid; gap: 8px; padding: 12px; margin-bottom: 10px; border: 1px solid var(--line-2); border-radius: 10px; }
.ch { width: auto; }
.ch td { padding: 2px 8px 2px 0; border: none; font-size: var(--fs-sm); }
.ch .ar { color: var(--text-3); }
.sub { color: var(--text-2); }
code { font: var(--fs-sm) ui-monospace, "SF Mono", Menlo, monospace; }
.pa { display: flex; justify-content: flex-end; gap: 8px; }
.pa button { display: inline-flex; align-items: center; gap: 4px; height: 30px; padding: 0 14px; }
button.ghost { background: transparent; color: var(--text-2); }
button:disabled { opacity: .45; cursor: default; }
.past-h { margin: 10px 0 4px; font-size: var(--fs-xs); color: var(--text-3); font-weight: 600; }
.past { display: flex; align-items: center; gap: 8px; padding: 5px 0; font-size: var(--fs-sm); min-width: 0; }
.past .pn { flex: none; }
.mono { font: var(--fs-xs) ui-monospace, "SF Mono", Menlo, monospace; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.small { font-size: var(--fs-sm); }
</style>

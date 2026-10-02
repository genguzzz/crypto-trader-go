<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Check, X, BrainCircuit, Settings2, FileSearch, Sparkles, TriangleAlert } from 'lucide-vue-next'
import { aiApi, tradeApi, type Account, type AiStatus, type DecisionRecord, type MemoryDoc, type Proposal, type Signal } from '../../tradeApi'
import { useTradeLive } from '../../composables/tradeLive'
import { time, price, ago } from '../../fmt'
import CoinIcon from '../../components/CoinIcon.vue'
import Loading from '../../components/ui/Loading.vue'
import Modal from '../../components/Modal.vue'
import { actionLabel, aiState, outcomeLabel, sym } from '../../components/trade/meta'

const decisions = ref<DecisionRecord[] | null>(null)
const proposals = ref<Proposal[] | null>(null)
const status = ref<AiStatus | null>(null)
const accounts = ref<Account[]>([])
const acc = ref<number>(0)
const kind = ref<'review' | ''>('review')
const error = ref('')
// engine verdict per decision: the signal that carried aiDecisionId
const verdict = ref<Record<number, Pick<Signal, 'outcome' | 'outcomeReason'>>>({})

async function load() {
  try {
    const [d, p] = await Promise.all([aiApi.decisions({ accountId: acc.value, kind: kind.value || undefined, limit: 200 }), aiApi.proposals(undefined, 50)])
    decisions.value = d
    proposals.value = p
    error.value = ''
    const ids = [...new Set(d.filter(x => x.accountId && x.signalId).map(x => x.accountId))]
    const lists = await Promise.all(ids.map(id => tradeApi.signals(id, 500).catch(() => [] as Signal[])))
    const m: Record<number, Pick<Signal, 'outcome' | 'outcomeReason'>> = {}
    for (const s of lists.flat()) if (s.aiDecisionId) m[s.aiDecisionId] = { outcome: s.outcome, outcomeReason: s.outcomeReason }
    verdict.value = m
  } catch (e: any) {
    error.value = e.message
  }
  aiApi.status().then(s => (status.value = s)).catch(() => {})
}
useTradeLive(load, { pollMs: 15_000 })
tradeApi.accounts().then(x => (accounts.value = x)).catch(() => {})
watch([acc, kind], load)

const accOpts = computed(() => [{ value: 0, label: '全部账户' }, ...accounts.value.map(a => ({ value: a.id, label: a.name, desc: a.mode === 'backtest' ? '回测' : '实时' }))])
const accName = (id: number) => accounts.value.find(a => a.id === id)?.name || (id ? `账户 #${id}` : '—')
const pending = computed(() => (proposals.value || []).filter(p => p.state === 'pending'))
const past = computed(() => (proposals.value || []).filter(p => p.state !== 'pending').slice(0, 10))
const st = computed(() => (status.value ? aiState(status.value) : null))
const stats = computed(() => {
  const xs = (decisions.value || []).filter(d => d.kind === 'review')
  const exec = xs.filter(d => d.action === 'execute').length
  const lat = xs.filter(d => d.latencyMs > 0)
  return { n: xs.length, exec, skip: xs.filter(d => d.action === 'skip').length, err: xs.filter(d => d.error).length, lat: lat.length ? lat.reduce((s, d) => s + d.latencyMs, 0) / lat.length : 0 }
})
const kindLabel: Record<string, string> = { review: '审核', test: '测试', reflect: '提炼经验' }
const act = (a: string) => actionLabel[a as keyof typeof actionLabel]
const oc = (o?: string) => (o ? outcomeLabel[o as keyof typeof outcomeLabel] : undefined)
const shown = ref(30)

// ---- detail (full request / context pack) ----
const detail = ref<DecisionRecord | null>(null)
const detailOpen = ref(false)
const detailErr = ref('')
async function openDetail(d: DecisionRecord) {
  detailOpen.value = true
  detail.value = null
  detailErr.value = ''
  try { detail.value = await aiApi.decision(d.id) } catch (e: any) { detailErr.value = e.message }
}
const pretty = (x: unknown) => JSON.stringify(x, null, 2)

// ---- proposals ----
const busy = ref<number | null>(null)
async function decide(p: Proposal, approve: boolean) {
  if (approve && !confirm(`批准后实例 #${p.instanceId}（${p.strategy}）生成新的配置版本并立即使用新参数。继续？`)) return
  busy.value = p.id
  try { await aiApi.decide(p.id, approve); await load() } catch (e: any) { error.value = e.message } finally { busy.value = null }
}
const changes = (p: Proposal) => Object.entries(p.params || {}).map(([k, v]) => ({ k, from: p.current?.[k], to: v }))

// ---- memory (strategy experience document) ----
const strategies = ref<string[]>([])
const memStrat = ref('')
const mem = ref<MemoryDoc | null>(null)
const memState = ref<'' | 'loading' | 'none' | 'reflect'>('')
const memErr = ref('')
tradeApi.strategies().then(x => { strategies.value = x.map(e => e.name); if (!memStrat.value) memStrat.value = strategies.value[0] || '' }).catch(() => {})
async function loadMem() {
  if (!memStrat.value) return
  memState.value = 'loading'
  memErr.value = ''
  try {
    const r = await aiApi.memory(memStrat.value)
    mem.value = r.current
    memState.value = r.current ? '' : 'none'
  } catch (e: any) {
    mem.value = null
    memState.value = 'none'
    if (!/not found/i.test(e.message)) memErr.value = e.message
  }
}
watch(memStrat, loadMem, { immediate: true })
async function reflect() {
  if (!confirm(`让 AI 读取「${memStrat.value}」最近的决策与结果，提炼一份新的经验文档？会产生一次模型调用费用。`)) return
  memState.value = 'reflect'
  memErr.value = ''
  try { mem.value = await aiApi.reflect(memStrat.value); memState.value = '' } catch (e: any) { memErr.value = e.message; memState.value = mem.value ? '' : 'none' }
}
</script>

<template>
  <div class="page ai">
    <div class="ph">
      <h1>AI 决策</h1>
      <InfoTip>
        执行方式为“AI 审核”的策略，每个信号都会先交给 AI：AI 看到信号、账户与持仓、行情摘要、该策略近期的决策结果和经验文档，回答<b>执行</b>或<b>跳过</b>，可以缩小仓位、调整止损止盈。<br />
        AI 的“执行”之后仍要经过账户风控，所以“引擎”结果可能是“风控拒绝”。AI 不能放大仓位、不能绕过风控、不能直接改参数。<br />
        <b>参数建议</b>：AI 认为参数需要调整时提出建议，只有你批准后才生效（生成新的配置版本）。<br />
        <b>经验文档</b>：定期让 AI 读取过去的决策和平仓结果，提炼成一份简短文档，之后的审核会带上它。<br />
        AI 只经代理访问 Anthropic；代理不可用时 AI 审核暂停（信号按跳过处理），<b>不会直连</b>。
      </InfoTip>
      <span v-if="st" class="tag" :class="st.c">{{ st.t }}</span>
      <span class="sp"></span>
      <USelect v-model="acc" :options="accOpts" size="sm" width="180px" aria-label="账户" />
      <RouterLink to="/settings#ai" class="lnk"><Settings2 :size="14" />AI 授权</RouterLink>
    </div>
    <div v-if="error" class="banner">{{ error }}</div>
    <div v-if="st && st.c !== 'good'" class="alert" :class="st.c">
      <TriangleAlert :size="15" /><span><b>{{ st.t }}</b>：{{ st.d }}</span>
      <RouterLink v-if="!status?.configured" to="/settings#ai">去授权 →</RouterLink>
    </div>

    <div class="kp">
      <div><span class="l">今日调用</span><b class="tnum">{{ status?.callsToday ?? '—' }}</b><span class="s">{{ status?.lastCallAt ? `最近 ${ago(status.lastCallAt)}前` : '尚未调用' }}</span></div>
      <div><span class="l">今日费用</span><b class="tnum">${{ (status?.costToday ?? 0).toFixed(2) }}</b><span class="s">{{ status?.kind === 'oauth_token' ? '订阅 Token：按 API 价格估算' : 'USD，UTC+8 当日' }}</span></div>
      <div><span class="l">审核（列表内）</span><b class="tnum">{{ stats.n }}</b><span class="s">执行 {{ stats.exec }} · 跳过 {{ stats.skip }}<template v-if="stats.err"> · 出错 {{ stats.err }}</template></span></div>
      <div><span class="l">平均耗时</span><b class="tnum">{{ stats.lat ? (stats.lat / 1000).toFixed(1) + 's' : '—' }}</b><span class="s">模型 {{ status?.model || '—' }}</span></div>
    </div>

    <div class="cols">
      <div class="panel">
        <h3 class="h3f">决策记录 <span class="sp"></span>
          <Segmented v-model="kind" size="sm" aria-label="类型" :options="[{ value: 'review', label: '审核' }, { value: '', label: '全部' }]" />
        </h3>
        <Loading v-if="!decisions && !error" text="加载决策" />
        <div v-else-if="decisions && !decisions.length" class="empty"><BrainCircuit :size="20" class="muted" /><span class="muted">还没有 AI {{ kind ? '审核' : '调用' }}记录</span></div>
        <ul class="ds">
          <li v-for="d in (decisions || []).slice(0, shown)" :key="d.id" class="d">
            <div class="r1">
              <template v-if="d.instId"><CoinIcon :inst-id="d.instId" :size="15" /><b>{{ sym(d.instId) }}</b></template>
              <span v-if="d.signalAction" class="act" :class="act(d.signalAction)?.tone">{{ act(d.signalAction)?.t || d.signalAction }}</span>
              <span v-if="d.kind !== 'review'" class="tag">{{ kindLabel[d.kind] || d.kind }}</span>
              <span v-if="d.error" class="tag bad">出错</span>
              <span v-else-if="d.kind === 'review'" class="tag" :class="d.action === 'execute' ? 'good' : ''">AI {{ d.action === 'execute' ? '执行' : d.action === 'skip' ? '跳过' : d.action || '—' }}</span>
              <span v-if="d.action === 'execute' && verdict[d.id] && oc(verdict[d.id].outcome)" class="tag" :class="oc(verdict[d.id].outcome)!.c" :title="verdict[d.id].outcomeReason">引擎：{{ oc(verdict[d.id].outcome)!.t }}</span>
              <span class="tm tnum">{{ time(d.ts, true) }}</span>
            </div>
            <div class="r2">
              <span v-if="d.accountId">{{ accName(d.accountId) }}</span>
              <span v-if="d.strategy">{{ d.strategy }}</span>
              <span v-if="d.decision?.confidence != null">置信 {{ (d.decision.confidence * 100).toFixed(0) }}%</span>
              <span v-if="d.action === 'execute' && d.decision?.sizeScale">仓位 ×{{ d.decision.sizeScale.toFixed(2) }}</span>
              <span v-if="d.decision?.stopLoss">止损 {{ price(d.decision.stopLoss) }}</span>
              <span v-if="d.decision?.takeProfit">止盈 {{ price(d.decision.takeProfit) }}</span>
              <span class="cost">{{ d.model || '—' }}<template v-if="d.promptVersion"> · 提示词 {{ d.promptVersion }}</template> · ${{ d.costUsd.toFixed(3) }} · {{ (d.latencyMs / 1000).toFixed(1) }}s</span>
              <button type="button" class="link" @click="openDetail(d)"><FileSearch :size="12" />详情</button>
            </div>
            <p v-if="d.decision?.rationale" class="why">{{ d.decision.rationale }}</p>
            <div v-if="d.error" class="err">{{ d.error }}</div>
            <div v-if="d.action === 'execute' && verdict[d.id]?.outcomeReason && verdict[d.id].outcome !== 'ordered'" class="eng">{{ verdict[d.id].outcomeReason }}</div>
            <ul v-if="d.outcomes?.length" class="outs"><li v-for="o in d.outcomes" :key="o.ts"><span class="muted tnum">{{ time(o.ts, true) }}</span> {{ o.result }}</li></ul>
          </li>
        </ul>
        <button v-if="(decisions || []).length > shown" type="button" class="more" @click="shown += 50">显示更多</button>
      </div>

      <div class="side">
        <div class="panel">
          <h3>参数建议 <span v-if="pending.length" class="tag warn">{{ pending.length }} 待处理</span></h3>
          <p v-if="proposals && !proposals.length" class="muted small">还没有参数建议</p>
          <div v-for="p in pending" :key="p.id" class="prop">
            <div class="r1"><b>{{ p.strategy }}</b><span class="muted small">实例 #{{ p.instanceId }} · {{ accName(p.accountId) }}</span><span class="tm tnum">{{ time(p.ts, true) }}</span></div>
            <table class="ch">
              <tbody><tr v-for="c in changes(p)" :key="c.k"><td><code>{{ c.k }}</code></td><td class="num sub">{{ c.from ?? '—' }}</td><td class="ar">→</td><td class="num"><b>{{ c.to }}</b></td></tr></tbody>
            </table>
            <p class="why">{{ p.rationale }}</p>
            <div class="pa">
              <button type="button" class="ghost" :disabled="busy === p.id" @click="decide(p, false)"><X :size="14" />拒绝</button>
              <button type="button" class="primary" :disabled="busy === p.id" @click="decide(p, true)"><Check :size="14" />批准</button>
            </div>
          </div>
          <template v-if="past.length">
            <div class="past-h">已处理</div>
            <div v-for="p in past" :key="p.id" class="past" :title="p.note || p.rationale">
              <span class="tag" :class="p.state === 'approved' ? 'good' : ''">{{ p.state === 'approved' ? '已批准' : '已拒绝' }}</span>
              <span class="pn">{{ p.strategy }}</span>
              <span class="muted mono">{{ changes(p).map(c => `${c.k}→${c.to}`).join(' ') }}</span>
            </div>
          </template>
        </div>

        <div class="panel">
          <h3 class="h3f">经验文档 <InfoTip>AI 从某个策略过去的审核决策和平仓结果中提炼的经验，之后审核这个策略的信号时会一并参考。每次提炼生成一个新版本，旧版本保留。</InfoTip>
            <span class="sp"></span>
            <USelect v-if="strategies.length" v-model="memStrat" :options="strategies.map(s => ({ value: s, label: s }))" size="sm" width="150px" aria-label="策略" />
          </h3>
          <Loading v-if="memState === 'loading' || memState === 'reflect'" :text="memState === 'reflect' ? 'AI 提炼中…' : '加载'" />
          <template v-else-if="mem">
            <div class="mh muted small">v{{ mem.version }} · {{ time(mem.ts, true) }} · 读取 {{ mem.decisions }} 条决策<template v-if="mem.model"> · {{ mem.model }}</template></div>
            <div class="doc">{{ mem.doc }}</div>
          </template>
          <p v-else-if="memState === 'none'" class="muted small">这个策略还没有经验文档</p>
          <div v-if="memErr" class="down small">{{ memErr }}</div>
          <div class="pa"><button type="button" :disabled="!memStrat || memState === 'reflect' || !status?.available" :title="status?.available ? '' : 'AI 当前不可用'" @click="reflect"><Sparkles :size="14" />重新提炼</button></div>
        </div>
      </div>
    </div>

    <Modal :open="detailOpen" :title="detail ? `决策 #${detail.id}` : '决策详情'" wide @close="detailOpen = false">
      <p v-if="detailErr" class="down small">{{ detailErr }}</p>
      <Loading v-else-if="!detail" text="读取" />
      <template v-else>
        <div class="muted small dm">{{ time(detail.ts, true) }} · {{ kindLabel[detail.kind] || detail.kind }} · {{ detail.model }} · 提示词 {{ detail.promptVersion || '—' }} · 经验 v{{ detail.memoryVersion }} · 请求 sha256 {{ detail.requestSha256.slice(0, 12) }}</div>
        <div class="sh">模型回答</div>
        <pre class="code">{{ pretty(detail.decision) }}</pre>
        <template v-if="detail.request">
          <div class="sh">发给模型的上下文</div>
          <pre class="code">{{ pretty(detail.request) }}</pre>
        </template>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.ph { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.ph h1 { margin: 0; font-size: var(--fs-2xl); font-weight: 600; }
.sp { flex: 1; }
.lnk { display: inline-flex; align-items: center; gap: 4px; font-size: var(--fs-sm); color: var(--text-2); }
.banner { border-radius: var(--radius-sm); }
.alert { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 10px 14px; border-radius: var(--radius); font-size: var(--fs-sm); background: var(--bg-2); color: var(--text-2); }
.alert.warn { background: var(--warn-bg); color: var(--warn); }
.alert.bad { background: var(--down-bg); color: var(--down); }
.alert span { min-width: 0; overflow-wrap: anywhere; flex: 1; }
.kp { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 150px), 1fr)); gap: 10px; }
.kp > div { display: grid; gap: 1px; padding: 10px 14px; background: var(--bg-1); border: 1px solid var(--line); border-radius: var(--radius); min-width: 0; }
.kp .l { font-size: var(--fs-xs); color: var(--text-3); }
.kp b { font-size: var(--fs-xl); }
.kp .s { font-size: var(--fs-xs); color: var(--text-2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.cols { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; align-items: start; }
.side { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; min-width: 0; }
@media (min-width: 1100px) { .cols { grid-template-columns: minmax(0, 1fr) 400px; } }
.h3f { display: flex; align-items: center; gap: 4px; }
.empty { display: flex; align-items: center; justify-content: center; gap: 8px; padding: 24px 0; }
.ds { list-style: none; margin: 0; padding: 0; }
.d { display: grid; gap: 4px; padding: 10px 0; border-top: 1px solid var(--line); min-width: 0; }
.d:first-child { border-top: none; padding-top: 0; }
.r1 { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; min-width: 0; }
.act { font-weight: 600; font-size: var(--fs-sm); }
.act.up { color: var(--up); } .act.down { color: var(--down); }
.tm { margin-left: auto; font-size: var(--fs-xs); color: var(--text-3); }
.r2 { display: flex; flex-wrap: wrap; align-items: center; gap: 2px 10px; font-size: var(--fs-xs); color: var(--text-2); }
.cost { color: var(--text-3); }
button.link { display: inline-flex; align-items: center; gap: 3px; border: none; background: none; padding: 0; color: var(--text-2); font-size: var(--fs-xs); }
@media (hover: hover) { button.link:hover { color: var(--accent); } }
.why { margin: 0; font-size: var(--fs-sm); color: var(--text-1); line-height: 1.6; overflow-wrap: anywhere; white-space: pre-wrap; }
.err, .eng { font-size: var(--fs-xs); color: var(--down); overflow-wrap: anywhere; }
.outs { list-style: none; margin: 0; padding: 0; font-size: var(--fs-xs); color: var(--text-2); }
.more { display: block; margin: 8px auto 0; background: transparent; color: var(--text-2); }
.prop { display: grid; gap: 8px; padding: 12px; margin-bottom: 10px; border: 1px solid var(--line-2); border-radius: 10px; }
.ch { width: auto; }
.ch td { padding: 2px 8px 2px 0; border: none; font-size: var(--fs-sm); }
.ch .ar { color: var(--text-3); }
.sub { color: var(--text-2); }
code { font: var(--fs-sm) ui-monospace, "SF Mono", Menlo, monospace; }
.pa { display: flex; justify-content: flex-end; gap: 8px; margin-top: 8px; }
.pa button { display: inline-flex; align-items: center; gap: 4px; height: 30px; padding: 0 14px; }
button.ghost { background: transparent; color: var(--text-2); }
button:disabled { opacity: .45; cursor: default; }
.past-h { margin: 10px 0 4px; font-size: var(--fs-xs); color: var(--text-3); font-weight: 600; }
.past { display: flex; align-items: center; gap: 8px; padding: 5px 0; font-size: var(--fs-sm); min-width: 0; }
.past .pn { flex: none; }
.mono { font: var(--fs-xs) ui-monospace, "SF Mono", Menlo, monospace; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mh { margin-bottom: 6px; }
.doc { font-size: var(--fs-sm); color: var(--text-1); line-height: 1.65; white-space: pre-wrap; overflow-wrap: anywhere; max-height: 420px; overflow: auto; }
.dm { margin-bottom: 10px; overflow-wrap: anywhere; }
.sh { font-size: var(--fs-sm); font-weight: 600; margin: 10px 0 6px; }
.code { margin: 0; padding: 12px 14px; background: var(--bg-0); border: 1px solid var(--line); border-radius: 8px; overflow: auto; max-width: 100%; max-height: 50vh; font: var(--fs-xs)/1.55 ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-2); white-space: pre; }
.small { font-size: var(--fs-sm); }
</style>

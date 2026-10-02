<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, BookOpen, CircleAlert, Copy, FlaskConical, Lock, Play, Rocket, SearchCheck, TriangleAlert, CircleCheck } from 'lucide-vue-next'
import { tradeApi, type CheckResult, type Diagnostic, type ParamSpec, type Source, type StrategyVersion, type TrialResult, type Universe } from '../../tradeApi'
import { store } from '../../store'
import CodeEditor from '../../components/editor/CodeEditor.vue'
import ParamsForm from '../../components/trade/ParamsForm.vue'
import UniversePicker from '../../components/trade/UniversePicker.vue'
import SignalList from '../../components/trade/SignalList.vue'
import RunLog from '../../components/trade/RunLog.vue'
import TradeCandleChart from '../../components/charts/TradeCandleChart.vue'
import Loading from '../../components/ui/Loading.vue'
import Switch from '../../components/ui/Switch.vue'
import { amt, day, directionText, paramError, pct, universeText } from '../../components/trade/meta'
import { exitKindLabel } from '../../components/trade/tradeMeta'

/**
 * Strategy editor. A user strategy (id) is a draft that autosaves and is checked as you type;
 * 发布 stores an immutable version for backtests and instances. A stored version (versionId, e.g. a
 * built-in strategy) opens read-only: it can be trial-run and copied into a new user strategy.
 */
const props = defineProps<{ id?: string; versionId?: string }>()
const router = useRouter()
const readonly = computed(() => !props.id)

const code = ref('')
const source = ref<Source | null>(null)
const version = ref<StrategyVersion | null>(null)
const loadErr = ref('')
const vimOn = ref(localStorage.getItem('ctg.vim') === '1')
watch(vimOn, v => localStorage.setItem('ctg.vim', v ? '1' : '0'))
const editor = ref<InstanceType<typeof CodeEditor>>()

async function load() {
  loadErr.value = ''
  try {
    if (props.id) {
      source.value = await tradeApi.source(+props.id)
      code.value = source.value.code || ''
      saved = code.value
      runCheck()
    } else if (props.versionId) {
      version.value = await tradeApi.strategy(props.versionId)
      code.value = version.value.code || ''
      resetParams(version.value.params, version.value.defaultUniverse)
    }
  } catch (e: any) { loadErr.value = e.message }
}
onMounted(load)
watch(() => [props.id, props.versionId], load)

// ---- autosave + check (user strategies) ----
let saved = ''
const saveState = ref<'saved' | 'saving' | 'dirty' | 'error'>('saved')
const saveErr = ref('')
let saveTimer = 0, checkTimer = 0
watch(code, v => {
  if (readonly.value || v === saved) return
  saveState.value = 'dirty'
  clearTimeout(saveTimer)
  saveTimer = window.setTimeout(save, 1200)
  clearTimeout(checkTimer)
  checkTimer = window.setTimeout(runCheck, 1500)
})
async function save() {
  if (readonly.value || !source.value || code.value === saved) return
  clearTimeout(saveTimer)
  const c = code.value
  saveState.value = 'saving'
  try {
    source.value = await tradeApi.saveSource(source.value.id, c)
    saved = c
    saveState.value = code.value === c ? 'saved' : 'dirty'
    saveErr.value = ''
  } catch (e: any) { saveState.value = 'error'; saveErr.value = e.message }
}
onBeforeUnmount(() => { clearTimeout(saveTimer); clearTimeout(checkTimer); if (code.value !== saved && !readonly.value) save() })

const check = ref<CheckResult | null>(null)
const checking = ref(false)
const checkErr = ref('')
async function runCheck() {
  if (readonly.value || !code.value.trim()) return
  clearTimeout(checkTimer)
  checking.value = true
  try {
    check.value = await tradeApi.check(code.value)
    checkErr.value = ''
    const st = check.value.strategies[0]
    if (st) resetParams(st.params, st.defaultUniverse)
  } catch (e: any) { checkErr.value = e.message } finally { checking.value = false }
}
const diags = computed<Diagnostic[]>(() => check.value?.diagnostics ?? [])
const errors = computed(() => diags.value.filter(d => d.severity === 'error').length)
const declared = computed<StrategyVersion | null>(() => (readonly.value ? version.value : check.value?.strategies[0] ?? null))

// ---- trial run inputs ----
const specs = ref<ParamSpec[]>([])
const params = ref<Record<string, number>>({})
const universe = ref<Universe>({ static: [] })
let universeKey = ''
function resetParams(ps: ParamSpec[], u: Universe) {
  const next: Record<string, number> = {}
  for (const p of ps) {
    const old = params.value[p.name]
    const prev = specs.value.find(x => x.name === p.name)
    next[p.name] = old !== undefined && prev?.type === p.type && !paramError(p, old) ? old : p.default
  }
  specs.value = ps
  params.value = next
  const k = JSON.stringify(u)
  if (k !== universeKey) { universeKey = k; universe.value = JSON.parse(k) } // the code's default changed: follow it
}
const days = ref(14)
const paramIssue = computed(() => specs.value.map(p => paramError(p, params.value[p.name]) && `${p.label || p.name}：${paramError(p, params.value[p.name])}`).find(Boolean) || '')

const tab = ref<'problems' | 'run' | 'result'>(readonly.value ? 'run' : 'problems')
const trial = shallowRef<TrialResult | null>(null)
const running = ref(false)
const runErr = ref('')
async function runTrial() {
  if (running.value) return
  running.value = true
  runErr.value = ''
  tab.value = 'result'
  try {
    if (!readonly.value) await save()
    trial.value = await tradeApi.trial({
      ...(readonly.value ? { versionId: props.versionId } : { sourceId: source.value?.id }),
      params: params.value, universe: universe.value, days: days.value,
    })
    if (trial.value.check) check.value = trial.value.check
    if (trial.value.check?.diagnostics.some(d => d.severity === 'error')) tab.value = 'problems'
  } catch (e: any) { runErr.value = e.message } finally { running.value = false }
}

// ---- publish / copy ----
const publishing = ref(false)
const pubMsg = ref('')
const published = ref<StrategyVersion | null>(null)
async function publish() {
  if (!source.value) return
  publishing.value = true
  pubMsg.value = ''
  try {
    await save()
    const r = await tradeApi.publishSource(source.value.id)
    check.value = r.check
    published.value = r.version ?? null
    source.value = await tradeApi.source(source.value.id)
    pubMsg.value = `已发布 ${r.version?.id.slice(0, 8)}，可以回测或添加到账户`
  } catch (e: any) {
    if (e.body?.check) { check.value = e.body.check; tab.value = 'problems' }
    pubMsg.value = '发布失败：' + e.message
  } finally { publishing.value = false }
}
const copying = ref(false)
async function copyAsNew() {
  if (!props.versionId) return
  copying.value = true
  try {
    const s = await tradeApi.createSource({ fromVersion: props.versionId })
    router.push(`/trading/strategies/edit/${s.id}`)
  } catch (e: any) { loadErr.value = e.message } finally { copying.value = false }
}

const title = computed(() => (readonly.value ? version.value?.name : declared.value?.name || source.value?.name) || '策略')
const status = computed(() => {
  if (readonly.value) return { t: version.value?.meta?.builtin ? '内置 · 只读' : '已发布版本 · 只读', c: '' }
  if (saveState.value === 'saving') return { t: '保存中…', c: '' }
  if (saveState.value === 'error') return { t: '保存失败', c: 'bad' }
  if (saveState.value === 'dirty') return { t: '未保存', c: 'warn' }
  if (!source.value?.published) return { t: '草稿 · 未发布', c: 'warn' }
  return source.value.dirty ? { t: '有未发布的改动', c: 'warn' } : { t: '已发布', c: 'good' }
})
const backtestId = computed(() => (readonly.value ? props.versionId : published.value?.id || (source.value && !source.value.dirty ? source.value.published : '')))
const ruleName = (id: string) => store.meta?.rules.find(r => r.id === id)?.name || id

// ---- trial summary ----
const sum = computed(() => {
  const r = trial.value
  if (!r) return null
  const sig = r.signals
  return {
    signals: sig.length, ordered: sig.filter(s => s.outcome === 'ordered').length, rejected: sig.filter(s => s.outcome === 'rejected').length,
    trades: r.trades.length, closed: r.trades.filter(t => t.closeTs).length, rep: r.report,
  }
})
const chartTrades = computed(() => (trial.value?.trades || []).filter(t => t.instId === trial.value?.candles?.instId))
const resultTab = ref<'signals' | 'log' | 'trades'>('signals')
</script>

<template>
  <div class="page ed">
    <header class="eh">
      <RouterLink to="/trading/strategies" class="back" aria-label="返回策略库"><ArrowLeft :size="16" /></RouterLink>
      <h1>{{ title }}</h1>
      <span class="tag" :class="status.c">{{ status.t }}</span>
      <span v-if="declared" class="muted small">{{ declared.timeframe }} · {{ directionText(declared.meta) }}</span>
      <span class="sp"></span>
      <label class="vim" title="Vim 键位（Esc 普通模式，i 插入，:w 保存）"><Switch v-model="vimOn" aria-label="Vim 模式" />Vim</label>
      <RouterLink to="/help/strategy" target="_blank" class="btn" title="策略文档（新标签页）"><BookOpen :size="14" />文档</RouterLink>
      <template v-if="readonly">
        <button type="button" class="btn" :disabled="copying" @click="copyAsNew"><Copy :size="14" />复制为新策略</button>
      </template>
      <template v-else>
        <button type="button" class="btn" :disabled="checking" @click="runCheck(); tab = 'problems'"><SearchCheck :size="14" />检查</button>
        <button type="button" class="btn primary" :disabled="publishing || errors > 0" :title="errors ? '先修复错误' : '保存为不可变版本，供回测和实例使用'" @click="publish">
          <Rocket :size="14" />发布
        </button>
      </template>
      <button type="button" class="btn run" :disabled="running || !!paramIssue" :title="paramIssue || '用最近的数据快速回测一遍'" @click="runTrial"><Play :size="14" />试运行</button>
    </header>
    <div v-if="loadErr" class="banner">{{ loadErr }}</div>
    <div v-if="readonly && version" class="note"><Lock :size="14" />
      {{ version.meta?.builtin ? '内置策略只读，作为示例和教材：' : '已发布的版本不可修改：' }}点「复制为新策略」得到一份可编辑的副本。代码里的中文注释讲解了每一步。
    </div>
    <div v-if="pubMsg" class="note" :class="{ bad: pubMsg.startsWith('发布失败') }">
      {{ pubMsg }}
      <RouterLink v-if="published" :to="`/trading/new?mode=backtest&strategy=${published.id}`">用此版本回测 →</RouterLink>
    </div>
    <div v-if="saveErr && saveState === 'error'" class="note bad">{{ saveErr }}</div>

    <div class="body">
      <div class="code">
        <Loading v-if="!loadErr && !code && (props.id ? !source : !version)" text="读取代码" />
        <CodeEditor v-else ref="editor" v-model="code" :readonly="readonly" :vim="vimOn" :diagnostics="diags" @save="save(); runCheck()" />
      </div>

      <aside class="side panel">
        <nav class="tabs" role="tablist">
          <button v-if="!readonly" type="button" role="tab" :class="{ on: tab === 'problems' }" @click="tab = 'problems'">
            问题<span v-if="diags.length" class="cnt" :class="{ bad: errors }">{{ diags.length }}</span>
          </button>
          <button type="button" role="tab" :class="{ on: tab === 'run' }" @click="tab = 'run'">参数与试运行</button>
          <button type="button" role="tab" :class="{ on: tab === 'result' }" @click="tab = 'result'">结果<span v-if="trial" class="cnt">{{ trial.signals?.length ?? 0 }}</span></button>
        </nav>

        <section v-if="tab === 'problems'" class="pane">
          <p v-if="checkErr" class="down small">检查失败：{{ checkErr }}</p>
          <p v-else-if="checking && !check" class="muted small">检查中…</p>
          <p v-else-if="check && !diags.length" class="ok small"><CircleCheck :size="14" />没有发现问题{{ declared ? `：策略 ${declared.name}，${declared.params.length} 个参数` : '' }}</p>
          <ul class="dl">
            <li v-for="(d, i) in diags" :key="i" :class="d.severity" @click="editor?.goto(d.line, d.col)">
              <component :is="d.severity === 'error' ? CircleAlert : TriangleAlert" :size="14" />
              <span class="ln tnum">第 {{ d.line }} 行</span><span class="msg">{{ d.message }}</span>
            </li>
          </ul>
          <p class="muted small hint">输入停顿后自动保存并检查：语法、未定义的名字、策略声明（参数范围、周期、品种）。按 Ctrl/⌘+S 立即保存。</p>
        </section>

        <section v-else-if="tab === 'run'" class="pane">
          <div class="row">
            <span class="l">回放天数</span>
            <Segmented v-model="days" size="sm" aria-label="试运行天数" :options="[3, 7, 14, 30].map(d => ({ value: d, label: `${d} 天` }))" />
          </div>
          <div class="row col">
            <span class="l">品种</span>
            <UniversePicker v-if="!readonly" v-model="universe" />
            <span v-else class="muted small">{{ universeText(universe, ruleName) }}（复制后可改）</span>
          </div>
          <h4 class="h">参数</h4>
          <ParamsForm v-model="params" :specs="specs" />
          <p v-if="paramIssue" class="down small">{{ paramIssue }}</p>
          <button type="button" class="btn run wide" :disabled="running || !!paramIssue" @click="runTrial"><Play :size="14" />{{ running ? '运行中…' : '试运行' }}</button>
          <p class="muted small hint">
            试运行 = 在最近 {{ days }} 天的历史数据上跑一次临时回测（初始资金 1 万 USDT、默认风控），最长 90 秒。
            不建账户、不发通知（ctx.notify 只记进日志），结果不保存。
          </p>
        </section>

        <section v-else class="pane">
          <Loading v-if="running" text="试运行中（最长 90 秒）" />
          <p v-else-if="runErr" class="down small">{{ runErr }}</p>
          <p v-else-if="!trial" class="muted small">还没有试运行。到「参数与试运行」里点试运行。</p>
          <template v-else>
            <p v-if="trial.error" class="down small">{{ trial.error }}</p>
            <div v-if="sum" class="kpis">
              <div><span class="l">信号</span><b class="tnum">{{ sum.signals }}</b><span class="s">下单 {{ sum.ordered }} · 拒绝 {{ sum.rejected }}</span></div>
              <div><span class="l">交易</span><b class="tnum">{{ sum.trades }}</b><span class="s">已平 {{ sum.closed }}</span></div>
              <div v-if="sum.rep"><span class="l">净收益</span><b class="tnum" :class="sum.rep.net >= 0 ? 'up' : 'down'">{{ amt(sum.rep.net) }}</b><span class="s">{{ pct(sum.rep.netPct) }}</span></div>
              <div v-if="sum.rep"><span class="l">胜率 / 回撤</span><b class="tnum">{{ sum.rep.winRate == null ? '—' : pct(sum.rep.winRate, 0) }}</b><span class="s">{{ pct(-sum.rep.maxDrawdown) }}</span></div>
            </div>
            <p class="muted small">{{ day(trial.from) }} – {{ day(trial.to) }} · 用时 {{ trial.seconds }} 秒</p>
            <TradeCandleChart v-if="trial.candles?.rows.length" :candles="trial.candles" :trades="chartTrades" :height="260" small />
            <Segmented v-model="resultTab" size="sm" aria-label="结果" :options="[{ value: 'signals', label: `信号 ${trial.signals.length}` }, { value: 'log', label: `日志 ${trial.logs.length}` }, { value: 'trades', label: `交易 ${trial.trades.length}` }]" />
            <SignalList v-if="resultTab === 'signals'" :signals="trial.signals" :limit="30" />
            <RunLog v-else-if="resultTab === 'log'" :lines="trial.logs" empty="策略没有写日志（ctx.log / ctx.notify）" />
            <div v-else class="table-scroll">
              <table v-if="trial.trades.length" class="tt">
                <thead><tr><th>品种</th><th>方向</th><th class="num">净收益</th><th>出场</th></tr></thead>
                <tbody>
                  <tr v-for="t in trial.trades" :key="t.id">
                    <td>{{ t.instId.replace('-USDT-SWAP', '') }}</td><td :class="t.posSide === 'long' ? 'up' : 'down'">{{ t.posSide === 'long' ? '多' : '空' }}</td>
                    <td class="num" :class="t.net >= 0 ? 'up' : 'down'">{{ amt(t.net) }}</td><td>{{ exitKindLabel[t.exitKind]?.t || t.exitKind }}</td>
                  </tr>
                </tbody>
              </table>
              <p v-else class="muted small">没有成交。看看「信号」里的拒绝原因，或「日志」里策略在等什么。</p>
            </div>
            <RouterLink v-if="backtestId" :to="`/trading/new?mode=backtest&strategy=${backtestId}`" class="btn wide"><FlaskConical :size="14" />用更长区间正式回测</RouterLink>
          </template>
        </section>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.ed { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; }
.eh { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.eh h1 { margin: 0; font-size: var(--fs-xl); font-weight: 600; overflow-wrap: anywhere; }
.back { display: inline-flex; padding: 4px; color: var(--text-2); }
.sp { flex: 1; }
.vim { display: inline-flex; align-items: center; gap: 6px; font-size: var(--fs-sm); color: var(--text-2); cursor: pointer; }
.btn { display: inline-flex; align-items: center; gap: 4px; height: 30px; padding: 0 12px; font-size: var(--fs-sm); border-radius: var(--radius-sm); text-decoration: none !important; }
.btn.primary { background: var(--accent); color: var(--on-accent); font-weight: 600; }
.btn.run { border-color: var(--accent); color: var(--accent); }
.btn.wide { justify-content: center; width: 100%; margin-top: 8px; }
.note { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 8px 12px; border-radius: var(--radius-sm); background: var(--bg-2); color: var(--text-2); font-size: var(--fs-sm); }
.note.bad { color: var(--down); }
.body { display: grid; grid-template-columns: minmax(0, 1fr) minmax(320px, 400px); gap: 12px; min-height: 0; }
.code { height: calc(100vh - 210px); min-height: 420px; }
.side { display: flex; flex-direction: column; min-height: 0; max-height: calc(100vh - 210px); padding: 0; overflow: hidden; }
.tabs { display: flex; gap: 2px; padding: 6px; border-bottom: 1px solid var(--line); }
.tabs button { all: unset; display: inline-flex; align-items: center; gap: 6px; height: 26px; padding: 0 10px; border-radius: 6px; font-size: var(--fs-sm); color: var(--text-3); cursor: pointer; }
.tabs button.on { background: var(--bg-3); color: var(--text-1); }
.cnt { min-width: 16px; height: 16px; padding: 0 4px; border-radius: 8px; background: var(--bg-3); color: var(--text-2); font-size: 10px; line-height: 16px; text-align: center; }
.cnt.bad { background: var(--down); color: #fff; }
.pane { padding: 10px 12px 14px; overflow: auto; display: grid; gap: 8px; align-content: start; }
.dl { list-style: none; margin: 0; padding: 0; display: grid; gap: 4px; }
.dl li { display: flex; gap: 6px; align-items: baseline; padding: 6px 8px; border-radius: 6px; background: var(--bg-2); font-size: var(--fs-sm); cursor: pointer; }
.dl li.error svg { color: var(--down); }
.dl li.warning svg { color: var(--warn); }
.dl svg { flex: none; transform: translateY(2px); }
.ln { flex: none; color: var(--text-3); font-size: var(--fs-xs); }
.msg { min-width: 0; overflow-wrap: anywhere; color: var(--text-1); }
.ok { display: flex; align-items: center; gap: 6px; color: var(--up); }
.hint { margin: 4px 0 0; line-height: 1.6; }
.row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.row.col { flex-direction: column; align-items: stretch; gap: 6px; }
.row .l { font-size: var(--fs-xs); color: var(--text-3); }
.h { margin: 6px 0 0; font-size: var(--fs-sm); font-weight: 600; }
.kpis { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 6px; }
.kpis > div { display: grid; padding: 6px 8px; border-radius: 6px; background: var(--bg-2); }
.kpis .l { font-size: var(--fs-xs); color: var(--text-3); }
.kpis b { font-size: var(--fs-lg); }
.kpis .s { font-size: var(--fs-xs); color: var(--text-3); }
.tt td, .tt th { font-size: var(--fs-sm); }
.small { font-size: var(--fs-sm); }
@media (max-width: 1000px) {
  .body { grid-template-columns: minmax(0, 1fr); }
  .code { height: 60vh; min-height: 320px; }
  .side { max-height: none; }
}
</style>

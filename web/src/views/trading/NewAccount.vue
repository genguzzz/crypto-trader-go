<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router'
import { RadioGroupRoot, RadioGroupItem } from 'reka-ui'
import { Radio, History, Landmark, Plus, Trash2, ChevronDown, ChevronLeft, ChevronRight, Check } from 'lucide-vue-next'
import { tradeApi, aiApi, defaultLimits, type ExecMode, type Limits, type Mode, type StrategyEntry, type StrategyVersion, type Universe } from '../../tradeApi'
import { store } from '../../store'
import LimitsForm from '../../components/trade/LimitsForm.vue'
import InstanceForm from '../../components/trade/InstanceForm.vue'
import StrategyPicker from '../../components/trade/StrategyPicker.vue'
import SettingRow from '../../components/settings/SettingRow.vue'
import NumInput from '../../components/ui/NumInput.vue'
import Modal from '../../components/Modal.vue'
import Loading from '../../components/ui/Loading.vue'
import { amt, confirmLabel, day, execLabel, limitErrors, modeLabel, paramError, parseDay, pct, universeText } from '../../components/trade/meta'

const route = useRoute()
const router = useRouter()

// ---- wizard state ----
const step = ref(1)
const steps = ['模式', '资金与风控', '策略', '确认']
const DAY = 86_400_000
const today = day(Date.now())
const form = reactive({
  name: '',
  mode: 'live' as Mode,
  from: day(Date.now() - 90 * DAY),
  to: day(Date.now() - DAY),
  deposit: 10_000,
  limits: defaultLimits() as Limits,
})
interface Draft { key: number; version: StrategyVersion; params: Record<string, number>; universe: Universe; exec: ExecMode; open: boolean }
const drafts = ref<Draft[]>([])
let seq = 0

const strategies = ref<StrategyEntry[] | null>(null)
const stratErr = ref('')
const aiReady = ref<boolean | undefined>(undefined)
const loadingFrom = ref(false)

function addDraft(v: StrategyVersion, over?: Partial<Draft>) {
  const params = Object.fromEntries((v.params || []).map(p => [p.name, p.default]))
  const universe: Universe = JSON.parse(JSON.stringify(v.defaultUniverse?.screener || v.defaultUniverse?.static?.length ? v.defaultUniverse : { static: [] }))
  drafts.value.forEach(d => (d.open = false))
  drafts.value.push({ key: ++seq, version: v, params, universe, exec: 'auto', open: true, ...over })
}
const pickOpen = ref(false)
async function pick(v: StrategyVersion) {
  pickOpen.value = false
  try { addDraft(v.params ? v : await tradeApi.strategy(v.id)) } catch (e: any) { stratErr.value = e.message }
}

onMounted(async () => {
  tradeApi.strategies().then(x => (strategies.value = x)).catch(e => (stratErr.value = e.message))
  aiApi.status().then(s => (aiReady.value = s.configured && !s.lastError)).catch(() => {})
  // ?mode=backtest&strategy=<versionId>: from the strategy library
  if (route.query.mode === 'backtest') setMode('backtest') // also switches the default name
  const sv = route.query.strategy
  if (typeof sv === 'string' && sv) { try { addDraft(await tradeApi.strategy(sv)) } catch (e: any) { stratErr.value = e.message } }
  // ?from=<accountId>: same deposit, limits and strategies as an existing (backtest) account, as a live account
  const from = Number(route.query.from)
  if (from) {
    loadingFrom.value = true
    try {
      const [d, insts] = await Promise.all([tradeApi.account(from), tradeApi.instances(from)])
      form.mode = 'live'
      form.deposit = d.account.deposit
      form.limits = { ...defaultLimits(), ...d.account.limits }
      form.name = d.account.name.replace(/回测/g, '').trim() + ' · 实时'
      for (const i of insts) {
        try {
          const v = await tradeApi.strategy(i.versionId)
          addDraft(v, { params: { ...i.params }, universe: JSON.parse(JSON.stringify(i.universe)), exec: i.exec, open: false })
        } catch { /* version gone: skip */ }
      }
    } catch (e: any) {
      stratErr.value = '读取原账户失败：' + e.message
    } finally {
      loadingFrom.value = false
    }
  }
})

// ---- validation per step ----
const range = computed(() => ({ from: parseDay(form.from), to: parseDay(form.to) + DAY }))
const err1 = computed(() => {
  const e: Record<string, string> = {}
  if (!form.name.trim()) e.name = '请填写账户名称'
  if (form.mode === 'backtest') {
    const { from, to } = range.value
    if (!isFinite(from) || !isFinite(to)) e.range = '请选择日期区间'
    else if (from >= to) e.range = '开始日期必须早于结束日期'
    else if (to > Date.now() + DAY) e.range = '结束日期不能晚于今天'
    else if (to - from > 366 * DAY) e.range = '区间不能超过一年'
  }
  return e
})
const err2 = computed(() => {
  const e = limitErrors(form.limits)
  if (!(form.deposit >= 100 && form.deposit <= 1e9)) e.deposit = '请输入 100–10 亿 USDT'
  return e
})
function draftError(d: Draft) {
  if (d.universe.static && !d.universe.static.length && !d.universe.screener) return '请至少选择一个合约'
  if (d.universe.screener && !(d.universe.screener.top >= 1)) return '取前 N 至少为 1'
  for (const p of d.version.params || []) if (paramError(p, d.params[p.name])) return `参数 ${p.name}：${paramError(p, d.params[p.name])}`
  return ''
}
const err3 = computed(() => {
  if (form.mode === 'backtest' && !drafts.value.length) return '回测至少需要一个策略'
  const bad = drafts.value.find(draftError)
  return bad ? `${bad.version.name}：${draftError(bad)}` : ''
})
const stepOk = (n: number) => (n === 1 ? !Object.keys(err1.value).length : n === 2 ? !Object.keys(err2.value).length : n === 3 ? !err3.value : true)
const touched = ref<Record<number, boolean>>({})
function next() {
  touched.value[step.value] = true
  if (!stepOk(step.value)) return
  if (step.value === 1 && !form.name.trim()) return
  step.value = Math.min(4, step.value + 1)
  document.querySelector('#app > main')?.scrollTo({ top: 0 })
  window.scrollTo({ top: 0 })
}
function go(n: number) { if (n < step.value || [1, 2, 3].slice(0, n - 1).every(stepOk)) step.value = n }
const defaultName = () => (form.mode === 'backtest' ? `回测 ${form.from.slice(5)} → ${form.to.slice(5)}` : `实时账户 ${today.slice(5)}`)
function setMode(m: string) {
  const auto = !form.name || form.name === defaultName()
  form.mode = m as Mode
  if (auto) form.name = defaultName()
}
if (!form.name) form.name = defaultName()
const rangePreset = computed({
  get: () => { const n = Math.round((range.value.to - range.value.from) / DAY); return form.to === day(Date.now() - DAY) && [30, 90, 180, 365].includes(n) ? n : 0 },
  set: (n: number) => { if (n) { const auto = form.name === defaultName(); form.to = day(Date.now() - DAY); form.from = day(Date.now() - n * DAY); if (auto) form.name = defaultName() } },
})

// ---- create ----
const creating = ref(false)
const progress = ref('')
const createErr = ref('')
let created = false
async function create() {
  if (![1, 2, 3].every(stepOk)) return
  creating.value = true
  createErr.value = ''
  try {
    progress.value = '创建账户…'
    const a = await tradeApi.createAccount({
      name: form.name.trim(), mode: form.mode, deposit: form.deposit, limits: form.limits,
      ...(form.mode === 'backtest' ? { from: range.value.from, to: range.value.to } : {}),
    })
    created = true
    for (const [i, d] of drafts.value.entries()) {
      progress.value = `挂载策略 ${i + 1}/${drafts.value.length}…`
      const inst = await tradeApi.createInstance(a.id, { versionId: d.version.id, params: d.params, universe: d.universe, exec: d.exec })
      if (form.mode === 'live') await tradeApi.instanceAction(inst.id, 'start')
    }
    if (form.mode === 'backtest') {
      progress.value = '开始回测…'
      await tradeApi.runBacktest(a.id)
      router.push(`/trading/backtests/${a.id}`)
    } else {
      router.push(`/trading/accounts/${a.id}`)
    }
  } catch (e: any) {
    createErr.value = (created ? '账户已创建，但后续步骤失败：' : '创建失败：') + e.message
  } finally {
    creating.value = false
    progress.value = ''
  }
}
onBeforeRouteLeave(() => created || step.value === 1 || window.confirm('放弃正在创建的账户？'))

const ruleName = (id: string) => store.meta?.rules.find(r => r.id === id)?.name || id
const modes = [
  { value: 'live', icon: Radio, t: '实时模拟', d: '用实时行情持续运行，模拟成交、手续费、滑点和资金费' },
  { value: 'backtest', icon: History, t: '历史回测', d: '在选定区间按历史 K 线快速重放，结束后出报告' },
  { value: 'real', icon: Landmark, t: '实盘', d: '向 OKX 真实下单，需要单独授权', disabled: true },
]
</script>

<template>
  <div class="page wz">
    <div class="ph">
      <h1>新建交易账户</h1>
      <InfoTip>
        先建账户（入金与风控规则），再把策略挂到账户上。多个策略共享同一账户的资金和风控。<br />
        所有账户都是<b>模拟盘</b>：不会向 OKX 下任何真实订单。<br />
        创建后可以随时追加策略、修改风控、入金或归档；修改策略参数会生成新的配置版本，旧信号保留原配置。
      </InfoTip>
    </div>

    <ol class="steps" aria-label="步骤">
      <li v-for="(s, i) in steps" :key="s" :class="{ on: step === i + 1, done: step > i + 1 }">
        <button type="button" :disabled="creating" @click="go(i + 1)">
          <span class="n"><Check v-if="step > i + 1" :size="12" /><template v-else>{{ i + 1 }}</template></span><span class="t">{{ s }}</span>
        </button>
      </li>
    </ol>

    <Loading v-if="loadingFrom" text="读取原账户配置" />

    <!-- ① mode -->
    <section v-show="step === 1" class="panel">
      <h3>账户模式</h3>
      <RadioGroupRoot :model-value="form.mode" class="modes" aria-label="账户模式" @update:model-value="v => setMode(String(v))">
        <RadioGroupItem v-for="m in modes" :key="m.value" :value="m.value" :disabled="m.disabled" class="mode">
          <component :is="m.icon" :size="18" class="mi" />
          <span class="mt">{{ m.t }}<span v-if="m.disabled" class="tag">后续开放</span></span>
          <span class="md">{{ m.d }}</span>
        </RadioGroupItem>
      </RadioGroupRoot>
      <SettingRow label="账户名称" for-id="w-name" :error="touched[1] ? err1.name : ''">
        <input id="w-name" v-model="form.name" class="txt" maxlength="40" placeholder="如：主流趋势" />
      </SettingRow>
      <SettingRow v-if="form.mode === 'backtest'" label="回测区间" desc="UTC+8 自然日，含首尾两天" :error="err1.range">
        <div class="line">
          <Segmented v-model="rangePreset" size="sm" aria-label="区间预设" :options="[{ value: 30, label: '30 天' }, { value: 90, label: '90 天' }, { value: 180, label: '180 天' }, { value: 365, label: '1 年' }]" />
          <span class="dates"><input v-model="form.from" type="date" :max="today" aria-label="开始日期" /><span class="muted">→</span><input v-model="form.to" type="date" :max="today" aria-label="结束日期" /></span>
        </div>
      </SettingRow>
    </section>

    <!-- ② funds & limits -->
    <section v-show="step === 2" class="panel">
      <h3>资金与风控 <InfoTip>入金是模拟的初始资金（USDT）。实时账户之后可以继续入金；回测账户的入金在区间开始时一次到账。风控规则对账户下的所有策略生效。</InfoTip></h3>
      <SettingRow label="初始入金" desc="模拟资金，USDT" for-id="w-dep" :error="err2.deposit">
        <div class="line">
          <Segmented :model-value="form.deposit" size="sm" aria-label="入金预设" :options="[1000, 10000, 100000].map(v => ({ value: v, label: amt(v, 0) }))" @update:model-value="v => (form.deposit = v)" />
          <NumInput id="w-dep" v-model="form.deposit" unit="USDT" :min="100" :step="100" width="96px" />
        </div>
      </SettingRow>
      <LimitsForm v-model="form.limits" />
    </section>

    <!-- ③ strategies -->
    <section v-show="step === 3" class="panel">
      <div class="h3row">
        <h3>挂载策略 <InfoTip>
          每个策略实例 = 策略代码的一个版本 + 你的参数 + 交易标的 + 执行方式。同一策略可以用不同参数挂多次。<br />
          实时账户创建后立即启动这些策略；也可以先不挂，之后在账户页追加。回测账户至少需要一个策略。
        </InfoTip></h3>
        <button type="button" class="primary add" @click="pickOpen = true"><Plus :size="14" />添加策略</button>
      </div>
      <p v-if="!drafts.length" class="muted small">{{ form.mode === 'backtest' ? '回测至少需要一个策略。' : '还没有挂载策略，可以稍后在账户页添加。' }}</p>
      <div v-for="(d, i) in drafts" :key="d.key" class="draft" :class="{ bad: touched[3] && draftError(d) }">
        <div class="dh">
          <button type="button" class="tg" :aria-expanded="d.open" @click="d.open = !d.open">
            <ChevronDown :size="15" class="chev" :class="{ open: d.open }" />
            <b>{{ d.version.name }}</b><span class="tag">{{ d.version.timeframe }}</span><span class="tag">{{ execLabel[d.exec].t }}</span>
            <span class="muted us">{{ universeText(d.universe, ruleName) }}</span>
          </button>
          <button type="button" class="icon" :aria-label="`移除 ${d.version.name}`" @click="drafts.splice(i, 1)"><Trash2 :size="14" /></button>
        </div>
        <div v-if="touched[3] && draftError(d)" class="down small">{{ draftError(d) }}</div>
        <InstanceForm v-if="d.open" v-model:params="d.params" v-model:universe="d.universe" v-model:exec="d.exec" :version="d.version" :ai-ready="aiReady" class="dbody" />
      </div>
      <div v-if="stratErr" class="down small">{{ stratErr }}</div>
    </section>

    <!-- ④ confirm -->
    <section v-show="step === 4" class="panel">
      <h3>确认</h3>
      <dl class="sum">
        <div><dt>名称</dt><dd>{{ form.name }}</dd></div>
        <div><dt>模式</dt><dd><span class="tag" :class="modeLabel[form.mode].c">{{ modeLabel[form.mode].t }}</span><template v-if="form.mode === 'backtest'"> {{ form.from }} → {{ form.to }}</template></dd></div>
        <div><dt>初始入金</dt><dd class="tnum">{{ amt(form.deposit, 0) }} <small>USDT</small></dd></div>
        <div><dt>持仓 / 杠杆</dt><dd class="tnum">最多 {{ form.limits.maxPositions }} 个 · {{ form.limits.maxLeverage }}x</dd></div>
        <div><dt>单笔风险</dt><dd class="tnum">{{ pct(form.limits.riskPct) }} · 单合约 ≤ {{ pct(form.limits.maxInstPct, 0) }}</dd></div>
        <div><dt>日亏损熔断</dt><dd class="tnum">{{ pct(form.limits.dailyLossPct, 1) }}</dd></div>
        <div><dt>成本</dt><dd class="tnum">滑点 {{ form.limits.slippageBp }} bp · 费率 {{ form.limits.takerFeeBp }}/{{ form.limits.makerFeeBp }} bp</dd></div>
        <div><dt>止损 / 冷却</dt><dd>{{ form.limits.requireStop ? '必须带止损' : '不强制止损' }} · {{ form.limits.cooldownMin }} 分钟</dd></div>
        <div><dt>人工确认</dt><dd>{{ confirmLabel[form.limits.confirm] }}<template v-if="form.limits.confirm !== 'off'"> · {{ form.limits.confirmTimeoutS }} 秒后{{ form.limits.confirmDefault === 'execute' ? '执行' : '跳过' }}</template></dd></div>
      </dl>
      <div class="sh">策略 <span class="muted">{{ drafts.length }}</span></div>
      <ul class="dl">
        <li v-for="d in drafts" :key="d.key">
          <b>{{ d.version.name }}</b> <span class="tag">{{ d.version.timeframe }}</span> <span class="tag">{{ execLabel[d.exec].t }}</span>
          <div class="muted small">{{ universeText(d.universe, ruleName) }}</div>
          <div v-if="d.version.params?.length" class="muted small mono">{{ Object.entries(d.params).map(([k, v]) => `${k}=${v}`).join('  ') }}</div>
        </li>
        <li v-if="!drafts.length" class="muted small">不挂载策略（稍后在账户页添加）</li>
      </ul>
      <p class="note">{{ form.mode === 'backtest' ? '创建后立即开始回测，完成后自动生成报告。' : '创建后立即启动上面的策略。' }}</p>
      <div v-if="createErr" class="down small">{{ createErr }}</div>
    </section>

    <div class="bar">
      <span class="st">
        <template v-if="progress"><span class="dot warn"></span>{{ progress }}</template>
        <template v-else-if="touched[step] && step === 1 && Object.keys(err1).length"><span class="down">{{ Object.values(err1)[0] }}</span></template>
        <template v-else-if="touched[step] && step === 2 && Object.keys(err2).length"><span class="down">请先修正标红的项</span></template>
        <template v-else-if="touched[step] && step === 3 && err3"><span class="down">{{ err3 }}</span></template>
        <template v-else><span class="muted">第 {{ step }} / 4 步</span></template>
      </span>
      <span class="acts">
        <button v-if="step > 1" type="button" :disabled="creating" @click="step--"><ChevronLeft :size="14" />上一步</button>
        <button v-if="step < 4" type="button" class="primary" @click="next">下一步<ChevronRight :size="14" /></button>
        <button v-else type="button" class="primary" :disabled="creating" @click="create">{{ creating ? '创建中…' : form.mode === 'backtest' ? '创建并开始回测' : '创建账户' }}</button>
      </span>
    </div>

    <Modal :open="pickOpen" title="添加策略" wide @close="pickOpen = false">
      <StrategyPicker :entries="strategies" :error="stratErr" @pick="pick" />
    </Modal>
  </div>
</template>

<style scoped>
.wz { max-width: 880px !important; margin: 0 auto; }
.ph { display: flex; align-items: center; gap: 6px; }
.ph h1 { margin: 0; font-size: var(--fs-2xl); font-weight: 600; }

.steps { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 6px; margin: 0; padding: 0; list-style: none; }
.steps button { all: unset; box-sizing: border-box; width: 100%; display: flex; align-items: center; gap: 8px; padding: 8px 10px; border-radius: 8px; border-top: 2px solid var(--line-2); cursor: pointer; color: var(--text-3); font-size: var(--fs-sm); min-width: 0; }
.steps .n { flex: none; display: inline-flex; align-items: center; justify-content: center; width: 20px; height: 20px; border-radius: 50%; background: var(--bg-3); font-size: var(--fs-xs); font-weight: 700; color: var(--text-2); }
.steps .t { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.steps .on button { border-top-color: var(--accent); color: var(--text-1); font-weight: 600; }
.steps .on .n { background: var(--accent); color: var(--on-accent); }
.steps .done button { border-top-color: var(--accent-bg); color: var(--text-2); }
.steps .done .n { background: var(--accent-bg); color: var(--accent); }

.modes { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; margin-bottom: 8px; }
.mode { all: unset; box-sizing: border-box; display: grid; gap: 4px; align-content: start; padding: 12px 14px; border: 1px solid var(--line-2); border-radius: 10px; cursor: pointer; min-width: 0; transition: border-color .12s, background .12s; }
@media (hover: hover) { .mode:not([data-disabled]):hover { border-color: var(--text-3); } }
.mode[data-state="checked"] { border-color: var(--accent); background: var(--accent-bg); }
.mode[data-state="checked"] .mi { color: var(--accent); }
.mode[data-disabled] { opacity: .45; cursor: not-allowed; }
.mode:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
.mi { color: var(--text-3); }
.mt { display: flex; align-items: center; gap: 6px; font-weight: 600; color: var(--text-1); }
.md { font-size: var(--fs-xs); color: var(--text-3); line-height: 1.5; }
.txt { width: 240px; max-width: 100%; height: 32px; }
.line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; justify-content: flex-end; }
.dates { display: inline-flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.dates input { height: 30px; color-scheme: dark; }

.h3row { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 12px; }
.h3row h3 { margin: 0; }
.add { display: inline-flex; align-items: center; gap: 4px; height: 28px; }
.draft { border: 1px solid var(--line-2); border-radius: 10px; padding: 8px 12px; margin-bottom: 10px; min-width: 0; }
.draft.bad { border-color: var(--down); }
.dh { display: flex; align-items: center; gap: 8px; min-width: 0; }
.tg { all: unset; flex: 1; min-width: 0; display: flex; align-items: center; gap: 8px; cursor: pointer; padding: 4px 0; flex-wrap: wrap; }
.chev { color: var(--text-3); transition: transform .15s; transform: rotate(-90deg); flex: none; }
.chev.open { transform: none; }
.us { font-size: var(--fs-xs); min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dbody { padding: 12px 0 6px; border-top: 1px solid var(--line); margin-top: 6px; }
button.icon { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; padding: 0; background: transparent; border-color: transparent; color: var(--text-3); flex: none; }
@media (hover: hover) { button.icon:hover { color: var(--text-1); border-color: var(--line-2); } }

.sum { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 240px), 1fr)); gap: 12px 24px; margin: 0 0 16px; }
.sum dt { font-size: var(--fs-xs); color: var(--text-3); }
.sum dd { margin: 2px 0 0; color: var(--text-1); font-weight: 500; }
.sum small { color: var(--text-3); font-size: var(--fs-xs); }
.sh { font-size: var(--fs-sm); font-weight: 600; margin-bottom: 6px; }
.dl { list-style: none; margin: 0; padding: 0; }
.dl li { padding: 8px 0; border-top: 1px solid var(--line); }
.mono { font-family: ui-monospace, "SF Mono", Menlo, monospace; overflow-wrap: anywhere; }
.small { font-size: var(--fs-sm); }

.bar { position: sticky; bottom: 0; z-index: 4; display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; padding: 12px 0; background: var(--bg-0); border-top: 1px solid var(--line); }
.st { display: inline-flex; align-items: center; gap: 8px; font-size: var(--fs-sm); min-width: 0; }
.acts { display: inline-flex; gap: 8px; margin-left: auto; }
.acts button { display: inline-flex; align-items: center; gap: 4px; height: 32px; padding: 0 16px; }
button:disabled { opacity: .45; cursor: default; }

@media (max-width: 900px) {
  .bar { bottom: calc(56px + env(safe-area-inset-bottom)); }
  .line { justify-content: flex-start; }
  .steps .t { font-size: var(--fs-xs); }
  .steps button { padding: 8px 4px; gap: 4px; flex-direction: column; text-align: center; }
}
@media (max-width: 640px) {
  .modes { grid-template-columns: minmax(0, 1fr); }
}
</style>

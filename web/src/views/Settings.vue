<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, useRoute } from 'vue-router'
import { Radar, FlaskConical, Database, Monitor, Undo2, Code2 } from 'lucide-vue-next'
import { api, type DevConfig, type Settings, type Storage } from '../api'
import { store, refreshMeta, followDefaultRule, ruleOptions } from '../store'
import { usd, catLabel, tzOptions, tzSetting, oiHalf, setOiHalf, oiFrac } from '../fmt'
import ChipGroup from '../components/ui/ChipGroup.vue'
import Meter from '../components/ui/Meter.vue'
import Loading from '../components/ui/Loading.vue'
import Modal from '../components/Modal.vue'
import SettingsSection from '../components/settings/SettingsSection.vue'
import SettingRow from '../components/settings/SettingRow.vue'
import { bytes } from '../components/status/storage'
import Switch from '../components/ui/Switch.vue'
import { devTab, setDevTab } from '../composables/devtab'
import InteractSection from '../components/settings/InteractSection.vue'
import AiAuthSection from '../components/settings/AiAuthSection.vue'
import AdminKeySection from '../components/settings/AdminKeySection.vue'

const clone = (x: Settings): Settings => JSON.parse(JSON.stringify(x))
// Render immediately from settings already loaded with /meta; refresh in the background.
const saved = ref<Settings | null>(store.meta?.settings ? clone(store.meta.settings) : null)
const s = ref<Settings | null>(saved.value ? clone(saved.value) : null)
const all = ref<[string, string, number, number][]>([])
const storage = ref<Storage | null>(null)
const msg = ref<{ ok: boolean; text: string } | null>(null)
const saving = ref(false)
const rulesOpen = ref(false)
const route = useRoute()

onMounted(async () => {
  if (!s.value) { saved.value = await api.settings(); s.value = clone(saved.value) }
  api.turnovers().then(x => (all.value = x.rows)) // compact (~10 KB) list for the live estimate
  api.storage().then(x => (storage.value = x)).catch(() => {})
  api.devConfig().then(x => { dev.value = x; globalRate.value = x.globalPerMin }).catch(() => {})
  const id = route.hash.slice(1)
  if (id) nextTick(() => document.getElementById(id)?.scrollIntoView({ block: 'start' }))
})

// ---- fields in display units ----
const thresholdWan = computed({
  get: () => (s.value ? s.value.threshold / 1e4 : 0),
  set: v => { if (s.value) s.value.threshold = Math.round(Number(v) * 1e4) },
})
const capGB = computed({
  get: () => (s.value ? +(s.value.storageCapMB / 1024).toFixed(2) : 0),
  set: v => { if (s.value) s.value.storageCapMB = Math.round(Number(v) * 1024) },
})

// ---- dirty state and validation (limits mirror internal/market/settings.go validate) ----
const sameCats = (a: string[], b: string[]) => [...a].sort().join() === [...b].sort().join()
const changed = computed(() => {
  const a = s.value, b = saved.value
  if (!a || !b) return {} as Record<string, boolean>
  return {
    threshold: a.threshold !== b.threshold, topN: a.topN !== b.topN, categories: !sameCats(a.categories, b.categories),
    defaultRule: a.defaultRule !== b.defaultRule, storageCapMB: a.storageCapMB !== b.storageCapMB,
  } as Record<string, boolean>
})
const nChanged = computed(() => Object.values(changed.value).filter(Boolean).length)
const errors = computed(() => {
  const e: Record<string, string> = {}
  const x = s.value
  if (!x) return e
  const w = thresholdWan.value
  if (!Number.isFinite(w) || w <= 0) e.threshold = '请输入阈值'
  else if (w < 10) e.threshold = '不能低于 10 万'
  else if (w > 1_000_000) e.threshold = '不能超过 100 亿'
  if (!Number.isInteger(x.topN) || x.topN < 1 || x.topN > 500) e.topN = '请输入 1–500 的整数'
  if (!x.categories.length) e.categories = '至少选择一个类别'
  const g = capGB.value
  if (!Number.isFinite(g) || g < 0.5 || g > 100) e.storageCapMB = '请输入 0.5–100 GB'
  return e
})
const valid = computed(() => Object.keys(errors.value).length === 0)
watch(s, () => { if (nChanged.value) msg.value = null }, { deep: true })

// Live estimate from the latest confirmed 12h turnover of every instrument.
const estimate = computed(() => {
  if (!s.value || !all.value.length) return null
  const cats = new Set(s.value.categories)
  const pass = all.value.filter(([, cat, turn, live]) => live === 1 && cats.has(cat) && turn > s.value!.threshold)
  const byCat: Record<string, number> = {}
  for (const [, cat] of pass) byCat[cat] = (byCat[cat] || 0) + 1
  return { n: Math.min(pass.length, s.value.topN), raw: pass.length, byCat }
})
// per-category count above the threshold (ignores the category filter), shown on the chips
const catCount = computed(() => {
  const out: Record<string, number> = {}
  if (!s.value) return out
  for (const [, cat, turn, live] of all.value) if (live === 1 && turn > s.value.threshold) out[cat] = (out[cat] || 0) + 1
  return out
})
const capBytes = computed(() => (s.value?.storageCapMB ?? 0) * (1 << 20))

const ruleName = computed(() => store.meta?.rules.find(r => r.id === s.value?.defaultRule))
const weights = (w: Record<string, number>) =>
  Object.entries(w).map(([k, v]) => `${store.meta?.fields.find(f => f.key === k)?.label || k} ${v > 0 ? '+' : ''}${v}`).join('，') || '—'

// ---- display (this browser only, applies immediately) ----
const tz = ref(tzSetting())
function setTz(v: string) {
  localStorage.setItem('ctg.tz', v)
  location.reload() // every chart and time label re-renders in the new zone
}
// scanner OI bar: the OI (万 USD) that fills half a row; applies live
const oiHalfWan = computed({ get: () => Math.round(oiHalf.value / 1e4), set: (v: number) => setOiHalf(v * 1e4) })
const oiSamples = [5e5, 5e6, 5e7, 5e8, 5e9]

// ---- developer: external API (server, applies immediately) ----
const dev = ref<DevConfig | null>(null)
const globalRate = ref(0)
const devMsg = ref('')
const devErr = computed(() => (!Number.isInteger(globalRate.value) || globalRate.value < 10 || globalRate.value > 60000 ? '请输入 10–60000 的整数' : ''))
async function saveDev(patch: Partial<DevConfig>) {
  if (!dev.value) return
  try {
    dev.value = await api.saveDevConfig({ ...dev.value, ...patch })
    globalRate.value = dev.value.globalPerMin
    devMsg.value = ''
  } catch (e: any) {
    devMsg.value = '保存失败：' + e.message
  }
}
function setGlobalRate(v: number) {
  globalRate.value = v
  if (!devErr.value && dev.value && v !== dev.value.globalPerMin) saveDev({ globalPerMin: v })
}

function revert() { if (saved.value) s.value = clone(saved.value); msg.value = null }
async function save() {
  if (!s.value || !valid.value || !nChanged.value) return
  saving.value = true
  try {
    const poolChanged = changed.value.threshold || changed.value.topN || changed.value.categories
    const r = await api.saveSettings(s.value)
    saved.value = r
    s.value = clone(r)
    followDefaultRule() // saving a default applies it here too; refreshMeta picks it up
    await refreshMeta()
    msg.value = { ok: true, text: poolChanged ? '已保存并生效：活跃池已重算，规则历史回放在后台重跑' : '已保存并生效' }
  } catch (e: any) {
    msg.value = { ok: false, text: '保存失败：' + e.message }
  } finally {
    saving.value = false
  }
}
onBeforeRouteLeave(() => !nChanged.value || window.confirm('有未保存的设置，确定离开？'))
</script>

<template>
  <div v-if="s" class="settings">
    <div class="ph">
      <h1>设置</h1>
    </div>

    <!-- 监控池 -->
    <SettingsSection id="pool" title="监控池" :icon="Radar">
      <template #tip>
        <b>12h 成交额</b> = 连续 720 根已确认 1m K 线的 USDT 成交额（volCcyQuote）之和，每确认一分钟重算一次；缺任何一分钟视为数据缺口，不参与排名。<br />
        <b>入池条件</b>：严格大于阈值，按成交额从高到低取前 N 个；达标不足 N 就显示实际数量，不拿低流动性合约凑数。<br />
        <b>类别</b>：加密 / 股票·ETF / 商品永续（OKX instCategory）。股票类成交集中在美股时段，没有现货和链上数据。<br />
        <b>保存后</b>：立即重算监控池；后台重跑 14 天历史回放，新进入的币会补齐历史数据（进度见“系统”页）。
      </template>
      <SettingRow label="12h 成交额阈值" desc="只监控 12h USDT 成交额严格大于它的合约" for-id="f-th" :changed="changed.threshold" :error="errors.threshold">
        <div class="ctl-line">
          <Segmented :model-value="thresholdWan" size="sm" aria-label="阈值预设" :options="[100, 300, 500, 1000, 2000].map(v => ({ value: v, label: v + '万' }))" @update:model-value="v => (thresholdWan = v)" />
          <span class="unit-in"><input id="f-th" type="number" min="10" step="50" v-model.number="thresholdWan" /><span>万</span></span>
        </div>
      </SettingRow>
      <SettingRow label="上限 N" desc="按成交额取前 N 个，达标不足时不凑数" for-id="f-n" :changed="changed.topN" :error="errors.topN">
        <div class="ctl-line">
          <Segmented v-model="s.topN" size="sm" aria-label="上限预设" :options="[50, 100, 200, 300].map(v => ({ value: v, label: String(v) }))" />
          <span class="unit-in"><input id="f-n" type="number" min="1" max="500" v-model.number="s.topN" /><span>个</span></span>
        </div>
      </SettingRow>
      <SettingRow label="包含类别" desc="数字为当前超过阈值的合约数" :changed="changed.categories" :error="errors.categories">
        <ChipGroup v-model="s.categories" at-least-one aria-label="类别"
          :options="Object.entries(catLabel).map(([c, l]) => ({ value: c, label: l, count: all.length ? catCount[c] || 0 : undefined }))" />
      </SettingRow>
      <template #footer>
        <div class="est">
          <span class="el">预计入池</span>
          <template v-if="estimate">
            <b class="en tnum">{{ estimate.n }}</b><span class="eu">个</span>
            <span class="ed">超过 {{ usd(s.threshold) }} 的共 {{ estimate.raw }} 个<template v-if="estimate.raw > s.topN">，截取前 {{ s.topN }}</template>
              <template v-for="(n, c) in estimate.byCat" :key="c"> · {{ catLabel[c] }} {{ n }}</template></span>
          </template>
          <span v-else class="ed">计算中…</span>
          <InfoTip>按最新确认的 12h 成交额即时估算，保存后以服务端重算结果为准。</InfoTip>
        </div>
      </template>
    </SettingsSection>

    <!-- 评分规则 -->
    <SettingsSection id="rules" title="评分规则" :icon="FlaskConical">
      <template #tip>
        这里选择扫描页和详情页<b>默认使用</b>的评分规则；每个人也可以在扫描页临时切换。<br />
        权重、点差门槛、新建规则在“规则与战绩”页编辑；每次保存都会发布新版本，旧版本的战绩保留，便于比较。<br /><br />
        <ScoringExplainer />
      </template>
      <template #aside>
        <button type="button" class="link" @click="rulesOpen = true">全部规则</button>
        <RouterLink to="/rules">规则与战绩 →</RouterLink>
      </template>
      <SettingRow label="默认评分规则" :changed="changed.defaultRule">
        <template #desc>
          扫描页和详情页默认使用<template v-if="ruleName"> · <span class="sub">v{{ ruleName.version }}</span></template>
        </template>
        <USelect v-model="s.defaultRule" :options="ruleOptions()" width="280px" aria-label="默认规则" />
      </SettingRow>
    </SettingsSection>

    <!-- 数据存储 -->
    <SettingsSection id="storage" title="数据存储" :icon="Database">
      <template #tip>
        SQLite 总大小上限。各类数据（1m K 线、持仓量、订单流…）按比例分配配额并按合约分层保留；某类超出配额时从最低层开始缩短保留期。评分快照（战绩）永不删除。<br />
        各类占用、配额与每层保留期见“系统 → 数据存储”。
      </template>
      <template #aside><RouterLink to="/status#storage">查看存储明细 →</RouterLink></template>
      <SettingRow label="存储上限" desc="超出时从低优先级数据开始淘汰" for-id="f-cap" :changed="changed.storageCapMB" :error="errors.storageCapMB">
        <div class="ctl-line">
          <Segmented :model-value="capGB" size="sm" aria-label="存储上限预设" :options="[1, 2, 5, 10].map(v => ({ value: v, label: v + ' GB' }))" @update:model-value="v => (capGB = v)" />
          <span class="unit-in"><input id="f-cap" type="number" min="0.5" max="100" step="0.5" v-model.number="capGB" /><span>GB</span></span>
        </div>
      </SettingRow>
      <SettingRow label="当前占用">
        <template #desc>
          <template v-if="storage">库文件 {{ bytes(storage.fileBytes) }}，其中可复用 {{ bytes(storage.freeBytes) }}</template>
          <template v-else>服务启动约 2 分钟后首次检查</template>
        </template>
        <div v-if="storage" class="usage">
          <div class="uv"><b class="tnum">{{ bytes(storage.usedBytes) }}</b> <span class="muted">/ {{ bytes(capBytes) }}</span></div>
          <Meter :value="storage.usedBytes" :max="capBytes" />
          <div v-if="changed.storageCapMB && storage.usedBytes > capBytes * 0.9" class="uw">新上限接近或低于当前占用，保存后会立即淘汰旧数据</div>
        </div>
        <span v-else class="muted">—</span>
      </SettingRow>
    </SettingsSection>

    <!-- 显示 -->
    <SettingsSection id="display" title="显示" :icon="Monitor">
      <template #tip>只保存在本浏览器，改完立即生效（切换时区会刷新页面）。<b>时区</b>决定 K 线横轴、十字光标与页面上所有时间的显示；服务端按 UTC 对齐 K 线（4H 在 UTC+8 下为 0/4/8/12/16/20 点），数据本身不变。<br />
        <b>持仓量条</b>：扫描列表每行的底色长条表示该合约持仓量（USDT）。按对数平滑换算：占比 = r / (1 + r)，r = √(持仓量 ÷ 半满点)。持仓量等于半满点时正好半行；10 倍 ≈ 76%，100 倍 ≈ 91%，1000 倍 ≈ 97%；1/10 ≈ 24%，1/100 ≈ 9%。这样 BTC 与中小山寨都能拉开差距，不会像线性那样只剩 BTC 一条。</template>
      <template #aside><span class="tag">仅本浏览器 · 立即生效</span></template>
      <SettingRow label="时区" desc="K 线横轴与页面时间">
        <Segmented :model-value="tz" size="sm" aria-label="时区" :options="tzOptions" @update:model-value="setTz" />
      </SettingRow>
      <SettingRow label="持仓量条半满点" desc="扫描列表底色长条：持仓量等于它时占半行，按对数平滑" for-id="f-oih">
        <div class="ctl-line">
          <Segmented :model-value="oiHalfWan" size="sm" aria-label="半满点预设" :options="[100, 500, 2000, 10000].map(v => ({ value: v, label: v >= 10000 ? v / 10000 + '亿' : v + '万' }))" @update:model-value="v => (oiHalfWan = v)" />
          <span class="unit-in"><input id="f-oih" type="number" min="1" step="100" :value="oiHalfWan" @change="e => (oiHalfWan = Number((e.target as HTMLInputElement).value))" /><span>万</span></span>
        </div>
        <div class="oi-demo">
          <div v-for="v in oiSamples" :key="v" class="oi-b"><span class="oi-f" :style="{ width: oiFrac(v) * 100 + '%' }"></span><span class="tnum">{{ usd(v) }}</span><span class="muted tnum">{{ Math.round(oiFrac(v) * 100) }}%</span></div>
        </div>
      </SettingRow>
    </SettingsSection>

    <!-- 交易：交互通道 / AI 授权 / 管理员密钥 (each saves on its own, outside the save bar) -->
    <InteractSection />
    <AiAuthSection />
    <AdminKeySection />

    <!-- 开发者 -->
    <SettingsSection id="dev" title="开发者" :icon="Code2">
      <template #tip>
        给外部 Agent 的只读数据接口：REST（<code>/api/open/v1</code>）与 MCP 服务（<code>/mcp</code>），用 API Key / Token 认证、按凭证限流。凭证在 Dev 标签里创建和管理。<br />
        <b>全服务器上限</b>：所有凭证加起来每分钟最多调用多少次（令牌桶，突发 = 1/6），用来保护行情记录不被外部查询拖慢；单个凭证的频率在创建时设置。<br />
        开关与上限改完立即生效。“显示 Dev 标签”只影响本浏览器。
      </template>
      <template #aside><RouterLink to="/dev">管理凭证 →</RouterLink></template>
      <SettingRow label="显示 Dev 标签" desc="导航栏里的 Dev 页：凭证、接口文档、试用与调用记录（仅本浏览器）">
        <Switch :model-value="devTab" aria-label="显示 Dev 标签" @update:model-value="setDevTab" />
      </SettingRow>
      <SettingRow label="开放外部 API 与 MCP" desc="关闭后所有凭证的调用返回 503，凭证本身保留">
        <Switch v-if="dev" :model-value="dev.enabled" aria-label="开放外部 API" @update:model-value="v => saveDev({ enabled: v })" />
        <span v-else class="muted">—</span>
      </SettingRow>
      <SettingRow label="全服务器频率上限" desc="所有凭证合计，次/分钟" for-id="f-gr" :error="devErr || devMsg">
        <div v-if="dev" class="ctl-line">
          <Segmented :model-value="globalRate" size="sm" aria-label="全局上限预设" :options="[300, 600, 1200, 3000].map(v => ({ value: v, label: String(v) }))" @update:model-value="setGlobalRate" />
          <span class="unit-in"><input id="f-gr" type="number" min="10" max="60000" step="100" :value="globalRate" @change="e => setGlobalRate(Number((e.target as HTMLInputElement).value))" /><span>次/分</span></span>
        </div>
      </SettingRow>
    </SettingsSection>

    <!-- save bar: sticky while there are unsaved changes or a fresh result -->
    <div class="savebar" :class="{ show: nChanged || msg, dirty: nChanged }" role="region" aria-label="保存">
      <span class="st">
        <template v-if="nChanged"><span class="dot warn"></span>{{ nChanged }} 项未保存的更改<template v-if="!valid"> · <span class="down">请先修正标红的项</span></template></template>
        <template v-else-if="msg"><span class="dot" :class="msg.ok ? 'ok' : 'bad'"></span><span :class="{ down: !msg.ok }">{{ msg.text }}</span>
          <RouterLink v-if="msg.ok" to="/status#run" class="sl">查看进度 →</RouterLink></template>
      </span>
      <span class="acts">
        <button v-if="nChanged" type="button" :disabled="saving" @click="revert"><Undo2 :size="13" /> 撤销</button>
        <button type="button" class="primary" :disabled="saving || !nChanged || !valid" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
      </span>
    </div>

    <Modal :open="rulesOpen" title="全部规则" wide @close="rulesOpen = false">
      <div class="table-scroll"><table class="rt">
        <thead><tr><th>规则</th><th>多头权重</th><th>空头权重</th><th>点差门槛</th><th></th></tr></thead>
        <tbody>
          <tr v-for="r in store.meta?.rules || []" :key="r.id">
            <td><b>{{ r.name }}</b> <span class="muted">v{{ r.version }}</span> <span class="tag info" v-if="r.id === s.defaultRule">默认</span></td>
            <td class="w">{{ weights(r.long) }}</td>
            <td class="w">{{ weights(r.short) }}</td>
            <td>{{ r.gates.maxSpreadBps ? r.gates.maxSpreadBps + 'bps' : '不限' }}</td>
            <td><RouterLink :to="`/rules/${r.id}`" @click="rulesOpen = false">编辑 / 战绩 →</RouterLink></td>
          </tr>
        </tbody>
      </table></div>
    </Modal>
  </div>
  <div v-else class="page"><Loading text="加载设置" /></div>
</template>

<style scoped>
.settings { max-width: 880px; margin: 0 auto; padding: 16px 16px 24px; display: grid; grid-template-columns: minmax(0, 1fr); gap: 14px; }
.ph { display: flex; align-items: baseline; gap: 12px; flex-wrap: wrap; padding: 4px 2px 2px; }
.ph h1 { margin: 0; font-size: var(--fs-2xl); font-weight: 600; }

.ctl-line { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; justify-content: flex-end; }
.unit-in { display: inline-flex; align-items: center; background: var(--bg-3); border: 1px solid var(--line-2); border-radius: var(--radius-sm); }
.unit-in:focus-within { border-color: var(--accent); }
.unit-in input { width: 84px; border: none; background: transparent; text-align: right; padding: 4px 4px 4px 10px; font-variant-numeric: tabular-nums; }
.unit-in input::-webkit-outer-spin-button, .unit-in input::-webkit-inner-spin-button { -webkit-appearance: none; margin: 0; }
.unit-in input[type=number] { -moz-appearance: textfield; appearance: textfield; }
.unit-in span { padding: 0 10px 0 2px; color: var(--text-3); font-size: var(--fs-sm); }
:deep(.invalid) .unit-in { border-color: var(--down); }
.oi-demo { display: grid; gap: 3px; width: 260px; max-width: 100%; }
.oi-b { position: relative; display: flex; justify-content: space-between; gap: 8px; padding: 2px 8px; font-size: var(--fs-xs); background: var(--bg-0); border-radius: 4px; overflow: hidden; }
.oi-b > span:not(.oi-f) { position: relative; }
.oi-f { position: absolute; inset: 0 auto 0 0; background: var(--oi-bar); }

.est { display: flex; align-items: baseline; flex-wrap: wrap; gap: 4px 8px; }
.el { font-size: var(--fs-sm); color: var(--text-3); }
.en { font-size: var(--fs-2xl); font-weight: 700; color: var(--text-1); line-height: 1; }
.eu { font-size: var(--fs-sm); color: var(--text-3); margin-right: 6px; }
.ed { font-size: var(--fs-sm); color: var(--text-2); }

button.link { border: none; background: none; padding: 0; color: var(--text-2); font-size: var(--fs-sm); }
@media (hover: hover) { button.link:hover { color: var(--text-1); } }

.usage { display: grid; gap: 6px; width: 260px; max-width: 100%; }
.uv { font-size: var(--fs-sm); text-align: right; }
.uv b { font-size: var(--fs-lg); }
.uw { font-size: var(--fs-xs); color: var(--warn); }

.savebar {
  position: sticky; bottom: 0; z-index: 4; display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap;
  margin: 0 -16px; padding: 12px 16px; background: var(--bg-0); border-top: 1px solid var(--line);
}
.savebar:not(.show) { display: none; }
.savebar.dirty { border-top-color: var(--line-2); }
.st { display: inline-flex; align-items: center; gap: 8px; font-size: var(--fs-sm); color: var(--text-2); min-width: 0; }
.sl { margin-left: 4px; }
.acts { display: inline-flex; gap: 8px; margin-left: auto; }
.acts button { display: inline-flex; align-items: center; gap: 4px; height: 32px; padding: 0 16px; }
button:disabled { opacity: .45; cursor: default; }

.rt td { font-size: var(--fs-sm); }
.rt .w { white-space: normal; min-width: 180px; color: var(--text-2); }

@media (max-width: 900px) {
  .savebar { bottom: calc(56px + env(safe-area-inset-bottom)); padding: 10px 16px; }
  .ctl-line { justify-content: flex-start; }
  .uv { text-align: left; }
}
</style>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, type Settings, type Row } from '../api'
import { store, refreshMeta, setRule, ruleOptions } from '../store'
import { usd, catLabel } from '../fmt'

const s = ref<Settings | null>(null)
const all = ref<Row[]>([])
const msg = ref('')
const saving = ref(false)
const thresholdWan = computed({
  get: () => (s.value ? s.value.threshold / 1e4 : 0),
  set: v => { if (s.value) s.value.threshold = Math.round(Number(v) * 1e4) },
})

onMounted(async () => {
  s.value = await api.settings()
  const snap = await api.universe('all')
  all.value = snap.rows || []
})

// Live estimate from the latest confirmed 12h turnover of every instrument.
const estimate = computed(() => {
  if (!s.value) return null
  const cats = new Set(s.value.categories)
  const pass = all.value.filter(r => r.status === 'live' && cats.has(r.category) && r.turnover12h > s.value!.threshold)
  const byCat: Record<string, number> = {}
  for (const r of pass) byCat[r.category] = (byCat[r.category] || 0) + 1
  return { n: Math.min(pass.length, s.value.topN), raw: pass.length, byCat }
})
function toggleCat(c: string) {
  if (!s.value) return
  const i = s.value.categories.indexOf(c)
  if (i >= 0) s.value.categories.splice(i, 1)
  else s.value.categories.push(c)
}
async function save() {
  if (!s.value) return
  saving.value = true
  try {
    s.value = await api.saveSettings(s.value)
    setRule(s.value.defaultRule)
    await refreshMeta()
    msg.value = '已保存并生效：活跃池已按新条件重算，规则历史回放在后台重跑（见系统页）。'
  } catch (e: any) {
    msg.value = '保存失败：' + e.message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page" v-if="s">
    <div class="panel">
      <h3>监控池 <InfoTip>
        <b>12h 成交额</b> = 连续 720 根已确认 1m K 线的 USDT 成交额（volCcyQuote）之和，每确认一分钟重算一次；缺任何一分钟视为数据缺口，不参与排名。<br />
        <b>入池条件</b>：严格大于阈值，按成交额从高到低取前 N 个；达标不足 N 就显示实际数量，不拿低流动性合约凑数。<br />
        <b>类别</b>：加密 / 股票·ETF / 商品永续（OKX instCategory）。股票类成交集中在美股时段，没有现货和链上数据。<br />
        <b>保存后</b>：立即重算监控池；后台重跑 14 天历史回放，新进入的币会补齐历史数据（进度见“系统”页）。
      </InfoTip></h3>
      <div class="grid">
        <label>
          <span>12h USDT 成交额阈值（万）</span>
          <input type="number" min="10" step="50" v-model.number="thresholdWan" />
          <Segmented :model-value="thresholdWan" size="sm" aria-label="阈值预设" :options="[100, 300, 500, 1000, 2000].map(v => ({ value: v, label: v + '万' }))" @update:model-value="v => (thresholdWan = v)" />
        </label>
        <label>
          <span>上限 N（最多监控几个）</span>
          <input type="number" min="1" max="500" v-model.number="s.topN" />
          <Segmented v-model="s.topN" size="sm" aria-label="上限预设" :options="[50, 100, 200, 300].map(v => ({ value: v, label: String(v) }))" />
        </label>
        <label>
          <span>包含类别</span>
          <span class="presets">
            <span v-for="(l, c) in catLabel" :key="c" class="chip" :class="{ on: s.categories.includes(c) }" @click="toggleCat(c)">{{ l }}</span>
          </span>
        </label>
      </div>
      <p class="est" v-if="estimate">
        按最新确认的 12h 成交额：<b>{{ estimate.n }}</b> 个合约会进入监控池
        <span class="muted">（超过 {{ usd(s.threshold) }} USDT 的共 {{ estimate.raw }} 个<template v-if="estimate.raw > s.topN">，截取前 {{ s.topN }}</template>；
          <template v-for="(n, c) in estimate.byCat" :key="c">{{ catLabel[c] }} {{ n }} </template>）</span>
      </p>
    </div>

    <div class="panel">
      <h3>入场规则 <InfoTip>
        这里选择扫描页和详情页<b>默认使用</b>的评分规则；每个人也可以在扫描页临时切换。<br />
        权重、点差门槛、新建规则在“规则与战绩”页编辑；每次保存都会发布新版本，旧版本的战绩保留，便于比较。<br /><br />
        <ScoringExplainer />
      </InfoTip></h3>
      <label class="row">
        <span>默认评分规则（扫描页/详情页默认使用）</span>
        <USelect v-model="s.defaultRule" :options="ruleOptions()" width="320px" aria-label="默认规则" />
      </label>
      <table>
        <thead><tr><th>规则</th><th>多头权重</th><th>空头权重</th><th>点差门槛</th><th></th></tr></thead>
        <tbody>
          <tr v-for="r in store.meta?.rules || []" :key="r.id">
            <td><b>{{ r.name }}</b> <span class="muted">v{{ r.version }}</span> <span class="tag" v-if="r.id === s.defaultRule">默认</span></td>
            <td class="small">{{ Object.entries(r.long).map(([k, w]) => `${store.meta?.fields.find(f => f.key === k)?.label || k} ${w > 0 ? '+' : ''}${w}`).join('，') || '—' }}</td>
            <td class="small">{{ Object.entries(r.short).map(([k, w]) => `${store.meta?.fields.find(f => f.key === k)?.label || k} ${w > 0 ? '+' : ''}${w}`).join('，') || '—' }}</td>
            <td>{{ r.gates.maxSpreadBps ? r.gates.maxSpreadBps + 'bps' : '不限' }}</td>
            <td><RouterLink :to="`/rules/${r.id}`">编辑 / 战绩 →</RouterLink></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="actions">
      <button class="primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存设置' }}</button>
      <span class="note">{{ msg }}</span>
    </div>
  </div>
  <div v-else class="page note">加载中…</div>
</template>

<style scoped>
.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 16px; }
label { display: grid; gap: 6px; align-content: start; color: var(--muted); font-size: 12px; }
label.row { max-width: 420px; margin-bottom: 10px; }
input[type=number] { width: 160px; }
.presets { display: flex; gap: 4px; flex-wrap: wrap; }
.est { margin: 12px 0 4px; }
.small { font-size: 12px; white-space: normal; }
.actions { display: flex; gap: 12px; align-items: center; }
</style>

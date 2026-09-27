<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, type Settings, type Storage } from '../api'
import { store, refreshMeta, setRule, ruleOptions } from '../store'
import { usd, catLabel, time } from '../fmt'
import ChipGroup from '../components/ui/ChipGroup.vue'

// Render immediately from settings already loaded with /meta; refresh in the background.
const s = ref<Settings | null>(store.meta?.settings ? JSON.parse(JSON.stringify(store.meta.settings)) : null)
const all = ref<[string, string, number, number][]>([])
const msg = ref('')
const saving = ref(false)
const thresholdWan = computed({
  get: () => (s.value ? s.value.threshold / 1e4 : 0),
  set: v => { if (s.value) s.value.threshold = Math.round(Number(v) * 1e4) },
})

onMounted(() => {
  if (!s.value) api.settings().then(x => (s.value = x))
  api.turnovers().then(x => (all.value = x.rows)) // compact (~10 KB) list for the live estimate
  api.storage().then(x => (storage.value = x)).catch(() => {})
})

// ---- data storage (retention.go) ----
const storage = ref<Storage | null>(null)
const capGB = computed({
  get: () => (s.value ? +(s.value.storageCapMB / 1024).toFixed(2) : 0),
  set: v => { if (s.value) s.value.storageCapMB = Math.round(Number(v) * 1024) },
})
const bytes = (b?: number) => (b == null ? '—' : b >= 1 << 30 ? (b / (1 << 30)).toFixed(2) + ' GB' : (b / (1 << 20)).toFixed(0) + ' MB')
const tableLabel: Record<string, string> = {
  candles_1m: '1m K 线', oi_samples: '持仓量样本', candles_1h: '1H K 线', funding_settled: '资金费（已结算）', funding_obs: '资金费（盘中观测）',
  orderflow_1m: '订单流（逐笔 1m / 足迹）', taker_volume: '主动买卖量（OKX 统计）', other: '规则 / 评分快照 / 其他',
}
// columns high → low priority; report ages are indexed none, base, viewed, pool, watch
const tierCols: [number, string][] = [[4, '自选'], [3, '活跃池'], [2, '最近查看'], [1, '全池录制'], [0, '未跟踪']]
const tierCount = (i: number) => storage.value?.tiers?.[storage.value.tierNames[i]] ?? 0

// Live estimate from the latest confirmed 12h turnover of every instrument.
const estimate = computed(() => {
  if (!s.value || !all.value.length) return null
  const cats = new Set(s.value.categories)
  const pass = all.value.filter(([, cat, turn, live]) => live === 1 && cats.has(cat) && turn > s.value!.threshold)
  const byCat: Record<string, number> = {}
  for (const [, cat] of pass) byCat[cat] = (byCat[cat] || 0) + 1
  return { n: Math.min(pass.length, s.value.topN), raw: pass.length, byCat }
})
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
          <ChipGroup v-model="s.categories" at-least-one aria-label="类别" :options="Object.entries(catLabel).map(([c, l]) => ({ value: c, label: l }))" />
        </label>
      </div>
      <p class="est" v-if="estimate">
        按最新确认的 12h 成交额：<b>{{ estimate.n }}</b> 个合约会进入监控池
        <span class="muted">（超过 {{ usd(s.threshold) }} USDT 的共 {{ estimate.raw }} 个<template v-if="estimate.raw > s.topN">，截取前 {{ s.topN }}</template>；
          <template v-for="(n, c) in estimate.byCat" :key="c">{{ catLabel[c] }} {{ n }} </template>）</span>
      </p>
    </div>

    <div class="panel">
      <h3>数据存储 <InfoTip>
        <b>分层</b>（取最高）：<b>自选</b> ＞ <b>活跃池</b>（在池内或离池不满 7 天）＞ <b>最近查看</b>（3 天内打开过详情）＞ <b>全池录制</b>（设置类别内、未进池）＞ <b>未跟踪</b>（已下线/类别外且未查看）。<br />
        <b>按周期分别维护</b>：1m K 线、持仓量、1H K 线、资金费、订单流行数差几个数量级，各自有保留期和容量配额（上限 × 1m 45% / 持仓量 10% / 1H 10% / 资金费 5% / 逐笔订单流 18% / 主动买卖量 2%，其余 10% 给规则与评分快照）。订单流只录活跃池、自选和正在查看的合约。<br />
        <b>淘汰</b>：每 30 分钟先按下表保留期删除；某类数据超出配额时，从最低层开始逐步缩短保留期（每次 ×2/3），直到回到配额的 90%，但不低于保底（1m/持仓量 26 小时，自选 3 天；1H 21 天），被收紧的格子会标出。<br />
        <b>下载</b>：打开详情或加自选时按需补近 7 天 1m（不超过该层保留期），离开列表的币按新层级自动淘汰。评分快照（战绩）永不删除。<br />
        删除后的空间在库文件内复用，文件本身不缩小。
      </InfoTip></h3>
      <div class="grid">
        <label>
          <span>存储上限（GB）</span>
          <input type="number" min="0.5" max="100" step="0.5" v-model.number="capGB" />
          <Segmented :model-value="capGB" size="sm" aria-label="上限预设" :options="[1, 2, 5, 10].map(v => ({ value: v, label: v + ' GB' }))" @update:model-value="v => (capGB = v)" />
        </label>
      </div>
      <template v-if="storage">
        <p class="est">已用 <b>{{ bytes(storage.usedBytes) }}</b> / {{ bytes(storage.capBytes) }}
          <span class="muted">（库文件 {{ bytes(storage.fileBytes) }}，其中可复用 {{ bytes(storage.freeBytes) }}；{{ time(storage.at) }} 检查）</span>
        </p>
        <div class="table-scroll"><table class="st">
          <thead><tr><th>数据</th><th class="num">占用 / 配额</th><th v-for="[i, l] in tierCols" :key="i" class="num">{{ l }}<br /><small class="muted">{{ tierCount(i) }} 个</small></th></tr></thead>
          <tbody>
            <tr v-for="t in storage.tables" :key="t.name">
              <td>{{ tableLabel[t.name] || t.name }} <span class="tag warn" v-if="t.tight" title="超出配额，已从低层开始缩短保留期">已收紧</span></td>
              <td class="num">{{ t.name === 'funding_obs' ? '（同上）' : bytes(t.bytes) + ' / ' + bytes(t.budget) }}</td>
              <td v-for="[i] in tierCols" :key="i" class="num">{{ t.ages?.[i] ?? '长期' }}</td>
            </tr>
          </tbody>
        </table></div>
        <p class="note" v-if="storage.error">{{ storage.error }}</p>
      </template>
      <p class="note" v-else>存储检查在服务启动约 2 分钟后首次运行。</p>
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
      <div class="table-scroll"><table>
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
      </table></div>
    </div>

    <div class="actions">
      <button class="primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存设置' }}</button>
      <span class="note">{{ msg }}</span>
    </div>
  </div>
  <div v-else class="page note">加载设置…</div>
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
.st th small { font-weight: 400; }
</style>

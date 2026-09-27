<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { LineSeries, type IChartApi } from 'lightweight-charts'
import { api, type Rule, type Track, type Stats } from '../api'
import { store, refreshMeta, setRule } from '../store'
import { time, num, chartTime } from '../fmt'
import { baseChart } from '../components/charts'

const props = defineProps<{ id?: string }>()
const router = useRouter()
const rules = computed(() => store.meta?.rules || [])
const cur = computed(() => rules.value.find(r => r.id === (props.id || store.ruleId)) || rules.value[0])
const tr = ref<Track | null>(null)
const draft = ref<Rule | null>(null)
const msg = ref('')
const newId = ref('')
const chartEl = ref<HTMLElement>()
let chart: IChartApi | undefined
let timer: number | undefined

watch(cur, r => {
  if (!r) return
  draft.value = JSON.parse(JSON.stringify(r))
  msg.value = ''
  loadTrack()
}, { immediate: true })

async function loadTrack() {
  if (!cur.value) return
  tr.value = await api.track(cur.value.id)
  await nextTick()
  drawCum()
}
onMounted(() => { timer = window.setInterval(loadTrack, 30_000) })
onUnmounted(() => { clearInterval(timer); chart?.remove() })

function drawCum() {
  if (!chartEl.value) return
  chart?.remove()
  chart = baseChart(chartEl.value, { timeScale: { timeVisible: true } })
  const add = (st: Stats | undefined, color: string, title: string) => {
    if (!st?.cumLongIC?.length) return
    const s = chart!.addSeries(LineSeries, { color, lineWidth: 2, title, priceFormat: { type: 'price', precision: 2, minMove: 0.01 } })
    s.setData(st.cumLongIC.map(p => ({ time: chartTime(p.ts), value: p.v })))
  }
  add(tr.value?.replay?.[0], '#8a93a3', '回放 1h')
  add(tr.value?.live?.[0], '#5b8def', '上线后 1h')
  chart.timeScale().fitContent()
}

function weight(side: 'long' | 'short', k: string) { return draft.value?.[side]?.[k] ?? '' }
function setWeight(side: 'long' | 'short', k: string, v: string) {
  if (!draft.value) return
  const n = parseFloat(v)
  if (v === '' || isNaN(n) || n === 0) delete draft.value[side][k]
  else draft.value[side][k] = n
}
async function save(asNew: boolean) {
  if (!draft.value) return
  const body = { ...draft.value }
  if (asNew) {
    if (!newId.value) { msg.value = '请填写新规则 ID（小写字母/数字/短横线）'; return }
    body.id = newId.value
  }
  try {
    const saved = await api.saveRule(body)
    await refreshMeta()
    setRule(saved.id)
    msg.value = `已保存 ${saved.id} v${saved.version}；历史回放正在重算，上线后战绩从现在开始记录。`
    router.replace(`/rules/${saved.id}`)
    setTimeout(loadTrack, 2500)
  } catch (e: any) {
    msg.value = '保存失败：' + e.message
  }
}
async function del() {
  if (!cur.value || !confirm(`删除规则 ${cur.value.id}？（已记录的快照保留在数据库中）`)) return
  try {
    await api.deleteRule(cur.value.id)
    await refreshMeta()
    router.replace('/rules')
  } catch (e: any) {
    msg.value = e.message
  }
}
const scorable = computed(() => (store.meta?.fields || []).filter(f => f.key !== 'turnover12h'))
const verdictClass = (v: string) => (v.startsWith('有初步') ? 'good' : v.startsWith('方向相反') ? 'bad' : 'warn')
const H = (h: number) => `${h}h`
</script>

<template>
  <div class="page rules">
    <div class="panel list">
      <h3>规则 <InfoTip>
        所有规则默认标“未验证”。“对照”是内置的三条比较基准（不是推荐）。<br />
        判断一条规则有没有用，只看右侧战绩，尤其是“上线后”的样本外记录；历史回放含幸存者偏差，只能参考。
      </InfoTip></h3>
      <RouterLink v-for="r in rules" :key="r.id" :to="`/rules/${r.id}`" class="item" :class="{ on: cur?.id === r.id }">
        <b>{{ r.name }}</b> <span class="muted">v{{ r.version }}</span>
        <span class="tag" v-if="r.preset">对照</span>
        <div class="muted small">{{ r.id }}</div>
      </RouterLink>
    </div>

    <div class="main" v-if="cur && draft">
      <div class="panel">
        <h3>{{ cur.name }} <span class="muted">v{{ cur.version }}</span> <span class="tag warn">未验证</span>
          <button class="small" @click="setRule(cur.id)" :class="{ on: store.ruleId === cur.id }">{{ store.ruleId === cur.id ? '扫描页使用中' : '在扫描页使用' }}</button>
        </h3>
        <p class="desc">{{ cur.description }}</p>
        <p class="note" v-if="tr && !tr.replayable">含“不可回放”字段，无法做历史回放，只能积累上线后战绩。</p>
      </div>

      <div class="panel">
        <h3>战绩 <InfoTip>
          <b>Rank IC</b>：同一时刻活跃池内“分数排名”与“之后 h 小时实际收益排名”的 Spearman 相关，取值 −1～1；0.02–0.05 在因子研究里已算有信号。空头 IC 用 −收益计算。<br />
          <b>t 值</b>：IC 均值 ÷ 标准误；每个周期只取不重叠样本（1h 每小时、4h 每 4 小时…），避免重复计数。|t| &gt; 2 大致对应 5% 显著性。<br />
          <b>前 20% 超额 / 净</b>：分数最高的五分之一，平均收益比池均值多多少（毛）；再扣双边 taker 5bps×2 和点差后的绝对收益（净）。<br />
          <b>结论门槛</b>：≥30 个截面、t &gt; 2 且扣费后为正，才标“有初步证据”；显著为负标“方向相反”。<br />
          <b>历史回放</b>：用 1H 数据按当时的监控池重算，只含现在仍上市的币（幸存者偏差），点差按 2bps 假设。<b>上线后</b>：每个整点真实快照，不可改写。<br />
          曲线为多头累计 IC：持续向上 = 稳定有效，向下 = 反向。
        </InfoTip></h3>
        <div class="note">回放：{{ tr?.replayState }} · 上线后：{{ tr?.liveSince ? '自 ' + time(tr.liveSince, true) + ' 起每整点记录' : '尚无记录（每个整点快照一次活跃池）' }}</div>
        <div class="tables">
          <div v-for="kind in (['replay', 'live'] as const)" :key="kind">
            <div class="kh">{{ kind === 'replay' ? '历史回放（1H 数据，当前挂牌币，含幸存者偏差，点差假设 2bps）' : '上线后（真实时点快照，不可改写）' }}</div>
            <table>
              <thead><tr><th>周期</th><th class="num">截面</th><th class="num">多 IC (t)</th><th class="num">空 IC (t)</th><th class="num">多 top20% 超额/净</th><th class="num">空 top20% 超额/净</th><th>结论</th></tr></thead>
              <tbody>
                <tr v-for="s in (kind === 'replay' ? tr?.replay : tr?.live) || []" :key="s.horizonH" :class="{ empty: !s.crossSections }">
                  <td>{{ H(s.horizonH) }}</td>
                  <td class="num">{{ s.crossSections }}<span class="muted">×{{ s.medianNames }}</span></td>
                  <td class="num" :class="s.longIC > 0 ? 'up' : 'down'">{{ num(s.longIC, 3) }} <span class="muted">({{ num(s.longT, 1) }})</span></td>
                  <td class="num" :class="s.shortIC > 0 ? 'up' : 'down'">{{ num(s.shortIC, 3) }} <span class="muted">({{ num(s.shortT, 1) }})</span></td>
                  <td class="num">{{ num(s.longTopExcessBps, 1) }} / <span :class="s.longTopNetBps > 0 ? 'up' : 'down'">{{ num(s.longTopNetBps, 1) }}</span> bps</td>
                  <td class="num">{{ num(s.shortTopExcessBps, 1) }} / <span :class="s.shortTopNetBps > 0 ? 'up' : 'down'">{{ num(s.shortTopNetBps, 1) }}</span> bps</td>
                  <td><span class="tag" :class="verdictClass(s.verdict)">{{ s.verdict }}</span></td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
        <div ref="chartEl" class="cum"></div>
      </div>

      <div class="panel">
        <h3>编辑权重 <InfoTip>
          <b>正权重</b> = 该字段在池内越高越加分；<b>负权重</b> = 越低越加分；留空或 0 = 不使用。权重只看相对大小（会除以 Σ|w|）。<br />
          <b>点差门槛</b>：点差超过该值时标“门槛”，分数照算但提示不可交易成本过高。<br />
          <b>保存</b> = 发布新版本（v+1）：立即对当前池重新打分，后台重算历史回放；旧版本的战绩保留。<br /><br />
          <ScoringExplainer />
        </InfoTip></h3>
        <div class="form">
          <label>名称 <input v-model="draft.name" /></label>
          <label>点差门槛 (bps，0=不限) <input type="number" step="1" min="0" v-model.number="draft.gates.maxSpreadBps" /></label>
          <label class="full">说明 <input v-model="draft.description" /></label>
        </div>
        <div class="table-scroll"><table class="weights">
          <thead><tr><th>字段</th><th class="num">多头权重</th><th class="num">空头权重</th><th class="hide-sm">定义</th></tr></thead>
          <tbody>
            <tr v-for="f in scorable" :key="f.key">
              <td>{{ f.label }} <span class="tag" v-if="!f.replayable">不可回放</span></td>
              <td class="num"><input type="number" step="0.05" :value="weight('long', f.key)" @change="setWeight('long', f.key, ($event.target as HTMLInputElement).value)" /></td>
              <td class="num"><input type="number" step="0.05" :value="weight('short', f.key)" @change="setWeight('short', f.key, ($event.target as HTMLInputElement).value)" /></td>
              <td class="muted small hide-sm wrap">{{ f.desc }}</td>
            </tr>
          </tbody>
        </table></div>
        <div class="actions">
          <button class="primary" @click="save(false)">保存为 {{ cur.id }} v{{ cur.version + 1 }}</button>
          <input v-model="newId" placeholder="新规则 ID，如 my-rule" />
          <button @click="save(true)">另存为新规则</button>
          <button v-if="!cur.preset" @click="del">删除</button>
          <span class="note">{{ msg }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.rules { grid-template-columns: 240px 1fr; align-items: start; }
.list { display: grid; gap: 4px; position: sticky; top: 56px; }
.item { display: block; padding: 6px 8px; border-radius: 6px; color: var(--text); }
.item.on { background: var(--panel2); }
@media (hover: hover) { .item:hover { background: var(--panel2); } }
.small { font-size: 11px; }
.main { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; min-width: 0; }
.desc { margin: 0; }
.tables { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; }
.tables > div { overflow-x: auto; }
.kh { font-weight: 600; margin: 6px 0 2px; }
.cum { height: 180px; margin-top: 8px; }
tr.empty td:not(:first-child):not(:last-child) { color: transparent; }
tr.empty td:nth-child(2) { color: var(--muted); }
.form { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; margin-bottom: 8px; }
.form label { display: grid; gap: 2px; color: var(--muted); font-size: 12px; }
.form .full { grid-column: 1 / -1; }
.weights input { width: 80px; text-align: right; }
.wrap { white-space: normal; }
.actions { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
h3 { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
@media (max-width: 900px) { .rules { grid-template-columns: minmax(0, 1fr); } .list { position: static; } .form { grid-template-columns: 1fr; } }
</style>

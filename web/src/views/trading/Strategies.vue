<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { RefreshCw, FileCode2, History, Library, Plus, BookOpen, Copy, Pencil, Trash2, Lock } from 'lucide-vue-next'
import { tradeApi, type Source, type StrategyEntry } from '../../tradeApi'
import { store } from '../../store'
import Loading from '../../components/ui/Loading.vue'
import Modal from '../../components/Modal.vue'
import { day, directionText, paramText, universeText } from '../../components/trade/meta'
import { templates } from '../../components/editor/templates'

const router = useRouter()
const entries = ref<StrategyEntry[] | null>(null)
const sources = ref<Source[]>([])
const error = ref('')
const reloading = ref(false)
const reloadMsg = ref('')
async function load() {
  try {
    const [es, ss] = await Promise.all([tradeApi.strategies(), tradeApi.sources().catch(() => [] as Source[])])
    entries.value = es
    sources.value = ss
    error.value = ''
  } catch (e: any) { error.value = e.message }
}
onMounted(load)
async function reload() {
  reloading.value = true
  reloadMsg.value = ''
  try {
    await tradeApi.reloadStrategies()
    const before = new Set((entries.value || []).flatMap(e => e.versions.map(v => v.id)))
    await load()
    const fresh = (entries.value || []).flatMap(e => e.versions).filter(v => !before.has(v.id)).length
    reloadMsg.value = fresh ? `发现 ${fresh} 个新版本` : '没有变化'
  } catch (e: any) {
    reloadMsg.value = '扫描失败：' + e.message
  } finally {
    reloading.value = false
  }
}

// built-in = shipped in strategies/library; user strategies are published from a source
const builtin = computed(() => (entries.value || []).filter(e => !e.latest.meta?.sourceId))
const published = computed(() => new Map((entries.value || []).filter(e => e.latest.meta?.sourceId).map(e => [e.latest.meta!.sourceId!, e])))
// published versions whose draft was deleted: still usable (instances, backtests), shown read-only
const orphans = computed(() => (entries.value || []).filter(e => e.latest.meta?.sourceId && !sources.value.some(s => s.id === e.latest.meta!.sourceId)))
const readonlyGroups = computed(() => [
  { key: 'lib', title: '内置策略', items: builtin.value },
  ...(orphans.value.length ? [{ key: 'orphan', title: '草稿已删除的已发布策略', items: orphans.value }] : []),
])

const ruleName = (id: string) => store.meta?.rules.find(r => r.id === id)?.name || id
const sel = ref<Record<string, string>>({}) // shown version per strategy name
const shown = (e: StrategyEntry) => e.versions.find(v => v.id === sel.value[e.name]) ?? e.latest
const verOpts = (e: StrategyEntry) => e.versions.map((v, i) => ({ value: v.id, label: `v${e.versions.length - i} · ${v.id.slice(0, 8)}`, desc: day(v.createdAt) + (v.id === e.latest.id ? ' · 最新' : '') }))
const range = (p: { type?: string; min?: number; max?: number; step?: number; options?: string[]; labels?: string[] }) =>
  p.type === 'bool' ? '开 / 关' : p.type === 'choice' || p.type === 'timeframe' ? (p.labels ?? p.options ?? []).join(' / ')
    : `${p.min ?? '—'} – ${p.max ?? '—'}${p.step ? ` / ${p.step}` : ''}`

// ---- new strategy ----
const newOpen = ref(false)
const creating = ref('')
const createErr = ref('')
async function create(code: string, id: string) {
  creating.value = id
  createErr.value = ''
  try {
    const s = await tradeApi.createSource({ code })
    router.push(`/trading/strategies/edit/${s.id}`)
  } catch (e: any) { createErr.value = e.message } finally { creating.value = '' }
}
async function copy(versionId: string) {
  try {
    const s = await tradeApi.createSource({ fromVersion: versionId })
    router.push(`/trading/strategies/edit/${s.id}`)
  } catch (e: any) { error.value = e.message }
}
async function remove(s: Source) {
  if (!confirm(`删除草稿「${s.name}」？已发布的版本会保留，正在用它的实例不受影响。`)) return
  try { await tradeApi.deleteSource(s.id); await load() } catch (e: any) { error.value = e.message }
}
</script>

<template>
  <div class="page">
    <div class="ph">
      <h1>策略库</h1>
      <InfoTip>
        策略是用 <code>ctg</code> SDK 写的 Python 类：声明参数、交易标的和周期，在每根已收盘 K 线上返回信号。<br />
        <b>内置策略</b>只读，作为示例；点「复制为新策略」得到可编辑的副本。<b>我的策略</b>在网页里编辑，自动保存为草稿，
        「发布」后生成不可变版本，才能用于回测和实例。<br />
        策略只发信号，下单、风控、仓位都由账户统一处理；策略进程在隔离容器里运行，没有网络。
      </InfoTip>
      <span class="sp"></span>
      <span v-if="reloadMsg" class="muted small">{{ reloadMsg }}</span>
      <RouterLink to="/help/strategy" class="btn"><BookOpen :size="14" />策略文档</RouterLink>
      <button type="button" class="btn" :disabled="reloading" title="重新读取 strategies/library 下的内置策略" @click="reload"><RefreshCw :size="14" :class="{ spin: reloading }" />重新扫描</button>
      <button type="button" class="btn primary" @click="newOpen = true"><Plus :size="14" />新建策略</button>
    </div>
    <div v-if="error" class="banner">{{ error }}</div>
    <Loading v-if="!entries && !error" text="加载策略库" />

    <template v-if="entries">
      <h2 class="sec">我的策略</h2>
      <div v-if="!sources.length" class="panel empty">
        <span class="muted">还没有自建策略。</span>
        <button type="button" class="btn" @click="newOpen = true"><Plus :size="14" />从模板新建</button>
        <span class="muted">或在下面的内置策略上点「复制为新策略」。</span>
      </div>
      <div class="grid">
        <article v-for="s in sources" :key="s.id" class="panel st">
          <header class="sh">
            <h3>{{ s.name }}</h3>
            <span class="tag" :class="!s.published ? 'warn' : s.dirty ? 'warn' : 'good'">{{ !s.published ? '草稿' : s.dirty ? '有未发布改动' : '已发布' }}</span>
            <span class="sp"></span>
            <span class="muted small">改于 {{ day(s.updatedAt) }}</span>
          </header>
          <template v-if="published.get(s.id)">
            <p class="desc">{{ published.get(s.id)!.latest.description || '—' }}</p>
            <div class="kv">
              <div><span class="l">周期 / 方向</span><span>{{ published.get(s.id)!.latest.timeframe }} · {{ directionText(published.get(s.id)!.latest.meta) }}</span></div>
              <div><span class="l">已发布</span><span>{{ published.get(s.id)!.versions.length }} 个版本 · 最新 {{ published.get(s.id)!.latest.id.slice(0, 8) }}</span></div>
            </div>
          </template>
          <p v-else class="desc muted">还没发布：发布后才能回测和添加到账户。</p>
          <footer class="sf">
            <button type="button" class="btn ghost" title="删除草稿" @click="remove(s)"><Trash2 :size="14" /></button>
            <span class="sp"></span>
            <RouterLink v-if="s.published" :to="`/trading/new?mode=backtest&strategy=${s.published}`" class="btn">回测</RouterLink>
            <RouterLink :to="`/trading/strategies/edit/${s.id}`" class="btn primary"><Pencil :size="14" />编辑</RouterLink>
          </footer>
        </article>
      </div>

      <template v-for="g in readonlyGroups" :key="g.key">
      <h2 class="sec">{{ g.title }} <Lock :size="13" class="muted" /></h2>
      <div v-if="!g.items.length" class="panel empty"><Library :size="22" class="muted" /><span class="muted">没有内置策略</span></div>
      <div class="grid">
        <article v-for="e in g.items" :key="e.name" class="panel st">
          <header class="sh">
            <h3>{{ e.name }}</h3>
            <span class="tag">{{ shown(e).timeframe }}</span>
            <span class="tag">{{ directionText(shown(e).meta) }}</span>
            <span class="sp"></span>
            <USelect v-if="e.versions.length > 1" :model-value="shown(e).id" :options="verOpts(e)" size="sm" width="170px" :icon="History" aria-label="版本" @update:model-value="v => (sel[e.name] = String(v))" />
            <span v-else class="ver">{{ shown(e).id.slice(0, 8) }}</span>
          </header>
          <p class="desc">{{ shown(e).description || '—' }}</p>
          <div class="kv">
            <div><span class="l">默认标的</span><span>{{ universeText(shown(e).defaultUniverse, ruleName) }}</span></div>
            <div><span class="l">版本</span><span>{{ e.versions.length }} 个 · 最新 {{ day(e.latest.createdAt) }}</span></div>
          </div>
          <div class="table-scroll">
            <table v-if="(shown(e).params || []).length" class="pt">
              <thead><tr><th>参数</th><th class="num">默认</th><th class="num">可选 / 范围</th><th>说明</th></tr></thead>
              <tbody>
                <tr v-for="p in shown(e).params" :key="p.name">
                  <td><span class="pl">{{ p.label || p.name }}</span><span v-if="p.aiTunable" class="tag info ai">AI</span><div v-if="p.group" class="pg">{{ p.group }}</div></td>
                  <td class="num">{{ paramText(p, p.default) }}</td>
                  <td class="num sub">{{ range(p) }}</td>
                  <td class="pd">{{ p.desc || '—' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <footer class="sf">
            <RouterLink :to="`/trading/strategies/view/${shown(e).id}`" class="btn"><FileCode2 :size="14" />代码与讲解</RouterLink>
            <button type="button" class="btn" @click="copy(shown(e).id)"><Copy :size="14" />复制为新策略</button>
            <RouterLink :to="`/trading/new?mode=backtest&strategy=${shown(e).id}`" class="btn primary">回测</RouterLink>
          </footer>
        </article>
      </div>
      </template>
    </template>

    <Modal :open="newOpen" title="新建策略" @close="newOpen = false">
      <p class="muted small">选一个起点，创建后在编辑器里修改。也可以先看<RouterLink to="/help/strategy/quickstart">快速开始</RouterLink>。</p>
      <p v-if="createErr" class="down small">{{ createErr }}</p>
      <div class="tpl">
        <button v-for="t in templates" :key="t.id" type="button" class="ti" :disabled="!!creating" @click="create(t.code, t.id)">
          <b>{{ t.title }}</b><span>{{ t.desc }}</span><span v-if="creating === t.id" class="muted">创建中…</span>
        </button>
      </div>
    </Modal>
  </div>
</template>

<style scoped>
.ph { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.ph h1 { margin: 0; font-size: var(--fs-2xl); font-weight: 600; }
.sp { flex: 1; }
.btn { display: inline-flex; align-items: center; gap: 4px; height: 30px; padding: 0 12px; font-size: var(--fs-sm); border-radius: var(--radius-sm); text-decoration: none !important; }
.btn.primary { background: var(--accent); color: var(--on-accent); font-weight: 600; }
.btn.ghost { border-color: transparent; background: none; color: var(--text-3); padding: 0 6px; }
.spin { animation: rot 1s linear infinite; }
@keyframes rot { to { transform: rotate(360deg); } }
.banner { border-radius: var(--radius-sm); }
.sec { display: flex; align-items: center; gap: 6px; margin: 14px 0 0; font-size: var(--fs-lg); font-weight: 600; }
.empty { display: flex; align-items: center; justify-content: center; gap: 8px; padding: 20px; flex-wrap: wrap; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 460px), 1fr)); gap: 12px; align-items: start; }
.st { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; }
.sh { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; min-width: 0; }
.sh h3 { margin: 0; font-size: var(--fs-xl); font-weight: 600; overflow-wrap: anywhere; }
.ver { font: var(--fs-xs) ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-3); }
.desc { margin: 0; color: var(--text-2); font-size: var(--fs-md); }
.kv { display: grid; gap: 4px; font-size: var(--fs-sm); }
.kv > div { display: flex; gap: 8px; min-width: 0; }
.kv .l { flex: none; width: 72px; color: var(--text-3); font-size: var(--fs-xs); line-height: 20px; }
.kv > div > :last-child { min-width: 0; overflow-wrap: anywhere; color: var(--text-2); }
code { font: var(--fs-sm) ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-1); }
.pt td { font-size: var(--fs-sm); }
.pl { color: var(--text-1); }
.pg { font-size: 10px; color: var(--text-3); }
.pt .pd { white-space: normal; min-width: 160px; color: var(--text-2); }
.pt .sub { color: var(--text-2); white-space: normal; }
.ai { margin-left: 6px; height: 16px; font-size: 10px; }
.sf { display: flex; gap: 8px; justify-content: flex-end; flex-wrap: wrap; }
.tpl { display: grid; gap: 8px; margin-top: 10px; }
.ti { all: unset; box-sizing: border-box; display: grid; gap: 2px; padding: 10px 12px; border: 1px solid var(--line-2); border-radius: 8px; cursor: pointer; }
.ti b { font-size: var(--fs-md); color: var(--text-1); }
.ti span { font-size: var(--fs-sm); color: var(--text-2); }
@media (hover: hover) { .ti:hover { border-color: var(--accent); } }
.small { font-size: var(--fs-sm); }
</style>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RefreshCw, FileCode2, History, Library } from 'lucide-vue-next'
import { tradeApi, type StrategyEntry, type StrategyVersion } from '../../tradeApi'
import { store } from '../../store'
import Loading from '../../components/ui/Loading.vue'
import Modal from '../../components/Modal.vue'
import { day, universeText } from '../../components/trade/meta'

const entries = ref<StrategyEntry[] | null>(null)
const error = ref('')
const reloading = ref(false)
const reloadMsg = ref('')
async function load() {
  try { entries.value = await tradeApi.strategies(); error.value = '' } catch (e: any) { error.value = e.message }
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

const ruleName = (id: string) => store.meta?.rules.find(r => r.id === id)?.name || id
const sel = ref<Record<string, string>>({}) // shown version per strategy name
const shown = (e: StrategyEntry) => e.versions.find(v => v.id === sel.value[e.name]) ?? e.latest
const verOpts = (e: StrategyEntry) => e.versions.map((v, i) => ({ value: v.id, label: `v${e.versions.length - i} · ${v.id.slice(0, 8)}`, desc: day(v.createdAt) + (v.id === e.latest.id ? ' · 最新' : '') }))

const codeOpen = ref(false)
const code = ref<StrategyVersion | null>(null)
const codeErr = ref('')
async function openCode(v: StrategyVersion) {
  codeOpen.value = true
  code.value = v.code ? v : null
  codeErr.value = ''
  if (!v.code) { try { code.value = await tradeApi.strategy(v.id) } catch (e: any) { codeErr.value = e.message } }
}
</script>

<template>
  <div class="page">
    <div class="ph">
      <h1>策略库</h1>
      <InfoTip>
        策略是 <code>strategies/library/</code> 下的 Python 文件，用 <code>ctg</code> SDK 编写：声明参数（默认值、范围、说明）、交易标的（固定列表或规则筛选）和周期，在每根已收盘 K 线上返回信号。<br />
        <b>版本</b>：代码每变化一次就生成一个新版本（代码哈希），旧版本保留，已挂载的实例继续用它们创建时的版本。<br />
        策略只发信号，下单、风控、仓位都由账户统一处理；策略进程在隔离容器里运行，没有网络。<br />
        改完代码后点“重新扫描”。
      </InfoTip>
      <span class="sp"></span>
      <span v-if="reloadMsg" class="muted small">{{ reloadMsg }}</span>
      <button type="button" :disabled="reloading" @click="reload"><RefreshCw :size="14" :class="{ spin: reloading }" />重新扫描</button>
    </div>
    <div v-if="error" class="banner">{{ error }}</div>
    <Loading v-if="!entries && !error" text="加载策略库" />
    <div v-else-if="entries && !entries.length" class="panel empty"><Library :size="22" class="muted" /><span class="muted">策略库为空</span></div>

    <div class="grid">
      <article v-for="e in entries || []" :key="e.name" class="panel st">
        <header class="sh">
          <h2>{{ e.name }}</h2>
          <span class="tag">{{ shown(e).timeframe }}</span>
          <span class="sp"></span>
          <USelect v-if="e.versions.length > 1" :model-value="shown(e).id" :options="verOpts(e)" size="sm" width="170px" :icon="History" aria-label="版本" @update:model-value="v => (sel[e.name] = String(v))" />
          <span v-else class="ver">{{ shown(e).id.slice(0, 8) }}</span>
        </header>
        <p class="desc">{{ shown(e).description || '—' }}</p>
        <div class="kv">
          <div><span class="l">文件</span><code>{{ shown(e).file }}</code></div>
          <div><span class="l">默认标的</span><span>{{ universeText(shown(e).defaultUniverse, ruleName) }}</span></div>
          <div><span class="l">版本</span><span>{{ e.versions.length }} 个 · 最新 {{ day(e.latest.createdAt) }}</span></div>
        </div>
        <div class="table-scroll">
          <table v-if="(shown(e).params || []).length" class="pt">
            <thead><tr><th>参数</th><th class="num">默认</th><th class="num">范围</th><th>说明</th></tr></thead>
            <tbody>
              <tr v-for="p in shown(e).params" :key="p.name">
                <td><code>{{ p.name }}</code><span v-if="p.aiTunable" class="tag info ai">AI</span></td>
                <td class="num">{{ p.default }}</td>
                <td class="num sub">{{ p.min ?? '—' }} – {{ p.max ?? '—' }}<template v-if="p.step"> / {{ p.step }}</template></td>
                <td class="pd">{{ p.desc || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <footer class="sf">
          <button type="button" @click="openCode(shown(e))"><FileCode2 :size="14" />代码</button>
          <RouterLink :to="`/trading/new?mode=backtest&strategy=${shown(e).id}`" class="btn primary">用此策略回测</RouterLink>
        </footer>
      </article>
    </div>

    <Modal :open="codeOpen" :title="code ? `${code.name} · ${code.id.slice(0, 8)}` : '代码'" wide @close="codeOpen = false">
      <p v-if="codeErr" class="down small">{{ codeErr }}</p>
      <Loading v-else-if="!code" text="读取代码" />
      <template v-else>
        <div class="muted small cf"><code>{{ code.file }}</code> · {{ day(code.createdAt) }}</div>
        <pre class="code"><code>{{ code.code }}</code></pre>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.ph { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.ph h1 { margin: 0; font-size: var(--fs-2xl); font-weight: 600; }
.ph button { display: inline-flex; align-items: center; gap: 4px; height: 30px; }
.sp { flex: 1; }
.spin { animation: rot 1s linear infinite; }
@keyframes rot { to { transform: rotate(360deg); } }
.banner { border-radius: var(--radius-sm); }
.empty { display: flex; align-items: center; justify-content: center; gap: 8px; padding: 32px; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 460px), 1fr)); gap: 12px; align-items: start; }
.st { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; }
.sh { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; min-width: 0; }
.sh h2 { margin: 0; font-size: var(--fs-xl); font-weight: 600; overflow-wrap: anywhere; }
.ver { font: var(--fs-xs) ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-3); }
.desc { margin: 0; color: var(--text-2); font-size: var(--fs-md); }
.kv { display: grid; gap: 4px; font-size: var(--fs-sm); }
.kv > div { display: flex; gap: 8px; min-width: 0; }
.kv .l { flex: none; width: 64px; color: var(--text-3); font-size: var(--fs-xs); line-height: 20px; }
.kv > div > :last-child { min-width: 0; overflow-wrap: anywhere; color: var(--text-2); }
code { font: var(--fs-sm) ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-1); }
.pt td { font-size: var(--fs-sm); }
.pt .pd { white-space: normal; min-width: 160px; color: var(--text-2); }
.pt .sub { color: var(--text-2); }
.ai { margin-left: 6px; height: 16px; font-size: 10px; }
.sf { display: flex; gap: 8px; justify-content: flex-end; }
.sf button, .btn { display: inline-flex; align-items: center; gap: 4px; height: 30px; padding: 0 12px; font-size: var(--fs-sm); }
.btn { border-radius: var(--radius-sm); text-decoration: none !important; }
.btn.primary { background: var(--accent); color: var(--on-accent); font-weight: 600; }
.cf { margin-bottom: 8px; }
.code { margin: 0; padding: 12px 14px; background: var(--bg-0); border: 1px solid var(--line); border-radius: 8px; overflow: auto; max-width: 100%; font-size: var(--fs-sm); line-height: 1.55; tab-size: 4; }
.code code { font-size: var(--fs-sm); color: var(--text-2); white-space: pre; }
.small { font-size: var(--fs-sm); }
</style>

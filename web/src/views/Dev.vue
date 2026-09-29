<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Plus, Pencil, Ban, Trash2, KeyRound, Timer, ChevronRight, FileJson, TriangleAlert } from 'lucide-vue-next'
import { api, type ApiKey, type DevConfig, type DevEndpoints, type DevLog } from '../api'
import { time, ago } from '../fmt'
import Loading from '../components/ui/Loading.vue'
import KeyForm from '../components/dev/KeyForm.vue'
import Tester from '../components/dev/Tester.vue'
import CopyBlock from '../components/dev/CopyBlock.vue'
import { claudeCodeSnippet, mcpJsonSnippet, mcpUrl, restBase } from '../components/dev/snippets'

const keys = ref<ApiKey[] | null>(null)
const cfg = ref<DevConfig | null>(null)
const eps = ref<DevEndpoints | null>(null)
const logs = ref<DevLog[] | null>(null)
const error = ref('')

async function refresh() {
  try {
    const [k, l] = await Promise.all([api.devKeys(), api.devLog()])
    keys.value = k
    logs.value = l
    error.value = ''
  } catch (e: any) {
    error.value = e.message
  }
}
onMounted(() => {
  refresh()
  api.devConfig().then(c => (cfg.value = c)).catch(e => (error.value = e.message))
  api.devEndpoints().then(x => { eps.value = x; if (!endpoint.value) endpoint.value = x.endpoints.find(e => e.name === 'get_universe')?.name || x.endpoints[0]?.name || '' })
})
const timer = window.setInterval(() => { if (!document.hidden) refresh() }, 10_000)
onUnmounted(() => clearInterval(timer))

// ---- keys ----
const formOpen = ref(false)
const editing = ref<ApiKey | null>(null)
const testKey = ref('') // this page only; filled with a newly created secret
const endpoint = ref('')
function openForm(k: ApiKey | null) { editing.value = k; formOpen.value = true }
function upsert(k: ApiKey) {
  const list = keys.value || []
  keys.value = list.some(x => x.id === k.id) ? list.map(x => (x.id === k.id ? k : x)) : [k, ...list]
}
function onSaved(k: ApiKey) { upsert(k); formOpen.value = false }
function onCreated(k: ApiKey & { secret: string }) {
  const { secret, ...rest } = k
  upsert(rest)
  testKey.value = secret
}
async function revoke(k: ApiKey) {
  if (!confirm(`吊销「${k.name}」？使用它的 Agent 会立即收到 401，无法恢复。`)) return
  try { upsert(await api.updateKey(k.id, { revoke: true })) } catch (e: any) { error.value = e.message }
}
async function remove(k: ApiKey) {
  if (!confirm(`删除「${k.name}」及其用量统计？`)) return
  try {
    await api.deleteKey(k.id)
    keys.value = (keys.value || []).filter(x => x.id !== k.id)
  } catch (e: any) {
    error.value = e.message
  }
}
const statusTag = (k: ApiKey) =>
  k.status === 'active' ? { t: '有效', c: 'good' } : k.status === 'expired' ? { t: '已过期', c: 'warn' } : { t: '已吊销', c: 'bad' }
const activeCount = computed(() => (keys.value || []).filter(k => k.status === 'active').length)

// ---- endpoints ----
const open = ref<string | null>(null)
const testerEl = ref<HTMLElement | null>(null)
function tryIt(name: string) {
  endpoint.value = name
  testerEl.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

// ---- access snippets ----
const snip = ref<'claude' | 'json'>('claude')
const shownLogs = ref(30)
const logCls = (s: number) => (s === 200 ? 'good' : s === 429 ? 'warn' : 'bad')
</script>

<template>
  <div class="page dev">
    <div class="banner" v-if="error">{{ error }}</div>
    <div v-if="cfg && !cfg.enabled" class="off">
      <TriangleAlert :size="15" />
      <span>外部 API 与 MCP 已关闭，所有 Key 调用会返回 503。</span>
      <RouterLink to="/settings#dev">去设置开启 →</RouterLink>
    </div>

    <div class="cols">
      <!-- keys -->
      <div class="panel keys-p">
        <div class="ph">
          <h3>访问凭证 <span class="panel-sub" v-if="keys">{{ activeCount }} 个有效</span>
            <InfoTip>
              <b>API Key</b>：长期凭证（可选过期），给常驻 Agent / 服务用。<b>Token</b>：短期凭证，必须设置有效期（≤ 30 天）。两者用法相同：<code>Authorization: Bearer &lt;key&gt;</code> 或 <code>X-API-Key</code> 请求头。<br />
              服务器只保存密钥的 SHA-256 哈希，明文只在创建时显示一次。<br /><br />
              <b>限流</b>：每个凭证一个令牌桶——容量 = 突发，按“频率上限/分钟”匀速补充；每次调用扣除该接口的消耗（多数为 1，较重的接口 2–3）。另有全服务器总上限（设置 → 开发者）保护行情记录。<br />
              每个响应都带 <code>X-RateLimit-Limit / Burst / Remaining / Reset / Cost</code>；超限返回 429 与 <code>Retry-After</code>（秒），MCP 工具则返回带 <code>retryAfterMs</code> 的错误结果。Agent 可调用 <code>get_usage</code>（免费）查看剩余额度。
            </InfoTip>
          </h3>
          <button class="primary add" @click="openForm(null)"><Plus :size="14" />新建</button>
        </div>
        <Loading v-if="!keys" text="加载凭证" />
        <div v-else-if="!keys.length" class="empty">
          <KeyRound :size="20" class="muted" />
          <span class="muted">还没有凭证，新建一个给 Agent 使用</span>
        </div>
        <ul v-else class="keys">
          <li v-for="k in keys" :key="k.id" class="key" :class="{ off: k.status !== 'active' }">
            <component :is="k.kind === 'token' ? Timer : KeyRound" :size="16" class="ki" />
            <div class="km">
              <div class="kn"><b>{{ k.name }}</b><span class="tag">{{ k.kind === 'token' ? 'Token' : 'API Key' }}</span>
                <span class="tag" :class="statusTag(k).c">{{ statusTag(k).t }}</span></div>
              <div class="ks"><code>{{ k.prefix }}…</code> · {{ k.ratePerMin }}/分钟 · 突发 {{ k.burst }}
                <template v-if="k.expiresAt"> · {{ k.status === 'expired' ? '已于' : '' }}{{ time(k.expiresAt, true) }}{{ k.status === 'expired' ? '过期' : ' 过期' }}</template>
                <template v-else> · 永不过期</template></div>
            </div>
            <div class="kstat k1">
              <span class="lbl">请求 / 限流</span>
              <span class="tnum">{{ k.requests }}<span class="muted"> / </span><span :class="{ warnc: k.limited }">{{ k.limited }}</span></span>
            </div>
            <div class="kstat k2">
              <span class="lbl">最近使用</span>
              <span class="tnum" :title="k.lastUsedAt ? time(k.lastUsedAt, true) : ''">{{ k.lastUsedAt ? ago(k.lastUsedAt) + '前' : '—' }}</span>
            </div>
            <div class="acts">
              <button class="icon" :aria-label="`编辑 ${k.name}`" :disabled="k.status === 'revoked'" @click="openForm(k)"><Pencil :size="14" /></button>
              <button v-if="k.status !== 'revoked'" class="icon" :aria-label="`吊销 ${k.name}`" title="吊销" @click="revoke(k)"><Ban :size="14" /></button>
              <button class="icon" :aria-label="`删除 ${k.name}`" title="删除" @click="remove(k)"><Trash2 :size="14" /></button>
            </div>
          </li>
        </ul>
      </div>

      <!-- access -->
      <aside class="side">
        <div class="panel">
          <h3>接入 <InfoTip>
            <b>REST</b>：只读 JSON 接口，完整文档见 OpenAPI（无需 Key 即可读取文档）。<br />
            <b>MCP</b>：Streamable HTTP（无状态，仅 POST JSON-RPC），工具与 REST 接口一一对应，适合 Claude Code、Cursor 等 Agent 直接调用。<br />
            只开放行情与研究数据（监控池、事实面板、规则分数、K 线、持仓量、资金费、盘口、订单流、强平、战绩）和数据新鲜度；不开放设置、推送渠道、存储与内存等系统细节，也不能修改任何东西。
          </InfoTip></h3>
          <div class="acc">
            <div class="ar"><span class="lbl">状态</span>
              <span v-if="cfg"><span class="dot" :class="cfg.enabled ? 'ok' : 'bad'"></span> {{ cfg.enabled ? '已开放' : '已关闭' }}
                <span class="muted">· 全服务器 {{ cfg.globalPerMin }}/分钟</span> <RouterLink to="/settings#dev" class="sl">设置</RouterLink></span>
              <span v-else class="muted">—</span></div>
            <CopyBlock :text="restBase()" label="REST 基础地址" />
            <CopyBlock :text="mcpUrl()" label="MCP 地址" />
            <a class="oa" href="/api/open/v1/openapi.json" target="_blank" rel="noopener"><FileJson :size="14" />OpenAPI 文档（openapi.json）</a>
            <div class="sn">
              <Segmented v-model="snip" size="sm" aria-label="配置格式" :options="[{ value: 'claude', label: 'Claude Code' }, { value: 'json', label: 'MCP JSON' }]" />
              <CopyBlock :text="snip === 'claude' ? claudeCodeSnippet(testKey || undefined) : mcpJsonSnippet(testKey || undefined)" />
            </div>
          </div>
        </div>
        <!-- request log -->
        <div class="panel log-p">
          <h3>最近请求 <InfoTip>最近 200 次外部调用（REST 与 MCP 工具，仅内存，重启清空），每 10 秒刷新。</InfoTip></h3>
          <Loading v-if="!logs" text="加载" />
          <p v-else-if="!logs.length" class="muted empty-line">还没有外部调用</p>
          <ul v-else class="logs">
            <li v-for="(l, i) in logs.slice(0, shownLogs)" :key="i" class="lg">
              <span class="lt tnum muted">{{ time(l.ts, true) }}</span>
              <span class="tag" :class="logCls(l.status)" :title="l.error">{{ l.status }}</span>
              <span class="lv tag">{{ l.via.toUpperCase() }}</span>
              <span class="le"><code>{{ l.endpoint }}</code><small v-if="l.error" class="muted"> · {{ l.error }}</small></span>
              <span class="lk muted">{{ l.keyName || '未认证' }}</span>
              <span class="lm tnum muted">{{ l.ms }}ms<template v-if="l.cost"> · {{ l.cost }}</template></span>
            </li>
          </ul>
          <button v-if="logs && logs.length > shownLogs" class="more" @click="shownLogs += 50">显示更多（共 {{ logs.length }} 条）</button>
        </div>
      </aside>

      <!-- endpoints + tester -->
      <div class="panel eps-p">
        <h3>接口 <span class="panel-sub" v-if="eps">{{ eps.endpoints.length }} 个 · REST <code>GET {{ eps.restBase }}/…</code> · MCP 工具同名</span></h3>
        <Loading v-if="!eps" text="加载接口" />
        <ul v-else class="eps">
          <li v-for="e in eps.endpoints" :key="e.name" class="ep" :class="{ open: open === e.name }">
            <button type="button" class="eh" :aria-expanded="open === e.name" @click="open = open === e.name ? null : e.name">
              <ChevronRight :size="14" class="chev" />
              <code class="path">/{{ e.path }}</code>
              <span class="sum">{{ e.summary }}</span>
              <code class="tool hide-sm">{{ e.name }}</code>
              <span v-if="e.cost && e.cost !== '1'" class="tag" title="限流消耗（令牌）">消耗 {{ e.cost }}</span>
            </button>
            <div v-if="open === e.name" class="ed">
              <p>{{ e.desc }}</p>
              <div class="table-scroll" v-if="e.params?.length"><table class="pt">
                <thead><tr><th>参数</th><th>类型</th><th>默认</th><th>说明</th></tr></thead>
                <tbody><tr v-for="p in e.params" :key="p.name">
                  <td><code>{{ p.name }}</code><b v-if="p.required || p.in === 'path'" class="req">*</b></td>
                  <td class="muted">{{ p.enum ? p.enum.join(' | ') : p.type }}<template v-if="p.max"> ({{ p.min }}–{{ p.max }})</template></td>
                  <td class="tnum">{{ p.default ?? '—' }}</td>
                  <td class="pd">{{ p.desc }}</td>
                </tr></tbody>
              </table></div>
              <div class="ea"><span class="muted">MCP 工具 <code>{{ e.name }}</code></span><button @click="tryIt(e.name)">试用 →</button></div>
            </div>
          </li>
        </ul>
        <div ref="testerEl" class="tw">
          <h4>试用</h4>
          <Tester v-if="eps" v-model:endpoint="endpoint" v-model:api-key="testKey" :endpoints="eps.endpoints" />
        </div>
      </div>

    </div>

    <KeyForm :open="formOpen" :edit="editing" :max-rate="eps?.limits.maxRatePerMin ?? 6000" :max-burst="eps?.limits.maxBurst ?? 1000"
      @close="formOpen = false" @saved="onSaved" @created="onCreated" />
  </div>
</template>

<style scoped>
.cols { display: grid; grid-template-columns: minmax(0, 1fr); grid-template-areas: "keys" "side" "eps"; gap: 12px; align-items: start; }
.keys-p { grid-area: keys; } .side { grid-area: side; display: grid; gap: 12px; min-width: 0; } .eps-p { grid-area: eps; }
@media (min-width: 1100px) {
  .cols { grid-template-columns: minmax(0, 1fr) 380px; grid-template-areas: "keys side" "eps side"; grid-template-rows: auto 1fr; }
}
.off { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 10px 14px; border-radius: var(--radius-sm); background: var(--warn-bg); color: var(--warn); font-size: var(--fs-sm); }
.banner { border-radius: var(--radius-sm); }
code { font: 12px ui-monospace, "SF Mono", Menlo, monospace; }
.ph { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 12px; }
.ph h3 { margin: 0; }
.add { display: inline-flex; align-items: center; gap: 4px; height: 28px; }
.empty { display: flex; align-items: center; justify-content: center; gap: 8px; padding: 28px 0; }
.empty-line { margin: 8px 0; font-size: var(--fs-sm); }
button.icon { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; padding: 0; background: transparent; border-color: transparent; color: var(--text-3); }
@media (hover: hover) { button.icon:hover:not(:disabled) { color: var(--text-1); border-color: var(--line-2); } }
button:disabled { opacity: .4; cursor: default; }
.more { display: block; margin: 8px auto 0; background: transparent; color: var(--text-2); }

.keys, .eps, .logs { list-style: none; margin: 0; padding: 0; }
.key { display: grid; grid-template-columns: auto minmax(0, 1fr) 96px 72px auto; align-items: center; gap: 14px; padding: 10px 4px; border-top: 1px solid var(--line); }
.key:first-child { border-top: none; }
.key.off .km, .key.off .kstat, .key.off .ki { opacity: .5; }
.ki { color: var(--text-3); }
.kn { display: flex; align-items: center; gap: 6px; min-width: 0; flex-wrap: wrap; }
.kn b { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ks { color: var(--text-2); font-size: var(--fs-sm); margin-top: 2px; overflow-wrap: anywhere; }
.kstat { display: grid; justify-items: end; line-height: 1.3; }
.kstat .lbl { font-size: var(--fs-xs); color: var(--text-3); }
.kstat > span:last-child { color: var(--text-2); font-size: var(--fs-sm); }
.warnc { color: var(--warn); }
.acts { display: flex; gap: 4px; }

.acc { display: grid; gap: 12px; }
.ar { display: flex; justify-content: space-between; gap: 8px; font-size: var(--fs-sm); flex-wrap: wrap; }
.ar .lbl { color: var(--text-3); }
.sl { margin-left: 4px; }
.oa { display: inline-flex; align-items: center; gap: 6px; font-size: var(--fs-sm); }
.sn { display: grid; gap: 8px; }

.ep { border-top: 1px solid var(--line); }
.ep:first-child { border-top: none; }
.eh {
  all: unset; box-sizing: border-box; display: flex; align-items: center; gap: 10px; width: 100%; padding: 8px 4px; cursor: pointer; min-width: 0;
}
@media (hover: hover) { .eh:hover { background: var(--bg-2); } }
.eh:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
.chev { color: var(--text-3); flex: none; transition: transform .12s; }
.ep.open .chev { transform: rotate(90deg); }
.path { color: var(--text-1); flex: none; }
.sum { color: var(--text-2); font-size: var(--fs-sm); min-width: 0; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tool { color: var(--text-3); flex: none; }
.ed { padding: 0 4px 12px 28px; display: grid; gap: 10px; }
.ed p { margin: 0; color: var(--text-2); font-size: var(--fs-sm); line-height: 1.6; }
.pt td { font-size: var(--fs-sm); vertical-align: top; }
.pt .pd { white-space: normal; min-width: 200px; color: var(--text-2); }
.req { color: var(--down); font-weight: 400; margin-left: 2px; }
.ea { display: flex; align-items: center; justify-content: space-between; gap: 8px; font-size: var(--fs-sm); }
.tw { margin-top: 14px; padding-top: 14px; border-top: 1px solid var(--line); scroll-margin-top: 16px; }
.tw h4 { margin: 0 0 10px; font-size: var(--fs-md); font-weight: 600; }

.lg { display: grid; grid-template-columns: 76px auto auto minmax(0, 1fr); grid-template-areas: "t s v e" ". k k m"; gap: 2px 8px; align-items: baseline; padding: 6px 0; border-top: 1px solid var(--line); font-size: var(--fs-sm); }
.lg:first-child { border-top: none; }
.lt { grid-area: t; font-size: var(--fs-xs); } .lg > .tag:nth-child(2) { grid-area: s; } .lv { grid-area: v; }
.le { grid-area: e; min-width: 0; overflow-wrap: anywhere; }
.lk { grid-area: k; font-size: var(--fs-xs); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.lm { grid-area: m; font-size: var(--fs-xs); text-align: right; }

@media (max-width: 700px) {
  .key { grid-template-columns: auto minmax(0, 1fr) auto; grid-template-areas: "i main acts" ". s1 s2"; row-gap: 6px; gap: 10px; }
  .ki { grid-area: i; } .km { grid-area: main; } .acts { grid-area: acts; }
  .kstat { justify-items: start; grid-auto-flow: column; gap: 6px; align-items: baseline; }
  .k1 { grid-area: s1; } .k2 { grid-area: s2; }
  .ed { padding-left: 4px; }
}
</style>

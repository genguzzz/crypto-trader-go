<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Play } from 'lucide-vue-next'
import type { DevEndpoint } from '../../api'
import CopyBlock from './CopyBlock.vue'
import { curlSnippet } from './snippets'

/** Calls the open API exactly like an agent would (Bearer key), showing status, rate-limit headers and the body. */
const props = defineProps<{ endpoints: DevEndpoint[] }>()
const selected = defineModel<string>('endpoint', { required: true })
const key = defineModel<string>('apiKey', { required: true })

const ep = computed(() => props.endpoints.find(e => e.name === selected.value) || props.endpoints[0])
const vals = ref<Record<string, string>>({})
watch(ep, e => {
  const v: Record<string, string> = {}
  for (const p of e?.params || []) v[p.name] = p.name === 'instId' ? 'BTC-USDT-SWAP' : p.name === 'ruleId' ? '' : p.default != null ? String(p.default) : ''
  vals.value = v
}, { immediate: true })

const path = computed(() => {
  const e = ep.value
  if (!e) return ''
  let p = e.path
  const qs = new URLSearchParams()
  for (const x of e.params || []) {
    const v = (vals.value[x.name] ?? '').trim()
    if (x.in === 'path') p = p.replace(`{${x.name}}`, encodeURIComponent(v))
    else if (v !== '' && String(x.default ?? '') !== v) qs.set(x.name, v)
  }
  const q = qs.toString()
  return q ? `${p}?${q}` : p
})

const res = ref<{ status: number; ms: number; headers: [string, string][]; body: string; size: number } | null>(null)
const sending = ref(false)
const LIMIT = 30_000
async function send() {
  sending.value = true
  const t0 = performance.now()
  try {
    const r = await fetch(`/api/open/v1/${path.value}`, { headers: key.value ? { Authorization: `Bearer ${key.value.trim()}` } : {} })
    const text = await r.text()
    let body = text
    try { body = JSON.stringify(JSON.parse(text), null, 2) } catch { /* not JSON */ }
    const headers = [...r.headers.entries()].filter(([k]) => k.startsWith('x-ratelimit') || k === 'retry-after')
    res.value = { status: r.status, ms: Math.round(performance.now() - t0), headers, body: body.length > LIMIT ? body.slice(0, LIMIT) + '\n… (truncated)' : body, size: text.length }
  } catch (e: any) {
    res.value = { status: 0, ms: Math.round(performance.now() - t0), headers: [], body: e.message, size: 0 }
  } finally {
    sending.value = false
  }
}
const statusCls = computed(() => (!res.value ? '' : res.value.status === 200 ? 'good' : res.value.status === 429 ? 'warn' : 'bad'))
const kb = (n: number) => (n < 1024 ? `${n} B` : `${(n / 1024).toFixed(1)} KB`)
</script>

<template>
  <div class="tester">
    <div class="row2">
      <label class="f"><span>接口</span>
        <USelect v-model="selected" :options="endpoints.map(e => ({ value: e.name, label: `/${e.path}` }))" width="100%" aria-label="接口" /></label>
      <label class="f"><span>API Key / Token</span>
        <input v-model="key" type="password" autocomplete="off" spellcheck="false" placeholder="ctg_sk_… （新建后自动填入，仅保存在本页）" /></label>
    </div>
    <div v-if="ep?.params?.length" class="params">
      <label v-for="p in ep.params" :key="p.name" class="f" :title="p.desc">
        <span>{{ p.name }}<b v-if="p.required || p.in === 'path'" class="req">*</b></span>
        <USelect v-if="p.enum || p.type === 'boolean'" v-model="vals[p.name]" size="sm" width="100%" :aria-label="p.name"
          :options="[...(p.required || p.default != null ? [] : [{ value: '', label: '—' }]), ...(p.enum || ['false', 'true']).map(o => ({ value: o, label: o }))]" />
        <input v-else v-model="vals[p.name]" :type="p.type === 'integer' ? 'number' : 'text'" :min="p.min" :max="p.max" spellcheck="false" />
      </label>
    </div>
    <div class="go">
      <code class="url">GET /api/open/v1/{{ path }}</code>
      <button class="primary" :disabled="sending || !ep" @click="send"><Play :size="13" />{{ sending ? '请求中…' : '发送' }}</button>
    </div>
    <CopyBlock :text="curlSnippet(key || undefined, path)" label="cURL" />
    <div v-if="res" class="res">
      <div class="rh">
        <span class="tag" :class="statusCls">{{ res.status || '网络错误' }}</span>
        <span class="muted tnum">{{ res.ms }} ms · {{ kb(res.size) }}</span>
        <span v-for="[k, v] in res.headers" :key="k" class="hd tnum">{{ k.replace('x-ratelimit-', '') }} <b>{{ v }}</b></span>
      </div>
      <pre class="body">{{ res.body }}</pre>
    </div>
  </div>
</template>

<style scoped>
.tester { display: grid; gap: 12px; min-width: 0; }
.row2 { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 12px; }
.params { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 10px; }
.f { display: grid; gap: 4px; min-width: 0; }
.f > span { color: var(--text-3); font-size: var(--fs-xs); }
.f input { width: 100%; min-width: 0; }
.req { color: var(--down); font-weight: 400; margin-left: 2px; }
.go { display: flex; align-items: center; gap: 10px; min-width: 0; }
.url { flex: 1; min-width: 0; font: 12px ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-2); overflow-wrap: anywhere; }
.go button { display: inline-flex; align-items: center; gap: 4px; height: 30px; flex: none; }
.res { display: grid; gap: 6px; min-width: 0; }
.rh { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; font-size: var(--fs-sm); }
.hd { color: var(--text-3); font-size: var(--fs-xs); }
.hd b { color: var(--text-1); font-weight: 600; }
.body {
  margin: 0; max-height: 420px; overflow: auto; padding: 10px; background: var(--bg-0); border: 1px solid var(--line); border-radius: var(--radius-sm);
  font: 12px/1.5 ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-2); white-space: pre-wrap; overflow-wrap: anywhere;
}
@media (max-width: 700px) { .row2 { grid-template-columns: minmax(0, 1fr); } }
</style>

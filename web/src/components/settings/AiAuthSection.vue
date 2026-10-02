<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Sparkles } from 'lucide-vue-next'
import { aiApi, type AiStatus } from '../../tradeApi'
import { ago, time } from '../../fmt'
import { aiState } from '../trade/meta'
import SettingsSection from './SettingsSection.vue'
import SettingRow from './SettingRow.vue'

/** Credential for the AI review layer: an Anthropic API key or a subscription token (claude setup-token). */
const st = ref<AiStatus | null>(null)
const loadErr = ref('')
const kind = ref<'api_key' | 'oauth_token'>('oauth_token')
const value = ref('')
const busy = ref('')
const res = ref<{ ok: boolean; t: string } | null>(null)

async function load() {
  try {
    st.value = await aiApi.status()
    if (st.value.kind && !value.value) kind.value = st.value.kind
    loadErr.value = ''
  } catch (e: any) {
    loadErr.value = e.message
  }
}
onMounted(load)
const timer = window.setInterval(() => { if (!document.hidden) load() }, 15_000)
onUnmounted(() => clearInterval(timer))

const state = computed(() => (st.value ? aiState(st.value) : { t: '—', c: '' as const, d: '' }))

// model (PUT ai/model; empty = server default)
const model = ref('')
const modelErr = computed(() => (model.value && !/^[a-z0-9][a-z0-9.\-]{1,62}(\[1m\])?$/.test(model.value.trim()) ? '模型名只能含小写字母、数字、点和横线' : ''))
async function saveModel() {
  if (modelErr.value) return
  busy.value = 'model'
  res.value = null
  try { st.value = await aiApi.setModel(model.value.trim()); model.value = ''; res.value = { ok: true, t: `模型已设为 ${st.value.model}` } } catch (e: any) { res.value = { ok: false, t: e.message } } finally { busy.value = '' }
}
const fmtErr = computed(() => {
  const v = value.value.trim()
  if (!v) return ''
  if (kind.value === 'api_key' && !v.startsWith('sk-ant-api')) return 'API Key 通常以 sk-ant-api 开头'
  if (kind.value === 'oauth_token' && !v.startsWith('sk-ant-oat')) return '订阅 Token 通常以 sk-ant-oat 开头'
  return ''
})
const placeholder = computed(() => {
  const s = st.value
  if (s?.configured && s.kind === kind.value && s.masked) return `已保存 ${s.masked}，留空不变`
  return kind.value === 'api_key' ? 'sk-ant-api03-…' : 'sk-ant-oat01-…'
})

async function save() {
  const v = value.value.trim()
  if (!v) return
  busy.value = 'save'
  res.value = null
  try {
    st.value = await aiApi.setCredential(kind.value, v)
    value.value = ''
    res.value = { ok: true, t: '已保存，正在验证…' }
    busy.value = ''
    await test()
  } catch (e: any) {
    res.value = { ok: false, t: e.message }
  } finally {
    busy.value = ''
  }
}
async function test() {
  busy.value = 'test'
  res.value = null
  try {
    const r = await aiApi.test()
    res.value = r.ok
      ? { ok: true, t: `验证通过${r.model ? ' · ' + r.model : ''}${r.latencyMs ? ` · ${(r.latencyMs / 1000).toFixed(1)}s` : ''}${r.costUsd ? ` · $${r.costUsd.toFixed(4)}` : ''}${r.reply ? ` · 回复：${r.reply.slice(0, 60)}` : ''}` }
      : { ok: false, t: r.error || '验证失败' }
  } catch (e: any) {
    res.value = { ok: false, t: e.message }
  } finally {
    busy.value = ''
    load()
  }
}
async function revoke() {
  if (!confirm('撤销 AI 凭证？之后“AI 审核”模式的信号都会被跳过，直到重新授权。')) return
  busy.value = 'revoke'
  try { await aiApi.revoke(); res.value = { ok: true, t: '已撤销' }; await load() } catch (e: any) { res.value = { ok: false, t: e.message } } finally { busy.value = '' }
}
</script>

<template>
  <SettingsSection id="ai" title="AI 授权" :icon="Sparkles">
    <template #tip>
      “AI 审核”执行方式需要一个 Claude 凭证，二选一：<br />
      <b>订阅 Token</b>：在<b>你自己的电脑</b>上安装 Claude Code 后运行 <code>claude setup-token</code>，按提示在浏览器登录你的 Claude 订阅，终端会打印一个长期有效的 Token（<code>sk-ant-oat…</code>），粘贴到这里。用量计入你的订阅额度。<br />
      <b>Anthropic API Key</b>：在 console.anthropic.com 创建（<code>sk-ant-api…</code>），按调用量计费。<br /><br />
      本应用<b>不提供</b> claude.ai 登录，也不会经手你的账号密码；凭证只保存在服务器数据库，页面只显示末 4 位，只交给隔离的 AI 进程使用。<br />
      <b>AI 进程</b>：独立运行的 AI 审核进程（Claude Agent SDK）是否在线并就绪；离线时 AI 审核的信号按“跳过”处理。<br />
      <b>代理</b>：AI 进程只经服务器代理访问 Anthropic。代理不可用时 AI 审核<b>暂停</b>（信号按跳过处理），<b>绝不直连</b>。<br />
      <b>费用</b>：按 API 价格计算；用订阅 Token 时是估算，实际计入订阅额度。<br />
      <b>模型</b>：留空恢复服务器默认。
    </template>
    <template #aside><span class="tag" :class="state.c">{{ state.t }}</span></template>
    <p v-if="loadErr" class="down small pad">读取失败：{{ loadErr }}</p>
    <template v-else>
      <SettingRow label="状态">
        <template #desc>
          <template v-if="st?.configured">{{ st.kind === 'api_key' ? 'Anthropic API Key' : '订阅 Token' }} {{ st.masked }}</template>
          <template v-else>尚未授权</template>
        </template>
        <div class="stat">
          <span class="si" :title="st?.worker ? `worker ${st.worker.version}${st.worker.sdk ? ' · SDK ' + st.worker.sdk : ''}${st.worker.cli ? ' · CLI ' + st.worker.cli : ''}` : ''">
            <span class="dot" :class="!st?.workerOnline ? 'bad' : st.workerReady ? 'ok' : 'warn'"></span>AI 进程{{ !st?.workerOnline ? '离线' : st.workerReady ? '就绪' : '未就绪' }}</span>
          <span class="si"><span class="dot" :class="st?.workerOnline ? (st.proxyOk ? 'ok' : 'bad') : ''"></span>代理{{ !st?.workerOnline ? '未知' : st.proxyOk ? '正常' : '不可用' }}</span>
          <span class="si" :title="st?.lastCallAt ? time(st.lastCallAt, true) : ''">最近调用 <b class="tnum">{{ st?.lastCallAt ? ago(st.lastCallAt) + '前' : '—' }}</b></span>
          <span class="si">今日 <b class="tnum">{{ st?.callsToday ?? 0 }}</b> 次 · <b class="tnum">${{ (st?.costToday ?? 0).toFixed(2) }}</b></span>
          <span v-if="st?.pendingProposals" class="si"><RouterLink to="/trading/ai">{{ st.pendingProposals }} 条参数建议待批 →</RouterLink></span>
          <span v-if="st?.workerOnline && !st.proxyOk" class="err">代理不可用，AI 审核已暂停（不会直连）<template v-if="st.proxyError">：{{ st.proxyError }}</template></span>
          <span v-if="st?.credentialError" class="err">凭证问题：{{ st.credentialError }}</span>
          <span v-if="st?.lastError" class="err">最近一次调用失败：{{ st.lastError }}</span>
          <span v-if="st?.storage === 'plain'" class="err warn">凭证未加密保存（服务器未设置 CTG_SECRET_KEY）</span>
        </div>
      </SettingRow>
      <SettingRow label="模型" for-id="ai-m" :changed="!!model" :error="modelErr">
        <template #desc>当前 <code>{{ st?.model || '—' }}</code></template>
        <div class="line">
          <input id="ai-m" v-model="model" class="w2" spellcheck="false" autocomplete="off" :placeholder="st?.model || 'claude-…'" @keydown.enter="saveModel" />
          <button type="button" :disabled="!!busy || !!modelErr || !model.trim()" @click="saveModel">{{ busy === 'model' ? '保存中…' : '保存' }}</button>
        </div>
      </SettingRow>
      <SettingRow label="凭证类型">
        <Segmented v-model="kind" size="sm" aria-label="凭证类型" :options="[{ value: 'oauth_token', label: '订阅 Token' }, { value: 'api_key', label: 'Anthropic API Key' }]" />
      </SettingRow>
      <SettingRow :label="kind === 'api_key' ? 'API Key' : '订阅 Token'" for-id="ai-v" :changed="!!value" :error="fmtErr">
        <template #desc><template v-if="kind === 'oauth_token'">在自己电脑上运行 <code>claude setup-token</code> 获得</template><template v-else>console.anthropic.com → API Keys</template></template>
        <input id="ai-v" v-model="value" class="w" type="password" autocomplete="off" spellcheck="false" :placeholder="placeholder" @keydown.enter="save" />
      </SettingRow>
    </template>
    <template #footer>
      <div class="acts">
        <button type="button" class="primary" :disabled="!!busy || !value.trim()" @click="save">{{ busy === 'save' ? '保存中…' : '保存并验证' }}</button>
        <button type="button" :disabled="!!busy || !st?.configured" @click="test">{{ busy === 'test' ? '验证中…' : '测试' }}</button>
        <button v-if="st?.configured" type="button" class="ghost" :disabled="!!busy" @click="revoke">撤销</button>
        <span v-if="res" class="res" :class="res.ok ? 'up' : 'down'">{{ res.t }}</span>
      </div>
    </template>
  </SettingsSection>
</template>

<style scoped>
.w { width: 300px; max-width: 100%; height: 32px; }
.stat { display: flex; flex-wrap: wrap; gap: 4px 14px; justify-content: flex-end; font-size: var(--fs-sm); color: var(--text-2); max-width: 420px; }
.si { display: inline-flex; align-items: center; gap: 6px; white-space: nowrap; }
.si b { color: var(--text-1); font-weight: 600; }
.line { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; justify-content: flex-end; }
.w2 { width: 200px; max-width: 100%; height: 32px; }
.line button { height: 32px; padding: 0 14px; }
.err.warn { color: var(--warn); }
.err { flex-basis: 100%; text-align: right; font-size: var(--fs-xs); color: var(--down); overflow-wrap: anywhere; }
code { font-size: var(--fs-xs); color: var(--text-2); }
.acts { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.acts button { height: 32px; padding: 0 16px; }
button.ghost { background: transparent; color: var(--text-3); }
button:disabled { opacity: .45; cursor: default; }
.res { font-size: var(--fs-sm); overflow-wrap: anywhere; }
.small { font-size: var(--fs-sm); }
.pad { padding: 12px 0; }
@media (max-width: 700px) { .stat, .line { justify-content: flex-start; } .err { text-align: left; } }
</style>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Sparkles } from 'lucide-vue-next'
import { aiApi, type AiStatus } from '../../tradeApi'
import { ago, time } from '../../fmt'
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

const state = computed(() => {
  const s = st.value
  if (!s) return { t: '—', c: '' }
  if (!s.configured) return { t: '未授权', c: '' }
  if (s.lastError) return { t: '失效', c: 'bad' }
  return { t: '正常', c: 'good' }
})
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
    res.value = r.ok ? { ok: true, t: `验证通过${r.model ? ' · ' + r.model : ''}${r.latencyMs ? ` · ${(r.latencyMs / 1000).toFixed(1)}s` : ''}` } : { ok: false, t: r.error || '验证失败' }
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
      <b>失效</b>：最近一次调用失败（Token 过期、被撤销、额度用尽等），重新粘贴或点“测试”。<br />
      <b>AI 进程</b>：独立运行的 AI 审核进程是否在线；离线时 AI 审核的信号按“跳过”处理。
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
          <span class="si"><span class="dot" :class="st?.workerOnline ? 'ok' : 'bad'"></span>AI 进程{{ st?.workerOnline ? '在线' : '离线' }}</span>
          <span class="si" :title="st?.lastCallAt ? time(st.lastCallAt, true) : ''">最近调用 <b class="tnum">{{ st?.lastCallAt ? ago(st.lastCallAt) + '前' : '—' }}</b></span>
          <span class="si">今日费用 <b class="tnum">${{ (st?.costToday ?? 0).toFixed(2) }}</b></span>
          <span v-if="st?.lastError" class="err">{{ st.lastError }}</span>
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
.err { flex-basis: 100%; text-align: right; font-size: var(--fs-xs); color: var(--down); overflow-wrap: anywhere; }
code { font-size: var(--fs-xs); color: var(--text-2); }
.acts { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.acts button { height: 32px; padding: 0 16px; }
button.ghost { background: transparent; color: var(--text-3); }
button:disabled { opacity: .45; cursor: default; }
.res { font-size: var(--fs-sm); overflow-wrap: anywhere; }
.small { font-size: var(--fs-sm); }
.pad { padding: 12px 0; }
@media (max-width: 700px) { .stat { justify-content: flex-start; } .err { text-align: left; } }
</style>

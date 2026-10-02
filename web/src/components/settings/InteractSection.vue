<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { MessagesSquare, UserPlus } from 'lucide-vue-next'
import { interactApi, type InteractConfig } from '../../tradeApi'
import { ago } from '../../fmt'
import SettingsSection from './SettingsSection.vue'
import SettingRow from './SettingRow.vue'
import ChipGroup from '../ui/ChipGroup.vue'
import Switch from '../ui/Switch.vue'
import { eventKinds } from '../trade/meta'

/**
 * Two-way interaction channel for trading (Telegram bot: notifications, confirmations, commands).
 * The token is never returned: the input starts empty with the masked value as placeholder; saving
 * an empty token keeps the saved one. Saves apply immediately (not part of the page save bar).
 */
const cfg = ref<InteractConfig | null>(null)
const loadErr = ref('')
const form = reactive({ enabled: false, token: '', users: '', kinds: [] as string[], quiet: false })
const busy = ref('')
const res = ref<{ ok: boolean; t: string } | null>(null)

const parseUsers = (s: string) => s.split(/[\s,，]+/).filter(Boolean).map(Number)
const usersErr = computed(() => (parseUsers(form.users).some(n => !Number.isInteger(n) || n <= 0) ? '用户 ID 是正整数，用逗号分隔' : ''))
const kindOpts = computed(() => (cfg.value?.allKinds?.length ? cfg.value.allKinds.map(k => ({ value: k.kind, label: k.label })) : eventKinds))
const allKeys = () => kindOpts.value.map(k => k.value)
function reset(c: InteractConfig) {
  cfg.value = c
  form.enabled = c.telegram.enabled
  form.token = ''
  form.users = (c.telegram.allowUserIds || []).join(', ')
  form.kinds = c.kinds?.length ? [...c.kinds] : allKeys() // empty = all
  form.quiet = c.quiet
}
async function load(first = false) {
  try {
    const c = await interactApi.config()
    if (first || !dirty.value) reset(c)
    else cfg.value = { ...cfg.value!, status: c.status } // keep edits, refresh status
    loadErr.value = ''
  } catch (e: any) {
    loadErr.value = e.message
  }
}
onMounted(() => load(true))
const timer = window.setInterval(() => { if (!document.hidden && cfg.value) load() }, 15_000)
onUnmounted(() => clearInterval(timer))

const sameSet = (a: string[], b: string[]) => [...a].sort().join() === [...b].sort().join()
const savedKinds = computed(() => (cfg.value?.kinds?.length ? cfg.value.kinds : allKeys()))
const changed = computed(() => {
  const c = cfg.value
  if (!c) return {} as Record<string, boolean>
  return {
    enabled: form.enabled !== c.telegram.enabled, token: !!form.token, quiet: form.quiet !== c.quiet,
    users: parseUsers(form.users).join() !== (c.telegram.allowUserIds || []).join(), kinds: !sameSet(form.kinds, savedKinds.value),
  } as Record<string, boolean>
})
const dirty = computed(() => Object.values(changed.value).some(Boolean))
async function save() {
  if (!cfg.value || usersErr.value) return false
  busy.value = 'save'
  res.value = null
  try {
    const all = sameSet(form.kinds, allKeys())
    reset(await interactApi.saveConfig({
      telegram: { enabled: form.enabled, botToken: form.token, configured: cfg.value.telegram.configured, allowUserIds: parseUsers(form.users) },
      kinds: all ? [] : form.kinds, quiet: form.quiet,
    }))
    res.value = { ok: true, t: '已保存' }
    return true
  } catch (e: any) {
    res.value = { ok: false, t: e.message }
    return false
  } finally {
    busy.value = ''
  }
}
async function test() {
  if (dirty.value && !(await save())) return
  busy.value = 'test'
  res.value = null
  try {
    const r = await interactApi.test()
    const bad = Object.entries(r.channels || {}).filter(([, v]) => v !== 'ok').map(([k, v]) => `${k === 'telegram' ? 'Telegram' : k}：${v}`)
    res.value = r.ok ? { ok: true, t: '测试消息已发送，随后会收到一条测试确认（也会出现在“待确认”）' } : { ok: false, t: bad.join('；') || r.error || '发送失败' }
  } catch (e: any) {
    res.value = { ok: false, t: e.message }
  } finally {
    busy.value = ''
  }
}
function allow(id: number) {
  const ids = parseUsers(form.users)
  if (!ids.includes(id)) form.users = [...ids, id].join(', ')
}

const botState = computed(() => {
  const s = cfg.value?.status
  if (!cfg.value?.telegram.configured) return { t: '未配置', c: '' }
  switch (s?.state) {
    case 'ok': return { t: s.bot ? '@' + s.bot.replace(/^@/, '') : '运行中', c: 'good' }
    case 'error': return { t: '出错', c: 'bad' }
    case 'starting': return { t: '启动中', c: 'warn' }
    case 'disabled': return { t: '未启用', c: '' }
    default: return { t: '已停止', c: '' }
  }
})
</script>

<template>
  <SettingsSection id="interact" title="交互通道" :icon="MessagesSquare">
    <template #tip>
      交易的<b>双向</b>通道：成交、止损、熔断等事件推送给你；开启人工确认的信号在 Telegram 里用按钮回答（执行 / 半仓 / 跳过），也可以用命令查看持仓、暂停策略。网页“交易 → 待确认”是同一套确认，任一处回答即可。<br /><br />
      <b>Bot Token</b>：在 @BotFather 新建一个<b>专用</b>机器人得到。不要和“监控”的推送机器人共用：这里会持续轮询消息，同一个 Token 只能有一个轮询者。<br />
      <b>允许的用户 ID</b>：只有这些 Telegram 用户能收到消息、确认和下命令，其他人的消息一律忽略。不知道 ID：保存 Token 并启用后，在私聊里给机器人发 /start，会出现在下方“陌生人”里，一键加入。<br />
      <b>推送的事件</b>：未选的事件只记录不推送；需要确认的信号无论如何都会推送。<b>静音</b>：除确认、止损、强平、熔断外，消息不响铃。<br />
      Token 只保存在服务器数据库，页面只显示末 4 位；留空保存 = 不修改。
    </template>
    <template #aside><span v-if="cfg" class="tag" :class="botState.c">{{ botState.t }}</span></template>
    <p v-if="loadErr" class="down small pad">读取失败：{{ loadErr }}</p>
    <template v-else-if="cfg">
      <SettingRow label="启用 Telegram" :changed="changed.enabled">
        <template #desc>
          <template v-if="cfg.status?.state === 'error' && cfg.status.lastError"><span class="down">{{ cfg.status.lastError }}</span></template>
          <template v-else-if="cfg.status?.lastPollAt">最近轮询 {{ ago(cfg.status.lastPollAt) }}前 · 接收人 {{ cfg.status.recipients }}<template v-if="cfg.status.backlog"> · 待发 {{ cfg.status.backlog }}</template></template>
          <template v-else>关闭后不推送也不接收命令，网页确认照常</template>
        </template>
        <Switch v-model="form.enabled" aria-label="启用 Telegram" />
      </SettingRow>
      <SettingRow label="Telegram Bot Token" desc="@BotFather 新建的专用机器人" for-id="ix-tok" :changed="changed.token">
        <input id="ix-tok" v-model="form.token" class="w" type="password" autocomplete="off" spellcheck="false" :placeholder="cfg.telegram.botToken ? `已保存 ${cfg.telegram.botToken}，留空不变` : '123456789:AA…'" />
      </SettingRow>
      <SettingRow label="允许的用户 ID" desc="只有这些 Telegram 用户能确认和下命令（最多 10 个）" for-id="ix-u" :error="usersErr" :changed="changed.users">
        <input id="ix-u" v-model="form.users" class="w" inputmode="numeric" spellcheck="false" placeholder="123456789, 987654321" />
        <div v-if="cfg.status?.strangers?.length" class="str">
          <span class="muted">陌生人：</span>
          <button v-for="s in cfg.status.strangers" :key="s.id" type="button" class="fchip" :title="`${ago(s.at)}前发来消息`" @click="allow(s.id)"><UserPlus :size="12" />{{ s.name || s.id }} <span class="muted tnum">{{ s.id }}</span></button>
        </div>
      </SettingRow>
      <SettingRow label="推送的事件" :changed="changed.kinds" :error="form.kinds.length ? '' : '至少选择一种（确认消息总会推送）'">
        <template #desc>未选的只记录不推送 · <button type="button" class="link" @click="form.kinds = allKeys()">全选</button></template>
        <ChipGroup v-model="form.kinds" :options="kindOpts" aria-label="推送的事件" class="kinds" />
      </SettingRow>
      <SettingRow label="静音" desc="除确认、止损、强平、熔断外不响铃" :changed="changed.quiet">
        <Switch v-model="form.quiet" aria-label="静音" />
      </SettingRow>
    </template>
    <template #footer>
      <div class="acts">
        <button type="button" class="primary" :disabled="!!busy || !dirty || !!usersErr || !form.kinds.length" @click="save">{{ busy === 'save' ? '保存中…' : '保存' }}</button>
        <button type="button" :disabled="!!busy || (!cfg?.telegram.configured && !form.token)" @click="test">{{ busy === 'test' ? '发送中…' : '发送测试' }}</button>
        <span v-if="res" class="res" :class="res.ok ? 'up' : 'down'">{{ res.t }}</span>
      </div>
    </template>
  </SettingsSection>
</template>

<style scoped>
.w { width: 300px; max-width: 100%; height: 32px; }
.kinds { justify-content: flex-end; max-width: 420px; }
.str { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; justify-content: flex-end; font-size: var(--fs-xs); max-width: 420px; }
.acts { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.acts button { height: 32px; padding: 0 16px; }
button:disabled { opacity: .45; cursor: default; }
button.link { border: none; background: none; padding: 0; color: var(--accent); font-size: inherit; }
.res { font-size: var(--fs-sm); overflow-wrap: anywhere; }
.small { font-size: var(--fs-sm); }
.pad { padding: 12px 0; }
@media (max-width: 700px) { .kinds, .str { justify-content: flex-start; } }
</style>

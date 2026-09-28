<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { Send, Webhook } from 'lucide-vue-next'
import { api, type AlertChannels } from '../../api'

/**
 * Delivery channel settings. The server never returns secrets: inputs start empty and show the
 * masked saved value as a placeholder; saving an empty field keeps the saved secret.
 */
const props = defineProps<{ channels: AlertChannels | null }>()
const emit = defineEmits<{ update: [AlertChannels] }>()

type Ch = 'telegram' | 'webhook'
const form = reactive({ botToken: '', chatId: '', url: '', secret: '' })
const busy = ref<Record<Ch, string>>({ telegram: '', webhook: '' })
const result = ref<Record<Ch, { ok: boolean; t: string } | null>>({ telegram: null, webhook: null })

watch(() => props.channels, c => { if (c) form.chatId = c.telegram.chatId }, { immediate: true })

const dirty = (ch: Ch) => ch === 'telegram'
  ? !!form.botToken || form.chatId !== (props.channels?.telegram.chatId || '')
  : !!form.url || !!form.secret

async function save(ch: Ch) {
  busy.value[ch] = 'save'
  result.value[ch] = null
  try {
    const out = await api.saveAlertChannels({
      telegram: { configured: false, botToken: form.botToken, chatId: form.chatId },
      webhook: { configured: false, url: form.url, secret: form.secret },
    })
    form.botToken = form.url = form.secret = ''
    emit('update', out)
    result.value[ch] = { ok: true, t: '已保存' }
    return true
  } catch (e: any) {
    result.value[ch] = { ok: false, t: e.message }
    return false
  } finally {
    busy.value[ch] = ''
  }
}

async function test(ch: Ch) {
  if (dirty(ch) && !(await save(ch))) return
  busy.value[ch] = 'test'
  result.value[ch] = null
  try {
    const r = await api.testAlertChannel(ch)
    result.value[ch] = r.ok ? { ok: true, t: '测试消息已发送' } : { ok: false, t: r.error || '发送失败' }
  } catch (e: any) {
    result.value[ch] = { ok: false, t: e.message }
  } finally {
    busy.value[ch] = ''
  }
}

async function clear(ch: Ch) {
  if (!confirm(`清除 ${ch === 'telegram' ? 'Telegram' : 'Webhook'} 配置？`)) return
  try {
    emit('update', await api.clearAlertChannel(ch))
    result.value[ch] = null
  } catch (e: any) {
    result.value[ch] = { ok: false, t: e.message }
  }
}
const ph = (masked: string, fallback: string) => (masked ? `已保存 ${masked}，留空不变` : fallback)
</script>

<template>
  <div class="panel">
    <h3>推送渠道 <InfoTip>
      <b>Telegram</b>：在 @BotFather 创建机器人得到 Bot Token；先给机器人发一条消息（群组需把机器人拉进群），再用 getUpdates 或 @userinfobot 查 Chat ID（群组为负数）。同一分钟的多条提醒合并成一条消息。<br /><br />
      <b>Webhook</b>：每条提醒 POST 一次 JSON：<code>{ruleId, rule, instId, type, value, message, ts}</code>（测试消息带 <code>test: true</code>）。填了密钥会放在请求头 <code>X-CTG-Secret</code>，接收端据此校验。<br /><br />
      <b>安全</b>：Token、密钥和 Webhook 地址只保存在服务器数据库，页面只显示末 4 位；输入框留空保存 = 不修改。发送失败会重试 3 次（网络错误 / 429 / 5xx），结果记在“最近触发”。
    </InfoTip></h3>

    <section class="ch">
      <div class="ch-h">
        <Send :size="15" class="ic" /><b>Telegram</b>
        <span class="tag" :class="channels?.telegram.configured ? 'good' : ''">{{ channels?.telegram.configured ? '已配置' : '未配置' }}</span>
      </div>
      <label class="f"><span>Bot Token</span>
        <input v-model="form.botToken" type="password" autocomplete="off" spellcheck="false" :placeholder="ph(channels?.telegram.botToken || '', '123456789:AA…')" /></label>
      <label class="f"><span>Chat ID</span>
        <input v-model="form.chatId" spellcheck="false" placeholder="123456789 / -100… / @channel" /></label>
      <div class="acts">
        <button class="primary" :disabled="!!busy.telegram || !dirty('telegram')" @click="save('telegram')">{{ busy.telegram === 'save' ? '保存中…' : '保存' }}</button>
        <button :disabled="!!busy.telegram || (!channels?.telegram.configured && !dirty('telegram'))" @click="test('telegram')">{{ busy.telegram === 'test' ? '发送中…' : '发送测试' }}</button>
        <button v-if="channels?.telegram.configured" class="ghost" @click="clear('telegram')">清除</button>
        <span v-if="result.telegram" class="res" :class="result.telegram.ok ? 'up' : 'down'">{{ result.telegram.t }}</span>
      </div>
    </section>

    <section class="ch">
      <div class="ch-h">
        <Webhook :size="15" class="ic" /><b>Webhook</b>
        <span class="tag" :class="channels?.webhook.configured ? 'good' : ''">{{ channels?.webhook.configured ? '已配置' : '未配置' }}</span>
      </div>
      <label class="f"><span>地址</span>
        <input v-model="form.url" type="url" autocomplete="off" spellcheck="false" :placeholder="ph(channels?.webhook.url || '', 'https://…')" /></label>
      <label class="f"><span>密钥（可选）</span>
        <input v-model="form.secret" type="password" autocomplete="off" :placeholder="ph(channels?.webhook.secret || '', '请求头 X-CTG-Secret')" /></label>
      <div class="acts">
        <button class="primary" :disabled="!!busy.webhook || !dirty('webhook')" @click="save('webhook')">{{ busy.webhook === 'save' ? '保存中…' : '保存' }}</button>
        <button :disabled="!!busy.webhook || (!channels?.webhook.configured && !dirty('webhook'))" @click="test('webhook')">{{ busy.webhook === 'test' ? '发送中…' : '发送测试' }}</button>
        <button v-if="channels?.webhook.configured" class="ghost" @click="clear('webhook')">清除</button>
        <span v-if="result.webhook" class="res" :class="result.webhook.ok ? 'up' : 'down'">{{ result.webhook.t }}</span>
      </div>
    </section>
  </div>
</template>

<style scoped>
.ch { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; padding: 12px 0; border-top: 1px solid var(--line); }
.ch:first-of-type { border-top: none; padding-top: 0; }
.ch-h { display: flex; align-items: center; gap: 8px; }
.ch-h .ic { color: var(--text-3); }
.ch-h .tag { margin-left: auto; }
.f { display: grid; gap: 4px; min-width: 0; }
.f > span { color: var(--text-3); font-size: var(--fs-xs); }
.f input { width: 100%; min-width: 0; }
.acts { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
button.ghost { background: transparent; color: var(--text-3); }
.res { font-size: var(--fs-sm); overflow-wrap: anywhere; }
code { font-size: var(--fs-xs); color: var(--text-2); }
</style>

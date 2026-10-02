<script setup lang="ts">
import { ref } from 'vue'
import { KeyRound } from 'lucide-vue-next'
import { adminKeySet, clearAdminKey, setAdminKey } from '../../composables/adminKey'
import SettingsSection from './SettingsSection.vue'
import SettingRow from './SettingRow.vue'

/** Owner key for state-changing trading / interaction / AI requests, stored in this browser only. */
const value = ref('')
const msg = ref('')
function save() {
  const v = value.value.trim()
  if (!v) return
  setAdminKey(v, true)
  value.value = ''
  msg.value = '已保存在本设备'
}
function clear() {
  clearAdminKey()
  msg.value = '已清除'
}
</script>

<template>
  <SettingsSection id="admin" title="管理员密钥" :icon="KeyRound">
    <template #tip>
      <b>模拟盘不需要密钥</b>：建账户、回测、启停策略、平仓、回答确认、批准 AI 建议都可以直接操作。<br />
      在登录功能（#1）上线前，服务器只用<b>管理员密钥</b>保护存放凭证和会花钱的操作：保存交互通道（Telegram Token）、发送测试、保存 / 撤销 AI 凭证、修改模型、AI 测试和提炼经验。<br />
      密钥由服务器运维设置，这里只是把它保存在<b>本设备浏览器</b>（localStorage），请求时放在请求头里发送；不保存也可以，操作时会弹窗询问。<br />
      公共电脑上不要保存；用完点“清除”。
    </template>
    <template #aside><span class="tag" :class="adminKeySet ? 'good' : ''">{{ adminKeySet ? '本设备已保存' : '未保存' }}</span></template>
    <SettingRow label="管理员密钥" desc="只保存在本浏览器，用于修改通道与 AI 凭证、AI 测试" for-id="adm-k" :changed="!!value">
      <div class="line">
        <input id="adm-k" v-model="value" class="w" type="password" autocomplete="off" spellcheck="false" :placeholder="adminKeySet ? '已保存，输入新值可替换' : '管理员密钥'" @keydown.enter="save" />
        <button type="button" class="primary" :disabled="!value.trim()" @click="save">保存</button>
        <button v-if="adminKeySet" type="button" class="ghost" @click="clear">清除</button>
      </div>
      <span v-if="msg" class="muted small">{{ msg }}</span>
    </SettingRow>
  </SettingsSection>
</template>

<style scoped>
.line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; justify-content: flex-end; }
.w { width: 220px; max-width: 100%; height: 32px; }
.line button { height: 32px; padding: 0 14px; }
button.ghost { background: transparent; color: var(--text-3); }
button:disabled { opacity: .45; cursor: default; }
.small { font-size: var(--fs-xs); }
@media (max-width: 700px) { .line { justify-content: flex-start; } }
</style>

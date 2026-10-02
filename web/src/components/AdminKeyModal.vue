<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { KeyRound } from 'lucide-vue-next'
import Modal from './Modal.vue'
import Switch from './ui/Switch.vue'
import { adminPrompt, resolveAdminKey, setAdminKey } from '../composables/adminKey'

/** Owner-key dialog opened by a 401 admin_required from a trading / interaction / AI action. */
const value = ref('')
const remember = ref(true)
const input = ref<HTMLInputElement>()
watch(() => adminPrompt.open, o => { if (o) { value.value = ''; nextTick(() => input.value?.focus()) } })

function submit() {
  const k = value.value.trim()
  if (!k) return
  setAdminKey(k, remember.value)
  resolveAdminKey(k)
}
</script>

<template>
  <Modal :open="adminPrompt.open" title="输入管理员密钥" @close="resolveAdminKey(null)">
    <form class="ak" @submit.prevent="submit">
      <div class="ak-h"><KeyRound :size="16" class="muted" /><span class="sub">修改凭证与 AI 调用需要管理员密钥</span>
        <InfoTip>模拟盘操作不需要密钥。在登录功能（#1）上线前，服务器只用管理员密钥保护存放凭证和会花钱的操作（交互通道配置与测试、AI 凭证、模型、AI 测试与提炼）。密钥由服务器运维设置，只保存在本设备浏览器里，可在“设置 → 管理员密钥”里清除。</InfoTip>
      </div>
      <input ref="input" v-model="value" type="password" autocomplete="current-password" spellcheck="false" placeholder="管理员密钥" aria-label="管理员密钥" />
      <div class="rm"><Switch v-model="remember" aria-label="在此设备上记住" /> 在此设备上记住</div>
      <div v-if="adminPrompt.error" class="down err">{{ adminPrompt.error }}</div>
      <div class="acts">
        <button type="button" @click="resolveAdminKey(null)">取消</button>
        <button type="submit" class="primary" :disabled="!value.trim()">确定并重试</button>
      </div>
    </form>
  </Modal>
</template>

<style scoped>
.ak { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; }
.ak-h { display: flex; align-items: center; gap: 8px; font-size: var(--fs-sm); }
.ak > input[type=password] { width: 100%; height: 36px; }
.rm { display: flex; align-items: center; gap: 8px; font-size: var(--fs-sm); color: var(--text-2); }
.err { font-size: var(--fs-sm); }
.acts { display: flex; justify-content: flex-end; gap: 8px; }
.acts button { height: 32px; padding: 0 16px; }
button:disabled { opacity: .45; cursor: default; }
</style>

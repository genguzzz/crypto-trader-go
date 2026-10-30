<script setup lang="ts">
// iOS app only: which binary and web bundle are running, and a manual update check (docs/ios.md).
import { onMounted, ref } from 'vue'
import { Smartphone } from 'lucide-vue-next'
import { appUpdate, applyUpdate, bundleInfo, checkUpdate, updateError, type UpdateCheck } from '../../native'
import SettingsSection from './SettingsSection.vue'
import SettingRow from './SettingRow.vue'

const info = ref<Awaited<ReturnType<typeof bundleInfo>>>(null)
const result = ref<UpdateCheck | null>(null)
const busy = ref(false)
const RESULT: Record<UpdateCheck, string> = {
  latest: '已是最新版本',
  ready: '新版本已下载',
  binary: '需要在全能签中更新 App',
  busy: '正在检查…',
  error: '检查失败，请稍后再试',
}

async function load() { info.value = await bundleInfo().catch(() => null) }
async function check() {
  busy.value = true
  result.value = await checkUpdate()
  busy.value = false
  load()
}
onMounted(load)
</script>

<template>
  <SettingsSection id="app" title="App 版本" :icon="Smartphone">
    <template #tip>
      <b>App</b>：从全能签安装的版本，原生功能变化时才需要更新。<br />
      <b>网页版本</b>：界面和功能，打开 App 时自动在后台下载新版本，切到后台或点“立即更新”生效。
    </template>
    <SettingRow label="App" desc="全能签安装的版本 (构建号)">
      <span class="tnum">{{ info ? `${info.binary} (${info.build})` : '—' }}</span>
    </SettingRow>
    <SettingRow label="网页版本" :desc="info?.running ? '已通过热更新升级' : 'App 内置版本'">
      <span class="tnum mono">{{ info?.version ?? '—' }}</span>
    </SettingRow>
    <SettingRow label="检查更新" :desc="result ? RESULT[result] + (result === 'error' && updateError ? `（${updateError}）` : '') : '打开 App 时会自动检查'">
      <div class="line">
        <button v-if="appUpdate.ready && !appUpdate.applyLater" type="button" class="primary" :disabled="appUpdate.applying" @click="applyUpdate">{{ appUpdate.applying ? '更新中…' : '立即更新' }}</button>
        <button type="button" :disabled="busy" @click="check">{{ busy ? '检查中…' : '检查更新' }}</button>
      </div>
    </SettingRow>
  </SettingsSection>
</template>

<style scoped>
.line { display: flex; gap: 8px; }
.line button { height: 32px; padding: 0 14px; }
.mono { font-family: ui-monospace, "SF Mono", Menlo, monospace; font-size: var(--fs-sm); color: var(--text-2); }
button:disabled { opacity: .45; cursor: default; }
</style>

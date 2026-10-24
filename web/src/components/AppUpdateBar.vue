<script setup lang="ts">
// iOS app only: tells the user a new version is ready (web bundle downloaded, or a newer IPA in the
// software source). See src/native.ts and docs/ios.md.
import { RefreshCw, Download, X } from 'lucide-vue-next'
import { appUpdate, applyUpdate, dismissBinary } from '../native'
</script>

<template>
  <div v-if="appUpdate.ready || appUpdate.binary" class="upd">
    <div v-if="appUpdate.ready" class="card">
      <RefreshCw :size="18" class="ic" />
      <div class="txt">
        <b>新版本已就绪</b>
        <span v-if="appUpdate.applyLater">切到后台再回来即可完成更新</span>
        <span v-else-if="appUpdate.message">{{ appUpdate.message }}</span>
      </div>
      <button v-if="!appUpdate.applyLater" class="primary" :disabled="appUpdate.applying" @click="applyUpdate">{{ appUpdate.applying ? '更新中…' : '立即更新' }}</button>
      <button class="x" aria-label="关闭" @click="appUpdate.ready = false"><X :size="16" /></button>
    </div>
    <div v-if="appUpdate.binary" class="card">
      <Download :size="18" class="ic" />
      <div class="txt">
        <b>App 有新版本 {{ appUpdate.binary.version }}</b>
        <span>请在全能签中更新{{ appUpdate.binary.notes ? `：${appUpdate.binary.notes}` : '' }}</span>
      </div>
      <button class="x" aria-label="关闭" @click="dismissBinary"><X :size="16" /></button>
    </div>
  </div>
</template>

<style scoped>
.upd { position: fixed; left: 12px; right: 12px; bottom: 16px; z-index: 30; display: grid; gap: 8px; pointer-events: none; }
.card {
  pointer-events: auto; display: flex; align-items: center; gap: 10px; padding: 10px 10px 10px 12px;
  background: var(--bg-2); border: 1px solid var(--line-2); border-radius: var(--radius); box-shadow: 0 6px 24px rgba(0, 0, 0, .5);
}
.ic { flex: none; color: var(--accent); }
.txt { flex: 1; min-width: 0; display: grid; gap: 2px; font-size: var(--fs-sm); color: var(--text-2); }
.txt b { color: var(--text-1); font-size: var(--fs-md); }
.txt span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.primary { flex: none; height: 30px; padding: 0 12px; white-space: nowrap; }
.x { flex: none; display: inline-flex; align-items: center; justify-content: center; width: 30px; height: 30px; padding: 0; background: transparent; border: none; color: var(--text-3); }
@media (max-width: 900px) {
  .upd { bottom: calc(64px + env(safe-area-inset-bottom)); }
}
</style>

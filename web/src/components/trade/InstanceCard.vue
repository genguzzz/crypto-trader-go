<script setup lang="ts">
import { Play, Pause, Square, Pencil, RotateCw } from 'lucide-vue-next'
import type { Instance, InstanceAction } from '../../tradeApi'
import { store } from '../../store'
import { ago } from '../../fmt'
import { execLabel, stateLabel, universeText } from './meta'

/** One strategy instance: state, exec mode, universe, params, and lifecycle controls. */
defineProps<{ inst: Instance; busy?: boolean; readonly?: boolean }>()
const emit = defineEmits<{ act: [InstanceAction]; edit: [] }>()
const ruleName = (id: string) => store.meta?.rules.find(r => r.id === id)?.name || id
</script>

<template>
  <div class="ic" :class="inst.state">
    <div class="h">
      <b class="nm">{{ inst.name }}</b>
      <span class="ver" :title="`代码版本 ${inst.versionId} · 配置版本 v${inst.configVersion}`">{{ inst.versionId.slice(0, 7) }} · v{{ inst.configVersion }}</span>
      <span class="tag st" :class="stateLabel[inst.state]?.c">{{ stateLabel[inst.state]?.t || inst.state }}</span>
    </div>
    <div v-if="inst.stateReason" class="reason">{{ inst.stateReason }}</div>
    <div class="meta">
      <span class="tag">{{ execLabel[inst.exec]?.t || inst.exec }}</span>
      <span class="u">{{ universeText(inst.universe, ruleName) }}</span>
    </div>
    <div v-if="Object.keys(inst.params || {}).length" class="pa">{{ Object.entries(inst.params).map(([k, v]) => `${k}=${v}`).join('  ') }}</div>
    <div class="f">
      <span class="hb" :title="'最近一次处理完事件'">{{ inst.heartbeat ? `心跳 ${ago(inst.heartbeat)}前` : '尚未运行' }}</span>
      <span v-if="!readonly" class="acts">
        <button v-if="inst.state === 'created' || inst.state === 'stopped'" type="button" :disabled="busy" title="启动" @click="emit('act', 'start')"><Play :size="13" />启动</button>
        <button v-if="inst.state === 'paused' || inst.state === 'faulted'" type="button" :disabled="busy" title="恢复" @click="emit('act', 'resume')"><RotateCw :size="13" />恢复</button>
        <button v-if="inst.state === 'running'" type="button" :disabled="busy" title="暂停" @click="emit('act', 'pause')"><Pause :size="13" />暂停</button>
        <button v-if="inst.state === 'running' || inst.state === 'paused' || inst.state === 'faulted'" type="button" :disabled="busy" title="停止" @click="emit('act', 'stop')"><Square :size="12" />停止</button>
        <button type="button" :disabled="busy" title="修改参数" @click="emit('edit')"><Pencil :size="13" />参数</button>
      </span>
    </div>
  </div>
</template>

<style scoped>
.ic { display: grid; gap: 6px; padding: 10px 0; border-top: 1px solid var(--line); min-width: 0; }
.ic:first-child { border-top: none; padding-top: 0; }
.h { display: flex; align-items: center; gap: 8px; min-width: 0; }
.nm { font-weight: 600; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ver { font: var(--fs-xs) ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-3); white-space: nowrap; }
.st { margin-left: auto; }
.reason { font-size: var(--fs-xs); color: var(--warn); overflow-wrap: anywhere; }
.faulted .reason { color: var(--down); }
.meta { display: flex; align-items: center; gap: 8px; min-width: 0; font-size: var(--fs-sm); color: var(--text-2); }
.u { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pa { font: var(--fs-xs) ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-3); overflow-wrap: anywhere; }
.f { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.hb { font-size: var(--fs-xs); color: var(--text-3); }
.acts { display: inline-flex; gap: 4px; margin-left: auto; flex-wrap: wrap; }
.acts button { display: inline-flex; align-items: center; gap: 4px; height: 26px; padding: 0 8px; font-size: var(--fs-sm); background: transparent; }
button:disabled { opacity: .45; cursor: default; }
</style>

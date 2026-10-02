<script setup lang="ts">
import { ref, watch } from 'vue'
import { Play, Pause, Square, Pencil, RotateCw, ScrollText, Activity, ArrowUpCircle } from 'lucide-vue-next'
import { tradeApi, type Instance, type InstanceAction, type LogLine, type StrategyVersion } from '../../tradeApi'
import { store } from '../../store'
import { ago } from '../../fmt'
import { execLabel, stateLabel, universeText } from './meta'
import RunLog from './RunLog.vue'

/**
 * One strategy instance: state, the strategy's own status line (status()), exec mode, universe,
 * params, its run log (ctx.log / ctx.notify / errors) and lifecycle controls.
 */
const props = defineProps<{ inst: Instance; busy?: boolean; readonly?: boolean; newer?: StrategyVersion }>()
const emit = defineEmits<{ act: [InstanceAction]; edit: []; upgrade: [StrategyVersion] }>()
const ruleName = (id: string) => store.meta?.rules.find(r => r.id === id)?.name || id

const logOpen = ref(false)
const lines = ref<LogLine[] | null>(null)
const logErr = ref('')
async function loadLog() {
  try { lines.value = await tradeApi.runLog(props.inst.id); logErr.value = '' } catch (e: any) { logErr.value = e.message }
}
function toggleLog() { logOpen.value = !logOpen.value; if (logOpen.value) loadLog() }
watch(() => props.inst.heartbeat, () => { if (logOpen.value) loadLog() }) // new events → new lines
</script>

<template>
  <div class="ic" :class="inst.state">
    <div class="h">
      <b class="nm">{{ inst.name }}</b>
      <span class="ver" :title="`代码版本 ${inst.versionId} · 配置版本 v${inst.configVersion}`">{{ inst.versionId.slice(0, 7) }} · v{{ inst.configVersion }}</span>
      <span class="tag st" :class="stateLabel[inst.state]?.c">{{ stateLabel[inst.state]?.t || inst.state }}</span>
    </div>
    <div v-if="inst.stateReason" class="reason">{{ inst.stateReason }}</div>
    <div v-if="newer && !readonly" class="up">
      <ArrowUpCircle :size="13" />策略有新版本 {{ newer.id.slice(0, 8) }}
      <RouterLink :to="`/trading/strategies/view/${newer.id}`">看代码</RouterLink>
      <button type="button" :disabled="busy" @click="emit('upgrade', newer)">升级</button>
    </div>
    <div v-if="inst.status" class="status" title="策略自己报告的状态（status()）"><Activity :size="12" />{{ inst.status }}</div>
    <div class="meta">
      <span class="tag">{{ execLabel[inst.exec]?.t || inst.exec }}</span>
      <span class="u">{{ universeText(inst.universe, ruleName) }}</span>
    </div>
    <div v-if="Object.keys(inst.params || {}).length" class="pa">{{ Object.entries(inst.params).map(([k, v]) => `${k}=${v}`).join('  ') }}</div>
    <div class="f">
      <span class="hb" :title="'最近一次处理完事件'">{{ inst.heartbeat ? `心跳 ${ago(inst.heartbeat)}前` : inst.state === 'created' ? '尚未运行' : '' }}</span>
      <span v-if="!readonly" class="acts">
        <button v-if="inst.state === 'created' || inst.state === 'stopped'" type="button" :disabled="busy" title="启动" @click="emit('act', 'start')"><Play :size="13" />启动</button>
        <button v-if="inst.state === 'paused' || inst.state === 'faulted'" type="button" :disabled="busy" title="恢复" @click="emit('act', 'resume')"><RotateCw :size="13" />恢复</button>
        <button v-if="inst.state === 'running'" type="button" :disabled="busy" title="暂停" @click="emit('act', 'pause')"><Pause :size="13" />暂停</button>
        <button v-if="inst.state === 'running' || inst.state === 'paused' || inst.state === 'faulted'" type="button" :disabled="busy" title="停止" @click="emit('act', 'stop')"><Square :size="12" />停止</button>
        <button type="button" :disabled="busy" title="修改参数" @click="emit('edit')"><Pencil :size="13" />参数</button>
      </span>
      <button type="button" class="lg" :class="{ on: logOpen }" title="运行日志：ctx.log、ctx.notify 和报错（服务重启后清空）" @click="toggleLog"><ScrollText :size="13" />日志</button>
    </div>
    <div v-if="logOpen" class="log">
      <p v-if="logErr" class="down small">{{ logErr }}</p>
      <RunLog v-else-if="lines" :lines="lines" :limit="30" empty="还没有日志：策略没调用 ctx.log / ctx.notify，或服务刚重启" />
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
.up { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; font-size: var(--fs-xs); color: var(--accent); }
.up button { height: 22px; padding: 0 8px; font-size: var(--fs-xs); }
.status { display: flex; gap: 6px; align-items: baseline; font-size: var(--fs-xs); color: var(--text-2); overflow-wrap: anywhere; }
.status svg { flex: none; color: var(--accent); transform: translateY(1px); }
.lg { display: inline-flex; align-items: center; gap: 4px; height: 26px; padding: 0 8px; font-size: var(--fs-sm); background: transparent; }
.lg.on { color: var(--accent); }
.log { max-height: 260px; overflow: auto; padding: 6px; border-radius: 6px; background: var(--bg-0); }
.small { font-size: var(--fs-xs); }
.acts button { display: inline-flex; align-items: center; gap: 4px; height: 26px; padding: 0 8px; font-size: var(--fs-sm); background: transparent; }
button:disabled { opacity: .45; cursor: default; }
</style>

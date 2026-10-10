<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ChevronLeft } from 'lucide-vue-next'
import { tradeApi, aiApi, type ExecMode, type Instance, type StrategyEntry, type StrategyVersion, type Universe } from '../../tradeApi'
import Modal from '../Modal.vue'
import Loading from '../ui/Loading.vue'
import InstanceForm from './InstanceForm.vue'
import StrategyPicker from './StrategyPicker.vue'
import { paramError, universeError } from './meta'

/**
 * Add a strategy instance to an account (pick → configure → create and start) or edit an existing
 * one (PATCH creates a new config version; signals already emitted keep their old version).
 */
const props = defineProps<{ open: boolean; accountId: number; inst?: Instance | null; startOnCreate?: boolean }>()
const emit = defineEmits<{ close: []; saved: [Instance] }>()

const version = ref<StrategyVersion | null>(null)
const params = ref<Record<string, number>>({})
const universe = ref<Universe>({ static: [] })
const exec = ref<ExecMode>('auto')
const entries = ref<StrategyEntry[] | null>(null)
const err = ref('')
const saving = ref(false)
const aiReady = ref<boolean | undefined>(undefined)

watch(() => props.open, async o => {
  if (!o) return
  err.value = ''
  version.value = null
  aiApi.status().then(s => (aiReady.value = s.configured && !s.lastError)).catch(() => {})
  if (props.inst) {
    params.value = { ...props.inst.params }
    universe.value = JSON.parse(JSON.stringify(props.inst.universe || { static: [] }))
    exec.value = props.inst.exec
    try { version.value = await tradeApi.strategy(props.inst.versionId) } catch (e: any) { err.value = e.message }
  } else {
    entries.value = null
    tradeApi.strategies().then(x => (entries.value = x)).catch(e => (err.value = e.message))
  }
})

async function pick(v: StrategyVersion) {
  try {
    const full = v.params ? v : await tradeApi.strategy(v.id)
    version.value = full
    params.value = Object.fromEntries(full.params.map(p => [p.name, p.default]))
    universe.value = JSON.parse(JSON.stringify(full.defaultUniverse?.screener || full.defaultUniverse?.static?.length ? full.defaultUniverse : { static: [] }))
    exec.value = 'auto'
  } catch (e: any) { err.value = e.message }
}

const invalid = computed(() => {
  if (!version.value) return '请选择策略'
  const ue = universeError(universe.value)
  if (ue) return ue
  for (const p of version.value.params) { const e = paramError(p, params.value[p.name]); if (e) return `${p.name}：${e}` }
  return ''
})
const changed = computed(() => !props.inst || JSON.stringify([params.value, universe.value, exec.value]) !== JSON.stringify([props.inst.params, props.inst.universe, props.inst.exec]))

async function save() {
  if (invalid.value || !version.value) return
  saving.value = true
  err.value = ''
  try {
    let out: Instance
    if (props.inst) out = await tradeApi.patchInstance(props.inst.id, { params: params.value, universe: universe.value, exec: exec.value })
    else {
      out = await tradeApi.createInstance(props.accountId, { versionId: version.value.id, params: params.value, universe: universe.value, exec: exec.value })
      if (props.startOnCreate) out = await tradeApi.instanceAction(out.id, 'start')
    }
    emit('saved', out)
  } catch (e: any) {
    err.value = e.message
  } finally {
    saving.value = false
  }
}
const title = computed(() => (props.inst ? `修改「${props.inst.name}」` : '添加策略'))
</script>

<template>
  <Modal :open="open" :title="title" wide @close="emit('close')">
    <div class="ie">
      <template v-if="!inst && !version">
        <StrategyPicker :entries="entries" :error="err" @pick="pick" />
      </template>
      <template v-else-if="version">
        <div class="vh">
          <button v-if="!inst" type="button" class="back" @click="version = null"><ChevronLeft :size="14" />换一个</button>
          <b>{{ version.name }}</b><span class="tag">{{ version.timeframe }}</span>
          <span v-if="inst" class="muted small">当前配置 v{{ inst.configVersion }}，保存后为 v{{ inst.configVersion + 1 }}</span>
        </div>
        <InstanceForm v-model:params="params" v-model:universe="universe" v-model:exec="exec" :version="version" :ai-ready="aiReady" />
        <div class="ft">
          <span class="small" :class="err || invalid ? 'down' : 'muted'">{{ err || invalid || (inst ? '已有持仓不受影响；新参数从下一根 K 线开始生效' : startOnCreate ? '创建后立即启动' : '') }}</span>
          <span class="acts">
            <button type="button" @click="emit('close')">取消</button>
            <button type="button" class="primary" :disabled="saving || !!invalid || !changed" @click="save">{{ saving ? '保存中…' : inst ? '保存为新版本' : startOnCreate ? '创建并启动' : '创建' }}</button>
          </span>
        </div>
      </template>
      <Loading v-else-if="!err" text="读取策略" />
      <p v-else class="down small">{{ err }}</p>
    </div>
  </Modal>
</template>

<style scoped>
.ie { display: grid; grid-template-columns: minmax(0, 1fr); gap: 14px; }
.vh { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.back { display: inline-flex; align-items: center; gap: 2px; height: 26px; padding: 0 8px; background: transparent; font-size: var(--fs-sm); }
.ft { position: sticky; bottom: -18px; display: flex; align-items: center; gap: 10px; flex-wrap: wrap; padding: 12px 0 4px; background: var(--bg-1); border-top: 1px solid var(--line); }
.acts { display: inline-flex; gap: 8px; margin-left: auto; }
.acts button { height: 32px; padding: 0 16px; }
button:disabled { opacity: .45; cursor: default; }
.small { font-size: var(--fs-sm); }
</style>

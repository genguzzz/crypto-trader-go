<script setup lang="ts">
import { RotateCcw } from 'lucide-vue-next'
import type { ParamSpec } from '../../tradeApi'
import NumInput from '../ui/NumInput.vue'
import { paramError } from './meta'

/** Strategy parameters generated from the strategy's ParamSpec list (min / max / step / desc). */
const props = defineProps<{ specs: ParamSpec[] }>()
const params = defineModel<Record<string, number>>({ required: true })

const err = (p: ParamSpec) => paramError(p, params.value[p.name])
void props
</script>


<template>
  <div class="pf">
    <div v-for="p in specs" :key="p.name" class="pr" :class="{ invalid: !!err(p) }">
      <div class="pl">
        <div class="pn"><code>{{ p.name }}</code><span v-if="p.aiTunable" class="tag info" title="AI 可以提出调整建议（需你批准）">AI 可调</span></div>
        <div class="pd">{{ p.desc || '—' }}<span class="rng"> · 默认 {{ p.default }}<template v-if="p.min != null || p.max != null">，范围 {{ p.min ?? '−∞' }}–{{ p.max ?? '∞' }}</template></span></div>
      </div>
      <div class="pc">
        <NumInput v-model="params[p.name]" :min="p.min" :max="p.max" :step="p.step ?? 'any'" :aria-label="p.name" width="80px" />
        <button v-if="params[p.name] !== p.default" type="button" class="rs" title="恢复默认" :aria-label="`恢复 ${p.name} 默认值`" @click="params[p.name] = p.default"><RotateCcw :size="13" /></button>
        <div v-if="err(p)" class="pe">{{ err(p) }}</div>
      </div>
    </div>
    <p v-if="!specs.length" class="muted small">此策略没有可调参数</p>
  </div>
</template>

<style scoped>
.pf { display: grid; grid-template-columns: minmax(0, 1fr); }
.pr { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 6px 16px; align-items: center; padding: 8px 0; border-bottom: 1px solid var(--line); }
.pr:last-child { border-bottom: none; }
.pn { display: flex; align-items: center; gap: 6px; }
code { font: 600 var(--fs-sm) ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-1); }
.pd { font-size: var(--fs-xs); color: var(--text-2); margin-top: 2px; }
.rng { color: var(--text-3); }
.pc { display: flex; align-items: center; gap: 4px; flex-wrap: wrap; justify-content: flex-end; }
.pe { flex-basis: 100%; text-align: right; font-size: var(--fs-xs); color: var(--down); }
.rs { display: inline-flex; padding: 4px; border: none; background: none; color: var(--text-3); }
@media (hover: hover) { .rs:hover { color: var(--text-1); } }
.small { font-size: var(--fs-xs); }
@media (max-width: 520px) {
  .pr { grid-template-columns: minmax(0, 1fr); }
  .pc { justify-content: flex-start; }
  .pe { text-align: left; }
}
</style>

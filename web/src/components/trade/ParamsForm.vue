<script setup lang="ts">
import { computed } from 'vue'
import { RotateCcw } from 'lucide-vue-next'
import type { ParamSpec } from '../../tradeApi'
import NumInput from '../ui/NumInput.vue'
import Switch from '../ui/Switch.vue'
import Segmented from '../ui/Segmented.vue'
import Select from '../ui/Select.vue'
import { paramError, paramText } from './meta'

/**
 * Strategy parameters generated from the strategy's ParamSpec list: numbers (min / max / step),
 * switches (bool), option buttons or a dropdown (choice, timeframe), in the strategy's groups.
 * Values stay numbers: bool 0/1, choice / timeframe the option index.
 */
const props = defineProps<{ specs: ParamSpec[]; readonly?: boolean }>()
const params = defineModel<Record<string, number>>({ required: true })

const err = (p: ParamSpec) => paramError(p, params.value[p.name])
const groups = computed(() => {
  const m = new Map<string, ParamSpec[]>()
  for (const p of props.specs) { const g = p.group || ''; if (!m.has(g)) m.set(g, []); m.get(g)!.push(p) }
  return [...m.entries()]
})
const options = (p: ParamSpec) => (p.options ?? []).map((o, i) => ({ value: i, label: p.labels?.[i] ?? o }))
const range = (p: ParamSpec) => (p.min != null || p.max != null) && p.type !== 'bool' && p.type !== 'choice' && p.type !== 'timeframe'
  ? `，范围 ${p.min ?? '−∞'}–${p.max ?? '∞'}` : ''
</script>

<template>
  <div class="pf">
    <section v-for="[g, ps] in groups" :key="g" class="pg">
      <h4 v-if="g && groups.length > 1" class="gt">{{ g }}</h4>
      <div v-for="p in ps" :key="p.name" class="pr" :class="{ invalid: !!err(p) }">
        <div class="pl">
          <div class="pn">
            <span class="lbl">{{ p.label || p.name }}</span>
            <code v-if="p.label" class="nm">{{ p.name }}</code>
            <span v-if="p.aiTunable" class="tag info" title="AI 可以提出调整建议（需你批准）">AI 可调</span>
          </div>
          <div class="pd">{{ p.desc || '' }}<span class="rng">{{ p.desc ? ' · ' : '' }}默认 {{ paramText(p, p.default) }}{{ range(p) }}</span></div>
        </div>
        <div class="pc">
          <template v-if="readonly"><span class="ro">{{ paramText(p, params[p.name]) }}</span></template>
          <Switch v-else-if="p.type === 'bool'" :model-value="!!params[p.name]" :aria-label="p.label || p.name"
            @update:model-value="(v: boolean) => (params[p.name] = v ? 1 : 0)" />
          <Segmented v-else-if="(p.type === 'choice' || p.type === 'timeframe') && (p.options?.length ?? 0) <= 4" v-model="params[p.name]" size="sm"
            :options="options(p)" :aria-label="p.label || p.name" />
          <Select v-else-if="p.type === 'choice' || p.type === 'timeframe'" v-model="params[p.name]" size="sm" :options="options(p)" :aria-label="p.label || p.name" />
          <NumInput v-else v-model="params[p.name]" :min="p.min" :max="p.max" :step="p.step ?? (p.type === 'int' ? 1 : 'any')" :aria-label="p.label || p.name" width="80px" />
          <button v-if="!readonly && params[p.name] !== p.default" type="button" class="rs" title="恢复默认" :aria-label="`恢复 ${p.label || p.name} 默认值`"
            @click="params[p.name] = p.default"><RotateCcw :size="13" /></button>
          <div v-if="err(p)" class="pe">{{ err(p) }}</div>
        </div>
      </div>
    </section>
    <p v-if="!specs.length" class="muted small">此策略没有可调参数</p>
  </div>
</template>

<style scoped>
.pf { display: grid; grid-template-columns: minmax(0, 1fr); gap: 4px; }
.gt { margin: 10px 0 2px; font-size: var(--fs-xs); font-weight: 600; color: var(--text-3); letter-spacing: .02em; }
.pg:first-child .gt { margin-top: 0; }
.pr { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 6px 16px; align-items: center; padding: 8px 0; border-bottom: 1px solid var(--line); }
.pg .pr:last-child { border-bottom: none; }
.pn { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.lbl { font-size: var(--fs-sm); font-weight: 600; color: var(--text-1); }
.nm { font: var(--fs-xs) ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-3); }
.pd { font-size: var(--fs-xs); color: var(--text-2); margin-top: 2px; }
.rng { color: var(--text-3); }
.pc { display: flex; align-items: center; gap: 4px; flex-wrap: wrap; justify-content: flex-end; }
.ro { font-variant-numeric: tabular-nums; color: var(--text-1); font-size: var(--fs-sm); }
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

<script setup lang="ts">
import { ref } from 'vue'
import { Plus } from 'lucide-vue-next'
import type { StrategyEntry, StrategyVersion } from '../../tradeApi'
import { day } from './meta'

/** Strategy library list for attaching a strategy: name, description, timeframe, version choice. */
const props = defineProps<{ entries: StrategyEntry[] | null; error?: string }>()
const emit = defineEmits<{ pick: [StrategyVersion] }>()
const ver = ref<Record<string, string>>({})
const chosen = (e: StrategyEntry) => e.versions.find(v => v.id === ver.value[e.name]) ?? e.latest
const verOpts = (e: StrategyEntry) => e.versions.map((v, i) => ({ value: v.id, label: `${v.id.slice(0, 8)}${v.id === e.latest.id ? '（最新）' : ''}`, desc: `${day(v.createdAt)} · v${e.versions.length - i}` }))
void props
</script>

<template>
  <div class="sp">
    <div v-if="error" class="down small">{{ error }}</div>
    <div v-else-if="!entries" class="muted small">加载策略库…</div>
    <div v-else-if="!entries.length" class="muted small">策略库为空：把策略文件放进 strategies/library 后在“策略库”页重新扫描。</div>
    <div v-for="e in entries || []" :key="e.name" class="it">
      <div class="m">
        <div class="n"><b>{{ e.name }}</b><span class="tag">{{ chosen(e).timeframe }}</span><span class="muted ver">{{ chosen(e).id.slice(0, 8) }}</span></div>
        <div class="d">{{ chosen(e).description || '—' }}</div>
        <div class="p muted">{{ (chosen(e).params || []).length }} 个参数<template v-if="(chosen(e).params || []).length">：{{ chosen(e).params.map(p => p.name).join(', ') }}</template></div>
      </div>
      <div class="a">
        <USelect v-if="e.versions.length > 1" :model-value="chosen(e).id" :options="verOpts(e)" size="sm" width="150px" aria-label="版本" @update:model-value="v => (ver[e.name] = String(v))" />
        <button type="button" class="primary add" @click="emit('pick', chosen(e))"><Plus :size="14" />添加</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.sp { display: grid; grid-template-columns: minmax(0, 1fr); }
.it { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px 16px; align-items: center; padding: 12px 0; border-bottom: 1px solid var(--line); }
.it:last-child { border-bottom: none; }
.n { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.n b { font-size: var(--fs-lg); }
.ver { font: var(--fs-xs) ui-monospace, "SF Mono", Menlo, monospace; }
.d { margin-top: 4px; color: var(--text-2); font-size: var(--fs-sm); }
.p { margin-top: 2px; font-size: var(--fs-xs); overflow-wrap: anywhere; }
.a { display: flex; align-items: center; gap: 8px; }
.add { display: inline-flex; align-items: center; gap: 4px; height: 28px; }
.small { font-size: var(--fs-sm); }
@media (max-width: 600px) { .it { grid-template-columns: minmax(0, 1fr); } }
</style>

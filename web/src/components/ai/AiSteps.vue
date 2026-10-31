<script setup lang="ts">
import { computed, ref } from 'vue'
import { Check, CircleAlert, ChevronRight } from 'lucide-vue-next'
import type { Part } from '../../composables/ai'

/**
 * A run of tool calls, collapsed to one line like Claude's apps: the current step while running,
 * "N 步" with a summary when done. Expanded, every step opens to show what it was asked and what came back.
 */
type Step = Extract<Part, { t: 'tool' }>
const props = defineProps<{ items: Step[]; live: boolean }>()

const open = ref(false)
const detail = ref<string | null>(null)
const running = computed(() => props.items.find(s => s.ok == null) ?? null)
const failed = computed(() => props.items.filter(s => s.ok === false).length)
const current = computed(() => running.value ?? props.items[props.items.length - 1])
const summary = computed(() => [...new Set(props.items.map(s => s.label))].slice(0, 4).join('、'))
</script>

<template>
  <div class="steps" :class="{ open }">
    <button type="button" class="head" @click="open = !open">
      <span class="st">
        <span v-if="live && running" class="spin"></span>
        <CircleAlert v-else-if="failed && !live" :size="14" class="warn" />
        <Check v-else :size="14" class="ok" />
      </span>
      <span v-if="live && running" class="now"><b>{{ current.label }}</b><span v-if="current.input" class="subj">{{ current.input }}</span></span>
      <span v-else class="now"><b>{{ items.length }} 步</b><span class="subj">{{ summary }}</span></span>
      <span class="cnt">{{ live && running ? `第 ${items.length} 步` : '' }}</span>
      <ChevronRight :size="15" class="chev" />
    </button>
    <div v-if="open" class="list">
      <div v-for="s in items" :key="s.id" class="row" :class="{ sub: s.sub, on: detail === s.id }">
        <button type="button" class="line" @click="detail = detail === s.id ? null : s.id">
          <span class="st"><span v-if="s.ok == null" class="spin"></span><Check v-else-if="s.ok" :size="13" class="ok" /><CircleAlert v-else :size="13" class="warn" /></span>
          <span class="lbl">{{ s.label }}</span><span v-if="s.input" class="subj">{{ s.input }}</span>
          <ChevronRight :size="13" class="chev" />
        </button>
        <div v-if="detail === s.id" class="detail">
          <div v-if="s.args" class="blk"><span class="k">请求</span><pre>{{ s.args }}</pre></div>
          <div class="blk"><span class="k">{{ s.ok === false ? '出错' : '结果' }}</span><pre>{{ s.output ?? (s.ok == null ? '进行中…' : '（无输出）') }}</pre></div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.steps { margin: 6px 0 8px; border-radius: 12px; background: rgba(255, 255, 255, .025); border: 1px solid var(--line); overflow: hidden; }
.head, .line { all: unset; box-sizing: border-box; display: flex; align-items: center; gap: 8px; width: 100%; cursor: pointer; min-width: 0; }
.head { padding: 9px 12px; font-size: var(--fs-sm); color: var(--text-2); }
.head:active, .line:active { background: var(--bg-2); }
.now { flex: 1; min-width: 0; display: flex; gap: 6px; align-items: baseline; overflow: hidden; white-space: nowrap; }
.now b { font-weight: 500; color: var(--text-1); flex: none; }
.subj { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-3); font-size: var(--fs-xs); }
.cnt { flex: none; font-size: var(--fs-xs); color: var(--text-4); }
.chev { flex: none; color: var(--text-4); transition: transform .2s; }
.steps.open > .head .chev, .row.on > .line .chev { transform: rotate(90deg); }
.st { flex: none; width: 14px; display: grid; place-items: center; }
.ok { color: var(--accent); }
.warn { color: var(--warn); }
.list { border-top: 1px solid var(--line); padding: 4px 0; }
.line { padding: 7px 12px; font-size: var(--fs-sm); }
.row.sub .line { padding-left: 30px; }
.lbl { flex: none; color: var(--text-1); }
.line .chev { margin-left: auto; }
.detail { padding: 2px 12px 10px 34px; display: grid; gap: 8px; animation: pop .2s ease-out; }
.blk { display: grid; gap: 3px; min-width: 0; }
.k { font-size: var(--fs-xs); color: var(--text-3); }
pre { margin: 0; max-height: 260px; overflow: auto; padding: 8px 10px; border-radius: 8px; background: #0a0a0a; border: 1px solid var(--line);
  font: 11px/1.5 ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-2); white-space: pre-wrap; word-break: break-all; }
.spin { width: 12px; height: 12px; border-radius: 50%; border: 2px solid var(--line-2); border-top-color: var(--accent); animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
@keyframes pop { from { opacity: 0; transform: translateY(-3px); } }
</style>

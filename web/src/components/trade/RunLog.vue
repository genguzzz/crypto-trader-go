<script setup lang="ts">
import { computed, ref } from 'vue'
import { BellRing } from 'lucide-vue-next'
import type { LogLine } from '../../tradeApi'
import { time } from '../../fmt'

/**
 * A strategy's run log (ctx.log, ctx.notify, handler errors), newest first. Times are the
 * instance's clock (replay time in backtests and trial runs). Tracebacks fold to their last line.
 */
const props = withDefaults(defineProps<{ lines: LogLine[]; limit?: number; empty?: string }>(), { limit: 50, empty: '还没有日志' })
const shown = ref(props.limit)
const open = ref<Set<number>>(new Set())
const list = computed(() => props.lines.map((l, i) => ({ ...l, i })).reverse())
const first = (m: string) => {
  const ls = m.trim().split('\n')
  return ls.length > 1 ? ls[0] + ' … ' + ls[ls.length - 1] : ls[0]
}
function toggle(i: number) { const s = new Set(open.value); if (s.has(i)) s.delete(i); else s.add(i); open.value = s }
</script>

<template>
  <div class="rl">
    <ul v-if="list.length" class="ls">
      <li v-for="l in list.slice(0, shown)" :key="l.i" class="l" :class="[l.level, l.kind]" @click="l.msg.includes('\n') && toggle(l.i)">
        <span class="t tnum">{{ time(l.clock || l.ts, true) }}</span>
        <BellRing v-if="l.kind === 'notify'" :size="12" class="ic" :title="l.sent ? '已发送到通知通道' : '只记录（回测 / 试运行 / 超出频率）'" />
        <span class="m" :class="{ multi: l.msg.includes('\n') }">{{ open.has(l.i) || !l.msg.includes('\n') ? l.msg : first(l.msg) }}</span>
      </li>
    </ul>
    <p v-else class="muted small">{{ empty }}</p>
    <button v-if="list.length > shown" type="button" class="more" @click="shown += 100">再显示 {{ Math.min(100, list.length - shown) }} 条</button>
  </div>
</template>

<style scoped>
.ls { list-style: none; margin: 0; padding: 0; display: grid; gap: 2px; font: var(--fs-xs)/1.5 ui-monospace, "SF Mono", Menlo, monospace; }
.l { display: flex; gap: 8px; align-items: baseline; padding: 3px 6px; border-radius: 4px; min-width: 0; }
.l.warn { background: rgba(255, 180, 0, .07); }
.l.error { background: rgba(255, 80, 80, .08); }
.l.notify .m { color: var(--accent); }
.t { flex: none; color: var(--text-3); }
.ic { flex: none; color: var(--accent); transform: translateY(1px); }
.m { min-width: 0; white-space: pre-wrap; overflow-wrap: anywhere; color: var(--text-2); }
.l.error .m { color: var(--down); }
.l.warn .m { color: var(--warn); }
.multi { cursor: pointer; }
.more { margin-top: 6px; height: 26px; font-size: var(--fs-xs); }
.small { font-size: var(--fs-xs); }
</style>

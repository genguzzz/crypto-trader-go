<script setup lang="ts">
import { computed } from 'vue'
import { Timer } from 'lucide-vue-next'
import type { Prompt } from '../../tradeApi'
import { time } from '../../fmt'
import { modeLabel } from './meta'

/** One open confirmation: what happened, a countdown to the deadline, and the answer buttons. */
const props = defineProps<{ p: Prompt; now: number; busy?: boolean }>()
const emit = defineEmits<{ answer: [string] }>()
const deadline = computed(() => (typeof props.p.deadline === 'number' ? props.p.deadline : Date.parse(props.p.deadline)))
const left = computed(() => Math.max(0, Math.floor((deadline.value - props.now) / 1000)))
const leftText = computed(() => (left.value >= 60 ? `${Math.floor(left.value / 60)}分${String(left.value % 60).padStart(2, '0')}秒` : `${left.value}秒`))
const defLabel = computed(() => props.p.options.find(o => o.key === props.p.default)?.label || props.p.default)
const btnClass = (k: string) => (k === 'execute' ? 'primary' : k === 'skip' ? 'ghost' : '')
</script>

<template>
  <div class="pc" :class="{ late: left <= 15 }">
    <div class="h">
      <span v-if="p.event.mode" class="tag" :class="modeLabel[p.event.mode]?.c">{{ modeLabel[p.event.mode]?.t || p.event.mode }}</span>
      <span v-else-if="p.event.kind === 'test'" class="tag">测试</span>
      <RouterLink v-if="p.event.accountId" :to="`/trading/accounts/${p.event.accountId}`" class="acc">{{ p.event.account }}</RouterLink>
      <span class="tm tnum">{{ time(p.event.ts, true) }}</span>
      <span class="cd tnum" :title="`超时后默认：${defLabel}`"><Timer :size="13" />{{ left ? leftText : '已超时' }}</span>
    </div>
    <div class="ptl">{{ p.event.title }}</div>
    <ul v-if="p.event.lines?.length" class="ln"><li v-for="(l, i) in p.event.lines" :key="i">{{ l }}</li></ul>
    <div class="f">
      <span class="muted small">超时默认：{{ defLabel }}</span>
      <span class="acts">
        <button v-for="o in p.options" :key="o.key" type="button" :class="btnClass(o.key)" :disabled="busy || !left" @click="emit('answer', o.key)">{{ o.label }}</button>
      </span>
    </div>
  </div>
</template>

<style scoped>
.pc { display: grid; gap: 8px; padding: 14px 16px; background: var(--bg-1); border: 1px solid var(--line-2); border-left: 3px solid var(--warn); border-radius: var(--radius); min-width: 0; }
.h { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; font-size: var(--fs-sm); }
.acc { color: var(--text-2); font-weight: 500; }
.tm { color: var(--text-3); font-size: var(--fs-xs); }
.cd { margin-left: auto; display: inline-flex; align-items: center; gap: 4px; color: var(--text-2); font-weight: 600; }
.late .cd { color: var(--warn); }
.ptl { font-size: var(--fs-xl); font-weight: 600; color: var(--text-1); }
.ln { margin: 0; padding: 0 0 0 16px; color: var(--text-2); font-size: var(--fs-sm); display: grid; gap: 2px; overflow-wrap: anywhere; }
.f { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.acts { display: inline-flex; gap: 8px; margin-left: auto; flex-wrap: wrap; }
.acts button { height: 34px; padding: 0 16px; font-weight: 500; }
button.ghost { background: transparent; color: var(--text-2); }
button:disabled { opacity: .45; cursor: default; }
.small { font-size: var(--fs-xs); }
</style>

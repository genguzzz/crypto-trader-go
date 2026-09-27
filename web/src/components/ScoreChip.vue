<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{ v: number | null | undefined; side: 'long' | 'short' }>()
// Above-neutral scores get the side colour with strength-scaled tint; neutral/below stay quiet.
const style = computed(() => {
  if (props.v == null) return { color: 'var(--text-3)' }
  if (props.v < 50) return { color: 'var(--text-3)', background: 'var(--bg-2)' }
  const a = (props.v - 50) / 50
  const rgb = props.side === 'long' ? '14,203,129' : '246,70,93'
  return {
    color: a > 0.55 ? '#fff' : props.side === 'long' ? 'var(--up)' : 'var(--down)',
    background: `rgba(${rgb},${(0.1 + a * 0.75).toFixed(2)})`,
  }
})
</script>

<template>
  <span class="score" :style="style" :title="v == null ? '缺少输入字段：未评分（不是 0 分）' : ''">{{ v == null ? '—' : v.toFixed(0) }}</span>
</template>

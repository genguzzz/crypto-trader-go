<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{ v: number | null | undefined; side: 'long' | 'short' }>()
const style = computed(() => {
  if (props.v == null) return {}
  const a = Math.max(0, (props.v - 50) / 50) // only above-neutral scores are tinted
  const rgb = props.side === 'long' ? '38,166,154' : '239,83,80'
  return { background: `rgba(${rgb},${(0.12 + a * 0.6).toFixed(2)})` }
})
</script>

<template>
  <span class="score" :style="style" :title="v == null ? '缺少输入字段：未评分（不是 0 分）' : ''">{{ v == null ? '—' : v.toFixed(0) }}</span>
</template>

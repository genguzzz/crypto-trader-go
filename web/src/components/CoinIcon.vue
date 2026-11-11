<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { apiUrl } from '../native'
import { withKeyParam } from '../composables/adminKey'
const props = withDefaults(defineProps<{ instId: string; size?: number }>(), { size: 18 })
const ccy = computed(() => props.instId.split('-')[0])
const failed = ref(false)
watch(() => props.instId, () => (failed.value = false))
// stable hue per symbol for the fallback avatar
const hue = computed(() => [...ccy.value].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 360, 7))
</script>

<template>
  <img v-if="!failed" class="coin" :src="withKeyParam(apiUrl(`/api/v1/icons/${ccy.toLowerCase()}.png`))" :width="size" :height="size" :alt="ccy" loading="lazy" @error="failed = true" />
  <span v-else class="coin fb" :style="{ width: size + 'px', height: size + 'px', fontSize: size * 0.5 + 'px', background: `hsl(${hue},45%,35%)` }">{{ ccy.slice(0, 2) }}</span>
</template>

<style scoped>
.coin { border-radius: 50%; vertical-align: middle; flex: none; display: inline-block; object-fit: cover; background: #222; }
.fb { display: inline-flex; align-items: center; justify-content: center; color: #fff; font-weight: 700; }
</style>

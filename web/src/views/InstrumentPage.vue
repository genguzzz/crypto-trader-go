<script setup lang="ts">
import InstrumentDetail from '../components/InstrumentDetail.vue'
defineProps<{ id: string }>()
</script>

<template>
  <div class="back"><a href="#" @click.prevent="$router.back()">← 返回列表</a></div>
  <InstrumentDetail :id="id" :key="id" />
</template>

<style scoped>
.back { padding: 8px 12px 0; }
</style>

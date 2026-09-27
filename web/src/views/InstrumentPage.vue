<script setup lang="ts">
import InstrumentDetail from '../components/InstrumentDetail.vue'
defineProps<{ id: string }>()
</script>

<template>
  <!-- back button lives in the detail header -->
  <InstrumentDetail :id="id" :key="id" />
</template>

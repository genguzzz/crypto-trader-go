<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import InstrumentDetail from '../components/InstrumentDetail.vue'
import { isDesktopClient } from '../composables/device'
const props = defineProps<{ id: string }>()
// PC: the workbench on the scanner page (list + chart + side panels) is the detail view
const router = useRouter()
const desktop = isDesktopClient()
onMounted(() => { if (desktop) router.replace({ path: '/', query: { sel: props.id } }) })
</script>

<template>
  <!-- phones: stacked detail page; back button lives in the detail header -->
  <InstrumentDetail v-if="!desktop" :id="id" :key="id" />
</template>

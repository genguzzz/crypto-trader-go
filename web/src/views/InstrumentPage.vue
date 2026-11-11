<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import InstrumentDetail from '../components/InstrumentDetail.vue'
import { isDesktopClient } from '../composables/device'
const props = defineProps<{ id: string }>()
// PC: the workbench on the scanner page (list + chart + side panels) is the detail view.
// ?proposal=<n> (Telegram "查看" on an AI trade proposal) draws that proposal on the chart; it survives the redirect.
const router = useRouter()
const route = useRoute()
const desktop = isDesktopClient()
const proposal = computed(() => (typeof route.query.proposal === 'string' ? route.query.proposal : ''))
onMounted(() => { if (desktop) router.replace({ path: '/', query: { sel: props.id, ...(proposal.value ? { proposal: proposal.value } : {}) } }) })
</script>

<template>
  <!-- phones: stacked detail page; back button lives in the detail header -->
  <InstrumentDetail v-if="!desktop" :id="id" :key="id" :proposal="Number(proposal) || undefined" />
</template>

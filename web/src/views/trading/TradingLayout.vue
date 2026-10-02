<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { Wallet, Library, Inbox, BrainCircuit } from 'lucide-vue-next'
import { store } from '../../store'

/** Trading section: sub navigation (accounts / strategies / confirmations / AI) above the page. */
const route = useRoute()
const tabs = computed(() => [
  { to: '/trading', label: '账户', icon: Wallet, on: !/^\/trading\/(strategies|prompts|ai)/.test(route.path) },
  { to: '/trading/strategies', label: '策略库', icon: Library, on: route.path.startsWith('/trading/strategies') },
  { to: '/trading/prompts', label: '待确认', icon: Inbox, on: route.path.startsWith('/trading/prompts'), badge: store.openPrompts },
  { to: '/trading/ai', label: 'AI 决策', icon: BrainCircuit, on: route.path.startsWith('/trading/ai') },
])
</script>

<template>
  <div class="tl">
    <nav class="tsub" aria-label="交易">
      <RouterLink v-for="t in tabs" :key="t.to" :to="t.to" class="ts" :class="{ on: t.on }" :aria-current="t.on ? 'page' : undefined">
        <component :is="t.icon" :size="14" /><span>{{ t.label }}</span>
        <span v-if="t.badge" class="bd tnum">{{ t.badge }}</span>
      </RouterLink>
    </nav>
    <RouterView />
  </div>
</template>

<style scoped>
.tl { max-width: 1400px; margin: 0 auto; padding: 12px 16px 0; display: grid; grid-template-columns: minmax(0, 1fr); }
.tsub { display: flex; gap: 2px; padding: 2px; justify-self: start; max-width: 100%; overflow-x: auto; background: var(--bg-0); border: 1px solid var(--line); border-radius: 8px; scrollbar-width: none; }
.ts { display: inline-flex; align-items: center; gap: 6px; height: 28px; padding: 0 12px; border-radius: 6px; color: var(--text-3); font-size: var(--fs-sm); font-weight: 500; white-space: nowrap; text-decoration: none !important; transition: color .12s, background .12s; }
@media (hover: hover) { .ts:hover { color: var(--text-1); opacity: 1; } }
.ts.on { color: var(--text-1); background: var(--bg-3); box-shadow: 0 1px 3px rgba(0, 0, 0, .4); }
.ts.on svg { color: var(--accent); }
.bd { min-width: 16px; height: 16px; padding: 0 4px; border-radius: 8px; background: var(--warn); color: var(--on-accent); font-size: 10px; font-weight: 700; line-height: 16px; text-align: center; }
/* child page root carries this scope id; width: 100% so `margin: auto` on a grid item can't shrink it */
.tl > .page { width: 100%; padding: 12px 0 16px; max-width: none; }
@media (max-width: 900px) {
  .tl { padding: 10px 12px 0; }
  .tsub { justify-self: stretch; }
  .ts { flex: 1; justify-content: center; padding: 0 8px; }
}
</style>

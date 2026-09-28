<script setup lang="ts">
/**
 * One setting: label + one-line description on the left, control on the right (stacked on phones).
 * `changed` marks an unsaved edit; `error` shows an inline validation message under the control.
 */
defineProps<{ label: string; desc?: string; error?: string; changed?: boolean; forId?: string }>()
</script>

<template>
  <div class="sr" :class="{ changed, invalid: !!error }">
    <div class="lbl">
      <label :for="forId" class="name">{{ label }}<span v-if="changed" class="chg" title="未保存"></span></label>
      <div v-if="desc || $slots.desc" class="desc"><slot name="desc">{{ desc }}</slot></div>
    </div>
    <div class="ctl">
      <slot />
      <div v-if="error" class="err" role="alert">{{ error }}</div>
    </div>
  </div>
</template>

<style scoped>
.sr { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, auto); align-items: center; gap: 10px 32px; padding: 16px 0; border-bottom: 1px solid var(--line); }
.sr:last-child { border-bottom: none; }
.lbl { min-width: 0; }
.name { display: inline-flex; align-items: center; gap: 6px; font-size: var(--fs-md); font-weight: 500; color: var(--text-1); }
.chg { width: 6px; height: 6px; border-radius: 50%; background: var(--accent); }
.desc { margin-top: 3px; font-size: var(--fs-sm); color: var(--text-3); line-height: 1.5; }
.ctl { display: flex; flex-direction: column; align-items: flex-end; gap: 6px; min-width: 0; }
.err { font-size: var(--fs-xs); color: var(--down); }
.invalid :deep(input) { border-color: var(--down); }
@media (max-width: 700px) {
  .sr { grid-template-columns: minmax(0, 1fr); }
  .ctl { align-items: flex-start; }
}
</style>

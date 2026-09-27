<script setup lang="ts">
import { computed } from 'vue'
import { PopoverRoot, PopoverTrigger, PopoverPortal, PopoverContent } from 'reka-ui'
import { LayoutPanelLeft, RotateCcw, GripVertical, MoveHorizontal } from 'lucide-vue-next'
import { layoutRegistry } from '../composables/layout'

/** IDE-style layout entry point in the top bar (PC only): shows customised parts and resets them. */
const entries = computed(() => Object.entries(layoutRegistry))
const anyCustom = computed(() => entries.value.some(([, e]) => e.customized))
const resetAll = () => entries.value.forEach(([, e]) => e.customized && e.reset())
</script>

<template>
  <PopoverRoot v-if="entries.length">
    <PopoverTrigger class="lm-btn" :class="{ custom: anyCustom }" aria-label="布局" title="布局">
      <LayoutPanelLeft :size="16" />
      <span v-if="anyCustom" class="badge"></span>
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent class="lm-pop" side="bottom" align="end" :side-offset="8">
        <div class="lm-title">布局</div>
        <div v-for="[k, e] in entries" :key="k" class="lm-row">
          <span>{{ e.label }}</span>
          <span class="state" :class="{ on: e.customized }">{{ e.customized ? '已自定义' : '默认' }}</span>
          <button class="lm-reset" :disabled="!e.customized" title="恢复默认" @click="e.reset()"><RotateCcw :size="13" /></button>
        </div>
        <button class="lm-all" :disabled="!anyCustom" @click="resetAll"><RotateCcw :size="13" /> 全部恢复默认</button>
        <div class="lm-hint"><GripVertical :size="13" /> 按住面板左侧把手拖动排序</div>
        <div class="lm-hint"><MoveHorizontal :size="13" /> 拖动列表与详情之间的分隔线调整宽度，双击复原</div>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>

<style>
.lm-btn { position: relative; display: inline-flex; align-items: center; justify-content: center; width: 30px; height: 30px; padding: 0; border-radius: 8px; border: 1px solid transparent; background: transparent; color: var(--text-2); }
.lm-btn:hover, .lm-btn[data-state="open"] { background: var(--bg-2); color: var(--text-1); border-color: var(--line-2); }
.lm-btn .badge { position: absolute; top: 5px; right: 5px; width: 6px; height: 6px; border-radius: 50%; background: var(--accent); }
.lm-pop { z-index: 120; width: 280px; background: var(--bg-2); border: 1px solid var(--line-2); border-radius: 12px; padding: 10px; box-shadow: 0 16px 48px rgba(0, 0, 0, .5); font-size: var(--fs-sm); }
.lm-title { font-weight: 600; color: var(--text-1); padding: 2px 6px 8px; }
.lm-row { display: flex; align-items: center; gap: 8px; padding: 6px; border-radius: 6px; color: var(--text-1); }
.lm-row:hover { background: var(--bg-3); }
.lm-row .state { margin-left: auto; font-size: var(--fs-xs); color: var(--text-3); }
.lm-row .state.on { color: var(--accent); }
.lm-reset { border: none; background: transparent; padding: 2px; color: var(--text-3); display: inline-flex; }
.lm-reset:not(:disabled):hover { color: var(--text-1); }
.lm-reset:disabled { opacity: .3; cursor: default; }
.lm-all { width: 100%; margin: 6px 0 8px; display: inline-flex; align-items: center; justify-content: center; gap: 6px; }
.lm-all:disabled { opacity: .45; cursor: default; }
.lm-hint { display: flex; align-items: center; gap: 6px; color: var(--text-3); font-size: var(--fs-xs); padding: 2px 6px; }
</style>

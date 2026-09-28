<script setup lang="ts">
import { computed } from 'vue'
import type { Storage } from '../../api'
import { time } from '../../fmt'
import Meter from '../ui/Meter.vue'
import { bytes, count, tableLabel, tierCols } from './storage'

/** Storage usage summary, per-data-type quota / retention table and row counts (status page). */
const props = defineProps<{ storage: Storage; rows: Record<string, number> | null }>()
const sortedRows = computed(() => Object.entries(props.rows || {}).sort((a, b) => b[1] - a[1]))
const tierCount = (i: number) => props.storage.tiers?.[props.storage.tierNames?.[i]] ?? 0
</script>

<template>
  <div class="sum">
    <div class="big">
      <span class="v tnum">{{ bytes(storage.usedBytes) }}</span>
      <span class="of">/ {{ bytes(storage.capBytes) }}</span>
      <RouterLink to="/settings#storage" class="adj">调整上限 →</RouterLink>
    </div>
    <Meter :value="storage.usedBytes" :max="storage.capBytes" :height="8" />
    <div class="meta">库文件 {{ bytes(storage.fileBytes) }} · 可复用 {{ bytes(storage.freeBytes) }} · {{ time(storage.at) }} 检查</div>
  </div>

  <div class="table-scroll"><table class="st">
    <thead><tr>
      <th>数据</th><th class="num">占用 / 配额</th>
      <th v-for="[i, l] in tierCols" :key="i" class="num">{{ l }}<small>{{ tierCount(i) }} 个</small></th>
    </tr></thead>
    <tbody>
      <tr v-for="t in storage.tables" :key="t.name">
        <td>{{ tableLabel[t.name] || t.name }} <span class="tag warn" v-if="t.tight" title="超出配额，已从低层开始缩短保留期">已收紧</span></td>
        <td class="num use">
          <template v-if="t.name === 'funding_obs'"><span class="muted">同上</span></template>
          <template v-else>
            <span>{{ bytes(t.bytes) }} <span class="muted">/ {{ bytes(t.budget) }}</span></span>
            <Meter :value="t.bytes" :max="t.budget" :height="3" />
          </template>
        </td>
        <td v-for="[i] in tierCols" :key="i" class="num age">{{ t.ages?.[i] ?? '长期' }}</td>
      </tr>
    </tbody>
  </table></div>
  <p class="note err" v-if="storage.error">{{ storage.error }}</p>

  <div class="rows">
    <div class="rh">记录数 <span class="muted">每分钟统计</span></div>
    <div v-if="rows" class="rg">
      <div v-for="[k, v] in sortedRows" :key="k" class="ri">
        <span class="rl">{{ tableLabel[k] || k }}</span><span class="rv tnum">{{ count(v) }}</span>
      </div>
    </div>
    <div v-else class="note">行数统计中…</div>
  </div>
</template>

<style scoped>
.sum { display: grid; gap: 8px; margin-bottom: 16px; }
.big { display: flex; align-items: baseline; gap: 6px; flex-wrap: wrap; }
.big .v { font-size: var(--fs-2xl); font-weight: 700; }
.big .of { color: var(--text-3); font-size: var(--fs-md); }
.adj { margin-left: auto; font-size: var(--fs-sm); }
.meta { font-size: var(--fs-xs); color: var(--text-3); }
.st th small { display: block; font-weight: 400; color: var(--text-3); font-size: var(--fs-xs); }
.st td { font-size: var(--fs-sm); }
.st .use { min-width: 140px; }
.st .use > span { display: block; margin-bottom: 3px; }
.st .age { color: var(--text-2); }
.err { color: var(--warn); }
.rows { margin-top: 18px; }
.rh { font-size: var(--fs-sm); font-weight: 600; color: var(--text-2); margin-bottom: 8px; }
.rh .muted { font-weight: 400; font-size: var(--fs-xs); margin-left: 6px; }
.rg { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(180px, 100%), 1fr)); gap: 0 24px; }
.ri { display: flex; justify-content: space-between; gap: 12px; padding: 6px 0; border-bottom: 1px solid var(--line); font-size: var(--fs-sm); min-width: 0; }
.rl { color: var(--text-3); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rv { color: var(--text-1); }
</style>

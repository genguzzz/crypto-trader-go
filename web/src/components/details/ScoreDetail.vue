<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { store, setRule, fieldLabel, ruleOptions } from '../../store'
import { fieldValue } from '../../fmt'
import InfoTip from '../InfoTip.vue'
import ScoringExplainer from '../ScoringExplainer.vue'
import type { DetailCtx } from './types'

/** Score breakdown for the selected rule (long/short contributions) plus every fact-panel field. */
const props = defineProps<{ ctx: DetailCtx }>()
const ruleModel = computed({ get: () => store.ruleId, set: (v: string) => setRule(v) })
const row = computed(() => props.ctx.row)
const score = computed(() => row.value?.scores?.[store.ruleId])
</script>

<template>
  <div class="bar">
    <USelect v-model="ruleModel" :options="ruleOptions()" size="sm" width="240px" aria-label="评分规则" />
    <InfoTip label="原理"><ScoringExplainer /></InfoTip>
    <RouterLink :to="`/rules/${store.ruleId}`" class="link">战绩 →</RouterLink>
  </div>
  <div v-if="score" class="sides">
    <div v-for="side in (['long', 'short'] as const)" :key="side" class="side">
      <div class="sh">
        <span class="sl">{{ side === 'long' ? '多头分' : '空头分' }}</span>
        <span class="sv" :class="score[side].score != null && score[side].score! >= 50 ? side : ''">{{ score[side].score != null ? score[side].score!.toFixed(0) : '—' }}</span>
        <span class="tag bad" v-if="score.gate !== 'PASS'">{{ score.gate.replace('BLOCKED:', '门槛：') }}</span>
      </div>
      <div v-for="c in score[side].contrib || []" :key="c.field" class="contrib">
        <span class="f" :title="c.field">{{ fieldLabel(c.field) }}</span>
        <span class="v">{{ fieldValue(c.field, row?.values?.[c.field]) }}</span>
        <span class="p">分位 {{ c.pct.toFixed(0) }}</span>
        <span class="barwrap"><span class="b" :class="c.points >= 0 ? 'pos' : 'neg'" :style="{ width: Math.min(100, Math.abs(c.points) * 2) + '%' }"></span></span>
        <span class="pts" :class="c.points >= 0 ? 'up' : 'down'">{{ c.points >= 0 ? '+' : '' }}{{ c.points.toFixed(1) }}</span>
      </div>
      <div v-if="score[side].missing?.length" class="note">缺少：{{ score[side].missing!.map(fieldLabel).join('、') }} → 未评分（不是 0）</div>
    </div>
  </div>
  <div v-else class="note">暂无评分（数据预热中或缺少输入）</div>

  <h4>全部字段 <InfoTip>
    <b>数值</b>：只使用决策时刻已经可见的数据；盘中每 10 秒按未收盘分钟重算（会变动），战绩只记录分钟确认值。<br />
    <b>池内分位</b>：该值在当前活跃池里的位置，0 = 最低，100 = 最高；规则打分用的就是它。<br />
    <b>不可回放</b>：历史回放中没有该字段（点差、基差、预测资金费等只在实时有），含这些字段的规则只能积累上线后战绩。<br />
    缺失显示“—”，表示未知，不当作 0。
  </InfoTip></h4>
  <div class="table-scroll">
    <table>
      <thead><tr><th>字段</th><th class="num">数值</th><th class="num">池内分位</th><th class="hide-sm">定义</th></tr></thead>
      <tbody>
        <tr v-for="f in store.meta?.fields || []" :key="f.key">
          <td>{{ f.label }}<span class="tag" v-if="!f.replayable" title="历史回放中没有此字段">不可回放</span></td>
          <td class="num">{{ fieldValue(f.key, row?.values?.[f.key]) }}</td>
          <td class="num">{{ row?.pct?.[f.key] != null ? row.pct[f.key].toFixed(0) : '—' }}</td>
          <td class="muted hide-sm wrap">{{ f.desc }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.bar { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 14px; }
.link { margin-left: auto; font-size: var(--fs-sm); }
.sides { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px; }
.side .sh { display: flex; gap: 10px; align-items: baseline; margin-bottom: 8px; padding-bottom: 8px; border-bottom: 1px solid var(--line); }
.sl { font-size: var(--fs-sm); color: var(--text-3); }
.sv { font-size: var(--fs-3xl); font-weight: 700; line-height: 1; color: var(--text-2); font-variant-numeric: tabular-nums; }
.sv.long { color: var(--up); } .sv.short { color: var(--down); }
.contrib { display: grid; grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr) 52px minmax(24px, 1fr) 44px; gap: 8px; align-items: center; font-size: var(--fs-sm); padding: 3px 0; font-variant-numeric: tabular-nums; }
.contrib .f { color: var(--text-1); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.contrib .v { color: var(--text-2); } .contrib .p { color: var(--text-3); }
.pts { font-weight: 600; text-align: right; }
.pts.up { color: var(--up); } .pts.down { color: var(--down); }
.barwrap { height: 4px; background: var(--bg-3); border-radius: 2px; overflow: hidden; }
.b { display: block; height: 100%; }
.b.pos { background: var(--up); } .b.neg { background: var(--down); }
h4 { display: flex; align-items: center; gap: 6px; margin: 20px 0 8px; font-size: var(--fs-md); color: var(--text-1); }
.wrap { white-space: normal; }
@media (max-width: 900px) { .sides { grid-template-columns: minmax(0, 1fr); } }
@container (max-width: 560px) { .sides { grid-template-columns: minmax(0, 1fr); } }
</style>

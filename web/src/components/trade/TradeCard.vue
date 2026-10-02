<script setup lang="ts">
import { computed } from 'vue'
import { CandlestickChart, ChevronLeft, ChevronRight, X } from 'lucide-vue-next'
import type { Trade } from '../../tradeApi'
import { time, price } from '../../fmt'
import CoinIcon from '../CoinIcon.vue'
import { amt, pct, qty, sym, tone } from './meta'
import { exitKindLabel, holdText } from './tradeMeta'

/**
 * One round trip: side, result, prices, size, costs, reasons and (optionally) every fill.
 * Used as the hover card on the equity chart, the pinned card on phones and the side panel of the
 * instrument view. `nav` shows prev/next (keyboard ← → on PC is handled by the explorer).
 */
const props = defineProps<{ t: Trade; fills?: boolean; kline?: boolean; nav?: { i: number; n: number }; closable?: boolean }>()
const emit = defineEmits<{ kline: []; prev: []; next: []; close: [] }>()
const long = computed(() => props.t.posSide === 'long')
const ek = computed(() => exitKindLabel[props.t.exitKind] || { t: props.t.exitKind, c: '' })
const fillKind: Record<string, string> = { open: '开仓', add: '加仓', reduce: '减仓', close: '平仓' }
const orderType: Record<string, string> = { market: '市价', limit: '限价', stop: '止损', target: '止盈', liquidation: '强平' }
</script>

<template>
  <div class="tc">
    <div class="hd">
      <CoinIcon :inst-id="t.instId" :size="20" />
      <b class="sy">{{ sym(t.instId) }}</b>
      <span class="tag" :class="long ? 'good' : 'bad'">{{ long ? '多' : '空' }} {{ t.leverage }}x</span>
      <span class="tag" :class="ek.c">{{ ek.t }}</span>
      <span class="sp"></span>
      <template v-if="nav">
        <button type="button" class="ib" :disabled="nav.i <= 0" aria-label="上一笔" title="上一笔（←）" @click="emit('prev')"><ChevronLeft :size="15" /></button>
        <span class="nv tnum">{{ nav.i + 1 }}/{{ nav.n }}</span>
        <button type="button" class="ib" :disabled="nav.i >= nav.n - 1" aria-label="下一笔" title="下一笔（→）" @click="emit('next')"><ChevronRight :size="15" /></button>
      </template>
      <button v-if="closable" type="button" class="ib" aria-label="关闭" @click="emit('close')"><X :size="15" /></button>
    </div>
    <div class="net">
      <span class="v tnum" :class="tone(t.net)">{{ amt(t.net, 2, true) }}<small> USDT</small></span>
      <span class="r tnum" :class="tone(t.returnPct)">{{ pct(t.returnPct, 2, true) }}<small> 保证金收益</small></span>
    </div>
    <dl class="kv">
      <div><dt>开仓</dt><dd class="tnum">{{ time(t.openTs, true) }} <span class="sub">@ {{ price(t.entryPx) }}</span></dd></div>
      <div><dt>平仓</dt><dd class="tnum"><template v-if="t.closeTs">{{ time(t.closeTs, true) }} <span class="sub">@ {{ price(t.exitPx) }}</span></template><template v-else>持仓中</template></dd></div>
      <div><dt>持有</dt><dd class="tnum">{{ holdText(t) }}</dd></div>
      <div><dt>数量</dt><dd class="tnum">{{ qty(t.qty) }} 张 <span class="sub">× {{ t.ctVal }}</span></dd></div>
      <div><dt>名义</dt><dd class="tnum">{{ amt(t.notional) }} <span class="sub">USDT</span></dd></div>
      <div><dt>保证金</dt><dd class="tnum">{{ amt(t.margin) }} <span class="sub">USDT</span></dd></div>
      <div><dt>盈亏（未含费）</dt><dd class="tnum" :class="tone(t.pnl)">{{ amt(t.pnl, 2, true) }}</dd></div>
      <div><dt>手续费 / 资金费</dt><dd class="tnum"><span class="down">{{ amt(-Math.abs(t.fees), 2) }}</span> / <span :class="tone(t.funding)">{{ amt(t.funding, 2, true) }}</span></dd></div>
      <div v-if="t.stopLoss || t.takeProfit"><dt>止损 / 止盈</dt><dd class="tnum"><span class="down">{{ price(t.stopLoss) }}</span> / <span class="up">{{ price(t.takeProfit) }}</span></dd></div>
    </dl>
    <div v-if="t.openReason" class="why"><span class="k">开仓理由</span>{{ t.openReason }}</div>
    <div v-if="t.closeReason && t.closeReason !== t.openReason" class="why"><span class="k">平仓理由</span>{{ t.closeReason }}</div>
    <div v-if="fills && t.fills.length" class="fl">
      <div v-for="f in t.fills" :key="f.id" class="fr">
        <span class="tnum muted">{{ time(f.ts, true) }}</span>
        <span :class="f.side === 'buy' ? 'up' : 'down'">{{ fillKind[f.kind] || f.kind }}</span>
        <span class="muted">{{ orderType[f.orderType] || f.orderType }}</span>
        <span class="tnum">{{ qty(f.qty) }} 张 @ {{ price(f.px) }}</span>
        <span class="tnum" :class="tone(f.pnl)">{{ f.pnl ? amt(f.pnl, 2, true) : '' }}</span>
      </div>
    </div>
    <div v-if="kline || t.aiDecisionId" class="ft">
      <RouterLink v-if="t.aiDecisionId" to="/trading/ai" class="lk">AI 决策 #{{ t.aiDecisionId }}</RouterLink>
      <button v-if="kline" type="button" class="go" @click="emit('kline')"><CandlestickChart :size="14" />在 K 线中查看</button>
    </div>
  </div>
</template>

<style scoped>
.tc { display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; min-width: 0; }
.hd { display: flex; align-items: center; gap: 6px; min-width: 0; }
.sy { font-size: var(--fs-lg); font-weight: 700; }
.sp { flex: 1; }
.ib { display: inline-flex; align-items: center; justify-content: center; width: 26px; height: 26px; padding: 0; background: transparent; border-color: var(--line-2); color: var(--text-2); }
.ib:disabled { opacity: .35; cursor: default; }
.nv { font-size: var(--fs-xs); color: var(--text-3); min-width: 34px; text-align: center; }
.net { display: flex; align-items: baseline; gap: 12px; flex-wrap: wrap; }
.net .v { font-size: var(--fs-2xl); font-weight: 700; line-height: 1.15; }
.net .r { font-size: var(--fs-md); font-weight: 600; }
.net small { font-size: var(--fs-xs); font-weight: 500; color: var(--text-3); }
.kv { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 6px 14px; margin: 0; }
.kv dt { font-size: var(--fs-xs); color: var(--text-3); }
.kv dd { margin: 1px 0 0; font-size: var(--fs-sm); color: var(--text-1); overflow-wrap: anywhere; }
.sub { color: var(--text-3); }
.why { font-size: var(--fs-sm); color: var(--text-2); overflow-wrap: anywhere; }
.why .k { display: inline-block; margin-right: 6px; font-size: var(--fs-xs); color: var(--text-3); }
.fl { display: grid; gap: 2px; border-top: 1px solid var(--line); padding-top: 6px; }
.fr { display: grid; grid-template-columns: 76px 34px 34px minmax(0, 1fr) auto; gap: 6px; font-size: var(--fs-xs); align-items: baseline; }
.ft { display: flex; align-items: center; gap: 8px; justify-content: flex-end; flex-wrap: wrap; }
.lk { font-size: var(--fs-xs); color: var(--text-2); margin-right: auto; }
.go { display: inline-flex; align-items: center; gap: 4px; height: 28px; padding: 0 10px; font-size: var(--fs-sm); background: var(--accent-bg); border-color: var(--accent); color: var(--text-1); }
</style>

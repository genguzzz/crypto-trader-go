<script setup lang="ts">
import { computed, ref } from 'vue'
import { Plus, Wallet, Archive } from 'lucide-vue-next'
import { tradeApi, type Account } from '../../tradeApi'
import { useTradeLive } from '../../composables/tradeLive'
import AccountCard from '../../components/trade/AccountCard.vue'
import FilterChip from '../../components/ui/FilterChip.vue'
import Loading from '../../components/ui/Loading.vue'
import { amt, tone } from '../../components/trade/meta'

const accounts = ref<Account[] | null>(null)
const running = ref<Record<number, number>>({})
const paused = ref<Record<number, number>>({})
const error = ref('')
const showArchived = ref(false)

async function load() {
  try {
    const list = await tradeApi.accounts()
    accounts.value = list
    error.value = ''
    // running-instance counts for live accounts (a handful of small requests)
    const live = list.filter(a => a.mode === 'live' && !a.archived)
    const lists = await Promise.all(live.map(a => tradeApi.instances(a.id).catch(() => undefined)))
    const m: Record<number, number> = {}, pz: Record<number, number> = {}
    live.forEach((a, i) => {
      const xs = lists[i]
      if (!xs) return
      m[a.id] = xs.filter(x => x.state === 'running').length
      pz[a.id] = xs.filter(x => x.state === 'paused' || x.state === 'faulted').length
    })
    running.value = m
    paused.value = pz
  } catch (e: any) {
    error.value = e.message
  }
}
useTradeLive(load, { pollMs: 15_000 })

const visible = computed(() => (accounts.value || []).filter(a => showArchived.value || !a.archived))
const live = computed(() => visible.value.filter(a => a.mode !== 'backtest'))
const backtests = computed(() => visible.value.filter(a => a.mode === 'backtest'))
const nArchived = computed(() => (accounts.value || []).filter(a => a.archived).length)
const sum = computed(() => {
  const xs = (accounts.value || []).filter(a => a.mode === 'live' && !a.archived)
  return { equity: xs.reduce((s, a) => s + a.equity, 0), day: xs.reduce((s, a) => s + a.dayPnl, 0), n: xs.length, halted: xs.filter(a => a.halted).length }
})
</script>

<template>
  <div class="page">
    <div class="ph">
      <h1>交易账户</h1>
      <InfoTip>
        <b>模拟交易</b>：所有账户都是模拟盘，不会向 OKX 下任何真实订单，也不读取你的 OKX 账户。成交按真实盘口（实时）或 K 线（回测）模拟，计入手续费、滑点和资金费。<br />
        <b>账户</b>：一笔入金 + 一组硬性风控规则；策略挂在账户下运行，多个策略共享账户的资金和风控。<br />
        <b>实时模拟</b>：用实时行情持续运行。<b>历史回测</b>：在选定日期区间内按历史 K 线快速重放，结束后生成回测报告。<br />
        <b>熔断</b>：当日亏损达到上限后当天停止开仓，次日（UTC+8）自动恢复。
      </InfoTip>
      <div class="sp"></div>
      <FilterChip v-if="nArchived" v-model="showArchived" label="已归档" :icon="Archive" :count="nArchived" />
      <RouterLink to="/trading/new" class="btn primary"><Plus :size="14" />新建账户</RouterLink>
    </div>

    <div v-if="error" class="banner">{{ error }}</div>
    <Loading v-if="!accounts && !error" text="加载账户" />

    <template v-else-if="accounts">
      <div v-if="sum.n" class="sum">
        <div class="s"><span class="lbl">实时账户总权益</span><span class="v tnum">{{ amt(sum.equity) }} <small>USDT</small></span></div>
        <div class="s"><span class="lbl">今日盈亏</span><span class="v2 tnum" :class="tone(sum.day)">{{ amt(sum.day, 2, true) }}</span></div>
        <div class="s"><span class="lbl">账户</span><span class="v2 tnum">{{ sum.n }}<span v-if="sum.halted" class="tag bad">{{ sum.halted }} 熔断</span></span></div>
      </div>

      <div v-if="!visible.length" class="empty panel">
        <Wallet :size="22" class="muted" />
        <div>还没有交易账户</div>
        <div class="muted small">先建账户（入金与风控），再挂上策略；也可以先做一次历史回测。</div>
        <RouterLink to="/trading/new" class="btn primary"><Plus :size="14" />新建账户</RouterLink>
      </div>

      <section v-if="live.length">
        <h2>实时模拟 <span class="muted">{{ live.length }}</span></h2>
        <div class="grid"><AccountCard v-for="a in live" :key="a.id" :a="a" :running="running[a.id]" :paused="paused[a.id]" /></div>
      </section>
      <section v-if="backtests.length">
        <h2>历史回测 <span class="muted">{{ backtests.length }}</span></h2>
        <div class="grid"><AccountCard v-for="a in backtests" :key="a.id" :a="a" /></div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.ph { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.ph h1 { margin: 0; font-size: var(--fs-2xl); font-weight: 600; }
.sp { flex: 1; }
.btn { display: inline-flex; align-items: center; gap: 4px; height: 30px; padding: 0 12px; border-radius: var(--radius-sm); border: 1px solid var(--line-2); background: var(--bg-3); color: var(--text-1); font-weight: 500; text-decoration: none !important; }
.btn.primary { background: var(--accent); border-color: var(--accent); color: var(--on-accent); font-weight: 600; }
.banner { border-radius: var(--radius-sm); }
.sum { display: flex; gap: 12px 32px; flex-wrap: wrap; padding: 12px 16px; background: var(--bg-1); border: 1px solid var(--line); border-radius: var(--radius); }
.s { display: grid; gap: 2px; }
.lbl { font-size: var(--fs-xs); color: var(--text-3); }
.v { font-size: var(--fs-2xl); font-weight: 700; line-height: 1.2; }
.v small { font-size: var(--fs-xs); color: var(--text-3); font-weight: 500; }
.v2 { display: inline-flex; align-items: center; gap: 6px; font-size: var(--fs-xl); font-weight: 600; line-height: 1.5; color: var(--text-2); }
.v2.up { color: var(--up); } .v2.down { color: var(--down); }
h2 { margin: 6px 2px 8px; font-size: var(--fs-lg); font-weight: 600; }
h2 .muted { font-weight: 400; font-size: var(--fs-sm); }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 320px), 1fr)); gap: 12px; }
.empty { display: grid; justify-items: center; gap: 8px; padding: 36px 16px; text-align: center; }
.small { font-size: var(--fs-sm); }
</style>

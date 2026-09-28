<script setup lang="ts">
import { computed } from 'vue'
import { Radar, FlaskConical, Wallet, SlidersHorizontal, Activity } from 'lucide-vue-next'
import { store } from './store'
import LayoutMenu from './components/LayoutMenu.vue'
import CoinIcon from './components/CoinIcon.vue'
import { useDesktop } from './composables/device'
import { time, clock, usd, price, logPct } from './fmt'

const desktop = useDesktop()
const s = computed(() => store.snap)
const phase = computed(() => {
  const p = s.value?.phase
  return p === 'live' ? { t: '实时', c: 'ok' } : p === 'backfill-1m' ? { t: '回填中', c: 'warn' } : p === 'bootstrap-5m' ? { t: '快速建池', c: 'warn' } : { t: '启动中', c: 'warn' }
})

const nav = [
  { to: '/', label: '扫描', icon: Radar, exact: true },
  { to: '/rules', label: '规则与战绩', short: '规则', icon: FlaskConical },
  { to: '/positions', label: '仓位', icon: Wallet },
  { to: '/settings', label: '设置', icon: SlidersHorizontal },
  { to: '/status', label: '系统', icon: Activity },
]

/** Market pulse: BTC / ETH live price + 24h change from the current snapshot. */
const pulse = computed(() =>
  ['BTC-USDT-SWAP', 'ETH-USDT-SWAP'].map(id => {
    const r = s.value?.rows?.find(x => x.instId === id)
    const ret = r?.values?.ret_24h
    return { id, sym: id.split('-')[0], px: store.prices[id]?.[0] ?? r?.last, ret, cls: ret == null ? '' : ret >= 0 ? 'up' : 'down' }
  }),
)
</script>

<template>
  <header class="topbar">
    <RouterLink to="/" class="brand" aria-label="OKX Helper 首页">
      <img class="mark" src="/icon.svg" alt="" />
      <span class="word">OKX Helper<small>永续研究</small></span>
    </RouterLink>

    <nav class="nav" aria-label="主导航">
      <RouterLink v-for="n in nav" :key="n.to" :to="n.to" class="nav-i" :class="{ exact: n.exact }">
        <component :is="n.icon" :size="15" class="ni" /><span>{{ n.label }}</span>
      </RouterLink>
    </nav>

    <div class="right">
      <div class="pulse hide-sm">
        <RouterLink v-for="p in pulse" :key="p.id" :to="`/?sel=${p.id}`" class="pp" :title="`${p.id} · 24h ${logPct(p.ret)}`">
          <CoinIcon :inst-id="p.id" :size="14" />
          <span class="ps">{{ p.sym }}</span>
          <span class="pv tnum">{{ price(p.px) }}</span>
          <span class="pr tnum" :class="p.cls">{{ logPct(p.ret) }}</span>
        </RouterLink>
      </div>

      <div v-if="s" class="status" :title="store.connected ? '实时推送已连接' : '推送断开，自动重连中'">
        <span class="dot" :class="store.connected ? phase.c : 'bad'"></span>
        <span class="sv"><b class="tnum">{{ s.coverage.active }}</b><span class="dim">/{{ s.topN }}</span></span>
        <span class="sep hide-sm"></span>
        <span class="hide-sm dim">&gt;{{ usd(s.threshold) }}</span>
        <span class="sep"></span>
        <span class="tm tnum" title="最近一次确认（已收盘分钟）">{{ time(s.decisionTs) }}</span>
        <span v-if="s.preview" class="tm live tnum hide-sm" title="盘中每 10 秒重算">{{ clock(s.previewAt) }}</span>
        <span v-if="phase.t !== '实时' || s.provisional" class="tag warn">{{ s.provisional ? '临时名单' : phase.t }}</span>
        <InfoTip class="hide-sm">
          <b>达标</b>：12h USDT 成交额（连续 720 根已确认 1m K 线）严格大于阈值、按成交额取前 N 的合约数（格式：达标数/上限）。<br />
          <b>第一个时间</b>：最近一次按“已收盘分钟”确认的时间，监控池增删只在这时发生。<br />
          <b>第二个时间</b>（闪烁）：盘中每 10 秒按未收盘分钟重算事实面板和分数，会变动，不进入战绩。<br />
          <b>临时名单</b>：启动时 1m 窗口补齐前，用 5m K 线近似（实测与精确结果一致）。<br />
          圆点：绿 = 实时推送已连接；黄 = 启动/回填中；红 = 断开，自动重连中。
        </InfoTip>
      </div>
      <LayoutMenu v-if="desktop" />
    </div>
  </header>
  <div class="banner" v-if="store.error">后端不可用：{{ store.error }}</div>
  <main><RouterView /></main>

  <!-- mobile bottom tab bar -->
  <nav class="tabbar" aria-label="主导航">
    <RouterLink v-for="n in nav" :key="n.to" :to="n.to" class="tab" :class="{ exact: n.exact }">
      <component :is="n.icon" :size="20" /><span>{{ n.short || n.label }}</span>
    </RouterLink>
  </nav>
</template>

<style>
.topbar {
  position: sticky; top: 0; z-index: 10; flex: none; display: flex; align-items: center; gap: 16px; height: 52px; padding: 0 14px;
  background: var(--bg-0); border-bottom: 1px solid var(--line);
}
.brand { display: flex; align-items: center; gap: 8px; text-decoration: none !important; color: var(--text-1); padding-right: 6px; }
.mark { width: 26px; height: 26px; }
.word { display: grid; line-height: 1.05; font-weight: 700; font-size: var(--fs-lg); letter-spacing: .5px; }
.word small { font-size: 10px; font-weight: 500; color: var(--text-3); letter-spacing: .3px; }

.nav { display: flex; gap: 2px; }
.nav-i {
  display: inline-flex; align-items: center; gap: 6px; height: 32px; padding: 0 11px; border-radius: 8px; color: var(--text-3);
  font-weight: 500; text-decoration: none !important; transition: color .12s, background .12s;
}
@media (hover: hover) { .nav-i:hover { color: var(--text-1); background: rgba(255, 255, 255, .04); } }
.nav-i .ni { transition: color .12s; }
.nav-i.router-link-active:not(.exact), .nav-i.exact.router-link-exact-active {
  color: var(--text-1); background: var(--bg-3); box-shadow: 0 0 0 1px var(--line-2) inset;
}
.nav-i.router-link-active:not(.exact) .ni, .nav-i.exact.router-link-exact-active .ni { color: var(--accent); }

.right { margin-left: auto; display: flex; align-items: center; gap: 10px; min-width: 0; }
.pulse { display: flex; gap: 4px; }
.pp { display: inline-flex; align-items: center; gap: 6px; height: 30px; padding: 0 10px; border-radius: 8px; color: var(--text-2); font-size: var(--fs-sm); text-decoration: none !important; }
@media (hover: hover) { .pp:hover { background: rgba(255, 255, 255, .04); } }
.pp .ps { color: var(--text-3); font-weight: 600; font-size: var(--fs-xs); }
.pp .pv { color: var(--text-1); font-weight: 600; }
.pp .pr { font-size: var(--fs-xs); font-weight: 600; }
.pp .pr.up { color: var(--up); } .pp .pr.down { color: var(--down); }

.status {
  display: inline-flex; align-items: center; gap: 8px; height: 30px; padding: 0 10px; border-radius: 999px;
  background: var(--bg-0); border: 1px solid var(--line); font-size: var(--fs-sm); color: var(--text-2); white-space: nowrap;
}
.status .sv b { color: var(--text-1); font-size: var(--fs-md); }
.status .dim { color: var(--text-3); }
.status .sep { width: 1px; height: 14px; background: var(--line-2); }
.status .tm { font-family: ui-monospace, "SF Mono", Menlo, monospace; font-size: var(--fs-xs); color: var(--text-2); }
.status .tm.live { color: var(--accent); animation: tick 10s linear infinite; }
@keyframes tick { 0%, 92% { opacity: 1; } 96% { opacity: .35; } 100% { opacity: 1; } }

.tabbar { display: none; }

@media (max-width: 900px) {
  .topbar { height: 48px; gap: 10px; padding: 0 12px; }
  .nav { display: none; }
  .word small { display: none; }
  .tabbar {
    display: grid; grid-template-columns: repeat(5, 1fr); position: fixed; left: 0; right: 0; bottom: 0; z-index: 20;
    padding: 6px 4px calc(6px + env(safe-area-inset-bottom)); background: rgba(0, 0, 0, .94); backdrop-filter: blur(10px);
    border-top: 1px solid var(--line);
  }
  .tab { display: grid; justify-items: center; gap: 2px; color: var(--text-3); font-size: 10px; text-decoration: none !important; padding: 2px 0; }
  .tab.router-link-active:not(.exact), .tab.exact.router-link-exact-active { color: var(--accent); }
  #app > main { padding-bottom: calc(64px + env(safe-area-inset-bottom)); }
}
</style>

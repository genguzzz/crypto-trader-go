<script setup lang="ts">
import { computed } from 'vue'
import { store } from './store'
import { time, usd } from './fmt'
const clock = (ms?: number) => !ms ? "—" : new Date(ms).toLocaleTimeString('zh-CN', { hour12: false })

const s = computed(() => store.snap)
const phaseText = computed(() => {
  const p = s.value?.phase
  return p === 'live' ? '实时' : p === 'backfill-1m' ? '回填 1m' : p === 'bootstrap-5m' ? '快速建池' : '启动中'
})
</script>

<template>
  <header class="topbar">
    <div class="brand">OKX 研究台</div>
    <nav>
      <RouterLink to="/">扫描</RouterLink>
      <RouterLink to="/rules">规则与战绩</RouterLink>
      <RouterLink to="/positions">仓位</RouterLink>
      <RouterLink to="/settings">设置</RouterLink>
      <RouterLink to="/status">系统</RouterLink>
    </nav>
    <div class="pool" v-if="s">
      <span class="dot" :class="store.connected ? 'ok' : 'bad'" :title="store.connected ? '推送已连接' : '推送断开，自动重连中'"></span>
      <b>{{ s.coverage.active }}</b><span class="muted">/{{ s.topN }} 达标</span>
      <InfoTip class="hide-sm">
        <b>达标</b>：12h USDT 成交额（连续 720 根已确认 1m K 线）严格大于阈值、按成交额取前 N 的合约数。<br />
        <b>确认</b>：最近一次按“已收盘分钟”计算的时间，监控池增删只在这时发生。<br />
        <b>盘中</b>：事实面板和分数每 10 秒按未收盘分钟重算（会变动），不进入战绩。<br />
        <b>临时名单</b>：启动时 1m 窗口补齐前，用 5m K 线近似（实测与精确结果一致）。<br />
        绿点 = 实时推送已连接；红点 = 断开，自动重连中。
      </InfoTip>
      <span class="muted hide-sm">· 12h &gt; {{ usd(s.threshold) }} USDT</span>
      <span class="muted">· 确认 {{ time(s.decisionTs) }}</span>
      <span class="tag" v-if="s.preview" title="事实面板与分数每 10 秒按当前未收盘分钟重算（会变动）；战绩只记录分钟确认值">盘中 {{ clock(s.previewAt) }} · 每10秒</span>
      <span class="tag" :class="s.phase === 'live' ? '' : 'warn'">{{ phaseText }}</span>
      <span class="tag warn" v-if="s.provisional" title="1m 窗口未补齐前，名单来自 5m 近似（实测与 1m 一致但未经确认）">临时名单</span>
    </div>
  </header>
  <div class="banner" v-if="store.error">后端不可用：{{ store.error }}</div>
  <main><RouterView /></main>
</template>

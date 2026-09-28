<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { CheckCircle2, AlertTriangle, XCircle } from 'lucide-vue-next'
import { api, type Storage } from '../api'
import { time, ago, usd } from '../fmt'
import Loading from '../components/ui/Loading.vue'
import Meter from '../components/ui/Meter.vue'
import SectionNav, { type NavItem } from '../components/status/SectionNav.vue'
import StorageDetail from '../components/status/StorageDetail.vue'
import { bytes, count, type SystemStatus, type Tone } from '../components/status/storage'

const s = ref<SystemStatus | null>(null)
const storage = ref<Storage | null>(null)
const storageLoaded = ref(false)
const err = ref('')
const now = ref(Date.now())
const route = useRoute()
const nav = ref<InstanceType<typeof SectionNav> | null>(null)

let timer: number | undefined
let storageTimer: number | undefined
const load = async () => {
  try { s.value = await api.status(); err.value = '' } catch (e: any) { err.value = e.message }
  now.value = Date.now()
}
const loadStorage = async () => {
  try { storage.value = await api.storage() } catch { /* keep the last report */ }
  storageLoaded.value = true
}
onMounted(async () => {
  await Promise.all([load(), loadStorage()])
  timer = window.setInterval(load, 3000)
  storageTimer = window.setInterval(loadStorage, 60_000)
  const id = route.hash.slice(1) // e.g. /status#storage from the settings page
  if (id) nextTick(() => nav.value?.go(id))
})
onUnmounted(() => { clearInterval(timer); clearInterval(storageTimer) })

const worst = (...t: Tone[]): Tone => (t.includes('bad') ? 'bad' : t.includes('warn') ? 'warn' : 'ok')
const phaseLabel: Record<string, string> = { live: '实时', 'backfill-1m': '1m 回填中', 'bootstrap-5m': '快速建池', starting: '启动中' }
const jobState = (m: string): { t: string; c: string } =>
  m.startsWith('完成') ? { t: '完成', c: 'good' } : m.startsWith('等待') || m.startsWith('排队') ? { t: '等待', c: '' } : { t: '进行中', c: 'info' }

// ---- health model: every check maps to a section so the summary can jump there ----
const h = computed(() => {
  const x = s.value
  if (!x) return null
  const live = x.phase === 'live'
  const lagMs = now.value - (x.asOfMinute + 60_000) // how old the latest confirmed minute is
  const ws = x.ws || []
  const wsUp = ws.filter(w => w.connected).length
  const wsSlow = ws.filter(w => w.connected && w.lastMsgAgoMs > 30_000)
  const cov = x.coverage
  const errRate = x.restRequests ? x.restErrors / x.restRequests : 0
  const used = storage.value ? storage.value.usedBytes / storage.value.capBytes : 0
  const tight = storage.value?.tables?.filter(t => t.tight).length ?? 0

  const issues: { tone: Tone; text: string; section: string }[] = []
  if (!live) issues.push({ tone: 'warn', text: phaseLabel[x.phase] || x.phase, section: 'run' })
  if (live && lagMs > 180_000) issues.push({ tone: 'bad', text: `数据已 ${ago(x.asOfMinute + 60_000)} 未更新`, section: 'run' })
  if (wsUp < ws.length) issues.push({ tone: 'bad', text: `${ws.length - wsUp} 条推送断开`, section: 'conn' })
  if (wsSlow.length) issues.push({ tone: 'warn', text: `${wsSlow.map(w => w.name).join('/')} 推送延迟`, section: 'conn' })
  if (cov.stale) issues.push({ tone: cov.stale > cov.eligible * 0.05 ? 'bad' : 'warn', text: `${cov.stale} 个合约数据缺口`, section: 'coverage' })
  if (errRate > 0.01) issues.push({ tone: 'warn', text: `REST 错误率 ${(errRate * 100).toFixed(1)}%`, section: 'conn' })
  if (used >= 0.9) issues.push({ tone: used >= 1 ? 'bad' : 'warn', text: `存储已用 ${(used * 100).toFixed(0)}%`, section: 'storage' })
  if (tight) issues.push({ tone: 'warn', text: `${tight} 类数据保留期被收紧`, section: 'storage' })

  const tone = (sec: string) => worst('ok', ...issues.filter(i => i.section === sec).map(i => i.tone))
  return {
    issues, overall: worst('ok', ...issues.map(i => i.tone)), tone,
    lagMs, lagTone: (!live ? 'warn' : lagMs > 180_000 ? 'bad' : lagMs > 90_000 ? 'warn' : 'ok') as Tone,
    wsUp, wsTotal: ws.length, lastMsg: Math.max(0, ...ws.map(w => w.lastMsgAgoMs)), used,
  }
})

const navItems = computed<NavItem[]>(() => [
  { id: 'overview', label: '概览', tone: h.value?.overall },
  { id: 'run', label: '运行', tone: h.value?.tone('run') },
  { id: 'coverage', label: '覆盖', tone: h.value?.tone('coverage') },
  { id: 'conn', label: 'OKX 连接', tone: h.value?.tone('conn') },
  { id: 'storage', label: '数据存储', tone: h.value?.tone('storage') },
  { id: 'proc', label: '进程' },
])

// coverage breakdown (eligible = ready + warming + stale + new)
const segs = computed(() => {
  const c = s.value?.coverage
  if (!c) return []
  return [
    { k: 'ready', label: '12h 窗口完整', n: c.ready, cls: 'ok' },
    { k: 'warming', label: '预热中', n: c.warming, cls: 'info' },
    { k: 'stale', label: '数据缺口', n: c.stale, cls: 'bad' },
    { k: 'new', label: '上市不足 12h', n: c.new, cls: 'mute' },
  ]
})
const icon = { ok: CheckCircle2, warn: AlertTriangle, bad: XCircle }
/** Age of the latest confirmed minute in seconds up to 2 minutes (ago() rounds 61s to "1m"). */
const lagText = computed(() => { const x = Math.max(0, Math.round((h.value?.lagMs ?? 0) / 1000)); return x < 120 ? x + 's' : Math.round(x / 60) + 'm' })
</script>

<template>
  <div v-if="s && h" class="sys">
    <SectionNav ref="nav" :items="navItems" class="side" />

    <div class="content">
      <!-- 概览: one glance — is everything OK, plus the few numbers that matter -->
      <section id="overview" class="panel hero" :class="h.overall">
        <div class="head">
          <component :is="icon[h.overall]" :size="22" class="hi" />
          <div class="ht">
            <div class="title">{{ h.overall === 'ok' ? '运行正常' : `${h.issues.length} 项需要关注` }}</div>
            <div class="subline">
              <span class="tag" :class="s.phase === 'live' ? 'good' : 'warn'">{{ phaseLabel[s.phase] || s.phase }}</span>
              <span>已运行 {{ s.uptime }}</span>
              <span v-if="err" class="down">刷新失败：{{ err }}</span>
            </div>
          </div>
          <InfoTip>
            <b>健康检查</b>（每 3 秒刷新）：<br />
            · 阶段不是“实时”、最新确认分钟超过 3 分钟没更新 → 需关注 / 异常；<br />
            · WS 推送断开 → 异常；已连接但 30 秒没有消息 → 需关注；<br />
            · 有合约出现数据缺口（缺分钟不参与排名）→ 需关注，超过 5% → 异常；<br />
            · REST 错误率超过 1%、存储用量 ≥ 90% 或有数据类型的保留期被收紧 → 需关注。<br />
            点击问题标签跳到对应分区。
          </InfoTip>
        </div>
        <div v-if="h.issues.length" class="issues">
          <button v-for="i in h.issues" :key="i.text" type="button" class="tag issue" :class="i.tone === 'bad' ? 'bad' : 'warn'" @click="nav?.go(i.section)">{{ i.text }} →</button>
        </div>

        <div class="kpis">
          <div class="kpi">
            <div class="kl"><span class="dot" :class="h.lagTone"></span>数据新鲜度</div>
            <div class="kv tnum">{{ lagText }}<small> 前</small></div>
            <div class="ks">最新确认 {{ time(s.asOfMinute + 60_000) }}</div>
          </div>
          <div class="kpi">
            <div class="kl"><span class="dot ok"></span>活跃池</div>
            <div class="kv tnum">{{ s.coverage.active }}<small> / {{ s.config.topN }}</small></div>
            <div class="ks">12h &gt; {{ usd(s.config.threshold) }}</div>
          </div>
          <div class="kpi">
            <div class="kl"><span class="dot" :class="h.tone('coverage')"></span>数据缺口</div>
            <div class="kv tnum" :class="{ down: s.coverage.stale }">{{ s.coverage.stale }}</div>
            <div class="ks">完整 {{ s.coverage.ready }} / {{ s.coverage.eligible }}</div>
          </div>
          <div class="kpi">
            <div class="kl"><span class="dot" :class="h.tone('conn')"></span>实时推送</div>
            <div class="kv tnum">{{ h.wsUp }}<small> / {{ h.wsTotal }} 在线</small></div>
            <div class="ks">最近消息 {{ (h.lastMsg / 1000).toFixed(1) }}s</div>
          </div>
          <div class="kpi">
            <div class="kl"><span class="dot" :class="h.tone('storage')"></span>存储</div>
            <template v-if="storage">
              <div class="kv tnum">{{ bytes(storage.usedBytes) }}</div>
              <div class="ks"><Meter :value="storage.usedBytes" :max="storage.capBytes" :height="4" class="km" />上限 {{ bytes(storage.capBytes) }}</div>
            </template>
            <template v-else><div class="kv muted">—</div><div class="ks">首次检查中</div></template>
          </div>
        </div>
      </section>

      <!-- 运行 -->
      <section id="run" class="panel sec">
        <h3>运行 <InfoTip>
          <b>1m 回填</b>：启动时为全部可交易合约补齐最近 12h 的 1m K 线，完成后进入“实时”。<br />
          <b>历史回放</b>：用近 {{ Math.round(s.config.replayHours / 24) }} 天的整点横截面重算各规则的战绩（改设置或规则后后台重跑）。<br />
          <b>1m 历史回补</b>：为活跃池与自选补齐近 7 天 1m K 线，供图表与研究使用。
        </InfoTip></h3>
        <div class="kv-list">
          <div class="row">
            <span class="k">1m 回填</span>
            <span class="v">
              <span class="tnum">{{ s.backfillDone }} / {{ s.backfillTotal }}</span>
              <span v-if="s.backfillTotal && s.backfillDone >= s.backfillTotal" class="tag good">完成</span>
              <Meter v-else :value="s.backfillDone" :max="s.backfillTotal" :height="4" tone="ok" class="inline-meter" />
            </span>
          </div>
          <div class="row">
            <span class="k">最新确认分钟</span>
            <span class="v tnum">{{ time(s.asOfMinute + 60_000) }} <small class="muted">计算于 {{ ago(s.computedAt) }} 前</small></span>
          </div>
          <div class="row job">
            <span class="k">历史回放</span>
            <span class="v"><span class="tag" :class="jobState(s.replay).c">{{ jobState(s.replay).t }}</span></span>
            <span class="msg">{{ s.replay }}</span>
          </div>
          <div class="row job">
            <span class="k">1m 历史回补</span>
            <span class="v"><span class="tag" :class="jobState(s.history).c">{{ jobState(s.history).t }}</span></span>
            <span class="msg">{{ s.history }}</span>
          </div>
        </div>
      </section>

      <!-- 覆盖 -->
      <section id="coverage" class="panel sec">
        <h3>覆盖 <span class="hmeta tnum">{{ s.coverage.eligible }} 个可交易合约</span> <InfoTip>
          <b>12h 窗口完整</b>：连续 720 根已确认 1m K 线齐全，可以参与排名。<br />
          <b>预热中</b>：刚开始录制，窗口还没补齐。<b>数据缺口</b>：窗口内缺分钟（缺失不当 0），暂不参与排名。<br />
          <b>上市不足 12h</b>：新合约，满 12h 才参与。<br />
          <b>活跃池</b>：12h 成交额严格大于阈值、按成交额取前 N（在“设置 → 监控池”调整）。
        </InfoTip></h3>
        <div class="stack" role="img" aria-label="合约覆盖分布">
          <i v-for="g in segs" v-show="g.n" :key="g.k" :class="g.cls" :style="{ flexGrow: g.n }" :title="`${g.label} ${g.n}`"></i>
        </div>
        <div class="legend">
          <div v-for="g in segs" :key="g.k" class="lg" :class="{ zero: !g.n }">
            <span class="sw" :class="g.cls"></span><span class="ll">{{ g.label }}</span><b class="tnum" :class="{ down: g.k === 'stale' && g.n }">{{ g.n }}</b>
          </div>
        </div>
        <div class="pool">
          <div class="pk">活跃池</div>
          <div class="pv tnum"><b>{{ s.coverage.active }}</b> / {{ s.config.topN }} <small class="muted">12h 成交额 &gt; {{ usd(s.config.threshold) }}</small></div>
          <Meter :value="s.coverage.active" :max="s.config.topN" :height="4" tone="ok" />
        </div>
      </section>

      <!-- OKX 连接 -->
      <section id="conn" class="panel sec">
        <h3>OKX 连接 <InfoTip>
          仅使用 OKX v5 <b>公共</b>接口（无凭据、无下单）。<br />
          <b>business</b> WS：全池 1m K 线；<b>public</b> WS：全池 OI 与资金费，盘口报价只订阅活跃池和自选（全池订阅约 40GB/天，没必要）。<br />
          REST：现货行情 15 秒、永续 ticker 30 秒兜底，历史回填与补洞走独立限频。时间戳按交易所时间记录，缺失分钟不当 0。
        </InfoTip></h3>
        <div class="cards">
          <div v-for="w in s.ws || []" :key="w.name" class="card">
            <div class="ch">
              <span class="dot" :class="!w.connected ? 'bad' : w.lastMsgAgoMs > 30_000 ? 'warn' : 'ok'"></span>
              <b>{{ w.name }}</b><span class="muted">WS</span>
              <span class="cr tnum" :class="{ down: !w.connected }">{{ w.connected ? (w.lastMsgAgoMs / 1000).toFixed(1) + 's' : '断开' }}</span>
            </div>
            <div class="cs">
              <div><span>订阅</span><b class="tnum">{{ w.subscriptions }}</b></div>
              <div><span>消息</span><b class="tnum">{{ count(w.messages) }}</b></div>
              <div><span>重连</span><b class="tnum" :class="{ wn: w.reconnects }">{{ w.reconnects }}</b></div>
            </div>
            <div v-if="w.subErrors" class="note down">订阅错误 {{ w.subErrors }}</div>
          </div>
          <div class="card">
            <div class="ch"><span class="dot" :class="h.tone('conn') === 'ok' || !s.restErrors ? 'ok' : 'warn'"></span><b>REST</b><span class="muted">HTTP</span></div>
            <div class="cs">
              <div><span>请求</span><b class="tnum">{{ count(s.restRequests) }}</b></div>
              <div><span>错误</span><b class="tnum">{{ s.restErrors }}</b></div>
              <div><span>限频</span><b class="tnum" :class="{ wn: s.restRateLimited }">{{ s.restRateLimited }}</b></div>
            </div>
          </div>
        </div>
      </section>

      <!-- 数据存储 (moved here from settings: it is information, not a setting) -->
      <section id="storage" class="panel sec">
        <h3>数据存储 <InfoTip>
          <b>分层</b>（取最高）：<b>自选</b> ＞ <b>活跃池</b>（在池内或离池不满 7 天）＞ <b>最近查看</b>（3 天内打开过详情）＞ <b>全池录制</b>（设置类别内、未进池）＞ <b>未跟踪</b>（已下线/类别外且未查看）。表中各层列为保留期，列头为当前合约数。<br />
          <b>按周期分别维护</b>：1m K 线、持仓量、1H K 线、资金费、订单流行数差几个数量级，各自有保留期和容量配额（上限 × 1m 45% / 持仓量 10% / 1H 10% / 资金费 5% / 逐笔订单流 18% / 主动买卖量 2%，其余 10% 给规则与评分快照）。订单流只录活跃池、自选和正在查看的合约。<br />
          <b>淘汰</b>：每 30 分钟先按下表保留期删除；某类数据超出配额时，从最低层开始逐步缩短保留期（每次 ×2/3），直到回到配额的 90%，但不低于保底（1m/持仓量 26 小时，自选 3 天；1H 21 天），被收紧的格子会标出。<br />
          <b>下载</b>：打开详情或加自选时按需补近 7 天 1m（不超过该层保留期），离开列表的币按新层级自动淘汰。评分快照（战绩）永不删除。<br />
          删除后的空间在库文件内复用，文件本身不缩小。全池 1m 约 60 MB/天。
        </InfoTip></h3>
        <StorageDetail v-if="storage" :storage="storage" :rows="s.db" />
        <p v-else-if="storageLoaded" class="note">存储检查在服务启动约 2 分钟后首次运行。</p>
        <Loading v-else text="读取存储占用" />
      </section>

      <!-- 进程 -->
      <section id="proc" class="panel sec">
        <h3>进程 <InfoTip>Go 进程内存与协程，用于排查泄漏；“内存中合约”为保留 1m 环形缓冲的合约数。</InfoTip></h3>
        <div v-if="s.mem" class="mini">
          <div><span>堆内存</span><b class="tnum">{{ s.mem.heapAllocMB }}<small> MB</small></b></div>
          <div><span>进程内存</span><b class="tnum">{{ s.mem.sysMB }}<small> MB</small></b></div>
          <div><span>协程</span><b class="tnum">{{ s.mem.goroutines }}</b></div>
          <div><span>内存中合约</span><b class="tnum">{{ s.mem.insts }}</b></div>
          <div><span>GC 次数</span><b class="tnum">{{ count(s.mem.numGC) }}</b></div>
          <div><span>订单流解码错误</span><b class="tnum" :class="{ wn: s.mem.flowDecodeErrors }">{{ s.mem.flowDecodeErrors }}</b></div>
        </div>
      </section>
    </div>
  </div>
  <div v-else class="page"><Loading :text="err ? '后端不可用：' + err : '加载系统状态'" /></div>
</template>

<style scoped>
.sys { display: grid; grid-template-columns: 168px minmax(0, 1fr); gap: 24px; max-width: 1240px; margin: 0 auto; padding: 16px; }
.content { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; min-width: 0; }
section { scroll-margin-top: 16px; }
.panel h3 { display: flex; align-items: center; gap: 4px; }
.hmeta { font-size: var(--fs-sm); font-weight: 400; color: var(--text-3); margin-left: 4px; }
small { font-size: var(--fs-xs); }
.wn { color: var(--warn); }

/* hero */
.hero { padding: 18px 20px; }
.hero .head { display: flex; align-items: flex-start; gap: 12px; }
.hi { flex: none; margin-top: 2px; color: var(--up); }
.hero.warn .hi { color: var(--warn); } .hero.bad .hi { color: var(--down); }
.hero.warn { border-color: var(--warn-bg); } .hero.bad { border-color: var(--down-bg); }
.ht { flex: 1; min-width: 0; }
.title { font-size: var(--fs-xl); font-weight: 600; color: var(--text-1); }
.subline { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-top: 4px; font-size: var(--fs-sm); color: var(--text-3); }
.issues { display: flex; flex-wrap: wrap; gap: 6px; margin: 12px 0 0 34px; }
.issue { border: none; cursor: pointer; height: 24px; padding: 0 8px; font: inherit; font-size: var(--fs-xs); }
.kpis { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); margin-top: 18px; border-top: 1px solid var(--line); padding-top: 16px; }
.kpi { min-width: 0; padding: 0 16px; border-left: 1px solid var(--line); }
.kpi:first-child { padding-left: 0; border-left: none; }
.kl { display: flex; align-items: center; gap: 8px; font-size: var(--fs-xs); color: var(--text-3); white-space: nowrap; }
.kv { font-size: var(--fs-3xl); font-weight: 700; color: var(--text-1); line-height: 1.25; margin-top: 6px; white-space: nowrap; }
.kv small { font-size: var(--fs-md); font-weight: 500; color: var(--text-3); }
.kv.down { color: var(--down); }
.ks { font-size: var(--fs-xs); color: var(--text-2); margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.km { margin: 6px 0 4px; }

/* key-value rows */
.kv-list { display: grid; }
.row { display: grid; grid-template-columns: 120px minmax(0, 1fr); align-items: center; gap: 4px 12px; padding: 9px 0; border-bottom: 1px solid var(--line); }
.row:last-child { border-bottom: none; }
.row .k { font-size: var(--fs-sm); color: var(--text-3); }
.row .v { display: flex; align-items: center; gap: 8px; color: var(--text-1); min-width: 0; }
.row.job .msg { grid-column: 2; font-size: var(--fs-xs); color: var(--text-2); overflow-wrap: anywhere; }
.inline-meter { max-width: 200px; }

/* coverage */
.stack { display: flex; gap: 2px; height: 10px; border-radius: 999px; overflow: hidden; background: var(--bg-3); }
.stack i { display: block; min-width: 3px; }
.ok { --c: var(--up); } .info { --c: var(--info); } .bad { --c: var(--down); } .mute { --c: var(--text-3); }
.stack i, .sw { background: var(--c); }
.legend { display: flex; flex-wrap: wrap; gap: 8px 28px; margin-top: 12px; }
.lg { display: flex; align-items: center; gap: 6px; font-size: var(--fs-sm); min-width: 0; }
.lg.zero { opacity: .5; }
.sw { width: 8px; height: 8px; border-radius: 2px; flex: none; }
.ll { color: var(--text-3); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.lg b { color: var(--text-1); margin-left: 2px; }
.lg b.down { color: var(--down); }
.pool { display: grid; gap: 6px; margin-top: 16px; padding-top: 14px; border-top: 1px solid var(--line); }
.pk { font-size: var(--fs-xs); color: var(--text-3); }
.pv b { font-size: var(--fs-xl); }
.pv small { margin-left: 8px; }

/* connection cards */
.cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(220px, 100%), 1fr)); gap: 10px; }
.card { background: var(--bg-0); border: 1px solid var(--line); border-radius: 8px; padding: 10px 12px; min-width: 0; }
.ch { display: flex; align-items: center; gap: 8px; font-size: var(--fs-md); }
.ch .muted { font-size: var(--fs-xs); }
.cr { margin-left: auto; font-size: var(--fs-xs); color: var(--text-2); }
.cs, .mini { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin-top: 10px; }
.cs > div, .mini > div { display: grid; gap: 2px; min-width: 0; }
.cs span, .mini span { font-size: var(--fs-xs); color: var(--text-3); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.cs b { font-size: var(--fs-lg); font-weight: 600; }
.mini { grid-template-columns: repeat(6, minmax(0, 1fr)); margin-top: 0; }
.mini b { font-size: var(--fs-xl); font-weight: 600; }
.mini small { font-weight: 400; color: var(--text-3); }

@media (max-width: 1100px) {
  .kpis { grid-template-columns: repeat(3, minmax(0, 1fr)); row-gap: 16px; }
  .kpi:nth-child(4) { padding-left: 0; border-left: none; }
  .mini { grid-template-columns: repeat(3, minmax(0, 1fr)); row-gap: 14px; }
}
@media (max-width: 900px) {
  .sys { grid-template-columns: minmax(0, 1fr); gap: 12px; padding: 0 16px 16px; }
  section { scroll-margin-top: 104px; }
  .hero { padding: 16px; }
  .issues { margin-left: 0; }
  .kpis { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .kpi, .kpi:nth-child(4) { padding: 0 12px; border-left: 1px solid var(--line); }
  .kpi:nth-child(odd) { padding-left: 0; border-left: none; }
  .kpi:last-child { grid-column: 1 / -1; padding-left: 0; border-left: none; }
  .kv { font-size: var(--fs-2xl); }
  .legend { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px 16px; }
  .lg b { margin-left: auto; }
  .row { grid-template-columns: 96px minmax(0, 1fr); }
}
</style>

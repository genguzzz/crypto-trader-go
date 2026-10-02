// Mock server for the trading UI (no dependencies). Serves realistic fake JSON for every endpoint
// of docs/trading-design.md §4 (trade / interact / ai), proxies everything else to a real backend,
// and serves the built web app (web/dist) with an SPA fallback — so the trading pages can be used
// and screenshotted before the backend exists.
//
//   cd web && npm run build && node scripts/mock-trade.mjs
//   PORT=5180 BACKEND=http://127.0.0.1:8686 DIST=dist node scripts/mock-trade.mjs
//
// PASSTHROUGH=1 BACKEND=http://127.0.0.1:8787 node scripts/mock-trade.mjs serves the fresh build
// against a real server instead. When the backend is unreachable, a few core endpoints (meta, universe, catalog, settings, stream…)
// fall back to small stubs so the app shell still renders. State-changing requests under the three
// prefixes require a non-empty X-CTG-Admin header (any value), like the real server.
// With `npm run dev`, point Vite at the mock instead: CTG_API=http://127.0.0.1:5180 npm run dev.
import http from 'node:http'
import https from 'node:https'
import { readFile, stat } from 'node:fs/promises'
import { extname, join, normalize, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const PORT = Number(process.env.PORT || 5180)
const BACKEND = new URL(process.env.BACKEND || 'http://127.0.0.1:8686')
const DIST = process.env.DIST || join(here, '..', 'dist')
// PASSTHROUGH=1: no fake data — serve this checkout's web/dist and proxy every /api call to BACKEND
// (UI work against a real server whose embedded web build is older).
const PASSTHROUGH = process.env.PASSTHROUGH === '1'

// ---------- deterministic randomness (stable screenshots) ----------
let seed = 42
const rnd = () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 2 ** 32)
const gauss = () => Math.sqrt(-2 * Math.log(rnd() + 1e-12)) * Math.cos(2 * Math.PI * rnd())
const pick = xs => xs[Math.floor(rnd() * xs.length)]

const MIN = 60_000, HOUR = 3_600_000, DAY = 86_400_000
const now = () => Date.now()
const T0 = now()

// ---------- static data ----------
const PX = { 'BTC-USDT-SWAP': 118420.5, 'ETH-USDT-SWAP': 4386.21, 'SOL-USDT-SWAP': 212.874, 'DOGE-USDT-SWAP': 0.24871, 'XRP-USDT-SWAP': 2.8734, 'SUI-USDT-SWAP': 3.6125, 'LINK-USDT-SWAP': 22.418, 'AVAX-USDT-SWAP': 29.734, 'TON-USDT-SWAP': 3.1182, 'PEPE-USDT-SWAP': 0.00001102 }
const CTVAL = { 'BTC-USDT-SWAP': 0.01, 'ETH-USDT-SWAP': 0.1, 'SOL-USDT-SWAP': 1, 'DOGE-USDT-SWAP': 1000, 'XRP-USDT-SWAP': 100, 'SUI-USDT-SWAP': 1, 'LINK-USDT-SWAP': 1, 'AVAX-USDT-SWAP': 1, 'TON-USDT-SWAP': 1, 'PEPE-USDT-SWAP': 10000000 }
const RULES = [
  { id: 'trend-oi-confirm', name: '趋势 + OI 确认', version: 3, description: '4h 趋势强度与持仓量同向增加，过滤点差过大的合约', preset: true, long: { trend_z_4h: 1, oi_chg_4h: 0.5 }, short: { trend_z_4h: -1, oi_chg_4h: 0.5 }, gates: { maxSpreadBps: 8 } },
  { id: 'momentum-24h', name: '24h 动量', version: 1, description: '24h 收益与相对 BTC 超额收益', preset: true, long: { ret_24h: 1, excess_btc_4h: 0.5 }, short: { ret_24h: -1 }, gates: {} },
  { id: 'my-breakout', name: '放量突破', version: 2, description: '放量 + 4h 收益，我的规则', preset: false, long: { vol_surprise_4h: 1, ret_4h: 1 }, short: { vol_surprise_4h: 1, ret_4h: -1 }, gates: { maxSpreadBps: 10 } },
]

const CODE = name => `from ctg import Strategy, Param, Signal, ta


class ${name.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('')}(Strategy):
    """${name}: example strategy shipped with the mock server."""
    name = "${name}"
    description = "EMA crossover with an ATR stop"
    universe = ["BTC-USDT-SWAP", "ETH-USDT-SWAP"]
    timeframe = "4H"

    fast = Param(20, min=5, max=100, step=1, desc="fast EMA length", ai_tunable=True)
    slow = Param(60, min=20, max=300, step=1, desc="slow EMA length")
    atr_mult = Param(2.0, min=0.5, max=5, step=0.1, desc="stop distance in ATR(14)")

    def on_bar(self, ctx, bar):
        b = ctx.bars(bar.inst, self.timeframe, self.slow + 5)
        fast, slow = ta.ema(b.c, self.fast), ta.ema(b.c, self.slow)
        pos = ctx.position(bar.inst)
        if ta.crossover(fast, slow) and not pos:
            return Signal.open_long(bar.inst, stop_loss=f"{self.atr_mult}atr",
                                    reason=f"EMA{self.fast} crossed above EMA{self.slow}")
        if ta.crossunder(fast, slow) and pos:
            return Signal.close(bar.inst, reason="trend reversed")
`
const P = (name, def, min, max, step, desc, aiTunable) => ({ name, default: def, min, max, step, desc, aiTunable })
const VERSIONS = [
  { id: '9f8e7d6c5b4a', name: 'majors-trend', file: 'library/majors_trend.py', description: '主流币 4H 趋势跟随：EMA 金叉开多、死叉平仓，ATR 止损', timeframe: '4H',
    params: [P('fast', 20, 5, 100, 1, '快速 EMA 周期', true), P('slow', 60, 20, 300, 1, '慢速 EMA 周期'), P('atr_mult', 2, 0.5, 5, 0.1, '止损距离（ATR 倍数）', true)],
    defaultUniverse: { static: ['BTC-USDT-SWAP', 'ETH-USDT-SWAP'] }, createdAt: T0 - 3 * DAY },
  { id: 'a1b2c3d4e5f6', name: 'majors-trend', file: 'library/majors_trend.py', description: '主流币 4H 趋势跟随：EMA 金叉开多、死叉平仓', timeframe: '4H',
    params: [P('fast', 20, 5, 100, 1, '快速 EMA 周期', true), P('slow', 50, 20, 300, 1, '慢速 EMA 周期')],
    defaultUniverse: { static: ['BTC-USDT-SWAP', 'ETH-USDT-SWAP'] }, createdAt: T0 - 30 * DAY },
  { id: '3c4d5e6f7a8b', name: 'alt-momentum', file: 'library/alt_momentum.py', description: '按评分规则取多头分最高的山寨币，1H 动量突破入场，2ATR 止损、4ATR 止盈', timeframe: '1H',
    params: [P('lookback', 24, 6, 96, 1, '突破回看 K 线数', true), P('stop_atr', 2, 0.5, 5, 0.1, '止损 ATR 倍数', true), P('target_atr', 4, 1, 10, 0.5, '止盈 ATR 倍数'), P('min_score', 70, 0, 100, 1, '入场最低分')],
    defaultUniverse: { screener: { rule: 'trend-oi-confirm', side: 'long', top: 20, minScore: 60, refresh: '1H' } }, createdAt: T0 - 9 * DAY },
  { id: '7d8e9f0a1b2c', name: 'funding-reversal', file: 'library/funding_reversal.py', description: '资金费率极端时反向开仓，持有若干根 K 线后平仓', timeframe: '1H',
    params: [P('threshold', 0.0005, 0.0001, 0.005, 0.0001, '每 8h 资金费阈值'), P('hold_bars', 8, 1, 48, 1, '持有 K 线数')],
    defaultUniverse: { static: ['BTC-USDT-SWAP', 'ETH-USDT-SWAP', 'SOL-USDT-SWAP', 'DOGE-USDT-SWAP'] }, createdAt: T0 - 14 * DAY },
]
const verById = id => VERSIONS.find(v => v.id === id)
const LIMITS = { maxPositions: 5, maxLeverage: 5, riskPct: 0.01, maxInstPct: 0.3, dailyLossPct: 0.05, slippageBp: 10, cooldownMin: 30, takerFeeBp: 5, makerFeeBp: 2, requireStop: true, confirm: 'off', confirmTimeoutS: 120, confirmDefault: 'skip' }

// ---------- mutable state ----------
const db = { accounts: [], instances: [], positions: [], orders: [], fills: [], signals: [], ledger: [], equity: {}, prompts: [], decisions: [], proposals: [], ids: { acc: 10, inst: 20, ord: 1000, fill: 5000, sig: 9000, led: 20000, dec: 300, prop: 40 } }
// internal/aigate Status (PROXY_DOWN=1 simulates the worker's proxy failing)
const PROXY_DOWN = process.env.PROXY_DOWN === '1'
const ai = {
  configured: true, kind: 'oauth_token', masked: '••••x7Qa', storage: 'encrypted', updatedAt: T0 - 5 * DAY, model: 'claude-sonnet-4-5',
  workerOnline: true, workerReady: !PROXY_DOWN, proxyOk: !PROXY_DOWN, proxyError: PROXY_DOWN ? 'CONNECT 127.0.0.1:7080: connection refused' : undefined,
  worker: { version: '1', sdk: '0.1.4', cli: '2.1.0', ready: !PROXY_DOWN, proxyOk: !PROXY_DOWN, connectedAt: T0 - HOUR },
  available: !PROXY_DOWN, lastCallAt: T0 - 4 * MIN, costToday: 0.84, callsToday: 12, pendingProposals: 1,
}
const memory = { 'alt-momentum': [{ strategy: 'alt-momentum', version: 2, ts: T0 - DAY, doc: '1. 4h 涨幅超过 15% 后的突破信号胜率明显偏低（7 次中 2 次盈利），这类信号应缩小仓位或跳过。\n2. 点差 > 8bp 的小币滑点经常吃掉一半以上的预期收益。\n3. BTC 同步走强时的突破更可靠；BTC 走弱时山寨突破多数在 3 根 K 线内回落。', source: 'reflect', throughDecision: 309, decisions: 40, model: 'claude-sonnet-4-5', costUsd: 0.12, auditId: 310 }] }
// internal/interact ConfigView
const ALL_KINDS = [['signal', '信号'], ['order', '下单'], ['fill', '成交'], ['cancel', '撤单'], ['stop', '止损'], ['target', '止盈'], ['liquidation', '强平'], ['reject', '拒单'], ['breaker', '熔断'], ['instance', '策略实例'], ['ai', 'AI'], ['daily', '日报']].map(([kind, label]) => ({ kind, label }))
const ix = {
  telegram: { enabled: true, botToken: '••••3kLm', configured: true, allowUserIds: [123456789] },
  kinds: ['breaker', 'daily', 'fill', 'instance', 'liquidation', 'stop', 'target'], quiet: false,
  status: { state: 'ok', bot: 'ctg_desk_bot', lastPollAt: T0, recipients: 1, backlog: 0, strangers: [{ id: 555000111, name: 'Alice', at: T0 - 20 * MIN }] },
  allKinds: ALL_KINDS,
}

function equitySeries(deposit, from, to, step, drift, vol) {
  const out = []
  let e = deposit
  for (let t = from; t <= to; t += step) { e *= 1 + drift + vol * gauss(); out.push([t, +e.toFixed(2)]) }
  return out
}
function addAccount(a) {
  const acc = { id: a.id ?? ++db.ids.acc, archived: false, createdAt: now(), equity: a.deposit, cash: a.deposit, dayPnl: 0, halted: false, openCount: 0, instances: 0, ...a, limits: { ...LIMITS, ...(a.limits || {}) } }
  db.accounts.push(acc)
  db.ledger.push({ id: ++db.ids.led, accountId: acc.id, kind: 'deposit', amount: acc.deposit, ts: acc.createdAt })
  return acc
}
function addInstance(accountId, versionId, over = {}) {
  const v = verById(versionId) || VERSIONS[0]
  const inst = { id: ++db.ids.inst, accountId, versionId: v.id, name: v.name, params: Object.fromEntries(v.params.map(p => [p.name, p.default])), universe: JSON.parse(JSON.stringify(v.defaultUniverse)), exec: 'auto', state: 'created', configVersion: 1, heartbeat: 0, createdAt: now(), ...over }
  db.instances.push(inst)
  return inst
}
function addPosition(accountId, instanceId, instId, side, qty, entryPx, lev, sl, tp) {
  const mark = PX[instId], ct = CTVAL[instId]
  const notional = qty * ct * entryPx, margin = notional / lev
  const dir = side === 'long' ? 1 : -1
  const liq = entryPx * (1 - dir * (1 / lev - 0.005))
  db.positions.push({ accountId, instanceId, instId, posSide: side, qty, ctVal: ct, entryPx, leverage: lev, margin: +margin.toFixed(2), liqPx: +liq.toPrecision(6), markPx: mark, unrealizedPnl: +(dir * (mark - entryPx) * qty * ct).toFixed(2), stopLoss: sl, takeProfit: tp, openedAt: now() - Math.floor(rnd() * 40) * HOUR })
  const grp = 'oco-' + (++db.ids.ord)
  for (const [type, px] of [['stop', sl], ['target', tp]]) {
    if (!px) continue
    db.orders.push({ id: ++db.ids.ord, accountId, instanceId, instId, side: side === 'long' ? 'sell' : 'buy', posSide: side, type, qty, triggerPx: px, reduceOnly: true, ocoGroup: grp, leverage: lev, state: 'waiting_trigger', filled: 0, createdAt: now() - 2 * HOUR, updatedAt: now() - 2 * HOUR })
  }
}
const REASONS = { open_long: ['EMA20 上穿 EMA60，4H 收盘确认', '突破 24 根 K 线高点，放量 2.3 倍', '多头分 84，进入前 5'], open_short: ['资金费 0.12%/8h 极端，反向开空', 'EMA20 下穿 EMA60'], close: ['趋势反转：EMA20 下穿 EMA60', '持有 8 根 K 线到期'], reduce: ['盈利超过 3ATR，减半锁定利润'] }
const OUT = [
  ['ordered', ''], ['ordered', ''], ['ordered', ''], ['rejected', '已达最大持仓数 5'], ['rejected', '冷却中：30 分钟内平过 SOL-USDT-SWAP'], ['rejected', '仓位低于最小下单量（0.01 张）'],
  ['skipped_ai', 'AI：24h 已涨 18%，追高风险大，跳过'], ['skipped_user', 'Telegram @owner 选择跳过'], ['expired', '确认超时（120 秒），按默认跳过'], ['recorded', ''],
]
function genSignals(acc, insts, n, span) {
  for (let i = 0; i < n; i++) {
    const inst = pick(insts)
    const instId = inst.universe.static?.length ? pick(inst.universe.static) : pick(Object.keys(PX))
    const action = pick(['open_long', 'open_long', 'open_short', 'close', 'reduce'])
    let [outcome, why] = inst.exec === 'record' ? ['recorded', ''] : pick(OUT)
    if (outcome === 'recorded' && inst.exec !== 'record') outcome = 'ordered'
    const px = PX[instId] * (1 + 0.03 * gauss())
    const ts = now() - Math.floor(rnd() * span / HOUR) * HOUR
    const opening = action.startsWith('open')
    db.signals.push({ id: ++db.ids.sig, instanceId: inst.id, accountId: acc.id, instId, action, strength: opening ? +(0.5 + rnd() * 0.5).toFixed(2) : 1, stopLoss: opening ? +(px * (action === 'open_long' ? 0.96 : 1.04)).toPrecision(6) : undefined, takeProfit: opening ? +(px * (action === 'open_long' ? 1.08 : 0.92)).toPrecision(6) : undefined, reason: pick(REASONS[action]), ts, recvTs: ts + 1200, price: +px.toPrecision(6), outcome, outcomeReason: why || undefined, configVersion: inst.configVersion, aiDecisionId: undefined })
  }
}
function genFills(acc, insts, n, span) {
  for (let i = 0; i < n; i++) {
    const inst = pick(insts)
    const instId = inst.universe.static?.length ? pick(inst.universe.static) : pick(Object.keys(PX))
    const posSide = rnd() < 0.7 ? 'long' : 'short'
    const closing = rnd() < 0.5
    const side = (posSide === 'long') !== closing ? 'buy' : 'sell'
    const px = PX[instId] * (1 + 0.04 * gauss()), qty = Math.max(1, Math.round(rnd() * 20))
    const fee = px * qty * CTVAL[instId] * 0.0005
    db.fills.push({ id: ++db.ids.fill, orderId: ++db.ids.ord, accountId: acc.id, instanceId: inst.id, instId, side, posSide, qty, px: +px.toPrecision(6), fee: +fee.toFixed(4), slippageBp: +(rnd() * 6).toFixed(1), realizedPnl: closing ? +(acc.deposit * 0.01 * gauss() * 2).toFixed(2) : 0, liquidation: closing && rnd() < 0.03, ts: now() - Math.floor(rnd() * span) })
  }
}
function genOrderHistory(acc) {
  for (const f of db.fills.filter(f => f.accountId === acc.id)) {
    db.orders.push({ id: f.orderId, accountId: acc.id, instanceId: f.instanceId, instId: f.instId, side: f.side, posSide: f.posSide, type: f.realizedPnl && rnd() < 0.4 ? pick(['stop', 'target']) : 'market', qty: f.qty, price: f.px * (f.side === 'buy' ? 1.001 : 0.999), reduceOnly: !!f.realizedPnl, leverage: 3, state: 'filled', filled: f.qty, avgPx: f.px, createdAt: f.ts - 800, updatedAt: f.ts })
  }
  db.orders.push({ id: ++db.ids.ord, accountId: acc.id, instanceId: 0, instId: 'DOGE-USDT-SWAP', side: 'buy', posSide: 'long', type: 'market', qty: 3, price: 0.2501, reduceOnly: false, leverage: 3, state: 'rejected', filled: 0, reason: '盘口在滑点容忍内的深度不足', createdAt: now() - 9 * HOUR, updatedAt: now() - 9 * HOUR })
}
function genLedger(acc) {
  for (const f of db.fills.filter(f => f.accountId === acc.id)) {
    db.ledger.push({ id: ++db.ids.led, accountId: acc.id, kind: 'fee', amount: -f.fee, instId: f.instId, refId: f.id, ts: f.ts })
    if (f.realizedPnl) db.ledger.push({ id: ++db.ids.led, accountId: acc.id, kind: f.liquidation ? 'liquidation' : 'pnl', amount: f.realizedPnl, instId: f.instId, refId: f.id, ts: f.ts })
  }
  for (let i = 0; i < 6; i++) db.ledger.push({ id: ++db.ids.led, accountId: acc.id, kind: 'funding', amount: +(gauss() * 0.8).toFixed(4), instId: pick(['BTC-USDT-SWAP', 'ETH-USDT-SWAP']), ts: now() - i * 8 * HOUR })
}
function refreshDerived(acc) {
  const pos = db.positions.filter(p => p.accountId === acc.id)
  const series = db.equity[acc.id]
  const upnl = pos.reduce((s, p) => s + p.unrealizedPnl, 0)
  if (series?.length) acc.equity = series[series.length - 1][1]
  acc.openCount = pos.length
  acc.cash = +(acc.equity - pos.reduce((s, p) => s + p.margin, 0) - upnl).toFixed(2)
  acc.instances = db.instances.filter(i => i.accountId === acc.id).length
}
function makeReport(acc) {
  // internal/trade Report: USDT amounts, ratios as fractions, nullable sharpe / winRate / profitFactor
  const series = db.equity[acc.id] || []
  let peak = -Infinity, mdd = 0
  for (const [, e] of series) { peak = Math.max(peak, e); mdd = Math.max(mdd, 1 - e / peak) }
  const fills = db.fills.filter(f => f.accountId === acc.id)
  const closes = fills.filter(f => f.realizedPnl)
  const wins = closes.filter(f => f.realizedPnl > 0)
  const fees = fills.reduce((s, f) => s + f.fee, 0)
  const funding = db.ledger.filter(l => l.accountId === acc.id && l.kind === 'funding').reduce((s, l) => s + l.amount, 0)
  const equity = series.at(-1)?.[1] ?? acc.deposit
  const net = equity - acc.deposit
  const unrealized = db.positions.filter(x => x.accountId === acc.id).reduce((s, x) => s + x.unrealizedPnl, 0)
  const byInst = {}
  for (const f of fills) {
    const b = (byInst[f.instId] ||= { instId: f.instId, trades: 0, wins: 0, pnl: 0, fees: 0, funding: 0, turnover: 0 })
    b.fees += f.fee; b.pnl -= f.fee; b.turnover += f.px * f.qty * CTVAL[f.instId]
    if (f.realizedPnl) { b.trades++; b.pnl += f.realizedPnl; if (f.realizedPnl > 0) b.wins++ }
  }
  const sigs = db.signals.filter(s => s.accountId === acc.id)
  const outcomes = {}
  for (const x of sigs) outcomes[x.outcome] = (outcomes[x.outcome] || 0) + 1
  const grossWin = wins.reduce((s, f) => s + f.realizedPnl, 0), grossLoss = -closes.filter(f => f.realizedPnl < 0).reduce((s, f) => s + f.realizedPnl, 0)
  const r2 = x => +x.toFixed(2)
  return {
    accountId: acc.id, deposit: acc.deposit, equity: r2(equity), gross: r2(net + fees - funding), net: r2(net), netPct: +(net / acc.deposit).toFixed(6),
    realized: r2(closes.reduce((s, f) => s + f.realizedPnl, 0)), unrealized: r2(unrealized), maxDrawdown: +mdd.toFixed(4),
    sharpe: series.length > 30 ? 1.42 : null, sharpeBasis: 'daily', winRate: closes.length ? wins.length / closes.length : null,
    profitFactor: grossLoss ? r2(grossWin / grossLoss) : null, turnover: r2(fills.reduce((s, f) => s + f.px * f.qty * CTVAL[f.instId], 0)),
    fees: r2(fees), funding: r2(funding), liquidations: fills.filter(f => f.liquidation).length, trades: closes.length, openTrades: db.positions.filter(x => x.accountId === acc.id).length,
    byInst: Object.values(byInst).map(b => ({ ...b, pnl: r2(b.pnl), fees: r2(b.fees), turnover: r2(b.turnover) })),
    signals: { ordered: outcomes.ordered || 0, rejected: outcomes.rejected || 0, skipped: (outcomes.skipped_ai || 0) + (outcomes.skipped_user || 0) + (outcomes.expired || 0), recorded: outcomes.recorded || 0, pending: outcomes.pending || 0 },
    outcomes, from: acc.from, to: acc.to,
  }
}

// ---- seed ----
{
  const a1 = addAccount({ id: 1, name: '主流趋势', mode: 'live', deposit: 10000, createdAt: T0 - 21 * DAY, limits: { confirm: 'open' } })
  const i1 = addInstance(1, '9f8e7d6c5b4a', { state: 'running', heartbeat: T0 - 40_000, configVersion: 2, params: { fast: 21, slow: 55, atr_mult: 2.2 }, createdAt: T0 - 21 * DAY })
  const i2 = addInstance(1, '7d8e9f0a1b2c', { state: 'paused', stateReason: '手动暂停', heartbeat: T0 - 3 * HOUR, exec: 'record', createdAt: T0 - 10 * DAY })
  db.equity[1] = equitySeries(10000, T0 - 21 * DAY, T0, 15 * MIN, 0.000022, 0.0016)
  addPosition(1, i1.id, 'BTC-USDT-SWAP', 'long', 8, 114950.2, 3, 111800, 125000)
  addPosition(1, i1.id, 'ETH-USDT-SWAP', 'long', 15, 4422.8, 3, 4250, 4800)
  addPosition(1, i2.id, 'SOL-USDT-SWAP', 'short', 12, 208.3, 2, 219.5, 190)
  genSignals(a1, [i1, i2], 34, 7 * DAY); genFills(a1, [i1, i2], 28, 14 * DAY); genOrderHistory(a1); genLedger(a1)
  a1.dayPnl = 86.42

  const a2 = addAccount({ id: 2, name: '山寨动量 · AI 审核', mode: 'live', deposit: 5000, createdAt: T0 - 9 * DAY, limits: { maxPositions: 8, riskPct: 0.008, confirm: 'all', confirmTimeoutS: 90 } })
  const i3 = addInstance(2, '3c4d5e6f7a8b', { state: 'running', exec: 'ai', heartbeat: T0 - 12_000, createdAt: T0 - 9 * DAY })
  const i4 = addInstance(2, '9f8e7d6c5b4a', { state: 'faulted', stateReason: '事件 5 秒未确认，已重启 3 次后停止（Python: ZeroDivisionError in on_bar）', heartbeat: T0 - 50 * MIN, universe: { static: ['SUI-USDT-SWAP', 'LINK-USDT-SWAP', 'AVAX-USDT-SWAP', 'TON-USDT-SWAP', 'PEPE-USDT-SWAP'] }, createdAt: T0 - 5 * DAY })
  db.equity[2] = equitySeries(5000, T0 - 9 * DAY, T0, 15 * MIN, -0.00001, 0.0024)
  addPosition(2, i3.id, 'DOGE-USDT-SWAP', 'long', 40, 0.25812, 4, 0.2433, 0.2896)
  genSignals(a2, [i3, i4], 26, 5 * DAY); genFills(a2, [i3], 14, 8 * DAY); genOrderHistory(a2); genLedger(a2)
  a2.dayPnl = -268.4; a2.halted = true

  const from = Date.UTC(2026, 6, 1) - 8 * HOUR, to = Date.UTC(2026, 9, 1) - 8 * HOUR
  const a3 = addAccount({ id: 3, name: '趋势 2026Q3 回测', mode: 'backtest', deposit: 10000, from, to, createdAt: T0 - 2 * DAY })
  const i5 = addInstance(3, '9f8e7d6c5b4a', { state: 'done', heartbeat: T0 - 2 * DAY, createdAt: T0 - 2 * DAY })
  db.equity[3] = equitySeries(10000, from, to, 4 * HOUR, 0.00045, 0.009)
  genSignals(a3, [i5], 40, 90 * DAY); genFills(a3, [i5], 60, 90 * DAY); for (const f of db.fills.filter(f => f.accountId === 3)) f.ts = from + Math.floor(rnd() * (to - from)); genLedger(a3)

  const a4 = addAccount({ id: 4, name: '旧测试账户', mode: 'live', deposit: 2000, archived: true, createdAt: T0 - 60 * DAY })
  db.equity[4] = equitySeries(2000, T0 - 60 * DAY, T0 - 30 * DAY, HOUR, -0.00002, 0.002)
  addInstance(4, 'a1b2c3d4e5f6', { state: 'stopped', createdAt: T0 - 60 * DAY })
  for (const a of db.accounts) refreshDerived(a)
  a3.backtest = { state: 'done', progress: 1, clock: to, startedAt: T0 - 2 * DAY, finishedAt: T0 - 2 * DAY + 40_000 }

  // AI decisions + proposals
  const rationale = [
    'DOGE 1H 放量突破 24 根高点，OI 4h +6%，资金费正常（0.01%/8h），BTC 同步走强；止损放在突破 K 线低点下方，盈亏比约 2.4。执行，仓位维持。',
    '信号对应的 4h 涨幅已达 17.8%，成交额骤增但 OI 下降，更像空头回补而非新多入场；近 3 次同类信号 2 次止损。跳过。',
    'PEPE 点差扩大到 9bp，深度不足；即使方向正确，滑点会吃掉大部分预期收益。跳过。',
    'SUI 趋势与 BTC 相关性低（β 0.4），走出独立行情；收紧止损到 1.6ATR 以控制回撤，仓位 ×0.7 执行。',
  ]
  for (let i = 0; i < 12; i++) {
    const exec = i % 3 !== 1
    const instId = pick(['DOGE-USDT-SWAP', 'SUI-USDT-SWAP', 'PEPE-USDT-SWAP', 'LINK-USDT-SWAP', 'AVAX-USDT-SWAP'])
    // internal/aigate DecisionRecord; the engine verdict lives on the signal (aiDecisionId)
    const id = ++db.ids.dec
    const decision = { action: exec ? 'execute' : 'skip', sizeScale: exec ? pick([1, 0.7, 0.5]) : 0, stopLoss: exec ? +(PX[instId] * 0.95).toPrecision(5) : 0, takeProfit: 0, confidence: +(0.55 + rnd() * 0.35).toFixed(2), rationale: rationale[i % rationale.length] }
    db.decisions.push({ id, ts: T0 - i * 3 * HOUR - 7 * MIN, kind: 'review', signalId: 9100 + i, accountId: 2, instanceId: i3.id, strategy: 'alt-momentum', instId, signalAction: 'open_long', requestSha256: 'ab12cd34ef56' + i, memoryVersion: 2, promptVersion: 'r3', model: 'claude-sonnet-4-5', action: decision.action, decision, costUsd: +(0.02 + rnd() * 0.05).toFixed(3), latencyMs: Math.round(4000 + rnd() * 9000),
      error: i === 9 ? 'deadline exceeded (TTL 120s)' : undefined, outcomes: exec && i % 3 === 0 ? [{ ts: T0 - i * 3 * HOUR + 5 * HOUR, result: pick(['止盈，实现 +42.10 USDT（未含手续费），持有 4.8 小时', '止损，实现 -18.75 USDT（未含手续费），持有 1.2 小时']) }] : undefined })
    db.signals.push({ id: 9100 + i, instanceId: i3.id, accountId: 2, instId, action: 'open_long', strength: 1, reason: '突破 24 根 K 线高点', ts: T0 - i * 3 * HOUR - 8 * MIN, recvTs: T0 - i * 3 * HOUR - 8 * MIN, price: PX[instId], configVersion: 1, aiDecisionId: id,
      outcome: !exec ? 'skipped_ai' : i % 5 === 3 ? 'rejected' : 'ordered', outcomeReason: !exec ? 'AI 跳过' : i % 5 === 3 ? '已达最大持仓数 8' : '96 张 · 4.00x · 保证金约 87.20 USDT' })
  }
  db.proposals.push( // internal/aigate Proposal
    { id: ++db.ids.prop, ts: T0 - 5 * HOUR, accountId: 2, instanceId: i3.id, strategy: 'alt-momentum', decisionId: 301, current: { lookback: 24, stop_atr: 2 }, params: { lookback: 36, stop_atr: 2.5 }, rationale: '过去 40 个信号中 62% 在入场后 3 根 K 线内被 2ATR 止损扫出，随后又回到原方向；当前山寨波动率比 30 天均值高 35%。建议放宽止损到 2.5ATR，并把突破回看延长到 36 根以过滤噪声突破。', state: 'pending' },
    { id: ++db.ids.prop, ts: T0 - 3 * DAY, accountId: 1, instanceId: i1.id, strategy: 'majors-trend', decisionId: 290, current: { fast: 20 }, params: { fast: 21 }, rationale: '小幅调整避免与常见 EMA20 同时触发。', state: 'approved', decidedAt: T0 - 3 * DAY + HOUR },
    { id: ++db.ids.prop, ts: T0 - 6 * DAY, accountId: 2, instanceId: i3.id, strategy: 'alt-momentum', decisionId: 280, current: { min_score: 70 }, params: { min_score: 55 }, rationale: '放宽入场分数以增加信号数量。', state: 'rejected', decidedAt: T0 - 6 * DAY + HOUR, note: '被新的建议取代' },
  )
}

// ---------- round trips + period candles (internal/trade trades.go) ----------
db.trades = {}
db.candles = {}
const MORE = { 'ARB-USDT-SWAP': 0.4921, 'WIF-USDT-SWAP': 0.8834, 'ORDI-USDT-SWAP': 9.214, 'TIA-USDT-SWAP': 1.9873, 'FIL-USDT-SWAP': 2.4512 }
Object.assign(PX, MORE)
Object.assign(CTVAL, { 'ARB-USDT-SWAP': 10, 'WIF-USDT-SWAP': 1, 'ORDI-USDT-SWAP': 0.1, 'TIA-USDT-SWAP': 1, 'FIL-USDT-SWAP': 0.1 })
/** 1H random-walk candles ending near today's price, [ts, o, h, l, c, vol, volQuote]. */
function hourly(instId, from, to, vol) {
  const n = Math.ceil((to - from) / HOUR)
  const closes = new Array(n)
  let c = PX[instId]
  for (let i = n - 1; i >= 0; i--) { closes[i] = c; c = c / (1 + vol * gauss() + 0.00002) }
  const rows = []
  let prev = closes[0] * (1 + vol * gauss() * 0.3)
  for (let i = 0; i < n; i++) {
    const o = prev, cl = closes[i], hi = Math.max(o, cl) * (1 + Math.abs(gauss()) * vol * 0.6), lo = Math.min(o, cl) * (1 - Math.abs(gauss()) * vol * 0.6)
    const v = 1000 + rnd() * 9000
    rows.push([Math.floor(from / HOUR) * HOUR + i * HOUR, o, hi, lo, cl, v, v * cl * CTVAL[instId]])
    prev = cl
  }
  return rows
}
function aggregate(rows, ms) {
  const out = []
  for (const r of rows) {
    const t = Math.floor(r[0] / ms) * ms, last = out[out.length - 1]
    if (!last || last[0] !== t) out.push([t, r[1], r[2], r[3], r[4], r[5], r[6]])
    else { last[2] = Math.max(last[2], r[2]); last[3] = Math.min(last[3], r[3]); last[4] = r[4]; last[5] += r[5]; last[6] += r[6] }
  }
  return out
}
/**
 * Generate n round trips over [from, to] on the given instruments with prices taken from their
 * candles, and an hourly equity curve consistent with them (deposit + realized nets + open PnL noise).
 */
function genTrades(acc, insts, n, from, to, opts = {}) {
  const lead = Math.min((to - from) / 6, 60 * HOUR)
  const bars = {}
  for (const id of insts) bars[id] = hourly(id, from - lead, to, id.startsWith('BTC') ? 0.004 : id.startsWith('ETH') ? 0.005 : 0.009)
  db.candles[acc.id] = bars
  const trades = []
  let fid = 1, nextId = 1
  const span = to - from
  // walk each instrument forward in time: flat → trade → flat, about n / insts.length trades each
  const cursor = {} // instId → earliest next open
  const per = n / insts.length
  for (let k = 0; k < n * 40 && trades.length < n; k++) {
    const instId = insts[k % insts.length], b = bars[instId]
    const gap = (span / per) * (0.15 + rnd() * 1.1)
    const openTs = Math.floor(((cursor[instId] ?? from) + gap * (cursor[instId] ? 1.25 : rnd() * 0.5)) / HOUR) * HOUR
    if (openTs >= to - HOUR) continue
    const hold = Math.max(1, Math.round((opts.hold || 18) * (0.2 + rnd() * 1.8)))
    const openIdx = Math.floor((openTs - b[0][0]) / HOUR)
    const isOpen = opts.openLast && openTs > to - 2 * DAY && rnd() < 0.5
    const closeIdx = Math.min(b.length - 1, openIdx + hold)
    const closeTs = isOpen ? 0 : b[closeIdx][0]
    if (!isOpen && closeTs >= to) continue
    cursor[instId] = isOpen ? Infinity : closeTs
    const long = rnd() < (opts.longBias ?? 0.62)
    const entryPx = b[openIdx][4], exitPx = isOpen ? undefined : b[closeIdx][4]
    const ctVal = CTVAL[instId], lev = pick([3, 3, 5, 5, 10])
    const notional = (opts.size || 1500) * (0.25 + rnd() * 1.6)
    const qty = +(notional / (entryPx * ctVal)).toFixed(ctVal >= 1 ? 0 : 2) || 1
    const real = qty * ctVal * entryPx
    const dir = long ? 1 : -1
    const pnl = isOpen ? 0 : dir * ((exitPx ?? entryPx) - entryPx) * qty * ctVal
    const fees = real * 0.0005 * (isOpen ? 1 : 2)
    const funding = isOpen ? 0 : +(gauss() * real * 0.0002).toFixed(4)
    const net = pnl - fees + funding
    const stopLoss = +(entryPx * (1 - dir * 0.03)).toPrecision(6), takeProfit = +(entryPx * (1 + dir * 0.06)).toPrecision(6)
    let exitKind = isOpen ? 'open' : net > real * 0.02 && rnd() < 0.6 ? 'target' : net < -real * 0.01 && rnd() < 0.55 ? 'stop' : 'signal'
    if (!isOpen && rnd() < 0.012) exitKind = 'liquidation'
    if (!isOpen && exitKind === 'signal' && rnd() < 0.05) exitKind = 'manual'
    const side = long ? 'buy' : 'sell', exitSide = long ? 'sell' : 'buy'
    const fills = [{ id: fid++, ts: openTs, side, qty, px: entryPx, fee: real * 0.0005, pnl: 0, kind: 'open', orderType: 'market' }]
    if (!isOpen && hold > 6 && rnd() < 0.25) { // a partial take-profit on the way
      const mid = b[openIdx + Math.floor(hold / 2)], rq = +(qty / 2).toFixed(ctVal >= 1 ? 0 : 2) || 1
      fills.push({ id: fid++, ts: mid[0], side: exitSide, qty: rq, px: mid[4], fee: rq * ctVal * mid[4] * 0.0005, pnl: dir * (mid[4] - entryPx) * rq * ctVal, kind: 'reduce', orderType: 'limit' })
    }
    if (!isOpen) fills.push({ id: fid++, ts: closeTs, side: exitSide, qty: fills.length > 1 ? +(qty - fills[1].qty).toFixed(2) : qty, px: exitPx, fee: (exitPx ?? 0) * qty * ctVal * 0.0005, pnl: pnl - (fills[1]?.pnl ?? 0), kind: 'close', orderType: exitKind === 'stop' ? 'stop' : exitKind === 'target' ? 'target' : exitKind === 'liquidation' ? 'liquidation' : 'market' })
    trades.push({
      id: nextId++, instanceId: opts.instanceId || 0, instId, posSide: long ? 'long' : 'short', openTs, closeTs: closeTs || undefined, entryPx, exitPx, qty, ctVal,
      notional: +real.toFixed(2), leverage: lev, margin: +(real / lev).toFixed(2), pnl: +pnl.toFixed(4), fees: +fees.toFixed(4), funding, net: +net.toFixed(4),
      returnPct: +(net / (real / lev)).toFixed(4), exitKind, openReason: pick(opts.reasons || ['EMA20 上穿 EMA60，4H 收盘确认', '突破 24 根 K 线高点，放量 2.3 倍', '残差反转：1H 超跌 2.1σ']),
      closeReason: isOpen ? undefined : exitKind === 'stop' ? '触发止损' : exitKind === 'target' ? '触发止盈' : exitKind === 'liquidation' ? '标记价穿过强平价' : exitKind === 'manual' ? '手动平仓' : pick(['趋势反转：EMA20 下穿 EMA60', '持有到期', '分数跌出前 10']),
      stopLoss, takeProfit, aiDecisionId: opts.ai && rnd() < 0.7 ? 301 + Math.floor(rnd() * 12) : undefined, fills,
    })
  }
  trades.sort((a, b2) => a.openTs - b2.openTs)
  trades.forEach((t, i) => (t.id = 100 * acc.id + i + 1))
  db.trades[acc.id] = trades
  if (opts.complex) makeComplex(trades, bars)
  // hourly equity consistent with the trades
  const eq = []
  const closes = trades.filter(t => t.closeTs).sort((a, b2) => a.closeTs - b2.closeTs)
  let ci = 0, realized = 0, noise = 0
  for (let t = from; t <= to; t += HOUR) {
    while (ci < closes.length && closes[ci].closeTs <= t) realized += closes[ci++].net
    const open = trades.filter(x => x.openTs <= t && (!x.closeTs || x.closeTs > t))
    let upnl = 0
    for (const x of open) {
      const b = bars[x.instId], i = Math.min(b.length - 1, Math.floor((t - b[0][0]) / HOUR))
      upnl += (x.posSide === 'long' ? 1 : -1) * (b[i][4] - x.entryPx) * x.qty * x.ctVal
    }
    noise = noise * 0.85 + gauss() * acc.deposit * 0.00012
    eq.push([t, +(acc.deposit + realized + upnl + noise).toFixed(2)])
  }
  db.equity[acc.id] = eq
}
/**
 * Turn the longest closed trade into a scaled one: open, 2 adds, 2 partial reduces, final close —
 * the round trip stays one trade until the position is flat (internal/trade trades.go).
 */
function makeComplex(trades, bars) {
  const t = trades.filter(x => x.closeTs && x.exitKind !== 'liquidation').sort((a, b) => (b.closeTs - b.openTs) - (a.closeTs - a.openTs))[0]
  if (!t) return
  const b = bars[t.instId], at = ts => Math.max(0, Math.min(b.length - 1, Math.floor((ts - b[0][0]) / HOUR)))
  const i0 = at(t.openTs), i1 = Math.min(b.length - 1, Math.max(at(t.closeTs), i0 + 10))
  const idx = [i0, i0 + 1, i0 + 2, Math.round(i0 + (i1 - i0) * 0.55), Math.round(i0 + (i1 - i0) * 0.75), i1]
  const q = t.qty, ct = t.ctVal, dir = t.posSide === 'long' ? 1 : -1
  const buy = t.posSide === 'long' ? 'buy' : 'sell', sell = t.posSide === 'long' ? 'sell' : 'buy'
  const plan = [['open', buy, q], ['add', buy, q * 0.5], ['add', buy, q * 0.5], ['reduce', sell, q * 0.5], ['reduce', sell, q * 0.5], ['close', sell, q]]
  let pos = 0, cost = 0, pnl = 0, fees = 0, fid = t.fills[0].id
  t.fills = plan.map(([kind, side, qq], k) => {
    const px = b[idx[k]][4], fee = qq * ct * px * 0.0005
    fees += fee
    let fp = 0
    if (kind === 'open' || kind === 'add') { cost += qq * px; pos += qq } else { const avg = cost / pos; fp = dir * (px - avg) * qq * ct; pnl += fp; cost -= avg * qq; pos -= qq }
    return { id: fid + k, ts: b[idx[k]][0], side, qty: +qq.toFixed(4), px, fee: +fee.toFixed(4), pnl: +fp.toFixed(4), kind, orderType: kind === 'reduce' ? 'limit' : 'market' }
  })
  const opens = t.fills.filter(f => f.kind === 'open' || f.kind === 'add'), exits = t.fills.filter(f => f.kind === 'reduce' || f.kind === 'close')
  const vw = xs => xs.reduce((s2, f) => s2 + f.px * f.qty, 0) / xs.reduce((s2, f) => s2 + f.qty, 0)
  t.closeTs = b[i1][0]
  t.qty = +(q * 2).toFixed(4)
  t.entryPx = vw(opens); t.exitPx = vw(exits)
  t.notional = +(t.qty * ct * t.entryPx).toFixed(2); t.margin = +(t.notional / t.leverage).toFixed(2)
  t.pnl = +pnl.toFixed(4); t.fees = +fees.toFixed(4); t.net = +(pnl - fees + t.funding).toFixed(4); t.returnPct = +(t.net / t.margin).toFixed(4)
  t.exitKind = 'signal'; t.openReason = '分批建仓：开仓后两次加仓'; t.closeReason = '两次减仓锁定利润后信号平仓'
}
{
  const byAcc = id => db.accounts.find(a => a.id === id)
  const a1 = byAcc(1), a2 = byAcc(2), a3 = byAcc(3)
  genTrades(a1, ['BTC-USDT-SWAP', 'ETH-USDT-SWAP', 'SOL-USDT-SWAP'], 24, T0 - 21 * DAY, T0, { size: 2500, openLast: true, instanceId: 21 })
  genTrades(a2, ['DOGE-USDT-SWAP', 'SUI-USDT-SWAP', 'PEPE-USDT-SWAP', 'LINK-USDT-SWAP', 'AVAX-USDT-SWAP'], 18, T0 - 9 * DAY, T0, { size: 600, ai: true, openLast: true, instanceId: 23, hold: 8 })
  genTrades(a3, ['BTC-USDT-SWAP', 'ETH-USDT-SWAP'], 32, a3.from, a3.to, { size: 3000, hold: 40, instanceId: 24, complex: true })
  // dense fixture: a 1H pool strategy over 90 days, 253 round trips on 8 instruments
  const from = T0 - 92 * DAY, to = T0 - 2 * DAY
  const a5 = addAccount({ id: 5, name: '池子反转 · 回测（253 笔）', mode: 'backtest', deposit: 20000, from, to, createdAt: T0 - DAY })
  addInstance(5, '3c4d5e6f7a8b', { state: 'done', heartbeat: T0 - DAY, name: 'pool-reversal', universe: { screener: { rule: 'trend-oi-confirm', side: 'long', top: 10, refresh: '1H' } } })
  a5.backtest = { state: 'done', progress: 1, clock: to, startedAt: T0 - DAY, finishedAt: T0 - DAY + 95_000 }
  genTrades(a5, ['SOL-USDT-SWAP', 'DOGE-USDT-SWAP', 'SUI-USDT-SWAP', 'ARB-USDT-SWAP', 'WIF-USDT-SWAP', 'ORDI-USDT-SWAP', 'TIA-USDT-SWAP', 'FIL-USDT-SWAP'], 253, from, to,
    { size: 1800, hold: 6, longBias: 0.75, instanceId: 25, complex: true, reasons: ['残差反转：1H 超跌 2.1σ，多头分 82', '残差反转：1H 超跌 1.8σ，多头分 77', '残差反转：1H 超跌 2.6σ，多头分 91'] })
  for (const a of db.accounts) refreshDerived(a)
}

function openPrompts() {
  // two always-open prompts whose deadlines roll forward (stable for screenshots)
  const t = now()
  const base = [
    { id: 'p-ix1', event: { kind: 'signal', accountId: 2, account: '山寨动量 · AI 审核', mode: 'live', title: '开多 SUI-USDT-SWAP · 待确认', lines: ['策略 alt-momentum v1：突破 24 根 K 线高点，放量 2.1 倍', 'AI：执行，仓位 ×0.7（置信 71%）— 与 BTC 相关性低，独立趋势', '参考价 3.6125 · 止损 3.4890（-3.4%）· 止盈 3.9420（+9.1%）', '预计仓位 96 张 ≈ 347 USDT 名义，风险 40 USDT（0.8%）'], ts: t - 30_000 }, options: [{ key: 'execute', label: '执行' }, { key: 'half', label: '半仓' }, { key: 'skip', label: '跳过' }], deadline: t + 61_000, default: 'skip', state: 'open', createdAt: t - 30_000 },
    { id: 'p-ix2', event: { kind: 'signal', accountId: 1, account: '主流趋势', mode: 'live', title: '开多 ETH-USDT-SWAP · 待确认', lines: ['策略 majors-trend v2：EMA21 上穿 EMA55，4H 收盘确认', '参考价 4386.21 · 止损 4231.40（-3.5%）', '已有 ETH 多仓 15 张，本次为加仓'], ts: t - 75_000 }, options: [{ key: 'execute', label: '执行' }, { key: 'half', label: '半仓' }, { key: 'skip', label: '跳过' }], deadline: t + 14_000, default: 'skip', state: 'open', createdAt: t - 75_000 },
  ]
  return base.filter(p => !db.prompts.includes(p.id))
}

// ---------- HTTP helpers ----------
const send = (res, code, body) => { res.writeHead(code, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' }); res.end(JSON.stringify(body)) }
const ok = (res, body = { ok: true }) => send(res, 200, body)
const notFound = res => send(res, 404, { error: 'not found' })
const readBody = req => new Promise(r => { let b = ''; req.on('data', c => (b += c)); req.on('end', () => { try { r(b ? JSON.parse(b) : {}) } catch { r({}) } }) })
const acct = id => db.accounts.find(a => a.id === id)

// ---------- mock routes ----------
async function mockApi(req, res, url) {
  const p = url.pathname.replace(/^\/api\/v1\//, '')
  const m = req.method
  if (m !== 'GET' && !req.headers['x-ctg-admin']) return send(res, 401, { error: 'owner key required', code: 'admin_required' })
  const body = m === 'GET' ? {} : await readBody(req)
  const q = url.searchParams
  const limit = Number(q.get('limit') || 100)
  let r
  // ---- trade ----
  if (p === 'trade/accounts' && m === 'GET') return ok(res, db.accounts)
  if (p === 'trade/accounts' && m === 'POST') {
    if (body.mode === 'real') return send(res, 400, { error: '实盘尚未开放' })
    const a = addAccount({ name: body.name, mode: body.mode, deposit: body.deposit, limits: body.limits, from: body.from, to: body.to })
    db.equity[a.id] = body.mode === 'backtest' ? [] : [[now(), a.deposit]]
    if (body.mode === 'backtest') a.backtest = { state: 'idle', progress: 0 }
    refreshDerived(a)
    return ok(res, a)
  }
  if ((r = p.match(/^trade\/accounts\/(\d+)$/))) {
    const a = acct(+r[1]); if (!a) return notFound(res)
    if (m === 'PATCH') { if (body.name) a.name = body.name; if (body.limits) a.limits = body.limits; if (body.archived != null) a.archived = body.archived; return ok(res, a) }
    refreshDerived(a)
    return ok(res, { account: a, positions: db.positions.filter(x => x.accountId === a.id), orders: db.orders.filter(o => o.accountId === a.id && ['waiting_trigger', 'live', 'partially_filled'].includes(o.state)) })
  }
  if ((r = p.match(/^trade\/accounts\/(\d+)\/deposit$/))) {
    const a = acct(+r[1]); if (!a) return notFound(res)
    const amt = Number(body.amount) || 0
    a.deposit += amt
    const s = db.equity[a.id]; if (s?.length) s.push([now(), +(s.at(-1)[1] + amt).toFixed(2)])
    db.ledger.push({ id: ++db.ids.led, accountId: a.id, kind: 'deposit', amount: amt, ts: now() })
    refreshDerived(a)
    return ok(res, a)
  }
  if ((r = p.match(/^trade\/accounts\/(\d+)\/equity$/))) {
    const from = Number(q.get('from') || 0), to = Number(q.get('to') || Infinity)
    return ok(res, (db.equity[+r[1]] || []).filter(([t]) => t >= from && t < to))
  }
  if ((r = p.match(/^trade\/accounts\/(\d+)\/(fills|orders|signals|ledger)$/))) {
    const before = Number(q.get('before') || Infinity)
    const ts = x => x.ts ?? x.createdAt
    return ok(res, db[r[2]].filter(x => x.accountId === +r[1] && ts(x) < before).sort((a, b) => ts(b) - ts(a)).slice(0, limit))
  }
  if ((r = p.match(/^trade\/accounts\/(\d+)\/close$/))) {
    const id = +r[1]
    const gone = db.positions.filter(x => x.accountId === id && (!body.instId || x.instId === body.instId))
    for (const x of gone) db.fills.push({ id: ++db.ids.fill, orderId: ++db.ids.ord, accountId: id, instanceId: 0, instId: x.instId, side: x.posSide === 'long' ? 'sell' : 'buy', posSide: x.posSide, qty: x.qty, px: x.markPx, fee: +(x.markPx * x.qty * x.ctVal * 0.0005).toFixed(4), slippageBp: 1.2, realizedPnl: x.unrealizedPnl, ts: now() })
    db.positions = db.positions.filter(x => !gone.includes(x))
    db.orders = db.orders.filter(o => !(o.accountId === id && o.state === 'waiting_trigger' && gone.some(g => g.instId === o.instId)))
    pushEvent('trade', { accountId: id, kind: 'fill' })
    return ok(res, { closed: gone.length })
  }
  if ((r = p.match(/^trade\/accounts\/(\d+)\/report$/))) {
    const a = acct(+r[1]); if (!a) return notFound(res)
    return ok(res, makeReport(a)) // partial while a backtest runs, like the server
  }
  if ((r = p.match(/^trade\/backtests\/(\d+)\/run$/))) {
    const a = acct(+r[1]); if (!a || a.mode !== 'backtest') return notFound(res)
    const insts = db.instances.filter(i => i.accountId === a.id)
    if (!insts.length) return send(res, 400, { error: '账户没有策略实例' })
    if (a.backtest?.state === 'running') return send(res, 409, { error: '回测正在运行' })
    insts.forEach(i => (i.state = 'running'))
    a.backtest = { state: 'running', progress: 0, clock: a.from, startedAt: now() }
    db.equity[a.id] = []
    const full = equitySeries(a.deposit, a.from, a.to, 4 * HOUR, 0.0003, 0.008)
    let k = 0
    const tick = setInterval(() => {
      k += Math.ceil(full.length / 8)
      if (a.backtest.state !== 'running') return clearInterval(tick) // cancelled
      db.equity[a.id] = full.slice(0, k)
      a.backtest.progress = Math.min(1, k / full.length)
      a.backtest.clock = full[Math.min(k, full.length) - 1][0]
      refreshDerived(a)
      pushEvent('trade', { accountId: a.id, kind: 'backtest' })
      if (k >= full.length) {
        clearInterval(tick)
        if (!db.fills.some(f => f.accountId === a.id)) { genSignals(a, insts, 30, a.to - a.from); genFills(a, insts, 40, a.to - a.from); genLedger(a) }
        insts.forEach(i => { i.state = 'done'; i.heartbeat = now() })
        Object.assign(a.backtest, { state: 'done', progress: 1, clock: a.to, finishedAt: now() })
        pushEvent('trade', { accountId: a.id, kind: 'instance' })
      }
    }, 600)
    return send(res, 202, a)
  }
  if ((r = p.match(/^trade\/backtests\/(\d+)\/cancel$/))) {
    const a = acct(+r[1]); if (!a) return notFound(res)
    if (a.backtest?.state !== 'running') return send(res, 409, { error: '回测未在运行' })
    Object.assign(a.backtest, { state: 'failed', error: '已取消', finishedAt: now() })
    db.instances.filter(i => i.accountId === a.id).forEach(i => (i.state = 'stopped'))
    return ok(res, a)
  }
  if ((r = p.match(/^trade\/accounts\/(\d+)\/trades$/))) return ok(res, db.trades[+r[1]] || [])
  if ((r = p.match(/^trade\/accounts\/(\d+)\/candles$/))) {
    const id = +r[1], inst = q.get('instId') || ''
    const rows = db.candles[id]?.[inst]
    if (!inst) return send(res, 400, { error: 'instId required' })
    if (!rows) return ok(res, { instId: inst, bar: '1H', from: 0, to: 0, rows: [] })
    const span = rows[rows.length - 1][0] - rows[0][0]
    let bar = q.get('bar') || (span / HOUR <= 1500 ? '1H' : '4H')
    if (['1m', '5m', '15m'].includes(bar)) bar = '1H' // the fixture has hourly data only (like the server's 1m-retention fallback)
    const out = bar === '4H' ? aggregate(rows, 4 * HOUR) : bar === '1D' ? aggregate(rows, DAY) : rows
    return ok(res, { instId: inst, bar, from: rows[0][0], to: rows[rows.length - 1][0] + HOUR, rows: out })
  }
  if (p === 'trade/strategies' && m === 'GET') {
    const names = [...new Set(VERSIONS.map(v => v.name))]
    return ok(res, names.map(n => { const vs = VERSIONS.filter(v => v.name === n).sort((a, b) => b.createdAt - a.createdAt); return { name: n, latest: vs[0], versions: vs } }))
  }
  if (p === 'trade/strategies/reload') return ok(res, { ok: true, versions: VERSIONS.length })
  if ((r = p.match(/^trade\/strategies\/([\w-]+)$/))) { const v = verById(r[1]); return v ? ok(res, { ...v, code: CODE(v.name) }) : notFound(res) }
  if ((r = p.match(/^trade\/accounts\/(\d+)\/instances$/))) {
    if (m === 'GET') return ok(res, db.instances.filter(i => i.accountId === +r[1]))
    const inst = addInstance(+r[1], body.versionId, { params: body.params, universe: body.universe, exec: body.exec })
    refreshDerived(acct(+r[1]))
    return ok(res, inst)
  }
  if ((r = p.match(/^trade\/instances\/(\d+)$/)) && m === 'PATCH') {
    const i = db.instances.find(x => x.id === +r[1]); if (!i) return notFound(res)
    Object.assign(i, Object.fromEntries(Object.entries(body).filter(([k]) => ['params', 'universe', 'exec'].includes(k))))
    i.configVersion++
    return ok(res, i)
  }
  if ((r = p.match(/^trade\/instances\/(\d+)\/(start|pause|resume|stop)$/))) {
    const i = db.instances.find(x => x.id === +r[1]); if (!i) return notFound(res)
    i.state = { start: 'running', resume: 'running', pause: 'paused', stop: 'stopped' }[r[2]]
    i.stateReason = r[2] === 'pause' ? '手动暂停' : undefined
    if (i.state === 'running') i.heartbeat = now()
    pushEvent('trade', { accountId: i.accountId, kind: 'instance' })
    return ok(res, i)
  }
  // ---- interact ----
  if (p === 'interact/prompts' && m === 'GET') {
    const open = openPrompts()
    if (q.get('state') !== 'all') return ok(res, { prompts: open, now: now() })
    const t = now(), opts = [{ key: 'execute', label: '执行' }, { key: 'half', label: '半仓' }, { key: 'skip', label: '跳过' }]
    const closed = [
      { id: 'sig-88', event: { kind: 'signal', accountId: 1, account: '主流趋势', mode: 'live', title: '待确认 · 开多 BTC', lines: ['BTC-USDT-SWAP 开多 · 强度 1.00'], ts: t - 3 * HOUR }, options: opts, deadline: t - 3 * HOUR + 120_000, default: 'skip', state: 'answered', choice: 'execute', by: 'telegram:123456789', answeredAt: t - 3 * HOUR + 30_000, createdAt: t - 3 * HOUR },
      { id: 'sig-80', event: { kind: 'signal', accountId: 2, account: '山寨动量 · AI 审核', mode: 'live', title: '待确认 · 开多 PEPE', lines: [], ts: t - 7 * HOUR }, options: opts, deadline: t - 7 * HOUR + 90_000, default: 'skip', state: 'expired', choice: 'skip', by: 'timeout', answeredAt: t - 7 * HOUR + 90_000, createdAt: t - 7 * HOUR },
    ]
    return ok(res, { prompts: [...open, ...closed], now: t })
  }
  if ((r = p.match(/^interact\/prompts\/([\w-]+)$/))) { db.prompts.push(r[1]); pushEvent('interact', {}); return ok(res, { ok: true, choice: body.choice }) }
  if (p === 'interact/config') {
    if (m === 'PUT') {
      const t = body.telegram || {}
      const tok = String(t.botToken || '').trim()
      if (tok && !tok.startsWith('••••')) {
        if (!/^[0-9]{5,}:[A-Za-z0-9_-]{20,}$/.test(tok)) return send(res, 400, { error: 'Telegram Bot Token 格式不对（应形如 123456:ABC…，从 @BotFather 获取）' })
        ix.telegram.botToken = '••••' + tok.slice(-4); ix.telegram.configured = true
      }
      if (t.enabled && !ix.telegram.configured) return send(res, 400, { error: '启用 Telegram 前需填写 Bot Token' })
      ix.telegram.enabled = !!t.enabled
      ix.telegram.allowUserIds = t.allowUserIds || []
      ix.kinds = (body.kinds || []).slice().sort(); ix.quiet = !!body.quiet
      ix.status.state = ix.telegram.enabled ? 'ok' : 'disabled'
      ix.status.strangers = ix.status.strangers.filter(x => !ix.telegram.allowUserIds.includes(x.id))
    }
    return ok(res, ix)
  }
  if (p === 'interact/test') {
    if (!ix.telegram.enabled) return ok(res, { ok: false, channels: { telegram: '未启用' }, promptId: '' })
    return ok(res, { ok: true, channels: { telegram: 'ok' }, promptId: 'p-test' })
  }
  // ---- ai ----
  if (p === 'ai/status') return ok(res, ai)
  if (p === 'ai/credential') {
    if (m === 'DELETE') { Object.assign(ai, { configured: false, kind: undefined, masked: '', lastError: '' }); return ok(res, ai) }
    Object.assign(ai, { configured: true, kind: body.kind, masked: '••••' + String(body.value || '').slice(-4), lastError: '' })
    return ok(res, ai)
  }
  if (p === 'ai/model') { ai.model = String(body.model || '').trim() || 'claude-sonnet-4-5'; return ok(res, ai) }
  if (p === 'ai/test') {
    if (!ai.configured) return ok(res, { ok: false, costUsd: 0, latencyMs: 0, error: 'no credential configured' })
    if (!ai.proxyOk) return ok(res, { ok: false, costUsd: 0, latencyMs: 0, error: 'AI worker is not ready: ' + ai.proxyError })
    ai.lastCallAt = now(); ai.callsToday++
    return ok(res, { ok: true, model: ai.model, reply: 'ok', costUsd: 0.0004, latencyMs: 2140 })
  }
  if (p === 'ai/decisions') {
    const id = Number(q.get('accountId') || 0), kind = q.get('kind') || ''
    return ok(res, db.decisions.filter(d => (!id || d.accountId === id) && (!kind || d.kind === kind)).slice(0, Number(q.get('limit') || 50)))
  }
  if ((r = p.match(/^ai\/decisions\/(\d+)$/))) {
    const d = db.decisions.find(x => x.id === +r[1]); if (!d) return notFound(res)
    return ok(res, { ...d, request: { signal: db.signals.find(x => x.id === d.signalId), account: { id: d.accountId }, market: { ret_4h: 0.031, spread_bps: 4.2, oi_chg_4h: 0.06 }, memory: memory['alt-momentum'][0].doc } })
  }
  if ((r = p.match(/^ai\/memory\/([\w-]+)$/))) { const v = memory[r[1]] || []; return ok(res, { strategy: r[1], current: v[0] || null, versions: v }) }
  if ((r = p.match(/^ai\/memory\/([\w-]+)\/reflect$/))) {
    if (!ai.available) return send(res, 503, { error: 'AI unavailable' })
    const v = (memory[r[1]] ||= [])
    const doc = { strategy: r[1], version: (v[0]?.version || 0) + 1, ts: now(), doc: '（模拟）暂无足够的已平仓决策，先积累更多审核记录。', source: 'reflect', throughDecision: db.ids.dec, decisions: 0, model: ai.model, costUsd: 0.02 }
    v.unshift(doc)
    return ok(res, doc)
  }
  if (p === 'ai/proposals' && m === 'GET') { const st = q.get('state'); return ok(res, db.proposals.filter(x => !st || x.state === st)) }
  if ((r = p.match(/^ai\/proposals\/(\d+)$/))) {
    const x = db.proposals.find(y => y.id === +r[1]); if (!x) return notFound(res)
    if (x.state !== 'pending') return send(res, 409, { error: 'proposal is not pending' })
    x.state = body.approve ? 'approved' : 'rejected'
    x.decidedAt = now()
    ai.pendingProposals = db.proposals.filter(y => y.state === 'pending').length
    if (body.approve) { const i = db.instances.find(y => y.id === x.instanceId); if (i) { Object.assign(i.params, x.params); i.configVersion++ } }
    return ok(res, x)
  }
  return notFound(res)
}

// ---------- fallbacks for core endpoints when the backend is down ----------
const rows = Object.keys(PX).map((id, i) => ({ instId: id, category: '1', status: 'live', active: true, rank: i + 1, turnover12h: 9e8 / (i + 1), have: 720, last: PX[id], values: { ret_24h: 0.02 * gauss(), ret_4h: 0.01 * gauss() }, pct: {}, hasSpot: true, listTime: 0, freshnessMs: 900, scores: {} }))
function fallback(p) {
  const t = Math.floor(now() / MIN) * MIN
  switch (p) {
    case 'meta': return { fields: [], rules: RULES, settings: { threshold: 5e6, topN: 100, categories: ['1'], defaultRule: 'trend-oi-confirm', storageCapMB: 2048 }, categories: { 1: '加密', 3: '股票', 4: '商品' } }
    case 'universe': return { seq: 1, asOfMinute: t - MIN, decisionTs: t, computedAt: t, provisional: false, phase: 'live', threshold: 5e6, topN: 100, coverage: { eligible: 120, ready: 100, warming: 0, stale: 0, new: 0, active: 100 }, rows, removed: [] }
    case 'catalog': return { items: Object.keys(PX).map((id, i) => ({ instId: id, category: '1', listTime: 0, recorded: true, active: true, rank: i + 1, last: PX[id], open24h: PX[id] * 0.98 })) }
    case 'settings': return fallback('meta').settings
    case 'turnovers': return { asOfMinute: t, rows: [] }
    case 'rules': return RULES
    case 'storage': return null
    case 'dev/config': return { enabled: false, globalPerMin: 600 }
    case 'positions': return []
    default: return undefined
  }
}

// ---------- SSE: proxied stream + injected trade events, or a mock stream ----------
const clients = new Set()
function pushEvent(type, data) { for (const c of clients) c.write(`event: ${type}\ndata: ${JSON.stringify(data)}\n\n`) }
if (!PASSTHROUGH) setInterval(() => pushEvent('trade', { accountId: 1, kind: 'fill' }), 20_000)
function mockStream(res) {
  res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', 'X-Accel-Buffering': 'no' })
  res.write('event: hello\ndata: {"seq":1}\n\n')
  const prices = setInterval(() => res.write(`event: prices\ndata: ${JSON.stringify(Object.fromEntries(Object.entries(PX).map(([k, v]) => [k, [v * (1 + 0.0005 * gauss()), now()]])))}\n\n`), 2000)
  clients.add(res)
  res.on('close', () => { clearInterval(prices); clients.delete(res) })
}

// ---------- proxy ----------
function proxy(req, res, url, onFail) {
  const mod = BACKEND.protocol === 'https:' ? https : http
  const up = mod.request({ protocol: BACKEND.protocol, hostname: BACKEND.hostname, port: BACKEND.port, method: req.method, path: url.pathname + url.search, headers: { ...req.headers, host: BACKEND.host } }, r => {
    res.writeHead(r.statusCode || 502, r.headers)
    if (url.pathname === '/api/v1/stream') { clients.add(res); res.on('close', () => clients.delete(res)) }
    r.pipe(res)
  })
  up.on('error', () => (res.headersSent ? res.end() : onFail()))
  req.pipe(up)
}

// ---------- static web app ----------
const TYPES = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.ico': 'image/x-icon', '.json': 'application/json', '.webmanifest': 'application/manifest+json', '.woff2': 'font/woff2' }
async function serveStatic(res, pathname) {
  let file = normalize(join(DIST, decodeURIComponent(pathname)))
  if (!file.startsWith(normalize(DIST))) return notFound(res)
  try { if (!(await stat(file)).isFile()) throw 0 } catch { file = join(DIST, 'index.html') }
  try {
    const buf = await readFile(file)
    res.writeHead(200, { 'Content-Type': TYPES[extname(file)] || 'application/octet-stream', 'Cache-Control': file.endsWith('index.html') ? 'no-cache' : 'public, max-age=3600' })
    res.end(buf)
  } catch {
    send(res, 500, { error: `web app not built: run npm run build (looked in ${DIST})` })
  }
}

http.createServer((req, res) => {
  const url = new URL(req.url || '/', 'http://x')
  const p = url.pathname
  if (!PASSTHROUGH && /^\/api\/v1\/(trade|interact|ai)\//.test(p)) return mockApi(req, res, url).catch(e => send(res, 500, { error: String(e) }))
  if (p === '/api/v1/stream') return proxy(req, res, url, () => mockStream(res))
  if (p.startsWith('/api/')) {
    return proxy(req, res, url, () => {
      const fb = fallback(p.replace(/^\/api\/v1\//, ''))
      fb === undefined ? send(res, 502, { error: 'backend unreachable (mock)' }) : ok(res, fb)
    })
  }
  serveStatic(res, p)
}).listen(PORT, '127.0.0.1', () => console.log(`mock-trade on http://127.0.0.1:${PORT}  (${PASSTHROUGH ? 'passthrough to' : 'backend'} ${BACKEND.href}, web ${DIST})`))

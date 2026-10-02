// Mock server for the trading UI (no dependencies). Serves realistic fake JSON for every endpoint
// of docs/trading-design.md §4 (trade / interact / ai), proxies everything else to a real backend,
// and serves the built web app (web/dist) with an SPA fallback — so the trading pages can be used
// and screenshotted before the backend exists.
//
//   cd web && npm run build && node scripts/mock-trade.mjs
//   PORT=5180 BACKEND=http://127.0.0.1:8686 DIST=dist node scripts/mock-trade.mjs
//
// When the backend is unreachable, a few core endpoints (meta, universe, catalog, settings, stream…)
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
const db = { accounts: [], instances: [], positions: [], orders: [], fills: [], signals: [], ledger: [], equity: {}, reports: {}, prompts: [], decisions: [], proposals: [], ids: { acc: 10, inst: 20, ord: 1000, fill: 5000, sig: 9000, led: 20000, dec: 300, prop: 40 } }
const ai = { configured: true, kind: 'oauth_token', masked: '••••x7Qa', workerOnline: true, lastCallAt: T0 - 4 * MIN, lastError: '', costToday: 0.84 }
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
    db.signals.push({ id: ++db.ids.sig, instanceId: inst.id, accountId: acc.id, instId, action, strength: opening ? +(0.5 + rnd() * 0.5).toFixed(2) : 1, stopLoss: opening ? +(px * (action === 'open_long' ? 0.96 : 1.04)).toPrecision(6) : undefined, takeProfit: opening ? +(px * (action === 'open_long' ? 1.08 : 0.92)).toPrecision(6) : undefined, reason: pick(REASONS[action]), ts, recvTs: ts + 1200, price: +px.toPrecision(6), outcome, outcomeReason: why || undefined, configVersion: inst.configVersion, aiDecisionId: inst.exec === 'ai' ? db.ids.dec + 1 + (i % 8) : undefined })
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
  const series = db.equity[acc.id] || []
  let peak = -Infinity, mdd = 0
  for (const [, e] of series) { peak = Math.max(peak, e); mdd = Math.max(mdd, 1 - e / peak) }
  const fills = db.fills.filter(f => f.accountId === acc.id)
  const closes = fills.filter(f => f.realizedPnl)
  const wins = closes.filter(f => f.realizedPnl > 0)
  const fees = fills.reduce((s, f) => s + f.fee, 0)
  const funding = db.ledger.filter(l => l.accountId === acc.id && l.kind === 'funding').reduce((s, l) => s + l.amount, 0)
  const net = (series.at(-1)?.[1] ?? acc.deposit) - acc.deposit
  const byInst = {}
  for (const f of fills) {
    const b = (byInst[f.instId] ||= { instId: f.instId, trades: 0, net: 0, gross: 0, fees: 0, funding: 0, wins: 0 })
    b.fees += f.fee
    if (f.realizedPnl) { b.trades++; b.gross += f.realizedPnl; b.net += f.realizedPnl - f.fee; if (f.realizedPnl > 0) b.wins++ }
  }
  const sigs = db.signals.filter(s => s.accountId === acc.id)
  const grossWin = wins.reduce((s, f) => s + f.realizedPnl, 0), grossLoss = -closes.filter(f => f.realizedPnl < 0).reduce((s, f) => s + f.realizedPnl, 0)
  return {
    gross: +(net + fees - funding).toFixed(2), net: +net.toFixed(2), maxDrawdown: +mdd.toFixed(4), sharpe: 1.42, winRate: closes.length ? wins.length / closes.length : 0,
    profitFactor: grossLoss ? +(grossWin / grossLoss).toFixed(2) : 0, turnover: +fills.reduce((s, f) => s + f.px * f.qty * CTVAL[f.instId], 0).toFixed(0), fees: +fees.toFixed(2), funding: +funding.toFixed(2), trades: closes.length,
    byInst: Object.values(byInst).map(b => ({ instId: b.instId, trades: b.trades, net: +b.net.toFixed(2), gross: +b.gross.toFixed(2), winRate: b.trades ? b.wins / b.trades : 0, fees: +b.fees.toFixed(2), funding: +(gauss() * 3).toFixed(2) })),
    signals: { ordered: sigs.filter(s => s.outcome === 'ordered').length, rejected: sigs.filter(s => s.outcome === 'rejected').length, skipped: sigs.filter(s => s.outcome.startsWith('skipped') || s.outcome === 'expired').length },
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
  db.reports[3] = makeReport(a3)

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
    db.decisions.push({ id: ++db.ids.dec, ts: T0 - i * 3 * HOUR - 7 * MIN, signalId: 9000 + i, accountId: 2, instanceId: i3.id, instId, signalAction: 'open_long', action: exec ? 'execute' : 'skip', sizeScale: exec ? pick([1, 0.7, 0.5]) : 0, stopLoss: exec ? +(PX[instId] * 0.95).toPrecision(5) : undefined, confidence: +(0.55 + rnd() * 0.35).toFixed(2), rationale: rationale[i % rationale.length], model: 'claude-sonnet-4-5', costUsd: +(0.02 + rnd() * 0.05).toFixed(3), latencyMs: Math.round(4000 + rnd() * 9000), outcome: exec ? (i % 5 === 3 ? 'rejected' : 'ordered') : 'skipped_ai', outcomeReason: exec && i % 5 === 3 ? '已达最大持仓数 8' : undefined })
  }
  db.proposals.push(
    { id: ++db.ids.prop, ts: T0 - 5 * HOUR, accountId: 2, instanceId: i3.id, instanceName: 'alt-momentum', decisionId: 301, current: { lookback: 24, stop_atr: 2 }, proposed: { lookback: 36, stop_atr: 2.5 }, rationale: '过去 40 个信号中 62% 在入场后 3 根 K 线内被 2ATR 止损扫出，随后又回到原方向；当前山寨波动率比 30 天均值高 35%。建议放宽止损到 2.5ATR，并把突破回看延长到 36 根以过滤噪声突破。', state: 'open' },
    { id: ++db.ids.prop, ts: T0 - 3 * DAY, accountId: 1, instanceId: i1.id, instanceName: 'majors-trend', current: { fast: 20 }, proposed: { fast: 21 }, rationale: '小幅调整避免与常见 EMA20 同时触发。', state: 'approved' },
    { id: ++db.ids.prop, ts: T0 - 6 * DAY, accountId: 2, instanceId: i3.id, instanceName: 'alt-momentum', current: { min_score: 70 }, proposed: { min_score: 55 }, rationale: '放宽入场分数以增加信号数量。', state: 'rejected' },
  )
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
    if (a.mode !== 'backtest') return ok(res, makeReport(a))
    return db.reports[a.id] ? ok(res, db.reports[a.id]) : send(res, 409, { error: '回测尚未完成' })
  }
  if ((r = p.match(/^trade\/backtests\/(\d+)\/run$/))) {
    const a = acct(+r[1]); if (!a || a.mode !== 'backtest') return notFound(res)
    const insts = db.instances.filter(i => i.accountId === a.id)
    insts.forEach(i => (i.state = 'running'))
    delete db.reports[a.id]
    db.equity[a.id] = []
    const full = equitySeries(a.deposit, a.from, a.to, 4 * HOUR, 0.0003, 0.008)
    let k = 0
    const tick = setInterval(() => {
      k += Math.ceil(full.length / 8)
      db.equity[a.id] = full.slice(0, k)
      refreshDerived(a)
      pushEvent('trade', { accountId: a.id, kind: 'backtest' })
      if (k >= full.length) {
        clearInterval(tick)
        if (!db.fills.some(f => f.accountId === a.id)) { genSignals(a, insts, 30, a.to - a.from); genFills(a, insts, 40, a.to - a.from); genLedger(a) }
        insts.forEach(i => { i.state = 'done'; i.heartbeat = now() })
        db.reports[a.id] = makeReport(a)
        pushEvent('trade', { accountId: a.id, kind: 'instance' })
      }
    }, 600)
    return ok(res, { ok: true })
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
  if (p === 'interact/prompts' && m === 'GET') return ok(res, { prompts: openPrompts(), now: now() })
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
  if (p === 'ai/test') { ai.lastCallAt = now(); return ok(res, ai.configured ? { ok: true, model: 'claude-sonnet-4-5', latencyMs: 2140 } : { ok: false, error: '未配置凭证' }) }
  if (p === 'ai/decisions') { const id = Number(q.get('accountId') || 0); return ok(res, db.decisions.filter(d => !id || d.accountId === id).slice(0, limit)) }
  if (p === 'ai/proposals' && m === 'GET') return ok(res, db.proposals)
  if ((r = p.match(/^ai\/proposals\/(\d+)$/))) {
    const x = db.proposals.find(y => y.id === +r[1]); if (!x) return notFound(res)
    x.state = body.approve ? 'approved' : 'rejected'
    if (body.approve) { const i = db.instances.find(y => y.id === x.instanceId); if (i) { Object.assign(i.params, x.proposed); i.configVersion++ } }
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
setInterval(() => pushEvent('trade', { accountId: 1, kind: 'fill' }), 20_000)
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
  if (/^\/api\/v1\/(trade|interact|ai)\//.test(p)) return mockApi(req, res, url).catch(e => send(res, 500, { error: String(e) }))
  if (p === '/api/v1/stream') return proxy(req, res, url, () => mockStream(res))
  if (p.startsWith('/api/')) {
    return proxy(req, res, url, () => {
      const fb = fallback(p.replace(/^\/api\/v1\//, ''))
      fb === undefined ? send(res, 502, { error: 'backend unreachable (mock)' }) : ok(res, fb)
    })
  }
  serveStatic(res, p)
}).listen(PORT, '127.0.0.1', () => console.log(`mock-trade on http://127.0.0.1:${PORT}  (backend ${BACKEND.href}, web ${DIST})`))

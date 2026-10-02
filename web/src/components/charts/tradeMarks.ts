/**
 * Trade nodes for Lightweight Charts (v5 series primitives).
 *
 * - EquityTradeMarks: on the account equity line. Every round trip is a coin bubble at its exit
 *   (above the curve for wins, below for losses, with a stem to the curve point); open trades sit
 *   at their entry. Bubble area ∝ notional; ring colour = result (win/loss); a badge marks the exit
 *   kind (止 stop, 盈 target, 爆 liquidation, 手 manual). Overlapping bubbles merge into a cluster
 *   with a count; zooming in splits them. The hovered/selected trade also shows its entry bubble,
 *   a dashed entry→exit connector and its holding span.
 * - CandleTradeMarks: on one instrument's candles. Every fill at its price and time (entry fills:
 *   dashed ring in the side colour with ▲/▼; exit fills: solid ring in the result colour + exit
 *   badge), holding spans shaded by result, connector for the highlighted trade.
 *
 * Both keep the hit areas of the last draw so the chart's crosshair / click events can be mapped
 * back to trades (hit()). Positions use logical-index interpolation, so times between data points
 * (fills inside a candle, trades between equity samples) land at the right x.
 */
import type { ISeriesPrimitive, IPrimitivePaneView, IPrimitivePaneRenderer, SeriesAttachedParameter, Time, ISeriesApi, SeriesType, IChartApiBase, Logical } from 'lightweight-charts'
import type { CanvasRenderingTarget2D } from 'fancy-canvas'
import type { Trade } from '../../tradeApi'
import { C, alpha } from '../../theme'

// ---------- shared ----------

/** Logical-index lookup over sorted chart times (seconds), interpolating between points. */
export class TimeIndex {
  constructor(public times: number[]) {}
  logical(t: number): number {
    const xs = this.times, n = xs.length
    if (!n) return 0
    if (n === 1) return 0
    if (t <= xs[0]) return -(xs[0] - t) / (xs[1] - xs[0] || 1)
    if (t >= xs[n - 1]) return n - 1 + (t - xs[n - 1]) / (xs[n - 1] - xs[n - 2] || 1)
    let lo = 0, hi = n - 1
    while (hi - lo > 1) { const m = (lo + hi) >> 1; if (xs[m] <= t) lo = m; else hi = m }
    return lo + (t - xs[lo]) / (xs[hi] - xs[lo] || 1)
  }
  /** Index of the last point at or before t (-1 before the first). */
  at(t: number): number {
    const xs = this.times
    let lo = 0, hi = xs.length - 1, r = -1
    while (lo <= hi) { const m = (lo + hi) >> 1; if (xs[m] <= t) { r = m; lo = m + 1 } else hi = m - 1 }
    return r
  }
}

/** Coin icons for canvas, loaded once per symbol; listeners redraw charts when an icon arrives. */
const icons = new Map<string, HTMLImageElement | 'err'>()
const iconListeners = new Set<() => void>()
export function coinIcon(instId: string): HTMLImageElement | null {
  const ccy = instId.split('-')[0].toLowerCase()
  const got = icons.get(ccy)
  if (got) return got === 'err' || !got.complete || !got.naturalWidth ? null : got // a failed load is "complete" too
  const img = new Image()
  icons.set(ccy, img)
  img.onload = () => iconListeners.forEach(f => f())
  img.onerror = () => { icons.set(ccy, 'err') }
  img.src = `/api/v1/icons/${ccy}.png`
  return null
}

export const exitBadge: Record<string, { g: string; c: string; bg: string } | undefined> = {
  stop: { g: '止', c: '#000', bg: C.amber },
  target: { g: '盈', c: '#000', bg: C.lime },
  liquidation: { g: '爆', c: '#fff', bg: C.down },
  manual: { g: '手', c: '#000', bg: '#b3b3b3' },
}
export const tradeWin = (t: Trade) => t.net > 0
export const isOpen = (t: Trade) => !t.closeTs
const resultColor = (t: Trade) => (isOpen(t) ? C.text : tradeWin(t) ? C.up : C.down)
const sideColor = (long: boolean) => (long ? C.up : C.down)

/** Bubble radius (px): area ∝ notional, 7–16 px (phones a bit smaller). */
export function radiusFor(notional: number, max: number, small = false) {
  const lo = small ? 6 : 7, hi = small ? 13 : 16
  return max > 0 ? lo + (hi - lo) * Math.sqrt(Math.max(0, notional) / max) : lo
}

const hue = (s: string) => [...s].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 360, 7)
interface BubbleStyle { ring: string; ringW: number; dashed?: boolean; badge?: { g: string; c: string; bg: string }; arrow?: 'up' | 'down'; dim?: boolean; glow?: boolean; icon?: boolean; fill?: string }
function bubble(ctx: CanvasRenderingContext2D, x: number, y: number, r: number, instId: string, s: BubbleStyle) {
  ctx.save()
  if (s.dim) ctx.globalAlpha = 0.28
  if (s.glow) {
    ctx.beginPath(); ctx.arc(x, y, r + 6, 0, Math.PI * 2)
    ctx.fillStyle = alpha('lime', 0.22); ctx.fill()
  }
  ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2)
  ctx.fillStyle = s.fill || '#141414'; ctx.fill()
  const inner = r - s.ringW
  if (s.icon !== false) {
    const img = coinIcon(instId)
    if (img) {
      ctx.save(); ctx.beginPath(); ctx.arc(x, y, inner, 0, Math.PI * 2); ctx.clip()
      ctx.drawImage(img, x - inner, y - inner, inner * 2, inner * 2)
      ctx.restore()
    } else {
      ctx.beginPath(); ctx.arc(x, y, inner, 0, Math.PI * 2)
      ctx.fillStyle = `hsl(${hue(instId.split('-')[0])},45%,35%)`; ctx.fill()
      ctx.fillStyle = '#fff'; ctx.font = `700 ${Math.max(7, inner * 0.9)}px system-ui, sans-serif`
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle'
      ctx.fillText(instId.slice(0, inner > 7 ? 2 : 1), x, y + 0.5)
    }
  } else if (s.arrow) {
    ctx.fillStyle = s.ring
    ctx.beginPath()
    const a = Math.max(3, inner * 0.55), up = s.arrow === 'up'
    ctx.moveTo(x, y + (up ? -a : a)); ctx.lineTo(x - a, y + (up ? a * 0.7 : -a * 0.7)); ctx.lineTo(x + a, y + (up ? a * 0.7 : -a * 0.7)); ctx.closePath(); ctx.fill()
  }
  ctx.beginPath(); ctx.arc(x, y, r - s.ringW / 2, 0, Math.PI * 2)
  ctx.lineWidth = s.ringW; ctx.strokeStyle = s.ring
  ctx.setLineDash(s.dashed ? [2.5, 2] : [])
  ctx.stroke(); ctx.setLineDash([])
  const corner = (glyph: string, color: string, bg: string, dx: number) => {
    const br = Math.max(5.5, r * 0.42), bx = x + dx * r * 0.72, by = y + r * 0.72
    ctx.beginPath(); ctx.arc(bx, by, br, 0, Math.PI * 2); ctx.fillStyle = bg; ctx.fill()
    ctx.lineWidth = 1.5; ctx.strokeStyle = '#0e0e0e'; ctx.stroke()
    ctx.fillStyle = color; ctx.font = `700 ${br * 1.25}px system-ui, sans-serif`; ctx.textAlign = 'center'; ctx.textBaseline = 'middle'
    ctx.fillText(glyph, bx, by + 0.5)
  }
  if (s.badge) corner(s.badge.g, s.badge.c, s.badge.bg, 1)
  if (s.arrow && s.icon !== false) corner(s.arrow === 'up' ? '▲' : '▼', '#fff', s.arrow === 'up' ? C.up : C.down, -1)
  ctx.restore()
}

interface Node { id: number; x: number; y: number; r: number; ax?: number; ay?: number }
export interface Hit { x: number; y: number; r: number; ids: number[] }
/** Greedy screen-space clustering in x order: a node joins the previous cluster when they overlap. */
function cluster<T extends Node>(nodes: T[]): T[][] {
  const out: T[][] = []
  for (const n of [...nodes].sort((a, b) => a.x - b.x)) {
    const c = out[out.length - 1]
    const last = c?.[c.length - 1]
    if (c && last && Math.hypot(n.x - last.x, n.y - last.y) < (n.r + last.r) * 0.8) c.push(n)
    else out.push([n])
  }
  return out
}
function hitAt(hits: Hit[], x: number, y: number): Hit | null {
  let best: Hit | null = null, bd = Infinity
  for (const h of hits) {
    const d = Math.hypot(h.x - x, h.y - y)
    if (d <= h.r + 4 && d < bd) { best = h; bd = d }
  }
  return best
}

abstract class Marks implements ISeriesPrimitive<Time> {
  protected chart?: IChartApiBase<Time>
  protected series?: ISeriesApi<SeriesType>
  protected requestUpdate?: () => void
  protected trades: Trade[] = []
  protected idx = new TimeIndex([])
  protected hover = 0
  protected selected = 0
  protected dimmed: Set<number> | null = null
  protected hits: Hit[] = []
  protected small = false
  private view: IPrimitivePaneView
  private redraw = () => this.requestUpdate?.()
  constructor() {
    const r: IPrimitivePaneRenderer = { draw: t => this.draw(t), drawBackground: t => this.drawBg(t) }
    this.view = { zOrder: () => 'top', renderer: () => r }
  }
  attached(p: SeriesAttachedParameter<Time>) { this.chart = p.chart; this.series = p.series; this.requestUpdate = p.requestUpdate; iconListeners.add(this.redraw) }
  detached() { iconListeners.delete(this.redraw); this.chart = this.series = this.requestUpdate = undefined }
  paneViews() { return [this.view] }
  setHighlight(hover: number, selected: number) { if (hover !== this.hover || selected !== this.selected) { this.hover = hover; this.selected = selected; this.redraw() } }
  /** Trades outside the list filter are drawn faint (null = none dimmed). */
  setDimmed(ids: Set<number> | null) { this.dimmed = ids; this.redraw() }
  setSmall(on: boolean) { this.small = on }
  hit(x: number, y: number) { return hitAt(this.hits, x, y) }
  /** Hit areas of the last draw (pane coordinates); exposed on the chart element for UI tests. */
  hitList(): readonly Hit[] { return this.hits }
  /** x of chart time tSec. logicalToCoordinate only handles whole logical indexes, so interpolate between neighbours. */
  protected x(tSec: number) { return this.lx(this.idx.logical(tSec)) }
  protected lx(l: number): number | null {
    const ts = this.chart?.timeScale()
    if (!ts || !isFinite(l)) return null
    const i = Math.floor(l)
    const a = ts.logicalToCoordinate(i as Logical), b = ts.logicalToCoordinate((i + 1) as Logical)
    if (a == null || b == null) return a
    return a + (b - a) * (l - i)
  }
  protected abstract draw(t: CanvasRenderingTarget2D): void
  protected abstract drawBg(t: CanvasRenderingTarget2D): void
}

const sec = (ms: number, toChart: (ms: number) => number) => toChart(ms)

// ---------- equity ----------

export class EquityTradeMarks extends Marks {
  private values: number[] = []
  private maxN = 0
  private toChart: (ms: number) => number = ms => ms / 1000

  setData(times: number[], values: number[], trades: Trade[], toChart: (ms: number) => number) {
    this.idx = new TimeIndex(times)
    this.values = values
    this.trades = trades
    this.toChart = toChart
    this.maxN = Math.max(0, ...trades.map(t => t.notional))
    this.requestUpdate?.()
  }
  /** Equity interpolated at chart time t. */
  valueAt(t: number) {
    const i = this.idx.at(t), v = this.values, xs = this.idx.times
    if (i < 0) return v[0]
    if (i >= v.length - 1) return v[v.length - 1]
    const f = (t - xs[i]) / (xs[i + 1] - xs[i] || 1)
    return v[i] + (v[i + 1] - v[i]) * f
  }
  private anchor(t: Trade) {
    const ts = sec(t.closeTs || t.openTs, this.toChart)
    const x = this.x(ts), y = this.series?.priceToCoordinate(this.valueAt(ts))
    return x == null || y == null ? null : { x, y }
  }
  private entry(t: Trade) {
    const ts = sec(t.openTs, this.toChart)
    const x = this.x(ts), y = this.series?.priceToCoordinate(this.valueAt(ts))
    return x == null || y == null ? null : { x, y }
  }

  protected drawBg(target: CanvasRenderingTarget2D) {
    const t = this.trades.find(x => x.id === (this.hover || this.selected))
    if (!t) return
    target.useMediaCoordinateSpace(({ context: ctx, mediaSize }) => {
      const a = this.x(sec(t.openTs, this.toChart)), b = this.x(sec(t.closeTs || t.openTs, this.toChart))
      if (a == null || b == null) return
      ctx.fillStyle = isOpen(t) ? alpha('ink', 0.05) : tradeWin(t) ? alpha('up', 0.09) : alpha('down', 0.09)
      ctx.fillRect(Math.min(a, b), 0, Math.max(2, Math.abs(b - a)), mediaSize.height)
    })
  }

  protected draw(target: CanvasRenderingTarget2D) {
    if (!this.series || !this.trades.length) { this.hits = []; return }
    target.useMediaCoordinateSpace(({ context: ctx, mediaSize: { width: W, height: H } }) => {
      // Density: sweep wins (drawn above the curve) and losses (below) separately in x order and merge
      // nodes closer than one column (≈ a bubble width) into a group. At most one group per column and
      // side stays readable with hundreds of trades; zooming in widens the gaps and splits the groups.
      const byId = new Map<number, Trade>()
      const sides: Record<'up' | 'dn', Node[]> = { up: [], dn: [] }
      for (const t of this.trades) {
        byId.set(t.id, t)
        const a = this.anchor(t)
        if (!a || a.x < -40 || a.x > W + 40) continue
        sides[isOpen(t) || tradeWin(t) ? 'up' : 'dn'].push({ id: t.id, x: a.x, y: a.y, r: radiusFor(t.notional, this.maxN, this.small), ax: a.x, ay: a.y })
      }
      const COL = this.small ? 26 : 32
      const place = (ay: number, r: number, up: boolean) => {
        let y = up ? ay - r - 8 : ay + r + 8
        if (y - r < 2) y = ay + r + 8 // keep inside the pane
        if (y + r > H - 2) y = ay - r - 8
        return y
      }
      const hits: Hit[] = []
      const hl = new Set([this.hover, this.selected].filter(Boolean))
      for (const side of ['up', 'dn'] as const) {
        const xs = sides[side].sort((a, b) => a.x - b.x)
        const groups: Node[][] = []
        for (const n of xs) {
          const g = groups[groups.length - 1]
          if (g && n.x - g[0].x < COL) g.push(n)
          else groups.push([n])
        }
        for (const g of groups) {
          const vis = g.filter(n => !hl.has(n.id))
          if (!vis.length) continue
          if (vis.length === 1) {
            const n0 = vis[0], t = byId.get(n0.id)!
            const n = { ...n0, y: place(n0.ay!, n0.r, side === 'up') }
            this.stem(ctx, n, resultColor(t), this.dimmed != null && !this.dimmed.has(n.id))
            this.exitBubble(ctx, t, n, false)
            hits.push({ x: n.x, y: n.y, r: n.r, ids: [n.id] })
            continue
          }
          // group: biggest coins stacked + count; ring = side's result colour
          const ids = vis.map(n => n.id)
          const cx = vis.reduce((s2, n) => s2 + n.x, 0) / vis.length
          const ay = vis.reduce((s2, n) => s2 + (n.ay ?? 0), 0) / vis.length
          const r = Math.min(this.small ? 14 : 17, (this.small ? 9 : 10) + 1.6 * Math.log2(vis.length))
          const cy = place(ay, r, side === 'up')
          const dim = this.dimmed != null && !ids.some(id => this.dimmed!.has(id))
          const ring = side === 'up' ? C.up : C.down
          this.stem(ctx, { id: 0, x: cx, y: cy, r, ax: cx, ay }, ring, dim)
          const top = vis.slice().sort((p2, q) => byId.get(q.id)!.notional - byId.get(p2.id)!.notional).slice(0, 2).reverse()
          top.forEach((n, k) => {
            const off = (top.length - 1 - k) * 3
            bubble(ctx, cx + off, cy - off, r, byId.get(n.id)!.instId, { ring, ringW: 2, dim })
          })
          const label = String(vis.length), bw = Math.max(15, 6 + label.length * 6.5)
          ctx.save()
          if (dim) ctx.globalAlpha = 0.28
          ctx.fillStyle = C.lime
          roundRect(ctx, cx + r * 0.35, cy - r - 5, bw, 14, 7); ctx.fill()
          ctx.lineWidth = 1.5; ctx.strokeStyle = '#0e0e0e'; ctx.stroke()
          ctx.fillStyle = '#000'; ctx.font = '700 10px system-ui, sans-serif'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle'
          ctx.fillText(label, cx + r * 0.35 + bw / 2, cy - r + 2.5)
          ctx.restore()
          hits.push({ x: cx, y: cy, r: r + 4, ids })
        }
      }
      // highlighted trades: entry bubble + connector + exit on top
      for (const id of hl) {
        const t = byId.get(id)
        if (!t) continue
        const a = this.anchor(t), e = this.entry(t)
        if (!a) continue
        const r = radiusFor(t.notional, this.maxN, this.small)
        const up = isOpen(t) || tradeWin(t)
        let y = up ? a.y - r - 8 : a.y + r + 8
        if (y - r < 2) y = a.y + r + 8
        if (y + r > H - 2) y = a.y - r - 8
        const n = { id, x: a.x, y, r, ax: a.x, ay: a.y }
        if (e && !isOpen(t)) {
          const er = r * 0.85
          const ey = e.y - er - 8 < 2 ? e.y + er + 8 : e.y - er - 8
          ctx.save(); ctx.setLineDash([4, 3]); ctx.lineWidth = 1.5; ctx.strokeStyle = alpha('lime', 0.8)
          ctx.beginPath(); ctx.moveTo(e.x, ey); ctx.lineTo(n.x, n.y); ctx.stroke(); ctx.restore()
          this.stem(ctx, { id, x: e.x, y: ey, r: er, ax: e.x, ay: e.y }, sideColor(t.posSide === 'long'), false)
          bubble(ctx, e.x, ey, er, t.instId, { ring: sideColor(t.posSide === 'long'), ringW: 2, dashed: true, arrow: t.posSide === 'long' ? 'up' : 'down', glow: false })
          hits.push({ x: e.x, y: ey, r: er, ids: [id] })
        }
        this.stem(ctx, n, resultColor(t), false)
        this.exitBubble(ctx, t, n, true)
        hits.push({ x: n.x, y: n.y, r: n.r, ids: [id] })
      }
      this.hits = hits
    })
  }

  private stem(ctx: CanvasRenderingContext2D, n: Node, color: string, dim: boolean) {
    if (n.ay == null || n.ax == null) return
    ctx.save()
    if (dim) ctx.globalAlpha = 0.28
    ctx.strokeStyle = color; ctx.lineWidth = 1
    const dir = n.y < n.ay ? 1 : -1
    ctx.beginPath(); ctx.moveTo(n.ax, n.ay); ctx.lineTo(n.x, n.y + dir * n.r); ctx.stroke()
    ctx.beginPath(); ctx.arc(n.ax, n.ay, 2.5, 0, Math.PI * 2); ctx.fillStyle = color; ctx.fill()
    ctx.restore()
  }
  private exitBubble(ctx: CanvasRenderingContext2D, t: Trade, n: Node, glow: boolean) {
    const open = isOpen(t)
    bubble(ctx, n.x, n.y, n.r, t.instId, {
      ring: open ? sideColor(t.posSide === 'long') : resultColor(t), ringW: open ? 2 : 2.5, dashed: open,
      badge: open ? undefined : exitBadge[t.exitKind], arrow: open ? (t.posSide === 'long' ? 'up' : 'down') : undefined,
      dim: !glow && this.dimmed != null && !this.dimmed.has(t.id), glow,
    })
  }
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath()
  ctx.moveTo(x + r, y); ctx.arcTo(x + w, y, x + w, y + h, r); ctx.arcTo(x + w, y + h, x, y + h, r)
  ctx.arcTo(x, y + h, x, y, r); ctx.arcTo(x, y, x + w, y, r); ctx.closePath()
}

// ---------- candles ----------

export class CandleTradeMarks extends Marks {
  private maxN = 0
  private toChart: (ms: number) => number = ms => ms / 1000
  setData(times: number[], trades: Trade[], toChart: (ms: number) => number) {
    this.idx = new TimeIndex(times)
    this.trades = trades
    this.toChart = toChart
    this.maxN = Math.max(0, ...trades.map(t => t.notional))
    this.requestUpdate?.()
  }
  /** Logical range [entry, exit] of a trade (for zooming to it). */
  span(t: Trade): [number, number] {
    return [this.idx.logical(this.toChart(t.openTs)), this.idx.logical(this.toChart(t.closeTs || t.fills[t.fills.length - 1]?.ts || t.openTs))]
  }

  protected drawBg(target: CanvasRenderingTarget2D) {
    target.useMediaCoordinateSpace(({ context: ctx, mediaSize: { width: W, height: H } }) => {
      const lastX = this.chart?.timeScale().logicalToCoordinate((this.idx.times.length - 1) as Logical) ?? W
      for (const t of this.trades) {
        const a = this.x(this.toChart(t.openTs)), b = t.closeTs ? this.x(this.toChart(t.closeTs)) : lastX
        if (a == null || b == null || b < 0 || a > W) continue
        const on = t.id === this.selected || t.id === this.hover
        const dim = this.dimmed != null && !this.dimmed.has(t.id)
        const k = isOpen(t) ? 'ink' : tradeWin(t) ? 'up' : 'down'
        ctx.fillStyle = alpha(k, on ? 0.14 : dim ? 0.02 : 0.055)
        ctx.fillRect(a, 0, Math.max(2, b - a), H)
        if (on) {
          ctx.fillStyle = alpha(k, 0.6)
          ctx.fillRect(a, 0, 1, H); ctx.fillRect(b - 1, 0, 1, H)
        }
      }
    })
  }

  protected draw(target: CanvasRenderingTarget2D) {
    const s = this.series
    if (!s || !this.trades.length) { this.hits = []; return }
    target.useMediaCoordinateSpace(({ context: ctx, mediaSize: { width: W } }) => {
      const nodes: (Node & { t: Trade; f: Trade['fills'][number]; exit: boolean })[] = []
      for (const t of this.trades) {
        for (const f of t.fills) {
          const x = this.x(this.toChart(f.ts)), y = s.priceToCoordinate(f.px)
          if (x == null || y == null || x < -30 || x > W + 30) continue
          const exit = f.kind === 'close' || f.kind === 'reduce'
          const r = radiusFor(f.qty * t.ctVal * f.px, this.maxN, true) * (exit ? 1 : 0.9)
          nodes.push({ id: t.id, x, y, r, t, f, exit })
        }
      }
      const hl = new Set([this.hover, this.selected].filter(Boolean))
      // connector for highlighted trades: entry fill → exit fill
      for (const id of hl) {
        const ns = nodes.filter(n => n.id === id)
        const a = ns.find(n => !n.exit), b = [...ns].reverse().find(n => n.exit)
        if (a && b) {
          ctx.save(); ctx.setLineDash([4, 3]); ctx.lineWidth = 1.5; ctx.strokeStyle = alpha('lime', 0.85)
          ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.stroke(); ctx.restore()
        }
      }
      const hits: Hit[] = []
      const groups = cluster(nodes.filter(n => !hl.has(n.id)))
      const draw = (n: (typeof nodes)[number], glow: boolean) => {
        const long = n.t.posSide === 'long'
        const dim = !glow && this.dimmed != null && !this.dimmed.has(n.id)
        if (n.exit) bubble(ctx, n.x, n.y, n.r, n.t.instId, { ring: resultColor(n.t), ringW: 2.5, icon: false, arrow: n.f.side === 'buy' ? 'up' : 'down', fill: '#141414', badge: n.f.kind === 'close' ? exitBadge[n.t.exitKind] : undefined, dim, glow })
        else bubble(ctx, n.x, n.y, n.r, n.t.instId, { ring: sideColor(long), ringW: 2, dashed: true, icon: false, arrow: long ? 'up' : 'down', fill: alpha(long ? 'up' : 'down', 0.18), dim, glow })
      }
      for (const g of groups) {
        if (g.length === 1) { draw(g[0], false); hits.push({ x: g[0].x, y: g[0].y, r: g[0].r, ids: [g[0].id] }); continue }
        const ids = [...new Set(g.map(n => n.id))]
        const cx = g.reduce((s2, n) => s2 + n.x, 0) / g.length, cy = g.reduce((s2, n) => s2 + n.y, 0) / g.length
        const r = Math.min(16, Math.max(...g.map(n => n.r)) + 1)
        const net = ids.reduce((s2, id) => s2 + (this.trades.find(t => t.id === id)?.net ?? 0), 0)
        bubble(ctx, cx, cy, r, g[0].t.instId, { ring: net > 0 ? C.up : net < 0 ? C.down : C.text, ringW: 2, icon: false, fill: '#1f1f1f' })
        ctx.fillStyle = C.lime; ctx.font = '700 10px system-ui, sans-serif'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle'
        ctx.fillText(String(g.length), cx, cy + 0.5)
        hits.push({ x: cx, y: cy, r: r + 3, ids })
      }
      for (const n of nodes.filter(n2 => hl.has(n2.id))) { draw(n, true); hits.push({ x: n.x, y: n.y, r: n.r, ids: [n.id] }) }
      this.hits = hits
    })
  }
}

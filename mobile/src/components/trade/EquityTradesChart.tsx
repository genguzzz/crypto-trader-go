// 组合视角 chart (web charts/EquityTradesChart.vue + tradeMarks.ts EquityTradeMarks), native SVG.
// Equity line with the deposit as a dashed baseline, a drawdown pane below, and every round trip as a
// coin bubble at its exit (wins above the curve, losses below, with a stem; open trades at their entry).
// Bubble area ∝ notional; ring = result; badge = exit kind (止 / 盈 / 爆 / 手). Bubbles closer than one
// column merge into a group with a count; tapping a group zooms to it. The selected trade shows every
// fill on the curve, linked in time order, and its holding span.
// Touch: drag with one finger = readout (legend: time, equity, PnL, drawdown, open positions at that
// moment); tap a bubble = pick; tap blank = clear; two fingers = pinch zoom / pan; 复位 resets.
// Layers: SVG (lines, stems, bubble fills) → coin icons (CoinIcon, cached) → SVG (rings, badges).
import { memo, useMemo, useRef, useState } from "react";
import { Pressable, Text, View, type GestureResponderEvent, type LayoutChangeEvent } from "react-native";
import Svg, { Circle, G, Line, Path, Rect, Text as SvgText } from "react-native-svg";
import { time } from "@/lib/fmt";
import type { Trade } from "@/lib/tradeApi";
import { amt, pctf } from "@/lib/tradeMeta";
import { colors, fs, tnum } from "@/lib/theme";
import { CoinIcon } from "../CoinIcon";
import { tap } from "../ui";

const PAD = { l: 4, r: 54, t: 4, b: 18 };
const LIME = colors.primary;
const sym = (id: string) => id.replace("-USDT-SWAP", "").split("-")[0];

const exitBadge: Record<string, { g: string; c: string; bg: string } | undefined> = {
  stop: { g: "止", c: "#000", bg: colors.warn },
  target: { g: "盈", c: "#000", bg: LIME },
  liquidation: { g: "爆", c: "#fff", bg: colors.down },
  manual: { g: "手", c: "#000", bg: "#b3b3b3" },
};
const isOpen = (t: Trade) => !t.closeTs;
const win = (t: Trade) => t.net > 0;
const resultColor = (t: Trade) => (isOpen(t) ? "#8a8a8a" : win(t) ? colors.up : colors.down);
const sideColor = (long: boolean) => (long ? colors.up : colors.down);
const upA = (a: number) => `rgba(37,167,80,${a})`, downA = (a: number) => `rgba(202,63,100,${a})`;

/** Bubble radius (px): area ∝ notional (web radiusFor). */
function radiusFor(notional: number, max: number, small: boolean) {
  const lo = small ? 3.6 : 5.5, hi = small ? 8 : 12;
  return max > 0 ? lo + (hi - lo) * Math.sqrt(Math.max(0, notional) / max) : lo;
}

interface Bubble {
  key: string; x: number; y: number; r: number; instId: string; ring: string; ringW: number; dashed?: boolean; icon: boolean; fill?: string;
  badge?: { g: string; c: string; bg: string }; arrow?: "up" | "down"; dim?: boolean; glow?: boolean; count?: number;
}
interface Stem { key: string; x: number; y: number; ax: number; ay: number; r: number; color: string; dim?: boolean }
interface Hit { x: number; y: number; r: number; ids: number[] }
interface Series { ts: number[]; vs: number[]; dds: number[] }
interface Geo {
  iw: number; eqTop: number; eqH: number; ddTop: number; ddH: number; i0: number; i1: number; lo: number; hi: number; ddMin: number;
  x: (t: number) => number; y: (v: number) => number; yd: (d: number) => number; tAt: (px: number) => number;
}

/** Last index with xs[i] <= t (-1 before the first). */
function idxAt(xs: number[], t: number) {
  let lo = 0, hi = xs.length - 1, r = -1;
  while (lo <= hi) { const m = (lo + hi) >> 1; if (xs[m] <= t) { r = m; lo = m + 1; } else hi = m - 1; }
  return r;
}

/** Equity interpolated at time t. */
function valueAt({ ts, vs }: Series, t: number) {
  const i = idxAt(ts, t);
  if (i < 0) return vs[0];
  if (i >= vs.length - 1) return vs[vs.length - 1];
  return vs[i] + (vs[i + 1] - vs[i]) * ((t - ts[i]) / (ts[i + 1] - ts[i] || 1));
}

function ticks(min: number, max: number, n = 4) {
  if (!(max > min)) return [min];
  const raw = (max - min) / n;
  const p = Math.pow(10, Math.floor(Math.log10(raw)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * p).find((s) => s >= raw) ?? raw;
  const out: number[] = [];
  for (let v = Math.ceil(min / step) * step; v <= max + 1e-12; v += step) out.push(v);
  return out;
}

export interface EquityTradesChartProps {
  points: [number, number][]; deposit: number; trades: Trade[]; selectedId: number; dimmed: Set<number> | null; height: number;
  /** Tap: one trade id, a group's ids (the chart zooms to it itself), or [] for blank. */
  onPick: (ids: number[]) => void;
  onTouchActive?: (on: boolean) => void;
}

export function EquityTradesChart({ points, deposit, trades, selectedId, dimmed, height, onPick, onTouchActive }: EquityTradesChartProps) {
  const [w, setW] = useState(0);
  const onLayout = (e: LayoutChangeEvent) => setW(Math.round(e.nativeEvent.layout.width));
  const [hover, setHover] = useState<number | null>(null); // x of the finger (readout)
  const [view, setView] = useState<[number, number] | null>(null); // zoomed time range (null = all)

  // deduped, strictly increasing samples + drawdown from the running peak
  const data = useMemo((): Series => {
    const ts: number[] = [], vs: number[] = [], dds: number[] = [];
    let last = -Infinity, peak = -Infinity;
    for (const [t, v] of points) {
      if (!(t > last) || !isFinite(v)) continue;
      last = t; peak = Math.max(peak, v);
      ts.push(t); vs.push(v); dds.push(peak > 0 ? v / peak - 1 : 0);
    }
    return { ts, vs, dds };
  }, [points]);
  const T0 = data.ts[0] ?? 0, T1 = data.ts[data.ts.length - 1] ?? 1;
  // a zoom outside the current data (range switch, reload) falls back to the whole period
  const [v0, v1] = view && view[0] >= T0 && view[1] <= T1 ? view : [T0, T1];

  const geo = useMemo((): Geo | null => {
    const { ts, vs, dds } = data;
    if (ts.length < 2 || !w) return null;
    const iw = w - PAD.l - PAD.r;
    const plotH = height - PAD.t - PAD.b;
    const ddH = Math.max(40, Math.round(plotH * 0.18));
    const eqTop = PAD.t, eqH = plotH - ddH - 6, ddTop = eqTop + eqH + 6;
    const i0 = Math.max(0, idxAt(ts, v0)), i1 = Math.min(ts.length - 1, idxAt(ts, v1) + 1);
    let lo = Infinity, hi = -Infinity, ddMin = 0;
    for (let i = i0; i <= i1; i++) { lo = Math.min(lo, vs[i]); hi = Math.max(hi, vs[i]); ddMin = Math.min(ddMin, dds[i]); }
    if (!(hi > lo)) { lo -= 1; hi += 1; }
    if (ddMin > -1e-4) ddMin = -0.01;
    const span = v1 - v0 || 1;
    const x = (t: number) => PAD.l + ((t - v0) / span) * iw;
    // web scaleMargins: 16% free on top (room for win bubbles), 10% at the bottom
    const y = (v: number) => eqTop + eqH * 0.16 + (1 - (v - lo) / (hi - lo)) * eqH * 0.74;
    const yd = (d: number) => ddTop + (d / ddMin) * ddH;
    return { iw, eqTop, eqH, ddTop, ddH, i0, i1, lo, hi, ddMin, x, y, yd, tAt: (px: number) => v0 + ((px - PAD.l) / iw) * span };
  }, [data, w, height, v0, v1]);

  const marks = useMemo((): Marks | null => {
    if (!geo) return null;
    const small = w < 600;
    const maxN = Math.max(0, ...trades.map((t) => t.notional));
    const top = geo.eqTop, bottom = geo.eqTop + geo.eqH;
    const place = (ay: number, r: number, up: boolean) => {
      let y = up ? ay - r - 8 : ay + r + 8;
      if (y - r < top + 2) y = ay + r + 8;
      if (y + r > bottom - 2) y = ay - r - 8;
      return y;
    };
    const isDim = (id: number) => dimmed != null && !dimmed.has(id);
    const byId = new Map(trades.map((t) => [t.id, t]));
    const bubbles: Bubble[] = [], stems: Stem[] = [], hits: Hit[] = [];
    const exitBubble = (t: Trade, x: number, y: number, r: number, glow: boolean): Bubble => {
      const open = isOpen(t);
      return {
        key: `e${t.id}`, x, y, r, instId: t.instId, icon: true, ring: open ? sideColor(t.posSide === "long") : resultColor(t), ringW: open ? 2 : 2.5, dashed: open,
        badge: open ? undefined : exitBadge[t.exitKind], arrow: open ? (t.posSide === "long" ? "up" : "down") : undefined, dim: !glow && isDim(t.id), glow,
      };
    };
    const sides: Record<"up" | "dn", { id: number; x: number; ay: number; r: number }[]> = { up: [], dn: [] };
    for (const t of trades) {
      const at = t.closeTs || t.openTs;
      const x = geo.x(at);
      if (x < PAD.l - 10 || x > PAD.l + geo.iw + 10) continue;
      sides[isOpen(t) || win(t) ? "up" : "dn"].push({ id: t.id, x, ay: geo.y(valueAt(data, at)), r: radiusFor(t.notional, maxN, small) });
    }
    const COL = small ? 18 : 26;
    for (const side of ["up", "dn"] as const) {
      const groups: (typeof sides.up)[] = [];
      for (const n of sides[side].sort((a, b) => a.x - b.x)) {
        const g = groups[groups.length - 1];
        if (g && n.x - g[0].x < COL) g.push(n);
        else groups.push([n]);
      }
      for (const g of groups) {
        const vis = g.filter((n) => n.id !== selectedId);
        if (!vis.length) continue;
        if (vis.length === 1) {
          const n = vis[0], t = byId.get(n.id)!;
          const y = place(n.ay, n.r, side === "up");
          stems.push({ key: `s${n.id}`, x: n.x, y, ax: n.x, ay: n.ay, r: n.r, color: resultColor(t), dim: isDim(n.id) });
          bubbles.push(exitBubble(t, n.x, y, n.r, false));
          hits.push({ x: n.x, y, r: n.r, ids: [n.id] });
          continue;
        }
        // group: the two biggest coins stacked + a count; ring = the side's result colour
        const ids = vis.map((n) => n.id);
        const cx = vis.reduce((s, n) => s + n.x, 0) / vis.length;
        const ay = vis.reduce((s, n) => s + n.ay, 0) / vis.length;
        const r = Math.min(small ? 9 : 13, (small ? 5.5 : 8) + (small ? 0.9 : 1.3) * Math.log2(vis.length));
        const cy = place(ay, r, side === "up");
        const dim = dimmed != null && !ids.some((id) => dimmed.has(id));
        const ring = side === "up" ? colors.up : colors.down;
        stems.push({ key: `g${ids[0]}`, x: cx, y: cy, ax: cx, ay, r, color: ring, dim });
        const big = vis.slice().sort((a, b) => byId.get(b.id)!.notional - byId.get(a.id)!.notional).slice(0, 2).reverse();
        big.forEach((n, k) => {
          const off = (big.length - 1 - k) * 3;
          bubbles.push({ key: `g${ids[0]}-${k}`, x: cx + off, y: cy - off, r, instId: byId.get(n.id)!.instId, icon: true, ring, ringW: 2, dim, count: k === big.length - 1 ? vis.length : undefined });
        });
        hits.push({ x: cx, y: cy, r: r + 4, ids });
      }
    }
    // the selected trade: every fill as its own node on the curve, linked in time order; the coin at the close
    let chain: { x: number; y: number }[] = [], span: { a: number; b: number; color: string } | null = null;
    const t = byId.get(selectedId);
    if (t) {
      const up = isOpen(t) || win(t);
      const fills = [...t.fills].sort((a, b) => a.ts - b.ts || a.id - b.id);
      const at = t.closeTs || t.openTs;
      const R = radiusFor(t.notional, maxN, small);
      const lx = geo.x(at), lay = geo.y(valueAt(data, at));
      const last = { x: lx, y: place(lay, R, up) };
      const nodes = fills.filter((f) => isOpen(t) || f.kind !== "close").map((f) => {
        const x = geo.x(f.ts), ay = geo.y(valueAt(data, f.ts)), r = radiusFor(f.qty * t.ctVal * f.px, maxN, small) * 0.85;
        return { f, x, ay, r, y: place(ay, r, up) };
      });
      chain = [...nodes, last];
      span = { a: geo.x(t.openTs), b: geo.x(at), color: isOpen(t) ? "rgba(250,250,250,0.05)" : win(t) ? upA(0.09) : downA(0.09) };
      for (const q of nodes) {
        const opening = q.f.kind === "open" || q.f.kind === "add";
        const col = opening ? sideColor(t.posSide === "long") : resultColor(t);
        stems.push({ key: `f${q.f.id}`, x: q.x, y: q.y, ax: q.x, ay: q.ay, r: q.r, color: col });
        bubbles.push({ key: `f${q.f.id}`, x: q.x, y: q.y, r: q.r, instId: t.instId, icon: false, ring: col, ringW: 1.8, dashed: opening, arrow: q.f.side === "buy" ? "up" : "down", fill: opening ? (t.posSide === "long" ? upA(0.2) : downA(0.2)) : "#141414" });
        hits.push({ x: q.x, y: q.y, r: q.r, ids: [t.id] });
      }
      stems.push({ key: `s${t.id}`, x: last.x, y: last.y, ax: lx, ay: lay, r: R, color: resultColor(t) });
      bubbles.push(exitBubble(t, last.x, last.y, R, true));
      hits.push({ x: last.x, y: last.y, r: R, ids: [t.id] });
    }
    return { bubbles, stems, hits, chain, span };
  }, [data, geo, trades, selectedId, dimmed, w]);

  // ---- touch: one finger = readout / tap, two fingers = pinch zoom + pan ----
  const g = useRef({ x0: 0, y0: 0, at: 0, moved: false, multi: false, d0: 0, tc: 0, span0: 0 });
  const full = T1 - T0 || 1;
  const minSpan = Math.max(full / 500, 10 * 60_000);
  const clampView = (a: number, span: number): [number, number] | null => {
    span = Math.min(full, Math.max(minSpan, span));
    if (span >= full * 0.999) return null;
    a = Math.min(T1 - span, Math.max(T0, a));
    return [a, a + span];
  };
  const zoomTo = (from: number, to: number) => {
    const pad = Math.max((to - from) * 0.35, minSpan / 2);
    setView(clampView(from - pad, to - from + 2 * pad));
  };
  const clampX = (x: number) => Math.max(PAD.l, Math.min(w - PAD.r, x));
  const onGrant = (e: GestureResponderEvent) => {
    const { locationX, locationY } = e.nativeEvent;
    g.current = { ...g.current, x0: locationX, y0: locationY, at: Date.now(), moved: false, multi: false };
    onTouchActive?.(true);
    setHover(clampX(locationX));
  };
  const onMove = (e: GestureResponderEvent) => {
    const ts = e.nativeEvent.touches ?? []; // RN-web mouse events have none
    const s = g.current;
    if (ts.length >= 2 && geo) {
      const [a, b] = [ts[0], ts[1]];
      const d = Math.hypot(a.locationX - b.locationX, a.locationY - b.locationY), cx = (a.locationX + b.locationX) / 2;
      if (!s.multi) { s.multi = true; s.moved = true; s.d0 = d || 1; s.tc = geo.tAt(cx); s.span0 = v1 - v0; setHover(null); return; }
      const span = Math.min(full, Math.max(minSpan, (s.span0 * s.d0) / (d || 1)));
      setView(clampView(s.tc - ((cx - PAD.l) / geo.iw) * span, span));
      return;
    }
    if (s.multi) return; // lifting one finger of a pinch doesn't start a readout
    const { locationX, locationY } = e.nativeEvent;
    if (Math.hypot(locationX - s.x0, locationY - s.y0) > 8) s.moved = true;
    setHover(clampX(locationX));
  };
  const onEnd = (e: GestureResponderEvent | null) => {
    const s = g.current;
    setHover(null);
    onTouchActive?.(false);
    if (!e || s.moved || s.multi || Date.now() - s.at > 500 || !marks) return;
    let best: Hit | null = null, bd = Infinity;
    for (const h of marks.hits) {
      const d = Math.hypot(h.x - s.x0, h.y - s.y0);
      if (d <= h.r + 8 && d < bd) { best = h; bd = d; }
    }
    if (best && best.ids.length > 1) {
      const ts = best.ids.map((id) => trades.find((t) => t.id === id)).filter((t): t is Trade => !!t);
      tap();
      zoomTo(Math.min(...ts.map((t) => t.openTs)), Math.max(...ts.map((t) => t.closeTs || t.openTs)));
      return;
    }
    if (best) tap();
    onPick(best ? best.ids : []);
  };

  // ---- legend (finger time, else the latest sample) ----
  const legend = useMemo(() => {
    const { ts, vs, dds } = data;
    if (!ts.length) return null;
    const live = hover == null || !geo;
    const i = live ? ts.length - 1 : Math.max(0, idxAt(ts, geo.tAt(hover)));
    const t = ts[i], v = vs[i];
    const open = trades.filter((x) => x.openTs <= t && (!x.closeTs || x.closeTs > t));
    return { i, t, v, pnl: v - deposit, pnlPct: deposit ? v / deposit - 1 : null, dd: dds[i], open, live };
  }, [data, hover, geo, trades, deposit]);

  const big = Math.max(...data.vs.map(Math.abs), 0) >= 1000;
  const fmtY = (v: number) => amt(v, big ? 0 : 2);
  const fmtT = (t: number) => time(t, true);

  return (
    <View>
      {legend ? <Legend {...legend} /> : null}
      <View
        style={{ height }}
        onLayout={onLayout}
        onStartShouldSetResponder={() => true}
        onMoveShouldSetResponder={() => true}
        onResponderTerminationRequest={() => !onTouchActive}
        onResponderGrant={onGrant}
        onResponderMove={onMove}
        onResponderRelease={(e) => onEnd(e)}
        onResponderTerminate={() => onEnd(null)}
      >
        {geo && marks ? (
          <View pointerEvents="none" style={{ position: "absolute", left: 0, top: 0, width: w, height }}>
            <Plot data={data} geo={geo} w={w} height={height} deposit={deposit} marks={marks} />
            <Icons bubbles={marks.bubbles} />
            <Rings bubbles={marks.bubbles} w={w} height={height} />
            {hover != null && legend ? (
              <Svg width={w} height={height} style={{ position: "absolute", left: 0, top: 0 }}>
                <Line x1={hover} x2={hover} y1={PAD.t} y2={height - PAD.b} stroke="rgba(250,250,250,0.35)" strokeDasharray="3,3" />
                <Line x1={PAD.l} x2={w - PAD.r} y1={geo.y(legend.v)} y2={geo.y(legend.v)} stroke="rgba(250,250,250,0.2)" strokeDasharray="3,3" />
                <Circle cx={geo.x(legend.t)} cy={geo.y(legend.v)} r={4} fill={LIME} stroke="#000" strokeWidth={1} />
              </Svg>
            ) : null}
            <Axis geo={geo} w={w} height={height} deposit={deposit} fmtY={fmtY} fmtT={fmtT} v0={v0} v1={v1} />
          </View>
        ) : null}
        {v0 !== T0 || v1 !== T1 ? (
          <Pressable hitSlop={8} accessibilityLabel="复位" onPress={() => { tap(); setView(null); }}
            style={({ pressed }) => ({ position: "absolute", right: PAD.r + 6, top: geo ? geo.eqTop + geo.eqH - 30 : 8, height: 24, paddingHorizontal: 8, borderRadius: 12, justifyContent: "center", backgroundColor: pressed ? colors.cardMuted : "rgba(26,26,26,0.92)", borderWidth: 1, borderColor: colors.borderStrong })}>
            <Text style={{ fontSize: fs.xs, color: colors.text }}>复位</Text>
          </Pressable>
        ) : null}
      </View>
    </View>
  );
}

type Marks = { bubbles: Bubble[]; stems: Stem[]; hits: Hit[]; chain: { x: number; y: number }[]; span: { a: number; b: number; color: string } | null };

/** Path through samples i0..i1, thinned to min/max per pixel column when there are more samples than pixels. */
function linePath(xs: number[], vs: number[], i0: number, i1: number, x: (t: number) => number, y: (v: number) => number) {
  const out: string[] = [];
  let col = NaN, lo = 0, hi = 0, loY = 0, hiY = 0;
  const flush = () => { if (!isNaN(col)) { out.push(`${col.toFixed(1)},${loY.toFixed(1)}`); if (hi !== lo) out.push(`${col.toFixed(1)},${hiY.toFixed(1)}`); } };
  for (let i = i0; i <= i1; i++) {
    const px = Math.round(x(xs[i]) * 2) / 2, v = vs[i];
    if (px !== col) { flush(); col = px; lo = hi = v; loY = hiY = y(v); continue; }
    if (v < lo) { lo = v; loY = y(v); }
    if (v > hi) { hi = v; hiY = y(v); }
  }
  flush();
  return out.length ? "M" + out.join("L") : "";
}

const Plot = memo(function Plot({ data, geo, w, height, deposit, marks }: { data: Series; geo: Geo; w: number; height: number; deposit: number; marks: Marks }) {
  const eqPath = linePath(data.ts, data.vs, geo.i0, geo.i1, geo.x, geo.y);
  const ddLine = linePath(data.ts, data.dds, geo.i0, geo.i1, geo.x, geo.yd);
  const right = w - PAD.r;
  const ddArea = ddLine ? `${ddLine}L${geo.x(data.ts[geo.i1]).toFixed(1)},${geo.ddTop}L${geo.x(data.ts[geo.i0]).toFixed(1)},${geo.ddTop}Z` : "";
  const depY = geo.y(deposit);
  const depIn = deposit > 0 && depY >= geo.eqTop && depY <= geo.eqTop + geo.eqH;
  const { span, chain } = marks;
  return (
    <Svg width={w} height={height} style={{ position: "absolute", left: 0, top: 0 }}>
      {span ? <Rect x={Math.max(PAD.l, Math.min(span.a, span.b))} width={Math.max(2, Math.abs(span.b - span.a))} y={PAD.t} height={height - PAD.t - PAD.b} fill={span.color} /> : null}
      {ticks(geo.lo, geo.hi).map((v) => <Line key={v} x1={PAD.l} x2={right} y1={geo.y(v)} y2={geo.y(v)} stroke="#191919" strokeWidth={1} />)}
      <Line x1={PAD.l} x2={right} y1={geo.ddTop - 3} y2={geo.ddTop - 3} stroke="#1d1d1d" strokeWidth={1} />
      {ddArea ? <Path d={ddArea} fill={downA(0.22)} /> : null}
      {ddLine ? <Path d={ddLine} stroke={colors.down} strokeWidth={1} fill="none" /> : null}
      {depIn ? <Line x1={PAD.l} x2={right} y1={depY} y2={depY} stroke="#8a8a8a" strokeWidth={1} strokeDasharray="4,3" /> : null}
      <Path d={eqPath} stroke={LIME} strokeWidth={2} fill="none" />
      {marks.stems.map((s) => {
        const dir = s.y < s.ay ? 1 : -1;
        return (
          <G key={s.key} opacity={s.dim ? 0.28 : 1}>
            <Line x1={s.ax} y1={s.ay} x2={s.x} y2={s.y + dir * s.r} stroke={s.color} strokeWidth={1} />
            <Circle cx={s.ax} cy={s.ay} r={2.5} fill={s.color} />
          </G>
        );
      })}
      {chain.length > 1 ? <Path d={"M" + chain.map((q) => `${q.x.toFixed(1)},${q.y.toFixed(1)}`).join("L")} stroke="rgba(188,255,47,0.85)" strokeWidth={1.5} strokeDasharray="4,3" fill="none" /> : null}
      {marks.bubbles.map((b) => {
        const inner = b.r - b.ringW, a = Math.max(3, inner * 0.55), up = b.arrow === "up";
        return (
          <G key={b.key} opacity={b.dim ? 0.28 : 1}>
            {b.glow ? <Circle cx={b.x} cy={b.y} r={b.r + 6} fill="rgba(188,255,47,0.22)" /> : null}
            <Circle cx={b.x} cy={b.y} r={b.r} fill={b.fill ?? "#141414"} />
            {!b.icon && b.arrow ? (
              <Path d={`M${b.x},${b.y + (up ? -a : a)}L${b.x - a},${b.y + (up ? a * 0.7 : -a * 0.7)}L${b.x + a},${b.y + (up ? a * 0.7 : -a * 0.7)}Z`} fill={b.ring} />
            ) : null}
          </G>
        );
      })}
    </Svg>
  );
});

const Icons = memo(function Icons({ bubbles }: { bubbles: Bubble[] }) {
  return (
    <>
      {bubbles.filter((b) => b.icon).map((b) => {
        const inner = Math.max(2, b.r - b.ringW);
        return (
          <View key={b.key} style={{ position: "absolute", left: b.x - inner, top: b.y - inner, width: inner * 2, height: inner * 2, opacity: b.dim ? 0.28 : 1 }}>
            <CoinIcon instId={b.instId} size={inner * 2} />
          </View>
        );
      })}
    </>
  );
});

const Rings = memo(function Rings({ bubbles, w, height }: { bubbles: Bubble[]; w: number; height: number }) {
  const corner = (key: string, x: number, y: number, r: number, dx: number, glyph: string, color: string, bg: string) => {
    const br = Math.max(r < 7 ? 3.2 : 4.2, r * 0.4), bx = x + dx * r * 0.72, by = y + r * 0.72;
    return (
      <G key={key}>
        <Circle cx={bx} cy={by} r={br} fill={bg} stroke="#0e0e0e" strokeWidth={1} />
        <SvgText x={bx} y={by + br * 0.42} fontSize={br * 1.2} fontWeight="700" fill={color} textAnchor="middle">{glyph}</SvgText>
      </G>
    );
  };
  return (
    <Svg width={w} height={height} style={{ position: "absolute", left: 0, top: 0 }}>
      {bubbles.map((b) => {
        let pill = null;
        if (b.count) {
          const small = b.r < 10, f = small ? 8 : 9.5, bh = small ? 11 : 13, label = String(b.count);
          const bw = Math.max(bh, 5 + label.length * f * 0.62), px = b.x + b.r * 0.35, py = b.y - b.r - 4;
          pill = (
            <G>
              <Rect x={px} y={py} width={bw} height={bh} rx={bh / 2} fill={LIME} stroke="#0e0e0e" strokeWidth={1.5} />
              <SvgText x={px + bw / 2} y={py + bh / 2 + f * 0.36} fontSize={f} fontWeight="700" fill="#000" textAnchor="middle">{label}</SvgText>
            </G>
          );
        }
        return (
          <G key={b.key} opacity={b.dim ? 0.28 : 1}>
            <Circle cx={b.x} cy={b.y} r={b.r - b.ringW / 2} fill="none" stroke={b.ring} strokeWidth={b.ringW} strokeDasharray={b.dashed ? "2.5,2" : undefined} />
            {b.badge ? corner("b", b.x, b.y, b.r, 1, b.badge.g, b.badge.c, b.badge.bg) : null}
            {b.arrow && b.icon ? corner("a", b.x, b.y, b.r, -1, b.arrow === "up" ? "▲" : "▼", "#fff", b.arrow === "up" ? colors.up : colors.down) : null}
            {pill}
          </G>
        );
      })}
    </Svg>
  );
});

function Axis({ geo, w, height, deposit, fmtY, fmtT, v0, v1 }: { geo: Geo; w: number; height: number; deposit: number; fmtY: (v: number) => string; fmtT: (t: number) => string; v0: number; v1: number }) {
  const lab = { position: "absolute" as const, right: 0, width: PAD.r - 4, fontSize: 10, color: "#8a8a8a", textAlign: "right" as const };
  const depY = geo.y(deposit);
  return (
    <>
      {ticks(geo.lo, geo.hi).map((v) => <Text key={v} style={[lab, { top: geo.y(v) - 7 }, tnum]}>{fmtY(v)}</Text>)}
      {deposit > 0 && depY >= geo.eqTop && depY <= geo.eqTop + geo.eqH ? (
        <Text style={[lab, { top: depY - 7, color: colors.text, backgroundColor: "#262626", borderRadius: 3, overflow: "hidden" }, tnum]}>入金</Text>
      ) : null}
      <Text style={[lab, { top: geo.ddTop - 2 }, tnum]}>0%</Text>
      <Text style={[lab, { top: geo.ddTop + geo.ddH - 12, color: colors.down }, tnum]}>{pctf(geo.ddMin, 1)}</Text>
      <Text style={[{ position: "absolute", left: PAD.l, top: height - 14, fontSize: 10, color: "#8a8a8a" }, tnum]}>{fmtT(v0)}</Text>
      <Text style={[{ position: "absolute", right: PAD.r, top: height - 14, fontSize: 10, color: "#8a8a8a" }, tnum]}>{fmtT(v1)}</Text>
      <View style={{ position: "absolute", left: w - PAD.r, top: 0, bottom: PAD.b, width: 1, backgroundColor: "#2e2e2e" }} />
    </>
  );
}

/** Legend above the plot (web .lg, phone layout): time, equity, PnL vs deposit, drawdown, open positions. */
function Legend({ t, v, pnl, pnlPct, dd, open, live }: { t: number; v: number; pnl: number; pnlPct: number | null; dd: number; open: Trade[]; live: boolean }) {
  const it = (k: string, val: string, color = colors.text, sub?: string) => (
    <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>
      {k} <Text style={{ fontSize: fs.sm, fontWeight: "600", color }}>{val}{sub ? <Text style={{ fontSize: fs.xs, fontWeight: "500" }}> {sub}</Text> : null}</Text>
    </Text>
  );
  const shown = open.slice(0, 6);
  return (
    <View style={{ marginBottom: 4, paddingHorizontal: 2, gap: 2 }}>
      <View style={{ flexDirection: "row", flexWrap: "wrap", alignItems: "baseline", columnGap: 10, rowGap: 2 }}>
        <Text style={[{ fontSize: fs.xs, color: live ? colors.textFaint : colors.textSoft }, tnum]}>{time(t, true)}</Text>
        {it("权益", amt(v))}
        {it("盈亏", amt(pnl, 2, true), pnl > 0 ? colors.up : pnl < 0 ? colors.down : colors.text, pnlPct != null ? pctf(pnlPct, 2, true) : undefined)}
        {it("回撤", pctf(dd, 2), dd < 0 ? colors.down : colors.text)}
        {it("持仓", String(open.length))}
      </View>
      {/* fixed height so the plot doesn't jump while dragging */}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6, height: 16, overflow: "hidden" }}>
          {shown.map((x) => (
            <View key={x.id} style={{ flexDirection: "row", alignItems: "center", gap: 3 }}>
              <CoinIcon instId={x.instId} size={12} />
              <Text style={{ fontSize: fs.xs, color: colors.textSoft }}>{sym(x.instId)}</Text>
              <Text style={{ fontSize: fs.xs, fontWeight: "600", color: x.posSide === "long" ? colors.up : colors.down }}>{x.posSide === "long" ? "多" : "空"}</Text>
            </View>
          ))}
          {open.length > shown.length ? <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>+{open.length - shown.length}</Text> : null}
          {!open.length ? <Text style={{ fontSize: fs.xs, color: colors.textDim }}>无持仓</Text> : null}
      </View>
    </View>
  );
}

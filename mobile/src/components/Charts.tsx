// Small native charts (react-native-svg) for the secondary panels: multi-line and signed bars, with a
// touch readout. The K-line chart itself is the web chart in a WebView (ChartView.tsx); these are
// for compact history panels (relative performance, OI, funding, liquidations, equity later).
import { useMemo, useState } from "react";
import { Text, View, type LayoutChangeEvent } from "react-native";
import Svg, { Line, Path, Rect } from "react-native-svg";
import { colors, fs, tnum } from "@/lib/theme";

export interface LineSeriesData { label: string; color: string; points: { t: number; v: number }[]; width?: number }

const PAD = { l: 4, r: 52, t: 8, b: 18 };

function useWidth() {
  const [w, setW] = useState(0);
  return { w, onLayout: (e: LayoutChangeEvent) => setW(Math.round(e.nativeEvent.layout.width)) };
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

/** Lines sharing one y axis; `fmtY` labels the axis and the readout. Touch and drag to read values. */
export function LineChart({ series, height = 180, fmtY = (v) => v.toFixed(2), fmtT }: { series: LineSeriesData[]; height?: number; fmtY?: (v: number) => string; fmtT: (t: number) => string }) {
  const { w, onLayout } = useWidth();
  const [hover, setHover] = useState<number | null>(null);
  const geo = useMemo(() => {
    const all = series.flatMap((s) => s.points);
    if (!all.length || !w) return null;
    const t0 = Math.min(...all.map((p) => p.t)), t1 = Math.max(...all.map((p) => p.t));
    let y0 = Math.min(...all.map((p) => p.v)), y1 = Math.max(...all.map((p) => p.v));
    if (y0 === y1) { y0 -= 1; y1 += 1; }
    const pad = (y1 - y0) * 0.06;
    y0 -= pad; y1 += pad;
    const iw = w - PAD.l - PAD.r, ih = height - PAD.t - PAD.b;
    const x = (t: number) => PAD.l + (t1 > t0 ? ((t - t0) / (t1 - t0)) * iw : iw);
    const y = (v: number) => PAD.t + (1 - (v - y0) / (y1 - y0)) * ih;
    return { t0, t1, y0, y1, x, y, iw };
  }, [series, w, height]);

  const at = hover != null && geo ? geo.t0 + ((hover - PAD.l) / geo.iw) * (geo.t1 - geo.t0) : null;
  const readout = at != null ? series.map((s) => ({ s, p: nearest(s.points, at) })) : series.map((s) => ({ s, p: s.points[s.points.length - 1] }));
  const move = (x: number) => setHover(Math.max(PAD.l, Math.min(w - PAD.r, x)));

  return (
    <View>
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 10, marginBottom: 6 }}>
        {at != null ? <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{fmtT(at)}</Text> : null}
        {readout.map(({ s, p }) => (
          <Text key={s.label} style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>
            <Text style={{ color: s.color }}>● </Text>{s.label} <Text style={{ color: colors.text }}>{p ? fmtY(p.v) : "—"}</Text>
          </Text>
        ))}
      </View>
      <View
        style={{ height }}
        onLayout={onLayout}
        onStartShouldSetResponder={() => true}
        onMoveShouldSetResponder={() => true}
        onResponderTerminationRequest={() => true}
        onResponderGrant={(e) => move(e.nativeEvent.locationX)}
        onResponderMove={(e) => move(e.nativeEvent.locationX)}
        onResponderRelease={() => setHover(null)}
        onResponderTerminate={() => setHover(null)}
      >
        {geo ? (
          <Svg width={w} height={height}>
            {ticks(geo.y0, geo.y1).map((v) => (
              <Line key={v} x1={PAD.l} x2={w - PAD.r} y1={geo.y(v)} y2={geo.y(v)} stroke="#191919" strokeWidth={1} />
            ))}
            {series.map((s) => (
              <Path key={s.label} d={s.points.map((p, i) => `${i ? "L" : "M"}${geo.x(p.t).toFixed(1)},${geo.y(p.v).toFixed(1)}`).join("")} stroke={s.color} strokeWidth={s.width ?? 1.5} fill="none" />
            ))}
            {hover != null ? <Line x1={hover} x2={hover} y1={PAD.t} y2={height - PAD.b} stroke="#5b5b5b" strokeDasharray="3,3" /> : null}
          </Svg>
        ) : null}
        {geo ? <Axis geo={geo} w={w} height={height} fmtY={fmtY} fmtT={fmtT} /> : null}
      </View>
    </View>
  );
}

function Axis({ geo, w, height, fmtY, fmtT }: { geo: { t0: number; t1: number; y0: number; y1: number; y: (v: number) => number }; w: number; height: number; fmtY: (v: number) => string; fmtT: (t: number) => string }) {
  return (
    <>
      {ticks(geo.y0, geo.y1).map((v) => (
        <Text key={v} style={[{ position: "absolute", right: 0, width: PAD.r - 4, top: geo.y(v) - 7, fontSize: 10, color: "#8a8a8a", textAlign: "right" }, tnum]}>{fmtY(v)}</Text>
      ))}
      <Text style={[{ position: "absolute", left: PAD.l, top: height - 14, fontSize: 10, color: "#8a8a8a" }, tnum]}>{fmtT(geo.t0)}</Text>
      <Text style={[{ position: "absolute", right: PAD.r, top: height - 14, fontSize: 10, color: "#8a8a8a" }, tnum]}>{fmtT(geo.t1)}</Text>
      <View style={{ position: "absolute", left: w - PAD.r, top: 0, bottom: PAD.b, width: 1, backgroundColor: "#2e2e2e" }} />
    </>
  );
}

function nearest<T extends { t: number }>(xs: T[], t: number): T | undefined {
  let best: T | undefined, d = Infinity;
  for (const p of xs) {
    const e = Math.abs(p.t - t);
    if (e < d) { d = e; best = p; }
  }
  return best;
}

/** Signed bars around zero (funding per settlement, liquidations up/down). */
export function BarChart({ bars, height = 150, fmtY, fmtT, upColor = colors.up, downColor = colors.down }: {
  bars: { t: number; v: number; color?: string }[]; height?: number; fmtY: (v: number) => string; fmtT: (t: number) => string; upColor?: string; downColor?: string;
}) {
  const { w, onLayout } = useWidth();
  const [hover, setHover] = useState<number | null>(null);
  const max = Math.max(1e-12, ...bars.map((b) => Math.abs(b.v)));
  const hasNeg = bars.some((b) => b.v < 0), hasPos = bars.some((b) => b.v > 0);
  const lo = hasNeg ? -max : 0, hi = hasPos || !hasNeg ? max : 0;
  const iw = Math.max(1, w - PAD.l - PAD.r), ih = height - PAD.t - PAD.b;
  const y = (v: number) => PAD.t + (1 - (v - lo) / (hi - lo || 1)) * ih;
  const bw = bars.length ? iw / bars.length : iw;
  const idx = hover != null ? Math.max(0, Math.min(bars.length - 1, Math.floor((hover - PAD.l) / bw))) : null;
  const sel = idx != null ? bars[idx] : bars[bars.length - 1];
  const move = (x: number) => setHover(x);
  return (
    <View>
      <Text style={[{ fontSize: fs.xs, color: colors.textFaint, marginBottom: 6 }, tnum]}>
        {sel ? `${fmtT(sel.t)}  ` : ""}<Text style={{ color: sel ? (sel.v >= 0 ? upColor : downColor) : colors.text }}>{sel ? fmtY(sel.v) : "—"}</Text>
      </Text>
      <View style={{ height }} onLayout={onLayout} onStartShouldSetResponder={() => true} onMoveShouldSetResponder={() => true} onResponderTerminationRequest={() => true}
        onResponderGrant={(e) => move(e.nativeEvent.locationX)} onResponderMove={(e) => move(e.nativeEvent.locationX)} onResponderRelease={() => setHover(null)} onResponderTerminate={() => setHover(null)}>
        {w ? (
          <Svg width={w} height={height}>
            <Line x1={PAD.l} x2={w - PAD.r} y1={y(0)} y2={y(0)} stroke="#2e2e2e" strokeWidth={1} />
            {bars.map((b, i) => {
              const y0 = y(0), y1 = y(b.v);
              return <Rect key={b.t} x={PAD.l + i * bw + bw * 0.15} width={Math.max(1, bw * 0.7)} y={Math.min(y0, y1)} height={Math.max(1, Math.abs(y1 - y0))} fill={b.color ?? (b.v >= 0 ? upColor : downColor)} opacity={idx === i ? 1 : 0.85} />;
            })}
          </Svg>
        ) : null}
        {w ? (
          <>
            <Text style={[{ position: "absolute", right: 0, width: PAD.r - 4, top: y(hi) - 2, fontSize: 10, color: "#8a8a8a", textAlign: "right" }, tnum]}>{fmtY(hi)}</Text>
            {hasNeg ? <Text style={[{ position: "absolute", right: 0, width: PAD.r - 4, top: y(lo) - 12, fontSize: 10, color: "#8a8a8a", textAlign: "right" }, tnum]}>{fmtY(lo)}</Text> : null}
            {bars.length ? <Text style={[{ position: "absolute", left: PAD.l, top: height - 14, fontSize: 10, color: "#8a8a8a" }, tnum]}>{fmtT(bars[0].t)}</Text> : null}
            {bars.length ? <Text style={[{ position: "absolute", right: PAD.r, top: height - 14, fontSize: 10, color: "#8a8a8a" }, tnum]}>{fmtT(bars[bars.length - 1].t)}</Text> : null}
          </>
        ) : null}
      </View>
    </View>
  );
}

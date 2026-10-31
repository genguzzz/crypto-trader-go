// 试运行 K 线: the trial's candles (one instrument, from the trial result) with its trades marked —
// entry ▲ long / ▼ short, exit ●, a dashed line between them coloured by net result. Native SVG
// because a trial is not stored: /embed/trades/:accountId (TradeCandleView) only reads saved
// accounts. Candles are merged so one is at least 4 px wide. `selectedId` highlights one trade and
// draws its stop / target.
import { useMemo, useState } from "react";
import { Text, View, type LayoutChangeEvent } from "react-native";
import Svg, { Circle, G, Line, Path, Rect, Text as SvgText } from "react-native-svg";
import type { AccountCandles, Trade } from "@/lib/tradeApi";
import { price, time } from "@/lib/fmt";
import { colors, fs } from "@/lib/theme";

const PAD = { l: 4, r: 56, t: 8, b: 18 };

export function TrialChart({ candles, trades, selectedId, height = 240 }: { candles: AccountCandles; trades: Trade[]; selectedId?: number; height?: number }) {
  const [w, setW] = useState(0);
  const onLayout = (e: LayoutChangeEvent) => setW(Math.round(e.nativeEvent.layout.width));
  const g = useMemo(() => {
    const rows = candles.rows;
    if (!w || rows.length < 2) return null;
    const iw = w - PAD.l - PAD.r, ih = height - PAD.t - PAD.b;
    const per = Math.max(1, Math.ceil(rows.length / Math.max(10, Math.floor(iw / 4))));
    const bars: { t: number; o: number; h: number; l: number; c: number }[] = [];
    for (let i = 0; i < rows.length; i += per) {
      const chunk = rows.slice(i, i + per);
      bars.push({ t: chunk[0][0], o: chunk[0][1], h: Math.max(...chunk.map((r) => r[2])), l: Math.min(...chunk.map((r) => r[3])), c: chunk[chunk.length - 1][4] });
    }
    const step = rows[1][0] - rows[0][0];
    const t0 = rows[0][0], t1 = rows[rows.length - 1][0] + step;
    const pxs = [...bars.flatMap((b) => [b.h, b.l]), ...trades.flatMap((t) => [t.entryPx, t.exitPx ?? t.entryPx])].filter((v) => v > 0);
    let y0 = Math.min(...pxs), y1 = Math.max(...pxs);
    if (y0 === y1) { y0 *= 0.99; y1 *= 1.01; }
    const pad = (y1 - y0) * 0.05;
    y0 -= pad; y1 += pad;
    const x = (t: number) => PAD.l + ((t - t0) / (t1 - t0)) * iw;
    const y = (v: number) => PAD.t + (1 - (v - y0) / (y1 - y0)) * ih;
    const bw = (iw / bars.length) * 0.7;
    const ticks = [0, 1, 2, 3].map((i) => y0 + ((y1 - y0) * (i + 0.5)) / 4);
    return { bars, x, y, bw, ticks, t0, t1, iw, ih, slot: iw / bars.length };
  }, [candles, trades, w, height]);

  const sel = trades.find((t) => t.id === selectedId);
  return (
    <View onLayout={onLayout} style={{ height, borderRadius: 8, backgroundColor: colors.card, borderWidth: 1, borderColor: colors.border }}>
      {g ? (
        <Svg width={w} height={height}>
          {g.ticks.map((v) => (
            <G key={v}>
              <Line x1={PAD.l} x2={PAD.l + g.iw} y1={g.y(v)} y2={g.y(v)} stroke={colors.border} strokeWidth={1} />
              <SvgText x={w - PAD.r + 4} y={g.y(v) + 3} fontSize={9} fill={colors.textFaint}>{price(v)}</SvgText>
            </G>
          ))}
          {g.bars.map((b, i) => {
            const cx = PAD.l + g.slot * (i + 0.5);
            const up = b.c >= b.o;
            const c = up ? colors.up : colors.down;
            const top = g.y(Math.max(b.o, b.c)), bot = g.y(Math.min(b.o, b.c));
            return (
              <G key={b.t}>
                <Line x1={cx} x2={cx} y1={g.y(b.h)} y2={g.y(b.l)} stroke={c} strokeWidth={1} />
                <Rect x={cx - g.bw / 2} y={top} width={Math.max(1, g.bw)} height={Math.max(1, bot - top)} fill={c} />
              </G>
            );
          })}
          {sel ? [{ p: sel.stopLoss, c: colors.down }, { p: sel.takeProfit, c: colors.up }].filter((l) => l.p).map((l) => (
            <Line key={l.c} x1={PAD.l} x2={PAD.l + g.iw} y1={g.y(l.p!)} y2={g.y(l.p!)} stroke={l.c} strokeWidth={1} strokeDasharray="2 3" />
          )) : null}
          {trades.map((t) => {
            const dim = sel && sel.id !== t.id ? 0.35 : 1;
            const ex = g.x(t.openTs), ey = g.y(t.entryPx);
            const long = t.posSide === "long";
            const c = long ? colors.up : colors.down;
            const tri = long ? `M${ex} ${ey + 3} l-5 8 h10 Z` : `M${ex} ${ey - 3} l-5 -8 h10 Z`;
            const closed = t.closeTs && t.exitPx;
            const xx = closed ? g.x(t.closeTs!) : 0, xy = closed ? g.y(t.exitPx!) : 0;
            return (
              <G key={t.id} opacity={dim}>
                {closed ? <Line x1={ex} y1={ey} x2={xx} y2={xy} stroke={t.net >= 0 ? colors.up : colors.down} strokeWidth={sel?.id === t.id ? 2 : 1} strokeDasharray="3 3" /> : null}
                <Path d={tri} fill={c} stroke={colors.bg} strokeWidth={0.5} />
                {closed ? <Circle cx={xx} cy={xy} r={sel?.id === t.id ? 4 : 3} fill={colors.primary} stroke={colors.bg} strokeWidth={0.5} /> : null}
              </G>
            );
          })}
          <SvgText x={PAD.l} y={height - 4} fontSize={9} fill={colors.textFaint}>{time(g.t0, true)}</SvgText>
          <SvgText x={PAD.l + g.iw} y={height - 4} fontSize={9} fill={colors.textFaint} textAnchor="end">{time(g.t1, true)}</SvgText>
        </Svg>
      ) : w ? (
        <Text style={{ padding: 16, fontSize: fs.sm, color: colors.textFaint }}>K 线数据不足</Text>
      ) : null}
    </View>
  );
}

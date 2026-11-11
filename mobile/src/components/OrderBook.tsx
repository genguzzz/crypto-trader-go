// Live order book (web market/OrderBookView.vue): REST snapshot every second while shown — summary,
// ±1% imbalance, the USDT depth chart (web charts/DepthChart.vue), the capital needed to push price
// ±0.1…5%, and a top-of-book ladder.
import { useMemo, useState } from "react";
import { Text, View, type LayoutChangeEvent } from "react-native";
import Svg, { Circle, Line, Path, Text as SvgText } from "react-native-svg";
import { api, type BookLevel } from "@/lib/api";
import { ago, price, usd } from "@/lib/fmt";
import { useQuery } from "@/lib/query";
import { colors, fs, tnum } from "@/lib/theme";
import { B, InfoTip, P } from "./Form";
import { Loading } from "./ui";

/** Cumulative depth point (web market/depth.ts): offset from mid in %, cumulative USDT and base units. */
interface DepthPoint { off: number; px: number; cum: number; base: number }

function cumulative(levels: BookLevel[], mid: number, ctVal: number): DepthPoint[] {
  let cum = 0, base = 0;
  return levels.map(([px, usdt, sz]) => {
    cum += usdt;
    base += sz * ctVal;
    return { off: (px / mid - 1) * 100, px, cum, base };
  });
}

interface Sweep { side: "buy" | "sell"; targetPct: number; targetPx: number; usdt: number; avgPx: number; slipPct: number; levels: number; beyond: boolean }

/** Market order needed to move the price from mid to mid×(1+pct/100): opposite-side levels up to there. */
function sweepTo(pct: number, bids: DepthPoint[], asks: DepthPoint[], mid: number): Sweep {
  const buy = pct >= 0;
  const side = buy ? asks : bids;
  let last: DepthPoint | null = null, n = 0;
  for (const p of side) {
    if (buy ? p.off <= pct : p.off >= pct) { last = p; n++; }
    else break;
  }
  const usdt = last?.cum ?? 0, base = last?.base ?? 0;
  const avgPx = base > 0 ? usdt / base : mid;
  const edge = side[side.length - 1];
  return {
    side: buy ? "buy" : "sell", targetPct: pct, targetPx: mid * (1 + pct / 100), usdt, avgPx,
    slipPct: (avgPx / mid - 1) * 100, levels: n, beyond: !!edge && (buy ? pct > edge.off : pct < edge.off),
  };
}

// One view instead of range buttons: as deep as the fetched book reaches, capped at ±5% (OKX books-full
// gives ≤5000 levels per side; BTC covers ~±1%, thin alts reach 5%). Rounded down for readable ticks.
const MAX_RANGE = 5;
function depthRange(cover: number) {
  const r = Math.min(MAX_RANGE, cover);
  const step = r >= 2 ? 0.5 : r >= 0.5 ? 0.1 : 0.01;
  return Math.max(step, Math.floor(r / step + 1e-9) * step);
}

/** `ladder` = levels per side; 6 is the compact panel on the page (smaller depth chart), 10 the sheet. */
export function OrderBook({ id, ladder = 6 }: { id: string; ladder?: number }) {
  const { data: book, error } = useQuery(`book:${id}`, () => api.orderbook(id), { interval: 1000, staleMs: 800 });
  const d = useMemo(() => {
    if (!book) return null;
    const bids = cumulative(book.bids, book.mid, book.ctVal);
    const asks = cumulative(book.asks, book.mid, book.ctVal);
    const table = [0.1, 0.5, 1, 2, 5].map((p) => {
      const up = sweepTo(p, bids, asks, book.mid), dn = sweepTo(-p, bids, asks, book.mid);
      return { p, up, dn, ratio: up.usdt + dn.usdt > 0 ? dn.usdt / (up.usdt + dn.usdt) : 0.5 };
    });
    const la = book.asks.slice(0, ladder).map((l, i) => ({ l, cum: asks[i]?.cum ?? 0 })).reverse();
    const lb = book.bids.slice(0, ladder).map((l, i) => ({ l, cum: bids[i]?.cum ?? 0 }));
    const max = Math.max(la[0]?.cum ?? 0, lb[lb.length - 1]?.cum ?? 0, 1);
    const range = depthRange(book.levels ? Math.min(book.coverBid, book.coverAsk) : MAX_RANGE);
    return { bids, asks, range, table, la, lb, max };
  }, [book, ladder]);

  if (!book || !d) return error ? <Text style={{ color: colors.textFaint, fontSize: fs.sm }}>盘口获取失败：{error}</Text> : <Loading text="加载盘口" />;
  const one = d.table.find((r) => r.p === 1);
  return (
    <View style={{ gap: 12 }}>
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 16, alignItems: "flex-end" }}>
        <Kv k="买一" v={price(book.bestBid)} color={colors.up} />
        <Kv k="卖一" v={price(book.bestAsk)} color={colors.down} />
        <Kv k="中间价" v={price(book.mid)} />
        <Kv k="点差" v={`${book.spreadBps.toFixed(1)} bps`} />
      </View>
      {one ? (
        <View>
          <View style={{ flexDirection: "row" }}>
            <Text style={[{ fontSize: fs.xs, color: colors.textFaint, flex: 1 }, tnum]}>±1% 深度 买 {usd(one.dn.usdt)} / 卖 {usd(one.up.usdt)}</Text>
            <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{ago(book.ts)} 前 · 每秒刷新</Text>
          </View>
          <View style={{ height: 4, marginTop: 6, backgroundColor: colors.down, borderRadius: 2, overflow: "hidden" }}>
            <View style={{ height: 4, width: `${one.ratio * 100}%`, backgroundColor: colors.up }} />
          </View>
        </View>
      ) : null}

      <View>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 4, marginBottom: 6 }}>
          <Text style={{ fontSize: fs.md, fontWeight: "600", color: colors.text }}>深度图 <Text style={{ fontSize: fs.xs, fontWeight: "400", color: colors.textFaint }}>±{+d.range.toFixed(2)}% · USDT 累计</Text></Text>
          <InfoTip title="深度图">
            <P>横轴为距中间价的百分比，纵轴为累计挂单 USDT。范围自动取 <B>±5%</B> 与盘口数据覆盖范围中较小者{book.levels ? `（当前覆盖 −${book.coverBid.toFixed(2)}% / +${book.coverAsk.toFixed(2)}%，${book.levels} 档）` : ""}。按住或点按图表查看把价格推到该处需要吃掉多少资金。</P>
          </InfoTip>
        </View>
        <DepthChart bids={d.bids} asks={d.asks} mid={book.mid} range={d.range} height={ladder < 10 ? 200 : 260} />
      </View>

      <View>
        <View style={{ flexDirection: "row", paddingBottom: 4, borderBottomWidth: 1, borderColor: colors.border }}>
          <Th flex={0.8}>价格偏离</Th><Th right>买入推高需</Th><Th right>卖出压低需</Th><Th right flex={1.3}>均价滑点 买/卖</Th>
        </View>
        {d.table.map((r) => (
          <View key={r.p} style={{ flexDirection: "row", height: 28, alignItems: "center", borderBottomWidth: 1, borderColor: colors.border }}>
            <Td flex={0.8}>±{r.p}%</Td>
            <Td right color={colors.up}>{usd(r.up.usdt)}{r.up.beyond ? <Text style={{ color: colors.warn }}>+</Text> : null}</Td>
            <Td right color={colors.down}>{usd(r.dn.usdt)}{r.dn.beyond ? <Text style={{ color: colors.warn }}>+</Text> : null}</Td>
            <Td right flex={1.3} color={colors.textSoft}>{r.up.slipPct.toFixed(2)}% / {r.dn.slipPct.toFixed(2)}%</Td>
          </View>
        ))}
        <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 4 }}>“+” 表示超出 OKX 公开盘口深度（每侧最多 5000 档），实际需要更多。</Text>
      </View>

      <View>
        <View style={{ flexDirection: "row", paddingHorizontal: 6, paddingBottom: 4 }}>
          <Th>价格</Th><Th right>USDT</Th><Th right>累计</Th>
        </View>
        {d.la.map((x, i) => <Ladder key={"a" + i} px={x.l[0]} usdt={x.l[1]} cum={x.cum} max={d.max} ask />)}
        <View style={{ paddingVertical: 4, borderTopWidth: 1, borderBottomWidth: 1, borderColor: colors.border, marginVertical: 2, alignItems: "center" }}>
          <Text style={[{ fontWeight: "700", fontSize: fs.lg, color: colors.text }, tnum]}>{price(book.mid)} <Text style={{ fontSize: fs.xs, color: colors.textFaint, fontWeight: "400" }}>{book.spreadBps.toFixed(1)} bps</Text></Text>
        </View>
        {d.lb.map((x, i) => <Ladder key={"b" + i} px={x.l[0]} usdt={x.l[1]} cum={x.cum} max={d.max} />)}
      </View>
    </View>
  );
}

const DPAD = { l: 8, r: 58, t: 10, b: 22 };

/**
 * Cumulative depth in USDT around mid (x = % offset, y = cumulative USDT): bids left in green, asks
 * right in red. Touch or drag to read what it takes to push the price there; like the web on touch,
 * the reading stays after the finger lifts.
 */
function DepthChart({ bids, asks, mid, range, height }: { bids: DepthPoint[]; asks: DepthPoint[]; mid: number; range: number; height: number }) {
  const [w, setW] = useState(0);
  const [probeOff, setProbeOff] = useState<number | null>(null);
  const iw = Math.max(1, w - DPAD.l - DPAD.r);
  const inRange = (ps: DepthPoint[]) => ps.filter((p) => Math.abs(p.off) <= range);
  const edge = (ps: DepthPoint[]) => { const r = inRange(ps); return r.length ? r[r.length - 1].cum : 0; };
  const maxY = Math.max(edge(bids), edge(asks), 1) * 1.08;
  const x = (off: number) => DPAD.l + ((off + range) / (2 * range)) * iw;
  const y = (v: number) => height - DPAD.b - (v / maxY) * (height - DPAD.t - DPAD.b);
  // step path from mid outwards: cumulative stays flat until the next level's offset
  const step = (ps: DepthPoint[], dir: 1 | -1) => {
    let d = `M${x(0)},${y(0)}`;
    for (const p of inRange(ps)) d += `H${x(p.off).toFixed(1)}V${y(p.cum).toFixed(1)}`;
    d += `H${x(dir * range)}`;
    return { line: d, area: `${d}V${y(0)}Z` };
  };
  const bp = step(bids, -1), ap = step(asks, 1);
  const xTicks = [-1, -0.5, 0, 0.5, 1].map((k) => k * range);
  const yTicks = [0.25, 0.5, 0.75, 1].map((k) => (k * maxY) / 1.08);

  const move = (at: number) => {
    if (at < DPAD.l || at > w - DPAD.r) return;
    setProbeOff(Math.round((((at - DPAD.l) / iw) * 2 * range - range) * 100) / 100); // snap to 0.01%
  };
  // the book refreshes every second: the held reading is recomputed against the new levels
  const off = probeOff != null ? Math.max(-range, Math.min(range, probeOff)) : null;
  const sw = off != null ? sweepTo(off, bids, asks, mid) : null;
  const px = off != null ? x(off) : 0;
  const tipLeft = Math.min(Math.max(px + 12, 4), w - 214);

  return (
    <View style={{ height }} onLayout={(e: LayoutChangeEvent) => setW(Math.round(e.nativeEvent.layout.width))}
      onStartShouldSetResponder={() => true} onMoveShouldSetResponder={() => true} onResponderTerminationRequest={() => true}
      onResponderGrant={(e) => move(e.nativeEvent.locationX)} onResponderMove={(e) => move(e.nativeEvent.locationX)}>
      {w ? (
        <Svg width={w} height={height}>
          {yTicks.map((t) => <Line key={"g" + t} x1={DPAD.l} x2={w - DPAD.r} y1={y(t)} y2={y(t)} stroke={colors.border} strokeWidth={1} />)}
          <Path d={bp.area} fill={colors.upBg} />
          <Path d={bp.line} stroke={colors.up} strokeWidth={1.5} fill="none" />
          <Path d={ap.area} fill={colors.downBg} />
          <Path d={ap.line} stroke={colors.down} strokeWidth={1.5} fill="none" />
          <Line x1={x(0)} x2={x(0)} y1={DPAD.t} y2={height - DPAD.b} stroke={colors.textFaint} strokeDasharray="3,3" />
          {xTicks.map((t) => (
            <SvgText key={"x" + t} x={x(t)} y={height - 6} fontSize={10} fill={colors.textFaint} textAnchor={t === -range ? "start" : t === range ? "end" : "middle"}>{`${t > 0 ? "+" : ""}${+t.toFixed(2)}%`}</SvgText>
          ))}
          {yTicks.map((t) => <SvgText key={"y" + t} x={w - DPAD.r + 6} y={y(t) + 4} fontSize={10} fill={colors.textFaint}>{usd(t)}</SvgText>)}
          {sw ? (
            <>
              <Line x1={px} x2={px} y1={DPAD.t} y2={height - DPAD.b} stroke={colors.textSoft} strokeDasharray="2,3" />
              <Line x1={DPAD.l} x2={w - DPAD.r} y1={y(sw.usdt)} y2={y(sw.usdt)} stroke={colors.textSoft} strokeDasharray="2,3" />
              <Circle cx={px} cy={y(sw.usdt)} r={4} fill={sw.side === "buy" ? colors.down : colors.up} stroke={colors.bg} strokeWidth={2} />
            </>
          ) : null}
        </Svg>
      ) : null}
      {sw ? (
        <View pointerEvents="none" style={{ position: "absolute", top: 8, left: tipLeft, width: 206, backgroundColor: colors.raised, borderWidth: 1, borderColor: colors.borderStrong, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 8 }}>
          <Text style={[{ fontSize: fs.sm, fontWeight: "600", marginBottom: 4, color: sw.side === "buy" ? colors.up : colors.down }, tnum]}>
            {sw.side === "buy" ? "买入推高到" : "卖出压低到"} {sw.targetPct > 0 ? "+" : ""}{sw.targetPct.toFixed(2)}%
          </Text>
          <TipRow k="目标价" v={price(sw.targetPx)} />
          <TipRow k="需吃掉" v={`${usd(sw.usdt)} USDT`} />
          <TipRow k="成交均价" v={price(sw.avgPx)} />
          <TipRow k="均价滑点" v={`${sw.slipPct > 0 ? "+" : ""}${sw.slipPct.toFixed(3)}%`} />
          <TipRow k="档数" v={String(sw.levels)} />
          {sw.beyond ? <Text style={{ fontSize: fs.xs, color: colors.warn, marginTop: 4 }}>超出 OKX 公开盘口深度，实际需要更多</Text> : null}
        </View>
      ) : null}
    </View>
  );
}

function TipRow({ k, v }: { k: string; v: string }) {
  return (
    <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
      <Text style={{ fontSize: fs.xs, lineHeight: 19, color: colors.textFaint }}>{k}</Text>
      <Text style={[{ fontSize: fs.xs, lineHeight: 19, fontWeight: "600", color: colors.text }, tnum]}>{v}</Text>
    </View>
  );
}

function Kv({ k, v, color }: { k: string; v: string; color?: string }) {
  return (
    <View>
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{k}</Text>
      <Text style={[{ fontSize: fs.lg, fontWeight: "600", color: color ?? colors.text }, tnum]}>{v}</Text>
    </View>
  );
}

function Th({ children, right, flex = 1 }: { children: string; right?: boolean; flex?: number }) {
  return <Text style={{ flex, fontSize: fs.xs, color: colors.textFaint, textAlign: right ? "right" : "left" }}>{children}</Text>;
}

function Td({ children, right, flex = 1, color }: { children: React.ReactNode; right?: boolean; flex?: number; color?: string }) {
  return <Text style={[{ flex, fontSize: fs.sm, color: color ?? colors.text, textAlign: right ? "right" : "left" }, tnum]}>{children}</Text>;
}

function Ladder({ px, usdt, cum, max, ask }: { px: number; usdt: number; cum: number; max: number; ask?: boolean }) {
  return (
    <View style={{ flexDirection: "row", height: 22, alignItems: "center", paddingHorizontal: 6 }}>
      <View style={{ position: "absolute", right: 0, top: 2, bottom: 2, width: `${(cum / max) * 100}%`, borderRadius: 2, backgroundColor: ask ? colors.downBg : colors.upBg }} />
      <Td color={ask ? colors.down : colors.up}>{price(px)}</Td>
      <Td right>{usd(usdt)}</Td>
      <Td right color={colors.textSoft}>{usd(cum)}</Td>
    </View>
  );
}

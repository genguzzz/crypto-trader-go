// Live order book (web market/OrderBookView.vue): REST snapshot every second while shown — summary,
// ±1% imbalance, the capital needed to push price ±0.1…5%, and a top-of-book ladder.
import { useMemo } from "react";
import { Text, View } from "react-native";
import { api, type BookLevel } from "@/lib/api";
import { ago, price, usd } from "@/lib/fmt";
import { useQuery } from "@/lib/query";
import { colors, fs, tnum } from "@/lib/theme";
import { Loading } from "./ui";

interface DepthPoint { off: number; cum: number; base: number }

function cumulative(levels: BookLevel[], mid: number, ctVal: number): DepthPoint[] {
  let cum = 0, base = 0;
  return levels.map(([px, usdt, sz]) => {
    cum += usdt;
    base += sz * ctVal;
    return { off: (px / mid - 1) * 100, cum, base };
  });
}

/** Market order needed to move the price from mid to mid×(1+pct/100). */
function sweepTo(pct: number, bids: DepthPoint[], asks: DepthPoint[], mid: number) {
  const buy = pct >= 0;
  const side = buy ? asks : bids;
  let last: DepthPoint | null = null;
  for (const p of side) {
    if (buy ? p.off <= pct : p.off >= pct) last = p;
    else break;
  }
  const usdt = last?.cum ?? 0, base = last?.base ?? 0;
  const avgPx = base > 0 ? usdt / base : mid;
  const edge = side[side.length - 1];
  return { usdt, slipPct: (avgPx / mid - 1) * 100, beyond: !!edge && (buy ? pct > edge.off : pct < edge.off) };
}

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
    return { table, la, lb, max };
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
        <View style={{ flexDirection: "row", paddingBottom: 4, borderBottomWidth: 1, borderColor: colors.border }}>
          <Th flex={0.8}>价格偏离</Th><Th right>买入推高需</Th><Th right>卖出压低需</Th><Th right flex={1.3}>均价滑点 买/卖</Th>
        </View>
        {d.table.map((r) => (
          <View key={r.p} style={{ flexDirection: "row", height: 28, alignItems: "center", borderBottomWidth: 1, borderColor: colors.border }}>
            <Td flex={0.8}>±{r.p}%</Td>
            <Td right color={colors.up}>{usd(r.up.usdt)}{r.up.beyond ? "+" : ""}</Td>
            <Td right color={colors.down}>{usd(r.dn.usdt)}{r.dn.beyond ? "+" : ""}</Td>
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

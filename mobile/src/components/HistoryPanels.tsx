// Detail-page history panels (web CompareChart.vue, SeriesChart.vue, LiqPanel.vue) in native form.
import { useMemo, useState } from "react";
import { Text, View } from "react-native";
import { api } from "@/lib/api";
import { price, sym, time, usd } from "@/lib/fmt";
import { useQuery } from "@/lib/query";
import { colors, fs, tnum } from "@/lib/theme";
import { BarChart, DualBarChart, LineChart } from "./Charts";
import { Card, Loading, PanelTitle, Segmented } from "./ui";

const LINE_COLORS = [colors.primary, colors.warn, colors.info]; // this coin, BTC, ETH (web CompareChart)

/** Relative performance vs BTC / ETH, rebased to 100 at the left edge. */
export function ComparePanel({ id, bare }: { id: string; bare?: boolean }) {
  const [bar, setBar] = useState("15m");
  const { data, error } = useQuery(`compare:${id}:${bar}`, () => api.compare(id, bar, 300), { staleMs: 60_000, interval: 60_000 });
  const series = useMemo(
    () => (data?.ids ?? []).map((sid, i) => ({ label: sym(sid), color: LINE_COLORS[i] ?? colors.textSoft, width: i === 0 ? 2 : 1.2, points: (data?.points ?? []).filter((p) => p.v[sid] != null).map((p) => ({ t: p.t, v: p.v[sid] })) })),
    [data],
  );
  const body = (
    <>
      <PanelTitle title={bare ? "" : "相对 BTC / ETH 表现"} right={<Segmented small fit value={bar} onChange={setBar} options={["5m", "15m", "1H", "4H"].map((v) => ({ value: v, label: v }))} />} />
      {series.length ? <LineChart series={series} height={170} fmtY={(v) => v.toFixed(1)} fmtT={(t) => time(t, true)} /> : error ? <Text style={note}>{error}</Text> : <Loading />}
      <Text style={[note, { marginTop: 6 }]}>三者都以左端为 100 重新定基，看的是同一段时间里的相对涨跌。</Text>
    </>
  );
  return bare ? body : <Card>{body}</Card>;
}

/** Open interest in coins over 7 days (removes the "price went up so USD OI grew" effect). */
export function OIPanel({ id }: { id: string }) {
  const { data, error } = useQuery(`oi7d:${id}`, () => api.series(id, "oi", 168, 5 * 60_000), { staleMs: 60_000, interval: 60_000 });
  const pts = useMemo(() => (data ?? []).map((p) => ({ t: p.t, v: p.v })), [data]);
  return (
    <Card>
      <PanelTitle title="持仓量 OI" hint="币本位 · 7 天" />
      {pts.length ? <LineChart series={[{ label: "OI（币）", color: colors.info, points: pts, width: 1.5 }]} height={160} fmtY={usd} fmtT={(t) => time(t, true)} /> : error ? <Text style={note}>{error}</Text> : data ? <Text style={note}>没有持仓量数据</Text> : <Loading />}
      <Text style={[note, { marginTop: 6 }]}>OI 增加只说明新开仓多于平仓，不能说明是多头还是空头在加仓。</Text>
    </Card>
  );
}

/** Settled funding rates over 14 days (one bar per settlement). */
export function FundingPanel({ id }: { id: string }) {
  const { data, error } = useQuery(`funding14d:${id}`, () => api.series(id, "funding", 14 * 24), { staleMs: 5 * 60_000 });
  const bars = useMemo(() => (data ?? []).map((p) => ({ t: p.t, v: p.v })), [data]);
  return (
    <Card>
      <PanelTitle title="资金费" hint="已结算 · 14 天" />
      {bars.length ? <BarChart bars={bars} height={140} fmtY={(v) => (v * 100).toFixed(4) + "%"} fmtT={(t) => time(t, true)} /> : error ? <Text style={note}>{error}</Text> : data ? <Text style={note}>没有资金费记录</Text> : <Loading />}
      <Text style={[note, { marginTop: 6 }]}>正值 = 多头付给空头。结算间隔因合约而异（4h/8h），跨币比较请看“资金费/小时”。</Text>
    </Card>
  );
}

/** Recorded liquidations: window totals, bars per period, latest orders. */
export function LiqPanel({ id }: { id: string }) {
  const [period, setPeriod] = useState("1H");
  const depth: Record<string, number> = { "15m": 96, "1H": 72, "4H": 90 };
  const step: Record<string, number> = { "15m": 900e3, "1H": 3600e3, "4H": 14400e3 };
  const main = useQuery(`liq:${id}:${period}`, () => api.liquidations(id, period, depth[period], 20), { interval: 10_000, staleMs: 8_000 });
  const fine = useQuery(`liq5m:${id}`, () => api.liquidations(id, "5m", 289), { interval: 10_000, staleMs: 8_000 });

  const totals = useMemo(() => {
    const now = fine.at;
    return [1, 4, 24].map((h) => {
      const from = now - h * 3600e3;
      let long = 0, short = 0;
      for (const b of fine.data?.bars ?? []) if (b.t + 300e3 > from) { long += b.long; short += b.short; }
      return { h, long, short, partial: !!fine.data?.since && fine.data.since > from };
    });
  }, [fine.data, fine.at]);

  // one up bar (shorts liquidated) and one down bar (longs) per period, zero where nothing happened
  const bars = useMemo(() => {
    const d = main.data;
    if (!d) return [];
    const s = step[period], m = new Map(d.bars.map((b) => [b.t, b]));
    const now = Math.floor(main.at / s) * s;
    const start = Math.max(d.since ? Math.floor(d.since / s) * s : now, now - (depth[period] - 1) * s);
    const out: { t: number; up: number; down: number }[] = [];
    for (let t = start; t <= now; t += s) {
      const b = m.get(t);
      out.push({ t, up: b?.short ?? 0, down: b?.long ?? 0 });
    }
    return out;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [main.data, main.at, period]);

  const recent = main.data?.recent ?? [];
  const bigCut = [...recent.map((x) => x.usd)].sort((a, b) => b - a)[Math.floor(recent.length / 10)] ?? Infinity;
  return (
    <Card>
      <PanelTitle title="强平" right={<Segmented small fit value={period} onChange={setPeriod} options={["15m", "1H", "4H"].map((v) => ({ value: v, label: v }))} />} />
      <View style={{ flexDirection: "row", gap: 10, marginBottom: 10 }}>
        {totals.map((x) => (
          <View key={x.h} style={{ flex: 1 }}>
            <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>近 {x.h} 小时{x.partial ? "（不完整）" : ""}</Text>
            <Text style={[{ fontSize: fs.md }, tnum]}>
              <Text style={{ color: colors.down, fontWeight: "600" }}>{usd(x.long)}</Text>
              <Text style={{ color: colors.textFaint }}> / </Text>
              <Text style={{ color: colors.up, fontWeight: "600" }}>{usd(x.short)}</Text>
            </Text>
            {x.long + x.short > 0 ? (
              <View style={{ height: 3, marginTop: 4, backgroundColor: colors.up, borderRadius: 2, overflow: "hidden" }}>
                <View style={{ height: 3, width: `${(x.long / (x.long + x.short)) * 100}%`, backgroundColor: colors.down }} />
              </View>
            ) : null}
          </View>
        ))}
      </View>
      {bars.length ? <DualBarChart bars={bars} height={160} upLabel="空单" downLabel="多单" fmtV={usd} fmtT={(t) => time(t, true)} /> : main.error ? <Text style={note}>{main.error}</Text> : <Loading />}
      <Text style={[note, { marginTop: 4 }]}>每个周期两根柱：绿向上 = 空单被强平（强制买入），红向下 = 多单被强平（强制卖出）。按住图表查看该周期金额。OKX 只提供最近约 24 小时，本机保留 30 天。</Text>
      {main.data?.err ? <Text style={[note, { color: colors.warn }]}>同步失败：{main.data.err}</Text> : null}
      {recent.length ? (
        <View style={{ marginTop: 10 }}>
          <View style={{ flexDirection: "row", paddingBottom: 4, borderBottomWidth: 1, borderColor: colors.border }}>
            {["时间", "方向", "价格", "金额"].map((h, i) => <Text key={h} style={{ flex: 1, fontSize: fs.xs, color: colors.textFaint, textAlign: i > 1 ? "right" : "left" }}>{h}</Text>)}
          </View>
          {recent.map((x, i) => (
            <View key={i} style={{ flexDirection: "row", height: 24, alignItems: "center" }}>
              <Text style={[cell, { color: colors.textFaint }]}>{time(x.t)}</Text>
              <Text style={[cell, { color: x.long ? colors.down : colors.up }]}>{x.long ? "多单" : "空单"}</Text>
              <Text style={[cell, { textAlign: "right" }]}>{price(x.px)}</Text>
              <Text style={[cell, { textAlign: "right", fontWeight: x.usd >= bigCut ? "700" : "400" }]}>{usd(x.usd)}</Text>
            </View>
          ))}
        </View>
      ) : null}
    </Card>
  );
}

const note = { fontSize: fs.xs, lineHeight: 17, color: colors.textFaint } as const;
const cell = { flex: 1, fontSize: fs.sm, color: colors.text, fontVariant: ["tabular-nums" as const] };

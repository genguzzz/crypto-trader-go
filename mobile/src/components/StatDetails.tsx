// Instrument stat details (web components/details/FundingDetail, OIDetail, SpotDetail, TurnoverDetail):
// a row of key numbers and the chart behind them (web charts/FundingChart, OIChart, BasisChart,
// TurnoverChart), drawn natively with Charts.tsx. Shown in the instrument page's detail sheet.
import { useMemo, useState, type ReactNode } from "react";
import { Text, View } from "react-native";
import { api, type InstrumentInfo, type Row } from "@/lib/api";
import { fieldValue, logPct, price, sign, time, usd, type Tone } from "@/lib/fmt";
import { useLivePrice, useUniverse } from "@/lib/market";
import { useQuery } from "@/lib/query";
import { colors, fs, tnum } from "@/lib/theme";
import { BarChart, LineChart } from "./Charts";
import { B, InfoTip, P } from "./Form";
import { Loading, Segmented, toneColor } from "./ui";

interface Kpi { label: string; value: string; sub?: string; tone?: Tone }

/** Compact grid of key numbers (web ui/KpiRow.vue). */
function Kpis({ items }: { items: Kpi[] }) {
  return (
    <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
      {items.map((k) => (
        <View key={k.label} style={{ flexGrow: 1, flexBasis: "40%", minWidth: 0, backgroundColor: colors.raised, borderRadius: 8, paddingHorizontal: 12, paddingVertical: 8 }}>
          <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{k.label}</Text>
          <Text style={[{ fontSize: fs.xl, fontWeight: "600", color: toneColor(k.tone) }, tnum]} numberOfLines={1}>{k.value}</Text>
          {k.sub ? <Text style={{ fontSize: fs.xs, color: colors.textSoft }} numberOfLines={1}>{k.sub}</Text> : null}
        </View>
      ))}
    </View>
  );
}

function ChartHead({ title, tip, right }: { title: string; tip: ReactNode; right?: ReactNode }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", flexWrap: "wrap", gap: 8, marginTop: 14, marginBottom: 6 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 4, flexShrink: 1, marginRight: "auto" }}>
        <Text style={{ fontSize: fs.md, fontWeight: "600", color: colors.text, flexShrink: 1 }}>{title}</Text>
        <InfoTip title={title}>{tip}</InfoTip>
      </View>
      {right}
    </View>
  );
}

function Empty({ error, loaded, text }: { error?: string; loaded: boolean; text: string }) {
  return error ? <Text style={note}>{error}</Text> : loaded ? <Text style={note}>{text}</Text> : <Loading />;
}

const pctOf = (v?: number | null, d = 4) => (v == null || !isFinite(v) ? "—" : (v * 100).toFixed(d) + "%");
const fmtT = (t: number) => time(t, true);

// ---------- funding ----------

export function FundingDetail({ info, row, countdown }: { info: InstrumentInfo; row?: Row; countdown: string }) {
  const { data, error, at } = useQuery(`funding30d:${info.instId}`, () => api.series(info.instId, "funding", 30 * 24), { staleMs: 5 * 60_000 });
  const bars = useMemo(() => {
    const out: { t: number; v: number }[] = [];
    for (const p of data ?? []) if (!out.length || p.t > out[out.length - 1].t) out.push({ t: p.t, v: p.v });
    return out;
  }, [data]);
  const iv = info.fundingIntervalH || 8, v = row?.values;
  const week = bars.filter((x) => x.t > at - 7 * 864e5); // `at` = when the history was fetched
  const mean = week.length ? week.reduce((a, b) => a + b.v, 0) / week.length : null;
  const items: Kpi[] = [
    { label: "本期费率", value: pctOf(info.fundingRate), tone: sign(info.fundingRate), sub: `${iv}h 结算 · ${time(info.fundingTime)}` },
    { label: "距结算", value: countdown, sub: "倒计时" },
    { label: "折合每小时", value: pctOf(info.fundingRate / iv, 5), tone: sign(info.fundingRate), sub: "跨币比较用" },
    { label: "年化", value: pctOf((info.fundingRate * 24 * 365) / iv, 1), tone: sign(info.fundingRate), sub: "按本期费率简单年化" },
    { label: "上期已结算", value: pctOf(v?.funding_hourly != null ? v.funding_hourly * iv : null), tone: sign(v?.funding_hourly), sub: "评分用这一项" },
    { label: "7 日均值", value: pctOf(mean), tone: sign(mean), sub: `${week.length} 次结算` },
  ];
  return (
    <View>
      <Kpis items={items} />
      <ChartHead title="已结算资金费（30 天，单次 %）" tip={<P>正值 = 多头付给空头，负值 = 空头付给多头。OKX 各合约结算间隔不同（4h/8h），比较时看“折合每小时”。年化只是按本期费率简单换算，不代表未来。</P>} />
      {bars.length ? <BarChart bars={bars} height={220} fmtY={(x) => (x * 100).toFixed(4) + "%"} fmtT={fmtT} /> : <Empty error={error} loaded={!!data} text="没有资金费记录" />}
    </View>
  );
}

// ---------- open interest ----------

export function OIDetail({ info, row }: { info: InstrumentInfo; row?: Row }) {
  const [unit, setUnit] = useState<"ccy" | "usd">("ccy");
  const [days, setDays] = useState("7");
  const hours = Number(days) * 24;
  // recorded per minute; ≤ ~2000 points is plenty (7 days → 5 min, 1 day → as recorded)
  const min = Math.ceil((hours * 60) / 2016);
  const { data, error } = useQuery(`oi:${info.instId}:${hours}`, () => api.series(info.instId, "oi", hours, min > 1 ? min * 60e3 : 0), { staleMs: 60_000, interval: 60_000 });
  const pts = useMemo(() => {
    const out: { t: number; v: number }[] = [];
    for (const p of data ?? []) if (!out.length || p.t > out[out.length - 1].t) out.push({ t: p.t, v: unit === "usd" ? p.u || 0 : p.v });
    return out;
  }, [data, unit]);
  const v = row?.values;
  const items: Kpi[] = [
    { label: "持仓量（USD）", value: usd(info.oiUsd), sub: "名义价值" },
    { label: "持仓量（币）", value: usd(info.oiCcy), sub: "币本位 oiCcy" },
    { label: "1h 变化", value: logPct(v?.oi_chg_1h), tone: sign(v?.oi_chg_1h), sub: "币本位" },
    { label: "4h 变化", value: logPct(v?.oi_chg_4h), tone: sign(v?.oi_chg_4h), sub: "币本位" },
    { label: "OI / 12h 成交额", value: row?.turnover12h ? (info.oiUsd / row.turnover12h).toFixed(2) : "—", sub: "越高 = 持仓相对成交越重" },
  ];
  return (
    <View>
      <Kpis items={items} />
      <ChartHead
        title="持仓量走势"
        tip={<P>OI 是多空双方共同的未平仓合约数。币本位变化排除了价格涨跌对美元持仓的影响；OI 上升只说明新开仓多于平仓，<B>不代表</B>多头或空头哪一方在加仓。</P>}
        right={
          <View style={{ flexDirection: "row", gap: 6 }}>
            <Segmented small value={unit} onChange={setUnit} options={[{ value: "ccy", label: "币" }, { value: "usd", label: "USD" }]} />
            <Segmented small value={days} onChange={setDays} options={[{ value: "1", label: "1天" }, { value: "3", label: "3天" }, { value: "7", label: "7天" }]} />
          </View>
        }
      />
      {pts.length ? <LineChart series={[{ label: unit === "usd" ? "OI（USD）" : "OI（币）", color: colors.violet, points: pts, width: 2 }]} height={240} fmtY={usd} fmtT={fmtT} /> : <Empty error={error} loaded={!!data} text="没有持仓量数据" />}
    </View>
  );
}

// ---------- spot & basis ----------

export function SpotDetail({ info, row }: { info: InstrumentInfo; row?: Row }) {
  const [bar, setBar] = useState("15m");
  const perp = useLivePrice(info.instId, info.last);
  const id = info.instId;
  const { data, error } = useQuery(info.spotId ? `basis:${id}:${bar}` : null, async () => {
    const [p, s] = await Promise.all([api.candles(id, bar, 400), api.candles(id, bar, 400, "spot")]);
    const sp = new Map(s.map((c) => [c.t, c.c]));
    return p.filter((c) => sp.has(c.t)).map((c) => ({ t: c.t, v: (c.c / sp.get(c.t)! - 1) * 1e4 }));
  }, { staleMs: 60_000, interval: 60_000 });
  if (!info.spotId) return <Text style={note}>该合约没有 OKX 同名 USDT 现货（股票/商品永续或未上现货的币），无法计算基差。</Text>;
  const b = row?.values?.basis;
  const items: Kpi[] = [
    { label: "OKX 现货", value: price(info.spotLast), sub: info.spotId },
    { label: "永续", value: price(perp), sub: id },
    { label: "基差", value: fieldValue("basis", b), tone: b == null ? "" : b > 0 ? "up" : "down", sub: "永续相对现货" },
    { label: "基差（bps）", value: b != null ? ((Math.exp(b) - 1) * 1e4).toFixed(1) : "—", sub: "1 bps = 0.01%" },
  ];
  return (
    <View>
      <Kpis items={items} />
      <ChartHead
        title="期现基差走势（bps）"
        tip={<P>基差 = 永续收盘 ÷ 同时刻 OKX 同名 USDT 现货收盘 − 1。正值 = 永续比现货贵（多头情绪/资金费偏正时常见），负值相反。只是 OKX 一家现货，不代表全市场。</P>}
        right={<Segmented small fit value={bar} onChange={setBar} options={["5m", "15m", "1H"].map((x) => ({ value: x, label: x }))} />}
      />
      {data?.length ? <LineChart series={[{ label: "基差", color: colors.warn, points: data, width: 2 }]} height={220} fmtY={(x) => x.toFixed(1)} fmtT={fmtT} refY={0} /> : <Empty error={error} loaded={!!data} text="没有可对齐的现货 K 线" />}
    </View>
  );
}

// ---------- turnover ----------

const WINDOW_H = 12; // the 12h pool window, highlighted in the hourly bars

export function TurnoverDetail({ info, row }: { info: InstrumentInfo; row?: Row }) {
  const th = useUniverse().data?.threshold || 0;
  const { data, error } = useQuery(`turnover72h:${info.instId}`, () => api.candles(info.instId, "1H", 72), { staleMs: 60_000, interval: 60_000 });
  const bars = useMemo(() => {
    const cs = data ?? [], cut = cs.length - WINDOW_H;
    return cs.map((c, i) => ({ t: c.t, v: c.q, color: i >= cut ? colors.info : "rgba(168,168,168,0.35)" }));
  }, [data]);
  const vs = row?.values?.vol_surprise_4h;
  const items: Kpi[] = [
    { label: "12h 成交额", value: usd(row?.turnover12h) + " USDT", sub: row?.active ? `池内第 ${row.rank} 名` : "未进监控池" },
    { label: "相对阈值", value: th && row?.turnover12h ? "×" + (row.turnover12h / th).toFixed(2) : "—", sub: `阈值 ${usd(th)} USDT` },
    { label: "4h 放量", value: vs != null ? "×" + Math.exp(vs).toFixed(2) : "—", sub: "对 7 日 4h 中位数" },
    { label: "池内分位", value: row?.pct?.turnover12h != null ? row.pct.turnover12h.toFixed(0) : "—", sub: "0 最低 · 100 最高" },
  ];
  return (
    <View>
      <Kpis items={items} />
      <ChartHead title="每小时 USDT 成交额（72h，蓝色 = 当前 12h 窗口）" tip={<P>12h 成交额 = 最近 720 根已确认 1m K 线 volCcyQuote 之和，严格大于阈值才进监控池。这里按小时汇总展示，便于看成交是否集中在某一段。</P>} />
      {bars.length ? <BarChart bars={bars} height={220} fmtY={usd} fmtT={fmtT} upColor={colors.info} /> : <Empty error={error} loaded={!!data} text="没有成交数据" />}
    </View>
  );
}

const note = { fontSize: fs.xs, lineHeight: 17, color: colors.textFaint } as const;

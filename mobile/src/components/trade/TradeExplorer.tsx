// Account performance explorer (web TradeExplorer), shared by the account page and the backtest
// report. 组合视角: native equity curve with the deposit line and a drawdown pane; 币对视角: the web
// trade-marked candle chart in a WebView (TradeCandleView). Below: the round trips with the web's
// filters; a row opens its card in a sheet (prev / next, 在 K 线中查看).
import { forwardRef, useImperativeHandle, useMemo, useState, type ReactNode } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { price, time } from "@/lib/fmt";
import { useQuery } from "@/lib/query";
import { tradeApi, type Trade } from "@/lib/tradeApi";
import { amt, byInstrument, exitKindLabel, holdText, pctf, qty, tone } from "@/lib/tradeMeta";
import { colors, fs, radius, tnum } from "@/lib/theme";
import { LineChart } from "../Charts";
import { CoinIcon } from "../CoinIcon";
import { IconButton, PickerSheet, Segmented, Sheet, Tag, tap } from "../ui";
import { InfoTip, LTag, MoreButton, Muted, SmallButton, toneText } from "./common";
import { TradeCandleView } from "./TradeCandleView";

const symOf = (id: string) => id.replace("-USDT-SWAP", "");

/** Equity (USDT) with the deposit as a flat line, and the running drawdown from the peak below. */
export function EquityChart({ points, deposit, height = 200 }: { points: [number, number][]; deposit: number; height?: number }) {
  const { eq, dd } = useMemo(() => {
    const eq: { t: number; v: number }[] = [], dd: { t: number; v: number }[] = [];
    let peak = -Infinity, last = -Infinity;
    for (const [t, v] of points) {
      if (!(t > last) || !isFinite(v)) continue;
      last = t;
      peak = Math.max(peak, v);
      eq.push({ t, v });
      dd.push({ t, v: peak > 0 ? (v / peak - 1) * 100 : 0 });
    }
    return { eq, dd };
  }, [points]);
  if (eq.length < 2) return null;
  const fmtT = (t: number) => time(t, true);
  const big = Math.max(...eq.map((p) => Math.abs(p.v))) >= 1000;
  const base = deposit ? [{ label: "入金", color: colors.textDim, width: 1, points: [{ t: eq[0].t, v: deposit }, { t: eq[eq.length - 1].t, v: deposit }] }] : [];
  return (
    <View style={{ gap: 6 }}>
      <LineChart height={height} fmtT={fmtT} fmtY={(v) => amt(v, big ? 0 : 2)} series={[{ label: "权益", color: colors.primary, width: 2, points: eq }, ...base]} />
      <LineChart height={86} fmtT={fmtT} fmtY={(v) => v.toFixed(1) + "%"} series={[{ label: "回撤%", color: colors.down, points: dd }]} />
    </View>
  );
}

interface Filter { inst: string; side: "all" | "long" | "short"; result: "all" | "win" | "loss" | "open"; exit: string; sort: "time" | "net" | "ret" }
const matches = (t: Trade, x: Filter) =>
  (x.inst === "all" || t.instId === x.inst) && (x.side === "all" || t.posSide === x.side) && (x.exit === "all" || t.exitKind === x.exit) &&
  (x.result === "all" || (x.result === "open" ? !t.closeTs : x.result === "win" ? !!t.closeTs && t.net > 0 : !!t.closeTs && t.net <= 0));

export interface ExplorerHandle { openInst: (id: string) => void }

export const TradeExplorer = forwardRef<ExplorerHandle, {
  accountId: number; points: [number, number][]; deposit: number; reloadKey: string; tools?: ReactNode; onTouchActive?: (on: boolean) => void;
}>(function TradeExplorer({ accountId, points, deposit, reloadKey, tools, onTouchActive }, ref) {
  const router = useRouter();
  const q = useQuery<Trade[]>(`trade:trades:${accountId}:${reloadKey}`, () => tradeApi.trades(accountId), { staleMs: 60_000 });
  const trades = useMemo(() => (q.data ?? []).slice().sort((a, b) => a.openTs - b.openTs || a.id - b.id), [q.data]);
  const insts = useMemo(() => byInstrument(trades), [trades]);
  const [view, setView] = useState<"equity" | "inst">("equity");
  const [inst, setInst] = useState("");
  const [sel, setSel] = useState(0); // trade selected in 币对视角
  const [card, setCard] = useState(0); // trade shown in the sheet
  const [filter, setFilter] = useState<Filter>({ inst: "all", side: "all", result: "all", exit: "all", sort: "time" });
  const [pickOpen, setPickOpen] = useState<"" | "inst" | "exit" | "sort">("");
  const [shown, setShown] = useState(10);
  const curInst = inst || insts[0]?.instId || "";
  const byId = useMemo(() => new Map(trades.map((t) => [t.id, t])), [trades]);

  const openInst = (id: string) => { setInst(id); setSel(0); setView("inst"); };
  useImperativeHandle(ref, () => ({ openInst }));
  const openTrade = (id: number) => {
    const t = byId.get(id);
    if (!t) return;
    setCard(0);
    setInst(t.instId);
    setSel(id);
    setView("inst");
  };
  const navIdx = (id: number) => trades.findIndex((t) => t.id === id);
  const step = (d: number) => {
    if (!trades.length) return;
    const i = navIdx(sel);
    const j = Math.min(trades.length - 1, Math.max(0, (i < 0 ? (d > 0 ? -1 : trades.length) : i) + d));
    openTrade(trades[j].id);
  };
  const cardStep = (d: number) => {
    const i = navIdx(card);
    const j = Math.min(trades.length - 1, Math.max(0, i + d));
    if (trades[j]) setCard(trades[j].id);
  };

  const rows = useMemo(() => {
    const key = filter.sort === "net" ? (t: Trade) => t.net : filter.sort === "ret" ? (t: Trade) => t.returnPct : (t: Trade) => t.openTs;
    return trades.filter((t) => matches(t, filter)).sort((a, b) => key(b) - key(a) || b.id - a.id);
  }, [trades, filter]);
  const sum = { n: rows.length, net: rows.reduce((s, t) => s + t.net, 0), wins: rows.filter((t) => t.closeTs && t.net > 0).length, closed: rows.filter((t) => t.closeTs).length };
  const selTrade = byId.get(sel);
  const cardTrade = byId.get(card);
  const sortOpts = [{ value: "time", label: "按时间" }, { value: "net", label: "按净盈亏" }, { value: "ret", label: "按收益率" }];
  const exitOpts = [{ value: "all", label: "全部离场" }, ...Object.entries(exitKindLabel).map(([k, v]) => ({ value: k, label: v.t }))];
  const instOpts = [{ value: "all", label: "全部合约" }, ...[...new Set(trades.map((t) => t.instId))].sort().map((i) => ({ value: i, label: symOf(i) }))];

  return (
    <View style={{ gap: 10 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <Segmented small value={view} onChange={(v) => { if (v === "inst" && !trades.length) return; setView(v); }} options={[{ value: "equity", label: "组合视角" }, { value: "inst", label: "币对视角" }]} />
        <InfoTip title="组合视角 / 币对视角" items={[
          ["组合视角", "：账户权益曲线，入金为灰色水平线；下方是自权益高点的回撤。按住图表拖动查看任意时点。"],
          ["币对视角", "：所选合约的 K 线，每次成交按成交价和时间标出（▲ 买入 ▼ 卖出，虚线圈 = 开仓），持仓区间按盈亏着色，所选交易的止损 / 止盈 / 开平仓价画成价格线。双指缩放，拖动平移。"],
          "点下方列表里的一笔交易看详情，再点“在 K 线中查看”。",
        ]} />
        <View style={{ flex: 1 }} />
        {view === "inst" && trades.length ? (
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <IconButton name="chevron-back" size={18} label="上一笔" onPress={() => step(-1)} />
            <Text style={[{ fontSize: fs.xs, color: colors.textFaint, minWidth: 40, textAlign: "center" }, tnum]}>{navIdx(sel) >= 0 ? navIdx(sel) + 1 : "—"}/{trades.length}</Text>
            <IconButton name="chevron-forward" size={18} label="下一笔" onPress={() => step(1)} />
          </View>
        ) : null}
      </View>

      {tools && view === "equity" ? <View style={{ alignSelf: "flex-end" }}>{tools}</View> : null}

      {view === "inst" && insts.length ? (
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 6 }}>
          {insts.map((c) => {
            const on = c.instId === curInst;
            return (
              <Pressable key={c.instId} onPress={() => { tap(); openInst(c.instId); }} style={{ flexDirection: "row", alignItems: "center", gap: 6, height: 30, paddingHorizontal: 10, borderRadius: 15, borderWidth: 1, borderColor: on ? colors.primary : colors.borderStrong, backgroundColor: on ? colors.primarySoft : "transparent" }}>
                <CoinIcon instId={c.instId} size={16} />
                <Text style={{ fontWeight: "600", color: colors.text, fontSize: fs.sm }}>{symOf(c.instId)}</Text>
                <Text style={[{ fontSize: fs.sm, color: toneText(tone(c.net)) }, tnum]}>{amt(c.net, 0, true)}</Text>
                <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{c.n} 笔</Text>
              </Pressable>
            );
          })}
        </ScrollView>
      ) : null}

      {view === "equity" ? (
        <View>
          <EquityChart points={points} deposit={deposit} />
          {q.data && !trades.length ? <Muted style={{ marginTop: 4 }}>还没有交易</Muted> : null}
        </View>
      ) : curInst ? (
        <>
          <TradeCandleView key={`${accountId}:${curInst}:${reloadKey}`} accountId={accountId} instId={curInst} tradeId={selTrade?.instId === curInst ? sel : 0} onTouchActive={onTouchActive} onPick={(ids) => { if (ids[0]) setSel(ids[0]); }} style={{ height: 340, borderRadius: radius.sm }} />
          {selTrade ? (
            <View style={{ padding: 12, borderRadius: radius.md, backgroundColor: colors.raised, borderWidth: 1, borderColor: colors.borderStrong }}>
              <TradeCard t={selTrade} nav={{ i: navIdx(sel), n: trades.length }} onPrev={() => step(-1)} onNext={() => step(1)} onAi={selTrade.aiDecisionId ? () => router.push({ pathname: "/trade", params: { pane: "ai" } }) : undefined} />
            </View>
          ) : <Muted>点击 K 线上的标记或下方列表查看一笔交易</Muted>}
        </>
      ) : null}

      {q.error ? <Text style={{ color: colors.down, fontSize: fs.sm }}>{q.error}</Text> : null}

      {trades.length ? (
        <View style={{ gap: 8 }}>
          <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 6, alignItems: "center" }}>
            <Dropdown label={instOpts.find((o) => o.value === filter.inst)?.label ?? "全部合约"} onPress={() => setPickOpen("inst")} />
            <Segmented small style={{ width: 150 }} value={filter.side} onChange={(v) => setFilter({ ...filter, side: v })} options={[{ value: "all", label: "全部" }, { value: "long", label: "多", tone: "up" }, { value: "short", label: "空", tone: "down" }]} />
            <Segmented small style={{ width: 230 }} value={filter.result} onChange={(v) => setFilter({ ...filter, result: v })} options={[{ value: "all", label: "全部" }, { value: "win", label: "盈利" }, { value: "loss", label: "亏损" }, { value: "open", label: "持仓" }]} />
            <Dropdown label={exitOpts.find((o) => o.value === filter.exit)?.label ?? "全部离场"} onPress={() => setPickOpen("exit")} />
            <Dropdown label={sortOpts.find((o) => o.value === filter.sort)?.label ?? "按时间"} onPress={() => setPickOpen("sort")} />
          </ScrollView>
          <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>
            {sum.n} 笔 · 胜率 {sum.closed ? pctf(sum.wins / sum.closed, 0) : "—"} · <Text style={{ fontWeight: "600", color: toneText(tone(sum.net)) }}>{amt(sum.net, 2, true)}</Text>
          </Text>
          <View style={{ borderTopWidth: 1, borderColor: colors.border }}>
            {rows.slice(0, shown).map((t) => <TradeRow key={t.id} t={t} selected={t.id === sel && view === "inst"} onPress={() => setCard(t.id)} />)}
            {!rows.length ? <Muted style={{ paddingVertical: 8 }}>没有符合筛选的交易</Muted> : null}
            {rows.length > shown ? <MoreButton label={`显示更多（共 ${rows.length} 笔）`} onPress={() => setShown((n) => n + 50)} /> : null}
          </View>
        </View>
      ) : null}

      <PickerSheet visible={pickOpen === "inst"} onClose={() => setPickOpen("")} title="合约" value={filter.inst} options={instOpts} onPick={(v) => setFilter({ ...filter, inst: v })} />
      <PickerSheet visible={pickOpen === "exit"} onClose={() => setPickOpen("")} title="离场方式" value={filter.exit} options={exitOpts} onPick={(v) => setFilter({ ...filter, exit: v })} />
      <PickerSheet visible={pickOpen === "sort"} onClose={() => setPickOpen("")} title="排序" value={filter.sort} options={sortOpts} onPick={(v) => setFilter({ ...filter, sort: v as Filter["sort"] })} />
      <Sheet visible={!!cardTrade} onClose={() => setCard(0)} scroll>
        {cardTrade ? (
          <TradeCard t={cardTrade} fills nav={{ i: navIdx(card), n: trades.length }} onPrev={() => cardStep(-1)} onNext={() => cardStep(1)}
            onKline={() => openTrade(cardTrade.id)} onAi={cardTrade.aiDecisionId ? () => { setCard(0); router.push({ pathname: "/trade", params: { pane: "ai" } }); } : undefined} />
        ) : null}
      </Sheet>
    </View>
  );
});

function Dropdown({ label, onPress }: { label: string; onPress: () => void }) {
  return (
    <Pressable onPress={() => { tap(); onPress(); }} style={{ flexDirection: "row", alignItems: "center", gap: 4, height: 30, paddingHorizontal: 10, borderRadius: 8, borderWidth: 1, borderColor: colors.borderStrong }}>
      <Text style={{ fontSize: fs.sm, color: colors.text }}>{label}</Text>
      <Ionicons name="chevron-down" size={13} color={colors.textFaint} />
    </Pressable>
  );
}

function TradeRow({ t, selected, onPress }: { t: Trade; selected?: boolean; onPress: () => void }) {
  const long = t.posSide === "long";
  return (
    <Pressable onPress={() => { tap(); onPress(); }} style={({ pressed }) => ({ paddingVertical: 9, paddingHorizontal: 4, borderBottomWidth: 1, borderColor: colors.border, gap: 3, backgroundColor: selected ? colors.primarySolid : pressed ? colors.raised : "transparent" })}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <CoinIcon instId={t.instId} size={16} />
        <Text style={{ fontWeight: "600", color: colors.text, fontSize: fs.md }}>{symOf(t.instId)}</Text>
        <Text style={{ fontSize: fs.sm, fontWeight: "600", color: long ? colors.up : colors.down }}>{long ? "多" : "空"} <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{t.leverage}x</Text></Text>
        <LTag l={exitKindLabel[t.exitKind]} fallback={t.exitKind} />
        <View style={{ flex: 1 }} />
        <Text style={[{ fontSize: fs.md, fontWeight: "700", color: toneText(tone(t.net)) }, tnum]}>{amt(t.net, 2, true)}</Text>
        <Text style={[{ fontSize: fs.xs, width: 52, textAlign: "right", color: toneText(tone(t.returnPct)) }, tnum]}>{pctf(t.returnPct, 1, true)}</Text>
      </View>
      <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>
        {time(t.openTs, true)} · {price(t.entryPx)} → {t.exitPx ? price(t.exitPx) : "—"} · {holdText(t)} · 名义 {amt(t.notional, 0)}
      </Text>
    </Pressable>
  );
}

const fillKind: Record<string, string> = { open: "开仓", add: "加仓", reduce: "减仓", close: "平仓" };
const orderType: Record<string, string> = { market: "市价", limit: "限价", stop: "止损", target: "止盈", liquidation: "强平" };

/** One round trip (web TradeCard): result, prices, size, costs, reasons, fills. */
export function TradeCard({ t, fills, nav, onPrev, onNext, onKline, onAi }: {
  t: Trade; fills?: boolean; nav?: { i: number; n: number }; onPrev?: () => void; onNext?: () => void; onKline?: () => void; onAi?: () => void;
}) {
  const long = t.posSide === "long";
  const kv = (k: string, v: ReactNode) => (
    <View style={{ width: "50%", paddingRight: 8, paddingVertical: 3 }}>
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{k}</Text>
      <Text style={[{ fontSize: fs.sm, color: colors.text, marginTop: 1 }, tnum]}>{v}</Text>
    </View>
  );
  const sub = (s: string) => <Text style={{ color: colors.textFaint }}>{s}</Text>;
  return (
    <View style={{ gap: 8 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <CoinIcon instId={t.instId} size={20} />
        <Text style={{ fontSize: fs.lg, fontWeight: "700", color: colors.text }}>{symOf(t.instId)}</Text>
        <Tag text={`${long ? "多" : "空"} ${t.leverage}x`} tone={long ? "good" : "bad"} />
        <LTag l={exitKindLabel[t.exitKind]} fallback={t.exitKind} />
        <View style={{ flex: 1 }} />
        {nav ? (
          <>
            <IconButton name="chevron-back" size={18} label="上一笔" onPress={() => onPrev?.()} color={nav.i <= 0 ? colors.textDim : undefined} />
            <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{nav.i + 1}/{nav.n}</Text>
            <IconButton name="chevron-forward" size={18} label="下一笔" onPress={() => onNext?.()} color={nav.i >= nav.n - 1 ? colors.textDim : undefined} />
          </>
        ) : null}
      </View>
      <View style={{ flexDirection: "row", alignItems: "baseline", gap: 12, flexWrap: "wrap" }}>
        <Text style={[{ fontSize: fs.x2, fontWeight: "700", color: toneText(tone(t.net)) }, tnum]}>{amt(t.net, 2, true)}<Text style={{ fontSize: fs.xs, color: colors.textFaint, fontWeight: "500" }}> USDT</Text></Text>
        <Text style={[{ fontSize: fs.md, fontWeight: "600", color: toneText(tone(t.returnPct)) }, tnum]}>{pctf(t.returnPct, 2, true)}<Text style={{ fontSize: fs.xs, color: colors.textFaint, fontWeight: "500" }}> 保证金收益</Text></Text>
      </View>
      <View style={{ flexDirection: "row", flexWrap: "wrap" }}>
        {kv("开仓", <>{time(t.openTs, true)} {sub(`@ ${price(t.entryPx)}`)}</>)}
        {kv("平仓", t.closeTs ? <>{time(t.closeTs, true)} {sub(`@ ${price(t.exitPx)}`)}</> : "持仓中")}
        {kv("持有", holdText(t))}
        {kv("数量", <>{qty(t.qty)} 张 {sub(`× ${t.ctVal}`)}</>)}
        {kv("名义", <>{amt(t.notional)} {sub("USDT")}</>)}
        {kv("保证金", <>{amt(t.margin)} {sub("USDT")}</>)}
        {kv("盈亏（未含费）", <Text style={{ color: toneText(tone(t.pnl)) }}>{amt(t.pnl, 2, true)}</Text>)}
        {kv("手续费 / 资金费", <><Text style={{ color: colors.down }}>{amt(-Math.abs(t.fees), 2)}</Text> / <Text style={{ color: toneText(tone(t.funding)) }}>{amt(t.funding, 2, true)}</Text></>)}
        {t.stopLoss || t.takeProfit ? kv("止损 / 止盈", <><Text style={{ color: colors.down }}>{price(t.stopLoss)}</Text> / <Text style={{ color: colors.up }}>{price(t.takeProfit)}</Text></>) : null}
      </View>
      {t.openReason ? <Text style={{ fontSize: fs.sm, color: colors.textSoft }}><Text style={{ fontSize: fs.xs, color: colors.textFaint }}>开仓理由  </Text>{t.openReason}</Text> : null}
      {t.closeReason && t.closeReason !== t.openReason ? <Text style={{ fontSize: fs.sm, color: colors.textSoft }}><Text style={{ fontSize: fs.xs, color: colors.textFaint }}>平仓理由  </Text>{t.closeReason}</Text> : null}
      {fills && t.fills.length ? (
        <View style={{ borderTopWidth: 1, borderColor: colors.border, paddingTop: 6, gap: 3 }}>
          {t.fills.map((f) => (
            <View key={f.id} style={{ flexDirection: "row", gap: 6, alignItems: "baseline" }}>
              <Text style={[{ fontSize: fs.xs, color: colors.textFaint, width: 76 }, tnum]}>{time(f.ts, true)}</Text>
              <Text style={{ fontSize: fs.xs, width: 30, color: f.side === "buy" ? colors.up : colors.down }}>{fillKind[f.kind] || f.kind}</Text>
              <Text style={{ fontSize: fs.xs, width: 30, color: colors.textFaint }}>{orderType[f.orderType] || f.orderType}</Text>
              <Text style={[{ flex: 1, fontSize: fs.xs, color: colors.text }, tnum]}>{qty(f.qty)} 张 @ {price(f.px)}</Text>
              <Text style={[{ fontSize: fs.xs, color: toneText(tone(f.pnl)) }, tnum]}>{f.pnl ? amt(f.pnl, 2, true) : ""}</Text>
            </View>
          ))}
        </View>
      ) : null}
      {onKline || onAi ? (
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          {onAi ? <Pressable hitSlop={6} onPress={() => { tap(); onAi(); }}><Text style={{ fontSize: fs.xs, color: colors.textSoft, textDecorationLine: "underline" }}>AI 决策 #{t.aiDecisionId}</Text></Pressable> : null}
          <View style={{ flex: 1 }} />
          {onKline ? <SmallButton label="在 K 线中查看" icon="stats-chart" active onPress={onKline} /> : null}
        </View>
      ) : null}
    </View>
  );
}

/** Contribution per instrument: diverging bars around zero, sorted by |net| (web InstContribution). */
export function InstContribution({ rows, onOpen }: { rows: { instId: string; net: number; trades: number; wins: number }[]; onOpen: (id: string) => void }) {
  const list = [...rows].sort((a, b) => Math.abs(b.net) - Math.abs(a.net));
  const max = Math.max(1e-9, ...rows.map((r) => Math.abs(r.net)));
  const total = rows.reduce((s, r) => s + r.net, 0);
  if (!list.length) return <Muted>没有交易</Muted>;
  return (
    <View>
      {list.map((r) => (
        <Pressable key={r.instId} onPress={() => { tap(); onOpen(r.instId); }} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 8, borderBottomWidth: 1, borderColor: colors.border, backgroundColor: pressed ? colors.raised : "transparent" })}>
          <View style={{ width: 74, flexDirection: "row", alignItems: "center", gap: 6 }}>
            <CoinIcon instId={r.instId} size={16} />
            <Text style={{ fontWeight: "600", color: colors.text, fontSize: fs.sm }} numberOfLines={1}>{symOf(r.instId)}</Text>
          </View>
          <View style={{ flex: 1, height: 10, flexDirection: "row" }}>
            <View style={{ flex: 1, alignItems: "flex-end" }}>{r.net < 0 ? <View style={{ width: `${(Math.abs(r.net) / max) * 100}%`, height: 10, borderRadius: 2, backgroundColor: colors.down }} /> : null}</View>
            <View style={{ width: 1, marginVertical: -3, backgroundColor: colors.borderStrong }} />
            <View style={{ flex: 1 }}>{r.net >= 0 ? <View style={{ width: `${(Math.abs(r.net) / max) * 100}%`, height: 10, borderRadius: 2, backgroundColor: colors.up }} /> : null}</View>
          </View>
          <View style={{ width: 86, alignItems: "flex-end" }}>
            <Text style={[{ fontWeight: "600", fontSize: fs.sm, color: toneText(tone(r.net)) }, tnum]}>{amt(r.net, 2, true)}</Text>
            <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{r.trades} 笔 · {r.trades ? pctf(r.wins / r.trades, 0) : "—"}</Text>
          </View>
          <Ionicons name="chevron-forward" size={14} color={colors.textFaint} />
        </Pressable>
      ))}
      {list.length > 1 ? (
        <View style={{ flexDirection: "row", justifyContent: "space-between", paddingTop: 8 }}>
          <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>合计</Text>
          <Text style={[{ fontSize: fs.sm, fontWeight: "700", color: toneText(tone(total)) }, tnum]}>{amt(total, 2, true)} <Text style={{ fontSize: fs.xs, color: colors.textFaint, fontWeight: "400" }}>USDT</Text></Text>
        </View>
      ) : null}
    </View>
  );
}

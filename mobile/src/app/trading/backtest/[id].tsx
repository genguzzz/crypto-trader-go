// Backtest report (web views/trading/Report.vue): run state / progress, ten metrics, the trades
// explorer, contribution per instrument, strategy configuration; run again / cancel, open the
// account's signals and orders, start a live account with the same setup.
import { useRef, useState } from "react";
import { ScrollView, Text, View } from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { haptic } from "@/lib/device";
import { time } from "@/lib/fmt";
import { useMeta } from "@/lib/market";
import { useQuery } from "@/lib/query";
import { tradeApi, type AccountDetail, type Instance, type Report } from "@/lib/tradeApi";
import { useTradeEvents } from "@/lib/tradeLive";
import { amt, day, execLabel, paramsLine, pctf, stateLabel, tone, universeText, type Lbl } from "@/lib/tradeMeta";
import { colors, fs, tnum } from "@/lib/theme";
import { Card, ErrorBox, Loading, PanelTitle, Pull, Screen, Tag, TopBar, confirm, toastError } from "@/components/ui";
import { Banner, InfoTip, KpiGrid, LTag, Muted, SmallButton, mono, type Kpi } from "@/components/trade/common";
import { InstContribution, TradeExplorer, type ExplorerHandle } from "@/components/trade/TradeExplorer";

interface Main { d: AccountDetail; insts: Instance[]; eq: [number, number][] }

export default function BacktestScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const aid = Number(id);
  const router = useRouter();
  const meta = useMeta().data;
  const ruleName = (r: string) => meta?.rules.find((x) => x.id === r)?.name || r;
  const q = useQuery<Main>(`trade:bt:${aid}`, async () => {
    const [d, insts, eq] = await Promise.all([tradeApi.account(aid), tradeApi.instances(aid), tradeApi.equity(aid)]);
    return { d, insts, eq };
  }, { interval: 5_000, staleMs: 3_000 });
  const rep = useQuery<Report>(`trade:report:${aid}`, () => tradeApi.report(aid), { interval: 10_000, staleMs: 5_000 });
  const refresh = () => Promise.all([q.refresh(), rep.refresh()]);
  useTradeEvents(refresh, { accountId: aid });
  const explorer = useRef<ExplorerHandle>(null);
  const [busy, setBusy] = useState(false);
  const [scrollOn, setScrollOn] = useState(true);

  const a = q.data?.d.account;
  const bt = a?.backtest;
  const running = bt?.state === "running";
  // trades change only while the run goes on and once when it ends
  const reloadKey = `${bt?.state ?? ""}:${running ? Math.round((bt?.progress ?? 0) * 20) : 0}`;
  const btTag: Lbl = bt?.state === "running" ? { t: `回测中 ${((bt.progress || 0) * 100).toFixed(0)}%`, c: "warn" }
    : bt?.state === "done" ? { t: "已完成", c: "good" } : bt?.state === "failed" ? { t: bt.error === "已取消" ? "已取消" : "失败", c: "bad" } : { t: "尚未运行", c: "" };
  const ratio = (v: number | null | undefined, d = 2) => (v == null || !isFinite(v) ? "—" : v.toFixed(d));

  const r = rep.data;
  const metrics: Kpi[] = r && a ? (() => {
    const sg = r.signals || {};
    const total = Object.values(sg).reduce((s, n) => s + n, 0);
    return [
      { label: "净收益", value: pctf(r.netPct, 2, true), sub: `${amt(r.net, 2, true)} USDT`, tone: tone(r.net), big: true },
      { label: "最大回撤", value: pctf(-Math.abs(r.maxDrawdown), 2), sub: "自权益高点", tone: r.maxDrawdown ? "down" : "", big: true },
      { label: "夏普", value: ratio(r.sharpe), sub: r.sharpe == null ? "日收益不足 5 天" : ({ daily: "日收益年化" } as Record<string, string>)[r.sharpeBasis] || r.sharpeBasis || "日收益年化", big: true },
      { label: "胜率", value: r.winRate == null ? "—" : pctf(r.winRate, 1), sub: `${r.trades} 笔已平${r.openTrades ? ` · ${r.openTrades} 笔持仓中` : ""}`, big: true },
      { label: "盈亏比", value: ratio(r.profitFactor), sub: r.profitFactor == null && r.trades ? "没有亏损交易" : "总盈利 ÷ 总亏损" },
      { label: "已实现", value: amt(r.realized, 2, true), sub: `未实现 ${amt(r.unrealized, 2, true)}${r.liquidations ? ` · 强平 ${r.liquidations}` : ""}`, tone: tone(r.realized) },
      { label: "手续费", value: amt(-Math.abs(r.fees), 2), sub: `毛收益 ${amt(r.gross, 2, true)}`, tone: r.fees ? "down" : "" },
      { label: "资金费", value: amt(r.funding, 2, true), sub: "USDT，正 = 收到", tone: tone(r.funding) },
      { label: "成交额", value: amt(r.turnover, 0), sub: `换手 ${r.deposit ? (r.turnover / r.deposit).toFixed(1) : "—"} 倍` },
      { label: "信号", value: String(total), sub: `下单 ${sg.ordered ?? 0} · 拒绝 ${sg.rejected ?? 0} · 跳过 ${sg.skipped ?? 0}` },
    ];
  })() : [];
  const contrib = (r?.byInst || []).map((x) => ({ instId: x.instId, net: x.pnl, trades: x.trades, wins: x.wins }));

  const doRun = async (f: () => Promise<unknown>) => {
    setBusy(true);
    void haptic("medium");
    try { await f(); await refresh(); } catch (e) { toastError("操作失败", e); } finally { setBusy(false); }
  };
  const runBt = () => {
    if (bt && bt.state !== "idle") confirm("重新运行", "用相同配置重新运行回测？当前结果会被清空重算。", "重新运行", () => void doRun(() => tradeApi.runBacktest(aid)));
    else void doRun(() => tradeApi.runBacktest(aid));
  };

  return (
    <Screen>
      <TopBar title={a?.name ?? "回测报告"} sub="回测报告" />
      <ScrollView scrollEnabled={scrollOn} contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: 48 }} refreshControl={<Pull onRefresh={refresh} />}>
        {q.error && !q.data ? <ErrorBox text={q.error} onRetry={() => void q.refresh()} /> : null}
        {!a && !q.error ? <Loading text="加载回测" /> : null}
        {a ? (
          <>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
              <Tag text="回测报告" />
              <LTag l={btTag} />
              <InfoTip title="回测报告" items={[
                ["回测方式", "：按历史 K 线重放，信号在下一根 K 线开盘价成交（± 半个点差 + 1 跳），同一根 K 线内止损优先于止盈；手续费、资金费（真实结算记录）都计入。回测不等待人工确认。"],
                ["指标", "：净收益 = 期末权益 − 入金（含未平仓的未实现盈亏）；毛收益 = 净收益 + 手续费 − 资金费。一笔交易 = 同一策略在同一合约从开仓到完全平仓，盈亏 = 已实现 − 手续费；胜率、盈亏比按交易计算，没有交易时为“—”。夏普 = 每日（UTC+8）权益收益的均值 ÷ 标准差 × √365，不足 5 个交易日不计算。"],
                ["注意", "：回测只说明这组参数在这段历史上的表现，不代表未来；参数调得越多，越容易过拟合。用“实时模拟”在样本外观察一段时间再下结论。"],
              ]} />
            </View>
            <Text style={[{ fontSize: fs.sm, color: colors.textFaint }, tnum]}>
              {day(a.from)} → {day(a.to ? a.to - 1 : undefined)} · 入金 {amt(a.deposit, 0)} USDT · 期末权益 <Text style={{ color: colors.text, fontWeight: "600" }}>{amt(a.equity)}</Text>
            </Text>
            <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
              <SmallButton label="信号与委托" icon="list-outline" onPress={() => router.push({ pathname: "/trading/account/[id]", params: { id: String(aid) } })} />
              {running ? <SmallButton label="取消" icon="stop" disabled={busy} onPress={() => void doRun(() => tradeApi.cancelBacktest(aid))} />
                : <SmallButton label={bt?.state === "idle" || !bt ? "开始回测" : "重新运行"} icon={bt?.state === "idle" || !bt ? "play" : "refresh"} disabled={busy} onPress={runBt} />}
              <SmallButton label="用同参数开实时账户" icon="radio-outline" tone="primary" onPress={() => router.push({ pathname: "/trading/new", params: { from: String(aid) } })} />
            </View>

            {running ? (
              <Card style={{ gap: 8 }}>
                <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
                  <Text style={{ fontSize: fs.sm, color: colors.text }}>回测中 <Text style={[{ fontWeight: "700" }, tnum]}>{((bt?.progress || 0) * 100).toFixed(0)}%</Text></Text>
                  <Text style={[{ fontSize: fs.sm, color: colors.textFaint }, tnum]}>已重放到 {bt?.clock ? time(bt.clock, true) : "—"}</Text>
                </View>
                <View style={{ height: 6, borderRadius: 3, backgroundColor: colors.cardMuted, overflow: "hidden" }}>
                  <View style={{ width: `${(bt?.progress || 0) * 100}%`, height: 6, borderRadius: 3, backgroundColor: colors.up }} />
                </View>
              </Card>
            ) : bt?.state === "failed" ? (
              <Banner text={`回测${bt.error === "已取消" ? "已取消" : "失败"}${bt.error && bt.error !== "已取消" ? "：" + bt.error : ""}`} />
            ) : bt?.state === "idle" ? (
              <Card><Muted>尚未运行。点“开始回测”按当前配置重放 {day(a.from)} → {day(a.to ? a.to - 1 : undefined)}。</Muted></Card>
            ) : null}

            {metrics.length ? <KpiGrid items={metrics} /> : rep.error && !running ? <Muted>报告暂不可用：{rep.error}</Muted> : null}

            <Card>
              {(q.data?.eq.length ?? 0) > 1 ? (
                <TradeExplorer ref={explorer} accountId={aid} points={q.data!.eq} deposit={a.deposit} reloadKey={reloadKey} onTouchActive={(on) => setScrollOn(!on)} />
              ) : <Muted>还没有权益数据</Muted>}
            </Card>

            <Card>
              <PanelTitle title="各合约贡献" right={<InfoTip title="各合约贡献" items={["每个合约的盈亏 = 已实现 − 手续费 + 资金费（USDT），条形长度按绝对值比例；胜率 = 盈利交易 ÷ 交易数。点一行在币对视角中查看该合约的全部交易。"]} />} />
              <InstContribution rows={contrib} onOpen={(i) => explorer.current?.openInst(i)} />
            </Card>

            <Card>
              <PanelTitle title="策略配置" />
              {(q.data?.insts ?? []).map((i, k) => (
                <View key={i.id} style={{ gap: 4, paddingVertical: 8, borderTopWidth: k ? 1 : 0, borderColor: colors.border }}>
                  <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
                    <Text style={{ fontWeight: "700", color: colors.text, fontSize: fs.md }}>{i.name}</Text>
                    <Tag text={execLabel[i.exec]?.t ?? i.exec} />
                    <LTag l={stateLabel[i.state]} fallback={i.state} />
                  </View>
                  <Muted>{universeText(i.universe, ruleName)}</Muted>
                  <Text style={[mono, { color: colors.textFaint }]}>{paramsLine(i.params)}</Text>
                </View>
              ))}
              <View style={{ paddingTop: 8, borderTopWidth: 1, borderColor: colors.border, marginTop: 4 }}>
                <Muted>风控：最多 {a.limits.maxPositions} 仓 · {a.limits.maxLeverage}x · 单笔 {pctf(a.limits.riskPct)} · 熔断 {pctf(a.limits.dailyLossPct, 1)} · 费率 {a.limits.takerFeeBp}/{a.limits.makerFeeBp} bp</Muted>
              </View>
            </Card>
          </>
        ) : null}
      </ScrollView>
    </Screen>
  );
}

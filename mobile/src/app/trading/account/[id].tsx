// Account detail (web views/trading/Account.vue): KPIs, equity / trades explorer, positions, open
// orders, order history, fills, ledger, strategy instances (start / pause / resume / stop, params,
// upgrade, run log), signals; deposit, limits (风控), close all, archive.
import { useMemo, useState } from "react";
import { ScrollView, Text, View } from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { haptic } from "@/lib/device";
import { useMeta } from "@/lib/market";
import { useQuery } from "@/lib/query";
import { tradeApi, type AccountDetail, type Fill, type Instance, type InstanceAction, type LedgerEntry, type Limits, type Order, type Signal, type StrategyEntry, type StrategyVersion } from "@/lib/tradeApi";
import { useTradeEvents } from "@/lib/tradeLive";
import { accountStatus, amt, limitErrors, modeLabel, pctf, qty, tone } from "@/lib/tradeMeta";
import { colors, fs } from "@/lib/theme";
import { Card, ErrorBox, IconButton, Loading, PanelTitle, PillButton, Pull, Screen, Segmented, TopBar, confirm, toast, toastError } from "@/components/ui";
import { FieldRow, FormSheet, InfoTip, KpiGrid, LTag, Muted, NumInput, SmallButton, TextField } from "@/components/trade/common";
import { LimitsForm } from "@/components/trade/forms";
import { InstanceCard } from "@/components/trade/InstanceCard";
import { InstanceEditor } from "@/components/trade/InstanceEditor";
import { FillsList, LedgerList, OrdersList, PositionRow, SignalList } from "@/components/trade/rows";
import { TradeExplorer } from "@/components/trade/TradeExplorer";

const DAY = 86_400_000;
type Tab = "pos" | "open" | "orders" | "fills" | "ledger";
interface Main { d: AccountDetail; insts: Instance[]; signals: Signal[] }

export default function AccountScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const aid = Number(id);
  const router = useRouter();
  const meta = useMeta().data;
  const ruleName = (r: string) => meta?.rules.find((x) => x.id === r)?.name || r;
  const q = useQuery<Main>(`trade:account:${aid}`, async () => {
    const [d, insts, signals] = await Promise.all([tradeApi.account(aid), tradeApi.instances(aid), tradeApi.signals(aid, 200)]);
    return { d, insts, signals };
  }, { interval: 10_000, staleMs: 5_000 });
  const [range, setRange] = useState("0"); // days; 0 = all
  const eqKey = `trade:equity:${aid}:${range}`;
  const eq = useQuery<[number, number][]>(eqKey, () => tradeApi.equity(aid, Number(range) ? Date.now() - Number(range) * DAY : undefined), { interval: 60_000, staleMs: 30_000 });
  const [tab, setTab] = useState<Tab>("pos");
  const tabQ = useQuery<Order[] | Fill[] | LedgerEntry[]>(tab === "orders" || tab === "fills" || tab === "ledger" ? `trade:${tab}:${aid}` : null,
    () => (tab === "fills" ? tradeApi.fills(aid, 200) : tab === "orders" ? tradeApi.orders(aid, 200) : tradeApi.ledger(aid, 200)), { staleMs: 5_000 });
  const lib = useQuery<StrategyEntry[]>("trade:strategies", () => tradeApi.strategies(), { staleMs: 60_000 });
  const refreshAll = () => Promise.all([q.refresh(), eq.refresh(), tabQ.refresh()]);
  useTradeEvents(refreshAll, { accountId: aid });

  const a = q.data?.d.account;
  const insts = q.data?.insts;
  const positions = q.data?.d.positions ?? [];
  const isBacktest = a?.mode === "backtest";
  const running = insts?.filter((i) => i.state === "running").length;
  const st = a ? accountStatus(a, running, insts?.filter((i) => i.state === "paused" || i.state === "faulted").length) : null;
  const upnl = positions.reduce((s, p) => s + p.unrealizedPnl, 0);
  // round trips change only with fills: refetch them when positions or ordered signals change
  const tradesKey = a ? `${a.openCount}|${(q.data?.signals ?? []).filter((x) => x.outcome === "ordered").length}|${a.backtest?.state ?? ""}` : "";
  const latest = useMemo(() => Object.fromEntries((lib.data ?? []).map((e) => [e.name, e.latest])), [lib.data]);
  const versionName = useMemo(() => Object.fromEntries((lib.data ?? []).flatMap((e) => e.versions.map((v) => [v.id, e.name]))), [lib.data]);
  const newer = (i: Instance) => { const n = versionName[i.versionId]; const v = n ? latest[n] : undefined; return v && v.id !== i.versionId ? v : undefined; };

  const [busy, setBusy] = useState(false);
  const [scrollOn, setScrollOn] = useState(true);
  const run = async (f: () => Promise<unknown>, ok?: string) => {
    setBusy(true);
    try { await f(); if (ok) toast(ok); await refreshAll(); } catch (e) { toastError("操作失败", e); } finally { setBusy(false); }
  };
  const closeOne = (instId: string, side: string, n: number) => confirm("平仓", `按市价平掉 ${instId} ${side === "long" ? "多" : "空"}仓 ${qty(n)} 张？`, "平仓", () => void run(() => tradeApi.close(aid, instId), "已提交平仓"), true);
  const closeAll = () => confirm("全部平仓", `按市价平掉全部 ${positions.length} 个持仓？`, "全部平仓", () => void run(() => tradeApi.close(aid), "已提交全部平仓"), true);
  const act = (i: Instance, x: InstanceAction) => {
    const go = () => { void haptic("medium"); void run(() => tradeApi.instanceAction(i.id, x), { start: "已启动", pause: "已暂停", resume: "已恢复", stop: "已停止" }[x]); };
    if (x === "stop") confirm("停止策略", `停止「${i.name}」？已有持仓保留，可以手动平仓。`, "停止", go, true);
    else go();
  };
  const upgrade = (i: Instance, v: StrategyVersion) => confirm("升级策略", `把「${i.name}」换成 ${v.name} 的最新版本 ${v.id.slice(0, 8)}？\n参数尽量保留（新版本不接受的值恢复默认），运行中的实例会带着保存的状态重启。`, "升级", () => void run(() => tradeApi.patchInstance(i.id, { versionId: v.id }), "已升级"));
  const archive = (on: boolean) => {
    const go = async () => { await run(() => tradeApi.patchAccount(aid, { archived: on }), on ? "已归档" : "已取消归档"); if (on) router.back(); };
    if (on) confirm("归档", `归档「${a?.name}」？归档前请先停止策略、平掉持仓；归档后账户只读。`, "归档", () => void go(), true);
    else void go();
  };

  const [depOpen, setDepOpen] = useState(false);
  const [setOpen, setSetOpen] = useState(false);
  const [instOpen, setInstOpen] = useState(false);
  const [editInst, setEditInst] = useState<Instance | null>(null);

  const kpis = a ? [
    { label: "权益 USDT", value: amt(a.equity), sub: `入金 ${amt(a.deposit, 0)}`, big: true },
    { label: "累计收益", value: pctf(a.deposit ? (a.equity - a.deposit) / a.deposit : null, 2, true), sub: amt(a.equity - a.deposit, 2, true) + " USDT", tone: tone(a.equity - a.deposit), big: true },
    ...(isBacktest ? [] : [{ label: "今日盈亏", value: amt(a.dayPnl, 2, true), sub: a.halted ? "已熔断，今日停止开仓" : `熔断线 -${pctf(a.limits.dailyLossPct, 1)}`, tone: tone(a.dayPnl) }]),
    { label: "未实现盈亏", value: amt(upnl, 2, true), sub: `${positions.length} 个持仓`, tone: tone(upnl) },
    { label: "可用", value: amt(a.cash), sub: `保证金 ${amt(positions.reduce((s, p) => s + p.margin, 0))}` },
  ] : [];

  return (
    <Screen>
      <TopBar title={a?.name ?? "账户"} sub={a ? modeLabel[a.mode].t : undefined}
        right={a && !a.archived ? <IconButton name="archive-outline" label="归档" onPress={() => archive(true)} /> : undefined} />
      <ScrollView scrollEnabled={scrollOn} contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: 48 }} refreshControl={<Pull onRefresh={refreshAll} />}>
        {q.error && !q.data ? <ErrorBox text={q.error} onRetry={() => void q.refresh()} /> : null}
        {!a && !q.error ? <Loading text="加载账户" /> : null}
        {a ? (
          <>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
              <LTag l={modeLabel[a.mode]} />
              {st ? <LTag l={st} /> : null}
              <InfoTip title="账户" items={[
                ["权益", " = 现金 + 持仓保证金 + 未实现盈亏（按标记价）。"],
                ["可用", " = 不被保证金占用的现金。"],
                ["熔断", "：当日（UTC+8）亏损达到“日亏损熔断”后当天不再接受开仓信号，平仓与止损照常；次日自动解除。"],
                "所有成交都是模拟：实时账户按当时盘口逐档成交（受滑点容忍限制），止损止盈按市价触发。",
              ]} />
            </View>
            <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
              {isBacktest ? <SmallButton label="回测报告" icon="document-text-outline" onPress={() => router.push({ pathname: "/trading/backtest/[id]", params: { id: String(aid) } })} /> : null}
              {!isBacktest && !a.archived ? (
                <>
                  <SmallButton label="入金" icon="download-outline" disabled={busy} onPress={() => setDepOpen(true)} />
                  <SmallButton label="风控" icon="options-outline" disabled={busy} onPress={() => setSetOpen(true)} />
                  <SmallButton label="全部平仓" icon="close-circle-outline" disabled={busy || !positions.length} onPress={closeAll} />
                </>
              ) : null}
              {a.archived ? <SmallButton label="取消归档" icon="archive-outline" disabled={busy} onPress={() => archive(false)} /> : null}
            </View>

            <KpiGrid items={kpis} />

            <Card>
              {(eq.data?.length ?? 0) > 1 ? (
                <TradeExplorer accountId={aid} points={eq.data!} deposit={a.deposit} reloadKey={tradesKey} onTouchActive={(on) => setScrollOn(!on)}
                  tools={!isBacktest ? <Segmented small style={{ width: 200 }} value={range} onChange={setRange} options={[{ value: "1", label: "1D" }, { value: "7", label: "7D" }, { value: "30", label: "30D" }, { value: "0", label: "全部" }]} /> : undefined} />
              ) : eq.data ? <Muted>权益数据点不足，运行一段时间后显示（每分钟一个点）</Muted> : <Loading />}
            </Card>

            <Card>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 6, marginBottom: 6 }}>
                <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ flexShrink: 1 }}>
                  <Segmented small style={{ width: 330 }} value={tab} onChange={setTab} options={[
                    { value: "pos", label: `持仓 ${positions.length}` }, { value: "open", label: `委托 ${q.data?.d.orders.length ?? 0}` },
                    { value: "orders", label: "历史" }, { value: "fills", label: "成交" }, { value: "ledger", label: "流水" }]} />
                </ScrollView>
                <InfoTip title="明细" items={[
                  ["持仓", "：逐仓。强平价按 0.5% 维持保证金率估算；标记价穿过强平价时整仓按强平价平掉，保证金损失。"],
                  ["委托", "：未完成的委托，包括等待触发的止损、止盈（OCO：一个成交另一个自动撤销）。"],
                  ["流水", "：入金、手续费、资金费、已实现盈亏、强平，每一笔资金变动。"],
                ]} />
              </View>
              {tab === "pos" ? (
                positions.length ? positions.map((p) => (
                  <PositionRow key={p.instId + p.posSide + p.instanceId} p={p} insts={insts} busy={busy}
                    onClose={!isBacktest && !a.archived ? () => closeOne(p.instId, p.posSide, p.qty) : undefined} />
                )) : <Muted style={{ paddingVertical: 8 }}>没有持仓</Muted>
              ) : tab === "open" ? (
                <OrdersList orders={q.data?.d.orders ?? []} empty="没有未完成的委托" />
              ) : tab === "orders" ? (
                <OrdersList orders={(tabQ.data as Order[] | undefined) ?? null} />
              ) : tab === "fills" ? (
                <FillsList fills={(tabQ.data as Fill[] | undefined) ?? null} insts={insts} />
              ) : (
                <LedgerList ledger={(tabQ.data as LedgerEntry[] | undefined) ?? null} />
              )}
            </Card>

            <Card>
              <PanelTitle title="策略实例" right={
                <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                  <InfoTip title="策略实例" items={[
                    ["状态", "：未启动 / 运行中 / 已暂停（不处理新 K 线，持仓与止损照常）/ 故障（策略进程超时或连续出错，自动重启 3 次后停止）/ 已停止。"],
                    ["修改参数", "会生成新的配置版本（v2、v3…），之后的信号记新版本，历史信号保留原版本，便于对比。"],
                    "停止策略不会平仓；需要时请手动平仓。",
                  ]} />
                  {!isBacktest && !a.archived ? <SmallButton label="添加" icon="add" onPress={() => { setEditInst(null); setInstOpen(true); }} /> : null}
                </View>
              } />
              {(insts ?? []).map((i, k) => (
                <InstanceCard key={i.id} inst={i} first={k === 0} busy={busy} readonly={isBacktest || a.archived} newer={newer(i)} ruleName={ruleName}
                  onAct={(x) => act(i, x)} onEdit={() => { setEditInst(i); setInstOpen(true); }} onUpgrade={(v) => upgrade(i, v)}
                  onViewCode={(v) => router.push({ pathname: "/trading/strategy", params: { version: v } })} />
              ))}
              {insts && !insts.length ? <Muted>还没有策略。点“添加”把策略挂到这个账户。</Muted> : null}
            </Card>

            <Card>
              <PanelTitle title="信号" right={<InfoTip title="信号" items={[
                "策略发出的每个信号和它的去向：已下单、风控拒绝（原因见“结果”）、AI 跳过、用户跳过（人工确认时选了跳过）、过期（等待确认或 AI 超过有效期）、只记录（只记录模式）。",
                "时间是信号对应 K 线的收盘时间（交易所时间）。",
              ]} />} />
              <SignalList signals={q.data?.signals ?? null} insts={insts} />
            </Card>

            <DepositSheet open={depOpen} onClose={() => setDepOpen(false)} busy={busy} onDeposit={(n) => { setDepOpen(false); void run(() => tradeApi.deposit(aid, n), `已入金 ${amt(n, 0)} USDT`); }} />
            <SettingsSheet open={setOpen} onClose={() => setSetOpen(false)} name={a.name} limits={a.limits}
              onSave={async (name, limits) => { await tradeApi.patchAccount(aid, { name, limits }); toast("已保存"); setSetOpen(false); await q.refresh(); }} />
            <InstanceEditor open={instOpen} onClose={() => setInstOpen(false)} accountId={aid} inst={editInst} startOnCreate onSaved={() => { setInstOpen(false); void q.refresh(); }} />
          </>
        ) : null}
      </ScrollView>
    </Screen>
  );
}

function DepositSheet({ open, onClose, onDeposit, busy }: { open: boolean; onClose: () => void; onDeposit: (n: number) => void; busy: boolean }) {
  const [n, setN] = useState(1000);
  const err = n > 0 && n <= 1e9 ? "" : "请输入正数";
  return (
    <FormSheet visible={open} onClose={onClose} title="入金" footer={<PillButton label="确认入金" disabled={!!err || busy} onPress={() => onDeposit(n)} />}>
      <Muted style={{ marginBottom: 12 }}>模拟入金，立即计入可用资金和累计入金（收益率按累计入金计算）。</Muted>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <Segmented small value={String(n)} onChange={(v) => setN(Number(v))} options={[1000, 5000, 10000].map((v) => ({ value: String(v), label: amt(v, 0) }))} />
        <NumInput value={n} onChange={setN} unit="USDT" width={130} />
      </View>
      {err ? <Text style={{ color: colors.down, fontSize: fs.sm, marginTop: 6 }}>{err}</Text> : null}
    </FormSheet>
  );
}

/** Rename and edit hard limits (web AccountSettingsModal). */
function SettingsSheet({ open, onClose, name: name0, limits: limits0, onSave }: { open: boolean; onClose: () => void; name: string; limits: Limits; onSave: (name: string, l: Limits) => Promise<void> }) {
  const [name, setName] = useState(name0);
  const [limits, setLimits] = useState<Limits>({ ...limits0 });
  const [err, setErr] = useState("");
  const [saving, setSaving] = useState(false);
  const [wasOpen, setWasOpen] = useState(false);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) { setName(name0); setLimits({ ...limits0 }); setErr(""); }
  }
  const invalid = !name.trim() || Object.keys(limitErrors(limits)).length > 0;
  const changed = name.trim() !== name0 || JSON.stringify(limits) !== JSON.stringify(limits0);
  const save = async () => {
    if (invalid) return;
    setSaving(true);
    try { await onSave(name.trim(), limits); } catch (e) { setErr(e instanceof Error ? e.message : String(e)); } finally { setSaving(false); }
  };
  return (
    <FormSheet visible={open} onClose={onClose} title="账户设置"
      footer={
        <View style={{ gap: 8 }}>
          <Text style={{ fontSize: fs.sm, color: err ? colors.down : colors.textFaint }}>{err || "修改从下一个信号开始生效，不影响已有持仓"}</Text>
          <PillButton label={saving ? "保存中…" : "保存"} loading={saving} disabled={saving || invalid || !changed} onPress={() => void save()} />
        </View>
      }>
      <FieldRow label="账户名称" changed={name.trim() !== name0} error={name.trim() ? "" : "请填写名称"} stack>
        <View style={{ alignSelf: "stretch" }}><TextField value={name} onChange={setName} /></View>
      </FieldRow>
      <LimitsForm l={limits} onChange={setLimits} base={limits0} />
    </FormSheet>
  );
}

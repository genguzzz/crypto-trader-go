// 交易 → 账户 (web views/trading/Accounts.vue): totals of the live accounts, then live and backtest
// account cards; archived ones behind a chip. Running-instance counts per live account.
import { useState } from "react";
import { ScrollView, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { useQuery } from "@/lib/query";
import { tradeApi, type Account } from "@/lib/tradeApi";
import { useTradeEvents } from "@/lib/tradeLive";
import { amt, tone } from "@/lib/tradeMeta";
import { colors, fs, tnum } from "@/lib/theme";
import { Card, Chip, EmptyState, ErrorBox, Loading, PillButton, Pull, TAB_BAR_SPACE, Tag } from "../ui";
import { InfoTip, toneText } from "./common";
import { AccountCard } from "./rows";

interface AccountsData { list: Account[]; running: Record<number, number>; paused: Record<number, number> }

async function fetchAccounts(): Promise<AccountsData> {
  const list = await tradeApi.accounts();
  const live = list.filter((a) => a.mode === "live" && !a.archived);
  const lists = await Promise.all(live.map((a) => tradeApi.instances(a.id).catch(() => undefined)));
  const running: Record<number, number> = {}, paused: Record<number, number> = {};
  live.forEach((a, i) => {
    const xs = lists[i];
    if (!xs) return;
    running[a.id] = xs.filter((x) => x.state === "running").length;
    paused[a.id] = xs.filter((x) => x.state === "paused" || x.state === "faulted").length;
  });
  return { list, running, paused };
}

export function AccountsPane() {
  const router = useRouter();
  const q = useQuery<AccountsData>("trade:accounts", fetchAccounts, { interval: 15_000, staleMs: 8_000 });
  useTradeEvents(q.refresh);
  const [showArchived, setShowArchived] = useState(false);
  const all = q.data?.list ?? [];
  const visible = all.filter((a) => showArchived || !a.archived);
  const live = visible.filter((a) => a.mode !== "backtest");
  const backtests = visible.filter((a) => a.mode === "backtest");
  const nArchived = all.filter((a) => a.archived).length;
  const liveAll = all.filter((a) => a.mode === "live" && !a.archived);
  const sum = { equity: liveAll.reduce((s, a) => s + a.equity, 0), day: liveAll.reduce((s, a) => s + a.dayPnl, 0), n: liveAll.length, halted: liveAll.filter((a) => a.halted).length };
  const open = (a: Account) => router.push(a.mode === "backtest" ? { pathname: "/trading/backtest/[id]", params: { id: String(a.id) } } : { pathname: "/trading/account/[id]", params: { id: String(a.id) } });
  const section = (t: string, n: number) => (
    <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text, marginTop: 6, marginHorizontal: 2 }}>{t} <Text style={{ fontSize: fs.sm, fontWeight: "400", color: colors.textFaint }}>{n}</Text></Text>
  );

  return (
    <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: TAB_BAR_SPACE }} refreshControl={<Pull onRefresh={q.refresh} />}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Text style={{ fontSize: fs.xl, fontWeight: "600", color: colors.text }}>交易账户</Text>
        <InfoTip title="交易账户" items={[
          ["模拟交易", "：所有账户都是模拟盘，不会向 OKX 下任何真实订单，也不读取你的 OKX 账户。成交按真实盘口（实时）或 K 线（回测）模拟，计入手续费、滑点和资金费。"],
          ["账户", "：一笔入金 + 一组硬性风控规则；策略挂在账户下运行，多个策略共享账户的资金和风控。"],
          ["实时模拟", "：用实时行情持续运行。"],
          ["历史回测", "：在选定日期区间内按历史 K 线快速重放，结束后生成回测报告。"],
          ["熔断", "：当日亏损达到上限后当天停止开仓，次日（UTC+8）自动恢复。"],
        ]} />
        <View style={{ flex: 1 }} />
        {nArchived ? <Chip label="已归档" icon="archive-outline" count={nArchived} active={showArchived} onPress={() => setShowArchived((v) => !v)} /> : null}
      </View>
      {q.error && !q.data ? <ErrorBox text={q.error} onRetry={() => void q.refresh()} /> : null}
      {!q.data && !q.error ? <Loading text="加载账户" /> : null}
      {q.data ? (
        <>
          {sum.n ? (
            <Card style={{ flexDirection: "row", gap: 20, flexWrap: "wrap" }}>
              <View style={{ gap: 2 }}>
                <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>实时账户总权益</Text>
                <Text style={[{ fontSize: fs.x2, fontWeight: "700", color: colors.text }, tnum]}>{amt(sum.equity)} <Text style={{ fontSize: fs.xs, color: colors.textFaint, fontWeight: "500" }}>USDT</Text></Text>
              </View>
              <View style={{ gap: 2 }}>
                <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>今日盈亏</Text>
                <Text style={[{ fontSize: fs.xl, fontWeight: "600", color: toneText(tone(sum.day)), lineHeight: 26 }, tnum]}>{amt(sum.day, 2, true)}</Text>
              </View>
              <View style={{ gap: 2 }}>
                <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>账户</Text>
                <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                  <Text style={[{ fontSize: fs.xl, fontWeight: "600", color: colors.textSoft, lineHeight: 26 }, tnum]}>{sum.n}</Text>
                  {sum.halted ? <Tag text={`${sum.halted} 熔断`} tone="bad" /> : null}
                </View>
              </View>
            </Card>
          ) : null}
          {!visible.length ? (
            <Card>
              <EmptyState icon="wallet-outline" text={"还没有交易账户\n先建账户（入金与风控），再挂上策略；也可以先做一次历史回测。"}
                action={<PillButton small icon="add" label="新建账户" onPress={() => router.push("/trading/new")} />} />
            </Card>
          ) : null}
          {live.length ? section("实时模拟", live.length) : null}
          {live.map((a) => <AccountCard key={a.id} a={a} running={q.data!.running[a.id]} paused={q.data!.paused[a.id]} onPress={() => open(a)} />)}
          {backtests.length ? section("历史回测", backtests.length) : null}
          {backtests.map((a) => <AccountCard key={a.id} a={a} onPress={() => open(a)} />)}
        </>
      ) : null}
    </ScrollView>
  );
}

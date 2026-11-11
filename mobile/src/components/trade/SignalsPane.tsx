// 交易 → 信号: the AI's (and the owner's) signal subscriptions (lib/signals.ts). 活跃 sorts by how close
// each is to firing (or groups by coin); 已触发 / 已结束 are the history. Each row is a SignalCard:
// the subscriber opens that chat in the AI sheet, 查看 opens the detail sheet (K-line with the levels,
// intent, fire history), 取消 asks first. + opens 新建信号 (NewSignalSheet) for the owner's own subscription.
import { useMemo, useState } from "react";
import { ScrollView, Text, View } from "react-native";
import { Card, Chip, EmptyState, PillButton, Loading, Pull, Segmented, TAB_BAR_SPACE } from "../ui";
import { InfoTip } from "./common";
import { NewSignalSheet } from "./NewSignalSheet";
import { SignalCard, SignalSheet } from "../ai/SignalCard";
import { refreshSignals, useSignalList, useSignals, type Signal, type SignalFilter } from "@/lib/signals";
import { sym } from "@/lib/fmt";
import { colors, fs, tnum } from "@/lib/theme";

const FILTERS: { value: SignalFilter; label: string }[] = [
  { value: "active", label: "活跃" },
  { value: "fired", label: "已触发" },
  { value: "ended", label: "已结束" },
];

/** Closest first; without a distance (time, field, alert…) after those, then newest. */
const byCloseness = (a: Signal, b: Signal) => {
  const pa = a.progress && a.progress.pct >= 0 ? a.progress.pct : Infinity;
  const pb = b.progress && b.progress.pct >= 0 ? b.progress.pct : Infinity;
  return pa === pb ? b.id - a.id : pa - pb;
};

export function SignalsPane() {
  const owner = useSignals((s) => s.owner);
  const caps = useSignals((s) => s.caps);
  const [filter, setFilter] = useState<SignalFilter>("active");
  const [group, setGroup] = useState<"close" | "coin">("close");
  const [open, setOpen] = useState(0);
  const [creating, setCreating] = useState(false);
  const list = useSignalList(filter);
  const counts: Record<SignalFilter, number | undefined> = {
    active: useSignals((s) => s.lists.active?.length),
    fired: useSignals((s) => s.lists.fired?.length),
    ended: useSignals((s) => s.lists.ended?.length),
  };

  const sections = useMemo(() => {
    const xs = (list ?? []).slice();
    if (filter !== "active") return [{ key: "", items: xs }];
    xs.sort(byCloseness);
    if (group === "close") return [{ key: "", items: xs }];
    const m = new Map<string, Signal[]>();
    for (const s of xs) {
      const k = s.instIds[0] ?? "";
      m.set(k, [...(m.get(k) ?? []), s]);
    }
    return [...m.entries()].map(([key, items]) => ({ key, items }));
  }, [list, filter, group]);

  return (
    <ScrollView contentContainerStyle={{ padding: 12, gap: 10, paddingBottom: TAB_BAR_SPACE }} refreshControl={<Pull onRefresh={refreshSignals} />}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Text style={{ fontSize: fs.xl, fontWeight: "600", color: colors.text }}>信号订阅</Text>
        <InfoTip title="信号订阅" items={[
          "AI 觉得机会值得等、但条件还没到（没到入场区、没拐头、资金费没转正……）时，会订阅一个条件，而不是让你之后再来问。",
          "条件在服务器上随行情实时检查：价格上破/跌破（可选 K 线收盘确认）、进入区间、因子字段越过阈值、延续风险 P_tail、告警规则触发、到点复查、AI 计划进入入场区，以及它们的组合。",
          "条件从不成立变为成立时触发：在订阅它的那个对话里追加一条 ⚡ 信号消息并唤醒 AI，AI 用最新数据重新评估，可能提交交易提议（仍需你审核）、调整后重新订阅，或说明放弃。",
          "对话正在回答时，唤醒会排队到这轮结束。触发时交易机器人会推送一条通知。",
          caps ? `上限：同时 ${caps.maxActive} 个（每个对话 ${caps.perChat} 个），最长 ${caps.maxDays} 天；每天最多唤醒 AI ${caps.wakesPerDay} 次（今天已用 ${caps.wakesToday}）。` : "",
        ].filter(Boolean)} />
        <View style={{ flex: 1 }} />
        {owner ? <PillButton small icon="add" label="新建" onPress={() => setCreating(true)} /> : null}
      </View>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        {FILTERS.map((f) => <Chip key={f.value} label={f.label} active={filter === f.value} count={counts[f.value]} onPress={() => setFilter(f.value)} />)}
        <View style={{ flex: 1 }} />
        {filter === "active" && (list?.length ?? 0) > 1 ? (
          <Segmented small fit value={group} onChange={setGroup} options={[{ value: "close", label: "接近度" }, { value: "coin", label: "币种" }]} />
        ) : null}
      </View>
      {!owner && list === undefined ? <Loading text="加载信号" /> : null}
      {list && !list.length ? (
        <Card>
          <EmptyState icon="flash-outline" text={filter === "active" ? "没有在等待的信号。AI 在条件未到时会自己订阅，也可以在对话里让它“到了再叫我”，或点右上角 + 自己订阅。" : filter === "fired" ? "还没有触发过的信号" : "没有已结束的信号"} />
        </Card>
      ) : null}
      {sections.map((sec) => (
        <View key={sec.key || "all"} style={{ gap: 0 }}>
          {sec.key ? (
            <Text style={[{ marginTop: 4, marginBottom: 2, fontSize: fs.sm, fontWeight: "600", color: colors.textSoft }, tnum]}>{sym(sec.key)} · {sec.items.length}</Text>
          ) : null}
          {sec.items.map((s) => <SignalCard key={s.id} s={s} showSubscriber onOpen={() => setOpen(s.id)} />)}
        </View>
      ))}
      <SignalSheet id={open} onClose={() => setOpen(0)} />
      <NewSignalSheet visible={creating} onClose={() => setCreating(false)} />
    </ScrollView>
  );
}

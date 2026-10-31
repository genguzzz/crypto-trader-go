// Top / bottom exhaustion (server /api/v1/exhaustion = the agent's `rev` checklist): a compact meter for
// the price card and the full card — both checklists with the points each sign earned, the bar size,
// and the fade plan the stronger side points to. A heuristic checklist, not a validated model.
import { useState } from "react";
import { Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { api, type Exhaustion, type ExhaustionItem } from "@/lib/api";
import { price } from "@/lib/fmt";
import { useQuery } from "@/lib/query";
import { colors, fs, tnum } from "@/lib/theme";
import { Segmented, styles } from "./ui";

export const exhVerdict = (s: number) => (s >= 70 ? "强衰竭" : s >= 50 ? "有衰竭迹象" : s >= 30 ? "弱" : "无");
const tone = (s: number) => (s >= 70 ? colors.down : s >= 50 ? colors.warn : s >= 30 ? colors.textSoft : colors.textDim);

export function useExhaustion(id: string, bar = "1H") {
  return useQuery<Exhaustion>(`exh:${id}:${bar}`, () => api.exhaustion(id, bar), { interval: 15_000, staleMs: 12_000 });
}

/** Five-segment meter, lit up to the score. */
function Segs({ v, color }: { v: number; color: string }) {
  return (
    <View style={{ flexDirection: "row", gap: 2 }}>
      {[0, 1, 2, 3, 4].map((i) => (
        <View key={i} style={{ width: 9, height: 6, borderRadius: 1.5, backgroundColor: v >= (i + 1) * 20 - 10 ? color : colors.cardMuted }} />
      ))}
    </View>
  );
}

/** The price card's line: the stronger side's score, its meter and verdict; tap for the card. */
export function ExhaustionMeter({ d, onPress }: { d?: Exhaustion; onPress: () => void }) {
  const topSide = !d || d.top >= d.bottom;
  const v = d ? Math.max(d.top, d.bottom) : 0;
  const c = tone(v);
  return (
    <Pressable onPress={onPress} hitSlop={6} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 6, opacity: pressed ? 0.7 : 1 })}>
      <Ionicons name={topSide ? "trending-down" : "trending-up"} size={13} color={c} />
      <Text style={[{ fontSize: fs.sm, color: colors.textFaint, flexShrink: 0 }, tnum]} numberOfLines={1}>
        {d ? (topSide ? "顶部衰竭 " : "底部衰竭 ") : "衰竭 "}
        <Text style={{ fontWeight: "700", color: c }}>{d ? v : "—"}</Text>
      </Text>
      {d ? <Segs v={v} color={c} /> : null}
      {d?.squeeze ? <Text style={{ fontSize: fs.xs, fontWeight: "700", color: colors.warn }} numberOfLines={1}>轧空风险</Text> : null}
    </Pressable>
  );
}

function Side({ title, score, gate, items, top }: { title: string; score: number; gate: number; items: ExhaustionItem[]; top: boolean }) {
  const c = tone(score);
  return (
    <View style={{ marginBottom: 14 }}>
      <View style={{ flexDirection: "row", alignItems: "baseline", gap: 10, paddingBottom: 6, marginBottom: 4, borderBottomWidth: 1, borderColor: colors.border }}>
        <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>{title}</Text>
        <Text style={[{ fontSize: 28, fontWeight: "700", color: c }, tnum]}>{score}</Text>
        <Text style={{ fontSize: fs.sm, color: c }}>{exhVerdict(score)}</Text>
        <View style={{ flex: 1 }} />
        <Text style={[{ fontSize: fs.xs, color: gate < 1 ? colors.warn : colors.textFaint }, tnum]}>{top ? "近 30 根高位" : "近 30 根低位"} {(gate * 100).toFixed(0)}%</Text>
      </View>
      {items.map((it) => {
        const f = it.weight ? it.points / it.weight : 0;
        return (
          <View key={it.key} style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 4 }}>
            <Ionicons name={f >= 0.5 ? "checkmark-circle" : f > 0 ? "ellipse-outline" : "close"} size={13} color={f >= 0.5 ? c : colors.textDim} />
            <Text style={[{ flex: 1, fontSize: fs.sm, color: f > 0 ? colors.text : colors.textFaint }, tnum]} numberOfLines={2}>{it.label}</Text>
            <View style={{ width: 44, height: 4, borderRadius: 2, backgroundColor: colors.cardMuted, overflow: "hidden" }}>
              <View style={{ height: 4, width: `${f * 100}%`, backgroundColor: c }} />
            </View>
            <Text style={[{ width: 40, textAlign: "right", fontSize: fs.xs, color: colors.textFaint }, tnum]}>{it.points.toFixed(0)}/{it.weight}</Text>
          </View>
        );
      })}
    </View>
  );
}

/** The full card: bar switch, the stronger side first, then the other, then the fade plan. */
export function ExhaustionDetail({ id }: { id: string }) {
  const [bar, setBar] = useState("1H");
  const { data: d, error } = useExhaustion(id, bar);
  const sides = d
    ? [
        { title: "顶部衰竭", score: d.top, gate: d.atTop, items: d.topItems, top: true },
        { title: "底部衰竭", score: d.bottom, gate: d.atBottom, items: d.bottomItems, top: false },
      ].sort((a, b) => b.score - a.score)
    : [];
  const p = d?.plan;
  return (
    <View>
      <View style={{ flexDirection: "row", alignItems: "center", marginBottom: 12 }}>
        <Text style={{ flex: 1, fontSize: fs.xs, color: colors.textFaint }}>快到顶 / 到底的信号清单，按 K 线周期</Text>
        <Segmented small value={bar} onChange={setBar} options={["15m", "1H", "4H"].map((v) => ({ value: v, label: v }))} />
      </View>
      {!d ? <Text style={styles.note}>{error || "计算中…"}</Text> : null}
      {d?.squeeze ? (
        <View style={{ flexDirection: "row", gap: 8, padding: 10, marginBottom: 12, borderRadius: 10, backgroundColor: colors.warnBg, borderWidth: 1, borderColor: colors.warn }}>
          <Ionicons name="warning" size={16} color={colors.warn} />
          <View style={{ flex: 1, gap: 2 }}>
            <Text style={{ fontSize: fs.sm, fontWeight: "700", color: colors.warn }}>轧空风险，不宜做空</Text>
            <Text style={{ fontSize: fs.xs, lineHeight: 17, color: colors.textSoft }}>{d.squeezeWhy}。半年 OKX 山寨暴涨回测：这种状态 24 小时内再涨 20% 的概率 43%（全部暴涨状态 29%），做空还要持续付资金费。</Text>
          </View>
        </View>
      ) : null}
      {sides.map((s) => <Side key={s.title} {...s} />)}
      {d && p && Math.max(d.top, d.bottom) >= 30 ? (
        <View style={{ padding: 12, borderRadius: 10, backgroundColor: colors.cardMuted, gap: 4 }}>
          <Text style={{ fontSize: fs.sm, fontWeight: "600", color: p.side === "short" ? colors.down : colors.up }}>{p.side === "short" ? "反手做空参考" : "反手做多参考"}</Text>
          <Text style={[{ fontSize: fs.sm, color: colors.text }, tnum]}>入场 {price(p.entryLo)}–{price(p.entryHi)} · 止损 {price(p.stop)}（极值外 0.5 ATR）</Text>
          <Text style={[{ fontSize: fs.sm, color: colors.text }, tnum]}>目标 {price(p.tp1)}（EMA20）/ {price(p.tp2)}（回撤 38.2%） · 盈亏比 {p.rr.toFixed(1)}</Text>
          <Text style={styles.note}>只在衰竭开始兑现（上影/回落、量能见顶、主动流反转）时考虑；没有这些迹象不要逆势。</Text>
        </View>
      ) : null}
      <Text style={[styles.note, { marginTop: 10 }]}>
        注意：这是经验清单，权重和阈值是人为设定的。半年 OKX 数据里，影线、RSI、主动流等单项预测涨跌方向的 AUC 都约 0.5（接近抛硬币），它们更多说明“会大幅波动”，而不是往哪边走；目前唯一有回测支持的是上面的轧空否决。正在用历史数据重新验证和优化。{"\n"}每项按区间映射成 0–1 再乘权重：偏离 EMA20（ATR）、本轮涨跌幅、量能爆量、影线或回落、RSI 与背离、被挤方向的爆仓、资金费分位、近 3 根主动流；总分再乘“是否处在近 30 根的高/低位”。与 AI 分析里的 rev 是同一套清单，属于经验规则，未经回测验证。
      </Text>
    </View>
  );
}

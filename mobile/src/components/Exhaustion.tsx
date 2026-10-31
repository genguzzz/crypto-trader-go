// Move risk after an extended move (server /api/v1/exhaustion → risk, internal/market/moverisk.go).
// A 2-year OKX 1H out-of-sample study found no top/bottom signal that predicts direction (the old
// exhaustion checklist included, AUC ≈ 0.5); what did hold up are two probabilities, shown here:
//   延续风险 P_tail — price runs ≥ 2 ATR past the extreme within 24h (a fade's stop is hit);
//   波动幅度 P_big3 — a ≥ 3 ATR move either way within 24h (size, not direction).
// The checklist stays in the card as a description of the move, labelled as having no direction power.
import { useState } from "react";
import { Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { api, type Exhaustion, type ExhaustionItem, type MoveRiskSide } from "@/lib/api";
import { price } from "@/lib/fmt";
import { useQuery } from "@/lib/query";
import { colors, fs, tnum } from "@/lib/theme";
import { Segmented, styles, tap } from "./ui";

const FADE_BASE = "延伸状态里逆势单历史平均每笔约 −0.2%（含成本），59% 被止损";

export function useExhaustion(id: string, bar = "1H") {
  return useQuery<Exhaustion>(`exh:${id}:${bar}`, () => api.exhaustion(id, bar), { interval: 15_000, staleMs: 12_000 });
}

/** The side in an extended state (larger extension if both). */
export function extendedSide(d?: Exhaustion): { name: "顶" | "底"; s: MoveRiskSide } | null {
  const r = d?.risk;
  if (!r) return null;
  const xs: { name: "顶" | "底"; s: MoveRiskSide }[] = [];
  if (r.top.inState) xs.push({ name: "顶", s: r.top });
  if (r.bottom.inState) xs.push({ name: "底", s: r.bottom });
  if (!xs.length) return null;
  return xs.reduce((a, b) => (b.s.ext > a.s.ext ? b : a));
}

export const tailTone = (p: number) => (p >= 0.5 ? colors.down : p < 0.25 ? colors.up : colors.warn);
const tailWord = (p: number) => (p >= 0.5 ? "高" : p < 0.25 ? "低" : "中");
export const pc = (p: number) => `${Math.round(p * 100)}%`;

/** The price card's line: extended or not; if extended, continuation risk and move size. */
export const exhVerdict = (v: number) => (v >= 70 ? "强衰竭" : v >= 50 ? "有衰竭迹象" : v >= 30 ? "弱" : "无");
const exhTone = (v: number) => (v >= 70 ? colors.down : v >= 50 ? colors.warn : v >= 30 ? colors.textSoft : colors.textDim);

/** Five-segment meter, lit up to the score. */
function Segs({ v, color }: { v: number; color: string }) {
  return (
    <View style={{ flexDirection: "row", gap: 2 }}>
      {[0, 1, 2, 3, 4].map((i) => (
        <View key={i} style={{ width: 8, height: 6, borderRadius: 1.5, backgroundColor: v >= (i + 1) * 20 - 10 ? color : colors.cardMuted }} />
      ))}
    </View>
  );
}

/** The price card's line: the exhaustion checklist (how stretched / climactic the move looks — a
 *  description) next to the validated continuation risk when the move is extended. */
export function ExhaustionMeter({ d, onPress }: { d?: Exhaustion; onPress: () => void }) {
  const x = extendedSide(d);
  const top = !d || d.top >= d.bottom;
  const v = d ? Math.max(d.top, d.bottom) : 0;
  const c = exhTone(v);
  return (
    <Pressable onPress={onPress} hitSlop={6} style={({ pressed }) => ({ gap: 3, opacity: pressed ? 0.7 : 1 })}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Text style={[{ fontSize: fs.sm, color: colors.textFaint }, tnum]} numberOfLines={1}>
          {d ? (top ? "顶部衰竭 " : "底部衰竭 ") : "衰竭 "}<Text style={{ fontWeight: "700", color: c }}>{d ? v : "—"}</Text>
        </Text>
        {d ? <Segs v={v} color={c} /> : null}
        {d?.squeeze ? <Ionicons name="warning" size={13} color={colors.warn} accessibilityLabel="轧空否决：不做空" /> : null}
      </View>
      {d ? (
        <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]} numberOfLines={1}>
          {x ? (
            <>延续风险 <Text style={{ fontWeight: "700", color: tailTone(x.s.pTail) }}>{pc(x.s.pTail)}</Text> · 波动 <Text style={{ fontWeight: "700", color: colors.textSoft }}>{pc(x.s.pBig3)}</Text></>
          ) : "未处于延伸状态"}
        </Text>
      ) : null}
    </Pressable>
  );
}

/** One probability against its base rate: the value, a bar with the base marked. */
function Prob({ title, p, base, color, desc }: { title: string; p: number; base: number; color: string; desc: string }) {
  return (
    <View style={{ flex: 1, padding: 10, borderRadius: 10, backgroundColor: colors.cardMuted, gap: 4 }}>
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{title}</Text>
      <Text style={[{ fontSize: 26, fontWeight: "700", color }, tnum]}>{pc(p)}</Text>
      <View style={{ height: 5, borderRadius: 3, backgroundColor: colors.card }}>
        <View style={{ height: 5, borderRadius: 3, width: `${p * 100}%`, backgroundColor: color }} />
        <View style={{ position: "absolute", left: `${base * 100}%`, top: -2, width: 2, height: 9, backgroundColor: colors.text }} />
      </View>
      <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>基准 {pc(base)} · {desc}</Text>
    </View>
  );
}

function Checklist({ title, score, items }: { title: string; score: number; items: ExhaustionItem[] }) {
  return (
    <View style={{ marginBottom: 10 }}>
      <Text style={{ fontSize: fs.sm, color: colors.textSoft, marginBottom: 4 }}>{title} · 清单分 {score}/100</Text>
      {items.map((it) => {
        const f = it.weight ? it.points / it.weight : 0;
        return (
          <View key={it.key} style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 3 }}>
            <Ionicons name={f >= 0.5 ? "checkmark" : f > 0 ? "ellipse-outline" : "close"} size={12} color={f > 0 ? colors.textSoft : colors.textDim} />
            <Text style={[{ flex: 1, fontSize: fs.sm, color: f > 0 ? colors.textSoft : colors.textFaint }, tnum]} numberOfLines={2}>{it.label}</Text>
          </View>
        );
      })}
    </View>
  );
}

/** The full card. */
export function ExhaustionDetail({ id }: { id: string }) {
  const { data: d, error } = useExhaustion(id, "1H");
  const [bar, setBar] = useState("1H");
  const alt = useExhaustion(id, bar);
  const desc = bar === "1H" ? d : alt.data;
  const [showList, setShowList] = useState(true);
  const x = extendedSide(d);
  const p = d?.plan;
  return (
    <View>
      {!d ? <Text style={styles.note}>{error || "计算中…"}</Text> : null}
      {d?.squeeze ? (
        <View style={{ flexDirection: "row", gap: 8, padding: 10, marginBottom: 12, borderRadius: 10, backgroundColor: colors.warnBg, borderWidth: 1, borderColor: colors.warn }}>
          <Ionicons name="warning" size={16} color={colors.warn} />
          <View style={{ flex: 1, gap: 2 }}>
            <Text style={{ fontSize: fs.sm, fontWeight: "700", color: colors.warn }}>轧空否决：不做空</Text>
            <Text style={{ fontSize: fs.xs, lineHeight: 17, color: colors.textSoft }}>{d.squeezeWhy}。做空要持续付负资金费；回测里暴涨中逆势空单平均每笔 −0.72%（未命中时 0.00%）。</Text>
          </View>
        </View>
      ) : null}

      {d && x ? (
        <>
          <Text style={{ fontSize: fs.sm, color: colors.text, marginBottom: 8 }}>
            {x.name}部延伸 <Text style={[{ fontWeight: "700" }, tnum]}>{x.s.ext.toFixed(1)} ATR</Text>
            <Text style={{ color: colors.textFaint }}> · 极值 {price(x.s.extreme)} · 1H</Text>
          </Text>
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Prob title={`延续风险（${tailWord(x.s.pTail)}）`} p={x.s.pTail} base={x.s.baseTail} color={tailTone(x.s.pTail)} desc="24h 内再越过极值 2ATR" />
            <Prob title="波动幅度" p={x.s.pBig3} base={x.s.baseBig3} color={colors.text} desc="24h 内任一方向 3ATR" />
          </View>
          <Text style={[styles.note, { marginTop: 8 }]}>
            延续风险 = 在这里逆势挂单，止损（极值外）被扫的概率：≥ 50% 不要逆势，低于 25% 才值得考虑。波动幅度只说明会不会大动，不说明方向：高就缩小仓位、放宽止损。
          </Text>
        </>
      ) : d ? (
        <View style={{ padding: 12, borderRadius: 10, backgroundColor: colors.cardMuted, gap: 4 }}>
          <Text style={{ fontSize: fs.sm, fontWeight: "600", color: colors.text }}>未处于延伸状态</Text>
          <Text style={styles.note}>
            统计只在“极值偏离 EMA20 ≥ 2 ATR、位于近 30 根区间边缘、24h 成交额 ≥ 200 万”时成立。现在：顶 {d.risk ? d.risk.top.ext.toFixed(1) : "—"} ATR · 底 {d.risk ? d.risk.bottom.ext.toFixed(1) : "—"} ATR。
          </Text>
        </View>
      ) : null}

      {d && p && x && !(p.side === "short" && d.squeeze) ? (
        <View style={{ marginTop: 12, padding: 12, borderRadius: 10, borderWidth: 1, borderColor: colors.border, gap: 4 }}>
          <Text style={{ fontSize: fs.sm, fontWeight: "600", color: p.side === "short" ? colors.down : colors.up }}>{p.side === "short" ? "逆势做空参考" : "逆势做多参考"}</Text>
          <Text style={[{ fontSize: fs.sm, color: colors.text }, tnum]}>入场 {price(p.entryLo)}–{price(p.entryHi)} · 止损 {price(p.stop)}（极值外 0.5 ATR）</Text>
          <Text style={[{ fontSize: fs.sm, color: colors.text }, tnum]}>目标 {price(p.tp1)}（EMA20）/ {price(p.tp2)}（回撤 38.2%） · 盈亏比 {p.rr.toFixed(1)}</Text>
          <Text style={[styles.note, { color: colors.warn }]}>基础统计：{FADE_BASE}。只在延续风险低、且拐头已经出现时考虑。</Text>
        </View>
      ) : null}

      <Pressable onPress={() => { tap(); setShowList(!showList); }} style={{ flexDirection: "row", alignItems: "center", gap: 6, marginTop: 14, paddingVertical: 6 }}>
        <Ionicons name="chevron-forward" size={13} color={colors.textFaint} style={{ transform: [{ rotate: showList ? "90deg" : "0deg" }] }} />
        <Text style={{ flex: 1, fontSize: fs.sm, color: colors.textSoft }}>衰竭清单 顶 {d?.top ?? "—"} · 底 {d?.bottom ?? "—"}（盘面描述，历史检验无方向预测力）</Text>
      </Pressable>
      {showList ? (
        <View>
          <View style={{ alignSelf: "flex-end", marginBottom: 8 }}>
            <Segmented small value={bar} onChange={setBar} options={["15m", "1H", "4H"].map((v) => ({ value: v, label: v }))} />
          </View>
          {desc ? (
            <>
              <Checklist title="顶部" score={desc.top} items={desc.topItems} />
              <Checklist title="底部" score={desc.bottom} items={desc.bottomItems} />
            </>
          ) : <Text style={styles.note}>计算中…</Text>}
        </View>
      ) : null}

      <Text style={[styles.note, { marginTop: 10 }]}>
        依据：2 年 OKX 1H 数据、286 个合约、19 个月逐月样本外检验。量能高潮、影线、RSI 与背离、TD 序列、布林 %B、资金费、OI、主动买卖等“见顶/见底”信号预测方向的 AUC 都在 0.47–0.53（≈ 抛硬币），清单分也是；能预测的是这里的两个概率（AUC 约 0.63 / 0.62，19 个月都高于随机）。
      </Text>
    </View>
  );
}

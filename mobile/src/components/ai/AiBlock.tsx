// One generative-UI block of an agent answer (web AiBlock.vue; schemas in ai/prompts/chat_v1.md):
// verdict, kpi tiles, trade plan (price ladder, reward/risk), coin list, bull vs bear.
import { useEffect, useMemo, useState } from "react";
import { Animated, Pressable, Text, View, type ViewStyle } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { colors, fs, tnum } from "@/lib/theme";
import { tap } from "@/components/ui";
import { instOk, num, symOf, type Segment } from "./blocks";
import { goFromAi } from "./Markdown";

type Tone = { t: string; c: string; cb: string; line: string };
const TONE = {
  up: { c: colors.up, cb: "rgba(37,167,80,0.12)", line: "rgba(37,167,80,0.45)" },
  down: { c: colors.down, cb: "rgba(202,63,100,0.12)", line: "rgba(202,63,100,0.45)" },
  wait: { c: "#8a8a8a", cb: "rgba(255,255,255,0.05)", line: "rgba(138,138,138,0.45)" },
  warn: { c: colors.warn, cb: "rgba(247,166,0,0.1)", line: "rgba(247,166,0,0.45)" },
};
const SIDE: Record<string, Tone> = {
  long: { t: "做多", ...TONE.up },
  short: { t: "做空", ...TONE.down },
  wait: { t: "观望", ...TONE.wait },
  avoid: { t: "回避", ...TONE.warn },
  watch: { t: "关注", ...TONE.wait },
};
const side = (s: unknown): Tone => SIDE[String(s)] ?? { t: String(s ?? ""), ...TONE.wait };
const toneOf = (t: unknown) => (t === "up" ? colors.up : t === "down" ? colors.down : t === "warn" ? colors.warn : colors.text);
const fmt = (v: number) => (Math.abs(v) >= 1000 ? v.toLocaleString("en-US", { maximumFractionDigits: 1 }) : Math.abs(v) >= 1 ? +v.toFixed(4) + "" : +v.toPrecision(4) + "");

const box: ViewStyle = { marginVertical: 10, borderRadius: 14, borderWidth: 1, borderColor: colors.border, backgroundColor: colors.card, overflow: "hidden" };

function InstButton({ id, onPress }: { id: string; onPress: () => void }) {
  return (
    <Pressable onPress={onPress} hitSlop={6} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 2, paddingHorizontal: 8, paddingVertical: 3, borderRadius: 8, backgroundColor: pressed ? "rgba(188,255,47,0.22)" : colors.primarySoft })}>
      <Text style={{ fontWeight: "700", color: colors.primary, fontSize: fs.lg }}>{symOf(id)}</Text>
      <Ionicons name="chevron-forward" size={14} color={colors.primary} />
    </Pressable>
  );
}

function Skeleton() {
  const [a] = useState(() => new Animated.Value(0.4));
  useEffect(() => {
    const l = Animated.loop(Animated.sequence([Animated.timing(a, { toValue: 1, duration: 600, useNativeDriver: true }), Animated.timing(a, { toValue: 0.4, duration: 600, useNativeDriver: true })]));
    l.start();
    return () => l.stop();
  }, [a]);
  return (
    <Animated.View style={[box, { padding: 14, gap: 8, opacity: a }]}>
      <View style={{ height: 10, borderRadius: 5, backgroundColor: colors.cardMuted }} />
      <View style={{ height: 10, borderRadius: 5, backgroundColor: colors.cardMuted }} />
      <View style={{ height: 10, width: "55%", borderRadius: 5, backgroundColor: colors.cardMuted }} />
    </Animated.View>
  );
}

function Kpi({ items }: { items: any[] }) {
  const [w, setW] = useState(0);
  const cols = Math.max(1, Math.floor(w / 104));
  return (
    <View style={[box, { flexDirection: "row", flexWrap: "wrap" }]} onLayout={(e) => setW(e.nativeEvent.layout.width - 2)}>
      {w
        ? items.map((k, i) => (
            <View key={i} style={{ width: w / cols, paddingHorizontal: 11, paddingVertical: 10, gap: 2, borderColor: colors.border, borderRightWidth: (i + 1) % cols ? 0.5 : 0, borderBottomWidth: 0.5 }}>
              <Text numberOfLines={1} style={{ fontSize: fs.xs, color: colors.textFaint }}>{String(k?.label ?? "")}</Text>
              <Text style={[{ fontSize: fs.xl, fontWeight: "700", color: toneOf(k?.tone) }, tnum]}>{String(k?.value ?? "")}</Text>
              {k?.sub ? <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{String(k.sub)}</Text> : null}
            </View>
          ))
        : null}
    </View>
  );
}

export function AiBlock({ seg }: { seg: Extract<Segment, { k: "block" }> }) {
  const d = seg.data ?? {};
  const go = (id: unknown) => {
    if (!instOk(id)) return;
    tap();
    goFromAi({ pathname: "/instrument/[id]", params: { id: String(id) } });
  };

  // plan: a price ladder from target 2 down to the stop (or up, for shorts)
  const plan = useMemo(() => {
    if (seg.type !== "plan" || seg.open || seg.error) return null;
    const p = d;
    const ent: number[] = (Array.isArray(p.entry) ? p.entry : [p.entry]).map(num).filter((x: number | null): x is number => x != null);
    const mid = ent.length ? ent.reduce((a, b) => a + b, 0) / ent.length : null;
    const pct = (v: number | null) => (v == null || mid == null ? "" : `${v >= mid ? "+" : ""}${((v / mid - 1) * 100).toFixed(1)}%`);
    const tps: number[] = (Array.isArray(p.tp) ? p.tp : [p.tp]).map(num).filter((x: number | null): x is number => x != null);
    const stop = num(p.stop);
    const rows = [
      ...tps.map((v, i) => ({ k: "tp", label: `止盈 ${i + 1}`, v, pct: pct(v), text: "" })),
      ...(mid != null ? [{ k: "entry", label: "入场", v: mid, text: ent.map(fmt).join(" – "), pct: "" }] : []),
      ...(stop != null ? [{ k: "stop", label: "止损", v: stop, pct: pct(stop), text: "" }] : []),
    ].sort((a, b) => b.v - a.v);
    const rr = num(p.rr) ?? (mid != null && stop != null && tps[0] != null ? Math.abs(tps[0] - mid) / Math.abs(mid - stop) : null);
    return { rows, rr, lev: num(p.leverage), side: side(p.side) };
  }, [seg]); // eslint-disable-line react-hooks/exhaustive-deps

  if (seg.open) return <Skeleton />;
  if (seg.error) return <Text style={[box, { padding: 10, fontSize: fs.xs, color: colors.textFaint }]}>{seg.raw}</Text>;

  if (seg.type === "verdict") {
    const s = side(d.direction);
    const wait = !["long", "short", "avoid"].includes(String(d.direction));
    return (
      <View style={[box, { borderColor: s.line }]}>
        <View style={{ paddingHorizontal: 14, paddingTop: 14, paddingBottom: 12, backgroundColor: s.cb }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 5, paddingHorizontal: 11, paddingVertical: 4, borderRadius: 9, backgroundColor: wait ? colors.cardMuted : s.c }}>
              <Ionicons name={d.direction === "long" ? "trending-up" : d.direction === "short" ? "trending-down" : "pause"} size={16} color={wait ? colors.text : "#fff"} />
              <Text style={{ fontWeight: "700", fontSize: fs.lg, color: wait ? colors.text : "#fff" }}>{s.t}</Text>
            </View>
            {instOk(d.instId) ? <InstButton id={d.instId} onPress={() => go(d.instId)} /> : null}
            {d.confidence ? <Text style={{ marginLeft: "auto", fontSize: fs.sm, color: colors.textSoft }}>信心 {String(d.confidence)}</Text> : null}
          </View>
          {d.title ? <Text style={{ marginTop: 10, fontSize: fs.xl, fontWeight: "700", lineHeight: 23, color: colors.text }}>{String(d.title)}</Text> : null}
          {d.reason ? <Text style={{ marginTop: 4, fontSize: fs.md, lineHeight: 21, color: colors.textSoft }}>{String(d.reason)}</Text> : null}
        </View>
      </View>
    );
  }

  if (seg.type === "kpi") return <Kpi items={(Array.isArray(d) ? d : d.items) ?? []} />;

  if (seg.type === "plan" && plan) {
    const s = plan.side;
    return (
      <View style={[box, { borderColor: s.line }]}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, paddingVertical: 10, backgroundColor: s.cb }}>
          <Text style={{ fontWeight: "800", color: s.c, fontSize: fs.lg }}>{s.t}</Text>
          {instOk(d.instId) ? <InstButton id={d.instId} onPress={() => go(d.instId)} /> : null}
          <View style={{ marginLeft: "auto", flexDirection: "row", flexWrap: "wrap", justifyContent: "flex-end", gap: 5, flexShrink: 1 }}>
            {plan.lev ? <PlanTag text={`${plan.lev}x`} /> : null}
            {plan.rr ? <PlanTag text={`盈亏比 ${plan.rr.toFixed(1)}`} /> : null}
            {d.risk ? <PlanTag text={`风险 ${d.risk}`} /> : null}
          </View>
        </View>
        <View style={{ paddingHorizontal: 12, paddingTop: 4, paddingBottom: 6 }}>
          {plan.rows.map((r, i) => {
            const c = r.k === "tp" ? colors.up : r.k === "stop" ? colors.down : colors.primary;
            return (
              <View key={i} style={[{ flexDirection: "row", alignItems: "center", gap: 6, paddingVertical: 7, borderBottomWidth: i < plan.rows.length - 1 ? 1 : 0, borderStyle: "dashed", borderColor: colors.border }, r.k === "entry" && { backgroundColor: "rgba(255,255,255,0.03)", marginHorizontal: -12, paddingHorizontal: 12 }]}>
                <Ionicons name={r.k === "tp" ? "locate-outline" : r.k === "entry" ? "log-in-outline" : "shield-outline"} size={13} color={c} style={{ width: 18 }} />
                <Text style={{ width: 52, fontSize: fs.sm, color: colors.textSoft }}>{r.label}</Text>
                <Text style={[{ flex: 1, fontWeight: "700", fontSize: fs.lg, color: colors.text }, tnum]}>{r.text || fmt(r.v)}</Text>
                <Text style={[{ fontSize: fs.sm, color: c }, tnum]}>{r.pct}</Text>
              </View>
            );
          })}
        </View>
        {d.invalidate || d.hold ? (
          <View style={{ gap: 4, paddingHorizontal: 12, paddingTop: 8, paddingBottom: 10, borderTopWidth: 1, borderColor: colors.border }}>
            {d.invalidate ? (
              <Text style={{ fontSize: fs.sm, color: colors.textSoft, lineHeight: 18 }}>
                <Text style={{ color: colors.warn, fontWeight: "600" }}>作废  </Text>
                {String(d.invalidate)}
              </Text>
            ) : null}
            {d.hold ? (
              <View style={{ flexDirection: "row", alignItems: "center", gap: 4 }}>
                <Ionicons name="time-outline" size={12} color={colors.textSoft} />
                <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>{String(d.hold)}</Text>
              </View>
            ) : null}
          </View>
        ) : null}
      </View>
    );
  }

  if (seg.type === "coins") {
    const coins: any[] = (Array.isArray(d) ? d : d.items) ?? [];
    return (
      <View style={box}>
        {d.title ? <Text style={{ paddingHorizontal: 12, paddingTop: 9, paddingBottom: 4, fontSize: fs.sm, color: colors.textFaint }}>{String(d.title)}</Text> : null}
        {coins.map((c, i) => {
          const ok = instOk(c?.instId);
          const s = c?.side ? side(c.side) : null;
          return (
            <Pressable key={i} disabled={!ok} onPress={() => go(c.instId)} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, paddingVertical: 10, borderTopWidth: i === 0 && d.title ? 0 : i === 0 ? 0 : 1, borderColor: colors.border, backgroundColor: pressed ? colors.raised : undefined })}>
              <Text style={{ minWidth: 52, fontWeight: "700", fontSize: fs.lg, color: colors.text }}>{ok ? symOf(c.instId) : String(c?.instId ?? "")}</Text>
              {s ? (
                <View style={{ paddingHorizontal: 7, paddingVertical: 1, borderRadius: 6, backgroundColor: s.cb }}>
                  <Text style={{ fontSize: fs.xs, fontWeight: "600", color: s.c }}>{s.t}</Text>
                </View>
              ) : null}
              <Text numberOfLines={1} style={{ flex: 1, fontSize: fs.sm, color: colors.textSoft }}>{String(c?.note ?? "")}</Text>
              {c?.metric ? <Text style={[{ fontSize: fs.sm, fontWeight: "600", color: colors.text }, tnum]}>{String(c.metric)}</Text> : null}
              {ok ? <Ionicons name="chevron-forward" size={15} color={colors.textDim} /> : null}
            </Pressable>
          );
        })}
      </View>
    );
  }

  if (seg.type === "bullbear") {
    const col = (up: boolean) => {
      const c = up ? colors.up : colors.down;
      const list: unknown[] = (up ? d.bull : d.bear) ?? [];
      const score = up ? d.bullScore : d.bearScore;
      return (
        <View style={{ flex: 1, backgroundColor: up ? "rgba(37,167,80,0.06)" : "rgba(202,63,100,0.06)", paddingHorizontal: 10, paddingTop: 10, paddingBottom: 8, borderRightWidth: up ? 1 : 0, borderColor: colors.border }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 5 }}>
            <Ionicons name={up ? "trending-up" : "trending-down"} size={14} color={c} />
            <Text style={{ fontWeight: "700", color: c, fontSize: fs.md }}>{up ? "多方" : "空方"}</Text>
            {score != null ? <Text style={{ marginLeft: "auto", fontSize: fs.xs, color: colors.textSoft }}>{String(score)}/10</Text> : null}
          </View>
          <View style={{ marginTop: 6, gap: 3 }}>
            {list.map((x, i) => (
              <View key={i} style={{ flexDirection: "row", gap: 5 }}>
                <Text style={{ fontSize: fs.sm, lineHeight: 19, color: colors.textSoft }}>•</Text>
                <Text style={{ flex: 1, fontSize: fs.sm, lineHeight: 19, color: colors.textSoft }}>{String(x)}</Text>
              </View>
            ))}
          </View>
        </View>
      );
    };
    return (
      <View style={[box, { flexDirection: "row" }]}>
        {col(true)}
        {col(false)}
      </View>
    );
  }
  return null;
}

function PlanTag({ text }: { text: string }) {
  return (
    <View style={{ paddingHorizontal: 7, paddingVertical: 2, borderRadius: 6, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.raised }}>
      <Text style={{ fontSize: fs.xs, color: colors.textSoft }}>{text}</Text>
    </View>
  );
}

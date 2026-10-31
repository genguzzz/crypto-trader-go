// One stage of the agent's work — consecutive thinking and tool calls — as a collapsible card, like
// Claude's desktop app: while it is the live tail it stays open and the reasoning streams in with a
// timer; once the answer moves on it folds to one line ("思考 2 次 · 5 步 · 23s"). Open, it is a
// timeline in order: each thought shows its first lines (tap for all), each call opens to its
// request and result.
import { useEffect, useState } from "react";
import { ActivityIndicator, Platform, Pressable, ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import type { ThinkPart, ToolPart } from "@/lib/ai";
import { colors, fs, tnum } from "@/lib/theme";
import { tap } from "@/components/ui";

const mono = Platform.select({ ios: "Menlo", default: "monospace" });
export type StepItem = ToolPart | ThinkPart;
const key = (s: StepItem, i: number) => (s.t === "tool" ? s.id : `think${i}`);
const secs = (ms: number) => (ms < 10_000 ? (ms / 1000).toFixed(1) : Math.round(ms / 1000).toString()) + "s";

function thinkMs(s: ThinkPart, now: number) {
  return s.ms ?? (s.live && s.t0 ? now - s.t0 : 0);
}

function Status({ s, size = 14 }: { s: StepItem | "busy" | "ok" | "fail"; size?: number }) {
  const busy = s === "busy" || (typeof s === "object" && (s.t === "tool" ? s.ok == null : !!s.live));
  const fail = s === "fail" || (typeof s === "object" && s.t === "tool" && s.ok === false);
  const think = typeof s === "object" && s.t === "think";
  return (
    <View style={{ width: 16, alignItems: "center" }}>
      {busy ? (
        <ActivityIndicator size="small" color={colors.primary} style={{ transform: [{ scale: 0.7 }] }} />
      ) : think ? (
        <Ionicons name="bulb-outline" size={size - 1} color={colors.textSoft} />
      ) : fail ? (
        <Ionicons name="alert-circle-outline" size={size} color={colors.warn} />
      ) : (
        <Ionicons name="checkmark" size={size} color={colors.primary} />
      )}
    </View>
  );
}

function Block({ k, text }: { k: string; text: string }) {
  return (
    <View style={{ gap: 3 }}>
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{k}</Text>
      <ScrollView nestedScrollEnabled style={{ maxHeight: 260, borderRadius: 8, backgroundColor: "#0a0a0a", borderWidth: 1, borderColor: colors.border }} contentContainerStyle={{ paddingHorizontal: 10, paddingVertical: 8 }}>
        <Text selectable style={{ fontFamily: mono, fontSize: 11, lineHeight: 16, color: colors.textSoft }}>{text}</Text>
      </ScrollView>
    </View>
  );
}

/** A thought in the timeline: a faint quote; the live one shows its newest lines, a finished one its first. */
function Thought({ s, now }: { s: ThinkPart; now: number }) {
  const [full, setFull] = useState(false);
  const text = s.v.trim();
  const long = text.length > 220;
  const shown = full || !long ? text : s.live ? "…" + text.slice(-220) : text.slice(0, 220) + "…";
  return (
    <View style={{ paddingLeft: 12, paddingRight: 12, paddingVertical: 6 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Status s={s} size={13} />
        <Text style={[{ fontSize: fs.sm, color: colors.text }, tnum]}>{s.live ? "思考中" : "思考"}<Text style={{ color: colors.textFaint }}>{`  ${secs(thinkMs(s, now))}`}</Text></Text>
      </View>
      {text ? (
        <Pressable disabled={!long || !!s.live} onPress={() => { tap(); setFull(!full); }} style={{ marginLeft: 7, marginTop: 4, paddingLeft: 16, borderLeftWidth: 2, borderColor: "rgba(155,125,240,0.35)" }}>
          <Text selectable={full} style={{ fontSize: fs.sm, lineHeight: 19, color: colors.textFaint }}>{shown}</Text>
          {long && !s.live ? <Text style={{ fontSize: fs.xs, color: colors.textDim, marginTop: 3 }}>{full ? "收起" : "展开全部"}</Text> : null}
        </Pressable>
      ) : null}
    </View>
  );
}

export function AiSteps({ items, live, tail }: { items: StepItem[]; live: boolean; tail: boolean }) {
  // open while this stage is the live tail; folds once the answer moves on, unless toggled by hand
  const active = live && tail;
  const [manual, setManual] = useState<boolean | null>(null);
  const open = manual ?? active;
  const [detail, setDetail] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(t);
  }, [active]);

  const tools = items.filter((s): s is ToolPart => s.t === "tool");
  const thoughts = items.filter((s): s is ThinkPart => s.t === "think");
  const current = items[items.length - 1];
  const failed = tools.filter((s) => s.ok === false).length;
  const thinkTotal = thoughts.reduce((a, s) => a + thinkMs(s, now), 0);
  const busy = active && (current.t === "tool" ? current.ok == null : !!current.live);
  const summary = [
    thoughts.length ? `思考${thoughts.length > 1 ? ` ${thoughts.length} 次` : ""}` : "",
    tools.length ? `${tools.length} 步` : "",
    thinkTotal ? secs(thinkTotal) : "",
  ].filter(Boolean).join(" · ");
  const labels = [...new Set(tools.map((s) => s.label))].slice(0, 4).join("、");
  const headLabel = busy ? (current.t === "think" ? "思考中" : current.label) : summary;
  const headSub = busy ? (current.t === "think" ? secs(thinkMs(current, now)) : current.input) : labels;

  return (
    <View style={{ marginTop: 6, marginBottom: 8, borderRadius: 12, backgroundColor: "rgba(255,255,255,0.025)", borderWidth: 1, borderColor: colors.border, overflow: "hidden" }}>
      <Pressable onPress={() => { tap(); setManual(!open); }} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, paddingVertical: 9, backgroundColor: pressed ? colors.raised : undefined })}>
        <Status s={busy ? "busy" : failed && !live ? "fail" : "ok"} />
        <View style={{ flex: 1, flexDirection: "row", alignItems: "baseline", gap: 6, minWidth: 0 }}>
          <Text style={[{ fontSize: fs.sm, fontWeight: "500", color: colors.text, flexShrink: 0 }, tnum]}>{headLabel}</Text>
          <Text numberOfLines={1} style={[{ flex: 1, fontSize: fs.xs, color: colors.textFaint }, tnum]}>{headSub}</Text>
        </View>
        {busy && tools.length ? <Text style={{ fontSize: fs.xs, color: colors.textDim }}>第 {tools.length} 步</Text> : null}
        <Ionicons name="chevron-forward" size={15} color={colors.textDim} style={{ transform: [{ rotate: open ? "90deg" : "0deg" }] }} />
      </Pressable>
      {open ? (
        <View style={{ borderTopWidth: 1, borderColor: colors.border, paddingVertical: 4 }}>
          {items.map((s, i) => {
            if (s.t === "think") return <Thought key={key(s, i)} s={s} now={now} />;
            const on = detail === s.id;
            return (
              <View key={key(s, i)}>
                <Pressable onPress={() => { tap(); setDetail(on ? null : s.id); }} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 7, paddingRight: 12, paddingLeft: s.sub ? 30 : 12, backgroundColor: pressed ? colors.raised : undefined })}>
                  <Status s={s} size={13} />
                  <Text style={{ fontSize: fs.sm, color: colors.text }}>{s.label}</Text>
                  <Text numberOfLines={1} style={{ flex: 1, fontSize: fs.xs, color: colors.textFaint }}>{s.input}</Text>
                  <Ionicons name="chevron-forward" size={13} color={colors.textDim} style={{ transform: [{ rotate: on ? "90deg" : "0deg" }] }} />
                </Pressable>
                {on ? (
                  <View style={{ paddingLeft: 34, paddingRight: 12, paddingTop: 2, paddingBottom: 10, gap: 8 }}>
                    {s.args ? <Block k="请求" text={s.args} /> : null}
                    <Block k={s.ok === false ? "出错" : "结果"} text={s.output ?? (s.ok == null ? "进行中…" : "（无输出）")} />
                  </View>
                ) : null}
              </View>
            );
          })}
        </View>
      ) : null}
    </View>
  );
}

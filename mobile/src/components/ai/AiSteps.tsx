// A run of tool calls collapsed to one line like Claude's apps (web AiSteps.vue): the current step
// while running, "N 步" with a summary when done. Expanded, each step opens to its request and result.
import { useState } from "react";
import { ActivityIndicator, Platform, Pressable, ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import type { ToolPart } from "@/lib/ai";
import { colors, fs } from "@/lib/theme";
import { tap } from "@/components/ui";

const mono = Platform.select({ ios: "Menlo", default: "monospace" });

function Status({ s, size = 14 }: { s: ToolPart | null; size?: number }) {
  return (
    <View style={{ width: 16, alignItems: "center" }}>
      {s?.ok == null ? <ActivityIndicator size="small" color={colors.primary} style={{ transform: [{ scale: 0.7 }] }} /> : s.ok ? <Ionicons name="checkmark" size={size} color={colors.primary} /> : <Ionicons name="alert-circle-outline" size={size} color={colors.warn} />}
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

export function AiSteps({ items, live }: { items: ToolPart[]; live: boolean }) {
  const [open, setOpen] = useState(false);
  const [detail, setDetail] = useState<string | null>(null);
  const running = items.find((s) => s.ok == null) ?? null;
  const failed = items.filter((s) => s.ok === false).length;
  const current = running ?? items[items.length - 1];
  const summary = [...new Set(items.map((s) => s.label))].slice(0, 4).join("、");
  const busy = live && !!running;

  return (
    <View style={{ marginTop: 6, marginBottom: 8, borderRadius: 12, backgroundColor: "rgba(255,255,255,0.025)", borderWidth: 1, borderColor: colors.border, overflow: "hidden" }}>
      <Pressable onPress={() => { tap(); setOpen(!open); }} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, paddingVertical: 9, backgroundColor: pressed ? colors.raised : undefined })}>
        {busy ? <Status s={null} /> : failed && !live ? <Status s={{ ok: false } as ToolPart} /> : <Status s={{ ok: true } as ToolPart} />}
        <View style={{ flex: 1, flexDirection: "row", alignItems: "baseline", gap: 6, minWidth: 0 }}>
          <Text style={{ fontSize: fs.sm, fontWeight: "500", color: colors.text, flexShrink: 0 }}>{busy ? current.label : `${items.length} 步`}</Text>
          <Text numberOfLines={1} style={{ flex: 1, fontSize: fs.xs, color: colors.textFaint }}>{busy ? current.input : summary}</Text>
        </View>
        {busy ? <Text style={{ fontSize: fs.xs, color: colors.textDim }}>第 {items.length} 步</Text> : null}
        <Ionicons name="chevron-forward" size={15} color={colors.textDim} style={{ transform: [{ rotate: open ? "90deg" : "0deg" }] }} />
      </Pressable>
      {open ? (
        <View style={{ borderTopWidth: 1, borderColor: colors.border, paddingVertical: 4 }}>
          {items.map((s) => {
            const on = detail === s.id;
            return (
              <View key={s.id}>
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

// `ask` block: a question with tappable answers. mode one = a tap sends it; many = toggle, then 确认;
// other = a free-text row. Only the newest finished answer is live (ask given); older ones are greyed.
import { useState } from "react";
import { Pressable, Text, TextInput, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { colors, fs } from "@/lib/theme";
import { tap } from "@/components/ui";

export function AskBlock({ d, ask }: { d: any; ask?: (t: string) => void }) {
  const opts: string[] = (Array.isArray(d?.options) ? d.options : []).map((o: any) => String(o?.label ?? o)).filter(Boolean);
  const many = d?.mode === "many";
  const [sel, setSel] = useState<string[]>([]);
  const [other, setOther] = useState("");
  const off = !ask;
  const go = (t: string) => { if (ask && t.trim()) { tap(); ask(t.trim()); } };
  const chip = (on: boolean) => ({ flexDirection: "row" as const, alignItems: "center" as const, gap: 6, paddingHorizontal: 12, paddingVertical: 8, borderRadius: 18, borderWidth: 1, borderColor: on ? colors.primary : "rgba(188,255,47,0.35)", backgroundColor: on ? "rgba(188,255,47,0.22)" : "rgba(188,255,47,0.08)", opacity: off ? 0.4 : 1 });
  return (
    <View style={{ marginVertical: 8, gap: 10 }}>
      {d?.q ? <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text }}>{String(d.q)}</Text> : null}
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
        {opts.map((o) => {
          const on = sel.includes(o);
          return (
            <Pressable key={o} disabled={off} onPress={() => (many ? (tap(), setSel(on ? sel.filter((x) => x !== o) : [...sel, o])) : go(o))} style={chip(on)}>
              {many ? <Ionicons name={on ? "checkbox" : "square-outline"} size={15} color={colors.primary} /> : null}
              <Text style={{ fontSize: fs.md, fontWeight: "600", color: colors.text }}>{o}</Text>
            </Pressable>
          );
        })}
      </View>
      {d?.other && !off ? (
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8, borderRadius: 12, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.card, paddingLeft: 12, paddingRight: 4 }}>
          <TextInput value={other} onChangeText={setOther} placeholder={typeof d.other === "string" ? d.other : "其他…"} placeholderTextColor={colors.textFaint} returnKeyType="send" onSubmitEditing={() => go(other)} style={{ flex: 1, paddingVertical: 9, fontSize: fs.md, color: colors.text }} />
          <Pressable hitSlop={6} disabled={!other.trim()} onPress={() => go(other)} style={{ width: 32, height: 32, borderRadius: 16, alignItems: "center", justifyContent: "center", backgroundColor: other.trim() ? colors.primary : colors.cardMuted }}>
            <Ionicons name="arrow-up" size={16} color={other.trim() ? "#000" : colors.textFaint} />
          </Pressable>
        </View>
      ) : null}
      {many && !off ? (
        <Pressable disabled={!sel.length} onPress={() => go(sel.join("、"))} style={{ alignSelf: "flex-start", paddingHorizontal: 18, paddingVertical: 8, borderRadius: 18, backgroundColor: sel.length ? colors.primary : colors.cardMuted }}>
          <Text style={{ fontWeight: "700", fontSize: fs.md, color: sel.length ? "#000" : colors.textFaint }}>确认{sel.length ? ` (${sel.length})` : ""}</Text>
        </Pressable>
      ) : null}
    </View>
  );
}

// The chat header's model chip and the card it opens: model, thinking effort, web search for the next
// questions (remembered on this device, sent with each question).
import { useState } from "react";
import { Pressable, Switch, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { AI_EFFORTS, AI_MODELS, setEffort, setModel, setWebSearch, useAiSelect } from "@/lib/ai";
import { haptic } from "@/lib/device";
import { colors, fs, radius } from "@/lib/theme";

export function useModelChoice() {
  // selected values only: the chat store changes on every streamed token
  const m = useAiSelect((a) => a.model);
  const effort = useAiSelect((a) => a.effort);
  const webSearch = useAiSelect((a) => a.webSearch);
  // unset = nothing sent: the server's own configuration (web search is on there)
  return { model: AI_MODELS.find((o) => o.value === m) ?? AI_MODELS[0], effort, webSearch: webSearch ?? true };
}

/** The chip under the chat title: "Sonnet · 思考中"; tap opens the menu. */
export function ModelChip({ onPress }: { onPress: () => void }) {
  const { model, effort } = useModelChoice();
  const e = AI_EFFORTS.find((x) => x.value === effort);
  return (
    <Pressable
      hitSlop={8}
      accessibilityRole="button"
      accessibilityLabel={`模型 ${model.label}，思考${e?.label ?? "默认"}，点按调整`}
      onPress={() => { void haptic("select"); onPress(); }}
      style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 3, alignSelf: "flex-start", opacity: pressed ? 0.6 : 1 })}
    >
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{model.label}{e ? ` · 思考${e.label}` : ""}</Text>
      <Ionicons name="chevron-down" size={11} color={colors.textFaint} />
    </Pressable>
  );
}

function Option({ label, hint, on, onPress }: { label: string; hint: string; on: boolean; onPress: () => void }) {
  return (
    <Pressable
      accessibilityRole="radio"
      accessibilityState={{ selected: on }}
      onPress={() => { void haptic("select"); onPress(); }}
      style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 10, paddingVertical: 8, paddingHorizontal: 10, borderRadius: radius.sm, backgroundColor: on ? colors.raised : pressed ? colors.raised : "transparent" })}
    >
      <View style={{ flex: 1 }}>
        <Text style={{ fontSize: fs.lg, color: colors.text, fontWeight: on ? "600" : "400" }}>{label}</Text>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 1 }}>{hint}</Text>
      </View>
      {on ? <Ionicons name="checkmark" size={16} color={colors.primary} /> : null}
    </Pressable>
  );
}

const Title = ({ children }: { children: string }) => (
  <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 10, marginBottom: 2, marginLeft: 10 }}>{children}</Text>
);

/** The dropdown card (rendered over the chat; a tap outside closes it). */
export function ModelMenu({ top, onClose }: { top: number; onClose: () => void }) {
  const { model, effort, webSearch } = useModelChoice();
  // web search flips at once but the switch keeps its own state for a smooth animation
  const [web, setWeb] = useState(webSearch);
  return (
    <View style={{ position: "absolute", left: 0, right: 0, top: 0, bottom: 0, zIndex: 20 }}>
      <Pressable accessibilityLabel="关闭" onPress={onClose} style={{ position: "absolute", left: 0, right: 0, top: 0, bottom: 0, backgroundColor: "rgba(0,0,0,0.12)" }} />
      <View style={{ position: "absolute", top, left: 16, width: 290, maxWidth: "92%", padding: 6, borderRadius: radius.lg, backgroundColor: colors.card, borderWidth: 1, borderColor: colors.borderStrong, shadowColor: "#000", shadowOpacity: 0.18, shadowRadius: 18, shadowOffset: { width: 0, height: 6 }, elevation: 8 }}>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 4, marginBottom: 2, marginLeft: 10 }}>模型</Text>
        {AI_MODELS.map((m) => <Option key={m.value} label={m.label} hint={m.hint} on={m.value === model.value} onPress={() => setModel(m.value)} />)}
        <Title>思考程度</Title>
        <Option label="默认" hint="不指定，用服务器的设置" on={!effort} onPress={() => setEffort(null)} />
        {AI_EFFORTS.map((e) => <Option key={e.value} label={e.label} hint={e.hint} on={e.value === effort} onPress={() => setEffort(e.value)} />)}
        <View style={{ height: 1, backgroundColor: colors.border, marginVertical: 8, marginHorizontal: 10 }} />
        <View style={{ flexDirection: "row", alignItems: "center", paddingHorizontal: 10, paddingBottom: 6, gap: 10 }}>
          <View style={{ flex: 1 }}>
            <Text style={{ fontSize: fs.lg, color: colors.text }}>联网搜索</Text>
            <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 1 }}>查新闻、公告时搜网页；关掉更省</Text>
          </View>
          <Switch value={web} onValueChange={(v) => { setWeb(v); setWebSearch(v); }} />
        </View>
      </View>
    </View>
  );
}

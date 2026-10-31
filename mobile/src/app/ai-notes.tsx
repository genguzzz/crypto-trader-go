// AI memory (web AiPanel "偏好与记忆"): preferences every answer takes into account; the agent adds
// to them when told "记住…/我喜欢…". Adding and deleting needs the owner key.
import { useEffect, useState } from "react";
import { KeyboardAvoidingView, Platform, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { addNote, deleteNote, loadNotes, useAi } from "@/lib/ai";
import { colors, fs, radius } from "@/lib/theme";
import { EmptyState, ErrorBox, Loading, Pull, Screen, TopBar, confirm, tap, toastError, styles } from "@/components/ui";

const day = (t: number) => {
  const d = new Date(t);
  return `${d.getMonth() + 1}/${d.getDate()}`;
};

export default function AiNotes() {
  const s = useAi();
  const [state, setState] = useState<string>(s.notes.length ? "ok" : "loading");
  const [text, setText] = useState("");
  const [saving, setSaving] = useState(false);
  const load = () =>
    loadNotes()
      .then(() => setState("ok"))
      .catch((e) => setState(e instanceof Error ? e.message : String(e)));
  useEffect(() => {
    void load();
  }, []);

  const save = async () => {
    const t = text.trim();
    if (!t || saving) return;
    tap();
    setSaving(true);
    try {
      await addNote(t);
      setText("");
    } catch (e) {
      toastError("未能保存", e);
    } finally {
      setSaving(false);
    }
  };
  const remove = (id: number) => confirm("删除这条记忆", "AI 之后的回答不再参考它。", "删除", () => void deleteNote(id).catch((e) => toastError("未能删除", e)), true);

  return (
    <Screen>
      <TopBar title="偏好与记忆" />
      <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
        <ScrollView contentContainerStyle={{ padding: 12, paddingBottom: 40 }} keyboardShouldPersistTaps="handled" refreshControl={<Pull onRefresh={load} />}>
          <Text style={[styles.note, { marginHorizontal: 4, marginBottom: 10 }]}>AI 每次回答都会参考这些偏好；在对话里说“记住…/我喜欢…”也会自动加到这里。</Text>
          <View style={{ flexDirection: "row", gap: 6, marginBottom: 8 }}>
            <TextInput
              value={text}
              onChangeText={setText}
              placeholder="例如：杠杆偏好，多空都用 8 倍"
              placeholderTextColor={colors.textDim}
              maxLength={300}
              returnKeyType="done"
              onSubmitEditing={() => void save()}
              style={{ flex: 1, height: 40, borderRadius: radius.md, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.raised, color: colors.text, paddingHorizontal: 12, fontSize: 16 }}
            />
            <Pressable accessibilityLabel="添加" disabled={!text.trim() || saving} onPress={() => void save()} style={({ pressed }) => ({ width: 40, height: 40, borderRadius: radius.md, alignItems: "center", justifyContent: "center", backgroundColor: colors.primarySoft, opacity: !text.trim() || saving ? 0.5 : pressed ? 0.7 : 1 })}>
              <Ionicons name="add" size={20} color={colors.primary} />
            </Pressable>
          </View>
          {state === "loading" && !s.notes.length ? <Loading /> : state !== "ok" && state !== "loading" && !s.notes.length ? <ErrorBox text={state} onRetry={load} /> : null}
          {state === "ok" && !s.notes.length ? <EmptyState icon="bulb-outline" text="还没有记忆" /> : null}
          {s.notes.map((n) => (
            <View key={n.id} style={{ flexDirection: "row", alignItems: "flex-start", gap: 8, paddingVertical: 10, paddingLeft: 10, paddingRight: 4, borderBottomWidth: 1, borderColor: colors.border }}>
              <View style={{ flex: 1 }}>
                <Text selectable style={{ fontSize: fs.lg, lineHeight: 21, color: colors.text }}>{n.text}</Text>
                <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 3 }}>{n.by === "ai" ? "AI 记录" : "手动添加"} · {day(n.ts)}</Text>
              </View>
              <Pressable hitSlop={8} accessibilityLabel="删除" onPress={() => { tap(); remove(n.id); }} style={({ pressed }) => ({ width: 36, height: 36, borderRadius: 8, alignItems: "center", justifyContent: "center", backgroundColor: pressed ? colors.cardMuted : undefined })}>
                <Ionicons name="trash-outline" size={16} color={colors.textDim} />
              </Pressable>
            </View>
          ))}
        </ScrollView>
      </KeyboardAvoidingView>
    </Screen>
  );
}

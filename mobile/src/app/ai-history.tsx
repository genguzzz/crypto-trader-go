// AI chat history (web AiPanel "历史对话"): recent conversations, tap to open, delete with confirm.
import { useEffect, useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { deleteChat, loadChats, openChat, useAi } from "@/lib/ai";
import { colors, fs } from "@/lib/theme";
import { EmptyState, ErrorBox, Loading, Pull, Screen, TopBar, confirm, tap, toastError } from "@/components/ui";

const ago = (t: number) => {
  const s = (Date.now() - t) / 1000;
  return s < 60 ? "刚刚" : s < 3600 ? `${Math.floor(s / 60)} 分钟前` : s < 86400 ? `${Math.floor(s / 3600)} 小时前` : `${Math.floor(s / 86400)} 天前`;
};

export default function AiHistory() {
  const s = useAi();
  const router = useRouter();
  const [state, setState] = useState<"loading" | "ok" | string>(s.chats.length ? "ok" : "loading");
  const load = () =>
    loadChats()
      .then(() => setState("ok"))
      .catch((e) => setState(e instanceof Error ? e.message : String(e)));
  useEffect(() => {
    void load();
  }, []);

  const pick = (id: number) => {
    tap();
    router.back();
    openChat(id).catch((e) => toastError("未能打开对话", e));
  };
  const remove = (id: number, title: string) =>
    confirm("删除对话", `「${title}」会从服务器删除。`, "删除", () => void deleteChat(id).catch((e) => toastError("未能删除", e)), true);

  return (
    <Screen>
      <TopBar title="历史对话" />
      {state === "loading" && !s.chats.length ? (
        <Loading text="加载对话" />
      ) : state !== "ok" && state !== "loading" && !s.chats.length ? (
        <ErrorBox text={state} onRetry={load} />
      ) : (
        <ScrollView contentContainerStyle={{ padding: 8, paddingBottom: 40 }} refreshControl={<Pull onRefresh={load} />}>
          {!s.chats.length ? <EmptyState icon="chatbubbles-outline" text="还没有对话" /> : null}
          {s.chats.map((c) => (
            <Pressable key={c.id} onPress={() => pick(c.id)} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 10, paddingLeft: 10, paddingRight: 4, borderRadius: 10, backgroundColor: c.id === s.chatId ? colors.primarySoft : pressed ? colors.raised : undefined })}>
              <View style={{ flex: 1, minWidth: 0 }}>
                <Text numberOfLines={1} style={{ fontSize: fs.lg, color: colors.text }}>{c.title}</Text>
                <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 2 }}>
                  {ago(c.updated)}
                  {c.instId ? ` · ${c.instId.split("-")[0]}` : ""}
                  {c.costUsd ? ` · $${c.costUsd.toFixed(2)}` : ""}
                  {c.running ? <Text style={{ color: colors.primary }}>  进行中</Text> : null}
                </Text>
              </View>
              <Pressable hitSlop={8} accessibilityLabel="删除" onPress={() => { tap(); remove(c.id, c.title); }} style={({ pressed }) => ({ width: 36, height: 36, borderRadius: 8, alignItems: "center", justifyContent: "center", backgroundColor: pressed ? colors.cardMuted : undefined })}>
                <Ionicons name="trash-outline" size={16} color={colors.textDim} />
              </Pressable>
            </Pressable>
          ))}
        </ScrollView>
      )}
    </Screen>
  );
}

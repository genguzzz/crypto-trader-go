// History and memory as panes inside the AI sheet (they slide over the chat). They used to be modal
// routes pushed from the sheet, and a modal presented from a form sheet showed but took no touches.
import { useEffect, useState, type ReactNode } from "react";
import { KeyboardAvoidingView, Platform, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { addNote, deleteChat, deleteNote, loadChats, loadNotes, openChat, useAi } from "@/lib/ai";
import { colors, fs, radius } from "@/lib/theme";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { copy } from "@/lib/device";
import { EmptyState, ErrorBox, Loading, Pull, Tag, confirm, tap, toast, toastError, styles } from "@/components/ui";
import { ProposalCard } from "@/components/ai/ProposalCard";
import { useQuery } from "@/lib/query";
import { refreshProposals, rtApi, usePendingProposals, useRealtrade, useRecentProposals, type RtConfig } from "@/lib/realtrade";

function PaneHeader({ title, onClose, right }: { title: string; onClose: () => void; right?: ReactNode }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 6, paddingTop: Platform.OS === "web" ? 12 : 18, paddingHorizontal: 8, paddingBottom: 8, borderBottomWidth: 1, borderColor: colors.border }}>
      <Pressable hitSlop={10} accessibilityLabel="返回对话" onPress={() => { tap(); onClose(); }} style={{ width: 40, height: 36, justifyContent: "center", alignItems: "center" }}>
        <Ionicons name="chevron-back" size={24} color={colors.text} />
      </Pressable>
      <Text style={{ flex: 1, fontSize: 17, fontWeight: "600", color: colors.text }}>{title}</Text>
      {right ? <View style={{ paddingRight: 8 }}>{right}</View> : null}
    </View>
  );
}

const ago = (t: number) => {
  const s = (Date.now() - t) / 1000;
  return s < 60 ? "刚刚" : s < 3600 ? `${Math.floor(s / 60)} 分钟前` : s < 86400 ? `${Math.floor(s / 3600)} 小时前` : `${Math.floor(s / 86400)} 天前`;
};

export function HistoryPane({ onClose }: { onClose: () => void }) {
  const s = useAi();
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
    onClose();
    openChat(id).catch((e) => toastError("未能打开对话", e));
  };
  const remove = (id: number, title: string) =>
    confirm("删除对话", `「${title}」会从服务器删除。`, "删除", () => void deleteChat(id).catch((e) => toastError("未能删除", e)), true);

  return (
    <View style={{ flex: 1, backgroundColor: colors.bg }}>
      <PaneHeader title="历史对话" onClose={onClose} />
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
    </View>
  );
}

const day = (t: number) => {
  const d = new Date(t);
  return `${d.getMonth() + 1}/${d.getDate()}`;
};

export function NotesPane({ onClose }: { onClose: () => void }) {
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
    <View style={{ flex: 1, backgroundColor: colors.bg }}>
      <PaneHeader title="偏好与记忆" onClose={onClose} />
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
    </View>
  );
}

/** AI trade proposals: pending first, then recent ones, with the server's mode and limits. Owner only. */
export function ProposalsPane({ onClose }: { onClose: () => void }) {
  const owner = useRealtrade((r) => r.owner);
  const mode = useRealtrade((r) => r.mode);
  const pending = usePendingProposals();
  const recent = useRecentProposals();
  const cfg = useQuery<RtConfig>(owner ? "rt:config" : null, () => rtApi.config(), { staleMs: 60_000 });
  const list = [...pending, ...(recent ?? []).filter((p) => p.status !== "pending" && !pending.some((x) => x.id === p.id))];
  const lim = cfg.data?.limits;
  const tg = cfg.data?.telegram;
  return (
    <View style={{ flex: 1, backgroundColor: colors.bg }}>
      <PaneHeader title="交易提议" onClose={onClose} right={mode ? <Tag text={mode === "dryrun" ? "DRY RUN" : "LIVE"} tone={mode === "dryrun" ? "warn" : "bad"} /> : null} />
      {!owner ? (
        <EmptyState icon="key-outline" text="交易提议只对所有者可见，在设置里填入所有者密钥。" />
      ) : (
        <ScrollView contentContainerStyle={{ padding: 12, paddingBottom: 40 }} refreshControl={<Pull onRefresh={() => Promise.all([refreshProposals(), cfg.refresh()])} />}>
          <Text style={[styles.note, { marginHorizontal: 4, marginBottom: 4 }]}>
            {mode === "live" ? "LIVE：同意后以市价在 OKX 真实下单（逐仓，带止盈止损）。" : "DRY RUN：同意后只生成并记录下单请求，不会真实下单。"}
            {"AI 在对话或信号触发时提议，Telegram、网页、App 任一处先答复为准。"}
          </Text>
          {lim ? (
            <Text style={[styles.note, { marginHorizontal: 4, marginBottom: 6 }]}>
              {`杠杆 ≤ ${lim.maxLeverage}× · 单笔保证金 ≤ ${lim.maxMarginUsdt} U · 同时 ≤ ${lim.maxOpen} 笔 · 日亏损上限 ${lim.dailyLossUsdt} U · 盈亏比 ≥ ${lim.minRR} · ${lim.expiryMin} 分钟未审核自动过期`}
              {tg ? ` · Telegram ${tg.enabled ? tg.state || "已开启" : "未开启"}` : ""}
            </Text>
          ) : null}
          {recent === null && !pending.length ? <Loading text="加载提议" /> : null}
          {recent !== null && !list.length ? <EmptyState icon="receipt-outline" text="还没有交易提议" /> : null}
          {list.map((p) => <ProposalCard key={p.id} p={p} />)}
        </ScrollView>
      )}
    </View>
  );
}

/** Markdown → plain text for selecting: links, emphasis and code marks dropped, structure kept. */
export function plainText(md: string) {
  return md
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/(\*\*|__)(.+?)\1/g, "$2")
    .replace(/`([^`\n]+)`/g, "$1")
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

/** The answer as native iOS text: drag the handles to select any span, then copy / look up / share. */
export function SelectPane({ text, onClose }: { text: string; onClose: () => void }) {
  const insets = useSafeAreaInsets();
  return (
    <View style={{ flex: 1 }}>
      <PaneHeader title="选择文本" onClose={onClose} />
      <TextInput
        value={text}
        editable={false}
        multiline
        scrollEnabled
        style={{ flex: 1, paddingHorizontal: 16, paddingTop: 12, paddingBottom: insets.bottom + 24, fontSize: fs.lg, lineHeight: 24, color: colors.text, textAlignVertical: "top" }}
      />
      <View style={{ position: "absolute", right: 12, top: Platform.OS === "web" ? 10 : 16 }}>
        <Pressable
          hitSlop={8}
          onPress={() => { tap(); void copy(text).then((ok) => toast(ok ? "已复制全文" : "复制失败")); }}
          style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 5, paddingHorizontal: 11, height: 32, borderRadius: 16, backgroundColor: pressed ? colors.raised : colors.cardMuted })}
        >
          <Ionicons name="copy-outline" size={14} color={colors.text} />
          <Text style={{ fontSize: fs.sm, color: colors.text }}>全部复制</Text>
        </Pressable>
      </View>
    </View>
  );
}

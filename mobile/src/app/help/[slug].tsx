// One help article, rendered natively from the bundled Markdown. Links to other articles stay in
// the app; other app paths and external links open in the browser.
import { useEffect, useRef, useState } from "react";
import { Pressable, ScrollView, Text, View, type ScrollViewInstance } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { strategyDocs } from "@/content/strategyDocs";
import { WEB_ORIGIN } from "@/lib/api";
import { openPage } from "@/lib/device";
import { colors, fs } from "@/lib/theme";
import { Markdown } from "@/components/Markdown";
import { EmptyState, IconButton, PickerSheet, Screen, TopBar, tap } from "@/components/ui";

export default function HelpArticle() {
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const router = useRouter();
  const scroll = useRef<ScrollViewInstance>(null);
  const [pick, setPick] = useState(false);
  const idx = strategyDocs.findIndex((a) => a.slug === slug);
  const doc = strategyDocs[idx];
  const prev = idx > 0 ? strategyDocs[idx - 1] : null;
  const next = idx >= 0 && idx < strategyDocs.length - 1 ? strategyDocs[idx + 1] : null;
  useEffect(() => {
    scroll.current?.scrollTo({ y: 0, animated: false });
  }, [slug]);
  const open = (s: string) => router.replace({ pathname: "/help/[slug]", params: { slug: s } });

  const onLink = (href: string) => {
    const m = href.match(/^\/help\/strategy\/([\w-]+)(#.*)?$/);
    if (m && strategyDocs.some((a) => a.slug === m[1])) router.push({ pathname: "/help/[slug]", params: { slug: m[1] } });
    else if (href.startsWith("/")) void openPage(WEB_ORIGIN + href);
    else if (/^https?:/.test(href)) void openPage(href);
  };

  return (
    <Screen>
      <TopBar title={doc?.title ?? "帮助"} sub={doc ? `策略文档 · ${idx + 1}/${strategyDocs.length}` : undefined} right={<IconButton name="list" label="选择文章" onPress={() => setPick(true)} />} />
      {!doc ? (
        <EmptyState icon="book-outline" text="没有这篇文章" />
      ) : (
        <ScrollView ref={scroll} contentContainerStyle={{ padding: 16, paddingBottom: 60 }}>
          <Markdown source={doc.body} onLink={onLink} />
          <View style={{ flexDirection: "row", gap: 10, marginTop: 28, paddingTop: 16, borderTopWidth: 1, borderColor: colors.border }}>
            {prev ? <PageLink dir="prev" title={prev.title} onPress={() => open(prev.slug)} /> : <View style={{ flex: 1 }} />}
            {next ? <PageLink dir="next" title={next.title} onPress={() => open(next.slug)} /> : <View style={{ flex: 1 }} />}
          </View>
        </ScrollView>
      )}
      <PickerSheet visible={pick} onClose={() => setPick(false)} title="策略文档" value={slug}
        options={strategyDocs.map((a, i) => ({ value: a.slug, label: `${i + 1}. ${a.title}`, desc: a.summary }))} onPick={(s) => s !== slug && open(s)} />
    </Screen>
  );
}

function PageLink({ dir, title, onPress }: { dir: "prev" | "next"; title: string; onPress: () => void }) {
  const next = dir === "next";
  return (
    <Pressable onPress={() => { tap(); onPress(); }} style={({ pressed }) => ({ flex: 1, flexDirection: "row", alignItems: "center", gap: 6, padding: 10, borderRadius: 8, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: pressed ? colors.raised : "transparent", justifyContent: next ? "flex-end" : "flex-start" })}>
      {!next ? <Ionicons name="chevron-back" size={14} color={colors.textFaint} /> : null}
      <View style={{ flexShrink: 1, alignItems: next ? "flex-end" : "flex-start" }}>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{next ? "下一篇" : "上一篇"}</Text>
        <Text style={{ fontSize: fs.md, color: colors.text }} numberOfLines={1}>{title}</Text>
      </View>
      {next ? <Ionicons name="chevron-forward" size={14} color={colors.textFaint} /> : null}
    </Pressable>
  );
}

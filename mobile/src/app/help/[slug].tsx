// One help article, rendered natively from the bundled Markdown, or the SDK API reference (slug
// "api"). Links to other articles stay in the app; other app paths and external links open in the
// browser. Strategy examples get 「在编辑器中打开」: it creates a draft from the snippet (like the
// web) and opens it on the strategy screen.
import { useEffect, useRef, useState } from "react";
import { Pressable, ScrollView, Text, View, type ScrollViewInstance } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { strategyDocs } from "@/content/strategyDocs";
import { WEB_ORIGIN } from "@/lib/api";
import { openPage } from "@/lib/device";
import { tradeApi } from "@/lib/tradeApi";
import { colors, fs } from "@/lib/theme";
import { API_ARTICLE, ApiRef } from "@/components/ApiRef";
import { Markdown, type CodeAction } from "@/components/Markdown";
import { EmptyState, IconButton, PickerSheet, Screen, TopBar, tap, toastError } from "@/components/ui";

const articles = [...strategyDocs, { ...API_ARTICLE, body: "" }];
const isStrategy = (code: string) => /class \w+\((\w+\.)?Strategy\)/.test(code) && /^from ctg import/m.test(code);

export default function HelpArticle() {
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const router = useRouter();
  const scroll = useRef<ScrollViewInstance>(null);
  const [pick, setPick] = useState(false);
  const [opening, setOpening] = useState(false);
  const idx = articles.findIndex((a) => a.slug === slug);
  const doc = articles[idx];
  const prev = idx > 0 ? articles[idx - 1] : null;
  const next = idx >= 0 && idx < articles.length - 1 ? articles[idx + 1] : null;
  useEffect(() => {
    scroll.current?.scrollTo({ y: 0, animated: false });
  }, [slug]);
  const open = (s: string) => router.replace({ pathname: "/help/[slug]", params: { slug: s } });

  const onLink = (href: string) => {
    const m = href.match(/^\/help\/strategy\/([\w-]+)(#.*)?$/);
    if (m && articles.some((a) => a.slug === m[1])) router.push({ pathname: "/help/[slug]", params: { slug: m[1] } });
    else if (href.startsWith("/")) void openPage(WEB_ORIGIN + href);
    else if (/^https?:/.test(href)) void openPage(href);
  };

  const openInEditor = async (code: string) => {
    if (opening) return;
    setOpening(true);
    try {
      const s = await tradeApi.createSource({ code: code.trimEnd() + "\n" });
      router.push({ pathname: "/trading/strategy", params: { source: String(s.id) } });
    } catch (e) {
      toastError("未能打开", e);
    } finally {
      setOpening(false);
    }
  };
  const codeAction = (code: string): CodeAction | null =>
    isStrategy(code) ? { label: opening ? "打开中…" : "在编辑器中打开", icon: "create-outline", onPress: () => void openInEditor(code) } : null;

  return (
    <Screen>
      <TopBar title={doc?.title ?? "帮助"} sub={doc ? (doc.slug === API_ARTICLE.slug ? "策略文档" : `策略文档 · ${idx + 1}/${strategyDocs.length}`) : undefined} right={<IconButton name="list" label="选择文章" onPress={() => setPick(true)} />} />
      {!doc ? (
        <EmptyState icon="book-outline" text="没有这篇文章" />
      ) : (
        <ScrollView ref={scroll} contentContainerStyle={{ padding: 16, paddingBottom: 60 }}>
          {doc.slug === API_ARTICLE.slug ? <ApiRef /> : <Markdown source={doc.body} onLink={onLink} codeAction={codeAction} />}
          <View style={{ flexDirection: "row", gap: 10, marginTop: 28, paddingTop: 16, borderTopWidth: 1, borderColor: colors.border }}>
            {prev ? <PageLink dir="prev" title={prev.title} onPress={() => open(prev.slug)} /> : <View style={{ flex: 1 }} />}
            {next ? <PageLink dir="next" title={next.title} onPress={() => open(next.slug)} /> : <View style={{ flex: 1 }} />}
          </View>
        </ScrollView>
      )}
      <PickerSheet visible={pick} onClose={() => setPick(false)} title="策略文档" value={slug}
        options={articles.map((a, i) => ({ value: a.slug, label: a.slug === API_ARTICLE.slug ? a.title : `${i + 1}. ${a.title}`, desc: a.summary }))} onPick={(s) => s !== slug && open(s)} />
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

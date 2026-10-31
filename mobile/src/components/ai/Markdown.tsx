// Markdown for AI answers: markdown-it (pure JS, same parser and options as the web panel) → RN
// elements. Same hierarchy as web AiPanel's .md styles: headings, paragraphs, lists, bold, inline
// code, code blocks, quotes, rules, tables (horizontal scroll). Links to app paths
// ([SOL](/instrument/SOL-USDT-SWAP), /rules/<id>, /alerts, /trading, /) are lime chips that open
// the native screen; other links open in the in-app browser.
import { memo, useMemo, type ReactNode } from "react";
import { Platform, ScrollView, Text, View, type TextStyle } from "react-native";
import { router, type Href } from "expo-router";
import MarkdownIt from "markdown-it";
import { openPage } from "@/lib/device";
import { colors, fs } from "@/lib/theme";
import { tap } from "@/components/ui";

const md = new MarkdownIt({ html: false, linkify: true, breaks: true });
type Token = ReturnType<typeof md.parse>[number];

interface Node {
  type: string;
  tag: string;
  attrs: Record<string, string>;
  content: string;
  info: string;
  children: Node[];
}

function tree(tokens: Token[]): Node[] {
  const root: Node = { type: "root", tag: "", attrs: {}, content: "", info: "", children: [] };
  const stack = [root];
  for (const t of tokens) {
    const top = stack[stack.length - 1];
    if (t.nesting === -1) {
      if (stack.length > 1) stack.pop();
      continue;
    }
    const node: Node = {
      type: t.nesting === 1 ? t.type.replace(/_open$/, "") : t.type,
      tag: t.tag,
      attrs: Object.fromEntries(t.attrs ?? []),
      content: t.content,
      info: t.info,
      children: t.type === "inline" && t.children ? tree(t.children) : [],
    };
    top.children.push(node);
    if (t.nesting === 1) stack.push(node);
  }
  return root.children;
}

/** Puts the typing caret after the last inline content. */
function addCaret(nodes: Node[]): boolean {
  for (let i = nodes.length - 1; i >= 0; i--) {
    const n = nodes[i];
    if (n.type === "inline") {
      n.children.push({ type: "caret", tag: "", attrs: {}, content: "", info: "", children: [] });
      return true;
    }
    if (n.children.length && addCaret(n.children)) return true;
  }
  return false;
}

// ---------- in-app links ----------

const APP_PATH = /^\/(instrument\/[A-Z0-9-]+|rules(\/[\w-]+)?|alerts|trading(\/[\w/-]+)?|status|settings|help\/[\w/-]+)?$/;

/** Web route → native route. */
export function appRoute(href: string): Href | null {
  if (!APP_PATH.test(href)) return null;
  let m: RegExpMatchArray | null;
  if ((m = href.match(/^\/instrument\/(.+)$/))) return { pathname: "/instrument/[id]", params: { id: m[1] } };
  if ((m = href.match(/^\/rules\/(.+)$/))) return { pathname: "/rules", params: { id: m[1] } };
  if (href === "/rules") return "/rules";
  if (href === "/alerts") return "/alerts";
  if (href === "/status") return "/status";
  if (href === "/settings") return "/settings";
  if (href.startsWith("/trading")) return "/trade";
  if (href.startsWith("/help")) return "/help";
  return "/";
}

export function openLink(href: string) {
  tap();
  const r = appRoute(href);
  if (r) router.push(r);
  else if (/^https?:\/\//.test(href)) void openPage(href);
}

// ---------- styles (web AiPanel .md) ----------

const mono = Platform.select({ ios: "Menlo", default: "monospace" });
const S = {
  p: { fontSize: fs.lg, lineHeight: 24, color: colors.text, marginVertical: 5 } as TextStyle,
  h1: { fontSize: fs.xl, lineHeight: 24, fontWeight: "700", color: colors.text, marginTop: 14, marginBottom: 6 } as TextStyle,
  h4: { fontSize: fs.lg, lineHeight: 22, fontWeight: "700", color: colors.text, marginTop: 12, marginBottom: 4 } as TextStyle,
  strong: { fontWeight: "700", color: colors.white } as TextStyle,
  code: { fontFamily: mono, fontSize: fs.md - 0.5, backgroundColor: colors.cardMuted, color: colors.text } as TextStyle,
  link: { color: colors.info, textDecorationLine: "underline" } as TextStyle,
  chip: { color: colors.primary, fontWeight: "600", fontSize: fs.md, backgroundColor: "rgba(188,255,47,0.14)" } as TextStyle,
  cell: { fontSize: fs.sm, lineHeight: 18, color: colors.text } as TextStyle,
};

function inline(nodes: Node[], key = "i"): ReactNode[] {
  return nodes.map((n, i) => {
    const k = `${key}.${i}`;
    switch (n.type) {
      case "text":
      case "html_inline":
        return n.content;
      case "softbreak":
      case "hardbreak":
        return "\n";
      case "code_inline":
        return <Text key={k} style={S.code}>{` ${n.content} `}</Text>;
      case "strong":
        return <Text key={k} style={S.strong}>{inline(n.children, k)}</Text>;
      case "em":
        return <Text key={k} style={{ fontStyle: "italic" }}>{inline(n.children, k)}</Text>;
      case "s":
        return <Text key={k} style={{ textDecorationLine: "line-through" }}>{inline(n.children, k)}</Text>;
      case "link": {
        const href = n.attrs.href ?? "";
        if (APP_PATH.test(href))
          return (
            <Text key={k} style={S.chip} onPress={() => openLink(href)} suppressHighlighting={false}>
              {" "}{inline(n.children, k)} ›{" "}
            </Text>
          );
        return <Text key={k} style={S.link} onPress={() => openLink(href)}>{inline(n.children, k)}</Text>;
      }
      case "image":
        return n.content || n.attrs.alt || "";
      case "caret":
        return <Text key={k} style={{ color: colors.primary }}>▍</Text>;
      default:
        return n.children.length ? <Text key={k}>{inline(n.children, k)}</Text> : n.content;
    }
  });
}

const inlineOf = (n: Node) => (n.children[0]?.type === "inline" ? n.children[0].children : n.children);

function Table({ node }: { node: Node }) {
  const rows: { cells: Node[]; head: boolean }[] = [];
  for (const sec of node.children) for (const tr of sec.children) rows.push({ cells: tr.children, head: sec.type === "thead" });
  const cols = Math.max(0, ...rows.map((r) => r.cells.length));
  // column by column so every column takes its widest cell; cells never wrap (web: nowrap)
  return (
    <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ marginVertical: 8 }}>
      <View style={{ flexDirection: "row", borderLeftWidth: 1, borderTopWidth: 1, borderColor: colors.borderStrong }}>
        {Array.from({ length: cols }, (_, c) => (
          <View key={c}>
            {rows.map((r, ri) => {
              const cell = r.cells[c];
              const align = cell?.attrs.style?.includes("right") ? "right" : cell?.attrs.style?.includes("center") ? "center" : "left";
              return (
                <View key={ri} style={{ paddingHorizontal: 8, paddingVertical: 4, borderRightWidth: 1, borderBottomWidth: 1, borderColor: colors.borderStrong, backgroundColor: r.head ? colors.raised : undefined }}>
                  <Text numberOfLines={1} style={[S.cell, { textAlign: align }, r.head && { fontWeight: "600" }]}>{cell ? inline(inlineOf(cell), `t${ri}.${c}`) : " "}</Text>
                </View>
              );
            })}
          </View>
        ))}
      </View>
    </ScrollView>
  );
}

function blocks(nodes: Node[], tight = false, key = "b"): ReactNode[] {
  return nodes.map((n, i) => {
    const k = `${key}.${i}`;
    switch (n.type) {
      case "paragraph":
        return <Text key={k} style={[S.p, tight && { marginVertical: 1 }]}>{inline(inlineOf(n), k)}</Text>;
      case "inline":
        return <Text key={k} style={S.p}>{inline(n.children, k)}</Text>;
      case "heading":
        return <Text key={k} style={["h1", "h2", "h3"].includes(n.tag) ? S.h1 : S.h4}>{inline(inlineOf(n), k)}</Text>;
      case "bullet_list":
      case "ordered_list": {
        const start = Number(n.attrs.start ?? 1);
        return (
          <View key={k} style={{ marginVertical: 4 }}>
            {n.children.map((li, j) => (
              <View key={j} style={{ flexDirection: "row", marginVertical: 2 }}>
                <Text style={[S.p, { marginVertical: 1, width: n.type === "ordered_list" ? 22 : 16, color: colors.textSoft }]}>{n.type === "ordered_list" ? `${start + j}.` : "•"}</Text>
                <View style={{ flex: 1 }}>{blocks(li.children, true, `${k}.${j}`)}</View>
              </View>
            ))}
          </View>
        );
      }
      case "blockquote":
        return <View key={k} style={{ marginVertical: 6, paddingLeft: 10, borderLeftWidth: 3, borderColor: colors.borderStrong, opacity: 0.85 }}>{blocks(n.children, tight, k)}</View>;
      case "fence":
      case "code_block":
        return (
          <ScrollView key={k} horizontal showsHorizontalScrollIndicator={false} style={{ marginVertical: 6, borderRadius: 8, backgroundColor: colors.raised }} contentContainerStyle={{ padding: 10 }}>
            <Text style={{ fontFamily: mono, fontSize: fs.sm, lineHeight: 18, color: colors.textSoft }}>{n.content.replace(/\n$/, "")}</Text>
          </ScrollView>
        );
      case "hr":
        return <View key={k} style={{ height: 1, backgroundColor: colors.borderStrong, marginVertical: 12 }} />;
      case "table":
        return <Table key={k} node={n} />;
      default:
        return n.children.length ? <View key={k}>{blocks(n.children, tight, k)}</View> : null;
    }
  });
}

export const Markdown = memo(function Markdown({ text, caret }: { text: string; caret?: boolean }) {
  const nodes = useMemo(() => {
    const t = tree(md.parse(text, {}));
    if (caret) addCaret(t);
    return t;
  }, [text, caret]);
  return <View>{blocks(nodes)}</View>;
});

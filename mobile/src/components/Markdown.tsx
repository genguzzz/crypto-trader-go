// Small native Markdown renderer for the bundled help articles (src/content/strategyDocs.ts):
// headings, paragraphs, bold, inline code, links, nested lists, blockquotes, tables, rules
// and fenced code blocks (monospace, horizontal scroll, light Python highlighting, copy button).
// Covers what web/src/docs uses; no HTML.
import { memo, useMemo, type ReactNode } from "react";
import { Platform, Pressable, ScrollView, Text, View, type TextStyle } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { copy } from "@/lib/device";
import { colors, fs } from "@/lib/theme";
import { tap, toast } from "./ui";

export const mono = Platform.select({ ios: "Menlo", android: "monospace", default: "ui-monospace, SFMono-Regular, Menlo, monospace" });

type ListItem = { text: string; children: Block[] };
type Block =
  | { t: "h"; level: number; text: string }
  | { t: "p"; text: string }
  | { t: "code"; lang: string; text: string }
  | { t: "quote"; blocks: Block[] }
  | { t: "list"; ordered: boolean; start: number; items: ListItem[] }
  | { t: "table"; head: string[]; rows: string[][] }
  | { t: "hr" };

const LIST_RE = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/;

function splitRow(line: string): string[] {
  const s = line.trim().replace(/^\|/, "").replace(/\|$/, "");
  const out: string[] = [];
  let cur = "";
  let inCode = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === "\\" && s[i + 1] === "|") { cur += "|"; i++; continue; }
    if (c === "`") inCode = !inCode;
    if (c === "|" && !inCode) { out.push(cur.trim()); cur = ""; continue; }
    cur += c;
  }
  out.push(cur.trim());
  return out;
}

function parseList(lines: string[]): Block {
  const first = lines[0].match(LIST_RE)!;
  const base = first[1].length;
  const ordered = /\d/.test(first[2]);
  const items: ListItem[] = [];
  let cur: { text: string; sub: string[] } | null = null;
  const flush = () => {
    if (cur) items.push({ text: cur.text, children: cur.sub.length ? parse(cur.sub.map((l) => l.slice(Math.min(l.search(/\S|$/), base + 2)))) : [] });
  };
  for (const l of lines) {
    const m = l.match(LIST_RE);
    if (m && m[1].length <= base) {
      flush();
      cur = { text: m[3], sub: [] };
    } else if (cur) {
      if (!cur.sub.length && !LIST_RE.test(l) && l.trim()) cur.text += " " + l.trim();
      else cur.sub.push(l);
    }
  }
  flush();
  return { t: "list", ordered, start: ordered ? parseInt(first[2], 10) : 1, items };
}

export function parse(src: string | string[]): Block[] {
  const lines = Array.isArray(src) ? src : src.replace(/\r\n/g, "\n").split("\n");
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i++; continue; }
    const fence = line.match(/^\s*(```+|~~~+)\s*([\w+-]*)/);
    if (fence) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith(fence[1])) body.push(lines[i++]);
      i++;
      out.push({ t: "code", lang: fence[2], text: body.join("\n") });
      continue;
    }
    const h = line.match(/^(#{1,6})\s+(.*)$/);
    if (h) { out.push({ t: "h", level: h[1].length, text: h[2].trim() }); i++; continue; }
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) { out.push({ t: "hr" }); i++; continue; }
    if (line.trim().startsWith("|") && /^\s*\|?\s*:?-{2,}/.test(lines[i + 1] ?? "")) {
      const head = splitRow(line);
      const rows: string[][] = [];
      i += 2;
      while (i < lines.length && lines[i].trim().startsWith("|")) rows.push(splitRow(lines[i++]));
      out.push({ t: "table", head, rows });
      continue;
    }
    if (/^\s*>/.test(line)) {
      const q: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) q.push(lines[i++].replace(/^\s*>\s?/, ""));
      out.push({ t: "quote", blocks: parse(q) });
      continue;
    }
    if (LIST_RE.test(line)) {
      const l: string[] = [];
      const indent = line.match(LIST_RE)![1].length;
      while (i < lines.length) {
        const x = lines[i];
        if (!x.trim()) {
          // a blank line ends the list unless the next line continues it (indented or another item)
          const nx = lines[i + 1] ?? "";
          if (nx.trim() && (nx.search(/\S/) > indent || (LIST_RE.test(nx) && nx.match(LIST_RE)![1].length === indent))) { i++; continue; }
          break;
        }
        if (l.length && !LIST_RE.test(x) && x.search(/\S/) <= indent && !/^\s/.test(x)) break;
        l.push(x);
        i++;
      }
      out.push(parseList(l));
      continue;
    }
    const p: string[] = [];
    while (i < lines.length && lines[i].trim() && !/^(#{1,6}\s|\s*```|\s*>|\s*\|)/.test(lines[i]) && !(p.length && LIST_RE.test(lines[i]))) p.push(lines[i++].trim());
    out.push({ t: "p", text: p.join(" ") });
  }
  return out;
}

// ---------- inline ----------

const INLINE = /(`+)([\s\S]+?)\1|\*\*([\s\S]+?)\*\*|\[([^\]]+)\]\(([^)\s]+)\)/g;

function inline(text: string, onLink: (href: string) => void, key = "i"): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let n = 0;
  for (const m of text.matchAll(INLINE)) {
    if (m.index! > last) out.push(text.slice(last, m.index));
    const k = `${key}.${n++}`;
    if (m[1]) out.push(<Text key={k} style={codeInline}>{`\u00a0${m[2].trim()}\u00a0`}</Text>);
    else if (m[3] !== undefined) out.push(<Text key={k} style={{ fontWeight: "700", color: colors.text }}>{inline(m[3], onLink, k)}</Text>);
    else if (m[4] !== undefined) {
      const href = m[5];
      out.push(<Text key={k} style={{ color: colors.primary }} onPress={() => { tap(); onLink(href); }}>{inline(m[4], onLink, k)}</Text>);
    }
    last = m.index! + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

const codeInline: TextStyle = { fontFamily: mono, fontSize: 13, color: colors.text, backgroundColor: colors.cardMuted };

// ---------- code highlighting (Python-ish; anything else stays plain) ----------

const PY_KW = new Set("and as assert async await break class continue def del elif else except False finally for from global if import in is lambda None nonlocal not or pass raise return self True try while with yield".split(" "));
const TOKEN = /(#.*$)|("""[\s\S]*?"""|'''[\s\S]*?'''|[rbfu]?"(?:\\.|[^"\\])*"|[rbfu]?'(?:\\.|[^'\\])*')|(\b\d+(?:\.\d+)?\b)|(\b[A-Za-z_]\w*\b)/gm;
const C = { comment: "#6a737d", string: "#a5d6a7", number: "#f7a600", keyword: "#c792ea", fn: "#82aaff" };

function highlight(code: string, lang: string): ReactNode[] {
  if (!/^(py|python|)$/.test(lang) || (!lang && !/\b(def|import|class|return)\b/.test(code))) return [code];
  const out: ReactNode[] = [];
  let last = 0;
  let n = 0;
  for (const m of code.matchAll(TOKEN)) {
    if (m.index! > last) out.push(code.slice(last, m.index));
    const color = m[1] ? C.comment : m[2] ? C.string : m[3] ? C.number : PY_KW.has(m[4]) ? C.keyword : code[m.index! + m[0].length] === "(" ? C.fn : null;
    out.push(color ? <Text key={n++} style={{ color }}>{m[0]}</Text> : m[0]);
    last = m.index! + m[0].length;
  }
  if (last < code.length) out.push(code.slice(last));
  return out;
}

export function CodeBlock({ text, lang = "" }: { text: string; lang?: string }) {
  const parts = useMemo(() => highlight(text, lang), [text, lang]);
  return (
    <View style={{ marginVertical: 8, borderRadius: 8, borderWidth: 1, borderColor: colors.border, backgroundColor: "#050505" }}>
      <View style={{ flexDirection: "row", alignItems: "center", paddingHorizontal: 10, paddingTop: 6 }}>
        <Text style={{ fontSize: fs.xs, color: colors.textDim, flex: 1 }}>{lang || "text"}</Text>
        <Pressable hitSlop={10} accessibilityLabel="复制代码" onPress={() => { tap(); void copy(text).then(() => toast("已复制代码")); }} style={{ flexDirection: "row", alignItems: "center", gap: 4 }}>
          <Ionicons name="copy-outline" size={13} color={colors.textFaint} />
          <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>复制</Text>
        </Pressable>
      </View>
      <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ paddingHorizontal: 12, paddingTop: 4, paddingBottom: 10 }}>
        <Text selectable style={{ fontFamily: mono, fontSize: 12, lineHeight: 18, color: "#d6d6d6" }}>{parts}</Text>
      </ScrollView>
    </View>
  );
}

// ---------- blocks ----------

const body: TextStyle = { fontSize: 15, lineHeight: 25, color: colors.textSoft };
const visLen = (s: string) => [...s.replace(/`|\*\*/g, "")].reduce((a, c) => a + (c.charCodeAt(0) > 0x2e80 ? 2 : 1), 0);

function Table({ head, rows, onLink }: { head: string[]; rows: string[][]; onLink: (h: string) => void }) {
  const widths = head.map((h, c) => Math.max(64, Math.min(280, 24 + 7.8 * Math.max(visLen(h), ...rows.map((r) => Math.min(visLen(r[c] ?? ""), 40))))));
  const cell = (s: string, c: number, header: boolean, k: string) => (
    <View key={k} style={{ width: widths[c], paddingHorizontal: 8, paddingVertical: 6, borderRightWidth: c < head.length - 1 ? 1 : 0, borderColor: colors.borderStrong }}>
      <Text style={{ fontSize: 13, lineHeight: 19, color: header ? colors.text : colors.textSoft, fontWeight: header ? "600" : "400" }}>{inline(s, onLink, k)}</Text>
    </View>
  );
  return (
    <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ marginVertical: 8 }}>
      <View style={{ borderWidth: 1, borderColor: colors.borderStrong, borderRadius: 6, overflow: "hidden" }}>
        <View style={{ flexDirection: "row", backgroundColor: colors.raised }}>{head.map((h, c) => cell(h, c, true, `h${c}`))}</View>
        {rows.map((r, i) => (
          <View key={i} style={{ flexDirection: "row", borderTopWidth: 1, borderColor: colors.borderStrong }}>{head.map((_, c) => cell(r[c] ?? "", c, false, `r${i}.${c}`))}</View>
        ))}
      </View>
    </ScrollView>
  );
}

function Blocks({ blocks, onLink, depth = 0 }: { blocks: Block[]; onLink: (h: string) => void; depth?: number }) {
  return (
    <>
      {blocks.map((b, i) => {
        switch (b.t) {
          case "h": {
            const size = [26, 20, 17, 15, 14, 13][b.level - 1];
            return (
              <View key={i} style={{ marginTop: b.level === 1 ? 0 : b.level === 2 ? 26 : 18, marginBottom: 8, paddingBottom: b.level === 2 ? 6 : 0, borderBottomWidth: b.level === 2 ? 1 : 0, borderColor: colors.border }}>
                <Text style={{ fontSize: size, lineHeight: size * 1.35, fontWeight: "700", color: colors.text }}>{inline(b.text, onLink, `h${i}`)}</Text>
              </View>
            );
          }
          case "p":
            return <Text key={i} style={[body, { marginVertical: 6 }]}>{inline(b.text, onLink, `p${i}`)}</Text>;
          case "code":
            return <CodeBlock key={i} text={b.text} lang={b.lang} />;
          case "hr":
            return <View key={i} style={{ height: 1, backgroundColor: colors.border, marginVertical: 20 }} />;
          case "quote":
            return (
              <View key={i} style={{ marginVertical: 8, paddingVertical: 4, paddingHorizontal: 12, borderLeftWidth: 3, borderColor: colors.primary, backgroundColor: colors.raised, borderTopRightRadius: 6, borderBottomRightRadius: 6 }}>
                <Blocks blocks={b.blocks} onLink={onLink} depth={depth + 1} />
              </View>
            );
          case "table":
            return <Table key={i} head={b.head} rows={b.rows} onLink={onLink} />;
          case "list":
            return (
              <View key={i} style={{ marginVertical: 4 }}>
                {b.items.map((it, j) => (
                  <View key={j} style={{ flexDirection: "row", gap: 6, marginVertical: 2 }}>
                    <Text style={[body, { width: b.ordered ? 22 : 14, textAlign: b.ordered ? "right" : "center", color: b.ordered ? colors.textFaint : colors.primary }]}>{b.ordered ? `${b.start + j}.` : depth % 2 ? "◦" : "•"}</Text>
                    <View style={{ flex: 1 }}>
                      <Text style={body}>{inline(it.text, onLink, `l${i}.${j}`)}</Text>
                      {it.children.length ? <Blocks blocks={it.children} onLink={onLink} depth={depth + 1} /> : null}
                    </View>
                  </View>
                ))}
              </View>
            );
        }
        return null;
      })}
    </>
  );
}

export const Markdown = memo(function Markdown({ source, onLink }: { source: string; onLink: (href: string) => void }) {
  const blocks = useMemo(() => parse(source), [source]);
  return <Blocks blocks={blocks} onLink={onLink} />;
});

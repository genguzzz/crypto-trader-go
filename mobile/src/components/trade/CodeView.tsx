// Read-only Python source with line numbers and syntax colors (strategy code; the web edits it in
// CodeMirror). A small line tokenizer: comments, strings (incl. triple-quoted blocks), keywords,
// builtins, numbers, decorators, class / def names. Lines with diagnostics get a red or amber mark.
import { memo, useMemo } from "react";
import { Platform, ScrollView, Text, View } from "react-native";
import type { Diagnostic } from "@/lib/tradeApi";
import { colors } from "@/lib/theme";

const C = { kw: "#c792ea", str: "#c3e88d", com: "#6a737d", num: "#f78c6c", fn: "#82aaff", cls: "#ffcb6b", dec: "#bcff2f", self: "#ff5370", text: "#d4d4d4", ln: "#4b4b4b" };
const KW = new Set("False None True and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield match case".split(" "));
const BUILTIN = new Set("abs all any bool dict enumerate filter float int len list map max min print range round set sorted str sum tuple zip isinstance getattr hasattr super".split(" "));
const font = Platform.select({ ios: "Menlo", default: "monospace" });

type Tok = { t: string; c: string };

function tokenize(code: string): Tok[][] {
  const out: Tok[][] = [];
  let inTriple: string | null = null;
  for (const line of code.replace(/\t/g, "    ").split("\n")) {
    const toks: Tok[] = [];
    let i = 0;
    let prev = "";
    const push = (t: string, c: string) => { if (t) toks.push({ t, c }); };
    while (i < line.length) {
      if (inTriple) {
        const end = line.indexOf(inTriple, i);
        if (end < 0) { push(line.slice(i), C.str); i = line.length; break; }
        push(line.slice(i, end + 3), C.str);
        i = end + 3;
        inTriple = null;
        continue;
      }
      const rest = line.slice(i);
      let m: RegExpMatchArray | null;
      if (rest[0] === "#") { push(rest, C.com); break; }
      if ((m = rest.match(/^[rbfuRBFU]{0,2}("""|''')/))) {
        const q = m[1];
        const end = rest.indexOf(q, m[0].length);
        if (end < 0) { push(rest, C.str); inTriple = q; break; }
        push(rest.slice(0, end + 3), C.str);
        i += end + 3;
        continue;
      }
      if ((m = rest.match(/^[rbfuRBFU]{0,2}("([^"\\]|\\.)*"?|'([^'\\]|\\.)*'?)/))) { push(m[0], C.str); i += m[0].length; continue; }
      if ((m = rest.match(/^@[\w.]+/))) { push(m[0], C.dec); i += m[0].length; continue; }
      if ((m = rest.match(/^(0x[\da-fA-F_]+|\d[\d_]*\.?\d*(e[+-]?\d+)?)/))) { push(m[0], C.num); i += m[0].length; continue; }
      if ((m = rest.match(/^[A-Za-z_]\w*/))) {
        const w = m[0];
        const c = prev === "def" ? C.fn : prev === "class" ? C.cls : KW.has(w) ? C.kw : w === "self" || w === "ctx" ? C.self : BUILTIN.has(w) ? C.fn : /^[A-Z]/.test(w) ? C.cls : C.text;
        push(w, c);
        prev = w;
        i += w.length;
        continue;
      }
      m = rest.match(/^\s+/);
      const t = m ? m[0] : rest[0];
      push(t, C.text);
      if (!m) prev = "";
      i += t.length;
    }
    out.push(toks);
  }
  return out;
}

export const CodeView = memo(function CodeView({ code, diagnostics }: { code: string; diagnostics?: Diagnostic[] }) {
  const lines = useMemo(() => tokenize(code), [code]);
  const marks = useMemo(() => {
    const m = new Map<number, Diagnostic>();
    for (const d of diagnostics ?? []) if (!m.has(d.line) || d.severity === "error") m.set(d.line, d);
    return m;
  }, [diagnostics]);
  const w = String(lines.length).length * 8 + 10;
  return (
    <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ backgroundColor: colors.bg, borderRadius: 8, borderWidth: 1, borderColor: colors.border }} contentContainerStyle={{ paddingVertical: 8, paddingRight: 14 }}>
      <View>
        {lines.map((toks, i) => {
          const d = marks.get(i + 1);
          return (
            <View key={i} style={{ flexDirection: "row", backgroundColor: d ? (d.severity === "error" ? colors.downBg : colors.warnBg) : "transparent" }}>
              <Text style={{ width: w, textAlign: "right", paddingRight: 8, fontFamily: font, fontSize: 11, lineHeight: 17, color: d ? (d.severity === "error" ? colors.down : colors.warn) : C.ln }}>{i + 1}</Text>
              <Text selectable style={{ fontFamily: font, fontSize: 11, lineHeight: 17, color: C.text }}>
                {toks.length ? toks.map((t, j) => <Text key={j} style={{ color: t.c }}>{t.t}</Text>) : " "}
              </Text>
            </View>
          );
        })}
      </View>
    </ScrollView>
  );
});

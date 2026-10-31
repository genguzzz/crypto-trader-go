// Strategy code editor for the phone (the web uses CodeMirror). Keyboard down: the colored CodeView,
// tap a line to edit there. Editing: a plain multiline TextInput (monospace, no autocorrect), new
// lines keep the indentation (+4 after a colon), and a key row above the keyboard types what iOS
// hides (tab, brackets, quotes, : = . # _), moves the caret and closes the keyboard. A strip on top
// shows the caret line and its diagnostic. `jump` (line + nonce, seen once per mount; the parent
// clears it on tab changes) opens the editor at a line.
import { useEffect, useMemo, useRef, useState } from "react";
import { InputAccessoryView, Keyboard, KeyboardAvoidingView, Platform, Pressable, ScrollView, Text, TextInput, View, type TextInputInstance, type TextInputSelectionChangeEvent } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import type { Diagnostic } from "@/lib/tradeApi";
import { haptic } from "@/lib/device";
import { colors, fs } from "@/lib/theme";
import { CodeView, codeFont } from "./CodeView";

const ACCESSORY = "ctg-code-keys";
const INDENT = "    ";
const KEYS: { k: string; label?: string; icon?: keyof typeof Ionicons.glyphMap }[] = [
  { k: "tab", label: "⇥" }, { k: "outdent", label: "⇤" }, { k: "left", icon: "chevron-back" }, { k: "right", icon: "chevron-forward" },
  ...["(", ")", ":", "=", ".", ",", "\"", "'", "[", "]", "{", "}", "_", "#", "+", "-", "*", "/", "<", ">"].map((k) => ({ k })),
];

type Sel = { start: number; end: number };

const lineAt = (code: string, pos: number) => {
  let n = 1;
  for (let i = 0; i < pos && i < code.length; i++) if (code.charCodeAt(i) === 10) n++;
  return n;
};
const lineStart = (code: string, line: number) => {
  let pos = 0;
  for (let n = 1; n < line; n++) {
    const i = code.indexOf("\n", pos);
    if (i < 0) return code.length;
    pos = i + 1;
  }
  return pos;
};

/** A single "\n" typed into prev → the text with the line's indentation added after it, and the caret. */
function autoIndent(prev: string, next: string): { text: string; caret: number } | null {
  if (next.length !== prev.length + 1) return null;
  let i = 0;
  while (i < prev.length && prev[i] === next[i]) i++;
  if (next[i] !== "\n" || next.slice(i + 1) !== prev.slice(i)) return null;
  const line = prev.slice(prev.lastIndexOf("\n", i - 1) + 1, i);
  let indent = /^[ \t]*/.exec(line)![0];
  if (/:\s*(#.*)?$/.test(line)) indent += INDENT;
  if (!indent) return null;
  return { text: next.slice(0, i + 1) + indent + next.slice(i + 1), caret: i + 1 + indent.length };
}

export function CodeEditor({ code, onChange, diagnostics, jump, onEditing }: {
  code: string; onChange: (c: string) => void; diagnostics: Diagnostic[]; jump?: { line: number; n: number } | null; onEditing?: (on: boolean) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [caretLine, setCaretLine] = useState(1);
  const input = useRef<TextInputInstance>(null);
  const sel = useRef<Sel>({ start: 0, end: 0 });
  const pendingCaret = useRef<number | null>(null);
  const [focusReq, setFocusReq] = useState({ line: 1, n: 0 });
  const [seenJump, setSeenJump] = useState(0);

  const marks = useMemo(() => {
    const m = new Map<number, Diagnostic>();
    for (const d of diagnostics) if (!m.has(d.line) || d.severity === "error") m.set(d.line, d);
    return m;
  }, [diagnostics]);

  const place = (pos: number, text = code) => {
    sel.current = { start: pos, end: pos };
    input.current?.setSelection?.(pos, pos); // missing on react-native-web
    setCaretLine(lineAt(text, pos));
  };
  const edit = (line: number) => {
    setFocusReq((r) => ({ line, n: r.n + 1 }));
    setCaretLine(line);
    setEditing(true);
  };
  if (jump && jump.n !== seenJump) { // a diagnostic tapped on the 问题 tab
    setSeenJump(jump.n);
    edit(jump.line);
  }
  useEffect(() => { onEditing?.(editing); }, [editing, onEditing]);
  useEffect(() => {
    if (!editing || !focusReq.n) return;
    // caret first, then focus: becoming first responder scrolls the caret into view
    const t = setTimeout(() => { place(lineStart(code, focusReq.line)); input.current?.focus(); }, 30);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editing, focusReq]);
  // a programmatic change (auto-indent, key row) moves the caret once the new text is on screen
  useEffect(() => {
    if (pendingCaret.current == null) return;
    const p = pendingCaret.current;
    pendingCaret.current = null;
    requestAnimationFrame(() => place(p, code));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [code]);

  const change = (next: string) => {
    const ai = autoIndent(code, next);
    if (ai) { pendingCaret.current = ai.caret; onChange(ai.text); } else onChange(next);
  };
  const onSel = (e: TextInputSelectionChangeEvent) => {
    const s = e.nativeEvent.selection;
    sel.current = s;
    const l = lineAt(code, s.start);
    setCaretLine((x) => (x === l ? x : l));
  };
  const insert = (text: string, caret = text.length) => {
    const c = code;
    const { start, end } = sel.current;
    pendingCaret.current = start + caret;
    onChange(c.slice(0, start) + text + c.slice(end));
  };
  const key = (k: string) => {
    void haptic("light");
    const c = code;
    const { start, end } = sel.current;
    if (k === "tab") insert(INDENT);
    else if (k === "outdent") {
      const ls = c.lastIndexOf("\n", start - 1) + 1;
      const n = /^ {0,4}/.exec(c.slice(ls))![0].length || (c[ls] === "\t" ? 1 : 0);
      if (!n) return;
      pendingCaret.current = Math.max(ls, start - n);
      onChange(c.slice(0, ls) + c.slice(ls + n));
    } else if (k === "left") place(Math.max(0, (start === end ? start : start + 1) - 1));
    else if (k === "right") place(Math.min(c.length, end + (start === end ? 1 : 0)));
    else insert(k);
  };

  const d = marks.get(caretLine);
  const keyRow = (
    <View style={{ flexDirection: "row", alignItems: "center", backgroundColor: colors.raised, borderTopWidth: 1, borderColor: colors.borderStrong }}>
      <ScrollView horizontal keyboardShouldPersistTaps="always" showsHorizontalScrollIndicator={false} contentContainerStyle={{ paddingHorizontal: 4, gap: 4, alignItems: "center", height: 44 }}>
        {KEYS.map((x) => (
          <Pressable key={x.k} accessibilityLabel={x.k} onPress={() => key(x.k)}
            style={({ pressed }) => ({ minWidth: 34, height: 34, paddingHorizontal: 6, borderRadius: 6, alignItems: "center", justifyContent: "center", backgroundColor: pressed ? colors.borderStrong : colors.cardMuted })}>
            {x.icon ? <Ionicons name={x.icon} size={16} color={colors.text} /> : <Text style={{ fontFamily: codeFont, fontSize: 16, color: colors.text }}>{x.label ?? x.k}</Text>}
          </Pressable>
        ))}
      </ScrollView>
      <Pressable accessibilityLabel="收起键盘" hitSlop={6} onPress={() => { void haptic("light"); Keyboard.dismiss(); input.current?.blur(); }}
        style={{ height: 44, paddingHorizontal: 12, justifyContent: "center", borderLeftWidth: 1, borderColor: colors.borderStrong }}>
        <Text style={{ fontSize: fs.md, fontWeight: "600", color: colors.primary }}>完成</Text>
      </Pressable>
    </View>
  );

  if (!editing) {
    return (
      <ScrollView style={{ flex: 1 }} contentContainerStyle={{ padding: 12, paddingBottom: 48 }}>
        <CodeView code={code} diagnostics={diagnostics} onLinePress={edit} />
        <Text style={{ marginTop: 8, fontSize: fs.xs, color: colors.textFaint }}>点代码某一行开始编辑；停顿后自动保存为草稿并检查。</Text>
      </ScrollView>
    );
  }
  return (
    <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6, paddingHorizontal: 12, paddingVertical: 5, borderBottomWidth: 1, borderColor: colors.border, backgroundColor: d ? (d.severity === "error" ? colors.downBg : colors.warnBg) : colors.card }}>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint, fontFamily: codeFont }}>第 {caretLine} 行</Text>
        {d ? <Ionicons name={d.severity === "error" ? "alert-circle" : "warning"} size={13} color={d.severity === "error" ? colors.down : colors.warn} /> : null}
        <Text style={{ flex: 1, fontSize: fs.xs, color: d ? colors.text : colors.textDim }} numberOfLines={2}>{d ? d.message : marks.size ? `共 ${marks.size} 行有问题` : ""}</Text>
      </View>
      <TextInput
        ref={input}
        value={code}
        onChangeText={change}
        onSelectionChange={onSel}
        onBlur={() => setEditing(false)}
        multiline
        scrollEnabled
        autoCorrect={false}
        autoCapitalize="none"
        autoComplete="off"
        spellCheck={false}
        smartInsertDelete={false}
        textContentType="none"
        keyboardAppearance="dark"
        inputAccessoryViewID={Platform.OS === "ios" ? ACCESSORY : undefined}
        style={{ flex: 1, padding: 12, paddingTop: 10, fontFamily: codeFont, fontSize: 13, lineHeight: 19, color: colors.text, textAlignVertical: "top", backgroundColor: colors.bg }}
      />
      {Platform.OS === "ios" ? <InputAccessoryView nativeID={ACCESSORY}>{keyRow}</InputAccessoryView> : keyRow}
    </KeyboardAvoidingView>
  );
}

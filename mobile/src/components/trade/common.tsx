// Small building blocks shared by the trading pages: labelled tags, KPI grid, info sheets (web
// InfoTip), number inputs (web NumInput), setting rows, form sheets that stay above the keyboard.
import { useState, type ReactNode } from "react";
import { KeyboardAvoidingView, Modal, Platform, Pressable, ScrollView, Switch, Text, TextInput, View, type StyleProp, type TextStyle, type ViewStyle } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { haptic } from "@/lib/device";
import type { Lbl } from "@/lib/tradeMeta";
import { colors, fs, radius, tnum } from "@/lib/theme";
import { Sheet, Tag, tap } from "../ui";

export const mono: TextStyle = { fontFamily: Platform.select({ ios: "Menlo", default: "monospace" }), fontSize: fs.xs };
export const toneText = (t: string | undefined) => (t === "up" ? colors.up : t === "down" ? colors.down : t === "warn" ? colors.warn : colors.text);

/** Tag from a web label ({t, c}). */
export function LTag({ l, fallback }: { l?: Lbl; fallback?: string }) {
  return <Tag text={l?.t ?? fallback ?? "—"} tone={l?.c || undefined} />;
}

/** "i" button that opens the web InfoTip text in a sheet. Paragraphs: array items; bold lead: [b, rest]. */
export function InfoTip({ title, items }: { title: string; items: (string | [string, string])[] }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Pressable hitSlop={10} accessibilityLabel="说明" onPress={() => { tap(); setOpen(true); }}>
        <Ionicons name="information-circle-outline" size={17} color={colors.textFaint} />
      </Pressable>
      <Sheet visible={open} onClose={() => setOpen(false)} title={title} scroll>
        <View style={{ gap: 10, paddingBottom: 8 }}>
          {items.map((x, i) => (
            <Text key={i} style={{ fontSize: fs.md, lineHeight: 21, color: colors.textSoft }}>
              {Array.isArray(x) ? <Text style={{ color: colors.text, fontWeight: "600" }}>{x[0]}</Text> : null}
              {Array.isArray(x) ? x[1] : x}
            </Text>
          ))}
        </View>
      </Sheet>
    </>
  );
}

export interface Kpi { label: string; value: string; sub?: string; tone?: string; big?: boolean }
/** Two-column grid of numbers (web KpiRow / report metrics). */
export function KpiGrid({ items, cols = 2 }: { items: Kpi[]; cols?: number }) {
  return (
    <View style={{ flexDirection: "row", flexWrap: "wrap", borderRadius: radius.md, borderWidth: 1, borderColor: colors.border, overflow: "hidden", backgroundColor: colors.border, gap: 1 }}>
      {items.map((k) => (
        <View key={k.label} style={{ flexGrow: 1, flexBasis: `${100 / cols - 1}%`, backgroundColor: colors.card, paddingVertical: 10, paddingHorizontal: 12, gap: 2 }}>
          <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{k.label}</Text>
          <Text style={[{ fontSize: k.big ? fs.x2 : fs.xl, fontWeight: k.big ? "700" : "600", color: toneText(k.tone) }, tnum]} numberOfLines={1} adjustsFontSizeToFit>{k.value}</Text>
          {k.sub ? <Text style={{ fontSize: fs.xs, color: colors.textSoft }} numberOfLines={1}>{k.sub}</Text> : null}
        </View>
      ))}
    </View>
  );
}

/** Label + value pair used in cards (web .m / dl rows). */
export function Stat({ label, value, tone, style }: { label: string; value: string; tone?: string; style?: StyleProp<ViewStyle> }) {
  return (
    <View style={[{ gap: 1, minWidth: 0 }, style]}>
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{label}</Text>
      <Text style={[{ fontSize: fs.md, fontWeight: "500", color: tone ? toneText(tone) : colors.textSoft }, tnum]} numberOfLines={1}>{value}</Text>
    </View>
  );
}

export function Muted({ children, style }: { children: ReactNode; style?: StyleProp<TextStyle> }) {
  return <Text style={[{ fontSize: fs.sm, color: colors.textFaint, lineHeight: 18 }, style]}>{children}</Text>;
}

export function Banner({ text, tone = "bad" }: { text: string; tone?: "bad" | "warn" | "ok" }) {
  const [fg, bg] = tone === "bad" ? [colors.down, colors.downBg] : tone === "warn" ? [colors.warn, colors.warnBg] : [colors.up, colors.upBg];
  return (
    <View style={{ padding: 10, borderRadius: radius.sm, backgroundColor: bg }}>
      <Text style={{ fontSize: fs.md, color: fg, lineHeight: 19 }}>{text}</Text>
    </View>
  );
}

/** Number field (web NumInput): `scale` shows fractions as percent (0.3 → 30). NaN while the text isn't a number. */
export function NumInput({ value, onChange, unit, scale = 1, width = 84, integer, placeholder }: {
  value: number; onChange: (v: number) => void; unit?: string; scale?: number; width?: number; integer?: boolean; placeholder?: string;
}) {
  const show = (v: number) => (isFinite(v) ? String(+(v * scale).toPrecision(10)) : "");
  const [text, setText] = useState(show(value));
  const [seen, setSeen] = useState(value);
  if (!Object.is(seen, value)) {
    // follow outside changes (presets), keep what the user is typing when it parses to the same value
    setSeen(value);
    if (isFinite(value) && !(text.trim() !== "" && Number(text.replace(",", ".")) === +(value * scale).toPrecision(10))) setText(show(value));
  }
  return (
    <View style={{ flexDirection: "row", alignItems: "center", height: 34, width, paddingHorizontal: 8, borderRadius: radius.sm, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.cardMuted }}>
      <TextInput
        value={text}
        placeholder={placeholder}
        placeholderTextColor={colors.textDim}
        onChangeText={(t) => {
          setText(t);
          const n = t.trim() === "" ? NaN : Number(t.replace(",", "."));
          onChange(isFinite(n) ? n / scale : NaN);
        }}
        keyboardType={integer ? "number-pad" : "decimal-pad"}
        returnKeyType="done"
        selectTextOnFocus
        style={[{ flex: 1, color: colors.text, fontSize: fs.lg, padding: 0 }, tnum]}
      />
      {unit ? <Text style={{ fontSize: fs.sm, color: colors.textFaint, marginLeft: 4 }}>{unit}</Text> : null}
    </View>
  );
}

export function TextField({ value, onChange, placeholder, maxLength = 40 }: { value: string; onChange: (v: string) => void; placeholder?: string; maxLength?: number }) {
  return (
    <TextInput
      value={value}
      onChangeText={onChange}
      placeholder={placeholder}
      placeholderTextColor={colors.textDim}
      maxLength={maxLength}
      style={{ height: 38, paddingHorizontal: 10, borderRadius: radius.sm, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.cardMuted, color: colors.text, fontSize: fs.lg }}
    />
  );
}

export function Toggle({ value, onChange }: { value: boolean; onChange: (v: boolean) => void }) {
  return <Switch value={value} onValueChange={(v) => { void haptic("select"); onChange(v); }} trackColor={{ true: colors.primary, false: colors.cardMuted }} thumbColor={colors.white} ios_backgroundColor={colors.cardMuted} />;
}

/** Setting row (web SettingRow): label, description, error, control on the right or below. */
export function FieldRow({ label, desc, error, changed, children, stack }: { label: string; desc?: ReactNode; error?: string; changed?: boolean; children: ReactNode; stack?: boolean }) {
  return (
    <View style={{ paddingVertical: 11, borderBottomWidth: 1, borderColor: colors.border, gap: 6 }}>
      <View style={{ flexDirection: stack ? "column" : "row", alignItems: stack ? "stretch" : "center", gap: stack ? 8 : 12 }}>
        <View style={{ flex: stack ? undefined : 1, gap: 2 }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <Text style={{ fontSize: fs.lg, color: colors.text, fontWeight: "500" }}>{label}</Text>
            {changed ? <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: colors.primary }} /> : null}
          </View>
          {desc ? (typeof desc === "string" ? <Text style={{ fontSize: fs.xs, color: colors.textFaint, lineHeight: 16 }}>{desc}</Text> : desc) : null}
        </View>
        <View style={{ alignItems: stack ? "flex-start" : "flex-end" }}>{children}</View>
      </View>
      {error ? <Text style={{ fontSize: fs.xs, color: colors.down }}>{error}</Text> : null}
    </View>
  );
}

export function GroupTitle({ title, right }: { title: string; right?: ReactNode }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 6, marginTop: 16, paddingBottom: 4, borderBottomWidth: 1, borderColor: colors.border }}>
      <Text style={{ fontSize: fs.xs, fontWeight: "600", color: colors.textFaint, letterSpacing: 0.3 }}>{title}</Text>
      {right}
    </View>
  );
}

/** Bottom sheet for forms: tall, scrolls, stays above the keyboard, footer pinned. */
export function FormSheet({ visible, onClose, title, children, footer }: { visible: boolean; onClose: () => void; title: string; children: ReactNode; footer?: ReactNode }) {
  const insets = useSafeAreaInsets();
  return (
    <Modal visible={visible} transparent animationType="slide" onRequestClose={onClose} statusBarTranslucent>
      <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} style={{ flex: 1 }}>
        <Pressable style={{ flex: 1, minHeight: 60, backgroundColor: colors.backdrop }} onPress={onClose} />
        <View style={{ maxHeight: "92%", backgroundColor: colors.sheet, borderTopLeftRadius: 20, borderTopRightRadius: 20, paddingTop: 10, paddingBottom: insets.bottom + 10, borderTopWidth: 1, borderColor: colors.borderStrong }}>
          <View style={{ alignSelf: "center", width: 38, height: 5, borderRadius: 3, backgroundColor: colors.borderStrong, marginBottom: 10 }} />
          <View style={{ flexDirection: "row", alignItems: "center", paddingHorizontal: 16, marginBottom: 6 }}>
            <Text style={{ flex: 1, fontSize: 17, fontWeight: "700", color: colors.text }} numberOfLines={1}>{title}</Text>
            <Pressable hitSlop={10} accessibilityLabel="关闭" onPress={() => { tap(); onClose(); }}><Ionicons name="close" size={22} color={colors.textSoft} /></Pressable>
          </View>
          <ScrollView style={{ flexGrow: 0 }} contentContainerStyle={{ paddingHorizontal: 16, paddingBottom: 12 }} keyboardShouldPersistTaps="handled" showsVerticalScrollIndicator={false}>
            {children}
          </ScrollView>
          {footer ? <View style={{ paddingHorizontal: 16, paddingTop: 10, borderTopWidth: 1, borderColor: colors.border }}>{footer}</View> : null}
        </View>
      </KeyboardAvoidingView>
    </Modal>
  );
}

/** Small outline button (web button.sm). */
export function SmallButton({ label, icon, onPress, disabled, tone, active }: { label: string; icon?: keyof typeof Ionicons.glyphMap; onPress: () => void; disabled?: boolean; tone?: "primary" | "danger"; active?: boolean }) {
  const fg = tone === "primary" ? colors.onPrimary : tone === "danger" ? colors.down : active ? colors.primary : colors.text;
  return (
    <Pressable
      disabled={disabled}
      onPress={() => { tap(); onPress(); }}
      style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 4, height: 30, paddingHorizontal: 10, borderRadius: radius.sm, borderWidth: 1, borderColor: tone === "primary" ? colors.primary : active ? colors.primary : colors.borderStrong, backgroundColor: tone === "primary" ? colors.primary : pressed ? colors.raised : "transparent", opacity: disabled ? 0.45 : 1 })}
    >
      {icon ? <Ionicons name={icon} size={14} color={fg} /> : null}
      <Text style={{ fontSize: fs.sm, fontWeight: "500", color: fg }}>{label}</Text>
    </Pressable>
  );
}

/** "显示更多" footer for long lists. */
export function MoreButton({ label, onPress }: { label: string; onPress: () => void }) {
  return (
    <Pressable onPress={() => { tap(); onPress(); }} style={{ alignSelf: "center", paddingVertical: 10, paddingHorizontal: 16 }}>
      <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>{label}</Text>
    </Pressable>
  );
}

export function Divider() {
  return <View style={{ height: 1, backgroundColor: colors.border }} />;
}

// Form and explanation pieces for the drawer pages (rules, alerts, settings, Dev): labelled fields,
// text/number inputs, switch rows, a meter bar, and InfoTip (web components/InfoTip.vue: the ⓘ
// that opens an explanation; here a bottom sheet). P/B/Code style the rich text inside a tip.
import { useState, type ReactNode } from "react";
import { Pressable, Switch, Text, TextInput, View, type StyleProp, type TextInputProps, type TextStyle, type ViewStyle } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { haptic } from "@/lib/device";
import { colors, fs, radius, tnum } from "@/lib/theme";
import { mono } from "./Markdown";
import { Sheet, tap } from "./ui";

// ---------- InfoTip ----------

export function InfoTip({ title, children, label }: { title: string; children: ReactNode; label?: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Pressable hitSlop={10} accessibilityLabel={`${title}说明`} onPress={() => { tap(); setOpen(true); }} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 3, opacity: pressed ? 0.6 : 1 })}>
        <Ionicons name="information-circle-outline" size={17} color={open ? colors.primary : colors.textFaint} />
        {label ? <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>{label}</Text> : null}
      </Pressable>
      <Sheet visible={open} onClose={() => setOpen(false)} title={title} scroll>
        <View style={{ gap: 8, paddingBottom: 12 }}>{children}</View>
      </Sheet>
    </>
  );
}

const tipText: TextStyle = { fontSize: fs.lg, lineHeight: 23, color: colors.textSoft };
export const P = ({ children, style }: { children: ReactNode; style?: StyleProp<TextStyle> }) => <Text style={[tipText, style]}>{children}</Text>;
export const B = ({ children }: { children: ReactNode }) => <Text style={{ fontWeight: "700", color: colors.text }}>{children}</Text>;
export const Code = ({ children }: { children: ReactNode }) => <Text style={{ fontFamily: mono, fontSize: fs.md, color: colors.text, backgroundColor: colors.cardMuted }}> {children} </Text>;
export const H4 = ({ children }: { children: ReactNode }) => <Text style={{ fontSize: fs.xl, fontWeight: "700", color: colors.text, marginTop: 6 }}>{children}</Text>;

// ---------- fields ----------

export function Field({ label, desc, error, changed, children, right, style }: { label: string; desc?: ReactNode; error?: string; changed?: boolean; children?: ReactNode; right?: ReactNode; style?: StyleProp<ViewStyle> }) {
  return (
    <View style={[{ gap: 6, paddingVertical: 10, borderTopWidth: 1, borderColor: colors.border }, style]}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <View style={{ flex: 1, gap: 2 }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <Text style={{ fontSize: fs.lg, color: colors.text, fontWeight: "500" }}>{label}</Text>
            {changed ? <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: colors.warn }} /> : null}
          </View>
          {desc ? <Text style={{ fontSize: fs.sm, lineHeight: 17, color: colors.textFaint }}>{desc}</Text> : null}
        </View>
        {right}
      </View>
      {children}
      {error ? <Text style={{ fontSize: fs.sm, color: colors.down }}>{error}</Text> : null}
    </View>
  );
}

export function Input({ style, mono: isMono, invalid, ...rest }: TextInputProps & { mono?: boolean; invalid?: boolean }) {
  return (
    <TextInput
      placeholderTextColor={colors.textDim}
      autoCapitalize="none"
      autoCorrect={false}
      spellCheck={false}
      {...rest}
      style={[{ height: 40, borderRadius: radius.sm + 2, borderWidth: 1, borderColor: invalid ? colors.down : colors.borderStrong, backgroundColor: colors.cardMuted, color: colors.text, paddingHorizontal: 10, fontSize: fs.lg }, isMono && { fontFamily: mono, fontSize: fs.md }, style]}
    />
  );
}

/** Number input that keeps the typed text while editing; onChange gets NaN for an empty/invalid entry. */
export function NumInput({ value, onChange, unit, width = 110, decimal = true, placeholder, invalid }: { value: number | null | undefined; onChange: (v: number) => void; unit?: string; width?: number; decimal?: boolean; placeholder?: string; invalid?: boolean }) {
  const show = (v: number | null | undefined) => (v == null || !isFinite(v) ? "" : String(v));
  const [text, setText] = useState(show(value));
  const [seen, setSeen] = useState(value);
  if (!Object.is(seen, value)) {
    // follow outside changes (presets, revert) unless they equal what is typed
    setSeen(value);
    if (!(text !== "" && Number(text) === value)) setText(show(value));
  }
  return (
    <View style={{ flexDirection: "row", alignItems: "center", width, height: 36, borderRadius: radius.sm + 2, borderWidth: 1, borderColor: invalid ? colors.down : colors.borderStrong, backgroundColor: colors.cardMuted }}>
      <TextInput
        value={text}
        onChangeText={(t) => { setText(t); onChange(t.trim() === "" ? NaN : Number(t)); }}
        keyboardType={decimal ? "decimal-pad" : "number-pad"}
        placeholder={placeholder}
        placeholderTextColor={colors.textDim}
        selectTextOnFocus
        style={[{ flex: 1, minWidth: 0, width: "100%", height: 36, paddingHorizontal: 8, color: colors.text, fontSize: fs.lg, textAlign: "right" }, tnum]}
      />
      {unit ? <Text style={{ paddingRight: 8, color: colors.textFaint, fontSize: fs.sm }}>{unit}</Text> : null}
    </View>
  );
}

export function Toggle({ value, onChange, disabled }: { value: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <Switch
      value={value}
      disabled={disabled}
      onValueChange={(v) => { void haptic("select"); onChange(v); }}
      trackColor={{ false: colors.cardMuted, true: colors.up }}
      thumbColor={colors.white}
      ios_backgroundColor={colors.cardMuted}
    />
  );
}

export function Meter({ value, max, height = 6, tone }: { value: number; max: number; height?: number; tone?: "ok" }) {
  const f = max > 0 ? Math.max(0, Math.min(1, value / max)) : 0;
  const c = tone === "ok" ? colors.up : f >= 1 ? colors.down : f >= 0.9 ? colors.warn : colors.up;
  return (
    <View style={{ height, borderRadius: height, backgroundColor: colors.cardMuted, overflow: "hidden" }}>
      <View style={{ width: `${f * 100}%`, height, borderRadius: height, backgroundColor: c }} />
    </View>
  );
}

/** Result line under a form's buttons (web .res up/down). */
export function Result({ r }: { r: { ok: boolean; t: string } | null }) {
  if (!r) return null;
  return <Text style={{ fontSize: fs.sm, lineHeight: 18, color: r.ok ? colors.up : colors.down }}>{r.t}</Text>;
}

/** Section card heading with an icon, a tip and right-side content (web SettingsSection). */
export function SectionHead({ icon, title, tip, right }: { icon?: keyof typeof Ionicons.glyphMap; title: string; tip?: ReactNode; right?: ReactNode }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 4 }}>
      {icon ? <Ionicons name={icon} size={17} color={colors.textFaint} /> : null}
      <Text style={{ fontSize: fs.xl, fontWeight: "600", color: colors.text }}>{title}</Text>
      {tip ? <InfoTip title={title}>{tip}</InfoTip> : null}
      <View style={{ flex: 1 }} />
      {right}
    </View>
  );
}

/** Small text link (web RouterLink "… →"). */
export function LinkText({ label, onPress }: { label: string; onPress: () => void }) {
  return (
    <Pressable hitSlop={8} onPress={() => { tap(); onPress(); }}>
      {({ pressed }) => <Text style={{ fontSize: fs.sm, color: colors.primary, opacity: pressed ? 0.6 : 1 }}>{label}</Text>}
    </Pressable>
  );
}

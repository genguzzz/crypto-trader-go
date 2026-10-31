// Shared UI pieces (mediahub's ui.tsx, restyled with the web app's tokens). Every tap gets haptics,
// results use toast()/toastError(), destructive actions go through confirm().
import { useEffect, useState, useSyncExternalStore, type ReactNode } from "react";
import { ActivityIndicator, Alert, Animated, KeyboardAvoidingView, Modal, Platform, Pressable, RefreshControl, ScrollView, Text, View, type RefreshControlProps, type StyleProp, type TextProps, type TextStyle, type ViewStyle } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { FullWindowOverlay } from "react-native-screens";
import { useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { copy, haptic } from "@/lib/device";
import { colors, fs, radius, tnum } from "@/lib/theme";
import type { Tone } from "@/lib/fmt";
import { openDrawer } from "./drawerState";

export const tap = () => void haptic("light");
export type IconName = keyof typeof Ionicons.glyphMap;

// Space the system tab bar covers at the bottom of tab screens.
export const TAB_BAR_SPACE = 110;

export const toneColor = (t: Tone | undefined, neutral = colors.text) => (t === "up" ? colors.up : t === "down" ? colors.down : neutral);

export function Screen({ children, style }: { children: ReactNode; style?: StyleProp<ViewStyle> }) {
  return <View style={[{ flex: 1, backgroundColor: colors.bg }, style]}>{children}</View>;
}

// Top bar for pushed screens: back chevron, centered title, optional right actions.
export function TopBar({ title, sub, right, left }: { title?: string; sub?: string; right?: ReactNode; left?: ReactNode }) {
  const router = useRouter();
  const insets = useSafeAreaInsets();
  return (
    <View style={{ flexDirection: "row", alignItems: "center", paddingHorizontal: 12, paddingBottom: 8, paddingTop: insets.top + 4, backgroundColor: colors.bg, borderBottomWidth: 1, borderColor: colors.border }}>
      <Pressable hitSlop={12} accessibilityLabel="返回" onPress={() => { tap(); if (router.canGoBack()) router.back(); else router.replace("/"); }} style={{ width: 40, height: 36, justifyContent: "center" }}>
        <Ionicons name="chevron-back" size={26} color={colors.text} />
      </Pressable>
      {left ?? (
        <View style={{ flex: 1, alignItems: "center" }}>
          <Text style={{ fontSize: 17, fontWeight: "600", color: colors.text }} numberOfLines={1}>{title ?? ""}</Text>
          {sub ? <Text style={{ fontSize: fs.xs, color: colors.textFaint }} numberOfLines={1}>{sub}</Text> : null}
        </View>
      )}
      <View style={{ minWidth: 40, flexDirection: "row", justifyContent: "flex-end", alignItems: "center", gap: 14 }}>{right}</View>
    </View>
  );
}

// Header for tab screens: drawer button, large title, actions on the right.
export function TabHeader({ title, right, sub }: { title: string; right?: ReactNode; sub?: ReactNode }) {
  const insets = useSafeAreaInsets();
  return (
    <View style={{ paddingTop: insets.top + 6, paddingHorizontal: 16, paddingBottom: 6 }}>
      <View style={{ flexDirection: "row", alignItems: "center", minHeight: 40, gap: 12 }}>
        <Pressable
          hitSlop={10}
          accessibilityLabel="打开菜单"
          onPress={() => { tap(); openDrawer(); }}
          style={({ pressed }) => ({ width: 36, height: 36, borderRadius: 18, alignItems: "center", justifyContent: "center", backgroundColor: colors.card, borderWidth: 1, borderColor: colors.borderStrong, opacity: pressed ? 0.7 : 1 })}
        >
          <View style={{ gap: 4 }}>
            <View style={{ width: 15, height: 2, borderRadius: 1, backgroundColor: colors.text }} />
            <View style={{ width: 10, height: 2, borderRadius: 1, backgroundColor: colors.text }} />
          </View>
        </Pressable>
        <Text style={styles.h1}>{title}</Text>
        <View style={{ flex: 1 }} />
        <View style={{ flexDirection: "row", alignItems: "center", gap: 16 }}>{right}</View>
      </View>
      {sub ? <View style={{ marginTop: 2 }}>{typeof sub === "string" ? <Text style={styles.faint}>{sub}</Text> : sub}</View> : null}
    </View>
  );
}

export function IconButton({ name, onPress, size = 22, color, label }: { name: IconName; onPress: () => void; size?: number; color?: string; label?: string }) {
  return (
    <Pressable hitSlop={10} accessibilityLabel={label} onPress={() => { tap(); onPress(); }} style={({ pressed }) => ({ opacity: pressed ? 0.6 : 1 })}>
      <Ionicons name={name} size={size} color={color ?? colors.textSoft} />
    </Pressable>
  );
}

// Panel (web .panel): bg-1 with a hairline border.
export function Card({ children, style, onPress, onLongPress }: { children: ReactNode; style?: StyleProp<ViewStyle>; onPress?: () => void; onLongPress?: () => void }) {
  const base: ViewStyle = { backgroundColor: colors.card, borderRadius: radius.md, padding: 14, borderWidth: 1, borderColor: colors.border };
  if (!onPress && !onLongPress) return <View style={[base, style]}>{children}</View>;
  return (
    <Pressable onPress={onPress ? () => { tap(); onPress(); } : undefined} onLongPress={onLongPress ? () => { tap(); onLongPress(); } : undefined} style={({ pressed }) => [base, style, pressed && { backgroundColor: colors.raised }]}>
      {children}
    </Pressable>
  );
}

// Panel heading (web .panel h3) with an optional hint and right-side content.
export function PanelTitle({ title, hint, right, style }: { title: string; hint?: string; right?: ReactNode; style?: StyleProp<ViewStyle> }) {
  return (
    <View style={[{ flexDirection: "row", alignItems: "center", marginBottom: 10, gap: 8 }, style]}>
      <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text }}>{title}</Text>
      {hint ? <Text style={{ fontSize: fs.sm, color: colors.textFaint, flexShrink: 1 }} numberOfLines={1}>{hint}</Text> : null}
      <View style={{ flex: 1 }} />
      {right}
    </View>
  );
}

export function PillButton({ label, icon, onPress, variant = "primary", loading, disabled, style, small }: {
  label: string;
  icon?: IconName;
  onPress: () => void;
  variant?: "primary" | "ghost" | "danger" | "soft";
  loading?: boolean;
  disabled?: boolean;
  style?: StyleProp<ViewStyle>;
  small?: boolean;
}) {
  const bg = { primary: colors.primary, danger: colors.down, ghost: "transparent", soft: colors.cardMuted }[variant];
  const fg = variant === "primary" ? colors.onPrimary : variant === "danger" ? colors.white : colors.text;
  return (
    <Pressable
      disabled={disabled || loading}
      onPress={() => { tap(); onPress(); }}
      style={({ pressed }) => [
        { height: small ? 34 : 46, borderRadius: radius.md, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 7, paddingHorizontal: small ? 12 : 20, backgroundColor: bg, opacity: disabled ? 0.45 : pressed ? 0.85 : 1 },
        (variant === "ghost" || variant === "soft") && { borderWidth: 1, borderColor: colors.borderStrong },
        style,
      ]}
    >
      {loading ? <ActivityIndicator color={fg} /> : icon ? <Ionicons name={icon} size={small ? 15 : 18} color={fg} /> : null}
      <Text style={{ fontSize: small ? fs.md : fs.xl, fontWeight: "600", color: fg }}>{label}</Text>
    </Pressable>
  );
}

// Filter chip (web .fchip): outline when off, lime outline + tint when on.
export function Chip({ label, active, onPress, count, icon, disabled }: { label: string; active?: boolean; onPress?: () => void; count?: number; icon?: IconName; disabled?: boolean }) {
  return (
    <Pressable
      disabled={!onPress || disabled}
      onPress={() => { tap(); onPress?.(); }}
      style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 5, paddingHorizontal: 11, height: 30, borderRadius: 15, backgroundColor: active ? colors.primarySoft : pressed ? colors.raised : "transparent", borderWidth: 1, borderColor: active ? colors.primary : colors.borderStrong, opacity: disabled ? 0.4 : 1 })}
    >
      {icon ? <Ionicons name={icon} size={13} color={active ? colors.primary : colors.textFaint} /> : null}
      <Text style={{ fontSize: fs.sm, fontWeight: "500", color: active ? colors.text : colors.textSoft }} numberOfLines={1}>{label}</Text>
      {count !== undefined ? <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{count}</Text> : null}
    </Pressable>
  );
}

export function ChipRow({ children, style }: { children: ReactNode; style?: StyleProp<ViewStyle> }) {
  return (
    <ScrollView horizontal showsHorizontalScrollIndicator={false} style={[{ flexGrow: 0, flexShrink: 0 }, style]} contentContainerStyle={{ gap: 8, paddingHorizontal: 12, alignItems: "center" }}>
      {children}
    </ScrollView>
  );
}

// Segmented control (web ui/Segmented.vue).
export function Segmented<T extends string>({ value, options, onChange, style, small }: { value: T; options: { value: T; label: string; tone?: Tone }[]; onChange: (v: T) => void; style?: StyleProp<ViewStyle>; small?: boolean }) {
  return (
    <View style={[{ flexDirection: "row", backgroundColor: colors.card, borderRadius: 8, padding: 2, borderWidth: 1, borderColor: colors.borderStrong }, style]}>
      {options.map((o) => {
        const on = value === o.value;
        return (
          <Pressable key={o.value} onPress={() => { if (!on) { void haptic("select"); onChange(o.value); } }} style={{ flexGrow: 1, flexShrink: 1, flexBasis: "auto", minWidth: small ? 36 : 48, height: small ? 26 : 30, paddingHorizontal: 8, borderRadius: 6, alignItems: "center", justifyContent: "center", backgroundColor: on ? colors.cardMuted : "transparent" }}>
            <Text style={{ fontSize: small ? fs.sm : fs.md, fontWeight: on ? "600" : "500", color: on ? toneColor(o.tone, colors.text) : colors.textFaint }}>{o.label}</Text>
          </Pressable>
        );
      })}
    </View>
  );
}

// Tag (web .tag): neutral, or good / bad / warn / info tone.
export function Tag({ text, tone, icon }: { text: string; tone?: "good" | "bad" | "warn" | "info"; icon?: ReactNode }) {
  const c = { good: [colors.up, colors.upBg], bad: [colors.down, colors.downBg], warn: [colors.warn, colors.warnBg], info: [colors.primary, colors.primarySoft] }[tone ?? "good"];
  const [fg, bg] = tone ? c : [colors.textSoft, colors.cardMuted];
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 4, height: 20, paddingHorizontal: 6, borderRadius: 4, backgroundColor: bg, alignSelf: "flex-start" }}>
      {icon}
      <Text style={{ fontSize: fs.xs, fontWeight: "500", color: fg }}>{text}</Text>
    </View>
  );
}

// Status dot (web .dot ok/bad/warn).
export function Dot({ tone }: { tone: "ok" | "bad" | "warn" | "off" }) {
  const c = { ok: colors.up, bad: colors.down, warn: colors.warn, off: colors.textFaint }[tone];
  return <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: c, shadowColor: c, shadowOpacity: tone === "off" ? 0 : 0.8, shadowRadius: 3, shadowOffset: { width: 0, height: 0 } }} />;
}

export function EmptyState({ icon = "file-tray-outline", text, action }: { icon?: IconName; text: string; action?: ReactNode }) {
  return (
    <View style={{ alignItems: "center", justifyContent: "center", paddingVertical: 60, paddingHorizontal: 32, gap: 12 }}>
      <Ionicons name={icon} size={38} color={colors.textDim} />
      <Text style={[styles.body, { textAlign: "center" }]}>{text}</Text>
      {action}
    </View>
  );
}

export function ErrorBox({ text, onRetry }: { text: string; onRetry?: () => void }) {
  return (
    <View style={{ margin: 12, padding: 12, borderRadius: radius.md, backgroundColor: colors.downBg, flexDirection: "row", alignItems: "center", gap: 10 }}>
      <Ionicons name="alert-circle" size={18} color={colors.down} />
      <Text style={{ flex: 1, color: colors.text, fontSize: fs.md }}>{text}</Text>
      {onRetry ? <Pressable hitSlop={8} onPress={() => { tap(); onRetry(); }}><Text style={{ color: colors.primary, fontWeight: "600" }}>重试</Text></Pressable> : null}
    </View>
  );
}

export function Loading({ text }: { text?: string }) {
  return (
    <View style={{ paddingVertical: 40, alignItems: "center", gap: 10 }}>
      <ActivityIndicator color={colors.primary} />
      {text ? <Text style={styles.faint}>{text}</Text> : null}
    </View>
  );
}

// Text that copies itself on long press.
export function CopyText({ copy: value, label, children, ...rest }: TextProps & { copy?: string; label?: string; children?: ReactNode }) {
  const text = value ?? (typeof children === "string" || typeof children === "number" ? String(children) : "");
  return (
    <Text {...rest} onLongPress={text ? () => { void haptic("medium"); void copy(text).then(() => toast(`已复制${label ?? ""}`, text.length > 80 ? `${text.slice(0, 80)}…` : text)); } : undefined}>
      {children}
    </Text>
  );
}

export function Row({ label, value, onPress, right }: { label: string; value?: string | number | null; onPress?: () => void; right?: ReactNode }) {
  const body = (
    <View style={{ flexDirection: "row", alignItems: "center", paddingVertical: 10, gap: 12, borderBottomWidth: 1, borderColor: colors.border }}>
      <Text style={{ width: 96, fontSize: fs.md, color: colors.textFaint }}>{label}</Text>
      <CopyText label={label} style={{ flex: 1, fontSize: fs.lg, color: colors.text }}>{value === null || value === undefined || value === "" ? "—" : String(value)}</CopyText>
      {right}
      {onPress ? <Ionicons name="chevron-forward" size={16} color={colors.textFaint} /> : null}
    </View>
  );
  return onPress ? <Pressable onPress={() => { tap(); onPress(); }}>{body}</Pressable> : body;
}

// ---------- bottom sheet ----------

export function Sheet({ visible, onClose, title, children, scroll, footer, fill }: { visible: boolean; onClose: () => void; title?: string; children?: ReactNode; scroll?: boolean; footer?: ReactNode; fill?: boolean }) {
  // The native iOS page sheet (like the AI panel): swipe down to close (the content hands the drag to
  // the sheet once it is scrolled to the top), rounded glass edge, the screen behind scales back.
  // Content always scrolls, so tall details are never cut off; `scroll` is kept for callers.
  void scroll;
  const insets = useSafeAreaInsets();
  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" allowSwipeDismissal onRequestClose={onClose}>
      <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} style={{ flex: 1, backgroundColor: colors.sheet }}>
        <View style={{ paddingTop: 8, paddingHorizontal: 16, paddingBottom: 6 }}>
          <View style={{ alignSelf: "center", width: 38, height: 5, borderRadius: 3, backgroundColor: colors.borderStrong, marginBottom: 10 }} />
          <View style={{ flexDirection: "row", alignItems: "center", gap: 10, minHeight: 32 }}>
            <Text style={{ flex: 1, fontSize: 17, fontWeight: "700", color: colors.text }} numberOfLines={1}>{title ?? ""}</Text>
            <Pressable hitSlop={10} accessibilityLabel="关闭" onPress={() => { tap(); onClose(); }} style={({ pressed }) => ({ width: 30, height: 30, borderRadius: 15, alignItems: "center", justifyContent: "center", backgroundColor: colors.cardMuted, opacity: pressed ? 0.7 : 1 })}>
              <Ionicons name="close" size={17} color={colors.textSoft} />
            </Pressable>
          </View>
        </View>
        {fill ? (
          // the caller lays out its own scrolling (e.g. horizontal cards)
          <View style={{ flex: 1, paddingHorizontal: 16, paddingTop: 6, paddingBottom: footer ? 12 : insets.bottom + 12 }}>{children}</View>
        ) : (
          <ScrollView style={{ flex: 1 }} contentContainerStyle={{ paddingHorizontal: 16, paddingTop: 6, paddingBottom: footer ? 12 : insets.bottom + 20 }} showsVerticalScrollIndicator={false} keyboardShouldPersistTaps="handled" keyboardDismissMode="interactive">
            {children}
          </ScrollView>
        )}
        {footer ? <View style={{ paddingHorizontal: 16, paddingTop: 10, paddingBottom: insets.bottom + 12, borderTopWidth: 1, borderColor: colors.border }}>{footer}</View> : null}
      </KeyboardAvoidingView>
    </Modal>
  );
}

// One-of-many picker in a sheet (rule, sort field…).
export function PickerSheet<T extends string>({ visible, onClose, title, value, options, onPick }: {
  visible: boolean; onClose: () => void; title: string; value: T;
  options: { value: T; label: string; desc?: string; group?: string; badge?: string }[]; onPick: (v: T) => void;
}) {
  const heads = options.map((o, i) => (o.group && o.group !== options[i - 1]?.group ? o.group : ""));
  return (
    <Sheet visible={visible} onClose={onClose} title={title} scroll>
      {options.map((o, i) => {
        const head = heads[i];
        const on = o.value === value;
        return (
          <View key={o.value}>
            {head ? <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 10, marginBottom: 4, letterSpacing: 0.5 }}>{head}</Text> : null}
            <Pressable onPress={() => { void haptic("select"); onPick(o.value); onClose(); }} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 10, paddingVertical: 11, paddingHorizontal: 10, borderRadius: 8, backgroundColor: on ? colors.primarySoft : pressed ? colors.raised : "transparent" })}>
              <View style={{ flex: 1 }}>
                <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                  <Text style={{ fontSize: fs.lg, color: colors.text, fontWeight: on ? "600" : "400" }}>{o.label}</Text>
                  {o.badge ? <Tag text={o.badge} tone="warn" /> : null}
                </View>
                {o.desc ? <Text style={{ fontSize: fs.sm, color: colors.textFaint, marginTop: 2 }} numberOfLines={2}>{o.desc}</Text> : null}
              </View>
              {on ? <Ionicons name="checkmark" size={18} color={colors.primary} /> : null}
            </Pressable>
          </View>
        );
      })}
    </Sheet>
  );
}

export function confirm(title: string, message: string, ok: string, onOk: () => void, destructive = false) {
  Alert.alert(title, message, [
    { text: "取消", style: "cancel" },
    { text: ok, style: destructive ? "destructive" : "default", onPress: onOk },
  ]);
}

// ---------- toast ----------

type ToastAction = { label: string; onPress: () => void };
type ToastMsg = { id: number; title: string; body?: string; actions: ToastAction[]; error?: boolean };
let toastState: ToastMsg | null = null;
const toastListeners = new Set<() => void>();
let toastTimer: ReturnType<typeof setTimeout> | null = null;

function setToast(msg: ToastMsg | null) {
  toastState = msg;
  toastListeners.forEach((l) => l());
}

export function toast(title: string, body?: string, action?: ToastAction | ToastAction[], error = false) {
  const actions = action ? (Array.isArray(action) ? action : [action]) : [];
  setToast({ id: Date.now(), title, body, actions, error });
  void haptic(error ? "error" : "success");
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => setToast(null), actions.length || error ? 6000 : 3200);
}

export const toastError = (title: string, e: unknown) => toast(title, e instanceof Error ? e.message : String(e), undefined, true);

export function ToastHost() {
  const insets = useSafeAreaInsets();
  const msg = useSyncExternalStore((l) => { toastListeners.add(l); return () => toastListeners.delete(l); }, () => toastState);
  const [anim] = useState(() => new Animated.Value(0));
  useEffect(() => {
    Animated.spring(anim, { toValue: msg ? 1 : 0, useNativeDriver: true, friction: 8 }).start();
  }, [msg, anim]);
  if (!msg) return null;
  // A banner from the top, in its own window above everything (native sheets included: inside the
  // app's root view it was hidden behind the AI panel and every pop-up).
  const banner = (
    <Animated.View pointerEvents="box-none" style={{ position: "absolute", left: 12, right: 12, top: insets.top + 6, zIndex: 300, opacity: anim, transform: [{ translateY: anim.interpolate({ inputRange: [0, 1], outputRange: [-40, 0] }) }] }}>
      <View style={{ flexDirection: "row", alignItems: "flex-start", gap: 12, backgroundColor: colors.raised, borderRadius: 16, padding: 14, borderWidth: 1, borderColor: msg.error ? colors.down : colors.borderStrong, shadowColor: "#000", shadowOpacity: 0.5, shadowRadius: 18, shadowOffset: { width: 0, height: 8 } }}>
        <Ionicons name={msg.error ? "alert-circle" : "checkmark-circle"} size={22} color={msg.error ? colors.down : colors.up} />
        <View style={{ flex: 1 }}>
          <Text style={{ color: colors.text, fontWeight: "600", fontSize: fs.xl }} numberOfLines={2}>{msg.title}</Text>
          {msg.body ? <Text style={{ color: colors.textSoft, fontSize: fs.md, lineHeight: 19, marginTop: 2 }} numberOfLines={5}>{msg.body}</Text> : null}
          {msg.actions.length ? (
            <View style={{ flexDirection: "row", gap: 16, marginTop: 8 }}>
              {msg.actions.map((a) => (
                <Pressable key={a.label} hitSlop={8} onPress={() => { tap(); setToast(null); a.onPress(); }}>
                  <Text style={{ color: colors.primary, fontWeight: "600", fontSize: fs.lg }}>{a.label}</Text>
                </Pressable>
              ))}
            </View>
          ) : null}
        </View>
        <Pressable hitSlop={10} onPress={() => setToast(null)}>
          <Ionicons name="close" size={18} color={colors.textSoft} />
        </Pressable>
      </View>
    </Animated.View>
  );
  return Platform.OS === "ios" ? <FullWindowOverlay>{banner}</FullWindowOverlay> : banner;
}

export const styles = {
  h1: { fontSize: 24, fontWeight: "700", color: colors.text, letterSpacing: 0.2 } as TextStyle,
  title: { fontSize: fs.lg, fontWeight: "600", color: colors.text } as TextStyle,
  body: { fontSize: fs.lg, lineHeight: 21, color: colors.textSoft } as TextStyle,
  faint: { fontSize: fs.sm, color: colors.textFaint } as TextStyle,
  note: { fontSize: fs.sm, lineHeight: 19, color: colors.textFaint } as TextStyle,
};

// Pull-to-refresh that only spins for the user's own pull, not for background polling.
export function Pull({ onRefresh, ...rest }: { onRefresh: () => unknown } & Omit<RefreshControlProps, "refreshing" | "onRefresh">) {
  const [on, setOn] = useState(false);
  return (
    <RefreshControl
      {...rest}
      refreshing={on}
      tintColor={colors.primary}
      onRefresh={() => {
        setOn(true);
        void haptic("light");
        void Promise.resolve(onRefresh()).finally(() => setOn(false));
      }}
    />
  );
}

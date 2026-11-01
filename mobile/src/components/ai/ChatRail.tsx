// The conversation's ruler (like ChatGPT's): one tick per message that was delivered into the chat —
// the owner's questions, ⚡ signal wake-ups, trade outcomes — placed by where it sits in the
// conversation. Tap or drag along the rail to jump; while dragging, the nearest message is previewed.
import { useMemo, useState } from "react";
import { Animated, Text, View } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import { haptic } from "@/lib/device";
import { colors, fs } from "@/lib/theme";

export type RailKind = "user" | "signal" | "trade";
export interface RailMark { key: string; y: number; kind: RailKind; label: string }

const KIND: Record<RailKind, { color: string; icon: string }> = {
  user: { color: colors.primary, icon: "" },
  signal: { color: "#9b7df0", icon: "⚡ " },
  trade: { color: "#2fe0ff", icon: "✅ " },
};
const PAD = 10; // rail inset top / bottom

export function ChatRail({ marks, contentH, viewH, scrollY, onJump }: {
  marks: RailMark[]; contentH: number; viewH: number; scrollY: Animated.Value; onJump: (y: number) => void;
}) {
  const [railH, setRailH] = useState(0);
  const [active, setActive] = useState<number | null>(null); // index into marks while the finger is down
  const span = Math.max(1, railH - 2 * PAD);
  const max = Math.max(1, contentH - viewH);
  const pos = useMemo(() => marks.map((m) => PAD + Math.min(1, Math.max(0, m.y / Math.max(1, contentH))) * span), [marks, contentH, span]);
  const nearest = (fy: number) => {
    let best = 0;
    pos.forEach((p, i) => { if (Math.abs(p - fy) < Math.abs(pos[best] - fy)) best = i; });
    return best;
  };
  const pick = (fy: number) => {
    const i = nearest(fy);
    if (i !== active) { void haptic("select"); setActive(i); }
  };
  const jump = (fy: number) => {
    const i = nearest(fy);
    setActive(null);
    if (marks[i]) onJump(Math.max(0, marks[i].y - 8));
  };
  const tap = Gesture.Tap().runOnJS(true).onEnd((e) => jump(e.y));
  const pan = Gesture.Pan().runOnJS(true).activeOffsetY([-4, 4]).failOffsetX([-30, 30])
    .onBegin((e) => pick(e.y)).onUpdate((e) => pick(e.y)).onEnd((e) => jump(e.y)).onFinalize(() => setActive(null));
  if (marks.length < 2 || contentH <= viewH + 40) return null;

  // where the viewport is: a thin thumb sized like the visible share of the conversation
  const thumbH = Math.max(14, (viewH / contentH) * span);
  const thumbY = scrollY.interpolate({ inputRange: [0, max], outputRange: [PAD, PAD + span - thumbH], extrapolate: "clamp" });
  const a = active != null ? marks[active] : null;
  return (
    <>
      <GestureDetector gesture={Gesture.Exclusive(pan, tap)}>
        <View onLayout={(e) => setRailH(e.nativeEvent.layout.height)} style={{ position: "absolute", left: 0, top: 0, bottom: 0, width: 22, zIndex: 5 }}>
          <Animated.View pointerEvents="none" style={{ position: "absolute", left: 3, width: 3, height: thumbH, borderRadius: 2, backgroundColor: "rgba(255,255,255,0.14)", transform: [{ translateY: thumbY }] }} />
          {marks.map((m, i) => (
            <View key={m.key} pointerEvents="none" style={{ position: "absolute", left: 3, top: pos[i] - 1, width: active === i ? 14 : 8, height: 2, borderRadius: 1, backgroundColor: KIND[m.kind].color, opacity: active == null || active === i ? 0.95 : 0.45 }} />
          ))}
        </View>
      </GestureDetector>
      {a ? (
        <View pointerEvents="none" style={{ position: "absolute", left: 24, right: 40, top: Math.max(0, Math.min(railH - 56, pos[active!] - 20)), zIndex: 6, paddingHorizontal: 10, paddingVertical: 7, borderRadius: 10, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.raised, shadowColor: "#000", shadowOpacity: 0.5, shadowRadius: 10 }}>
          <Text numberOfLines={2} style={{ fontSize: fs.sm, lineHeight: 18, color: colors.text }}>
            <Text style={{ color: KIND[a.kind].color }}>{KIND[a.kind].icon}</Text>
            {a.label}
          </Text>
        </View>
      ) : null}
    </>
  );
}

/** One line of a message for the rail's preview. */
export function railLabel(text: string) {
  return text.replace(/\s+/g, " ").trim().slice(0, 80) || "（空）";
}

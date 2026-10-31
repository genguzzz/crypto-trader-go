// The global AI orb (web AiOrb.vue): floats centred above the tab bar on every screen. Tap opens
// the chat sheet (with the coin of an instrument page as context); press and hold talks — a capsule
// with a live waveform shows above it, slide up to cancel, release to transcribe and send.
import { useEffect, useState } from "react";
import { Animated, Easing, Text, View } from "react-native";
import { router, usePathname } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { Orb } from "@/components/ai/Orb";
import { toast } from "@/components/ui";
import { send, setContext, useAiSelect } from "@/lib/ai";
import { colors, fs } from "@/lib/theme";
import { useHoldToTalk } from "@/lib/voice";

const SIZE = 56;
const TAB_ROUTES = new Set(["/", "/watch", "/trade"]);
const HIDDEN = [/^\/ai/, /^\/chart\//, /^\/settings/, /^\/diagnostics/];

function open(pathname: string) {
  const m = /^\/instrument\/([A-Z0-9]+-USDT-SWAP)/i.exec(pathname);
  if (m) setContext(m[1].toUpperCase());
  router.push("/ai");
}

export function FloatingOrb() {
  const pathname = usePathname();
  const insets = useSafeAreaInsets();
  const running = useAiSelect((a) => a.running);
  const unread = useAiSelect((a) => a.unread);
  const talk = useHoldToTalk({
    onTap: () => open(pathname),
    onText: (t) => {
      open(pathname);
      void send(t);
    },
    onError: (msg) => toast(msg),
  });
  const rec = talk.phase === "rec";
  const busy = talk.phase === "busy";

  // breathing glow (idle) and expanding ripples (recording)
  const [breath] = useState(() => new Animated.Value(0));
  const [ripple] = useState(() => new Animated.Value(0));
  useEffect(() => {
    const l = Animated.loop(Animated.sequence([
      Animated.timing(breath, { toValue: 1, duration: 1600, easing: Easing.inOut(Easing.sin), useNativeDriver: true }),
      Animated.timing(breath, { toValue: 0, duration: 1600, easing: Easing.inOut(Easing.sin), useNativeDriver: true }),
    ]));
    l.start();
    return () => l.stop();
  }, [breath]);
  useEffect(() => {
    if (!rec) return;
    ripple.setValue(0);
    const l = Animated.loop(Animated.timing(ripple, { toValue: 1, duration: 1400, easing: Easing.out(Easing.quad), useNativeDriver: true }));
    l.start();
    return () => l.stop();
  }, [rec, ripple]);

  if (HIDDEN.some((r) => r.test(pathname))) return null;
  // above the tab bar on tab screens (bar ≈ 49 pt + home indicator), near the bottom elsewhere
  const bottom = TAB_ROUTES.has(pathname) ? insets.bottom + 49 + 14 : insets.bottom + 18;
  const scale = rec ? 1.2 + talk.level * 0.25 : talk.phase === "arming" ? 0.92 : 1;
  const glowScale = breath.interpolate({ inputRange: [0, 1], outputRange: [0.92, 1.12] });
  const glowOpacity = breath.interpolate({ inputRange: [0, 1], outputRange: [0.25, 0.55] });

  return (
    <>
      {rec || busy ? (
        <View pointerEvents="none" style={{ position: "absolute", left: 0, right: 0, bottom: bottom + SIZE + 46, alignItems: "center", zIndex: 40 }}>
          <View style={{ minWidth: 210, paddingHorizontal: 20, paddingTop: 14, paddingBottom: 12, borderRadius: 22, alignItems: "center", gap: 8, backgroundColor: "rgba(16,18,14,0.94)", borderWidth: 1, borderColor: talk.cancelZone ? "rgba(202,63,100,0.6)" : "rgba(188,255,47,0.28)" }}>
            {rec ? (
              <>
                <View style={{ flexDirection: "row", alignItems: "center", gap: 3, height: 36 }}>
                  {talk.levels.map((v, i) => (
                    <View key={i} style={{ width: 4, height: 4 + v * 30, borderRadius: 2, backgroundColor: talk.cancelZone ? colors.down : i % 2 ? "#2fe0ff" : colors.primary }} />
                  ))}
                </View>
                <Text style={{ fontSize: fs.sm, color: talk.cancelZone ? "#ff8aa5" : colors.textSoft }}>{talk.cancelZone ? "松开取消" : "松开发送 · 上滑取消"}</Text>
              </>
            ) : (
              <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>识别中…</Text>
            )}
          </View>
        </View>
      ) : null}

      <View style={{ position: "absolute", left: 0, right: 0, bottom, alignItems: "center", zIndex: 41 }} pointerEvents="box-none">
        <View {...talk.handlers} accessibilityRole="button" accessibilityLabel="AI 助手：点按打开，长按说话" style={{ width: SIZE, height: SIZE, alignItems: "center", justifyContent: "center" }}>
          {!rec ? (
            <Animated.View pointerEvents="none" style={{ position: "absolute", width: SIZE + 14, height: SIZE + 14, borderRadius: SIZE, backgroundColor: "rgba(188,255,47,0.22)", opacity: glowOpacity, transform: [{ scale: glowScale }] }} />
          ) : (
            [0, 0.5].map((d) => (
              <Animated.View key={d} pointerEvents="none" style={{ position: "absolute", width: SIZE, height: SIZE, borderRadius: SIZE, borderWidth: 2, borderColor: talk.cancelZone ? "rgba(202,63,100,0.6)" : "rgba(188,255,47,0.55)",
                opacity: ripple.interpolate({ inputRange: [0, 1], outputRange: [d ? 0.5 : 0.9, 0] }),
                transform: [{ scale: ripple.interpolate({ inputRange: [0, 1], outputRange: [1 + d * 0.5, 2.1 + d * 0.3] }) }] }} />
            ))
          )}
          <Orb size={SIZE} ring={3} period={rec ? 1400 : running || busy ? 1800 : 6000} scale={scale} glow={rec ? 0.7 : 0.4} danger={talk.cancelZone}>
            <Ionicons name={rec ? "mic" : "sparkles"} size={23} color="#f4ffe0" />
          </Orb>
          {running ? <Satellite /> : null}
          {unread && !running ? <View pointerEvents="none" style={{ position: "absolute", top: 2, right: 2, width: 12, height: 12, borderRadius: 6, backgroundColor: colors.warn, borderWidth: 2, borderColor: "#000" }} /> : null}
        </View>
      </View>
    </>
  );
}

/** A dot circling the orb while an answer is being written. */
function Satellite() {
  const [spin] = useState(() => new Animated.Value(0));
  useEffect(() => {
    const l = Animated.loop(Animated.timing(spin, { toValue: 1, duration: 1600, easing: Easing.linear, useNativeDriver: true }));
    l.start();
    return () => l.stop();
  }, [spin]);
  const rotate = spin.interpolate({ inputRange: [0, 1], outputRange: ["0deg", "360deg"] });
  return (
    <Animated.View pointerEvents="none" style={{ position: "absolute", width: SIZE + 10, height: SIZE + 10, transform: [{ rotate }] }}>
      <View style={{ position: "absolute", top: -1, left: (SIZE + 10) / 2 - 4, width: 8, height: 8, borderRadius: 4, backgroundColor: colors.primary, shadowColor: colors.primary, shadowOpacity: 0.9, shadowRadius: 6, shadowOffset: { width: 0, height: 0 } }} />
    </Animated.View>
  );
}

// Left drawer with everything that isn't a tab: rules and track record, monitoring, system status,
// help, settings, Dev. Opens from the menu button in TabHeader or a swipe from the left edge.
import { useEffect, useState, type ReactNode } from "react";
import { Animated, Pressable, ScrollView, Text, useWindowDimensions, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { Image } from "expo-image";
import { useRouter, type Href } from "expo-router";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useStreamConnected, useUniverse } from "@/lib/market";
import { time, usd } from "@/lib/fmt";
import { useSettings } from "@/lib/settings";
import { colors, fs, tnum } from "@/lib/theme";
import { closeDrawer, openDrawer, useDrawerOpen } from "./drawerState";
import { Dot, tap, type IconName } from "./ui";

export function DrawerHost() {
  const isOpen = useDrawerOpen();
  const { width } = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const panel = Math.min(310, width * 0.8);
  const [x] = useState(() => new Animated.Value(-panel));
  const snap = useUniverse().data;
  const live = useStreamConnected();
  const devTab = useSettings((s) => s.devTab);

  useEffect(() => {
    Animated.spring(x, { toValue: isOpen ? 0 : -panel, useNativeDriver: true, friction: 10, tension: 70 }).start();
  }, [isOpen, panel, x]);

  const go = (href: Href) => {
    tap();
    closeDrawer();
    setTimeout(() => router.push(href), 160);
  };
  const phase = snap?.phase === "live" ? "实时" : snap?.phase === "backfill-1m" ? "回填中" : snap ? "启动中" : "连接中";

  // Always mounted (off-screen when closed) so the slide-out can animate; touches pass through when closed.
  return (
    <View style={{ position: "absolute", top: 0, left: 0, right: 0, bottom: 0, zIndex: 100 }} pointerEvents={isOpen ? "box-none" : "none"}>
      <Animated.View style={{ position: "absolute", top: 0, left: 0, right: 0, bottom: 0, backgroundColor: colors.backdrop, opacity: x.interpolate({ inputRange: [-panel, 0], outputRange: [0, 1] }) }}>
        <Pressable style={{ flex: 1 }} onPress={closeDrawer} />
      </Animated.View>
      <Animated.View style={{ position: "absolute", top: 0, bottom: 0, left: 0, width: panel, backgroundColor: colors.card, borderRightWidth: 1, borderColor: colors.borderStrong, transform: [{ translateX: x }] }}>
        <ScrollView contentContainerStyle={{ paddingTop: insets.top + 16, paddingBottom: insets.bottom + 24, paddingHorizontal: 12 }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 12, paddingHorizontal: 8, marginBottom: 14 }}>
            <Image source={require("../../assets/icon.png")} style={{ width: 42, height: 42, borderRadius: 10 }} />
            <View>
              <Text style={{ fontSize: 19, fontWeight: "700", color: colors.text }}>OKX Helper</Text>
              <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>永续研究</Text>
            </View>
          </View>
          <View style={{ marginHorizontal: 8, marginBottom: 16, padding: 10, borderRadius: 8, backgroundColor: colors.bg, borderWidth: 1, borderColor: colors.border, gap: 4 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <Dot tone={!snap ? "off" : live && snap.phase === "live" ? "ok" : live ? "warn" : "bad"} />
              <Text style={{ color: colors.text, fontSize: fs.md, fontWeight: "600" }}>{phase}</Text>
              <Text style={[{ color: colors.textFaint, fontSize: fs.sm }, tnum]}>{live ? "推送已连接" : "推送断开，自动重连"}</Text>
            </View>
            {snap ? (
              <Text style={[{ color: colors.textSoft, fontSize: fs.sm }, tnum]}>
                达标 {snap.coverage.active}/{snap.topN} · 阈值 &gt;{usd(snap.threshold)} · 确认 {time(snap.decisionTs)}
              </Text>
            ) : null}
          </View>

          <Section title="研究">
            <Item icon="flask-outline" label="规则与战绩" onPress={() => go("/rules")} />
            <Item icon="notifications-outline" label="监控告警" onPress={() => go("/alerts")} />
          </Section>
          <Section title="系统">
            <Item icon="pulse-outline" label="系统状态" onPress={() => go("/status")} />
            <Item icon="book-outline" label="帮助" onPress={() => go("/help")} />
            <Item icon="settings-outline" label="设置" onPress={() => go("/settings")} />
            {devTab ? <Item icon="code-slash-outline" label="Dev" onPress={() => go("/dev")} /> : null}
          </Section>
        </ScrollView>
      </Animated.View>
    </View>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <View style={{ marginBottom: 12 }}>
      <Text style={{ fontSize: fs.xs, fontWeight: "600", color: colors.textFaint, paddingHorizontal: 10, marginBottom: 4, letterSpacing: 1 }}>{title}</Text>
      {children}
    </View>
  );
}

function Item({ icon, label, onPress }: { icon: IconName; label: string; onPress: () => void }) {
  return (
    <Pressable onPress={onPress} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 14, paddingHorizontal: 10, height: 46, borderRadius: 10, backgroundColor: pressed ? colors.raised : "transparent" })}>
      <Ionicons name={icon} size={20} color={colors.textSoft} />
      <Text style={{ fontSize: fs.xl, color: colors.text, fontWeight: "500" }}>{label}</Text>
    </Pressable>
  );
}

// Swipe from the left edge of a tab screen to open the drawer.
export function DrawerEdge() {
  const pan = Gesture.Pan()
    .activeOffsetX(16)
    .failOffsetY([-20, 20])
    .runOnJS(true)
    .onEnd((e) => {
      if (e.translationX > 50 || e.velocityX > 500) openDrawer();
    });
  return (
    <GestureDetector gesture={pan}>
      <View style={{ position: "absolute", left: 0, top: 100, bottom: 120, width: 24, zIndex: 50 }} />
    </GestureDetector>
  );
}

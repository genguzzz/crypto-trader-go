import { useEffect } from "react";
import { ActivityIndicator, AppState, Platform, View } from "react-native";
import { Stack, router, usePathname } from "expo-router";
import { StatusBar } from "expo-status-bar";
import * as Device from "expo-device";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { SafeAreaProvider } from "react-native-safe-area-context";
import { AdminKeySheet } from "@/components/AdminKeySheet";
import { FloatingOrb } from "@/components/ai/FloatingOrb";
import { ToastHost, toast } from "@/components/ui";
import { loadAdminKey } from "@/lib/adminKey";
import { initAi, useAiSelect } from "@/lib/ai";
import { appVersion } from "@/lib/api";
import { flush, installGlobalHandlers, logger, pruneLogs, sessionId } from "@/lib/log";
import { startLive } from "@/lib/market";
import { loadSettings, useSettingsLoaded } from "@/lib/settings";
import { colors } from "@/lib/theme";
import { installTracking, track } from "@/lib/track";
import { logRunningUpdate, restartToApply, startForegroundChecks, useUpdates } from "@/lib/updates";

const log = logger("app");
installGlobalHandlers();
installTracking();
log.info("launch", { session: sessionId, version: appVersion, os: `${Platform.OS} ${Platform.Version}`, device: `${Device.manufacturer ?? ""} ${Device.modelName ?? ""}`.trim() });
logRunningUpdate();
startForegroundChecks();
void Promise.all([loadSettings(), loadAdminKey()]).then(() => {
  track("app_open", { version: appVersion, os: `${Platform.OS} ${Platform.Version}` });
  pruneLogs();
  startLive();
});

export default function RootLayout() {
  const loaded = useSettingsLoaded();
  const pathname = usePathname();

  useEffect(() => {
    log.info("route", { pathname });
    track("screen", { path: pathname.replace(/\/[A-Z0-9]+-USDT-SWAP$/i, "/:id") });
  }, [pathname]);

  // OTA: once an update has downloaded, offer the restart (it also applies on the next cold start).
  const { isUpdatePending } = useUpdates();
  useEffect(() => {
    if (isUpdatePending) toast("新版本已就绪", "重启后生效", { label: "立即重启", onPress: () => void restartToApply() });
  }, [isUpdatePending]);

  // AI: pick up a question left running; an answer that finished while the sheet was closed says so once
  const unread = useAiSelect((a) => a.unread);
  useEffect(() => { if (loaded) void initAi(); }, [loaded]);
  useEffect(() => {
    if (unread) toast("AI 回答好了", undefined, { label: "查看", onPress: () => router.push("/ai") });
  }, [unread]);

  useEffect(() => {
    const sub = AppState.addEventListener("change", (s) => {
      if (s !== "active") void flush();
    });
    return () => sub.remove();
  }, []);

  return (
    <GestureHandlerRootView style={{ flex: 1, backgroundColor: colors.bg }}>
      <SafeAreaProvider>
        <StatusBar style="light" />
        {loaded ? (
          <Stack screenOptions={{ headerShown: false, contentStyle: { backgroundColor: colors.bg }, orientation: "portrait_up" }}>
            <Stack.Screen name="(tabs)" />
            <Stack.Screen name="settings" options={{ presentation: "modal" }} />
            {/* the AI chat is a native sheet over any screen, opened from the floating orb */}
            <Stack.Screen name="ai" options={{ presentation: "formSheet", sheetAllowedDetents: [0.65, 1], sheetGrabberVisible: true, sheetCornerRadius: 22, sheetExpandsWhenScrolledToEdge: true }} />
            <Stack.Screen name="ai-history" options={{ presentation: "modal" }} />
            <Stack.Screen name="ai-notes" options={{ presentation: "modal" }} />
            <Stack.Screen name="chart/[id]" options={{ presentation: "fullScreenModal", animation: "fade", orientation: "landscape", gestureEnabled: false }} />
          </Stack>
        ) : (
          <View style={{ flex: 1, backgroundColor: colors.bg, alignItems: "center", justifyContent: "center" }}>
            <ActivityIndicator color={colors.primary} />
          </View>
        )}
        {loaded ? <FloatingOrb /> : null}
        {loaded ? <AdminKeySheet /> : null}
        {loaded ? <ToastHost /> : null}
      </SafeAreaProvider>
    </GestureHandlerRootView>
  );
}

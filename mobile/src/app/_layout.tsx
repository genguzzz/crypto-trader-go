import { useEffect } from "react";
import { ActivityIndicator, AppState, Platform, View } from "react-native";
import { Stack, usePathname } from "expo-router";
import { StatusBar } from "expo-status-bar";
import * as Device from "expo-device";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { SafeAreaProvider } from "react-native-safe-area-context";
import { AdminKeySheet } from "@/components/AdminKeySheet";
import { ToastHost, toast } from "@/components/ui";
import { loadAdminKey } from "@/lib/adminKey";
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
            <Stack.Screen name="chart/[id]" options={{ presentation: "fullScreenModal", animation: "fade", orientation: "landscape", gestureEnabled: false }} />
          </Stack>
        ) : (
          <View style={{ flex: 1, backgroundColor: colors.bg, alignItems: "center", justifyContent: "center" }}>
            <ActivityIndicator color={colors.primary} />
          </View>
        )}
        {loaded ? <AdminKeySheet /> : null}
        {loaded ? <ToastHost /> : null}
      </SafeAreaProvider>
    </GestureHandlerRootView>
  );
}

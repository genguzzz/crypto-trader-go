import { useEffect } from "react";
import { View } from "react-native";
import { router } from "expo-router";
import { NativeTabs } from "expo-router/native-tabs";
import { DrawerEdge, DrawerHost } from "@/components/Drawer";
import { toast } from "@/components/ui";
import { initAi, useAiSelect } from "@/lib/ai";
import { colors } from "@/lib/theme";

// Four daily screens on the system tab bar (Liquid Glass on iOS 26+); rules, monitoring, status,
// help, settings and Dev live in the left drawer.
export default function TabsLayout() {
  // AI: pick up a question left running; an answer that finished elsewhere badges the tab and says so once
  const unread = useAiSelect((a) => a.unread);
  useEffect(() => { void initAi(); }, []);
  useEffect(() => {
    if (unread) toast("AI 回答好了", undefined, { label: "查看", onPress: () => router.navigate("/ai") });
  }, [unread]);
  return (
    <View style={{ flex: 1, backgroundColor: colors.bg }}>
      <NativeTabs tintColor={colors.primary} minimizeBehavior="onScrollDown">
        <NativeTabs.Trigger name="index">
          <NativeTabs.Trigger.Icon sf={{ default: "dot.radiowaves.left.and.right", selected: "dot.radiowaves.left.and.right" }} md="radar" />
          <NativeTabs.Trigger.Label>扫描</NativeTabs.Trigger.Label>
        </NativeTabs.Trigger>
        <NativeTabs.Trigger name="watch">
          <NativeTabs.Trigger.Icon sf={{ default: "star", selected: "star.fill" }} md="star" />
          <NativeTabs.Trigger.Label>自选</NativeTabs.Trigger.Label>
        </NativeTabs.Trigger>
        <NativeTabs.Trigger name="ai">
          <NativeTabs.Trigger.Icon sf={{ default: "sparkles", selected: "sparkles" }} md="auto_awesome" />
          <NativeTabs.Trigger.Label>AI</NativeTabs.Trigger.Label>
          {unread ? <NativeTabs.Trigger.Badge>新</NativeTabs.Trigger.Badge> : null}
        </NativeTabs.Trigger>
        <NativeTabs.Trigger name="trade">
          <NativeTabs.Trigger.Icon sf={{ default: "chart.line.uptrend.xyaxis", selected: "chart.line.uptrend.xyaxis" }} md="candlestick_chart" />
          <NativeTabs.Trigger.Label>交易</NativeTabs.Trigger.Label>
        </NativeTabs.Trigger>
      </NativeTabs>
      <DrawerEdge />
      <DrawerHost />
    </View>
  );
}

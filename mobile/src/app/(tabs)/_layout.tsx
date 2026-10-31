import { View } from "react-native";
import { NativeTabs } from "expo-router/native-tabs";
import { DrawerEdge, DrawerHost } from "@/components/Drawer";
import { colors } from "@/lib/theme";
import { useOpenPrompts } from "@/lib/tradeLive";

// Three daily screens on the system tab bar (Liquid Glass on iOS 26+); the AI is the floating orb
// above it (root layout); rules, monitoring, status, help, settings and Dev live in the left drawer.
export default function TabsLayout() {
  const prompts = useOpenPrompts(); // open confirmations: badge on 交易 (web nav badge)
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
        <NativeTabs.Trigger name="trade">
          <NativeTabs.Trigger.Icon sf={{ default: "chart.line.uptrend.xyaxis", selected: "chart.line.uptrend.xyaxis" }} md="candlestick_chart" />
          <NativeTabs.Trigger.Label>交易</NativeTabs.Trigger.Label>
          <NativeTabs.Trigger.Badge hidden={!prompts}>{prompts ? String(prompts) : undefined}</NativeTabs.Trigger.Badge>
        </NativeTabs.Trigger>
      </NativeTabs>
      <DrawerEdge />
      <DrawerHost />
    </View>
  );
}

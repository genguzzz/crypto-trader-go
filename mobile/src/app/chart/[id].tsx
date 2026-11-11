// Full-screen K-line chart in landscape (from the chart's own full-screen button). The root stack
// owns rotation (orientation: "landscape" on this route), like mediahub's player.
import { useEffect } from "react";
import { Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { StatusBar } from "expo-status-bar";
import { ChartView } from "@/components/ChartView";
import { tap } from "@/components/ui";
import { keepAwake } from "@/lib/device";
import { sym } from "@/lib/fmt";
import { colors, fs } from "@/lib/theme";

export default function FullChart() {
  const { id, spot, oi } = useLocalSearchParams<{ id: string; spot?: string; oi?: string }>();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  useEffect(() => {
    void keepAwake("chart", true);
    return () => void keepAwake("chart", false);
  }, []);
  const close = () => (router.canGoBack() ? router.back() : router.replace("/"));
  return (
    <View style={{ flex: 1, backgroundColor: colors.card, paddingTop: insets.top, paddingBottom: insets.bottom, paddingLeft: insets.left, paddingRight: insets.right }}>
      <StatusBar hidden />
      <View style={{ flexDirection: "row", alignItems: "center", height: 32, paddingHorizontal: 10, gap: 10 }}>
        <Pressable hitSlop={12} accessibilityLabel="关闭全屏" onPress={() => { tap(); close(); }}>
          <Ionicons name="close" size={22} color={colors.text} />
        </Pressable>
        <Text style={{ color: colors.text, fontSize: fs.lg, fontWeight: "700" }}>{sym(id)}/USDT 永续</Text>
      </View>
      <ChartView id={id} hasSpot={spot === "1"} hasOi={oi === "1"} full style={{ flex: 1 }} onFullscreen={(enter) => !enter && close()} />
    </View>
  );
}

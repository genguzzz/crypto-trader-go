// 扫描: the monitoring pool (web views/Scanner.vue).
import { Text, View } from "react-native";
import { ScannerList } from "@/components/ScannerList";
import { Dot, Screen, TabHeader } from "@/components/ui";
import { time, usd } from "@/lib/fmt";
import { useStreamConnected, useUniverse } from "@/lib/market";
import { colors, fs, tnum } from "@/lib/theme";

export default function ScannerTab() {
  const snap = useUniverse().data;
  const live = useStreamConnected();
  const phase = snap?.phase === "live" ? "" : snap?.provisional ? "临时名单" : snap?.phase === "backfill-1m" ? "回填中" : snap ? "启动中" : "";
  return (
    <Screen>
      <TabHeader
        title="扫描"
        sub={snap ? (
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <Dot tone={live ? (snap.phase === "live" ? "ok" : "warn") : "bad"} />
            <Text style={[{ fontSize: fs.sm, color: colors.textFaint }, tnum]}>
              达标 <Text style={{ color: colors.text, fontWeight: "600" }}>{snap.coverage.active}</Text>/{snap.topN} · &gt;{usd(snap.threshold)} · 确认 {time(snap.decisionTs)}{phase ? ` · ${phase}` : ""}
            </Text>
          </View>
        ) : undefined}
      />
      <ScannerList />
    </Screen>
  );
}

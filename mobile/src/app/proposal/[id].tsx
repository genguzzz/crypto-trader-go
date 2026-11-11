// One AI trade proposal, opened from the trade bot's 查看 link (okxhelper://proposal/<id>, via the
// server's /go/proposal/<id> bounce page) or anywhere else: the K-line with its levels, then the card.
import { ScrollView, Text, useWindowDimensions, View } from "react-native";
import { useLocalSearchParams } from "expo-router";
import { useProposal } from "@/lib/realtrade";
import { time } from "@/lib/fmt";
import { colors, fs, tnum } from "@/lib/theme";
import { ChartView } from "@/components/ChartView";
import { ProposalCard } from "@/components/ai/ProposalCard";
import { Loading, Screen, TopBar } from "@/components/ui";

export default function ProposalScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const n = Number(id) || 0;
  const p = useProposal(n);
  const { height } = useWindowDimensions();
  return (
    <Screen>
      <TopBar title={`交易提议 #${n}`} sub={p ? p.instId.replace("-USDT-SWAP", "") : undefined} />
      {!p ? (
        <Loading />
      ) : (
        <ScrollView contentContainerStyle={{ paddingBottom: 60 }}>
          <ChartView id={p.instId} proposal={p.id} compact style={{ height: Math.round(Math.max(300, Math.min(520, height * 0.5))) }} />
          <View style={{ padding: 10, gap: 10 }}>
            <ProposalCard p={p} inChart />
            {p.events?.map((e, i) => (
              <View key={i} style={{ flexDirection: "row", gap: 8, paddingHorizontal: 6 }}>
                <Text style={[{ width: 74, fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(e.ts, true)}</Text>
                <Text selectable style={{ flex: 1, fontSize: fs.sm, lineHeight: 18, color: colors.textSoft }}>
                  {e.text}
                  {e.by ? <Text style={{ color: colors.textFaint }}>{`  ${e.by}`}</Text> : null}
                </Text>
              </View>
            ))}
          </View>
        </ScrollView>
      )}
    </Screen>
  );
}

// 帮助 — strategy docs index (web views/Help.vue). Articles are bundled from web/src/docs/strategy
// (scripts/gen-docs.mjs → src/content/strategyDocs.ts) and render natively in help/[slug].tsx.
import { ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { strategyDocs } from "@/content/strategyDocs";
import { colors, fs, tnum } from "@/lib/theme";
import { API_ARTICLE } from "@/components/ApiRef";
import { Card, Screen, TopBar, styles } from "@/components/ui";

export default function HelpScreen() {
  const router = useRouter();
  return (
    <Screen>
      <TopBar title="帮助" sub="策略文档" />
      <ScrollView contentContainerStyle={{ padding: 12, gap: 10, paddingBottom: 40 }}>
        <View style={{ paddingHorizontal: 4, paddingVertical: 6, gap: 6 }}>
          <Text style={{ fontSize: 24, fontWeight: "700", color: colors.text }}>策略文档</Text>
          <Text style={styles.body}>从这里学会写自己的交易策略：怎么声明参数、读数据、发信号、做空、做多币种组合、发通知，以及为什么策略没有出信号。想直接动手：交易 → 策略库 → 新建策略。</Text>
        </View>
        {strategyDocs.map((a, i) => (
          <Card key={a.slug} onPress={() => router.push({ pathname: "/help/[slug]", params: { slug: a.slug } })} style={{ flexDirection: "row", gap: 12, alignItems: "center" }}>
            <Text style={[{ width: 20, fontSize: fs.lg, color: colors.textFaint, textAlign: "right" }, tnum]}>{i + 1}</Text>
            <View style={{ flex: 1, gap: 3 }}>
              <Text style={{ fontSize: fs.xl, fontWeight: "600", color: colors.text }}>{a.title}</Text>
              <Text style={{ fontSize: fs.md, lineHeight: 19, color: colors.textSoft }}>{a.summary}</Text>
            </View>
            <Ionicons name="chevron-forward" size={16} color={colors.textFaint} />
          </Card>
        ))}
        <Card onPress={() => router.push({ pathname: "/help/[slug]", params: { slug: API_ARTICLE.slug } })} style={{ flexDirection: "row", gap: 12, alignItems: "center" }}>
          <Ionicons name="code-slash-outline" size={18} color={colors.textFaint} style={{ width: 20 }} />
          <View style={{ flex: 1, gap: 3 }}>
            <Text style={{ fontSize: fs.xl, fontWeight: "600", color: colors.text }}>{API_ARTICLE.title}</Text>
            <Text style={{ fontSize: fs.md, lineHeight: 19, color: colors.textSoft }}>{API_ARTICLE.summary}</Text>
          </View>
          <Ionicons name="chevron-forward" size={16} color={colors.textFaint} />
        </Card>
      </ScrollView>
    </Screen>
  );
}

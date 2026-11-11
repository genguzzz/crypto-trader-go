// 规则与战绩 — rule list (web views/Rules.vue left column + Settings "全部规则"): comparison rules
// and the owner's rules with their weights; tap one for its track record and the weight editor.
import { ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import type { Field, Rule } from "@/lib/api";
import { useMeta, useRuleId } from "@/lib/market";
import { refreshMeta, weightsText } from "@/lib/sysApi";
import { colors, fs } from "@/lib/theme";
import { B, InfoTip, P } from "@/components/Form";
import { Card, ErrorBox, Loading, Pull, Screen, Tag, TopBar, styles } from "@/components/ui";

export default function RulesScreen() {
  const meta = useMeta();
  const ruleId = useRuleId(meta.data);
  const rules = meta.data?.rules ?? [];
  const groups: [string, Rule[]][] = [["对照规则", rules.filter((r) => r.preset)], ["我的规则", rules.filter((r) => !r.preset)]];

  return (
    <Screen>
      <TopBar
        title="规则与战绩"
        right={
          <InfoTip title="规则与战绩">
            <P>所有规则默认标“未验证”。“对照”是内置的比较基准（不是推荐）。</P>
            <P>判断一条规则有没有用，只看战绩，尤其是“上线后”的样本外记录；历史回放含幸存者偏差，只能参考。</P>
            <P>在规则详情里可以编辑权重与点差门槛：<B>保存</B> = 发布新版本（v+1），旧版本的战绩保留；也可以<B>另存为新规则</B>。</P>
          </InfoTip>
        }
      />
      {!meta.data ? (
        meta.error ? <ErrorBox text={meta.error} onRetry={meta.refresh} /> : <Loading text="加载规则" />
      ) : (
        <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: 40 }} refreshControl={<Pull onRefresh={refreshMeta} />}>
          {groups.map(([title, list]) => (
            <View key={title} style={{ gap: 8 }}>
              <Text style={{ fontSize: fs.sm, color: colors.textFaint, marginLeft: 4 }}>{title} · {list.length}</Text>
              {list.length ? list.map((r) => <RuleCard key={r.id} r={r} inUse={r.id === ruleId} isDefault={r.id === meta.data?.settings.defaultRule} fields={meta.data?.fields} />) : (
                <Card><Text style={styles.note}>还没有自己的规则。打开任一规则，改权重后“另存为新规则”。</Text></Card>
              )}
            </View>
          ))}
        </ScrollView>
      )}
    </Screen>
  );
}

function RuleCard({ r, inUse, isDefault, fields }: { r: Rule; inUse: boolean; isDefault: boolean; fields?: Field[] }) {
  const router = useRouter();
  return (
    <Card onPress={() => router.push({ pathname: "/rules/[id]", params: { id: r.id } })} style={{ gap: 6 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Text style={{ flexShrink: 1, fontSize: fs.xl, fontWeight: "600", color: colors.text }}>{r.name}</Text>
        <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>v{r.version}</Text>
        <View style={{ flex: 1 }} />
        <Ionicons name="chevron-forward" size={16} color={colors.textFaint} />
      </View>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
        {r.preset ? <Tag text="对照" /> : null}
        <Tag text="未验证" tone="warn" />
        {isDefault ? <Tag text="默认" tone="info" /> : null}
        {inUse ? <Tag text="扫描页使用中" tone="good" /> : null}
        <Text style={{ fontSize: fs.xs, color: colors.textDim }}>{r.id}</Text>
      </View>
      {r.description ? <Text style={{ fontSize: fs.md, lineHeight: 19, color: colors.textSoft }} numberOfLines={3}>{r.description}</Text> : null}
      <Text style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textFaint }}><Text style={{ color: colors.up }}>多 </Text>{weightsText(r.long, fields)}</Text>
      <Text style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textFaint }}><Text style={{ color: colors.down }}>空 </Text>{weightsText(r.short, fields)}</Text>
      <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>点差门槛 {r.gates.maxSpreadBps ? `${r.gates.maxSpreadBps} bps` : "不限"}</Text>
    </Card>
  );
}

// Screens built in later phases: what each will hold, plus a link to the web page meanwhile.
import { ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { WEB_ORIGIN } from "@/lib/api";
import { openPage } from "@/lib/device";
import { colors, fs } from "@/lib/theme";
import { Card, PillButton, TAB_BAR_SPACE, type IconName } from "./ui";

export function Placeholder({ icon, title, intro, items, webPath, tab }: { icon: IconName; title: string; intro: string; items: string[]; webPath?: string; tab?: boolean }) {
  return (
    <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: tab ? TAB_BAR_SPACE : 40 }}>
      <Card style={{ alignItems: "center", gap: 10, paddingVertical: 26 }}>
        <Ionicons name={icon} size={36} color={colors.primary} />
        <Text style={{ fontSize: fs.x2, fontWeight: "700", color: colors.text }}>{title}</Text>
        <Text style={{ fontSize: fs.md, color: colors.textSoft, textAlign: "center", lineHeight: 20 }}>{intro}</Text>
        <View style={{ paddingHorizontal: 10, paddingVertical: 3, borderRadius: 10, backgroundColor: colors.warnBg }}>
          <Text style={{ fontSize: fs.xs, color: colors.warn }}>原生页面开发中</Text>
        </View>
      </Card>
      <Card>
        <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text, marginBottom: 8 }}>将会包含</Text>
        {items.map((x) => (
          <View key={x} style={{ flexDirection: "row", gap: 8, paddingVertical: 5 }}>
            <Text style={{ color: colors.primary }}>·</Text>
            <Text style={{ flex: 1, fontSize: fs.md, color: colors.textSoft, lineHeight: 20 }}>{x}</Text>
          </View>
        ))}
      </Card>
      {webPath ? <PillButton variant="soft" icon="open-outline" label="暂时在网页版打开" onPress={() => void openPage(WEB_ORIGIN + webPath)} /> : null}
    </ScrollView>
  );
}

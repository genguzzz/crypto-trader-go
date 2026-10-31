// 能力自检: what native capabilities this binary carries, with a quick on-device test for each.
import { useState } from "react";
import { Pressable, ScrollView, Text } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { Card, Screen, TopBar, tap, toast, styles } from "@/components/ui";
import { CAPABILITY_LABEL, authenticate, capabilities, connection, copy, haptic, listenOnce, micPermission, notify, shareText, type Capability } from "@/lib/device";
import { colors, fs } from "@/lib/theme";

const TESTS: Partial<Record<Capability, () => Promise<string>>> = {
  haptics: async () => { await haptic("medium"); return "已震动"; },
  clipboard: async () => ((await copy("OKX Helper")) ? "已复制“OKX Helper”" : "不可用"),
  sharing: async () => { await shareText("OKX Helper"); return "已打开分享"; },
  network: async () => { const c = await connection(); return c ? `${c.type}${c.cellular ? "（蜂窝）" : ""}` : "不可用"; },
  biometrics: async () => ((await authenticate("验证面容 ID")) ? "验证通过" : "未通过"),
  notifications: async () => ((await notify("OKX Helper", "本地通知正常")) ? "已发送" : "未授权或不可用"),
  speech: async () => (await listenOnce()) ?? "没有听到内容",
  audio: async () => ((await micPermission()) ? "麦克风已授权" : "未授权"),
};

export default function Diagnostics() {
  const caps = capabilities();
  const [result, setResult] = useState<Partial<Record<Capability, string>>>({});
  return (
    <Screen>
      <TopBar title="能力自检" />
      <ScrollView contentContainerStyle={{ padding: 12, paddingBottom: 40 }}>
        <Text style={[styles.note, { marginBottom: 10 }]}>安装包预先带上这些原生能力，之后的功能可以通过热更新上线。缺失的能力需要新的安装包。</Text>
        <Card style={{ paddingVertical: 4 }}>
          {(Object.keys(caps) as Capability[]).map((c) => {
            const test = TESTS[c];
            return (
              <Pressable key={c} disabled={!caps[c] || !test} onPress={async () => {
                tap();
                try {
                  const r = await test!();
                  setResult((x) => ({ ...x, [c]: r }));
                } catch (e) {
                  toast("测试失败", e instanceof Error ? e.message : String(e), undefined, true);
                }
              }} style={{ flexDirection: "row", alignItems: "center", gap: 10, paddingVertical: 11, borderBottomWidth: 1, borderColor: colors.border }}>
                <Ionicons name={caps[c] ? "checkmark-circle" : "close-circle"} size={18} color={caps[c] ? colors.up : colors.down} />
                <Text style={{ flex: 1, fontSize: fs.lg, color: colors.text }}>{CAPABILITY_LABEL[c]}</Text>
                {result[c] ? <Text style={{ fontSize: fs.sm, color: colors.textSoft, maxWidth: 170 }} numberOfLines={1}>{result[c]}</Text> : caps[c] && test ? <Text style={{ fontSize: fs.sm, color: colors.primary }}>测试</Text> : null}
              </Pressable>
            );
          })}
        </Card>
      </ScrollView>
    </Screen>
  );
}

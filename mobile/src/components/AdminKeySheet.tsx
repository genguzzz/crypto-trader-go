// Asks for the owner key once when a protected write returns 401 admin_required (lib/adminKey.ts).
import { useState } from "react";
import { Text, TextInput, View } from "react-native";
import { resolveAdminKey, useAdminPrompt } from "@/lib/adminKey";
import { colors, fs, radius } from "@/lib/theme";
import { PillButton, Sheet, styles } from "./ui";

export function AdminKeySheet() {
  const p = useAdminPrompt();
  const [v, setV] = useState("");
  const done = (k: string | null) => {
    setV("");
    resolveAdminKey(k);
  };
  return (
    <Sheet visible={p.open} onClose={() => done(null)} title="需要所有者密钥">
      <Text style={styles.note}>修改交易、AI、策略等受保护内容需要服务器上配置的所有者密钥（CTG_ADMIN_KEY）。密钥只保存在本机钥匙串，可在设置里清除。</Text>
      {p.error ? <Text style={{ color: colors.down, fontSize: fs.md, marginTop: 8 }}>{p.error}</Text> : null}
      <TextInput
        value={v}
        onChangeText={setV}
        autoFocus
        secureTextEntry
        autoCapitalize="none"
        autoCorrect={false}
        placeholder="所有者密钥"
        placeholderTextColor={colors.textDim}
        onSubmitEditing={() => v.trim() && done(v.trim())}
        style={{ marginTop: 14, height: 46, borderRadius: radius.md, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.cardMuted, color: colors.text, paddingHorizontal: 12, fontSize: fs.xl }}
      />
      <View style={{ flexDirection: "row", gap: 10, marginTop: 14 }}>
        <PillButton variant="soft" label="取消" onPress={() => done(null)} style={{ flex: 1 }} />
        <PillButton label="保存并继续" disabled={!v.trim()} onPress={() => done(v.trim())} style={{ flex: 1 }} />
      </View>
    </Sheet>
  );
}

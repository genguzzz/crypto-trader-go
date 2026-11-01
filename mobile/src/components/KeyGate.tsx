// Full-screen access gate: shown when no owner key is stored, after the server rejects the key
// (401 admin_required anywhere), or from Settings → 更换密钥. The key is checked against the server
// before it is stored (lib/adminKey.ts); waiting requests then retry with it.
import { useState } from "react";
import { KeyboardAvoidingView, Modal, Platform, Pressable, Text, TextInput, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { acceptKey, closeKeyGate, useKeyGate } from "@/lib/adminKey";
import { verifyKey } from "@/lib/api";
import { haptic, has, paste } from "@/lib/device";
import { colors, fs, radius } from "@/lib/theme";
import { PillButton, tap } from "./ui";

export function KeyGate() {
  const g = useKeyGate();
  const insets = useSafeAreaInsets();
  const [v, setV] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(g.error);
  // a new gate state (opened, re-opened with a reason) starts from an empty field
  const [shown, setShown] = useState(g);
  if (shown !== g) {
    setShown(g);
    setV("");
    setErr(g.error);
  }

  const submit = async () => {
    const k = v.trim();
    if (!k || busy) return;
    setBusy(true);
    setErr("");
    const r = await verifyKey(k);
    setBusy(false);
    if (r === "ok") {
      void haptic("success");
      setV("");
      acceptKey(k);
      return;
    }
    void haptic("error");
    setErr(r === "bad" ? "密钥不正确" : r === "limited" ? "错误次数过多，请几分钟后再试" : r);
  };
  const pasteKey = async () => {
    const t = (await paste().catch(() => null))?.trim();
    if (t) setV(t);
  };

  return (
    <Modal visible={g.open} animationType="fade" presentationStyle="fullScreen" onRequestClose={closeKeyGate}>
      <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} style={{ flex: 1, backgroundColor: colors.bg }}>
        {g.cancellable ? (
          <Pressable hitSlop={10} accessibilityLabel="关闭" onPress={() => { tap(); closeKeyGate(); }}
            style={({ pressed }) => ({ position: "absolute", top: insets.top + 10, right: 16, zIndex: 1, width: 32, height: 32, borderRadius: 16, alignItems: "center", justifyContent: "center", backgroundColor: colors.cardMuted, opacity: pressed ? 0.7 : 1 })}>
            <Ionicons name="close" size={18} color={colors.textSoft} />
          </Pressable>
        ) : null}
        <View style={{ flex: 1, justifyContent: "center", paddingHorizontal: 24, paddingTop: insets.top, paddingBottom: insets.bottom + 12 }}>
          <View style={{ alignSelf: "center", width: 64, height: 64, borderRadius: 32, alignItems: "center", justifyContent: "center", backgroundColor: colors.primarySoft, marginBottom: 20 }}>
            <Ionicons name="key-outline" size={30} color={colors.primary} />
          </View>
          <Text style={{ color: colors.text, fontSize: fs.x2, fontWeight: "700", textAlign: "center" }}>输入访问密钥</Text>
          <Text style={{ color: colors.textFaint, fontSize: fs.md, lineHeight: 19, textAlign: "center", marginTop: 8 }}>
            OKX Helper 的所有数据都需要所有者密钥（服务器上的 CTG_ADMIN_KEY）。密钥只保存在本机钥匙串，可在设置里更换或清除。
          </Text>
          <View style={{ flexDirection: "row", alignItems: "center", marginTop: 24, height: 50, borderRadius: radius.md, borderWidth: 1, borderColor: err ? colors.down : colors.borderStrong, backgroundColor: colors.card, paddingLeft: 14, paddingRight: 6 }}>
            <TextInput
              value={v}
              onChangeText={(t) => { setV(t); if (err) setErr(""); }}
              autoFocus={g.cancellable}
              secureTextEntry
              autoCapitalize="none"
              autoCorrect={false}
              autoComplete="off"
              textContentType="password"
              returnKeyType="go"
              placeholder="访问密钥"
              placeholderTextColor={colors.textDim}
              onSubmitEditing={() => void submit()}
              style={{ flex: 1, color: colors.text, fontSize: fs.xl, height: "100%" }}
            />
            {has("clipboard") ? (
              <Pressable hitSlop={6} accessibilityLabel="粘贴" onPress={() => { tap(); void pasteKey(); }}
                style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 4, height: 36, paddingHorizontal: 10, borderRadius: radius.sm, backgroundColor: colors.cardMuted, opacity: pressed ? 0.7 : 1 })}>
                <Ionicons name="clipboard-outline" size={15} color={colors.textSoft} />
                <Text style={{ color: colors.textSoft, fontSize: fs.md }}>粘贴</Text>
              </Pressable>
            ) : null}
          </View>
          <Text style={{ color: colors.down, fontSize: fs.md, minHeight: 20, marginTop: 8 }}>{err}</Text>
          <PillButton label="验证" loading={busy} disabled={!v.trim()} onPress={() => void submit()} style={{ marginTop: 6 }} />
        </View>
      </KeyboardAvoidingView>
    </Modal>
  );
}

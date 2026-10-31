// Settings (modal): owner key, display options, app version and updates, log upload, diagnostics.
import { useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { useRouter } from "expo-router";
import * as Updates from "expo-updates";
import { Card, PickerSheet, PillButton, Row, Screen, TopBar, confirm, tap, toast, toastError, styles } from "@/components/ui";
import { clearAdminKey, askAdminKey, useAdminKeySet } from "@/lib/adminKey";
import { api, appVersion } from "@/lib/api";
import { capabilities } from "@/lib/device";
import { tzOptions, usd } from "@/lib/fmt";
import { collectLogs, sessionId } from "@/lib/log";
import { setSettings, useSettings } from "@/lib/settings";
import { colors, fs } from "@/lib/theme";
import { checkAndFetch, restartToApply, runningLabel, useUpdates } from "@/lib/updates";

const OI_HALF = [1e6, 2e6, 5e6, 1e7, 2e7, 5e7].map((v) => ({ value: String(v), label: usd(v) + " USD" }));

export default function SettingsScreen() {
  const router = useRouter();
  const keySet = useAdminKeySet();
  const tz = useSettings((s) => s.tz);
  const oiHalf = useSettings((s) => s.oiHalf);
  const tall = useSettings((s) => s.chartTall);
  const [sheet, setSheet] = useState<"" | "tz" | "oi">("");
  const [checking, setChecking] = useState(false);
  const [uploading, setUploading] = useState(false);
  const { isUpdatePending } = useUpdates();

  const upload = async () => {
    setUploading(true);
    try {
      const log = await collectLogs();
      const r = await api.uploadLog(log.slice(-880_000), { version: appVersion, session: sessionId, update: runningLabel(), caps: capabilities() });
      toast("日志已上传", `编号 ${r.id}`);
    } catch (e) {
      toastError("未能上传日志", e);
    } finally {
      setUploading(false);
    }
  };

  const check = async () => {
    setChecking(true);
    try {
      const got = await checkAndFetch("manual");
      if (got) toast("新版本已下载", "重启后生效", { label: "立即重启", onPress: () => void restartToApply() });
      else toast("已是最新版本", runningLabel());
    } finally {
      setChecking(false);
    }
  };

  return (
    <Screen>
      <TopBar title="设置" />
      <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: 40 }}>
        <Card>
          <Text style={[styles.title, { marginBottom: 4 }]}>所有者密钥</Text>
          <Text style={styles.note}>修改交易、AI、策略等受保护内容时需要。只保存在本机钥匙串。</Text>
          <View style={{ flexDirection: "row", gap: 10, marginTop: 10 }}>
            <PillButton small variant="soft" label={keySet ? "更换密钥" : "填写密钥"} onPress={() => void askAdminKey()} style={{ flex: 1 }} />
            {keySet ? <PillButton small variant="ghost" label="清除" onPress={() => confirm("清除所有者密钥", "清除后，下次受保护的操作会再次询问。", "清除", clearAdminKey, true)} style={{ flex: 1 }} /> : null}
          </View>
        </Card>

        <Card>
          <Text style={[styles.title, { marginBottom: 2 }]}>显示</Text>
          <Row label="时区" value={tzOptions.find((o) => o.value === tz)?.label ?? tz} onPress={() => setSheet("tz")} />
          <Row label="持仓量条半满" value={`${usd(oiHalf)} USD`} onPress={() => setSheet("oi")} />
          <Row label="K 线高度" value={tall ? "高" : "标准"} onPress={() => setSettings({ chartTall: !tall })} />
          <Text style={[styles.note, { marginTop: 8 }]}>持仓量条：扫描列表每行背景按持仓量填充，这个值的持仓量正好填满一半。</Text>
        </Card>

        <Card>
          <Text style={[styles.title, { marginBottom: 2 }]}>版本与更新</Text>
          <Row label="版本" value={appVersion} />
          <Row label="运行时" value={Updates.runtimeVersion ?? "—"} />
          <Row label="更新通道" value={Updates.isEnabled ? "OTA（ctg 服务器）" : "未启用（开发版）"} />
          <View style={{ flexDirection: "row", gap: 10, marginTop: 10 }}>
            <PillButton small variant="soft" icon="cloud-download-outline" label="检查更新" loading={checking} onPress={() => void check()} style={{ flex: 1 }} />
            {isUpdatePending ? <PillButton small label="重启应用更新" onPress={() => void restartToApply()} style={{ flex: 1 }} /> : null}
          </View>
        </Card>

        <Card>
          <Text style={[styles.title, { marginBottom: 2 }]}>诊断</Text>
          <Row label="会话" value={sessionId} />
          <View style={{ flexDirection: "row", gap: 10, marginTop: 10 }}>
            <PillButton small variant="soft" icon="cloud-upload-outline" label="上传日志" loading={uploading} onPress={() => void upload()} style={{ flex: 1 }} />
            <PillButton small variant="soft" icon="hardware-chip-outline" label="能力自检" onPress={() => router.push("/diagnostics")} style={{ flex: 1 }} />
          </View>
          <Text style={[styles.note, { marginTop: 8 }]}>上传日志需要所有者密钥；日志不含密钥。</Text>
        </Card>

        <Pressable onPress={() => { tap(); }}>
          <Text style={{ textAlign: "center", color: colors.textDim, fontSize: fs.xs }}>OKX Helper · 只用 OKX 公开行情，不下真实订单</Text>
        </Pressable>
      </ScrollView>
      <PickerSheet visible={sheet === "tz"} onClose={() => setSheet("")} title="时区" value={tz} options={tzOptions} onPick={(v) => setSettings({ tz: v })} />
      <PickerSheet visible={sheet === "oi"} onClose={() => setSheet("")} title="持仓量条半满" value={String(oiHalf)} options={OI_HALF} onPick={(v) => setSettings({ oiHalf: Number(v) })} />
    </Screen>
  );
}

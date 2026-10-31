// Settings (modal), web views/Settings.vue: server settings (monitoring pool, default rule, storage
// cap; one draft + save bar), display options (this device), interaction channel, AI authorization,
// owner key, app version and updates, developer (external API), log upload and diagnostics.
import { useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import * as Updates from "expo-updates";
import { Card, PickerSheet, PillButton, Row, Screen, Segmented, TopBar, confirm, tap, toast, toastError, styles } from "@/components/ui";
import { clearAdminKey, askAdminKey, useAdminKeySet } from "@/lib/adminKey";
import { api, appVersion } from "@/lib/api";
import { capabilities } from "@/lib/device";
import { oiFrac, tzOptions, usd } from "@/lib/fmt";
import { collectLogs, sessionId } from "@/lib/log";
import { setSettings, useSettings } from "@/lib/settings";
import { colors, fs, tnum } from "@/lib/theme";
import { checkAndFetch, restartToApply, runningLabel, useUpdates } from "@/lib/updates";
import { B, Field, NumInput, P, SectionHead } from "@/components/Form";
import { AiAuthSection } from "@/components/settings/AiAuthSection";
import { DevSection } from "@/components/settings/DevSection";
import { InteractSection } from "@/components/settings/InteractSection";
import { PoolSection, RuleSection, SaveBar, StorageSection, useServerSettings } from "@/components/settings/ServerSettings";

// OI bar half point, edited in 万 USD like the web (presets + free number)
const OI_PRESETS = [100, 500, 2000, 10000].map((v) => ({ value: String(v), label: v >= 10000 ? `${v / 10000}亿` : `${v}万` }));
const OI_SAMPLES = [5e5, 5e6, 5e7, 5e8, 5e9];

export default function SettingsScreen() {
  const router = useRouter();
  const keySet = useAdminKeySet();
  const tz = useSettings((s) => s.tz);
  const oiHalf = useSettings((s) => s.oiHalf);
  const tall = useSettings((s) => s.chartTall);
  const [sheet, setSheet] = useState<"" | "tz">("");
  const oiWan = Math.round(oiHalf / 1e4);
  const setOiWan = (v: number) => { if (Number.isFinite(v) && v >= 1) setSettings({ oiHalf: v * 1e4 }); };
  const [checking, setChecking] = useState(false);
  const [uploading, setUploading] = useState(false);
  const { isUpdatePending } = useUpdates();
  const server = useServerSettings();
  const insets = useSafeAreaInsets();
  const barShown = !!(server.nChanged || server.msg);

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
      <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: barShown ? 150 + insets.bottom : 40 }} keyboardShouldPersistTaps="handled">
        <PoolSection f={server} />
        <RuleSection f={server} />
        <StorageSection f={server} />

        <Card>
          <SectionHead icon="desktop-outline" title="显示" tip={<>
            <P>只保存在本机，改完立即生效。<B>时区</B>决定 K 线横轴、十字光标与页面上所有时间的显示；服务端按 UTC 对齐 K 线（4H 在 UTC+8 下为 0/4/8/12/16/20 点），数据本身不变。</P>
            <P><B>持仓量条</B>：扫描列表每行的底色长条表示该合约持仓量（USDT）。按对数平滑换算：占比 = r / (1 + r)，r = √(持仓量 ÷ 半满点)。持仓量等于半满点时正好半行；10 倍 ≈ 76%，100 倍 ≈ 91%，1000 倍 ≈ 97%；1/10 ≈ 24%，1/100 ≈ 9%。</P>
          </>} />
          <Row label="时区" value={tzOptions.find((o) => o.value === tz)?.label ?? tz} onPress={() => setSheet("tz")} />
          <Row label="K 线高度" value={tall ? "高" : "标准"} onPress={() => setSettings({ chartTall: !tall })} />
          <Field label="持仓量条半满点" desc="扫描列表底色长条：持仓量等于它时占半行，按对数平滑" style={{ borderTopWidth: 0 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <Segmented small style={{ flex: 1 }} value={String(oiWan)} onChange={(v) => setOiWan(Number(v))} options={OI_PRESETS} />
              <NumInput value={oiWan} unit="万" width={104} decimal={false} onChange={setOiWan} />
            </View>
            <View style={{ gap: 3, marginTop: 2 }}>
              {OI_SAMPLES.map((v) => (
                <View key={v} style={{ flexDirection: "row", justifyContent: "space-between", paddingHorizontal: 8, paddingVertical: 2, borderRadius: 4, overflow: "hidden", backgroundColor: colors.bg }}>
                  <View style={{ position: "absolute", left: 0, top: 0, bottom: 0, width: `${oiFrac(v, oiHalf) * 100}%`, backgroundColor: colors.oiBar }} />
                  <Text style={[{ fontSize: fs.xs, color: colors.text }, tnum]}>{usd(v)}</Text>
                  <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{Math.round(oiFrac(v, oiHalf) * 100)}%</Text>
                </View>
              ))}
            </View>
          </Field>
        </Card>

        <InteractSection />
        <AiAuthSection />

        <Card>
          <Text style={[styles.title, { marginBottom: 4 }]}>所有者密钥</Text>
          <Text style={styles.note}>修改交易、AI、策略等受保护内容时需要。只保存在本机钥匙串。</Text>
          <View style={{ flexDirection: "row", gap: 10, marginTop: 10 }}>
            <PillButton small variant="soft" label={keySet ? "更换密钥" : "填写密钥"} onPress={() => void askAdminKey()} style={{ flex: 1 }} />
            {keySet ? <PillButton small variant="ghost" label="清除" onPress={() => confirm("清除所有者密钥", "清除后，下次受保护的操作会再次询问。", "清除", clearAdminKey, true)} style={{ flex: 1 }} /> : null}
          </View>
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

        <DevSection />

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
      <SaveBar f={server} bottom={insets.bottom} />
      <PickerSheet visible={sheet === "tz"} onClose={() => setSheet("")} title="时区" value={tz} options={tzOptions} onPick={(v) => setSettings({ tz: v })} />
    </Screen>
  );
}

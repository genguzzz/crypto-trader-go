// Strategy page (web views/trading/StrategyEditor.vue, read-only on the phone): ?version=<id> opens a
// stored version (built-in or published), ?source=<id> one of my drafts. Shows the declared meta,
// parameters, published versions and the code with syntax colors; drafts can be checked and
// published here (owner key). Editing code and trial runs happen in the web editor (opened in the
// in-app browser) — CodeMirror isn't worth porting to a phone keyboard.
import { useMemo, useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { WEB_ORIGIN } from "@/lib/api";
import { haptic, openPage } from "@/lib/device";
import { useQuery } from "@/lib/query";
import { tradeApi, type CheckResult, type Source, type StrategyEntry, type StrategyVersion } from "@/lib/tradeApi";
import { day, directionText } from "@/lib/tradeMeta";
import { colors, fs, radius } from "@/lib/theme";
import { Card, ErrorBox, Loading, PanelTitle, Pull, Screen, Tag, TopBar, tap, toast, toastError } from "@/components/ui";
import { Banner, Muted, SmallButton, mono } from "@/components/trade/common";
import { CodeView } from "@/components/trade/CodeView";
import { ParamTable } from "@/components/trade/StrategiesPane";

export default function StrategyScreen() {
  const { version: versionId, source: sourceParam } = useLocalSearchParams<{ version?: string; source?: string }>();
  const sourceId = Number(sourceParam) || 0;
  const router = useRouter();
  const ver = useQuery<StrategyVersion>(versionId ? `trade:version:${versionId}` : null, () => tradeApi.strategy(versionId!), { staleMs: 10 * 60_000 });
  const src = useQuery<Source>(sourceId ? `trade:source:${sourceId}` : null, () => tradeApi.source(sourceId), { staleMs: 10_000 });
  const lib = useQuery<StrategyEntry[]>("trade:strategies", () => tradeApi.strategies(), { staleMs: 30_000 });
  const [check, setCheck] = useState<CheckResult | null>(null);
  const [checking, setChecking] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [copying, setCopying] = useState(false);
  const [published, setPublished] = useState<StrategyVersion | null>(null);

  const readonly = !sourceId;
  const s = src.data;
  const v = ver.data;
  const code = (readonly ? v?.code : s?.code) ?? "";
  const pubEntry = useMemo(() => (lib.data ?? []).find((e) => e.latest.meta?.sourceId === sourceId && sourceId), [lib.data, sourceId]);
  // what the code declares: the stored version, else the last check / the published version of the draft
  const declared: StrategyVersion | undefined = readonly ? v : check?.strategies[0] ?? pubEntry?.latest;
  const diags = check?.diagnostics ?? [];
  const errors = diags.filter((d) => d.severity === "error").length;
  const status = readonly
    ? { t: v?.meta?.builtin ? "内置 · 只读" : "已发布版本 · 只读", c: undefined }
    : !s ? null : !s.published ? { t: "草稿 · 未发布", c: "warn" as const } : s.dirty ? { t: "有未发布的改动", c: "warn" as const } : { t: "已发布", c: "good" as const };
  const backtestId = readonly ? versionId : published?.id || (s && !s.dirty ? s.published : "");
  const title = (readonly ? v?.name : declared?.name || s?.name) || "策略";
  const webPath = readonly ? `/trading/strategies/view/${versionId}` : `/trading/strategies/edit/${sourceId}`;

  const runCheck = async () => {
    if (!code.trim()) return;
    setChecking(true);
    try {
      const r = await tradeApi.check(code);
      setCheck(r);
      const n = r.diagnostics.filter((d) => d.severity === "error").length;
      if (n) toast(`发现 ${n} 个错误`, undefined, undefined, true);
      else toast("没有发现问题", r.strategies[0] ? `策略 ${r.strategies[0].name}，${r.strategies[0].params.length} 个参数` : undefined);
    } catch (e) { toastError("检查失败", e); } finally { setChecking(false); }
  };
  const publish = async () => {
    if (!s) return;
    setPublishing(true);
    void haptic("medium");
    try {
      const r = await tradeApi.publishSource(s.id);
      setCheck(r.check);
      setPublished(r.version ?? null);
      toast(`已发布 ${r.version?.id.slice(0, 8) ?? ""}`, "可以回测或添加到账户");
      await Promise.all([src.refresh(), lib.refresh()]);
    } catch (e) {
      toastError("发布失败", e);
      // show why: the server's check of the same code
      tradeApi.check(code).then(setCheck).catch(() => {});
    } finally { setPublishing(false); }
  };
  const copyAsNew = async () => {
    if (!versionId) return;
    setCopying(true);
    try {
      const n = await tradeApi.createSource({ fromVersion: versionId });
      toast("已复制为新策略", n.name);
      router.replace({ pathname: "/trading/strategy", params: { source: String(n.id) } });
    } catch (e) { toastError("复制失败", e); } finally { setCopying(false); }
  };

  const err = readonly ? ver.error : src.error;
  const loading = readonly ? !v && !err : !s && !err;
  return (
    <Screen>
      <TopBar title={title} sub={declared ? `${declared.timeframe} · ${directionText(declared.meta)}` : undefined}
        right={<Pressable hitSlop={10} accessibilityLabel="在网页版打开" onPress={() => { tap(); void openPage(WEB_ORIGIN + webPath); }}><Ionicons name="open-outline" size={21} color={colors.textSoft} /></Pressable>} />
      <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: 48 }} refreshControl={<Pull onRefresh={() => Promise.all([ver.refresh(), src.refresh(), lib.refresh()])} />}>
        {err ? <ErrorBox text={err} /> : null}
        {loading ? <Loading text="读取代码" /> : null}
        {status ? <View style={{ flexDirection: "row", gap: 6 }}><Tag text={status.t} tone={status.c} /></View> : null}
        {readonly && v ? (
          <Banner tone="warn" text={`${v.meta?.builtin ? "内置策略只读，作为示例和教材：" : "已发布的版本不可修改："}点「复制为新策略」得到一份可编辑的副本。代码里的中文注释讲解了每一步。`} />
        ) : null}

        <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
          {readonly ? (
            <SmallButton label={copying ? "复制中…" : "复制为新策略"} icon="copy-outline" disabled={copying || !v} onPress={() => void copyAsNew()} />
          ) : (
            <>
              <SmallButton label={checking ? "检查中…" : "检查"} icon="search" disabled={checking || !code} onPress={() => void runCheck()} />
              <SmallButton label={publishing ? "发布中…" : "发布"} icon="rocket-outline" tone="primary" disabled={publishing || errors > 0 || !s} onPress={() => void publish()} />
            </>
          )}
          {backtestId ? <SmallButton label="回测" icon="flask-outline" onPress={() => router.push({ pathname: "/trading/new", params: { mode: "backtest", strategy: backtestId } })} /> : null}
          <SmallButton label={readonly ? "试运行（网页）" : "编辑与试运行（网页）"} icon="open-outline" onPress={() => void openPage(WEB_ORIGIN + webPath)} />
        </View>
        {!readonly ? <Muted>代码在网页编辑器里修改（自动保存为草稿）；这里可以查看、检查和发布。发布 = 保存为不可变版本，供回测和实例使用。</Muted> : null}

        {diags.length || check ? (
          <Card>
            <PanelTitle title="问题" right={diags.length ? <Tag text={String(diags.length)} tone={errors ? "bad" : "warn"} /> : undefined} />
            {!diags.length ? <Text style={{ color: colors.up, fontSize: fs.sm }}>没有发现问题{declared ? `：策略 ${declared.name}，${declared.params.length} 个参数` : ""}</Text> : null}
            {diags.map((d, i) => (
              <View key={i} style={{ flexDirection: "row", gap: 6, padding: 8, marginBottom: 4, borderRadius: 6, backgroundColor: colors.raised }}>
                <Ionicons name={d.severity === "error" ? "alert-circle" : "warning"} size={14} color={d.severity === "error" ? colors.down : colors.warn} style={{ marginTop: 2 }} />
                <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 2 }}>第 {d.line} 行</Text>
                <Text style={{ flex: 1, fontSize: fs.sm, color: colors.text }}>{d.message}</Text>
              </View>
            ))}
          </Card>
        ) : null}

        {declared ? (
          <Card style={{ gap: 8 }}>
            <PanelTitle title={declared.name} right={<Text style={[mono, { color: colors.textFaint }]}>{declared.id ? declared.id.slice(0, 8) : ""}</Text>} style={{ marginBottom: 0 }} />
            <Text style={{ fontSize: fs.md, color: colors.textSoft, lineHeight: 19 }}>{declared.description || "—"}</Text>
            <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>{declared.timeframe} · {directionText(declared.meta)} · {(declared.params || []).length} 个参数</Text>
            {(declared.params || []).length ? <ParamTable params={declared.params} /> : null}
          </Card>
        ) : null}

        {!readonly && pubEntry ? (
          <Card>
            <PanelTitle title="已发布版本" hint={`${pubEntry.versions.length} 个`} />
            {pubEntry.versions.map((x, i) => (
              <Pressable key={x.id} onPress={() => { tap(); router.push({ pathname: "/trading/strategy", params: { version: x.id } }); }}
                style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 9, borderTopWidth: i ? 1 : 0, borderColor: colors.border, backgroundColor: pressed ? colors.raised : "transparent" })}>
                <Text style={{ fontSize: fs.sm, color: colors.text, width: 34 }}>v{pubEntry.versions.length - i}</Text>
                <Text style={[mono, { color: colors.textSoft }]}>{x.id.slice(0, 8)}</Text>
                <Text style={{ flex: 1, fontSize: fs.xs, color: colors.textFaint }}>{day(x.createdAt)}{x.id === pubEntry.latest.id ? " · 最新" : ""}</Text>
                <Ionicons name="chevron-forward" size={14} color={colors.textFaint} />
              </Pressable>
            ))}
          </Card>
        ) : null}

        {code ? (
          <View style={{ gap: 6 }}>
            <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text, marginLeft: 2 }}>代码</Text>
            <View style={{ borderRadius: radius.md, overflow: "hidden" }}>
              <CodeView code={code} diagnostics={diags} />
            </View>
          </View>
        ) : null}
      </ScrollView>
    </Screen>
  );
}

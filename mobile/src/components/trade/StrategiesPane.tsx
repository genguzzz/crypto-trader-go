// 交易 → 策略库 (web views/trading/Strategies.vue): my strategies (drafts + published), built-in
// strategies with version history and parameter tables, new from template, copy, reload.
// Creating / copying / deleting drafts runs code paths that need the owner key.
import { useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { WEB_ORIGIN } from "@/lib/api";
import { openPage } from "@/lib/device";
import { useMeta } from "@/lib/market";
import { useQuery } from "@/lib/query";
import { templates } from "@/lib/strategyTemplates";
import { tradeApi, type ParamSpec, type Source, type StrategyEntry } from "@/lib/tradeApi";
import { day, directionText, paramText, universeText } from "@/lib/tradeMeta";
import { colors, fs, radius } from "@/lib/theme";
import { Card, ErrorBox, IconButton, Loading, PickerSheet, Pull, Sheet, TAB_BAR_SPACE, Tag, confirm, tap, toast, toastError } from "../ui";
import { InfoTip, Muted, SmallButton, mono } from "./common";

interface StratData { entries: StrategyEntry[]; sources: Source[] }

export function StrategiesPane() {
  const router = useRouter();
  const meta = useMeta().data;
  const ruleName = (id: string) => meta?.rules.find((r) => r.id === id)?.name || id;
  const q = useQuery<StratData>("trade:strategyLib", async () => {
    const [entries, sources] = await Promise.all([tradeApi.strategies(), tradeApi.sources().catch(() => [] as Source[])]);
    return { entries, sources };
  }, { staleMs: 20_000 });
  const [reloading, setReloading] = useState(false);
  const [newOpen, setNewOpen] = useState(false);
  const [creating, setCreating] = useState("");
  const [sel, setSel] = useState<Record<string, string>>({});
  const [verFor, setVerFor] = useState<StrategyEntry | null>(null);

  const entries = q.data?.entries ?? [];
  const sources = q.data?.sources ?? [];
  const builtin = entries.filter((e) => !e.latest.meta?.sourceId);
  const published = new Map(entries.filter((e) => e.latest.meta?.sourceId).map((e) => [e.latest.meta!.sourceId!, e]));
  const orphans = entries.filter((e) => e.latest.meta?.sourceId && !sources.some((s) => s.id === e.latest.meta!.sourceId));
  const groups = [{ key: "lib", title: "内置策略", items: builtin }, ...(orphans.length ? [{ key: "orphan", title: "草稿已删除的已发布策略", items: orphans }] : [])];
  const shown = (e: StrategyEntry) => e.versions.find((v) => v.id === sel[e.name]) ?? e.latest;

  const openSource = (id: number) => router.push({ pathname: "/trading/strategy", params: { source: String(id) } });
  const openVersion = (id: string) => router.push({ pathname: "/trading/strategy", params: { version: id } });
  const backtest = (versionId: string) => router.push({ pathname: "/trading/new", params: { mode: "backtest", strategy: versionId } });

  const reload = async () => {
    setReloading(true);
    try {
      await tradeApi.reloadStrategies();
      const before = new Set(entries.flatMap((e) => e.versions.map((v) => v.id)));
      const after = await q.refresh().then(() => tradeApi.strategies());
      const fresh = after.flatMap((e) => e.versions).filter((v) => !before.has(v.id)).length;
      toast(fresh ? `发现 ${fresh} 个新版本` : "没有变化");
    } catch (e) { toastError("扫描失败", e); } finally { setReloading(false); }
  };
  const create = async (code: string, id: string) => {
    setCreating(id);
    try {
      const s = await tradeApi.createSource({ code });
      setNewOpen(false);
      await q.refresh();
      openSource(s.id);
    } catch (e) { toastError("创建失败", e); } finally { setCreating(""); }
  };
  const copy = async (versionId: string) => {
    try {
      const s = await tradeApi.createSource({ fromVersion: versionId });
      toast("已复制为新策略", s.name);
      await q.refresh();
      openSource(s.id);
    } catch (e) { toastError("复制失败", e); }
  };
  const remove = (s: Source) => confirm("删除草稿", `删除草稿「${s.name}」？已发布的版本会保留，正在用它的实例不受影响。`, "删除", async () => {
    try { await tradeApi.deleteSource(s.id); toast("已删除草稿"); await q.refresh(); } catch (e) { toastError("删除失败", e); }
  }, true);

  return (
    <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: TAB_BAR_SPACE }} refreshControl={<Pull onRefresh={q.refresh} />}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Text style={{ fontSize: fs.xl, fontWeight: "600", color: colors.text }}>策略库</Text>
        <InfoTip title="策略库" items={[
          "策略是用 ctg SDK 写的 Python 类：声明参数、交易标的和周期，在每根已收盘 K 线上返回信号。",
          ["内置策略", "只读，作为示例；点「复制为新策略」得到可编辑的副本。"],
          ["我的策略", "在网页里编辑，自动保存为草稿，「发布」后生成不可变版本，才能用于回测和实例。"],
          "策略只发信号，下单、风控、仓位都由账户统一处理；策略进程在隔离容器里运行，没有网络。",
        ]} />
        <View style={{ flex: 1 }} />
        <IconButton name="book-outline" label="策略文档" onPress={() => void openPage(`${WEB_ORIGIN}/help/strategy`)} />
        <IconButton name={reloading ? "hourglass-outline" : "refresh"} label="重新扫描" onPress={() => { if (!reloading) void reload(); }} />
      </View>
      {q.error && !q.data ? <ErrorBox text={q.error} onRetry={() => void q.refresh()} /> : null}
      {!q.data && !q.error ? <Loading text="加载策略库" /> : null}

      {q.data ? (
        <>
          <View style={{ flexDirection: "row", alignItems: "center" }}>
            <Text style={{ flex: 1, fontSize: fs.lg, fontWeight: "600", color: colors.text }}>我的策略</Text>
            <SmallButton label="新建策略" icon="add" tone="primary" onPress={() => setNewOpen(true)} />
          </View>
          {!sources.length ? (
            <Card style={{ alignItems: "center", gap: 8 }}>
              <Muted>还没有自建策略。</Muted>
              <SmallButton label="从模板新建" icon="add" onPress={() => setNewOpen(true)} />
              <Muted>或在下面的内置策略上点「复制为新策略」。</Muted>
            </Card>
          ) : null}
          {sources.map((s) => {
            const pub = published.get(s.id);
            return (
              <Card key={s.id} onPress={() => openSource(s.id)} style={{ gap: 8 }}>
                <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                  <Text style={{ flexShrink: 1, fontSize: fs.xl, fontWeight: "600", color: colors.text }}>{s.name}</Text>
                  <Tag text={!s.published ? "草稿" : s.dirty ? "有未发布改动" : "已发布"} tone={!s.published || s.dirty ? "warn" : "good"} />
                  <View style={{ flex: 1 }} />
                  <Muted>改于 {day(s.updatedAt)}</Muted>
                </View>
                {pub ? (
                  <>
                    <Text style={{ fontSize: fs.md, color: colors.textSoft }}>{pub.latest.description || "—"}</Text>
                    <KV k="周期 / 方向" v={`${pub.latest.timeframe} · ${directionText(pub.latest.meta)}`} />
                    <KV k="已发布" v={`${pub.versions.length} 个版本 · 最新 ${pub.latest.id.slice(0, 8)}`} />
                  </>
                ) : <Muted>还没发布：发布后才能回测和添加到账户。</Muted>}
                <View style={{ flexDirection: "row", gap: 8, alignItems: "center" }}>
                  <IconButton name="trash-outline" size={18} label="删除草稿" onPress={() => remove(s)} />
                  <View style={{ flex: 1 }} />
                  {s.published ? <SmallButton label="回测" onPress={() => backtest(s.published!)} /> : null}
                  <SmallButton label="代码与发布" icon="code-slash" tone="primary" onPress={() => openSource(s.id)} />
                </View>
              </Card>
            );
          })}

          {groups.map((g) => (
            <View key={g.key} style={{ gap: 12 }}>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 6, marginTop: 6 }}>
                <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text }}>{g.title}</Text>
                <Ionicons name="lock-closed" size={13} color={colors.textFaint} />
              </View>
              {!g.items.length ? <Card style={{ alignItems: "center" }}><Muted>没有内置策略</Muted></Card> : null}
              {g.items.map((e) => {
                const v = shown(e);
                return (
                  <Card key={e.name} style={{ gap: 10 }}>
                    <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                      <Text style={{ fontSize: fs.xl, fontWeight: "600", color: colors.text }}>{e.name}</Text>
                      <Tag text={v.timeframe} />
                      <Tag text={directionText(v.meta)} />
                      <View style={{ flex: 1 }} />
                      {e.versions.length > 1 ? (
                        <Pressable onPress={() => { tap(); setVerFor(e); }} style={{ flexDirection: "row", alignItems: "center", gap: 4, height: 28, paddingHorizontal: 8, borderRadius: 6, borderWidth: 1, borderColor: colors.borderStrong }}>
                          <Ionicons name="time-outline" size={13} color={colors.textSoft} />
                          <Text style={{ fontSize: fs.sm, color: colors.text }}>v{e.versions.length - e.versions.indexOf(v)} · {v.id.slice(0, 8)}</Text>
                        </Pressable>
                      ) : <Text style={[mono, { color: colors.textFaint }]}>{v.id.slice(0, 8)}</Text>}
                    </View>
                    <Text style={{ fontSize: fs.md, color: colors.textSoft, lineHeight: 19 }}>{v.description || "—"}</Text>
                    <KV k="默认标的" v={universeText(v.defaultUniverse, ruleName)} />
                    <KV k="版本" v={`${e.versions.length} 个 · 最新 ${day(e.latest.createdAt)}`} />
                    {(v.params || []).length ? <ParamTable params={v.params} /> : null}
                    <View style={{ flexDirection: "row", gap: 8, justifyContent: "flex-end", flexWrap: "wrap" }}>
                      <SmallButton label="代码与讲解" icon="document-text-outline" onPress={() => openVersion(v.id)} />
                      <SmallButton label="复制为新策略" icon="copy-outline" onPress={() => void copy(v.id)} />
                      <SmallButton label="回测" tone="primary" onPress={() => backtest(v.id)} />
                    </View>
                  </Card>
                );
              })}
            </View>
          ))}
        </>
      ) : null}

      {verFor ? (
        <PickerSheet visible onClose={() => setVerFor(null)} title={`${verFor.name} · 版本`} value={shown(verFor).id}
          options={verFor.versions.map((v, i) => ({ value: v.id, label: `v${verFor.versions.length - i} · ${v.id.slice(0, 8)}`, desc: day(v.createdAt) + (v.id === verFor.latest.id ? " · 最新" : "") }))}
          onPick={(id) => setSel((m) => ({ ...m, [verFor.name]: id }))} />
      ) : null}
      <Sheet visible={newOpen} onClose={() => setNewOpen(false)} title="新建策略" scroll>
        <Muted style={{ marginBottom: 10 }}>选一个起点，创建后在编辑器里修改（网页版编辑代码，App 里可查看与发布）。</Muted>
        <View style={{ gap: 8, paddingBottom: 8 }}>
          {templates.map((t) => (
            <Pressable key={t.id} disabled={!!creating} onPress={() => { tap(); void create(t.code, t.id); }} style={({ pressed }) => ({ gap: 3, padding: 12, borderRadius: radius.md, borderWidth: 1, borderColor: pressed ? colors.primary : colors.borderStrong, opacity: creating && creating !== t.id ? 0.5 : 1 })}>
              <Text style={{ fontSize: fs.md, fontWeight: "700", color: colors.text }}>{t.title}</Text>
              <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>{t.desc}</Text>
              {creating === t.id ? <Muted>创建中…</Muted> : null}
            </Pressable>
          ))}
        </View>
      </Sheet>
    </ScrollView>
  );
}

function KV({ k, v }: { k: string; v: string }) {
  return (
    <View style={{ flexDirection: "row", gap: 8 }}>
      <Text style={{ width: 72, fontSize: fs.xs, color: colors.textFaint, lineHeight: 19 }}>{k}</Text>
      <Text style={{ flex: 1, fontSize: fs.sm, color: colors.textSoft, lineHeight: 19 }}>{v}</Text>
    </View>
  );
}

const rangeText = (p: ParamSpec) =>
  p.type === "bool" ? "开 / 关" : p.type === "choice" || p.type === "timeframe" ? (p.labels ?? p.options ?? []).join(" / ") : `${p.min ?? "—"} – ${p.max ?? "—"}${p.step ? ` / ${p.step}` : ""}`;

/** Parameter table of a version: name, default, range, description (rows, phone-shaped). */
export function ParamTable({ params }: { params: ParamSpec[] }) {
  return (
    <View style={{ borderTopWidth: 1, borderColor: colors.border }}>
      {params.map((p) => (
        <View key={p.name} style={{ paddingVertical: 7, borderBottomWidth: 1, borderColor: colors.border, gap: 2 }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <Text style={{ fontSize: fs.sm, color: colors.text, fontWeight: "500" }}>{p.label || p.name}</Text>
            {p.aiTunable ? <Tag text="AI" tone="info" /> : null}
            {p.group ? <Text style={{ fontSize: 10, color: colors.textFaint }}>{p.group}</Text> : null}
            <View style={{ flex: 1 }} />
            <Text style={{ fontSize: fs.sm, color: colors.text }}>{paramText(p, p.default)}</Text>
          </View>
          <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{rangeText(p)}{p.desc ? ` · ${p.desc}` : ""}</Text>
        </View>
      ))}
    </View>
  );
}

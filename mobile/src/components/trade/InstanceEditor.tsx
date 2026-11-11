// Add a strategy instance to an account (pick → configure → create and start) or edit one
// (PATCH = new config version), in a form sheet (web InstanceEditor + StrategyPicker).
import { useEffect, useState } from "react";
import { Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@/lib/query";
import { aiApi, tradeApi, type ExecMode, type Instance, type StrategyEntry, type StrategyVersion, type Universe } from "@/lib/tradeApi";
import { day, paramError, universeError } from "@/lib/tradeMeta";
import { colors, fs } from "@/lib/theme";
import { Loading, PickerSheet, PillButton, Tag, tap, toast } from "../ui";
import { FormSheet, Muted, SmallButton, mono } from "./common";
import { InstanceForm } from "./forms";

export const freshUniverse = (v: StrategyVersion): Universe =>
  JSON.parse(JSON.stringify(v.defaultUniverse?.screener || v.defaultUniverse?.static?.length ? v.defaultUniverse : { static: [] }));
export const defaultParams = (v: StrategyVersion) => Object.fromEntries((v.params || []).map((p) => [p.name, p.default]));

/** AI ready for the exec hint (configured and last call fine). */
export function useAiReady() {
  const q = useQuery("ai:status", () => aiApi.status(), { staleMs: 30_000 });
  return q.data ? q.data.configured && !q.data.lastError : undefined;
}

export function InstanceEditor({ open, onClose, accountId, inst, startOnCreate, onSaved }: {
  open: boolean; onClose: () => void; accountId: number; inst?: Instance | null; startOnCreate?: boolean; onSaved: (i: Instance) => void;
}) {
  const [version, setVersion] = useState<StrategyVersion | null>(null);
  const [params, setParams] = useState<Record<string, number>>({});
  const [universe, setUniverse] = useState<Universe>({ static: [] });
  const [exec, setExec] = useState<ExecMode>("auto");
  const [err, setErr] = useState("");
  const [saving, setSaving] = useState(false);
  const aiReady = useAiReady();
  const entries = useQuery<StrategyEntry[]>(open && !inst ? "trade:strategies" : null, () => tradeApi.strategies(), { staleMs: 30_000 });

  // reset when the sheet opens (render-time adjustment), then load the edited instance's version
  const [openFor, setOpenFor] = useState<string | null>(null);
  const openKey = open ? `${inst?.id ?? "new"}` : null;
  if (openKey !== openFor) {
    setOpenFor(openKey);
    if (open) {
      setErr("");
      setVersion(null);
      if (inst) {
        setParams({ ...inst.params });
        setUniverse(JSON.parse(JSON.stringify(inst.universe || { static: [] })));
        setExec(inst.exec);
      }
    }
  }
  useEffect(() => {
    if (open && inst) tradeApi.strategy(inst.versionId).then(setVersion).catch((e: Error) => setErr(e.message));
  }, [open, inst]);

  const pick = async (v: StrategyVersion) => {
    try {
      const full = v.params ? v : await tradeApi.strategy(v.id);
      setVersion(full);
      setParams(defaultParams(full));
      setUniverse(freshUniverse(full));
      setExec("auto");
    } catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
  };

  let invalid = "";
  if (!version) invalid = "请选择策略";
  else {
    invalid = universeError(universe);
    if (!invalid) for (const p of version.params || []) { const e = paramError(p, params[p.name]); if (e) { invalid = `${p.name}：${e}`; break; } }
  }
  const changed = !inst || JSON.stringify([params, universe, exec]) !== JSON.stringify([inst.params, inst.universe, inst.exec]);

  const save = async () => {
    if (invalid || !version) return;
    setSaving(true);
    setErr("");
    try {
      let out: Instance;
      if (inst) out = await tradeApi.patchInstance(inst.id, { params, universe, exec });
      else {
        out = await tradeApi.createInstance(accountId, { versionId: version.id, params, universe, exec });
        if (startOnCreate) out = await tradeApi.instanceAction(out.id, "start");
      }
      toast(inst ? "已保存为新版本" : startOnCreate ? "已添加并启动" : "已添加");
      onSaved(out);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const title = inst ? `修改「${inst.name}」` : "添加策略";
  return (
    <FormSheet visible={open} onClose={onClose} title={title}
      footer={version ? (
        <View style={{ gap: 8 }}>
          <Text style={{ fontSize: fs.sm, color: err || invalid ? colors.down : colors.textFaint }}>
            {err || invalid || (inst ? "已有持仓不受影响；新参数从下一根 K 线开始生效" : startOnCreate ? "创建后立即启动" : "")}
          </Text>
          <PillButton label={saving ? "保存中…" : inst ? "保存为新版本" : startOnCreate ? "创建并启动" : "创建"} onPress={() => void save()} disabled={saving || !!invalid || !changed} loading={saving} />
        </View>
      ) : undefined}>
      {!inst && !version ? (
        <StrategyPicker entries={entries.data ?? null} error={entries.error || err} onPick={(v) => void pick(v)} />
      ) : version ? (
        <View style={{ gap: 14 }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            {!inst ? <SmallButton label="换一个" icon="chevron-back" onPress={() => setVersion(null)} /> : null}
            <Text style={{ fontSize: fs.lg, fontWeight: "700", color: colors.text }}>{version.name}</Text>
            <Tag text={version.timeframe} />
            {inst ? <Muted>当前配置 v{inst.configVersion}，保存后为 v{inst.configVersion + 1}</Muted> : null}
          </View>
          <InstanceForm version={version} params={params} universe={universe} exec={exec} onParams={setParams} onUniverse={setUniverse} onExec={setExec} aiReady={aiReady} />
        </View>
      ) : !err ? (
        <Loading text="读取策略" />
      ) : (
        <Text style={{ color: colors.down, fontSize: fs.sm }}>{err}</Text>
      )}
    </FormSheet>
  );
}

/** Strategy library list for attaching a strategy, with a version choice (web StrategyPicker). */
export function StrategyPicker({ entries, error, onPick }: { entries: StrategyEntry[] | null; error?: string; onPick: (v: StrategyVersion) => void }) {
  const [ver, setVer] = useState<Record<string, string>>({});
  const [verFor, setVerFor] = useState<StrategyEntry | null>(null);
  const chosen = (e: StrategyEntry) => e.versions.find((v) => v.id === ver[e.name]) ?? e.latest;
  if (error) return <Text style={{ color: colors.down, fontSize: fs.sm }}>{error}</Text>;
  if (!entries) return <Loading text="加载策略库…" />;
  if (!entries.length) return <Muted>策略库为空：把策略文件放进 strategies/library 后在“策略库”页重新扫描。</Muted>;
  return (
    <View>
      {entries.map((e) => {
        const c = chosen(e);
        return (
          <View key={e.name} style={{ paddingVertical: 12, borderBottomWidth: 1, borderColor: colors.border, gap: 6 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
              <Text style={{ fontSize: fs.lg, fontWeight: "700", color: colors.text }}>{e.name}</Text>
              <Tag text={c.timeframe} />
              <Text style={[mono, { color: colors.textFaint }]}>{c.id.slice(0, 8)}</Text>
            </View>
            <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>{c.description || "—"}</Text>
            <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{(c.params || []).length} 个参数{(c.params || []).length ? `：${c.params.map((p) => p.name).join(", ")}` : ""}</Text>
            <View style={{ flexDirection: "row", gap: 8, justifyContent: "flex-end" }}>
              {e.versions.length > 1 ? (
                <Pressable onPress={() => { tap(); setVerFor(e); }} style={{ flexDirection: "row", alignItems: "center", gap: 4, height: 30, paddingHorizontal: 10, borderRadius: 6, borderWidth: 1, borderColor: colors.borderStrong }}>
                  <Ionicons name="time-outline" size={14} color={colors.textSoft} />
                  <Text style={{ fontSize: fs.sm, color: colors.text }}>{c.id.slice(0, 8)}{c.id === e.latest.id ? "（最新）" : ""}</Text>
                </Pressable>
              ) : null}
              <SmallButton label="添加" icon="add" tone="primary" onPress={() => onPick(c)} />
            </View>
          </View>
        );
      })}
      {verFor ? (
        <PickerSheet visible onClose={() => setVerFor(null)} title={`${verFor.name} · 版本`} value={chosen(verFor).id}
          options={verFor.versions.map((v, i) => ({ value: v.id, label: `${v.id.slice(0, 8)}${v.id === verFor.latest.id ? "（最新）" : ""}`, desc: `${day(v.createdAt)} · v${verFor.versions.length - i}` }))}
          onPick={(id) => setVer((m) => ({ ...m, [verFor.name]: id }))} />
      ) : null}
    </View>
  );
}

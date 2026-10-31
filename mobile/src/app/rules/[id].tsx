// One rule (web views/Rules.vue main column): header with "use in scanner", the track record
// (replay and live Rank IC, t, top-20% excess/net per horizon, cumulative long IC chart) and the
// weight editor (save = new version, save as a new rule, delete own rules).
import { useMemo, useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import type { Field, Rule } from "@/lib/api";
import { num, time } from "@/lib/fmt";
import { setRule, useMeta, useRuleId } from "@/lib/market";
import { useQuery } from "@/lib/query";
import { refreshMeta, sysApi, verdictTone, type Stats, type Track } from "@/lib/sysApi";
import { track } from "@/lib/track";
import { colors, fs, tnum } from "@/lib/theme";
import { LineChart } from "@/components/Charts";
import { B, Field as FormField, InfoTip, Input, NumInput, P, Result } from "@/components/Form";
import { ScoringExplainer } from "@/components/ScoringExplainer";
import { Card, EmptyState, ErrorBox, Loading, PanelTitle, PillButton, Pull, Screen, Segmented, Tag, TopBar, confirm, styles, toast, toneColor } from "@/components/ui";

export default function RuleScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const meta = useMeta();
  const ruleId = useRuleId(meta.data);
  const cur = meta.data?.rules.find((r) => r.id === id);
  const tr = useQuery<Track>(cur ? `track:${id}` : null, () => sysApi.track(id), { interval: 30_000, staleMs: 20_000 });

  return (
    <Screen>
      <TopBar title={cur?.name ?? id} sub={cur ? `${cur.id} · v${cur.version}` : undefined} />
      {!meta.data ? (
        meta.error ? <ErrorBox text={meta.error} onRetry={meta.refresh} /> : <Loading text="加载规则" />
      ) : !cur ? (
        <EmptyState icon="flask-outline" text={`规则 ${id} 不存在或已删除`} />
      ) : (
        <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: 60 }} keyboardShouldPersistTaps="handled" refreshControl={<Pull onRefresh={() => Promise.all([refreshMeta(), tr.refresh()])} />}>
          <Card style={{ gap: 8 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
              <Text style={{ fontSize: fs.x2, fontWeight: "700", color: colors.text }}>{cur.name}</Text>
              <Text style={{ fontSize: fs.md, color: colors.textFaint }}>v{cur.version}</Text>
              {cur.preset ? <Tag text="对照" /> : null}
              <Tag text="未验证" tone="warn" />
            </View>
            {cur.description ? <Text style={styles.body}>{cur.description}</Text> : null}
            {tr.data && !tr.data.replayable ? <Text style={styles.note}>含“不可回放”字段，无法做历史回放，只能积累上线后战绩。</Text> : null}
            <PillButton small variant={ruleId === cur.id ? "soft" : "primary"} icon={ruleId === cur.id ? "checkmark" : "scan-outline"} label={ruleId === cur.id ? "扫描页使用中" : "在扫描页使用"} disabled={ruleId === cur.id}
              onPress={() => { setRule(cur.id); track("rule.use", { id: cur.id }); toast("扫描页已切换规则", cur.name); }} style={{ alignSelf: "flex-start" }} />
          </Card>

          <TrackCard tr={tr.data} error={tr.error} onRetry={tr.refresh} />
          <Editor key={`${cur.id}:${cur.version}`} rule={cur} fields={meta.data.fields} onSaved={() => setTimeout(() => void tr.refresh(), 2500)} />
        </ScrollView>
      )}
    </Screen>
  );
}

// ---------- track record ----------

function TrackCard({ tr, error, onRetry }: { tr?: Track; error?: string; onRetry: () => void }) {
  const hasLive = !!tr?.live?.some((s) => s.crossSections > 0);
  const [kind, setKind] = useState<"replay" | "live" | "">("");
  const k = kind || (hasLive ? "live" : "replay");
  const list = (k === "replay" ? tr?.replay : tr?.live) ?? [];
  const series = useMemo(() => {
    const out = [];
    const r = tr?.replay?.[0]?.cumLongIC, l = tr?.live?.[0]?.cumLongIC;
    if (r?.length) out.push({ label: "回放 1h", color: colors.text, points: r.map((p) => ({ t: p.ts, v: p.v })) });
    if (l?.length) out.push({ label: "上线后 1h", color: colors.primary, width: 2, points: l.map((p) => ({ t: p.ts, v: p.v })) });
    return out;
  }, [tr]);

  return (
    <Card>
      <PanelTitle title="战绩" right={<InfoTip title="战绩怎么看">
        <P><B>Rank IC</B>：同一时刻活跃池内“分数排名”与“之后 h 小时实际收益排名”的 Spearman 相关，取值 −1～1；0.02–0.05 在因子研究里已算有信号。空头 IC 用 −收益计算。</P>
        <P><B>t 值</B>：IC 均值 ÷ 标准误；每个周期只取不重叠样本（1h 每小时、4h 每 4 小时…），避免重复计数。|t| &gt; 2 大致对应 5% 显著性。</P>
        <P><B>前 20% 超额 / 净</B>：分数最高的五分之一，平均收益比池均值多多少（毛）；再扣双边 taker 5bps×2 和点差后的绝对收益（净）。</P>
        <P><B>结论门槛</B>：≥30 个截面、t &gt; 2 且扣费后为正，才标“有初步证据”；显著为负标“方向相反”。</P>
        <P><B>历史回放</B>：用 1H 数据按当时的监控池重算，只含现在仍上市的币（幸存者偏差），点差按 2bps 假设。<B>上线后</B>：每个整点真实快照，不可改写。</P>
        <P>曲线为多头累计 IC：持续向上 = 稳定有效，向下 = 反向。</P>
      </InfoTip>} />
      {!tr ? (
        error ? <ErrorBox text={error} onRetry={onRetry} /> : <Loading text="加载战绩" />
      ) : (
        <View style={{ gap: 10 }}>
          <Text style={styles.note}>回放：{tr.replayState} · 上线后：{tr.liveSince ? `自 ${time(tr.liveSince, true)} 起每整点记录` : "尚无记录（每个整点快照一次活跃池）"}</Text>
          <Segmented value={k} onChange={setKind} options={[{ value: "replay", label: "历史回放" }, { value: "live", label: "上线后" }]} />
          <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>{k === "replay" ? "1H 数据，当前挂牌币，含幸存者偏差，点差假设 2bps" : "真实时点快照，不可改写"}</Text>
          {list.length ? list.map((s) => <HorizonRow key={s.horizonH} s={s} />) : <Text style={styles.note}>暂无数据</Text>}
          {series.length ? (
            <View style={{ marginTop: 4 }}>
              <Text style={{ fontSize: fs.sm, color: colors.textFaint, marginBottom: 4 }}>多头累计 IC（1h）</Text>
              <LineChart series={series} height={170} fmtY={(v) => v.toFixed(2)} fmtT={(t) => time(t, true)} />
            </View>
          ) : null}
        </View>
      )}
    </Card>
  );
}

const sgn = (v: number | null) => toneColor(v == null ? "" : v > 0 ? "up" : "down", colors.textSoft);

function HorizonRow({ s }: { s: Stats }) {
  const empty = !s.crossSections;
  return (
    <View style={{ padding: 10, borderRadius: 8, backgroundColor: colors.bg, borderWidth: 1, borderColor: colors.border, gap: 8 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Text style={[{ fontSize: fs.xl, fontWeight: "700", color: colors.text, minWidth: 34 }, tnum]}>{s.horizonH}h</Text>
        <Text style={[{ fontSize: fs.sm, color: colors.textFaint }, tnum]}>截面 {s.crossSections}<Text style={{ color: colors.textDim }}>×{s.medianNames}</Text></Text>
        <View style={{ flex: 1 }} />
        <Tag text={s.verdict} tone={verdictTone(s.verdict)} />
      </View>
      {empty ? null : (
        <View style={{ flexDirection: "row", gap: 8 }}>
          <Side label="多" color={colors.up} ic={s.longIC} t={s.longT} ex={s.longTopExcessBps} net={s.longTopNetBps} />
          <Side label="空" color={colors.down} ic={s.shortIC} t={s.shortT} ex={s.shortTopExcessBps} net={s.shortTopNetBps} />
        </View>
      )}
    </View>
  );
}

function Side({ label, color, ic, t, ex, net }: { label: string; color: string; ic: number | null; t: number | null; ex: number | null; net: number | null }) {
  const k = { fontSize: fs.xs, color: colors.textFaint };
  return (
    <View style={{ flex: 1, gap: 3 }}>
      <Text style={{ fontSize: fs.xs, color, fontWeight: "600" }}>{label}头</Text>
      <Text style={[{ fontSize: fs.md, color: colors.textSoft }, tnum]}><Text style={k}>IC </Text><Text style={{ color: sgn(ic), fontWeight: "600" }}>{num(ic, 3)}</Text><Text style={k}> (t {num(t, 1)})</Text></Text>
      <Text style={[{ fontSize: fs.md, color: colors.textSoft }, tnum]}><Text style={k}>前20% </Text>{num(ex, 1)} / <Text style={{ color: sgn(net) }}>{num(net, 1)}</Text><Text style={k}> bps</Text></Text>
    </View>
  );
}

// ---------- weight editor ----------

const clone = (r: Rule): Rule => JSON.parse(JSON.stringify(r));

function Editor({ rule, fields, onSaved }: { rule: Rule; fields: Field[]; onSaved: () => void }) {
  const router = useRouter();
  const [draft, setDraft] = useState<Rule>(() => clone(rule));
  const [newId, setNewId] = useState("");
  const [busy, setBusy] = useState<"" | "save" | "new" | "del">("");
  const [msg, setMsg] = useState<{ ok: boolean; t: string } | null>(null);
  const [openDesc, setOpenDesc] = useState<string | null>(null);
  const scorable = fields.filter((f) => f.key !== "turnover12h");
  const dirty = JSON.stringify(draft) !== JSON.stringify(rule);

  const setW = (side: "long" | "short", k: string, v: number) =>
    setDraft((d) => {
      const w = { ...d[side] };
      if (!isFinite(v) || v === 0) delete w[k];
      else w[k] = v;
      return { ...d, [side]: w };
    });

  const save = async (asNew: boolean) => {
    const body: Partial<Rule> = { ...draft };
    if (asNew) {
      if (!newId.trim()) { setMsg({ ok: false, t: "请填写新规则 ID（小写字母/数字/短横线）" }); return; }
      body.id = newId.trim();
    }
    setBusy(asNew ? "new" : "save");
    setMsg(null);
    try {
      const saved = await sysApi.saveRule(body);
      await refreshMeta();
      setRule(saved.id);
      track("rule.save", { id: saved.id, version: saved.version, asNew });
      toast(`已保存 ${saved.id} v${saved.version}`, "历史回放正在重算，上线后战绩从现在开始记录。");
      onSaved();
      if (saved.id !== rule.id) router.replace({ pathname: "/rules/[id]", params: { id: saved.id } });
    } catch (e) {
      setMsg({ ok: false, t: "保存失败：" + (e instanceof Error ? e.message : String(e)) });
    } finally {
      setBusy("");
    }
  };

  const del = () =>
    confirm("删除规则", `删除规则 ${rule.id}？（已记录的快照保留在数据库中）`, "删除", async () => {
      setBusy("del");
      try {
        await sysApi.deleteRule(rule.id);
        await refreshMeta();
        toast("已删除规则", rule.id);
        router.back();
      } catch (e) {
        setMsg({ ok: false, t: e instanceof Error ? e.message : String(e) });
      } finally {
        setBusy("");
      }
    }, true);

  return (
    <Card>
      <PanelTitle title="编辑权重" right={<InfoTip title="编辑权重">
        <P><B>正权重</B> = 该字段在池内越高越加分；<B>负权重</B> = 越低越加分；留空或 0 = 不使用。权重只看相对大小（会除以 Σ|w|）。</P>
        <P><B>点差门槛</B>：点差超过该值时标“门槛”，分数照算但提示不可交易成本过高。</P>
        <P><B>保存</B> = 发布新版本（v+1）：立即对当前池重新打分，后台重算历史回放；旧版本的战绩保留。</P>
        <ScoringExplainer />
      </InfoTip>} />
      <FormField label="名称" style={{ borderTopWidth: 0, paddingTop: 0 }}>
        <Input value={draft.name} onChangeText={(v) => setDraft((d) => ({ ...d, name: v }))} />
      </FormField>
      <FormField label="点差门槛" desc="bps，0 = 不限" right={<NumInput value={draft.gates.maxSpreadBps ?? 0} unit="bps" decimal={false} onChange={(v) => setDraft((d) => ({ ...d, gates: { ...d.gates, maxSpreadBps: isFinite(v) ? v : 0 } }))} />} />
      <FormField label="说明">
        <Input value={draft.description} multiline onChangeText={(v) => setDraft((d) => ({ ...d, description: v }))} style={{ height: undefined, minHeight: 40, paddingVertical: 9 }} />
      </FormField>

      <View style={{ flexDirection: "row", alignItems: "center", paddingTop: 10, paddingBottom: 6, borderTopWidth: 1, borderColor: colors.border }}>
        <Text style={{ flex: 1, fontSize: fs.sm, color: colors.textFaint }}>字段（点按看定义）</Text>
        <Text style={{ width: 92, textAlign: "center", fontSize: fs.sm, color: colors.up }}>多头权重</Text>
        <Text style={{ width: 92, textAlign: "center", fontSize: fs.sm, color: colors.down, marginLeft: 6 }}>空头权重</Text>
      </View>
      {scorable.map((f) => (
        <View key={f.key} style={{ paddingVertical: 6, borderTopWidth: 1, borderColor: colors.border }}>
          <View style={{ flexDirection: "row", alignItems: "center" }}>
            <Pressable style={{ flex: 1, paddingRight: 6 }} onPress={() => setOpenDesc(openDesc === f.key ? null : f.key)}>
              <Text style={{ fontSize: fs.md, color: colors.text }}>{f.label}</Text>
              {!f.replayable ? <View style={{ marginTop: 2 }}><Tag text="不可回放" /></View> : null}
            </Pressable>
            <NumInput value={draft.long[f.key] ?? null} width={92} onChange={(v) => setW("long", f.key, v)} />
            <View style={{ width: 6 }} />
            <NumInput value={draft.short[f.key] ?? null} width={92} onChange={(v) => setW("short", f.key, v)} />
          </View>
          {openDesc === f.key ? <Text style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textFaint, marginTop: 4 }}>{f.desc}</Text> : null}
        </View>
      ))}

      <View style={{ gap: 10, paddingTop: 12, borderTopWidth: 1, borderColor: colors.border }}>
        <PillButton label={`保存为 ${rule.id} v${rule.version + 1}`} loading={busy === "save"} disabled={!!busy || !dirty} onPress={() => void save(false)} />
        <View style={{ flexDirection: "row", gap: 8, alignItems: "center" }}>
          <Input value={newId} onChangeText={setNewId} placeholder="新规则 ID，如 my-rule" style={{ flex: 1 }} />
          <PillButton small variant="soft" label="另存为新规则" loading={busy === "new"} disabled={!!busy} onPress={() => void save(true)} />
        </View>
        {!rule.preset ? <PillButton small variant="ghost" icon="trash-outline" label="删除规则" disabled={!!busy} onPress={del} /> : null}
        {dirty ? <PillButton small variant="ghost" icon="arrow-undo-outline" label="撤销修改" onPress={() => { setDraft(clone(rule)); setMsg(null); }} /> : null}
        <Result r={msg} />
        <Text style={styles.note}>保存会发布新版本并立即对当前池重新打分；旧版本的战绩保留，便于比较。</Text>
      </View>
    </Card>
  );
}

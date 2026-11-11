// Server settings (web views/Settings.vue top half): monitoring pool (12h turnover threshold, top N,
// categories, live estimate), default scoring rule, storage cap. One draft with a save bar, like the
// web page; saving recomputes the pool and reruns the rule replay on the server.
import { useState } from "react";
import { Text, View } from "react-native";
import { useRouter } from "expo-router";
import type { Settings } from "@/lib/api";
import { catLabel, usd } from "@/lib/fmt";
import { useMeta } from "@/lib/market";
import { useQuery } from "@/lib/query";
import { setSettings } from "@/lib/settings";
import { bytes, refreshMeta, sysApi, weightsText } from "@/lib/sysApi";
import { track } from "@/lib/track";
import { colors, fs, tnum } from "@/lib/theme";
import { ScoringExplainer } from "../ScoringExplainer";
import { B, Field, InfoTip, LinkText, Meter, NumInput, P, SectionHead } from "../Form";
import { Card, Chip, Dot, PickerSheet, PillButton, Row, Segmented } from "../ui";

const clone = (x: Settings): Settings => JSON.parse(JSON.stringify(x));
const sameCats = (a: string[], b: string[]) => [...a].sort().join() === [...b].sort().join();

export function useServerSettings() {
  const meta = useMeta();
  const saved = meta.data?.settings;
  const [draft, setDraft] = useState<Settings | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [saving, setSaving] = useState(false);
  const s = draft ?? (saved ? clone(saved) : null);
  const set = (patch: Partial<Settings>) => { if (s) { setDraft({ ...s, ...patch }); setMsg(null); } };

  const changed: Record<string, boolean> = s && saved
    ? { threshold: s.threshold !== saved.threshold, topN: s.topN !== saved.topN, categories: !sameCats(s.categories, saved.categories), defaultRule: s.defaultRule !== saved.defaultRule, storageCapMB: s.storageCapMB !== saved.storageCapMB }
    : {};
  const nChanged = Object.values(changed).filter(Boolean).length;

  // limits mirror internal/market/settings.go validate
  const errors: Record<string, string> = {};
  if (s) {
    const w = s.threshold / 1e4;
    if (!Number.isFinite(w) || w <= 0) errors.threshold = "请输入阈值";
    else if (w < 10) errors.threshold = "不能低于 10 万";
    else if (w > 1_000_000) errors.threshold = "不能超过 100 亿";
    if (!Number.isInteger(s.topN) || s.topN < 1 || s.topN > 500) errors.topN = "请输入 1–500 的整数";
    if (!s.categories.length) errors.categories = "至少选择一个类别";
    const g = s.storageCapMB / 1024;
    if (!Number.isFinite(g) || g < 0.5 || g > 100) errors.storageCapMB = "请输入 0.5–100 GB";
  }
  const valid = !Object.keys(errors).length;

  const revert = () => { setDraft(null); setMsg(null); };
  const save = async () => {
    if (!s || !valid || !nChanged) return;
    setSaving(true);
    try {
      const poolChanged = changed.threshold || changed.topN || changed.categories;
      await sysApi.saveSettings(s);
      if (changed.defaultRule) setSettings({ ruleId: "" }); // saving a default applies it here too
      await refreshMeta();
      setDraft(null);
      track("settings.save", { pool: poolChanged, rule: changed.defaultRule, storage: changed.storageCapMB });
      setMsg({ ok: true, text: poolChanged ? "已保存并生效：活跃池已重算，规则历史回放在后台重跑" : "已保存并生效" });
    } catch (e) {
      setMsg({ ok: false, text: "保存失败：" + (e instanceof Error ? e.message : String(e)) });
    } finally {
      setSaving(false);
    }
  };
  return { s, saved, set, changed, nChanged, errors, valid, revert, save, saving, msg, meta: meta.data };
}
export type ServerForm = ReturnType<typeof useServerSettings>;

export function PoolSection({ f }: { f: ServerForm }) {
  const all = useQuery("sys:turnovers", async () => (await sysApi.turnovers()).rows ?? [], { staleMs: 60_000 }).data ?? [];
  const s = f.s;
  if (!s) return null;
  const catCount: Record<string, number> = {};
  for (const [, cat, turn, live] of all) if (live === 1 && turn > s.threshold) catCount[cat] = (catCount[cat] || 0) + 1;
  const cats = new Set(s.categories);
  const pass = all.filter(([, cat, turn, live]) => live === 1 && cats.has(cat) && turn > s.threshold);
  const byCat: Record<string, number> = {};
  for (const [, cat] of pass) byCat[cat] = (byCat[cat] || 0) + 1;
  const wan = s.threshold / 1e4;

  return (
    <Card>
      <SectionHead icon="radio-outline" title="监控池" tip={<>
        <P><B>12h 成交额</B> = 连续 720 根已确认 1m K 线的 USDT 成交额（volCcyQuote）之和，每确认一分钟重算一次；缺任何一分钟视为数据缺口，不参与排名。</P>
        <P><B>入池条件</B>：严格大于阈值，按成交额从高到低取前 N 个；达标不足 N 就显示实际数量，不拿低流动性合约凑数。</P>
        <P><B>类别</B>：加密 / 股票·ETF / 商品永续（OKX instCategory）。股票类成交集中在美股时段，没有现货和链上数据。</P>
        <P><B>保存后</B>：立即重算监控池；后台重跑 14 天历史回放，新进入的币会补齐历史数据（进度见“系统状态”页）。</P>
      </>} />
      <Field label="12h 成交额阈值" desc="只监控 12h USDT 成交额严格大于它的合约" changed={f.changed.threshold} error={f.errors.threshold}>
        <View style={{ gap: 8, alignItems: "flex-end" }}>
          <Segmented small style={{ alignSelf: "stretch" }} value={String(wan)} onChange={(v) => f.set({ threshold: Number(v) * 1e4 })} options={[100, 300, 500, 1000, 2000].map((v) => ({ value: String(v), label: v + "万" }))} />
          <NumInput value={wan} unit="万" width={96} onChange={(v) => f.set({ threshold: Math.round(v * 1e4) })} invalid={!!f.errors.threshold} />
        </View>
      </Field>
      <Field label="上限 N" desc="按成交额取前 N 个，达标不足时不凑数" changed={f.changed.topN} error={f.errors.topN}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          <Segmented small style={{ flex: 1 }} value={String(s.topN)} onChange={(v) => f.set({ topN: Number(v) })} options={[50, 100, 200, 300].map((v) => ({ value: String(v), label: String(v) }))} />
          <NumInput value={s.topN} unit="个" width={96} decimal={false} onChange={(v) => f.set({ topN: v })} invalid={!!f.errors.topN} />
        </View>
      </Field>
      <Field label="包含类别" desc="数字为当前超过阈值的合约数" changed={f.changed.categories} error={f.errors.categories}>
        <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>
          {Object.entries(catLabel).map(([c, l]) => {
            const on = cats.has(c);
            return <Chip key={c} label={l} active={on} count={all.length ? catCount[c] || 0 : undefined}
              onPress={() => f.set({ categories: on ? s.categories.filter((x) => x !== c) : [...s.categories, c] })} />;
          })}
        </View>
      </Field>
      <View style={{ flexDirection: "row", alignItems: "baseline", flexWrap: "wrap", gap: 6, paddingTop: 10, borderTopWidth: 1, borderColor: colors.border }}>
        <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>预计入池</Text>
        {all.length ? (
          <>
            <Text style={[{ fontSize: fs.x2, fontWeight: "700", color: colors.text }, tnum]}>{Math.min(pass.length, s.topN)}</Text>
            <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>个</Text>
            <Text style={{ fontSize: fs.sm, color: colors.textSoft, flexBasis: "100%" }}>
              超过 {usd(s.threshold)} 的共 {pass.length} 个{pass.length > s.topN ? `，截取前 ${s.topN}` : ""}{Object.entries(byCat).map(([c, n]) => ` · ${catLabel[c] ?? c} ${n}`).join("")}
            </Text>
          </>
        ) : <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>计算中…</Text>}
        <InfoTip title="预计入池"><P>按最新确认的 12h 成交额即时估算，保存后以服务端重算结果为准。</P></InfoTip>
      </View>
    </Card>
  );
}

export function RuleSection({ f }: { f: ServerForm }) {
  const router = useRouter();
  const [pick, setPick] = useState(false);
  const s = f.s;
  const rules = f.meta?.rules ?? [];
  const cur = rules.find((r) => r.id === s?.defaultRule);
  if (!s) return null;
  return (
    <Card>
      <SectionHead icon="flask-outline" title="评分规则" right={<LinkText label="规则与战绩 →" onPress={() => router.push("/rules")} />} tip={<>
        <P>这里选择扫描页和详情页<B>默认使用</B>的评分规则；扫描页也可以临时切换。</P>
        <P>权重、点差门槛、新建规则在“规则与战绩”页编辑；每次保存都会发布新版本，旧版本的战绩保留，便于比较。</P>
        <ScoringExplainer />
      </>} />
      <Row label="默认规则" value={cur ? `${cur.name} · v${cur.version}` : s.defaultRule} onPress={() => setPick(true)} right={f.changed.defaultRule ? <Dot tone="warn" /> : undefined} />
      {cur ? (
        <View style={{ gap: 3, paddingTop: 8 }}>
          <Text style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textFaint }}><Text style={{ color: colors.up }}>多 </Text>{weightsText(cur.long, f.meta?.fields)}</Text>
          <Text style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textFaint }}><Text style={{ color: colors.down }}>空 </Text>{weightsText(cur.short, f.meta?.fields)}</Text>
          <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>点差门槛 {cur.gates.maxSpreadBps ? `${cur.gates.maxSpreadBps} bps` : "不限"}</Text>
        </View>
      ) : null}
      <PickerSheet visible={pick} onClose={() => setPick(false)} title="默认评分规则" value={s.defaultRule} onPick={(v) => f.set({ defaultRule: v })}
        options={rules.map((r) => ({ value: r.id, label: r.name, badge: "未验证", group: r.preset ? "对照规则" : "我的规则", desc: `v${r.version} · ${r.description}` }))} />
    </Card>
  );
}

export function StorageSection({ f }: { f: ServerForm }) {
  const router = useRouter();
  const storage = useQuery("sys:storage", () => sysApi.storage(), { staleMs: 60_000 }).data;
  const s = f.s;
  if (!s) return null;
  const gb = +(s.storageCapMB / 1024).toFixed(2);
  const capBytes = s.storageCapMB * (1 << 20);
  return (
    <Card>
      <SectionHead icon="server-outline" title="数据存储" right={<LinkText label="存储明细 →" onPress={() => router.push("/status")} />} tip={<>
        <P>SQLite 总大小上限。各类数据（1m K 线、持仓量、订单流…）按比例分配配额并按合约分层保留；某类超出配额时从最低层开始缩短保留期。评分快照（战绩）永不删除。</P>
        <P>各类占用、配额与每层保留期见“系统状态 → 数据存储”。</P>
      </>} />
      <Field label="存储上限" desc="超出时从低优先级数据开始淘汰" changed={f.changed.storageCapMB} error={f.errors.storageCapMB}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          <Segmented small style={{ flex: 1 }} value={String(gb)} onChange={(v) => f.set({ storageCapMB: Math.round(Number(v) * 1024) })} options={[1, 2, 5, 10].map((v) => ({ value: String(v), label: v + " GB" }))} />
          <NumInput value={gb} unit="GB" width={96} onChange={(v) => f.set({ storageCapMB: Math.round(v * 1024) })} invalid={!!f.errors.storageCapMB} />
        </View>
      </Field>
      <Field label="当前占用" desc={storage ? `库文件 ${bytes(storage.fileBytes)}，其中可复用 ${bytes(storage.freeBytes)}` : "服务启动约 2 分钟后首次检查"}>
        {storage ? (
          <View style={{ gap: 6 }}>
            <Text style={[{ fontSize: fs.sm, color: colors.textFaint }, tnum]}><Text style={{ fontSize: fs.lg, fontWeight: "700", color: colors.text }}>{bytes(storage.usedBytes)}</Text> / {bytes(capBytes)}</Text>
            <Meter value={storage.usedBytes} max={capBytes} />
            {f.changed.storageCapMB && storage.usedBytes > capBytes * 0.9 ? <Text style={{ fontSize: fs.xs, color: colors.warn }}>新上限接近或低于当前占用，保存后会立即淘汰旧数据</Text> : null}
          </View>
        ) : null}
      </Field>
    </Card>
  );
}

/** Sticky bar while there are unsaved server settings or a fresh result (web .savebar). */
export function SaveBar({ f, bottom }: { f: ServerForm; bottom: number }) {
  const router = useRouter();
  if (!f.nChanged && !f.msg) return null;
  return (
    <View style={{ position: "absolute", left: 0, right: 0, bottom: 0, paddingTop: 10, paddingBottom: bottom + 10, paddingHorizontal: 14, backgroundColor: colors.bg, borderTopWidth: 1, borderColor: f.nChanged ? colors.borderStrong : colors.border, gap: 8 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        {f.nChanged ? (
          <>
            <Dot tone="warn" />
            <Text style={{ flex: 1, fontSize: fs.sm, color: colors.textSoft }}>{f.nChanged} 项未保存的更改{!f.valid ? <Text style={{ color: colors.down }}> · 请先修正标红的项</Text> : null}</Text>
          </>
        ) : f.msg ? (
          <>
            <Dot tone={f.msg.ok ? "ok" : "bad"} />
            <Text style={{ flex: 1, fontSize: fs.sm, color: f.msg.ok ? colors.textSoft : colors.down }}>{f.msg.text}</Text>
            {f.msg.ok ? <LinkText label="查看进度 →" onPress={() => router.push("/status")} /> : null}
          </>
        ) : null}
      </View>
      {f.nChanged ? (
        <View style={{ flexDirection: "row", gap: 10 }}>
          <PillButton small variant="soft" icon="arrow-undo-outline" label="撤销" disabled={f.saving} onPress={f.revert} style={{ flex: 1 }} />
          <PillButton small label="保存" loading={f.saving} disabled={!f.valid} onPress={() => void f.save()} style={{ flex: 1 }} />
        </View>
      ) : null}
    </View>
  );
}

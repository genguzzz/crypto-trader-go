// 监控告警 (web views/Alerts.vue + components/alerts/*): alert rules (price move / rule score /
// funding) with on/off, edit and delete; delivery channels (Telegram, Webhook) with test; recent
// firings with per-channel delivery status. Rules are checked once per confirmed minute: poll 60 s.
import { useState, type ReactNode } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { ago, sym, time } from "@/lib/fmt";
import { useMeta, useRuleId } from "@/lib/market";
import { mutate, useQuery } from "@/lib/query";
import { sysApi, type AlertChannels, type AlertEvent, type AlertRule, type AlertType } from "@/lib/sysApi";
import { track } from "@/lib/track";
import { colors, fs, tnum } from "@/lib/theme";
import { B, Code, Field, InfoTip, Input, NumInput, P, Result, Toggle } from "@/components/Form";
import { Card, Chip, EmptyState, ErrorBox, IconButton, Loading, PanelTitle, PillButton, Pull, Screen, Segmented, Sheet, Tag, TopBar, confirm, tap, toast, toastError, styles, type IconName } from "@/components/ui";

const alertTypes: { key: AlertType; label: string; icon: IconName; defaults: () => Record<string, string | number> }[] = [
  { key: "price_move", label: "价格异动", icon: "trending-up-outline", defaults: () => ({ window: "1h", pct: 3, dir: "both" }) },
  { key: "score", label: "规则分数", icon: "speedometer-outline", defaults: () => ({ ruleId: "", side: "long", min: 80 }) },
  { key: "funding", label: "资金费", icon: "cash-outline", defaults: () => ({ source: "settled", pctPerHour: 0.01, dir: "both" }) },
];
const typeMeta = (k: AlertType) => alertTypes.find((t) => t.key === k) ?? alertTypes[0];
const scopeLabel = (r: Pick<AlertRule, "scope" | "instId">) => (r.scope === "active" ? "活跃池" : r.scope === "watch" ? "自选" : r.instId?.replace("-USDT-SWAP", "") || "—");
function cooldownLabel(m: number) {
  if (!m) return "无冷却";
  if (m < 60) return `${m} 分钟`;
  return m % 60 === 0 ? `${m / 60} 小时` : `${(m / 60).toFixed(1)} 小时`;
}
function deliveryTag(v: string): { tone?: "good" | "bad" | "warn"; t: string } {
  if (v === "ok") return { tone: "good", t: "已送达" };
  if (v === "pending") return { t: "发送中" };
  if (v.endsWith("未配置")) return { tone: "warn", t: "未配置" };
  return { tone: "bad", t: "失败" };
}
const channelName: Record<string, string> = { telegram: "Telegram", webhook: "Webhook" };
const chIcon = (ch: string): IconName => (ch === "telegram" ? "paper-plane-outline" : "git-network-outline");

const RULES = "alerts:rules";

export default function AlertsScreen() {
  const rules = useQuery(RULES, async () => (await sysApi.alertRules()).rules ?? [], { interval: 60_000, staleMs: 20_000 });
  const history = useQuery("alerts:history", async () => (await sysApi.alertHistory(100)) ?? [], { interval: 60_000, staleMs: 20_000 });
  const channels = useQuery("alerts:channels", () => sysApi.alertChannels(), { staleMs: 60_000 });
  const [form, setForm] = useState<{ open: boolean; rule: AlertRule | null }>({ open: false, rule: null });
  const [toggling, setToggling] = useState<number | null>(null);
  const [shown, setShown] = useState(30);

  const upsert = (r: AlertRule) => mutate<AlertRule[]>(RULES, (l = []) => (l.some((x) => x.id === r.id) ? l.map((x) => (x.id === r.id ? r : x)) : [...l, r]));
  const toggle = async (r: AlertRule, on: boolean) => {
    setToggling(r.id);
    try {
      upsert(await sysApi.saveAlertRule({ ...r, enabled: on }));
    } catch (e) {
      toastError("未能更新规则", e);
    } finally {
      setToggling(null);
    }
  };
  const remove = (r: AlertRule) =>
    confirm("删除监控规则", `删除监控规则「${r.name}」？（历史记录保留）`, "删除", async () => {
      try {
        await sysApi.deleteAlertRule(r.id);
        mutate<AlertRule[]>(RULES, (l = []) => l.filter((x) => x.id !== r.id));
        toast("已删除", r.name);
      } catch (e) {
        toastError("未能删除", e);
      }
    }, true);

  const ch = channels.data;
  const hist = history.data;
  return (
    <Screen>
      <TopBar title="监控告警" right={<IconButton name="add" size={26} color={colors.primary} label="新建监控规则" onPress={() => setForm({ open: true, rule: null })} />} />
      <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: 60 }} refreshControl={<Pull onRefresh={() => Promise.all([rules.refresh(), history.refresh(), channels.refresh()])} />}>
        <Card>
          <PanelTitle title="监控规则" right={<View style={{ flexDirection: "row", alignItems: "center", gap: 12 }}>
            <InfoTip title="监控规则">
              <P><B>何时检查</B>：每个已收盘分钟确认后，用当时的事实面板与规则分数检查一次（盘中 10 秒预览不触发）。</P>
              <P><B>触发方式</B>：条件从“不满足”变为“满足”的那一分钟触发一次（边沿触发），持续满足不重复提醒；回落后再次满足会再触发。</P>
              <P><B>冷却</B>：同一规则、同一合约两次提醒的最短间隔；冷却期内的新触发被忽略。</P>
              <P><B>数据缺失</B>：缺少所需字段（如新币、数据缺口）时跳过，不算“不满足”。规则分数只对活跃池与自选计算。</P>
              <P><B>第一分钟</B>：新建/修改规则或服务重启后的第一分钟只记录状态、不发提醒，已经满足条件的合约要等回落后再次满足才触发（避免一次性提醒整个池子）。想确认推送是否通，用“推送渠道”里的“发送测试”。</P>
              <P><B>价格异动</B>：窗口内涨跌幅（收盘价，简单收益）。<B>规则分数</B>：所选评分规则的多头/空头分（未验证）。<B>资金费</B>：已结算资金费或当前预测资金费，按每小时换算（4h/8h 结算已折算）。</P>
            </InfoTip>
            <PillButton small icon="add" label="新建" onPress={() => setForm({ open: true, rule: null })} />
          </View>} />
          {!rules.data ? (
            rules.error ? <ErrorBox text={rules.error} onRetry={rules.refresh} /> : <Loading text="加载规则" />
          ) : !rules.data.length ? (
            <EmptyState icon="notifications-outline" text="还没有监控规则" />
          ) : (
            rules.data.map((r, i) => (
              <Pressable key={r.id} onPress={() => { tap(); setForm({ open: true, rule: r }); }} onLongPress={() => remove(r)}
                style={({ pressed }) => ({ flexDirection: "row", gap: 12, paddingVertical: 10, borderTopWidth: i ? 1 : 0, borderColor: colors.border, backgroundColor: pressed ? colors.raised : "transparent" })}>
                <View style={{ paddingTop: 2 }}><Toggle value={r.enabled} disabled={toggling === r.id} onChange={(v) => void toggle(r, v)} /></View>
                <View style={{ flex: 1, gap: 4, opacity: r.enabled ? 1 : 0.5 }}>
                  <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                    <Ionicons name={typeMeta(r.type).icon} size={14} color={colors.textFaint} />
                    <Text style={{ flex: 1, fontSize: fs.lg, fontWeight: "600", color: colors.text }} numberOfLines={1}>{r.name}</Text>
                  </View>
                  <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>{r.summary}</Text>
                  {r.error ? <Text style={{ fontSize: fs.xs, color: colors.warn }}>{r.error}</Text> : null}
                  <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
                    <Tag text={scopeLabel(r)} />
                    <Tag text={cooldownLabel(r.cooldownMin)} />
                    {r.channels.telegram ? <Ionicons name="paper-plane-outline" size={13} color={ch && !ch.telegram.configured ? colors.warn : colors.textSoft} /> : null}
                    {r.channels.webhook ? <Ionicons name="git-network-outline" size={13} color={ch && !ch.webhook.configured ? colors.warn : colors.textSoft} /> : null}
                    <View style={{ flex: 1 }} />
                    <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>最近触发 <Text style={{ color: colors.textSoft }}>{r.lastFired ? ago(r.lastFired) + "前" : "—"}</Text></Text>
                  </View>
                </View>
                <IconButton name="trash-outline" size={17} color={colors.textFaint} label={`删除 ${r.name}`} onPress={() => remove(r)} />
              </Pressable>
            ))
          )}
        </Card>

        <ChannelsCard channels={ch} onUpdate={(c) => mutate<AlertChannels>("alerts:channels", () => c)} error={channels.error} />

        <Card>
          <PanelTitle title="最近触发" right={<InfoTip title="最近触发"><P>最近 100 条（服务器保留 30 天、最多 5000 条）。投递状态：已送达 / 发送中 / 失败（点按看原因）。每个已收盘分钟自动刷新。</P></InfoTip>} />
          {!hist ? (
            history.error ? <ErrorBox text={history.error} onRetry={history.refresh} /> : <Loading text="加载历史" />
          ) : !hist.length ? (
            <Text style={styles.note}>暂无触发记录</Text>
          ) : (
            hist.slice(0, shown).map((ev, i) => <EventRow key={ev.id} ev={ev} first={!i} />)
          )}
          {hist && hist.length > shown ? <PillButton small variant="ghost" label={`显示更多（共 ${hist.length} 条）`} onPress={() => setShown((n) => n + 50)} style={{ marginTop: 8 }} /> : null}
        </Card>
      </ScrollView>
      <RuleForm open={form.open} rule={form.rule} channels={ch} onClose={() => setForm((f) => ({ ...f, open: false }))} onSaved={(r) => { upsert(r); setForm((f) => ({ ...f, open: false })); }} />
    </Screen>
  );
}

function EventRow({ ev, first }: { ev: AlertEvent; first: boolean }) {
  const router = useRouter();
  const ds = Object.entries(ev.delivery ?? {});
  return (
    <View style={{ paddingVertical: 8, gap: 3, borderTopWidth: first ? 0 : 1, borderColor: colors.border }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Pressable hitSlop={6} onPress={() => { tap(); router.push({ pathname: "/instrument/[id]", params: { id: ev.instId } }); }}>
          <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text }}>{sym(ev.instId)}</Text>
        </Pressable>
        <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(ev.ts, true)}</Text>
        <View style={{ flex: 1 }} />
        {ds.length ? ds.map(([c, v]) => {
          const d = deliveryTag(v);
          return (
            <Pressable key={c} hitSlop={6} onPress={() => { tap(); toast(`${channelName[c] ?? c}：${d.t}`, v === "ok" ? undefined : v); }}>
              <Tag text={d.t} tone={d.tone} icon={<Ionicons name={chIcon(c)} size={10} color={d.tone === "good" ? colors.up : d.tone === "bad" ? colors.down : d.tone === "warn" ? colors.warn : colors.textSoft} />} />
            </Pressable>
          );
        }) : <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>仅记录</Text>}
      </View>
      <Text style={{ fontSize: fs.sm, lineHeight: 18, color: colors.textSoft }}>{ev.message.replace(ev.instId + " ", "")}<Text style={{ color: colors.textFaint }}> · {ev.ruleName}</Text></Text>
    </View>
  );
}

// ---------- rule form (web components/alerts/RuleForm.vue) ----------

type Draft = Omit<AlertRule, "id" | "createdAt" | "updatedAt" | "summary"> & { id?: number };
const blank = (): Draft => ({ name: "", enabled: true, type: "price_move", params: alertTypes[0].defaults(), scope: "active", instId: "", cooldownMin: 60, channels: { telegram: true, webhook: false } });

function RuleForm({ open, rule, channels, onClose, onSaved }: { open: boolean; rule: AlertRule | null; channels?: AlertChannels; onClose: () => void; onSaved: (r: AlertRule) => void }) {
  return (
    <Sheet visible={open} onClose={onClose} title={rule ? "编辑监控规则" : "新建监控规则"} scroll>
      {open ? <RuleFormBody key={rule?.id ?? "new"} rule={rule} channels={channels} onClose={onClose} onSaved={onSaved} /> : null}
    </Sheet>
  );
}

function RuleFormBody({ rule, channels, onClose, onSaved }: { rule: AlertRule | null; channels?: AlertChannels; onClose: () => void; onSaved: (r: AlertRule) => void }) {
  const meta = useMeta();
  const ruleId = useRuleId(meta.data);
  const [draft, setDraft] = useState<Draft>(() => {
    const d: Draft = rule ? JSON.parse(JSON.stringify(rule)) : blank();
    if (d.type === "score" && !d.params.ruleId) d.params.ruleId = ruleId;
    return d;
  });
  const [byType, setByType] = useState<Record<string, Record<string, string | number>>>({ [draft.type]: draft.params });
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState("");
  const p = draft.params;
  const setP = (k: string, v: string | number) => setDraft((d) => ({ ...d, params: { ...d.params, [k]: v } }));
  // switching type keeps each type's params, so flipping back and forth loses nothing
  const setType = (t: AlertType) => {
    const keep = { ...byType, [draft.type]: draft.params };
    const np = keep[t] ?? alertTypes.find((x) => x.key === t)!.defaults();
    if (t === "score" && !np.ruleId) np.ruleId = ruleId || meta.data?.rules[0]?.id || "";
    setByType(keep);
    setDraft((d) => ({ ...d, type: t, params: np }));
  };
  const autoName = () => {
    if (draft.type === "price_move") return `${p.window} 异动 ${p.pct}%`;
    if (draft.type === "score") return `${meta.data?.rules.find((r) => r.id === p.ruleId)?.name || p.ruleId} ${p.side === "long" ? "多" : "空"} ≥ ${p.min}`;
    return `资金费 ${p.pctPerHour}%/h`;
  };
  const dirOpts = (up: string, down: string) => [{ value: "both", label: "双向" }, { value: "up", label: up, tone: "up" as const }, { value: "down", label: down, tone: "down" as const }];
  const numStr = (v: unknown) => String(v);
  const unconfigured = (draft.channels.telegram && !channels?.telegram.configured) || (draft.channels.webhook && !channels?.webhook.configured);

  const save = async () => {
    setSaving(true);
    setErr("");
    try {
      const d = { ...draft, name: draft.name.trim() || autoName() };
      const saved = await sysApi.saveAlertRule(d as Partial<AlertRule>);
      track("alert.save", { type: d.type, isNew: !d.id });
      toast(d.id ? "已保存监控规则" : "已新建监控规则", saved.summary);
      onSaved(saved);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const row = (label: string, child: ReactNode, desc?: string) => <Field label={label} desc={desc}>{child}</Field>;
  return (
    <View style={{ paddingBottom: 8 }}>
      <Field label="名称" style={{ borderTopWidth: 0, paddingTop: 0 }}><Input value={draft.name} maxLength={40} placeholder={autoName()} onChangeText={(v) => setDraft((d) => ({ ...d, name: v }))} /></Field>
      {row("类型", <Segmented value={draft.type} onChange={setType} options={alertTypes.map((t) => ({ value: t.key, label: t.label }))} />)}

      {draft.type === "price_move" ? (
        <>
          {row("窗口", <Segmented small value={String(p.window)} onChange={(v) => setP("window", v)} options={["15m", "1h", "4h", "24h"].map((v) => ({ value: v, label: v }))} />)}
          {row("方向", <Segmented small value={String(p.dir)} onChange={(v) => setP("dir", v)} options={dirOpts("涨", "跌")} />)}
          {row("涨跌幅 ≥（%）", <View style={{ flexDirection: "row", gap: 8, alignItems: "center" }}>
            <NumInput value={Number(p.pct)} width={90} unit="%" onChange={(v) => setP("pct", v)} />
            <Segmented small style={{ flex: 1 }} value={numStr(p.pct)} onChange={(v) => setP("pct", Number(v))} options={[2, 3, 5, 10].map((v) => ({ value: String(v), label: v + "%" }))} />
          </View>)}
        </>
      ) : draft.type === "score" ? (
        <>
          {row("评分规则", <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
            {(meta.data?.rules ?? []).map((r) => <Chip key={r.id} label={r.name} active={p.ruleId === r.id} onPress={() => setP("ruleId", r.id)} />)}
          </View>)}
          {row("方向", <Segmented small value={String(p.side)} onChange={(v) => setP("side", v)} options={[{ value: "long", label: "多头分", tone: "up" }, { value: "short", label: "空头分", tone: "down" }]} />)}
          {row("分数 ≥", <View style={{ flexDirection: "row", gap: 8, alignItems: "center" }}>
            <NumInput value={Number(p.min)} width={90} decimal={false} onChange={(v) => setP("min", v)} />
            <Segmented small style={{ flex: 1 }} value={numStr(p.min)} onChange={(v) => setP("min", Number(v))} options={[70, 80, 90].map((v) => ({ value: String(v), label: String(v) }))} />
          </View>)}
        </>
      ) : (
        <>
          {row("来源", <Segmented small value={String(p.source)} onChange={(v) => setP("source", v)} options={[{ value: "settled", label: "已结算" }, { value: "predicted", label: "预测" }]} />)}
          {row("方向", <Segmented small value={String(p.dir)} onChange={(v) => setP("dir", v)} options={dirOpts("正费率", "负费率")} />)}
          {row("每小时资金费 ≥（%）", <View style={{ flexDirection: "row", gap: 8, alignItems: "center" }}>
            <NumInput value={Number(p.pctPerHour)} width={90} unit="%" onChange={(v) => setP("pctPerHour", v)} />
            <Segmented small style={{ flex: 1 }} value={numStr(p.pctPerHour)} onChange={(v) => setP("pctPerHour", Number(v))} options={[0.005, 0.01, 0.02, 0.05].map((v) => ({ value: String(v), label: v + "%" }))} />
          </View>)}
        </>
      )}

      {row("范围", <Segmented small value={draft.scope} onChange={(v) => setDraft((d) => ({ ...d, scope: v }))} options={[{ value: "active", label: "活跃池" }, { value: "watch", label: "自选" }, { value: "inst", label: "单个合约" }]} />)}
      {draft.scope === "inst" ? row("合约", <Input value={draft.instId} autoCapitalize="characters" placeholder="BTC-USDT-SWAP" onChangeText={(v) => setDraft((d) => ({ ...d, instId: v.trim().toUpperCase() }))} />) : null}
      {row("冷却（分钟）", <View style={{ flexDirection: "row", gap: 8, alignItems: "center" }}>
        <NumInput value={draft.cooldownMin} width={90} decimal={false} onChange={(v) => setDraft((d) => ({ ...d, cooldownMin: v }))} />
        <Segmented small style={{ flex: 1 }} value={String(draft.cooldownMin)} onChange={(v) => setDraft((d) => ({ ...d, cooldownMin: Number(v) }))} options={[0, 15, 60, 240, 1440].map((v) => ({ value: String(v), label: v === 0 ? "无" : v < 60 ? `${v}m` : `${v / 60}h` }))} />
      </View>)}
      {row("推送到", <View style={{ gap: 6 }}>
        <View style={{ flexDirection: "row", gap: 8 }}>
          <Chip label="Telegram" icon="paper-plane-outline" active={draft.channels.telegram} onPress={() => setDraft((d) => ({ ...d, channels: { ...d.channels, telegram: !d.channels.telegram } }))} />
          <Chip label="Webhook" icon="git-network-outline" active={draft.channels.webhook} onPress={() => setDraft((d) => ({ ...d, channels: { ...d.channels, webhook: !d.channels.webhook } }))} />
        </View>
        {unconfigured ? <Tag text="所选渠道未配置，只记入历史" tone="warn" /> : null}
      </View>)}
      <Field label="启用" right={<Toggle value={draft.enabled} onChange={(v) => setDraft((d) => ({ ...d, enabled: v }))} />} />

      <View style={{ flexDirection: "row", gap: 10, marginTop: 8 }}>
        <PillButton variant="soft" label="取消" onPress={onClose} style={{ flex: 1 }} />
        <PillButton label="保存" loading={saving} onPress={() => void save()} style={{ flex: 1 }} />
      </View>
      {err ? <Text style={{ color: colors.down, fontSize: fs.sm, marginTop: 8 }}>{err}</Text> : null}
    </View>
  );
}

// ---------- delivery channels (web components/alerts/ChannelsCard.vue) ----------

type Ch = "telegram" | "webhook";

function ChannelsCard({ channels, onUpdate, error }: { channels?: AlertChannels; onUpdate: (c: AlertChannels) => void; error?: string }) {
  const [form, setForm] = useState({ botToken: "", chatId: channels?.telegram.chatId ?? "", url: "", secret: "" });
  const [seen, setSeen] = useState(channels);
  if (channels !== seen) {
    // server value changed (load / save): follow its chat id
    setSeen(channels);
    setForm((f) => ({ ...f, chatId: channels?.telegram.chatId ?? "" }));
  }
  const [busy, setBusy] = useState<Record<Ch, string>>({ telegram: "", webhook: "" });
  const [result, setResult] = useState<Record<Ch, { ok: boolean; t: string } | null>>({ telegram: null, webhook: null });
  const setB = (ch: Ch, v: string) => setBusy((b) => ({ ...b, [ch]: v }));
  const setR = (ch: Ch, v: { ok: boolean; t: string } | null) => setResult((r) => ({ ...r, [ch]: v }));
  const dirty = (ch: Ch) => (ch === "telegram" ? !!form.botToken || form.chatId !== (channels?.telegram.chatId || "") : !!form.url || !!form.secret);

  const save = async (ch: Ch) => {
    setB(ch, "save");
    setR(ch, null);
    try {
      const out = await sysApi.saveAlertChannels({
        telegram: { configured: false, botToken: form.botToken, chatId: form.chatId },
        webhook: { configured: false, url: form.url, secret: form.secret },
      });
      setForm((f) => ({ ...f, botToken: "", url: "", secret: "" }));
      onUpdate(out);
      setR(ch, { ok: true, t: "已保存" });
      return true;
    } catch (e) {
      setR(ch, { ok: false, t: e instanceof Error ? e.message : String(e) });
      return false;
    } finally {
      setB(ch, "");
    }
  };
  const test = async (ch: Ch) => {
    if (dirty(ch) && !(await save(ch))) return;
    setB(ch, "test");
    setR(ch, null);
    try {
      const r = await sysApi.testAlertChannel(ch);
      setR(ch, r.ok ? { ok: true, t: "测试消息已发送" } : { ok: false, t: r.error || "发送失败" });
    } catch (e) {
      setR(ch, { ok: false, t: e instanceof Error ? e.message : String(e) });
    } finally {
      setB(ch, "");
    }
  };
  const clear = (ch: Ch) =>
    confirm("清除配置", `清除 ${channelName[ch]} 配置？`, "清除", async () => {
      try {
        onUpdate(await sysApi.clearAlertChannel(ch));
        setR(ch, null);
      } catch (e) {
        setR(ch, { ok: false, t: e instanceof Error ? e.message : String(e) });
      }
    }, true);
  const ph = (masked: string, fallback: string) => (masked ? `已保存 ${masked}，留空不变` : fallback);

  const section = (ch: Ch, inputs: ReactNode) => {
    const configured = !!channels?.[ch].configured;
    return (
      <View style={{ gap: 8, paddingVertical: 12, borderTopWidth: ch === "webhook" ? 1 : 0, borderColor: colors.border }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          <Ionicons name={chIcon(ch)} size={15} color={colors.textFaint} />
          <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text, flex: 1 }}>{channelName[ch]}</Text>
          <Tag text={configured ? "已配置" : "未配置"} tone={configured ? "good" : undefined} />
        </View>
        {inputs}
        <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>
          <PillButton small label="保存" loading={busy[ch] === "save"} disabled={!!busy[ch] || !dirty(ch)} onPress={() => void save(ch)} />
          <PillButton small variant="soft" label="发送测试" loading={busy[ch] === "test"} disabled={!!busy[ch] || (!configured && !dirty(ch))} onPress={() => void test(ch)} />
          {configured ? <PillButton small variant="ghost" label="清除" disabled={!!busy[ch]} onPress={() => clear(ch)} /> : null}
        </View>
        <Result r={result[ch]} />
      </View>
    );
  };
  const lbl = (t: string) => <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{t}</Text>;

  return (
    <Card>
      <PanelTitle title="推送渠道" style={{ marginBottom: 0 }} right={<InfoTip title="推送渠道">
        <P><B>Telegram</B>：在 @BotFather 创建机器人得到 Bot Token；先给机器人发一条消息（群组需把机器人拉进群），再用 getUpdates 或 @userinfobot 查 Chat ID（群组为负数）。同一分钟的多条提醒合并成一条消息。</P>
        <P><B>Webhook</B>：每条提醒 POST 一次 JSON：<Code>{"{ruleId, rule, instId, type, value, message, ts}"}</Code>（测试消息带 <Code>test: true</Code>）。填了密钥会放在请求头 <Code>X-CTG-Secret</Code>，接收端据此校验。</P>
        <P><B>安全</B>：Token、密钥和 Webhook 地址只保存在服务器数据库，页面只显示末 4 位；输入框留空保存 = 不修改。发送失败会重试 3 次（网络错误 / 429 / 5xx），结果记在“最近触发”。</P>
      </InfoTip>} />
      {error && !channels ? <ErrorBox text={error} /> : null}
      {section("telegram", <>
        {lbl("Bot Token")}
        <Input value={form.botToken} secureTextEntry placeholder={ph(channels?.telegram.botToken || "", "123456789:AA…")} onChangeText={(v) => setForm((f) => ({ ...f, botToken: v }))} />
        {lbl("Chat ID")}
        <Input value={form.chatId} placeholder="123456789 / -100… / @channel" onChangeText={(v) => setForm((f) => ({ ...f, chatId: v }))} />
      </>)}
      {section("webhook", <>
        {lbl("地址")}
        <Input value={form.url} keyboardType="url" placeholder={ph(channels?.webhook.url || "", "https://…")} onChangeText={(v) => setForm((f) => ({ ...f, url: v }))} />
        {lbl("密钥（可选）")}
        <Input value={form.secret} secureTextEntry placeholder={ph(channels?.webhook.secret || "", "请求头 X-CTG-Secret")} onChangeText={(v) => setForm((f) => ({ ...f, secret: v }))} />
      </>)}
    </Card>
  );
}

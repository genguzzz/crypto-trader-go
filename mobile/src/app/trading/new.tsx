// New account wizard (web views/trading/NewAccount.vue): ① mode and name (backtest range) ② deposit
// and limits ③ strategies (pick → configure) ④ confirm, then create, attach, start or run the backtest.
// ?mode=backtest&strategy=<versionId> from the library; ?from=<accountId> copies a backtest as live.
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Pressable, ScrollView, Text, View, type ScrollViewInstance } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, usePreventRemove, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { haptic } from "@/lib/device";
import { useMeta } from "@/lib/market";
import { useQuery } from "@/lib/query";
import { defaultLimits, tradeApi, type ExecMode, type Limits, type Mode, type StrategyEntry, type StrategyVersion, type Universe } from "@/lib/tradeApi";
import { amt, confirmLabel, day, execLabel, limitErrors, modeLabel, paramError, paramsLine, parseDay, pctf, universeError, universeText } from "@/lib/tradeMeta";
import { colors, fs, tnum } from "@/lib/theme";
import { Card, IconButton, Loading, PillButton, Screen, Segmented, Sheet, Tag, TopBar, confirm, tap, toast } from "@/components/ui";
import { FieldRow, FormSheet, GroupTitle, InfoTip, LTag, Muted, NumInput, TextField, mono } from "@/components/trade/common";
import { InstanceForm, LimitsForm } from "@/components/trade/forms";
import { StrategyPicker, defaultParams, freshUniverse, useAiReady } from "@/components/trade/InstanceEditor";

const DAY = 86_400_000;
const STEPS = ["模式", "资金与风控", "策略", "确认"];
interface Draft { key: number; version: StrategyVersion; params: Record<string, number>; universe: Universe; exec: ExecMode }

export default function NewAccountScreen() {
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const qp = useLocalSearchParams<{ mode?: string; strategy?: string; from?: string }>();
  const meta = useMeta().data;
  const ruleName = (id: string) => meta?.rules.find((r) => r.id === id)?.name || id;
  const aiReady = useAiReady();
  const [now] = useState(Date.now); // the page's "today" (fixed while it is open)
  const today = day(now);
  const initMode: Mode = qp.mode === "backtest" ? "backtest" : "live";
  const nameFor = (m: Mode, f: string, t: string) => (m === "backtest" ? `回测 ${f.slice(5)} → ${t.slice(5)}` : `实时账户 ${today.slice(5)}`);

  const [step, setStep] = useState(1);
  const [from, setFrom] = useState(() => day(now - 30 * DAY));
  const [to, setTo] = useState(() => day(now - DAY));
  const [mode, setModeRaw] = useState<Mode>(initMode);
  const [name, setName] = useState(() => nameFor(initMode, day(now - 30 * DAY), day(now - DAY)));
  const [deposit, setDeposit] = useState(10_000);
  const [limits, setLimits] = useState<Limits>(defaultLimits());
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [editing, setEditing] = useState<number | null>(null);
  const [pickOpen, setPickOpen] = useState(false);
  const [touched, setTouched] = useState<Record<number, boolean>>({});
  const [stratErr, setStratErr] = useState("");
  const [loadingFrom, setLoadingFrom] = useState(() => !!Number(qp.from));
  const [creating, setCreating] = useState(false);
  const [progress, setProgress] = useState("");
  const [createErr, setCreateErr] = useState("");
  const [created, setCreated] = useState(false);
  const seq = useRef(0);
  const scroll = useRef<ScrollViewInstance>(null);
  const entries = useQuery<StrategyEntry[]>("trade:strategies", () => tradeApi.strategies(), { staleMs: 30_000 });

  const defaultName = (m: Mode, f = from, t = to) => nameFor(m, f, t);
  const setMode = (m: Mode) => {
    if (!name || name === defaultName(mode)) setName(defaultName(m));
    setModeRaw(m);
  };
  const addDraft = (v: StrategyVersion, over?: Partial<Draft>) => {
    const key = ++seq.current;
    setDrafts((ds) => [...ds, { key, version: v, params: defaultParams(v), universe: freshUniverse(v), exec: "auto", ...over }]);
    if (!over) setEditing(key);
  };

  // query presets (from the library / a backtest), once
  useEffect(() => {
    if (qp.strategy) tradeApi.strategy(qp.strategy).then((v) => addDraft(v, { exec: "auto" })).catch((e: Error) => setStratErr(e.message));
    const src = Number(qp.from);
    if (src) {
      void (async () => {
        try {
          const [d, insts] = await Promise.all([tradeApi.account(src), tradeApi.instances(src)]);
          setModeRaw("live");
          setDeposit(d.account.deposit);
          setLimits({ ...defaultLimits(), ...d.account.limits });
          setName(d.account.name.replace(/回测/g, "").trim() + " · 实时");
          for (const i of insts) {
            try {
              const v = await tradeApi.strategy(i.versionId);
              addDraft(v, { params: { ...i.params }, universe: JSON.parse(JSON.stringify(i.universe)), exec: i.exec });
            } catch { /* version gone: skip */ }
          }
        } catch (e) {
          setStratErr("读取原账户失败：" + (e instanceof Error ? e.message : String(e)));
        } finally {
          setLoadingFrom(false);
        }
      })();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // leaving mid-way asks first (web onBeforeRouteLeave)
  const allowRef = useRef<() => void>(() => {});
  const allowLeave = usePreventRemove(step > 1 && !created, ({ repeat }) => {
    confirm("放弃创建", "放弃正在创建的账户？", "放弃", () => { allowRef.current(); repeat(); }, true);
  });
  useEffect(() => { allowRef.current = allowLeave; }, [allowLeave]);

  // ---- validation ----
  const range = { from: parseDay(from), to: parseDay(to) + DAY };
  const err1: Record<string, string> = {};
  if (!name.trim()) err1.name = "请填写账户名称";
  if (mode === "backtest") {
    if (!isFinite(range.from) || !isFinite(range.to)) err1.range = "请选择日期区间";
    else if (range.from >= range.to) err1.range = "开始日期必须早于结束日期";
    else if (range.to > now + DAY) err1.range = "结束日期不能晚于今天";
    else if (range.to - range.from > 366 * DAY) err1.range = "区间不能超过一年";
  }
  const err2 = limitErrors(limits);
  if (!(deposit >= 100 && deposit <= 1e9)) err2.deposit = "请输入 100–10 亿 USDT";
  const draftError = (d: Draft) => {
    const ue = universeError(d.universe);
    if (ue) return ue;
    for (const p of d.version.params || []) { const e = paramError(p, d.params[p.name]); if (e) return `参数 ${p.name}：${e}`; }
    return "";
  };
  const bad = drafts.find(draftError);
  const err3 = mode === "backtest" && !drafts.length ? "回测至少需要一个策略" : bad ? `${bad.version.name}：${draftError(bad)}` : "";
  const stepOk = (n: number) => (n === 1 ? !Object.keys(err1).length : n === 2 ? !Object.keys(err2).length : n === 3 ? !err3 : true);
  const go = (n: number) => { if (n < step || [1, 2, 3].slice(0, n - 1).every(stepOk)) { void haptic("select"); setStep(n); scroll.current?.scrollTo({ y: 0, animated: false }); } };
  const next = () => {
    setTouched((t) => ({ ...t, [step]: true }));
    if (!stepOk(step)) { void haptic("error"); return; }
    go(Math.min(4, step + 1));
  };
  const rangePreset = useMemo(() => {
    const n = Math.round((range.to - range.from) / DAY);
    return to === day(now - DAY) && [7, 30, 90, 365].includes(n) ? String(n) : "";
  }, [range.to, range.from, to, now]);
  const setPreset = (v: string) => {
    const n = Number(v);
    const f = day(Date.now() - n * DAY), t = day(Date.now() - DAY);
    if (name === defaultName(mode)) setName(defaultName(mode, f, t));
    setFrom(f);
    setTo(t);
  };

  const create = async () => {
    if (![1, 2, 3].every(stepOk)) return;
    let made = false;
    setCreating(true);
    setCreateErr("");
    void haptic("medium");
    try {
      setProgress("创建账户…");
      const a = await tradeApi.createAccount({
        name: name.trim(), mode, deposit, limits,
        ...(mode === "backtest" ? { from: range.from, to: Math.min(range.to, Date.now() - 60_000) } : {}),
      });
      made = true;
      setCreated(true);
      allowLeave();
      for (const [i, d] of drafts.entries()) {
        setProgress(`挂载策略 ${i + 1}/${drafts.length}…`);
        const inst = await tradeApi.createInstance(a.id, { versionId: d.version.id, params: d.params, universe: d.universe, exec: d.exec });
        if (mode === "live") await tradeApi.instanceAction(inst.id, "start");
      }
      if (mode === "backtest") {
        setProgress("开始回测…");
        await tradeApi.runBacktest(a.id);
        toast("已创建并开始回测", a.name);
        router.replace({ pathname: "/trading/backtest/[id]", params: { id: String(a.id) } });
      } else {
        toast("已创建账户", a.name);
        router.replace({ pathname: "/trading/account/[id]", params: { id: String(a.id) } });
      }
    } catch (e) {
      void haptic("error");
      setCreateErr((made ? "账户已创建，但后续步骤失败：" : "创建失败：") + (e instanceof Error ? e.message : String(e)));
    } finally {
      setCreating(false);
      setProgress("");
    }
  };

  const modes: { value: Mode; icon: keyof typeof Ionicons.glyphMap; t: string; d: string; disabled?: boolean }[] = [
    { value: "live", icon: "radio-outline", t: "实时模拟", d: "用实时行情持续运行，模拟成交、手续费、滑点和资金费" },
    { value: "backtest", icon: "time-outline", t: "历史回测", d: "在选定区间按历史 K 线快速重放，结束后出报告" },
    { value: "real", icon: "business-outline", t: "实盘", d: "向 OKX 真实下单，需要单独授权", disabled: true },
  ];
  const editDraft = drafts.find((d) => d.key === editing);
  const patchDraft = (key: number, p: Partial<Draft>) => setDrafts((ds) => ds.map((d) => (d.key === key ? { ...d, ...p } : d)));
  const status = progress ? progress
    : touched[step] && step === 1 && Object.keys(err1).length ? Object.values(err1)[0]
    : touched[step] && step === 2 && Object.keys(err2).length ? "请先修正标红的项"
    : touched[step] && step === 3 && err3 ? err3 : "";

  return (
    <Screen>
      <TopBar title="新建交易账户" right={<InfoTip title="新建交易账户" items={[
        "先建账户（入金与风控规则），再把策略挂到账户上。多个策略共享同一账户的资金和风控。",
        "所有账户都是模拟盘：不会向 OKX 下任何真实订单。",
        "创建后可以随时追加策略、修改风控、入金或归档；修改策略参数会生成新的配置版本，旧信号保留原配置。",
      ]} />} />
      <View style={{ flexDirection: "row", gap: 6, paddingHorizontal: 12, paddingTop: 10 }}>
        {STEPS.map((s, i) => {
          const n = i + 1, on = step === n, done = step > n;
          return (
            <Pressable key={s} disabled={creating} onPress={() => go(n)} style={{ flex: 1, alignItems: "center", gap: 4, paddingTop: 8, borderTopWidth: 2, borderColor: on ? colors.primary : done ? colors.primarySoft : colors.borderStrong }}>
              <View style={{ width: 20, height: 20, borderRadius: 10, alignItems: "center", justifyContent: "center", backgroundColor: on ? colors.primary : done ? colors.primarySoft : colors.cardMuted }}>
                {done ? <Ionicons name="checkmark" size={12} color={colors.primary} /> : <Text style={{ fontSize: fs.xs, fontWeight: "700", color: on ? colors.onPrimary : colors.textSoft }}>{n}</Text>}
              </View>
              <Text style={{ fontSize: fs.xs, color: on ? colors.text : colors.textFaint, fontWeight: on ? "600" : "400" }}>{s}</Text>
            </Pressable>
          );
        })}
      </View>
      <ScrollView ref={scroll} keyboardShouldPersistTaps="handled" contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: 30 }}>
        {loadingFrom ? <Loading text="读取原账户配置" /> : null}

        {step === 1 ? (
          <Card>
            <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text, marginBottom: 10 }}>账户模式</Text>
            <View style={{ gap: 8, marginBottom: 6 }}>
              {modes.map((m) => {
                const on = mode === m.value;
                return (
                  <Pressable key={m.value} disabled={m.disabled} onPress={() => { void haptic("select"); setMode(m.value); }}
                    style={{ flexDirection: "row", gap: 10, padding: 12, borderRadius: 10, borderWidth: 1, borderColor: on ? colors.primary : colors.borderStrong, backgroundColor: on ? colors.primarySoft : "transparent", opacity: m.disabled ? 0.45 : 1 }}>
                    <Ionicons name={m.icon} size={18} color={on ? colors.primary : colors.textFaint} />
                    <View style={{ flex: 1, gap: 3 }}>
                      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                        <Text style={{ fontWeight: "600", color: colors.text, fontSize: fs.md }}>{m.t}</Text>
                        {m.disabled ? <Tag text="后续开放" /> : null}
                      </View>
                      <Text style={{ fontSize: fs.xs, color: colors.textFaint, lineHeight: 16 }}>{m.d}</Text>
                    </View>
                  </Pressable>
                );
              })}
            </View>
            <FieldRow label="账户名称" error={touched[1] ? err1.name : ""} stack>
              <View style={{ alignSelf: "stretch" }}><TextField value={name} onChange={setName} placeholder="如：主流趋势" /></View>
            </FieldRow>
            {mode === "backtest" ? (
              <FieldRow label="回测区间" desc="UTC+8 自然日，含首尾两天" error={err1.range} stack>
                <View style={{ gap: 8, alignSelf: "stretch" }}>
                  <Segmented small value={rangePreset} onChange={setPreset} options={[{ value: "7", label: "7 天" }, { value: "30", label: "30 天" }, { value: "90", label: "90 天" }, { value: "365", label: "1 年" }]} />
                  <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                    <View style={{ flex: 1 }}><TextField value={from} onChange={setFrom} placeholder="yyyy-mm-dd" maxLength={10} /></View>
                    <Text style={{ color: colors.textFaint }}>→</Text>
                    <View style={{ flex: 1 }}><TextField value={to} onChange={setTo} placeholder="yyyy-mm-dd" maxLength={10} /></View>
                  </View>
                </View>
              </FieldRow>
            ) : null}
          </Card>
        ) : step === 2 ? (
          <Card>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
              <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text }}>资金与风控</Text>
              <InfoTip title="资金与风控" items={["入金是模拟的初始资金（USDT）。实时账户之后可以继续入金；回测账户的入金在区间开始时一次到账。风控规则对账户下的所有策略生效。"]} />
            </View>
            <FieldRow label="初始入金" desc="模拟资金，USDT" error={err2.deposit} stack>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                <Segmented small value={String(deposit)} onChange={(v) => setDeposit(Number(v))} options={[1000, 10000, 100000].map((v) => ({ value: String(v), label: amt(v, 0) }))} />
                <NumInput value={deposit} onChange={setDeposit} unit="USDT" width={130} />
              </View>
            </FieldRow>
            <LimitsForm l={limits} onChange={setLimits} />
          </Card>
        ) : step === 3 ? (
          <Card>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6, marginBottom: 10 }}>
              <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text }}>挂载策略</Text>
              <InfoTip title="挂载策略" items={[
                "每个策略实例 = 策略代码的一个版本 + 你的参数 + 交易标的 + 执行方式。同一策略可以用不同参数挂多次。",
                "实时账户创建后立即启动这些策略；也可以先不挂，之后在账户页追加。回测账户至少需要一个策略。",
              ]} />
              <View style={{ flex: 1 }} />
              <PillButton small icon="add" label="添加策略" onPress={() => setPickOpen(true)} />
            </View>
            {!drafts.length ? <Muted>{mode === "backtest" ? "回测至少需要一个策略。" : "还没有挂载策略，可以稍后在账户页添加。"}</Muted> : null}
            {drafts.map((d) => {
              const e = touched[3] ? draftError(d) : "";
              return (
                <View key={d.key} style={{ borderWidth: 1, borderColor: e ? colors.down : colors.borderStrong, borderRadius: 10, padding: 10, marginBottom: 10, gap: 6 }}>
                  <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                    <Pressable style={{ flex: 1, gap: 4 }} onPress={() => { tap(); setEditing(d.key); }}>
                      <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
                        <Text style={{ fontWeight: "700", color: colors.text, fontSize: fs.md }}>{d.version.name}</Text>
                        <Tag text={d.version.timeframe} />
                        <Tag text={execLabel[d.exec].t} />
                      </View>
                      <Text style={{ fontSize: fs.xs, color: colors.textFaint }} numberOfLines={1}>{universeText(d.universe, ruleName)}</Text>
                    </Pressable>
                    <IconButton name="create-outline" size={18} label={`修改 ${d.version.name}`} onPress={() => setEditing(d.key)} />
                    <IconButton name="trash-outline" size={18} label={`移除 ${d.version.name}`} onPress={() => setDrafts((ds) => ds.filter((x) => x.key !== d.key))} />
                  </View>
                  {e ? <Text style={{ fontSize: fs.sm, color: colors.down }}>{e}</Text> : null}
                </View>
              );
            })}
            {stratErr ? <Text style={{ fontSize: fs.sm, color: colors.down }}>{stratErr}</Text> : null}
          </Card>
        ) : (
          <Card>
            <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text, marginBottom: 8 }}>确认</Text>
            <SumRow k="名称" v={name} />
            <SumRow k="模式" v={<><LTag l={modeLabel[mode]} />{mode === "backtest" ? <Text style={{ color: colors.text }}>  {from} → {to}</Text> : null}</>} />
            <SumRow k="初始入金" v={`${amt(deposit, 0)} USDT`} />
            <SumRow k="持仓 / 杠杆" v={`最多 ${limits.maxPositions} 个 · ${limits.maxLeverage}x`} />
            <SumRow k="单笔风险" v={`${pctf(limits.riskPct)} · 单合约 ≤ ${pctf(limits.maxInstPct, 0)}`} />
            <SumRow k="日亏损熔断" v={pctf(limits.dailyLossPct, 1)} />
            <SumRow k="成本" v={`滑点 ${limits.slippageBp} bp · 费率 ${limits.takerFeeBp}/${limits.makerFeeBp} bp`} />
            <SumRow k="止损 / 冷却" v={`${limits.requireStop ? "必须带止损" : "不强制止损"} · ${limits.cooldownMin} 分钟`} />
            <SumRow k="人工确认" v={`${confirmLabel[limits.confirm]}${limits.confirm !== "off" ? ` · ${limits.confirmTimeoutS} 秒后${limits.confirmDefault === "execute" ? "执行" : "跳过"}` : ""}`} />
            <GroupTitle title={`策略 ${drafts.length}`} />
            {drafts.map((d) => (
              <View key={d.key} style={{ paddingVertical: 8, borderBottomWidth: 1, borderColor: colors.border, gap: 3 }}>
                <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                  <Text style={{ fontWeight: "700", color: colors.text, fontSize: fs.md }}>{d.version.name}</Text>
                  <Tag text={d.version.timeframe} />
                  <Tag text={execLabel[d.exec].t} />
                </View>
                <Muted>{universeText(d.universe, ruleName)}</Muted>
                {d.version.params?.length ? <Text style={[mono, { color: colors.textFaint }]}>{paramsLine(d.params)}</Text> : null}
              </View>
            ))}
            {!drafts.length ? <Muted style={{ paddingVertical: 8 }}>不挂载策略（稍后在账户页添加）</Muted> : null}
            <Text style={{ fontSize: fs.sm, color: colors.textSoft, marginTop: 10 }}>{mode === "backtest" ? "创建后立即开始回测，完成后自动生成报告。" : "创建后立即启动上面的策略。"}</Text>
            {createErr ? <Text style={{ fontSize: fs.sm, color: colors.down, marginTop: 6 }}>{createErr}</Text> : null}
          </Card>
        )}
      </ScrollView>

      <View style={{ flexDirection: "row", alignItems: "center", gap: 10, paddingHorizontal: 12, paddingTop: 10, paddingBottom: insets.bottom + 10, borderTopWidth: 1, borderColor: colors.border, backgroundColor: colors.bg }}>
        <Text style={[{ flex: 1, fontSize: fs.sm, color: status && !progress ? colors.down : colors.textFaint }, tnum]} numberOfLines={2}>{status || `第 ${step} / 4 步`}</Text>
        {step > 1 ? <PillButton small variant="soft" icon="chevron-back" label="上一步" disabled={creating} onPress={() => go(step - 1)} /> : null}
        {step < 4 ? <PillButton small label="下一步" onPress={next} />
          : <PillButton small label={creating ? "创建中…" : mode === "backtest" ? "创建并开始回测" : "创建账户"} loading={creating} disabled={creating} onPress={() => void create()} />}
      </View>

      <Sheet visible={pickOpen} onClose={() => setPickOpen(false)} title="添加策略" scroll>
        <StrategyPicker entries={entries.data ?? null} error={entries.error} onPick={async (v) => {
          setPickOpen(false);
          try { addDraft(v.params ? v : await tradeApi.strategy(v.id)); } catch (e) { setStratErr(e instanceof Error ? e.message : String(e)); }
        }} />
      </Sheet>
      {editDraft ? (
        <FormSheet visible onClose={() => setEditing(null)} title={`${editDraft.version.name} · ${editDraft.version.timeframe}`}
          footer={<PillButton label="完成" onPress={() => setEditing(null)} />}>
          <View style={{ paddingBottom: 8 }}>
            <InstanceForm version={editDraft.version} params={editDraft.params} universe={editDraft.universe} exec={editDraft.exec} aiReady={aiReady}
              onParams={(params) => patchDraft(editDraft.key, { params })} onUniverse={(universe) => patchDraft(editDraft.key, { universe })} onExec={(exec) => patchDraft(editDraft.key, { exec })} />
          </View>
        </FormSheet>
      ) : null}
    </Screen>
  );
}

function SumRow({ k, v }: { k: string; v: ReactNode }) {
  return (
    <View style={{ flexDirection: "row", gap: 10, paddingVertical: 6 }}>
      <Text style={{ width: 84, fontSize: fs.sm, color: colors.textFaint }}>{k}</Text>
      <View style={{ flex: 1, flexDirection: "row", alignItems: "center", flexWrap: "wrap" }}>
        {typeof v === "string" ? <Text style={[{ fontSize: fs.md, color: colors.text, fontWeight: "500" }, tnum]}>{v}</Text> : v}
      </View>
    </View>
  );
}


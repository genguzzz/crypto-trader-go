// Strategy editor (web views/trading/StrategyEditor.vue): ?source=<id> edits one of my drafts,
// ?version=<id> opens a stored version (built-in or published) read-only. Tabs: 代码 (CodeEditor,
// draft autosaves 1.2 s and checks 1.5 s after typing stops, like the web) · 问题/说明 (diagnostics,
// declared meta and params, published versions) · 试运行 (days, universe, params) · 结果 (KPIs,
// trade-marked candles, signals / log / trades). Saving, checking, trials and publishing use the
// owner key through req's AdminKeySheet retry. Leaving the page or the app saves a pending draft.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AppState, Pressable, ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { WEB_ORIGIN } from "@/lib/api";
import { haptic, openPage } from "@/lib/device";
import { time } from "@/lib/fmt";
import { useMeta } from "@/lib/market";
import { invalidate, useQuery } from "@/lib/query";
import { tradeApi, type CheckResult, type ParamSpec, type Source, type StrategyEntry, type StrategyVersion, type TrialResult, type Universe } from "@/lib/tradeApi";
import { amt, day, directionText, exitKindLabel, paramError, pctf, tone, universeText } from "@/lib/tradeMeta";
import { colors, fs, radius, tnum } from "@/lib/theme";
import { Card, ErrorBox, Loading, PanelTitle, PillButton, Pull, Screen, Segmented, Tag, TopBar, tap, toast, toastError } from "@/components/ui";
import { CoinIcon } from "@/components/CoinIcon";
import { Banner, KpiGrid, LTag, Muted, SmallButton, mono, toneText } from "@/components/trade/common";
import { CodeEditor } from "@/components/trade/CodeEditor";
import { CodeView } from "@/components/trade/CodeView";
import { ParamsForm, UniversePicker } from "@/components/trade/forms";
import { RunLog } from "@/components/trade/InstanceCard";
import { SignalList } from "@/components/trade/rows";
import { ParamTable } from "@/components/trade/StrategiesPane";
import { TrialChart } from "@/components/trade/TrialChart";

type Tab = "code" | "info" | "run" | "result";
type SaveState = "saved" | "saving" | "dirty" | "error";
const DAYS = ["3", "7", "14", "30"] as const;
const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

export default function StrategyScreen() {
  const { version: versionId, source: sourceParam } = useLocalSearchParams<{ version?: string; source?: string }>();
  const sourceId = Number(sourceParam) || 0;
  const readonly = !sourceId;
  const router = useRouter();
  const meta = useMeta().data;
  const ruleName = (id: string) => meta?.rules.find((r) => r.id === id)?.name || id;
  const ver = useQuery<StrategyVersion>(versionId ? `trade:version:${versionId}` : null, () => tradeApi.strategy(versionId!), { staleMs: 10 * 60_000 });
  const lib = useQuery<StrategyEntry[]>("trade:strategies", () => tradeApi.strategies(), { staleMs: 30_000 });
  const v = ver.data;

  const [tab, setTab] = useState<Tab>("code");
  const [editing, setEditing] = useState(false);
  const [jump, setJump] = useState<{ line: number; n: number } | null>(null);

  // ---- draft: loaded fresh (never from the persisted cache, so a save can't overwrite newer code) ----
  const [source, setSource] = useState<Source | null>(null);
  const [loadErr, setLoadErr] = useState("");
  const [draft, setDraft] = useState("");
  const codeRef = useRef("");
  const saved = useRef("");
  const srcRef = useRef<Source | null>(null);
  const [saveState, setSaveState] = useState<SaveState>("saved");
  const [saveErr, setSaveErr] = useState("");
  const saveTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const checkTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const saving = useRef<Promise<void>>(Promise.resolve());

  // ---- check ----
  const [check, setCheck] = useState<CheckResult | null>(null);
  const [checking, setChecking] = useState(false);
  const [checkErr, setCheckErr] = useState("");

  // ---- trial inputs ----
  const [specs, setSpecs] = useState<ParamSpec[]>([]);
  const [params, setParams] = useState<Record<string, number>>({});
  const [universe, setUniverse] = useState<Universe>({ static: [] });
  const universeKey = useRef("");
  const [days, setDays] = useState<(typeof DAYS)[number]>("14");
  const [trial, setTrial] = useState<TrialResult | null>(null);
  const [running, setRunning] = useState(false);
  const [runErr, setRunErr] = useState("");
  const [resultTab, setResultTab] = useState<"signals" | "log" | "trades">("signals");
  const [pickTrade, setPickTrade] = useState(0);

  const [publishing, setPublishing] = useState(false);
  const [published, setPublished] = useState<StrategyVersion | null>(null);
  const [copying, setCopying] = useState(false);

  const specsRef = useRef<ParamSpec[]>([]);
  const resetParams = useCallback((ps: ParamSpec[], u: Universe) => {
    const prevSpecs = specsRef.current;
    specsRef.current = ps;
    setSpecs(ps);
    setParams((old) => {
      const next: Record<string, number> = {};
      for (const p of ps) {
        const o = old[p.name];
        const prev = prevSpecs.find((x) => x.name === p.name);
        next[p.name] = o !== undefined && prev?.type === p.type && !paramError(p, o) ? o : p.default;
      }
      return next;
    });
    const k = JSON.stringify(u ?? { static: [] });
    if (k !== universeKey.current) { universeKey.current = k; setUniverse(JSON.parse(k) as Universe); } // the code's default changed: follow it
  }, []);

  const runCheck = useCallback(async () => {
    clearTimeout(checkTimer.current);
    const c = codeRef.current;
    if (readonly || !c.trim()) return;
    setChecking(true);
    try {
      const r = await tradeApi.check(c);
      setCheck(r);
      setCheckErr("");
      const st = r.strategies[0];
      if (st) resetParams(st.params || [], st.defaultUniverse);
    } catch (e) { setCheckErr(errMsg(e)); } finally { setChecking(false); }
  }, [readonly, resetParams]);

  const save = useCallback(() => {
    // serialised: a save waits for the one in flight, then sends whatever is current
    saving.current = saving.current.then(async () => {
      clearTimeout(saveTimer.current);
      const s = srcRef.current;
      const c = codeRef.current;
      if (readonly || !s || c === saved.current) return;
      setSaveState("saving");
      try {
        const n = await tradeApi.saveSource(s.id, c);
        srcRef.current = n;
        setSource(n);
        saved.current = c;
        setSaveState(codeRef.current === c ? "saved" : "dirty");
        setSaveErr("");
        invalidate("trade:strategyLib");
      } catch (e) { setSaveState("error"); setSaveErr(errMsg(e)); }
    });
    return saving.current;
  }, [readonly]);

  const applySource = useCallback((s: Source) => {
    setLoadErr("");
    srcRef.current = s;
    setSource(s);
    if (codeRef.current === saved.current) { // don't clobber unsaved typing
      codeRef.current = saved.current = s.code || "";
      setDraft(codeRef.current);
      setSaveState("saved");
      void runCheck();
    }
  }, [runCheck]);
  const load = useCallback(() => (sourceId ? tradeApi.source(sourceId).then(applySource, (e: unknown) => setLoadErr(errMsg(e))) : Promise.resolve()), [sourceId, applySource]);
  useEffect(() => {
    if (sourceId) tradeApi.source(sourceId).then(applySource, (e: unknown) => setLoadErr(errMsg(e)));
  }, [sourceId, applySource]);
  // a stored version: its params and universe seed the trial form (adjusted during render, no effect)
  const [seenV, setSeenV] = useState<StrategyVersion | undefined>(undefined);
  if (readonly && v && v !== seenV) {
    setSeenV(v);
    setSpecs(v.params || []);
    setParams(Object.fromEntries((v.params || []).map((p) => [p.name, p.default])));
    setUniverse(JSON.parse(JSON.stringify(v.defaultUniverse ?? { static: [] })) as Universe);
  }

  const setCode = (c: string) => {
    codeRef.current = c;
    setDraft(c);
    if (readonly || c === saved.current) return;
    setSaveState("dirty");
    clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => void save(), 1200);
    clearTimeout(checkTimer.current);
    checkTimer.current = setTimeout(() => void runCheck(), 1500);
  };
  // leaving the page or the app saves what's pending
  useEffect(() => {
    const sub = AppState.addEventListener("change", (st) => { if (st !== "active" && codeRef.current !== saved.current) void save(); });
    return () => {
      sub.remove();
      clearTimeout(saveTimer.current);
      clearTimeout(checkTimer.current);
      if (codeRef.current !== saved.current) void save();
    };
  }, [save]);

  const code = readonly ? v?.code ?? "" : draft;
  const diags = check?.diagnostics ?? [];
  const errors = diags.filter((d) => d.severity === "error").length;
  const pubEntry = useMemo(() => (lib.data ?? []).find((e) => sourceId && e.latest.meta?.sourceId === sourceId), [lib.data, sourceId]);
  // what the code declares: the stored version, else the last check / the published version of the draft
  const declared: StrategyVersion | undefined = readonly ? v : check?.strategies[0] ?? pubEntry?.latest;
  const paramIssue = specs.map((p) => paramError(p, params[p.name]) && `${p.label || p.name}：${paramError(p, params[p.name])}`).find(Boolean) || "";
  const backtestId = readonly ? versionId : published?.id || (source && !source.dirty ? source.published : "");
  const title = (readonly ? v?.name : declared?.name || source?.name) || "策略";
  const webPath = readonly ? `/trading/strategies/view/${versionId}` : `/trading/strategies/edit/${sourceId}`;
  const status = readonly
    ? { t: v?.meta?.builtin ? "内置 · 只读" : "已发布版本 · 只读", c: undefined }
    : saveState === "saving" ? { t: "保存中…", c: undefined }
    : saveState === "error" ? { t: "保存失败", c: "bad" as const }
    : saveState === "dirty" ? { t: "未保存", c: "warn" as const }
    : !source ? null
    : !source.published ? { t: "草稿 · 未发布", c: "warn" as const }
    : source.dirty ? { t: "有未发布的改动", c: "warn" as const } : { t: "已发布", c: "good" as const };

  const runTrial = async () => {
    if (running || paramIssue) return;
    setRunning(true);
    setRunErr("");
    setTab("result");
    setPickTrade(0);
    void haptic("medium");
    try {
      if (!readonly) await save();
      const r = await tradeApi.trial({ ...(readonly ? { versionId } : { sourceId: srcRef.current?.id }), params, universe, days: Number(days) });
      setTrial(r);
      if (r.check) setCheck(r.check);
      if (r.check?.diagnostics?.some((d) => d.severity === "error")) { setTab("info"); toast("代码有错误，没有运行", undefined, undefined, true); }
    } catch (e) { setRunErr(errMsg(e)); } finally { setRunning(false); }
  };
  const publish = async () => {
    const s = srcRef.current;
    if (!s) return;
    setPublishing(true);
    void haptic("medium");
    try {
      await save();
      const r = await tradeApi.publishSource(s.id);
      setCheck(r.check);
      setPublished(r.version ?? null);
      toast(`已发布 ${r.version?.id.slice(0, 8) ?? ""}`, "可以回测或添加到账户");
      invalidate("trade:strategyLib");
      await Promise.all([load(), lib.refresh()]);
    } catch (e) {
      toastError("发布失败", e);
      setTab("info");
      // show why: the server's check of the same code
      tradeApi.check(codeRef.current).then(setCheck).catch(() => {});
    } finally { setPublishing(false); }
  };
  const copyAsNew = async () => {
    if (!versionId) return;
    setCopying(true);
    try {
      const n = await tradeApi.createSource({ fromVersion: versionId });
      invalidate("trade:strategyLib");
      toast("已复制为新策略", n.name);
      router.replace({ pathname: "/trading/strategy", params: { source: String(n.id) } });
    } catch (e) { toastError("复制失败", e); } finally { setCopying(false); }
  };
  const refresh = async () => {
    if (readonly) await ver.refresh();
    else { await save(); await load(); }
    await lib.refresh();
  };

  const err = readonly ? ver.error : loadErr;
  const loading = readonly ? !v && !err : !source && !err;
  const tabs: { value: Tab; label: string }[] = [
    { value: "code", label: "代码" },
    { value: "info", label: readonly ? "说明" : diags.length ? `问题 ${diags.length}` : "问题" },
    { value: "run", label: "试运行" },
    { value: "result", label: trial ? `结果 ${trial.signals.length}` : "结果" },
  ];

  return (
    <Screen>
      <TopBar title={title} sub={declared ? `${declared.timeframe} · ${directionText(declared.meta)}` : undefined}
        right={<Pressable hitSlop={10} accessibilityLabel="在网页版打开" onPress={() => { tap(); void openPage(WEB_ORIGIN + webPath); }}><Ionicons name="open-outline" size={21} color={colors.textSoft} /></Pressable>} />
      {!editing ? (
        <View style={{ paddingHorizontal: 12, paddingTop: 10, gap: 8 }}>
          <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap", alignItems: "center" }}>
            {status ? <Tag text={status.t} tone={status.c} /> : null}
            {checking ? <Muted>检查中…</Muted> : null}
            <View style={{ flex: 1 }} />
            {readonly ? (
              <SmallButton label={copying ? "复制中…" : "复制为新策略"} icon="copy-outline" disabled={copying || !v} onPress={() => void copyAsNew()} />
            ) : (
              <>
                <SmallButton label="检查" icon="search" disabled={checking || !code.trim()} onPress={() => { setTab("info"); void runCheck(); }} />
                <SmallButton label={publishing ? "发布中…" : "发布"} icon="rocket-outline" tone="primary" disabled={publishing || errors > 0 || !source} onPress={() => void publish()} />
              </>
            )}
          </View>
          {saveErr && saveState === "error" ? <Banner text={`保存失败：${saveErr}`} /> : null}
          {published ? (
            <Banner tone="ok" text={`已发布 ${published.id.slice(0, 8)}，可以回测或添加到账户`} />
          ) : null}
        </View>
      ) : null}
      <View style={{ paddingHorizontal: 12, paddingVertical: 8 }}>
        <Segmented small value={tab} onChange={(t) => { if (t !== "code") setEditing(false); setJump(null); setTab(t); }} options={tabs} />
      </View>

      {err ? <View style={{ padding: 12 }}><ErrorBox text={err} onRetry={() => void refresh()} /></View> : null}
      {loading ? <Loading text="读取代码" /> : tab === "code" ? (
        readonly ? (
          <ScrollView contentContainerStyle={{ padding: 12, paddingTop: 0, gap: 10, paddingBottom: 48 }}>
            {v ? <Banner tone="warn" text={`${v.meta?.builtin ? "内置策略只读，作为示例和教材：" : "已发布的版本不可修改："}点「复制为新策略」得到一份可编辑的副本。代码里的中文注释讲解了每一步。`} /> : null}
            <CodeView code={code} />
          </ScrollView>
        ) : (
          <CodeEditor code={code} onChange={setCode} diagnostics={diags} jump={jump} onEditing={setEditing} />
        )
      ) : (
        <ScrollView contentContainerStyle={{ padding: 12, paddingTop: 0, gap: 12, paddingBottom: 48 }} keyboardShouldPersistTaps="handled" refreshControl={<Pull onRefresh={refresh} />}>
          {tab === "info" ? (
            <>
              {!readonly ? (
                <Card>
                  <PanelTitle title="问题" right={diags.length ? <Tag text={String(diags.length)} tone={errors ? "bad" : "warn"} /> : undefined} />
                  {checkErr ? <Text style={{ color: colors.down, fontSize: fs.sm }}>检查失败：{checkErr}</Text>
                    : checking && !check ? <Muted>检查中…</Muted>
                    : check && !diags.length ? <Text style={{ color: colors.up, fontSize: fs.sm }}>没有发现问题{declared ? `：策略 ${declared.name}，${declared.params.length} 个参数` : ""}</Text> : null}
                  {diags.map((d, i) => (
                    <Pressable key={i} onPress={() => { tap(); setTab("code"); setJump((j) => ({ line: d.line, n: (j?.n ?? 0) + 1 })); }}
                      style={({ pressed }) => ({ flexDirection: "row", gap: 6, padding: 8, marginBottom: 4, borderRadius: 6, backgroundColor: pressed ? colors.cardMuted : colors.raised })}>
                      <Ionicons name={d.severity === "error" ? "alert-circle" : "warning"} size={14} color={d.severity === "error" ? colors.down : colors.warn} style={{ marginTop: 2 }} />
                      <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 2 }}>第 {d.line} 行</Text>
                      <Text style={{ flex: 1, fontSize: fs.sm, color: colors.text }}>{d.message}</Text>
                    </Pressable>
                  ))}
                  <Muted style={{ marginTop: 4 }}>输入停顿后自动保存并检查：语法、未定义的名字、策略声明（参数范围、周期、品种）。点一条问题跳到那一行。发布 = 保存为不可变版本，供回测和实例使用。</Muted>
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
              <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
                {backtestId ? <SmallButton label="回测" icon="flask-outline" onPress={() => router.push({ pathname: "/trading/new", params: { mode: "backtest", strategy: backtestId } })} /> : null}
                <SmallButton label="策略文档" icon="book-outline" onPress={() => router.push("/help")} />
              </View>
            </>
          ) : tab === "run" ? (
            <>
              <Card style={{ gap: 12 }}>
                <View style={{ gap: 6 }}>
                  <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>回放天数</Text>
                  <Segmented small value={days} onChange={setDays} options={DAYS.map((d) => ({ value: d, label: `${d} 天` }))} />
                </View>
                <View style={{ gap: 6 }}>
                  <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>品种</Text>
                  {readonly ? <Text style={{ fontSize: fs.md, color: colors.textSoft }}>{universeText(universe, ruleName)}<Text style={{ color: colors.textFaint }}>（复制后可改）</Text></Text>
                    : <UniversePicker u={universe} onChange={setUniverse} />}
                </View>
              </Card>
              <Card>
                <PanelTitle title="参数" hint={specs.length ? `${specs.length} 个` : undefined} />
                <ParamsForm specs={specs} params={params} onChange={setParams} />
                {paramIssue ? <Text style={{ marginTop: 6, fontSize: fs.sm, color: colors.down }}>{paramIssue}</Text> : null}
              </Card>
              <PillButton icon="play" label={running ? "运行中…" : "试运行"} loading={running} disabled={running || !!paramIssue || !code.trim()} onPress={() => void runTrial()} />
              <Muted>试运行 = 在最近 {days} 天的历史数据上跑一次临时回测（初始资金 1 万 USDT、默认风控），最长 90 秒。不建账户、不发通知（ctx.notify 只记进日志），结果不保存。</Muted>
            </>
          ) : (
            <TrialPane trial={trial} running={running} runErr={runErr} resultTab={resultTab} setResultTab={setResultTab} pickTrade={pickTrade} setPickTrade={setPickTrade}
              onRun={() => setTab("run")} backtest={backtestId ? () => router.push({ pathname: "/trading/new", params: { mode: "backtest", strategy: backtestId } }) : undefined} />
          )}
        </ScrollView>
      )}
    </Screen>
  );
}

function TrialPane({ trial, running, runErr, resultTab, setResultTab, pickTrade, setPickTrade, onRun, backtest }: {
  trial: TrialResult | null; running: boolean; runErr: string; resultTab: "signals" | "log" | "trades"; setResultTab: (t: "signals" | "log" | "trades") => void;
  pickTrade: number; setPickTrade: (id: number) => void; onRun: () => void; backtest?: () => void;
}) {
  if (running) return <Loading text="试运行中（最长 90 秒）" />;
  if (runErr) return <ErrorBox text={runErr} />;
  if (!trial) return <Muted style={{ padding: 4 }}>还没有试运行。到「试运行」里设置参数后点试运行。<Text style={{ color: colors.primary }} onPress={onRun}>去设置</Text></Muted>;
  const sig = trial.signals;
  const rep = trial.report;
  const chartTrades = trial.trades.filter((t) => t.instId === trial.candles?.instId);
  return (
    <>
      {trial.error ? <Banner text={trial.error} /> : null}
      <KpiGrid items={[
        { label: "信号", value: String(sig.length), sub: `下单 ${sig.filter((s) => s.outcome === "ordered").length} · 拒绝 ${sig.filter((s) => s.outcome === "rejected").length}` },
        { label: "交易", value: String(trial.trades.length), sub: `已平 ${trial.trades.filter((t) => t.closeTs).length}` },
        ...(rep ? [
          { label: "净收益", value: amt(rep.net), sub: pctf(rep.netPct), tone: tone(rep.net) },
          { label: "胜率 / 回撤", value: rep.winRate == null ? "—" : pctf(rep.winRate, 0), sub: pctf(-rep.maxDrawdown) },
        ] : []),
      ]} />
      <Muted>{day(trial.from)} – {day(trial.to)} · 用时 {trial.seconds} 秒</Muted>
      {trial.candles?.rows.length ? (
        <View style={{ gap: 4 }}>
          <TrialChart candles={trial.candles} trades={chartTrades} selectedId={pickTrade} height={240} />
          <Muted style={{ fontSize: fs.xs }}>{trial.candles.instId.replace("-USDT-SWAP", "")} · {trial.candles.bar} · ▲ 开多 ▼ 开空 ● 平仓{chartTrades.length < trial.trades.length ? `；其他合约的 ${trial.trades.length - chartTrades.length} 笔交易见「交易」` : ""}</Muted>
        </View>
      ) : null}
      <Segmented small value={resultTab} onChange={setResultTab} options={[{ value: "signals", label: `信号 ${sig.length}` }, { value: "log", label: `日志 ${trial.logs.length}` }, { value: "trades", label: `交易 ${trial.trades.length}` }]} />
      {resultTab === "signals" ? <SignalList signals={sig} limit={30} />
        : resultTab === "log" ? <RunLog lines={trial.logs} empty="策略没有写日志（ctx.log / ctx.notify）" />
        : trial.trades.length ? (
          <View style={{ borderRadius: radius.md, borderWidth: 1, borderColor: colors.border, overflow: "hidden" }}>
            {trial.trades.map((t, i) => {
              const on = pickTrade === t.id;
              const onChart = t.instId === trial.candles?.instId;
              return (
                <Pressable key={t.id} disabled={!onChart} onPress={() => { tap(); setPickTrade(on ? 0 : t.id); }}
                  style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 9, paddingHorizontal: 10, borderTopWidth: i ? 1 : 0, borderColor: colors.border, backgroundColor: on ? colors.cardMuted : colors.card }}>
                  <CoinIcon instId={t.instId} size={16} />
                  <View style={{ flex: 1, gap: 2 }}>
                    <Text style={{ fontSize: fs.md, fontWeight: "600", color: colors.text }}>
                      {t.instId.replace("-USDT-SWAP", "")} <Text style={{ color: t.posSide === "long" ? colors.up : colors.down }}>{t.posSide === "long" ? "多" : "空"}</Text>
                    </Text>
                    <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(t.openTs, true)} → {t.closeTs ? time(t.closeTs, true) : "持仓中"}</Text>
                  </View>
                  <LTag l={exitKindLabel[t.exitKind]} fallback={t.exitKind} />
                  <Text style={[{ minWidth: 64, textAlign: "right", fontSize: fs.md, fontWeight: "600", color: toneText(tone(t.net)) }, tnum]}>{amt(t.net)}</Text>
                </Pressable>
              );
            })}
          </View>
        ) : <Muted>没有成交。看看「信号」里的拒绝原因，或「日志」里策略在等什么。</Muted>}
      {backtest ? <PillButton variant="soft" icon="flask-outline" label="用更长区间正式回测" onPress={backtest} /> : null}
    </>
  );
}

// 新建信号: the owner subscribes a condition himself (POST /api/v1/signals, server internal/signals/spec.go).
// When it fires, the server starts a new "信号 · …" AI chat and runs the agent with the intent as its
// instructions. One leaf per subscription: price break, zone, a fact-panel field, continuation risk
// (P_tail), a timed review, or a script (a ctg Condition class: starts from the server's template with
// the coin filled in, edited in a full-height CodeEditor sheet, 测试 replays it over recent history and
// shows the result; 订阅 tests first when the code changed since). Opened from 交易 → 信号 (+); `instId`
// prefills the coin (instrument page).
import { useEffect, useMemo, useState } from "react";
import { Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { api, type CatalogItem } from "@/lib/api";
import { price, sym } from "@/lib/fmt";
import { useLivePrice, useUniverse } from "@/lib/market";
import { useQuery } from "@/lib/query";
import { createSignal, sigApi, useSignals, type ScriptTest } from "@/lib/signals";
import { colors, fs, tnum } from "@/lib/theme";
import { CoinIcon } from "../CoinIcon";
import { Field, Input, NumInput } from "../Form";
import { Chip, PillButton, Segmented, Sheet, tap, toast, toastError } from "../ui";
import { problemText, ScriptTestView } from "../ai/ScriptSignal";
import { CodeEditor } from "./CodeEditor";
import { CodeView } from "./CodeView";
import { Banner, Muted, SmallButton } from "./common";
import { InstSearch, MAJORS } from "./forms";

type Kind = "price" | "zone" | "field" | "moverisk" | "time" | "script";
const KINDS: { value: Kind; label: string }[] = [
  { value: "price", label: "突破/跌破" },
  { value: "zone", label: "进入区间" },
  { value: "field", label: "字段" },
  { value: "moverisk", label: "延伸风险" },
  { value: "time", label: "定时复查" },
  { value: "script", label: "脚本" },
];
const EVERY: { value: "" | "tick" | "1m" | "5m" | "15m" | "1H"; label: string }[] = [
  { value: "", label: "默认" }, { value: "tick", label: "实时" }, { value: "1m", label: "1 分" }, { value: "5m", label: "5 分" }, { value: "15m", label: "15 分" }, { value: "1H", label: "1 时" },
];

/** The server's template with the coin put in (its example coin is AAVE). */
const personalize = (code: string, inst: string) => (inst ? code.replaceAll("AAVE-USDT-SWAP", inst).replace(/"""AAVE /, `"""${sym(inst)} `) : code);
const BARS = [{ value: "", label: "现价" }, ...["1m", "15m", "1H", "4H"].map((v) => ({ value: v, label: v }))];

// Fact-panel fields (features.Fields), entered in display units: rate → %/h, log → %, mult → ×.
type Unit = "rate" | "logpct" | "mult";
const FIELDS: { key: string; label: string; unit: Unit }[] = [
  { key: "funding_hourly", label: "资金费/小时", unit: "rate" },
  { key: "ret_1h", label: "1h 涨跌", unit: "logpct" },
  { key: "ret_4h", label: "4h 涨跌", unit: "logpct" },
  { key: "oi_chg_1h", label: "OI 变化 1h", unit: "logpct" },
  { key: "vol_surprise_4h", label: "放量 4h", unit: "mult" },
];
const UNIT_TEXT: Record<Unit, string> = { rate: "%/h", logpct: "%", mult: "×" };
const toRaw = (u: Unit, x: number) => (u === "rate" ? x / 100 : u === "logpct" ? Math.log1p(x / 100) : Math.log(x));
const fromRaw = (u: Unit, v: number) => (u === "rate" ? v * 100 : u === "logpct" ? Math.expm1(v) * 100 : Math.exp(v));
const OPS = [">", ">=", "<", "<="].map((v) => ({ value: v, label: v.replace(">=", "≥").replace("<=", "≤") }));

const pct = (a: number, b: number) => (Math.abs(a - b) / b) * 100;
/** "2026-11-02 10:00" in UTC+8, `min` minutes from now. */
const at8 = (min: number) => new Date(Date.now() + 8 * 3_600_000 + min * 60_000).toISOString().slice(0, 16).replace("T", " ");

const DEFAULT_INTENT: Record<Kind, (c: string, d: string) => string> = {
  price: (c, d) => `${d}后重新评估 ${c}：看量能、OI 和资金费确认突破是否有效，条件成立就提交交易提议（入场、止损、目标），否则说明放弃。`,
  zone: (c, d) => `${d}后重新评估 ${c} 的入场计划：区间内有承接/抛压就提交交易提议（入场、止损、目标），否则说明放弃。`,
  field: (c, d) => `${d}后重新评估 ${c}：结合价格结构和持仓变化判断方向，条件成立就提交交易提议，否则说明放弃。`,
  moverisk: (c, d) => `${d}后重新评估做空/抄底 ${c}：延伸风险降下来了，看结构是否转弱，条件成立就提交交易提议，否则说明放弃。`,
  time: (c) => `到点复查${c ? ` ${c}` : ""}：用最新数据重新评估之前的计划，条件成立就提交交易提议，否则说明为什么继续等或放弃。`,
  script: (_c, d) => `${d}成立后重新评估：用最新数据看结构、量能和持仓变化，条件成立就提交交易提议（入场、止损、目标），否则说明放弃。`,
};

export function NewSignalSheet({ visible, onClose, instId: initial }: { visible: boolean; onClose: () => void; instId?: string }) {
  const caps = useSignals((s) => s.caps);
  const maxHours = (caps?.maxDays ?? 7) * 24;
  const [inst, setInst] = useState(initial ?? "");
  const [kind, setKind] = useState<Kind>("price");
  // price
  const [op, setOp] = useState<"above" | "below">("above");
  const [level, setLevel] = useState(NaN);
  const [bar, setBar] = useState("");
  // zone
  const [lo, setLo] = useState(NaN);
  const [hi, setHi] = useState(NaN);
  // field
  const [field, setField] = useState(FIELDS[0].key);
  const [fop, setFop] = useState(">");
  const [fval, setFval] = useState(0);
  // moverisk
  const [side, setSide] = useState<"top" | "bottom">("top");
  const [rop, setRop] = useState("<");
  const [pTail, setPTail] = useState(30);
  // time
  const [tmode, setTmode] = useState<"after" | "at">("after");
  const [after, setAfter] = useState(60);
  const [at, setAt] = useState(() => at8(60));
  // common
  const [hours, setHours] = useState(24);
  const [maxFires, setMaxFires] = useState(1);
  // how often the server checks it ("" = its default: real time for last-price levels / zones / times, else each minute)
  const [every, setEvery] = useState<"" | "tick" | "1m" | "5m" | "15m" | "1H">("");
  const [intent, setIntent] = useState("");
  const [intentEdited, setIntentEdited] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  // script
  const [code, setCode] = useState("");
  const [editing, setEditing] = useState(false);
  const [test, setTest] = useState<{ code: string; r: ScriptTest } | null>(null);
  const [testing, setTesting] = useState(false);
  useEffect(() => {
    if (kind !== "script" || code || !visible) return;
    sigApi.scriptTemplate().then((t) => setCode((c) => c || personalize(t.code, inst))).catch((e) => setErr(`未能加载模板：${e instanceof Error ? e.message : String(e)}`));
  }, [kind, code, visible, inst]);
  const tested = test && test.code === code ? test.r : null;

  /** Compile + replay the current code; the result stays until the code changes. */
  const runTest = async (): Promise<ScriptTest | null> => {
    if (!code.trim()) { setErr("先写脚本"); return null; }
    setErr("");
    setTesting(true);
    try {
      const r = await sigApi.testScript(code);
      setTest({ code, r });
      return r;
    } catch (e) {
      toastError("测试失败", e);
      return null;
    } finally {
      setTesting(false);
    }
  };

  // follow a new prefilled coin (opened from another instrument page)
  const [seenInit, setSeenInit] = useState(initial);
  if (seenInit !== initial) { setSeenInit(initial); if (initial) setInst(initial); }

  // live price: the stream's, else the pool snapshot's or the catalog's last
  const universe = useUniverse().data;
  const row = universe?.rows?.find((r) => r.instId === inst);
  const cat = useQuery<{ items: CatalogItem[] }>(inst && !row ? "catalog" : null, () => api.catalog(), { staleMs: 10 * 60_000 });
  const px = useLivePrice(inst, row?.last || cat.data?.items.find((c) => c.instId === inst)?.last) || 0;
  const risk = useQuery(inst && kind === "moverisk" ? `exhaustion:${inst}:1H` : null, () => api.exhaustion(inst), { staleMs: 60_000 }).data?.risk;
  const fdef = FIELDS.find((f) => f.key === field)!;
  const fnow = row?.values?.[field];

  const c = inst ? sym(inst) : "";
  const desc = useMemo(() => {
    if (!c) return "";
    switch (kind) {
      case "price": return isFinite(level) && level > 0 ? (bar ? `${c} ${bar} 收盘${op === "above" ? "站上" : "跌破"} ${price(level)}` : `${c} 价格${op === "above" ? "上破" : "跌破"} ${price(level)}`) : "";
      case "zone": return isFinite(lo) && isFinite(hi) ? `${c} 进入 ${price(Math.min(lo, hi))}–${price(Math.max(lo, hi))}` : "";
      case "field": return isFinite(fval) ? `${c} ${fdef.label} ${OPS.find((o) => o.value === fop)?.label} ${+fval.toPrecision(6)}${UNIT_TEXT[fdef.unit]}` : "";
      case "moverisk": return isFinite(pTail) ? `${c} ${side === "top" ? "上涨端" : "下跌端"} P_tail ${rop === "<" ? "<" : ">"} ${pTail}%` : "";
      case "time": return tmode === "after" ? `${after} 分钟后` : `到 ${at.slice(5)}`;
      case "script": return "";
    }
  }, [c, kind, level, bar, op, lo, hi, fval, fdef, fop, side, rop, pTail, tmode, after, at]);
  const scriptDesc = tested?.meta ? `脚本 ${tested.meta.timeframe} · ${tested.meta.desc}` : "";
  const shownDesc = kind === "script" ? scriptDesc : desc;

  const defaultIntent = kind === "script" ? DEFAULT_INTENT.script(c, tested?.meta?.desc || "脚本条件")
    : c || kind === "time" ? DEFAULT_INTENT[kind](c, desc || "条件到了") : "";
  const intentText = intentEdited ? intent : defaultIntent;

  // distance from the live price, and a warning when the condition already holds (the server rejects it)
  const live = useMemo((): { text: string; holds?: boolean } | null => {
    if (!px) return null;
    const head = `现价 ${price(px)}`;
    if (kind === "price" && isFinite(level) && level > 0) {
      const holds = op === "above" ? px >= level : px <= level;
      return { text: `${head} · 距 ${price(level)} ${holds ? "已越过" : "还差"} ${pct(level, px).toFixed(2)}%`, holds: holds && !bar };
    }
    if (kind === "zone" && isFinite(lo) && isFinite(hi)) {
      const [a, b] = [Math.min(lo, hi), Math.max(lo, hi)];
      if (px >= a && px <= b) return { text: `${head} · 已在区间内`, holds: true };
      const t = px > b ? b : a;
      return { text: `${head} · 距 ${price(t)} 还差 ${pct(t, px).toFixed(2)}%` };
    }
    return { text: head };
  }, [px, kind, level, op, bar, lo, hi]);

  const spec = (): Record<string, unknown> | string => {
    if (kind === "script") return code.trim() ? { type: "script" } : "先写脚本";
    if (!inst && kind !== "time") return "先选一个合约";
    switch (kind) {
      case "price":
        if (!(level > 0)) return "填写价位";
        return { type: "price", instId: inst, op, level, ...(bar ? { bar } : {}) };
      case "zone":
        if (!(lo > 0 && hi > 0)) return "填写区间上下沿";
        return { type: "zone", instId: inst, lo: Math.min(lo, hi), hi: Math.max(lo, hi) };
      case "field": {
        const v = toRaw(fdef.unit, fval);
        if (!isFinite(v)) return "填写有效的阈值";
        return { type: "field", instId: inst, field, op: fop, value: v };
      }
      case "moverisk":
        if (!(pTail > 0 && pTail < 100)) return "P_tail 阈值在 0–100% 之间";
        return { type: "moverisk", instId: inst, side, metric: "pTail", op: rop, value: pTail / 100 };
      case "time":
        if (tmode === "after") return after >= 1 ? { type: "time", afterMin: Math.round(after), ...(inst ? { instId: inst } : {}) } : "填写分钟数";
        return /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/.test(at.trim()) ? { type: "time", at: at.trim(), ...(inst ? { instId: inst } : {}) } : "时间格式：2026-11-02 10:00";
    }
  };

  const hoursErr = !(hours >= 0.25 && hours <= maxHours) ? `0.25–${maxHours} 小时` : "";
  const firesErr = !(Number.isInteger(maxFires) && maxFires >= 1 && maxFires <= 5) ? "1–5 次" : "";

  const submit = async () => {
    const s = spec();
    if (typeof s === "string") { setErr(s); return; }
    if (hoursErr || firesErr) { setErr("检查有效期和触发次数"); return; }
    if (intentText.trim().length < 6) { setErr("请写清触发后让 AI 做什么（至少 6 个字）"); return; }
    if (kind === "script") { // the server runs the same check; testing first gives the reason in Chinese
      const r = tested ?? (await runTest());
      if (!r) return;
      if (!r.ok) { setErr(problemText(r)); return; }
    }
    setErr("");
    setBusy(true);
    try {
      const r = await createSignal({ spec: s, intent: intentText.trim(), expiresHours: hours, maxFires, ...(every ? { every } : {}), ...(kind === "script" ? { code } : {}) });
      toast("信号已订阅", r.desc);
      reset();
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const reset = () => {
    setLevel(NaN); setLo(NaN); setHi(NaN); setIntent(""); setIntentEdited(false); setErr(""); setCode(""); setTest(null);
    if (!initial) setInst("");
  };

  const lbl = (t: string) => <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginBottom: 4 }}>{t}</Text>;
  const nudge = (p: number) => <Chip key={p} label={`${p > 0 ? "+" : ""}${p}%`} onPress={() => { const v = +(px * (1 + p / 100)).toPrecision(6); setLevel(v); setOp(p > 0 ? "above" : "below"); }} />;

  return (
    <Sheet
      visible={visible}
      onClose={onClose}
      title="新建信号"
      footer={(
        <View style={{ gap: 8 }}>
          {err ? <Banner text={err} /> : null}
          <PillButton label="订阅" icon="flash-outline" loading={busy} onPress={submit} />
        </View>
      )}
    >
      {kind === "script" ? null : <Field label="合约" style={{ borderTopWidth: 0 }}>
        {inst ? (
          <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
            <CoinIcon instId={inst} size={20} />
            <Text style={{ fontSize: fs.xl, fontWeight: "700", color: colors.text }}>{c}</Text>
            <Text style={[{ flex: 1, fontSize: fs.md, color: colors.textSoft }, tnum]}>{px ? price(px) : ""}</Text>
            <Pressable hitSlop={8} onPress={() => { tap(); setInst(""); }} style={{ flexDirection: "row", alignItems: "center", gap: 3 }}>
              <Ionicons name="swap-horizontal" size={15} color={colors.textFaint} />
              <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>更换</Text>
            </Pressable>
          </View>
        ) : (
          <InstSearch onPick={setInst} icon="checkmark" empty={(
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
              <Muted>常用</Muted>
              {MAJORS.map((id) => <Chip key={id} label={sym(id)} onPress={() => setInst(id)} />)}
            </View>
          )} />
        )}
      </Field>}

      <Field label="条件" style={kind === "script" ? { borderTopWidth: 0 } : undefined}>
        <Segmented small value={kind} onChange={(k) => { setKind(k); setErr(""); }} options={KINDS} />
        {kind === "price" ? (
          <View style={{ gap: 10, marginTop: 4 }}>
            <View style={{ flexDirection: "row", alignItems: "flex-end", gap: 10 }}>
              <View>{lbl("方向")}<Segmented small value={op} onChange={setOp} options={[{ value: "above", label: "上破", tone: "up" }, { value: "below", label: "跌破", tone: "down" }]} /></View>
              <View style={{ flex: 1 }}>{lbl("价位")}<NumInput value={level} onChange={setLevel} width={140} placeholder={px ? price(px) : "价格"} /></View>
            </View>
            {px ? <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>{[-3, -1, 1, 3].map(nudge)}</View> : null}
            <View>{lbl("确认方式")}<Segmented small value={bar} onChange={setBar} options={BARS} /></View>
            {bar ? <Muted>等 {bar} K 线收盘价越过才触发，过滤插针。</Muted> : null}
          </View>
        ) : kind === "zone" ? (
          <View style={{ flexDirection: "row", gap: 10, marginTop: 4 }}>
            <View style={{ flex: 1 }}>{lbl("下沿")}<NumInput value={lo} onChange={setLo} width={140} placeholder="价格" /></View>
            <View style={{ flex: 1 }}>{lbl("上沿")}<NumInput value={hi} onChange={setHi} width={140} placeholder="价格" /></View>
          </View>
        ) : kind === "field" ? (
          <View style={{ gap: 10, marginTop: 4 }}>
            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
              {FIELDS.map((f) => <Chip key={f.key} label={f.label} active={field === f.key} onPress={() => { setField(f.key); setFval(f.unit === "mult" ? 2 : 0); }} />)}
            </View>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
              <Segmented small value={fop} onChange={setFop} options={OPS} />
              <NumInput value={fval} onChange={setFval} unit={UNIT_TEXT[fdef.unit]} />
            </View>
            {fnow != null ? <Muted>当前 {+fromRaw(fdef.unit, fnow).toPrecision(4)}{UNIT_TEXT[fdef.unit]}</Muted> : null}
          </View>
        ) : kind === "moverisk" ? (
          <View style={{ gap: 10, marginTop: 4 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
              <Segmented small value={side} onChange={setSide} options={[{ value: "top", label: "上涨端" }, { value: "bottom", label: "下跌端" }]} />
              <Text style={{ fontSize: fs.md, color: colors.textSoft }}>P_tail</Text>
              <Segmented small value={rop} onChange={setRop} options={[{ value: "<", label: "低于" }, { value: ">", label: "高于" }]} />
              <NumInput value={pTail} onChange={setPTail} unit="%" width={90} />
            </View>
            <Muted>按 1H 收盘计算的延续风险（上涨端：做空会被继续拉的概率）。{risk ? `当前 ${Math.round(risk[side].pTail * 100)}%` : ""}</Muted>
          </View>
        ) : kind === "script" ? (
          <View style={{ gap: 10, marginTop: 4 }}>
            <Muted>用策略 SDK 写一个 Condition 类：在它的 K 线收盘时运行，check 从不成立变为成立时触发。合约、周期都在代码里声明。详见 帮助 → 策略 → 脚本信号。</Muted>
            {code ? (
              <View style={{ maxHeight: 260, overflow: "hidden", borderRadius: 8 }}>
                <CodeView code={code} diagnostics={tested?.diagnostics} onLinePress={() => { tap(); setEditing(true); }} />
              </View>
            ) : <Muted>正在加载模板…</Muted>}
            <View style={{ flexDirection: "row", gap: 8 }}>
              <SmallButton label="编辑代码" icon="create-outline" onPress={() => setEditing(true)} disabled={!code} />
              <SmallButton label={testing ? "测试中…" : tested ? "重新测试" : "测试"} icon="play-outline" onPress={() => void runTest()} disabled={!code || testing} />
            </View>
            {tested ? <ScriptTestView t={tested} /> : test ? <Muted>代码改过了，测试结果已过期。</Muted> : <Muted>测试会把脚本在最近 7 天的每根 K 线收盘上跑一遍：会触发几次、何时、数值，以及有没有出错、快不快、现在是否已成立。</Muted>}
          </View>
        ) : (
          <View style={{ gap: 10, marginTop: 4 }}>
            <Segmented small value={tmode} onChange={setTmode} options={[{ value: "after", label: "N 分钟后" }, { value: "at", label: "指定时间" }]} style={{ alignSelf: "flex-start" }} />
            {tmode === "after" ? (
              <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
                <NumInput value={after} onChange={setAfter} unit="分钟" decimal={false} />
                {[30, 60, 240].map((m) => <Chip key={m} label={m < 60 ? `${m} 分` : `${m / 60} 小时`} active={after === m} onPress={() => setAfter(m)} />)}
              </View>
            ) : (
              <Input value={at} onChangeText={setAt} placeholder="2026-11-02 10:00" keyboardType="numbers-and-punctuation" />
            )}
            <Muted>{tmode === "at" ? "UTC+8。" : ""}不需要合约也可以；选了合约会作为复查对象。</Muted>
          </View>
        )}
        {live && kind !== "time" && kind !== "script" ? (
          <Text style={[{ fontSize: fs.sm, color: live.holds ? colors.warn : colors.textSoft, marginTop: 2 }, tnum]}>
            {live.text}{live.holds ? " · 条件已经成立，服务器会拒绝" : ""}
          </Text>
        ) : null}
        {shownDesc ? <Text style={{ fontSize: fs.md, color: colors.text, marginTop: 2 }}>条件：{shownDesc}</Text> : null}
      </Field>

      <Field label="触发后让 AI 做什么" desc="触发时会新开一个「信号 · …」对话，AI 按这段说明用最新数据重新评估。">
        <Input
          value={intentText}
          onChangeText={(t) => { setIntent(t); setIntentEdited(true); }}
          multiline
          maxLength={1500}
          placeholder="例如：到了之后重新评估做空计划：入场、止损、目标，条件成立就提交交易提议"
          style={{ height: 96, paddingTop: 8, textAlignVertical: "top" }}
        />
        {intentEdited ? <Pressable onPress={() => { tap(); setIntentEdited(false); setIntent(""); }}><Text style={{ fontSize: fs.sm, color: colors.textFaint }}>恢复默认</Text></Pressable> : null}
      </Field>

      <View style={{ flexDirection: "row", gap: 16, paddingVertical: 10, borderTopWidth: 1, borderColor: colors.border }}>
        <View>{lbl(`有效期（最长 ${maxHours} 小时）`)}<NumInput value={hours} onChange={setHours} unit="小时" invalid={!!hoursErr} /></View>
        <View>{lbl("最多触发")}<NumInput value={maxFires} onChange={setMaxFires} unit="次" decimal={false} width={90} invalid={!!firesErr} /></View>
      </View>
      <View style={{ gap: 4, paddingBottom: 8 }}>
        {lbl("检查间隔")}
        <Segmented small value={kind === "script" && every === "tick" ? "" : every} onChange={setEvery} options={kind === "script" ? EVERY.filter((o) => o.value !== "tick") : EVERY} />
        <Muted>{kind === "script" ? "脚本在它的 K 线收盘时运行；这里设的是最短间隔（比如 5m 周期选 15 分，就只在每个 15 分钟整点运行）。" : "越慢越省资源；不需要第一时间响应的条件选 5 分或更慢，两次检查之间的短暂插针可能错过。"}</Muted>
      </View>
      {caps ? <Muted>活跃 {caps.active}/{caps.maxActive} · 今天已唤醒 AI {caps.wakesToday}/{caps.wakesPerDay} 次{caps.scripts ? ` · 脚本 ${caps.scripts.activeScripts}/${caps.scripts.maxActive}` : ""}</Muted> : null}
      <ScriptEditorSheet visible={editing} code={code} onChange={setCode} diagnostics={tested?.diagnostics ?? []} testing={testing} result={tested}
        onTest={() => void runTest()} onClose={() => setEditing(false)} />
    </Sheet>
  );
}

/** Full-height code editor for the script, with 测试 and the verdict in one line. */
function ScriptEditorSheet({ visible, code, onChange, diagnostics, testing, result, onTest, onClose }: {
  visible: boolean; code: string; onChange: (c: string) => void; diagnostics: ScriptTest["diagnostics"]; testing: boolean; result: ScriptTest | null; onTest: () => void; onClose: () => void;
}) {
  return (
    <Sheet visible={visible} onClose={onClose} title="编辑脚本" fill footer={(
      <View style={{ gap: 8 }}>
        {result ? (
          <Text numberOfLines={3} style={{ fontSize: fs.sm, lineHeight: 18, color: result.ok ? colors.textSoft : colors.warn }}>
            {result.ok ? `通过：过去 ${result.replay ? +result.replay.days.toFixed(1) : 0} 天会触发 ${result.replay?.fireCount ?? 0} 次` : problemText(result)}
          </Text>
        ) : null}
        <View style={{ flexDirection: "row", gap: 10 }}>
          <View style={{ flex: 1 }}><PillButton variant="soft" icon="play-outline" label={testing ? "测试中…" : "测试"} loading={testing} onPress={onTest} /></View>
          <View style={{ flex: 1 }}><PillButton label="完成" onPress={onClose} /></View>
        </View>
      </View>
    )}>
      <View style={{ flex: 1, marginHorizontal: -16 }}>
        <CodeEditor code={code} onChange={onChange} diagnostics={diagnostics} />
      </View>
    </Sheet>
  );
}

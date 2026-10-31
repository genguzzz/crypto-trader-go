// Forms of the trading pages (web LimitsForm, RiskSlider, ParamsForm, UniversePicker, InstanceForm).
// Values keep the web's shapes: percents stored as fractions, params as numbers (bool 0/1, choice index).
import { useMemo, useState, type ReactNode } from "react";
import { Pressable, Text, TextInput, View, type LayoutChangeEvent } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { api, type CatalogItem } from "@/lib/api";
import { useMeta, useRuleId } from "@/lib/market";
import { useQuery } from "@/lib/query";
import type { ExecMode, Limits, ParamSpec, StrategyVersion, Universe } from "@/lib/tradeApi";
import { execLabel, limitErrors, paramError, paramText, screenerMaxTop } from "@/lib/tradeMeta";
import { colors, fs, radius, tnum } from "@/lib/theme";
import { CoinIcon } from "../CoinIcon";
import { Chip, PickerSheet, Segmented, Tag, tap } from "../ui";
import { FieldRow, GroupTitle, InfoTip, Muted, NumInput, Toggle, mono } from "./common";

type Upd<T> = (v: T) => void;

export function LimitsForm({ l, onChange, base }: { l: Limits; onChange: Upd<Limits>; base?: Limits }) {
  const e = limitErrors(l);
  const ch = (k: keyof Limits) => !!base && base[k] !== l[k];
  const set = <K extends keyof Limits>(k: K) => (v: Limits[K]) => onChange({ ...l, [k]: v });
  return (
    <View>
      <GroupTitle title="仓位与风险" right={<InfoTip title="仓位与风险" items={[
        ["账户硬规则", "：策略和 AI 都不能绕过。每个信号先过这些规则，再按风险计算仓位。"],
        ["单笔风险", "：信号强度为 1 时，止损被打到亏掉的权益比例，可设 1%–500%（滑杆为对数刻度）。超过 100% 意味着一次止损可能亏掉超过全部权益；实际仓位仍受杠杆和单合约上限约束。仓位 = 权益 × 单笔风险 × 强度 ÷ (|入场 − 止损| × 合约面值)，再受杠杆、单合约上限约束，按最小下单量向下取整。"],
        ["单合约上限", "：一个合约占用的保证金不超过权益的这个比例（逐仓）。"],
        ["日亏损熔断", "：当天（UTC+8）已实现 + 未实现亏损达到权益的这个比例后，当天不再接受开仓信号，次日自动恢复；平仓、止损不受影响。"],
        ["冷却", "：某合约平仓后多少分钟内不再开同一合约。"],
      ]} />} />
      <FieldRow label="最大持仓数" desc="同时持有的合约个数" changed={ch("maxPositions")} error={e.maxPositions}>
        <NumInput value={l.maxPositions} onChange={set("maxPositions")} unit="个" integer />
      </FieldRow>
      <FieldRow label="最大杠杆" desc="逐仓杠杆上限，信号请求更高时按此截断" changed={ch("maxLeverage")} error={e.maxLeverage} stack>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          <Segmented small value={String(l.maxLeverage)} onChange={(v) => set("maxLeverage")(Number(v))} options={[2, 3, 5, 10].map((v) => ({ value: String(v), label: v + "x" }))} />
          <NumInput value={l.maxLeverage} onChange={set("maxLeverage")} unit="x" width={72} />
        </View>
      </FieldRow>
      <FieldRow label="单笔风险" changed={ch("riskPct")} error={e.riskPct} stack
        desc={<Text style={{ fontSize: fs.xs, color: colors.textFaint, lineHeight: 16 }}>止损触发时亏掉的权益比例（强度 1），1%–500%{l.riskPct > 1 ? <Text style={{ color: colors.warn }}> · 超过 100%：一次止损亏掉的超过全部权益</Text> : null}</Text>}>
        <RiskSlider value={l.riskPct} onChange={set("riskPct")} />
      </FieldRow>
      <FieldRow label="单合约上限" desc="一个合约的保证金占权益比例" changed={ch("maxInstPct")} error={e.maxInstPct}>
        <NumInput value={l.maxInstPct} onChange={set("maxInstPct")} unit="%" scale={100} />
      </FieldRow>
      <FieldRow label="日亏损熔断" desc="当日亏损达到权益比例后停止开仓，次日恢复；0 = 不熔断" changed={ch("dailyLossPct")} error={e.dailyLossPct}>
        <NumInput value={l.dailyLossPct} onChange={set("dailyLossPct")} unit="%" scale={100} />
      </FieldRow>
      <FieldRow label="冷却时间" desc="平仓后多久才能再开同一合约" changed={ch("cooldownMin")} error={e.cooldownMin}>
        <NumInput value={l.cooldownMin} onChange={set("cooldownMin")} unit="分钟" integer width={96} />
      </FieldRow>
      <FieldRow label="必须带止损" desc="开仓信号没有止损价时直接拒绝" changed={ch("requireStop")}>
        <Toggle value={l.requireStop} onChange={set("requireStop")} />
      </FieldRow>

      <GroupTitle title="成本" right={<InfoTip title="成本" items={[
        ["滑点容忍", "：市价入场的最差成交价 = 中间价 ×（1 ± bp/10000），盘口吃不到的部分取消。止损、止盈触发后按市价成交，不受此限制。"],
        ["手续费", "：按成交额收取，默认 OKX 普通用户档 taker 5 bp / maker 2 bp。资金费按真实结算记录收取。"],
      ]} />} />
      <FieldRow label="滑点容忍" desc="市价入场允许偏离中间价的幅度" changed={ch("slippageBp")} error={e.slippageBp}>
        <NumInput value={l.slippageBp} onChange={set("slippageBp")} unit="bp" />
      </FieldRow>
      <FieldRow label="手续费" desc="taker / maker" changed={ch("takerFeeBp") || ch("makerFeeBp")} error={e.takerFeeBp || e.makerFeeBp}>
        <View style={{ flexDirection: "row", gap: 6 }}>
          <NumInput value={l.takerFeeBp} onChange={set("takerFeeBp")} unit="bp" width={70} />
          <NumInput value={l.makerFeeBp} onChange={set("makerFeeBp")} unit="bp" width={70} />
        </View>
      </FieldRow>

      <GroupTitle title="人工确认" right={<InfoTip title="人工确认" items={[
        "需要确认的信号会通过交互通道（Telegram 与本页“待确认”）推送给你，选择执行、半仓或跳过。",
        ["超时", "：在设定时间内没有回应，按“超时默认”处理。信号自身也有有效期（TTL），超过即作废。"],
        "回测账户不会等待人工确认。",
      ]} />} />
      <FieldRow label="需要确认" desc="哪些信号在下单前要你确认" changed={ch("confirm")} stack>
        <Segmented small value={l.confirm} onChange={set("confirm")} options={[{ value: "off", label: "关闭" }, { value: "open", label: "仅开仓" }, { value: "all", label: "全部" }]} />
      </FieldRow>
      {l.confirm !== "off" ? (
        <>
          <FieldRow label="确认超时" changed={ch("confirmTimeoutS")} error={e.confirmTimeoutS}>
            <NumInput value={l.confirmTimeoutS} onChange={set("confirmTimeoutS")} unit="秒" integer />
          </FieldRow>
          <FieldRow label="超时默认" desc="无人回应时的处理" changed={ch("confirmDefault")}>
            <Segmented small value={l.confirmDefault} onChange={set("confirmDefault")} options={[{ value: "skip", label: "跳过" }, { value: "execute", label: "执行" }]} />
          </FieldRow>
        </>
      ) : null}
    </View>
  );
}

// Risk per trade, 1%–500% on a log scale (web RiskSlider): drag the track or type the number.
const RMIN = 0.01, RMAX = 5;
const toPos = (v: number) => Math.log(Math.min(RMAX, Math.max(RMIN, v)) / RMIN) / Math.log(RMAX / RMIN);
function fromPos(p: number) {
  const v = RMIN * Math.pow(RMAX / RMIN, Math.min(1, Math.max(0, p)));
  const pv = v * 100, mag = Math.pow(10, Math.floor(Math.log10(pv)) - 1);
  return +Math.min(RMAX, Math.max(RMIN, (Math.round(pv / mag) * mag) / 100)).toPrecision(6);
}
export function RiskSlider({ value, onChange }: { value: number; onChange: Upd<number> }) {
  const [w, setW] = useState(0);
  const over = value > 1;
  const c = over ? colors.warn : colors.primary;
  const pos = isFinite(value) ? toPos(value) : 0;
  const move = (x: number) => { if (w) onChange(fromPos((x - 8) / (w - 16))); };
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 12, alignSelf: "stretch" }}>
      <View style={{ flex: 1 }}>
        <View
          onLayout={(e: LayoutChangeEvent) => setW(e.nativeEvent.layout.width)}
          style={{ height: 30, justifyContent: "center" }}
          onStartShouldSetResponder={() => true}
          onMoveShouldSetResponder={() => true}
          onResponderTerminationRequest={() => false}
          onResponderGrant={(e) => { tap(); move(e.nativeEvent.locationX); }}
          onResponderMove={(e) => move(e.nativeEvent.locationX)}
        >
          <View style={{ marginHorizontal: 8, height: 4, borderRadius: 2, backgroundColor: colors.cardMuted }}>
            <View style={{ width: `${pos * 100}%`, height: 4, borderRadius: 2, backgroundColor: c }} />
          </View>
          <View pointerEvents="none" style={{ position: "absolute", left: pos * Math.max(0, w - 16), width: 18, height: 18, borderRadius: 9, backgroundColor: colors.text, borderWidth: 3, borderColor: c }} />
        </View>
        <View style={{ height: 14, marginHorizontal: 8 }}>
          {[0.01, 0.05, 0.2, 1, 5].map((v) => (
            <Pressable key={v} hitSlop={6} onPress={() => { tap(); onChange(v); }} style={{ position: "absolute", left: `${toPos(v) * 100}%`, transform: [{ translateX: -12 }] }}>
              <Text style={{ fontSize: 10, color: colors.textFaint, width: 30 }}>{v * 100}%</Text>
            </Pressable>
          ))}
        </View>
      </View>
      <NumInput value={value} onChange={onChange} unit="%" scale={100} width={78} />
    </View>
  );
}

/** Strategy parameters from the ParamSpec list, in the strategy's groups (web ParamsForm). */
export function ParamsForm({ specs, params, onChange, readonly }: { specs: ParamSpec[]; params: Record<string, number>; onChange?: Upd<Record<string, number>>; readonly?: boolean }) {
  const [pick, setPick] = useState<ParamSpec | null>(null);
  const groups = useMemo(() => {
    const m = new Map<string, ParamSpec[]>();
    for (const p of specs) { const g = p.group || ""; if (!m.has(g)) m.set(g, []); m.get(g)!.push(p); }
    return [...m.entries()];
  }, [specs]);
  const set = (k: string, v: number) => onChange?.({ ...params, [k]: v });
  const range = (p: ParamSpec) => ((p.min != null || p.max != null) && p.type !== "bool" && p.type !== "choice" && p.type !== "timeframe" ? `，范围 ${p.min ?? "−∞"}–${p.max ?? "∞"}` : "");
  const opts = (p: ParamSpec) => (p.options ?? []).map((o, i) => ({ value: String(i), label: p.labels?.[i] ?? o }));
  if (!specs.length) return <Muted>此策略没有可调参数</Muted>;
  return (
    <View>
      {groups.map(([g, ps]) => (
        <View key={g}>
          {g && groups.length > 1 ? <Text style={{ marginTop: 10, fontSize: fs.xs, fontWeight: "600", color: colors.textFaint }}>{g}</Text> : null}
          {ps.map((p) => {
            const err = paramError(p, params[p.name]);
            const v = params[p.name];
            const choice = p.type === "choice" || p.type === "timeframe";
            return (
              <View key={p.name} style={{ paddingVertical: 9, borderBottomWidth: 1, borderColor: colors.border, gap: 6 }}>
                <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
                  <Text style={{ fontSize: fs.md, fontWeight: "600", color: colors.text }}>{p.label || p.name}</Text>
                  {p.label ? <Text style={[mono, { color: colors.textFaint }]}>{p.name}</Text> : null}
                  {p.aiTunable ? <Tag text="AI 可调" tone="info" /> : null}
                </View>
                <Text style={{ fontSize: fs.xs, color: colors.textSoft }}>{p.desc || ""}<Text style={{ color: colors.textFaint }}>{p.desc ? " · " : ""}默认 {paramText(p, p.default)}{range(p)}</Text></Text>
                <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                  {readonly ? (
                    <Text style={[{ fontSize: fs.md, color: colors.text }, tnum]}>{paramText(p, v)}</Text>
                  ) : p.type === "bool" ? (
                    <Toggle value={!!v} onChange={(on) => set(p.name, on ? 1 : 0)} />
                  ) : choice && (p.options?.length ?? 0) <= 4 ? (
                    <Segmented small value={String(v)} onChange={(x) => set(p.name, Number(x))} options={opts(p)} />
                  ) : choice ? (
                    <Pressable onPress={() => { tap(); setPick(p); }} style={{ flexDirection: "row", alignItems: "center", gap: 6, height: 32, paddingHorizontal: 10, borderRadius: radius.sm, borderWidth: 1, borderColor: colors.borderStrong }}>
                      <Text style={{ color: colors.text, fontSize: fs.md }}>{paramText(p, v)}</Text>
                      <Ionicons name="chevron-down" size={14} color={colors.textFaint} />
                    </Pressable>
                  ) : (
                    <NumInput value={v} onChange={(x) => set(p.name, x)} width={100} integer={p.type === "int"} />
                  )}
                  {!readonly && v !== p.default ? (
                    <Pressable hitSlop={8} accessibilityLabel={`恢复 ${p.label || p.name} 默认值`} onPress={() => { tap(); set(p.name, p.default); }}>
                      <Ionicons name="refresh" size={15} color={colors.textFaint} />
                    </Pressable>
                  ) : null}
                </View>
                {err ? <Text style={{ fontSize: fs.xs, color: colors.down }}>{err}</Text> : null}
              </View>
            );
          })}
        </View>
      ))}
      {pick ? (
        <PickerSheet visible onClose={() => setPick(null)} title={pick.label || pick.name} value={String(params[pick.name])} options={opts(pick)} onPick={(x) => set(pick.name, Number(x))} />
      ) : null}
    </View>
  );
}

const MAJORS = ["BTC-USDT-SWAP", "ETH-USDT-SWAP", "SOL-USDT-SWAP"];
const symOf = (id: string) => id.replace("-USDT-SWAP", "");

/** Instrument universe: a fixed list (catalog search) or a screener query (web UniversePicker). */
export function UniversePicker({ u, onChange }: { u: Universe; onChange: Upd<Universe> }) {
  const meta = useMeta().data;
  const ruleId = useRuleId(meta);
  const [q, setQ] = useState("");
  const [rulePick, setRulePick] = useState(false);
  const cat = useQuery<{ items: CatalogItem[] }>(q ? "catalog" : null, () => api.catalog(), { staleMs: 10 * 60_000 });
  const kind = u.screener ? "screener" : "static";
  const setKind = (k: string) => {
    if (k === "screener") onChange({ screener: u.screener ?? { rule: ruleId || undefined, side: "long", top: 20, refresh: "1H" } });
    else onChange({ static: u.static ?? [] });
  };
  const picked = useMemo(() => u.static ?? [], [u.static]);
  const results = useMemo(() => {
    const s = q.trim().toLowerCase();
    if (!s) return [];
    const have = new Set(picked);
    return (cat.data?.items ?? [])
      .filter((c) => !have.has(c.instId) && c.instId.toLowerCase().includes(s))
      .sort((a, b) => Number(!a.instId.toLowerCase().startsWith(s)) - Number(!b.instId.toLowerCase().startsWith(s)) || (a.rank ?? 999) - (b.rank ?? 999))
      .slice(0, 8);
  }, [q, cat.data, picked]);
  const add = (id: string) => { onChange({ static: [...picked, id] }); setQ(""); };
  const remove = (id: string) => onChange({ static: picked.filter((x) => x !== id) });
  const sc = u.screener;
  const setSc = (p: Partial<NonNullable<Universe["screener"]>>) => {
    const next = { ...sc!, ...p };
    for (const k of Object.keys(p) as (keyof typeof p)[]) if (p[k] === undefined) delete next[k];
    onChange({ screener: next });
  };
  const scope = sc?.scope === "market" ? "market" : "pool";
  const setScope = (v: string) => {
    if (!sc) return;
    if (v === "market") setSc({ scope: "market", rule: undefined, minScore: undefined, side: undefined });
    else setSc({ scope: undefined, rule: ruleId || undefined, side: "long", top: Math.min(sc.top, 50) });
  };
  const ruleName = meta?.rules.find((r) => r.id === sc?.rule)?.name ?? sc?.rule ?? "默认规则";
  const lbl = (t: string) => <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginBottom: 4 }}>{t}</Text>;

  return (
    <View style={{ gap: 10 }}>
      <Segmented small value={kind} onChange={setKind} options={[{ value: "static", label: "固定列表" }, { value: "screener", label: "动态筛选" }]} style={{ alignSelf: "flex-start" }} />
      {kind === "static" ? (
        <>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, minHeight: 26, alignItems: "center" }}>
            {picked.map((id) => (
              <Pressable key={id} onPress={() => { tap(); remove(id); }} accessibilityLabel={`移除 ${id}`} style={{ flexDirection: "row", alignItems: "center", gap: 5, height: 28, paddingLeft: 8, paddingRight: 6, borderRadius: 14, backgroundColor: colors.cardMuted }}>
                <CoinIcon instId={id} size={14} />
                <Text style={{ fontSize: fs.sm, fontWeight: "600", color: colors.text }}>{symOf(id)}</Text>
                <Ionicons name="close" size={13} color={colors.textFaint} />
              </Pressable>
            ))}
            {!picked.length ? <Muted>还没有选择合约</Muted> : null}
          </View>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6, height: 36, paddingHorizontal: 10, borderRadius: radius.sm, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: colors.cardMuted }}>
            <Ionicons name="search" size={15} color={colors.textFaint} />
            <TextInput value={q} onChangeText={setQ} placeholder="搜索合约，如 BTC" placeholderTextColor={colors.textDim} autoCapitalize="characters" autoCorrect={false} style={{ flex: 1, color: colors.text, fontSize: fs.lg, padding: 0 }} />
          </View>
          {q ? (
            <View style={{ borderRadius: 8, borderWidth: 1, borderColor: colors.border, padding: 4, gap: 2 }}>
              {results.map((c) => (
                <Pressable key={c.instId} onPress={() => { tap(); add(c.instId); }} style={({ pressed }) => ({ flexDirection: "row", alignItems: "center", gap: 8, padding: 8, borderRadius: 6, backgroundColor: pressed ? colors.raised : "transparent" })}>
                  <CoinIcon instId={c.instId} size={16} />
                  <Text style={{ fontWeight: "600", color: colors.text, fontSize: fs.md }}>{symOf(c.instId)}</Text>
                  <Text style={{ flex: 1, fontSize: fs.xs, color: colors.textFaint }} numberOfLines={1}>{c.instId}</Text>
                  {c.rank ? <Tag text={`池内 #${c.rank}`} /> : null}
                  <Ionicons name="add" size={16} color={colors.textFaint} />
                </Pressable>
              ))}
              {!results.length ? <Muted style={{ padding: 6 }}>{cat.data ? "没有匹配的合约" : "加载合约列表…"}</Muted> : null}
            </View>
          ) : !picked.length ? (
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
              <Muted>常用</Muted>
              {MAJORS.map((id) => <Chip key={id} label={symOf(id)} onPress={() => add(id)} />)}
            </View>
          ) : null}
        </>
      ) : sc ? (
        <View style={{ gap: 12 }}>
          <View>{lbl("排序依据")}<Segmented small value={scope} onChange={setScope} options={[{ value: "pool", label: "活跃池按评分" }, { value: "market", label: "全市场按成交额" }]} /></View>
          {scope === "pool" ? (
            <>
              <View>
                {lbl("评分规则")}
                <Pressable onPress={() => { tap(); setRulePick(true); }} style={{ flexDirection: "row", alignItems: "center", gap: 6, height: 34, paddingHorizontal: 10, borderRadius: radius.sm, borderWidth: 1, borderColor: colors.borderStrong, alignSelf: "flex-start" }}>
                  <Text style={{ color: colors.text, fontSize: fs.md }}>{ruleName}</Text>
                  <Ionicons name="chevron-down" size={14} color={colors.textFaint} />
                </Pressable>
              </View>
              <View>{lbl("方向")}<Segmented small value={sc.side || "long"} onChange={(v) => setSc({ side: v })} options={[{ value: "long", label: "多头分", tone: "up" }, { value: "short", label: "空头分", tone: "down" }, { value: "both", label: "较强一侧" }]} /></View>
            </>
          ) : null}
          <View style={{ flexDirection: "row", gap: 16, flexWrap: "wrap" }}>
            <View>{lbl(`取前 N（1–${screenerMaxTop(sc)}）`)}<NumInput value={sc.top} onChange={(v) => setSc({ top: v })} unit="个" integer /></View>
            {scope === "pool" ? <View>{lbl("最低分（可选）")}<NumInput value={sc.minScore ?? NaN} onChange={(v) => setSc({ minScore: isFinite(v) ? v : undefined })} unit="分" /></View> : null}
          </View>
          <View>{lbl("重新筛选")}<Segmented small value={sc.refresh || "1H"} onChange={(v) => setSc({ refresh: v })} options={["15m", "1H", "4H", "1D"].map((v) => ({ value: v, label: v }))} /></View>
          <PickerSheet visible={rulePick} onClose={() => setRulePick(false)} title="评分规则" value={sc.rule || ""} options={(meta?.rules ?? []).map((r) => ({ value: r.id, label: r.name, desc: r.description }))} onPick={(v) => setSc({ rule: v })} />
        </View>
      ) : null}
    </View>
  );
}

/** Exec mode + universe + params of one instance (web InstanceForm). */
export function InstanceForm({ version, params, universe, exec, onParams, onUniverse, onExec, aiReady }: {
  version: StrategyVersion; params: Record<string, number>; universe: Universe; exec: ExecMode; aiReady?: boolean;
  onParams: Upd<Record<string, number>>; onUniverse: Upd<Universe>; onExec: Upd<ExecMode>;
}) {
  const head = (t: string, tip?: ReactNode) => (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 6, marginBottom: 8 }}>
      <Text style={{ fontSize: fs.md, fontWeight: "600", color: colors.text }}>{t}</Text>
      {tip}
    </View>
  );
  return (
    <View style={{ gap: 18 }}>
      <View>
        {head("执行方式", <InfoTip title="执行方式" items={[
          ["自动", "：信号直接经过账户风控与仓位计算后下单。"],
          ["AI 审核", "：先把信号、持仓、行情摘要交给 AI，AI 决定执行或跳过（可缩小仓位、调整止损止盈），再经风控。AI 不可用或超时按“跳过”处理。"],
          ["只记录", "：只记录信号和它的去向，从不下单，用来观察新策略。"],
          "如果账户开启了人工确认，确认发生在 AI 审核之后、风控之前。",
        ]} />)}
        <Segmented small value={exec} onChange={onExec} options={(Object.keys(execLabel) as ExecMode[]).map((k) => ({ value: k, label: execLabel[k].t }))} />
        <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 6 }}>
          {execLabel[exec].d}{exec === "ai" && aiReady === false ? <Text style={{ color: colors.warn }}> · AI 尚未授权，信号会被跳过（设置 → AI 授权）</Text> : null}
        </Text>
      </View>
      <View>
        {head("交易标的", <InfoTip title="交易标的" items={[
          ["固定列表", "：只交易选中的合约。"],
          ["规则筛选", "：按评分规则在监控池里取分数最高的前 N 个，按“重新筛选”间隔更新；被移出的合约不再开新仓，已有持仓照常由策略管理。分数未经验证，只是排序依据。"],
        ]} />)}
        <UniversePicker u={universe} onChange={onUniverse} />
      </View>
      <View>
        {head(`参数`, <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>· {version.timeframe} 周期</Text>)}
        <ParamsForm specs={version.params || []} params={params} onChange={onParams} />
      </View>
    </View>
  );
}

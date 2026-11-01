// Script signals (lib/signals.ts ScriptInfo / ScriptTest; server internal/signals/script.go): the replay
// over recent history as a strip (one cell per bar close: holds / not / unknown, fires marked) with the
// numbers and the last fires; a test result's problem in Chinese; and the detail section of a script
// subscription (timeframe, code read-only, replay, the newest live check, errors).
import { useMemo, useState } from "react";
import { Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { time } from "@/lib/fmt";
import { PROBLEM_LABEL, type ScriptEval, type ScriptInfo, type ScriptLimits, type ScriptReplay, type ScriptTest } from "@/lib/signals";
import { colors, fs, tnum } from "@/lib/theme";
import { CodeView } from "@/components/trade/CodeView";
import { tap } from "@/components/ui";

const EVERY_TEXT: Record<string, string> = { "1m": "每 1 分钟", "5m": "每 5 分钟", "15m": "每 15 分钟", "1H": "每小时", "4H": "每 4 小时" };

const num = (v: number) => (Math.abs(v) >= 1000 || Number.isInteger(v) ? String(Math.round(v * 100) / 100) : String(+v.toPrecision(4)));
/** "RSI 28.1 · rsi=28.1" */
export function evalText(e: Pick<ScriptEval, "note" | "values">) {
  const vs = Object.entries(e.values ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([k, v]) => `${k}=${num(v)}`).join(" ");
  return [e.note, vs].filter(Boolean).join(" · ");
}

/** The newest check in words. */
export function lastCheckText(e: ScriptEval) {
  if (e.error) return `出错：${e.error}`;
  if (e.missing?.length) return `缺少 K 线：${e.missing.map((x) => x.split("-")[0]).join("、")}`;
  const state = e.fire == null ? "无结论" : e.fire ? "成立" : "未成立";
  const t = evalText(e);
  return t ? `${state} · ${t}` : state;
}

/** Why a test failed, in Chinese, from the structured result (the server's reason is written for the agent). */
export function problemText(t: Pick<ScriptTest, "problem" | "reason" | "diagnostics" | "replay"> & { limits?: ScriptLimits }): string {
  const r = t.replay;
  const lim = t.limits;
  switch (t.problem) {
    case "compile": {
      const d = t.diagnostics?.find((x) => x.severity === "error") ?? t.diagnostics?.[0];
      if (d) return `第 ${d.line} 行：${d.message}`;
      const inst = /unknown instrument (\S+)/.exec(t.reason ?? "");
      return inst ? `未知合约 ${inst[1]}` : t.reason ?? "代码有误";
    }
    case "error":
      return r?.firstError ? `回放中 ${r.errors} 根 K 线出错，首次 ${time(r.firstErrorAt ?? 0, true)}：${r.firstError}` : t.reason ?? "回放出错";
    case "slow":
      return r && r.errors ? `有 ${r.errors} 根 K 线超出每次 ${lim?.budgetMs ?? 200} ms 的 CPU 预算，请减少计算量（更少的 K 线或合约、numpy 向量化）`
        : `太慢：平均 ${r?.avgMs.toFixed(1)} ms，上限 ${lim?.maxAvgMs ?? 50} ms`;
    case "often":
      return `过去 ${r?.days} 天会触发 ${r?.fireCount} 次（每天 ${r?.perDay} 次，上限 ${lim?.maxFiresPerDay ?? 6} 次）：条件太松，收紧条件或换更慢的周期`;
    case "unknown":
      if (r && r.bars > 0 && r.unknown === r.bars) return "每根 K 线都没有结论（返回 None 或缺数据）";
      return `最新一根 K 线判断不了${r?.now?.missing?.length ? `：缺少 ${r.now.missing.map((x) => x.split("-")[0]).join("、")} 的 K 线` : ""}`;
    case "holds":
      return `最新一根 K 线（${time(r?.to ?? 0, true)}）条件已经成立${r?.now ? `：${evalText(r.now)}` : ""}。订阅只在从不成立变为成立时触发：现在就行动，或把条件收紧`;
    case "nodata":
      return "没有足够的最新 K 线数据";
  }
  return t.reason ?? "";
}

/** One cell per bar close, squeezed into the width: violet = holds, dim = not, outline = unknown; fires get a lime tick. */
function Strip({ states, fires, from, to }: { states: string; fires: number[]; from: number; to: number }) {
  // ≤ 168 cells: each shows whether any close inside it held (and fired)
  const cells = useMemo(() => {
    const n = states.length;
    const k = Math.max(1, Math.ceil(n / 168));
    const span = Math.max(1, to - from);
    const out: { v: "1" | "0" | "-"; fire: boolean }[] = [];
    for (let i = 0; i < n; i += k) {
      const part = states.slice(i, i + k);
      out.push({ v: part.includes("1") ? "1" : part.includes("0") ? "0" : "-", fire: false });
    }
    for (const t of fires) {
      const idx = Math.min(out.length - 1, Math.max(0, Math.floor(((t - from) / span) * (n - 1) / k)));
      if (out[idx]) out[idx].fire = true;
    }
    return out;
  }, [states, fires, from, to]);
  return (
    <View style={{ flexDirection: "row", alignItems: "flex-end", height: 18, gap: 1 }}>
      {cells.map((c, i) => (
        <View key={i} style={{ flex: 1, height: "100%", justifyContent: "flex-end" }}>
          {c.fire ? <View style={{ height: 4, marginBottom: 1, borderRadius: 1, backgroundColor: colors.primary }} /> : null}
          <View style={{ height: 10, borderRadius: 1, backgroundColor: c.v === "1" ? colors.violet : c.v === "0" ? colors.cardMuted : "transparent", borderWidth: c.v === "-" ? 1 : 0, borderColor: colors.border }} />
        </View>
      ))}
    </View>
  );
}

/** The replay: "过去 7 天会触发 3 次", a strip and the last fires. */
export function ReplayView({ r, compact }: { r: ScriptReplay; compact?: boolean }) {
  const [all, setAll] = useState(false);
  const fires = r.fires.slice().reverse();
  const shown = all ? fires : fires.slice(0, compact ? 3 : 5);
  const days = r.days >= 1 ? `${+r.days.toFixed(1)} 天` : `${Math.round(r.days * 24)} 小时`;
  return (
    <View style={{ gap: 6 }}>
      <Text style={[{ fontSize: fs.md, color: colors.text }, tnum]}>
        过去 {days}（{r.bars} 根 {r.timeframe}）会触发 <Text style={{ fontWeight: "700", color: r.fireCount ? colors.primary : colors.text }}>{r.fireCount}</Text> 次
        {r.fireCount ? `，每天 ${r.perDay} 次` : ""}
      </Text>
      {r.states ? <Strip states={r.states} fires={r.fires.map((f) => f.t)} from={r.from} to={r.to} /> : null}
      <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
        <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(r.from, true)}</Text>
        <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(r.to, true)}</Text>
      </View>
      <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>
        成立 {[...r.states].filter((c) => c === "1").length} 根 · 无结论 {r.unknown} 根 · 出错 {r.errors} 次 · 平均 {r.avgMs.toFixed(1)} ms，最慢 {r.maxMs.toFixed(1)} ms
      </Text>
      {r.noFactors ? <Text style={{ fontSize: fs.xs, color: colors.warn }}>脚本读了因子字段，回放里没有这些数据，实际触发可能更多。</Text> : null}
      {r.now ? <Text style={[{ fontSize: fs.sm, color: colors.textSoft }, tnum]}>最新一根（{time(r.now.at || r.to, true)}）：{lastCheckText(r.now)}</Text> : null}
      {shown.length ? (
        <View style={{ gap: 2 }}>
          {shown.map((f) => (
            <View key={f.t} style={{ flexDirection: "row", gap: 8, paddingVertical: 3, borderTopWidth: 1, borderColor: colors.border }}>
              <Ionicons name="flash" size={12} color={colors.primary} style={{ marginTop: 2 }} />
              <Text style={[{ fontSize: fs.sm, color: colors.textFaint }, tnum]}>{time(f.t, true)}</Text>
              <Text selectable style={[{ flex: 1, fontSize: fs.sm, color: colors.textSoft }, tnum]}>{evalText(f) || "成立"}</Text>
            </View>
          ))}
          {fires.length > shown.length || all ? (
            <Pressable hitSlop={6} onPress={() => { tap(); setAll(!all); }}>
              <Text style={{ fontSize: fs.xs, color: colors.textSoft }}>{all ? "收起" : `全部 ${fires.length} 次${r.fireCount > fires.length ? `（保留最近 ${fires.length} 次）` : ""}`}</Text>
            </Pressable>
          ) : null}
        </View>
      ) : null}
    </View>
  );
}

/** A test result: the replay, or why it fails. */
export function ScriptTestView({ t }: { t: ScriptTest }) {
  return (
    <View style={{ gap: 8, padding: 10, borderRadius: 10, borderWidth: 1, borderColor: t.ok ? colors.border : colors.warn, backgroundColor: colors.card }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Ionicons name={t.ok ? "checkmark-circle" : "alert-circle"} size={15} color={t.ok ? colors.up : colors.warn} />
        <Text style={{ fontSize: fs.md, fontWeight: "600", color: colors.text }}>{t.ok ? "通过检验，可以订阅" : `未通过：${t.problem ? PROBLEM_LABEL[t.problem] : "检验失败"}`}</Text>
      </View>
      {t.meta ? <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>{t.meta.desc} · {t.meta.timeframe} · {t.meta.instIds.map((x) => x.split("-")[0]).join("、")} · 回看 {t.meta.lookback} 根</Text> : null}
      {!t.ok ? <Text selectable style={{ fontSize: fs.sm, lineHeight: 18, color: colors.warn }}>{problemText(t)}</Text> : null}
      {t.replay && t.replay.bars > 0 ? <ReplayView r={t.replay} compact /> : null}
    </View>
  );
}

/** The script section of a subscription's detail. */
export function ScriptSection({ x, every }: { x: ScriptInfo; every?: string }) {
  const [code, setCode] = useState(false);
  const cadence = every && every !== "1m" && every !== x.timeframe ? `${x.timeframe} 收盘，至少间隔 ${EVERY_TEXT[every] ?? every}` : `每根 ${x.timeframe} K 线收盘`;
  return (
    <View style={{ gap: 8, paddingHorizontal: 4 }}>
      <Text style={{ fontSize: fs.sm, fontWeight: "600", color: colors.textSoft }}>脚本</Text>
      <Text style={[{ fontSize: fs.sm, color: colors.textSoft }, tnum]}>
        {cadence}检查 · {x.instIds.map((i) => i.split("-")[0]).join("、")} · 回看 {x.lookback} 根
      </Text>
      {x.last ? (
        <Text style={[{ fontSize: fs.sm, color: colors.text }, tnum]}>最近检查（{time(x.last.at, true)}）：{lastCheckText(x.last)}</Text>
      ) : null}
      <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>已检查 {x.checks} 次{x.checks ? ` · 平均 ${x.avgMs.toFixed(1)} ms` : ""}</Text>
      {x.lastError ? (
        <Text selectable style={{ fontSize: fs.sm, lineHeight: 18, color: x.errors ? colors.warn : colors.textFaint }}>
          {x.errors ? `连续出错 ${x.errors} 次（${time(x.lastErrorAt ?? 0, true)}）：` : `上次问题（${time(x.lastErrorAt ?? 0, true)}）：`}{x.lastError}
        </Text>
      ) : null}
      {x.replay ? (
        <View style={{ gap: 6, paddingTop: 4 }}>
          <Text style={{ fontSize: fs.sm, fontWeight: "600", color: colors.textSoft }}>订阅时的回放（{time(x.replay.at, true)}）</Text>
          <ReplayView r={x.replay} />
        </View>
      ) : null}
      <Pressable hitSlop={6} onPress={() => { tap(); setCode(!code); }} style={{ flexDirection: "row", alignItems: "center", gap: 4, paddingTop: 2 }}>
        <Ionicons name={code ? "chevron-down" : "chevron-forward"} size={13} color={colors.textSoft} />
        <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>代码（{x.class}）</Text>
      </Pressable>
      {code ? <CodeView code={x.code} /> : null}
    </View>
  );
}

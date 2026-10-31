// Monitoring-pool list (web views/Scanner.vue, phone cards): search, direction, rule, categories,
// gate filter, sort field + direction, watchlist stars, OI bar, recently removed, and matches from
// the whole OKX catalog while searching. `watchOnly` is the 自选 tab: the same rows, starred only.
import { memo, useCallback, useMemo, useRef, useState } from "react";
import { FlatList, Pressable, Text, TextInput, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { api, type CatalogItem, type Meta, type Row } from "@/lib/api";
import { catLabel, fundingH, fundingLevel, logPct, oiFrac, price, sign, sym, time, usd } from "@/lib/fmt";
import { gateOf, scoreOf, toggleWatch, useLivePrice, useMeta, useRuleId, useUniverse, setRule } from "@/lib/market";
import { useQuery } from "@/lib/query";
import { setSettings, useSettings, type Dir } from "@/lib/settings";
import { track } from "@/lib/track";
import { colors, fs, tnum } from "@/lib/theme";
import { CoinIcon } from "./CoinIcon";
import { ScoreChip } from "./ScoreChip";
import { Chip, ChipRow, Dot, EmptyState, ErrorBox, Loading, PickerSheet, Pull, Segmented, Sheet, TAB_BAR_SPACE, Tag, tap, toastError, toneColor } from "./ui";

const ABS_SORT = new Set(["funding_hourly", "funding_pred_hourly"]);
const ALL_CATS = ["1", "3", "4"];

function sortOptions(meta?: Meta) {
  const fs_ = meta?.fields ?? [];
  return [
    { value: "score", label: "按方向分" },
    { value: "long", label: "多头分" },
    { value: "short", label: "空头分" },
    ...fs_.filter((f) => ABS_SORT.has(f.key)).map((f) => ({ value: f.key, label: `${f.label}（绝对值）` })),
    { value: "mcap", label: "总市值" },
    { value: "oi", label: "持仓量" },
    { value: "turnover12h", label: "12h 成交额" },
    ...fs_.filter((f) => f.key !== "turnover12h" && !ABS_SORT.has(f.key)).map((f) => ({ value: f.key, label: f.label })),
  ];
}

export function ScannerList({ watchOnly = false, header }: { watchOnly?: boolean; header?: React.ReactNode }) {
  const router = useRouter();
  const meta = useMeta();
  const uni = useUniverse();
  const ruleId = useRuleId(meta.data);
  const dir = useSettings((s) => s.dir);
  const sortKey = useSettings((s) => s.sortKey);
  const sortDesc = useSettings((s) => s.sortDesc);
  const cats = useSettings((s) => s.cats);
  const onlyPass = useSettings((s) => s.onlyPass);
  const [q, setQ] = useState("");
  const [sheet, setSheet] = useState<"" | "rule" | "sort" | "removed">("");

  const rows = useMemo(() => uni.data?.rows ?? [], [uni.data]);
  const rule = meta.data?.rules.find((r) => r.id === ruleId);

  const dirScore = useCallback((r: Row) => {
    const l = scoreOf(r, ruleId, "long");
    const s = scoreOf(r, ruleId, "short");
    if (dir === "long") return l;
    if (dir === "short") return s;
    if (l == null && s == null) return null;
    return Math.max(l ?? -1, s ?? -1);
  }, [dir, ruleId]);

  const sortVal = useCallback((r: Row): number | null => {
    switch (sortKey) {
      case "score": return dirScore(r);
      case "turnover12h": return r.turnover12h || null;
      case "mcap": return r.mcap || null;
      case "oi": return r.oiUsd || null;
      case "long": return scoreOf(r, ruleId, "long");
      case "short": return scoreOf(r, ruleId, "short");
      default: {
        const v = r.values?.[sortKey] ?? null;
        return v != null && ABS_SORT.has(sortKey) ? Math.abs(v) : v;
      }
    }
  }, [sortKey, ruleId, dirScore]);

  const sorted = useMemo(() => {
    const s = q.trim().toLowerCase();
    const xs = rows.filter((r) => {
      if (watchOnly && !r.watch) return false;
      if (!watchOnly && !cats.includes(r.category)) return false;
      if (s && !r.instId.toLowerCase().includes(s)) return false;
      if (onlyPass && gateOf(r, ruleId) !== "PASS") return false;
      return true;
    });
    // unknown sorts last regardless of direction; ties by 12h turnover then id
    return xs.sort((a, b) => {
      const va = sortVal(a), vb = sortVal(b);
      if (va == null && vb == null) return b.turnover12h - a.turnover12h;
      if (va == null) return 1;
      if (vb == null) return -1;
      if (va !== vb) return sortDesc ? vb - va : va - vb;
      return b.turnover12h - a.turnover12h || a.instId.localeCompare(b.instId);
    });
  }, [rows, q, watchOnly, cats, onlyPass, ruleId, sortVal, sortDesc]);

  // The list re-sorts on every snapshot; while the finger is on it (dragging, momentum) keep the
  // previous order so the row under the finger can't move away before the tap lands.
  const [frozen, setFrozen] = useState<string[] | null>(null);
  const releaseTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const hold = () => {
    if (releaseTimer.current) clearTimeout(releaseTimer.current);
    setFrozen((f) => f ?? sorted.map((r) => r.instId));
  };
  const release = (ms = 600) => {
    if (releaseTimer.current) clearTimeout(releaseTimer.current);
    releaseTimer.current = setTimeout(() => setFrozen(null), ms);
  };
  const shown = useMemo(() => {
    if (!frozen) return sorted;
    const byId = new Map(sorted.map((r) => [r.instId, r]));
    const kept = frozen.map((id) => byId.get(id)).filter((r): r is Row => !!r);
    const known = new Set(frozen);
    return kept.concat(sorted.filter((r) => !known.has(r.instId)));
  }, [sorted, frozen]);

  // Search also covers every live OKX USDT perpetual (catalog); opening one downloads its history.
  const catalog = useQuery(q.trim() ? "catalog" : null, async () => (await api.catalog()).items, { staleMs: 60_000 });
  const others = useMemo(() => {
    const s = q.trim().toLowerCase();
    if (!s || onlyPass) return [];
    const listed = new Set(rows.map((r) => r.instId));
    return (catalog.data ?? [])
      .filter((c) => !listed.has(c.instId) && (watchOnly || cats.includes(c.category)) && c.instId.toLowerCase().includes(s))
      .sort((a, b) => Number(!a.instId.toLowerCase().startsWith(s)) - Number(!b.instId.toLowerCase().startsWith(s)) || a.instId.localeCompare(b.instId))
      .slice(0, 50);
  }, [q, onlyPass, rows, catalog.data, cats, watchOnly]);

  const filtered = !!q || onlyPass || (!watchOnly && cats.length < 3);
  const clear = () => { setQ(""); setSettings({ onlyPass: false, cats: ALL_CATS }); };
  const open = useCallback((id: string) => router.push({ pathname: "/instrument/[id]", params: { id } }), [router]);
  const star = useCallback((id: string, on: boolean) => void toggleWatch(id, on).catch((e) => toastError("未能更新自选", e)), []);
  const catCount = (c: string) => rows.filter((r) => r.category === c).length;
  const sortLabel = sortOptions(meta.data).find((o) => o.value === sortKey)?.label ?? sortKey;
  const removed = uni.data?.removed ?? [];

  const controls = (
    <View style={{ paddingTop: 4, paddingBottom: 8, gap: 8, borderBottomWidth: 1, borderColor: colors.border }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12 }}>
        <View style={{ flex: 1, flexDirection: "row", alignItems: "center", height: 34, borderRadius: 8, backgroundColor: colors.cardMuted, borderWidth: 1, borderColor: colors.borderStrong, paddingHorizontal: 10, gap: 6 }}>
          <Ionicons name="search" size={15} color={colors.textFaint} />
          <TextInput value={q} onChangeText={setQ} placeholder={watchOnly ? "搜索自选或添加合约" : "搜索合约"} placeholderTextColor={colors.textDim} autoCapitalize="characters" autoCorrect={false} clearButtonMode="while-editing" style={{ flex: 1, color: colors.text, fontSize: fs.lg, paddingVertical: 0 }} />
        </View>
        <Segmented<Dir> small value={dir} onChange={(v) => { track("scanner.dir", { v }); setSettings({ dir: v }); }} options={[{ value: "all", label: "全部" }, { value: "long", label: "做多", tone: "up" }, { value: "short", label: "做空", tone: "down" }]} style={{ flexGrow: 0 }} />
      </View>
      <Pressable onPress={() => { tap(); setSheet("rule"); }} style={({ pressed }) => ({ marginHorizontal: 12, flexDirection: "row", alignItems: "center", gap: 8, height: 34, paddingHorizontal: 10, borderRadius: 8, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: pressed ? colors.raised : colors.card })}>
        <Text style={{ color: colors.textFaint, fontSize: fs.md, fontWeight: "700" }}>Σ</Text>
        <Text style={{ flex: 1, color: colors.text, fontSize: fs.md }} numberOfLines={1}>{rule ? rule.name : "评分规则"}</Text>
        <Tag text="未验证" tone="warn" />
        <Ionicons name="chevron-down" size={14} color={colors.textFaint} />
      </Pressable>
      <ChipRow>
        {!watchOnly ? ALL_CATS.map((c) => (
          <Chip key={c} label={catLabel[c]} count={catCount(c)} active={cats.includes(c)} onPress={() => {
            const on = cats.includes(c);
            if (on && cats.length === 1) return; // at least one
            setSettings({ cats: on ? cats.filter((x) => x !== c) : [...cats, c] });
          }} />
        )) : null}
        <Chip label="门槛通过" icon="shield-checkmark-outline" active={onlyPass} onPress={() => setSettings({ onlyPass: !onlyPass })} />
        <Chip label={sortLabel} icon="swap-vertical" onPress={() => setSheet("sort")} />
        <Chip label={sortDesc ? "从大到小" : "从小到大"} icon={sortDesc ? "arrow-down" : "arrow-up"} onPress={() => setSettings({ sortDesc: !sortDesc })} />
        {!watchOnly && removed.length ? <Chip label="最近移出" icon="log-out-outline" count={removed.length} onPress={() => setSheet("removed")} /> : null}
      </ChipRow>
      <View style={{ flexDirection: "row", alignItems: "center", paddingHorizontal: 14, gap: 10 }}>
        <Text style={[{ fontSize: fs.sm, color: colors.text, fontWeight: "600" }, tnum]}>
          {shown.length}<Text style={{ color: colors.textFaint, fontWeight: "400" }}> / {watchOnly ? rows.filter((r) => r.watch).length : rows.length}</Text>
        </Text>
        {uni.data?.preview ? <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>盘中 {time(uni.data.previewAt)} 更新</Text> : null}
        <View style={{ flex: 1 }} />
        {filtered ? (
          <Pressable hitSlop={8} onPress={() => { tap(); clear(); }} style={{ flexDirection: "row", alignItems: "center", gap: 3 }}>
            <Ionicons name="close" size={12} color={colors.textFaint} />
            <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>清除筛选</Text>
          </Pressable>
        ) : null}
      </View>
    </View>
  );

  const footer = (
    <View style={{ paddingBottom: TAB_BAR_SPACE }}>
      {!uni.data && uni.loading ? <Loading text="加载行情" /> : null}
      {uni.data && !shown.length && !others.length ? (
        watchOnly && !q ? <EmptyState icon="star-outline" text="还没有自选合约。在扫描列表或详情页点星标添加，自选合约的数据保留更久、实时录制。" />
          : <EmptyState icon="search-outline" text={uni.data.phase === "live" ? "当前筛选下没有合约" : "正在建池…"} />
      ) : null}
      {others.length ? (
        <View>
          <Text style={{ paddingHorizontal: 12, paddingTop: 14, paddingBottom: 6, fontSize: fs.sm, fontWeight: "600", color: colors.textSoft, borderTopWidth: 1, borderColor: colors.borderStrong }}>
            OKX 其他合约 <Text style={[{ color: colors.textFaint, fontWeight: "400" }, tnum]}>{others.length}</Text>
          </Text>
          <Text style={{ paddingHorizontal: 12, paddingBottom: 6, fontSize: fs.xs, color: colors.textFaint }}>不在当前列表里的 OKX USDT 永续。点开后才按需下载近 7 天历史；加自选后长期保留。</Text>
          {others.map((c) => <OtherRow key={c.instId} c={c} onOpen={open} onStar={(id) => star(id, true)} />)}
        </View>
      ) : null}
    </View>
  );

  return (
    <View style={{ flex: 1 }}>
      {uni.error && !uni.data ? <ErrorBox text={`后端不可用：${uni.error}`} onRetry={uni.refresh} /> : null}
      <FlatList
        data={shown}
        keyExtractor={(r) => r.instId}
        renderItem={({ item }) => <ScanRow r={item} ruleId={ruleId} onOpen={open} onStar={star} />}
        ListHeaderComponent={<>{header}{controls}</>}
        ListFooterComponent={footer}
        refreshControl={<Pull onRefresh={() => Promise.all([uni.refresh(), meta.refresh()])} />}
        keyboardShouldPersistTaps="handled"
        keyboardDismissMode="on-drag"
        onScrollBeginDrag={hold}
        onScrollEndDrag={() => release(800)}
        onMomentumScrollEnd={() => release(400)}
        initialNumToRender={20}
        windowSize={11}
      />
      <PickerSheet
        visible={sheet === "rule"}
        onClose={() => setSheet("")}
        title="评分规则"
        value={ruleId}
        onPick={(v) => { track("scanner.rule"); setRule(v); }}
        options={(meta.data?.rules ?? []).map((r) => ({ value: r.id, label: r.name, badge: "未验证", group: r.preset ? "对照规则" : "我的规则", desc: `v${r.version} · ${r.description}` }))}
      />
      <PickerSheet visible={sheet === "sort"} onClose={() => setSheet("")} title="排序" value={sortKey} onPick={(v) => { track("scanner.sort", { v }); setSettings({ sortKey: v, sortDesc: true }); }} options={sortOptions(meta.data)} />
      <Sheet visible={sheet === "removed"} onClose={() => setSheet("")} title="最近 30 分钟移出监控池" scroll>
        {removed.map((r) => (
          <Pressable key={r.instId + r.at} onPress={() => { setSheet(""); open(r.instId); }} style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 9, borderBottomWidth: 1, borderColor: colors.border }}>
            <CoinIcon instId={r.instId} size={18} />
            <Text style={{ width: 80, color: colors.text, fontWeight: "600" }}>{sym(r.instId)}</Text>
            <Text style={{ flex: 1, color: colors.textFaint, fontSize: fs.sm }} numberOfLines={2}>{r.reason}</Text>
            <Text style={[{ color: colors.textFaint, fontSize: fs.sm }, tnum]}>{time(r.at)}</Text>
          </Pressable>
        ))}
      </Sheet>
    </View>
  );
}

function fundingColor(v?: number | null) {
  const l = fundingLevel(v, 1);
  if (!l || v == null) return { color: colors.textSoft };
  if (l === 3) return { color: colors.warn, backgroundColor: colors.warnBg, fontWeight: "600" as const };
  const c = v > 0 ? colors.up : colors.down;
  return l === 2 ? { color: c, backgroundColor: v > 0 ? colors.upBg : colors.downBg, fontWeight: "600" as const } : { color: c };
}

const ScanRow = memo(function ScanRow({ r, ruleId, onOpen, onStar }: { r: Row; ruleId: string; onOpen: (id: string) => void; onStar: (id: string, on: boolean) => void }) {
  const px = useLivePrice(r.instId, r.last);
  const fr = r.values?.funding_hourly;
  const gate = gateOf(r, ruleId);
  return (
    <Pressable onPress={() => { tap(); onOpen(r.instId); }} style={({ pressed }) => ({ paddingHorizontal: 12, paddingVertical: 8, gap: 5, borderBottomWidth: 1, borderColor: colors.border, backgroundColor: pressed ? colors.raised : colors.bg, opacity: r.active ? 1 : 0.6 })}>
      {/* OI bar: row background filled from the left by open interest (web oiFrac) */}
      <View pointerEvents="none" style={{ position: "absolute", left: 0, top: 0, bottom: 0, width: `${oiFrac(r.oiUsd) * 100}%`, backgroundColor: colors.oiBar }} />
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Dot tone={r.status === "live" ? "ok" : r.status === "stale" ? "bad" : "warn"} />
        <CoinIcon instId={r.instId} size={22} />
        <Text style={{ fontSize: fs.lg, fontWeight: "700", color: colors.text }} numberOfLines={1}>{sym(r.instId)}</Text>
        {r.category !== "1" ? <Tag text={catLabel[r.category] ?? r.category} /> : null}
        {gate?.startsWith("BLOCKED") ? <Tag text="门槛" tone="bad" /> : null}
        <Pressable hitSlop={10} onPress={() => { tap(); onStar(r.instId, !r.watch); }} style={{ paddingHorizontal: 4 }}>
          <Ionicons name={r.watch ? "star" : "star-outline"} size={16} color={r.watch ? colors.gold : colors.textFaint} />
        </Pressable>
        <View style={{ flex: 1 }} />
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>费/h</Text>
        <Text style={[{ fontSize: fs.sm, paddingHorizontal: 3, borderRadius: 3, overflow: "hidden" }, fundingColor(fr), tnum]}>{fundingH(fr)}</Text>
        <Text style={[{ fontSize: fs.lg, fontWeight: "600", color: colors.text, minWidth: 74, textAlign: "right" }, tnum]}>{price(px)}</Text>
      </View>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 12 }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 4 }}><Text style={{ fontSize: fs.sm, color: colors.textFaint }}>多</Text><ScoreChip v={scoreOf(r, ruleId, "long")} side="long" size="sm" /></View>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 4 }}><Text style={{ fontSize: fs.sm, color: colors.textFaint }}>空</Text><ScoreChip v={scoreOf(r, ruleId, "short")} side="short" size="sm" /></View>
        <Text style={[{ fontSize: fs.sm, color: toneColor(sign(r.values?.ret_4h), colors.textSoft) }, tnum]}>4h {logPct(r.values?.ret_4h)}</Text>
        <Text style={[{ fontSize: fs.sm, color: colors.textFaint }, tnum]}>{usd(r.turnover12h)}{r.provisional ? "*" : ""}</Text>
        {r.mcap ? <Text style={[{ fontSize: fs.sm, color: colors.textFaint }, tnum]} numberOfLines={1}>市值 {usd(r.mcap)}</Text> : null}
      </View>
    </Pressable>
  );
});

function OtherRow({ c, onOpen, onStar }: { c: CatalogItem; onOpen: (id: string) => void; onStar: (id: string) => void }) {
  const chg = c.last > 0 && c.open24h > 0 ? Math.log(c.last / c.open24h) : null;
  return (
    <Pressable onPress={() => { tap(); onOpen(c.instId); }} style={({ pressed }) => ({ paddingHorizontal: 12, paddingVertical: 9, gap: 4, borderBottomWidth: 1, borderColor: colors.border, backgroundColor: pressed ? colors.raised : colors.bg })}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <CoinIcon instId={c.instId} size={22} />
        <Text style={{ fontSize: fs.lg, fontWeight: "700", color: colors.text }}>{sym(c.instId)}</Text>
        {c.category !== "1" ? <Tag text={catLabel[c.category] ?? c.category} /> : null}
        {c.preMarket ? <Tag text="盘前" tone="warn" /> : null}
        <Tag text={c.recorded ? "池外" : "未录制"} />
        {!c.watch ? (
          <Pressable hitSlop={10} onPress={() => { tap(); onStar(c.instId); }} style={{ paddingHorizontal: 4 }}>
            <Ionicons name="star-outline" size={16} color={colors.textFaint} />
          </Pressable>
        ) : null}
        <View style={{ flex: 1 }} />
        <Text style={[{ fontSize: fs.sm, color: toneColor(sign(chg), colors.textSoft) }, tnum]}>24h {logPct(chg)}</Text>
        <Text style={[{ fontSize: fs.lg, fontWeight: "600", color: colors.text, minWidth: 74, textAlign: "right" }, tnum]}>{price(c.last)}</Text>
      </View>
    </Pressable>
  );
}


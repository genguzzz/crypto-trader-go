// Instrument detail (web components/InstrumentDetail.vue): header with live price and key numbers,
// stat cards that open their detail, the web K-line chart in a WebView, live order book, relative
// performance, OI and funding history, liquidations. Info polls every 5 s while shown.
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { api, type InstrumentInfo, type Row } from "@/lib/api";
import { ago, catLabel, fieldValue, fundingLevel, fundingLevelText, logPct, num, pct, price, sign, sym, usd, time } from "@/lib/fmt";
import { askAbout } from "@/lib/ai";
import { toggleWatch, useLivePrice, useMeta, useRuleId, setRule } from "@/lib/market";
import { mutate, useQuery } from "@/lib/query";
import { useSettings } from "@/lib/settings";
import { track } from "@/lib/track";
import { colors, fs, radius, tnum } from "@/lib/theme";
import { ChartView } from "@/components/ChartView";
import { CoinIcon } from "@/components/CoinIcon";
import { ComparePanel, FundingPanel, LiqPanel, OIPanel } from "@/components/HistoryPanels";
import { OrderBook } from "@/components/OrderBook";
import { Card, Dot, ErrorBox, Loading, PanelTitle, PickerSheet, Pull, Row as KvRow, Screen, Sheet, Tag, TopBar, tap, toastError, toneColor, styles } from "@/components/ui";

type DetailKey = "score" | "supply" | "book" | "spot" | "funding" | "oi" | "turnover";
const statusText: Record<string, string> = { live: "实时", stale: "数据缺口，补洞中", warming: "预热中", new: "上市不足 12h" };

export default function InstrumentScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const router = useRouter();
  const meta = useMeta();
  const ruleId = useRuleId(meta.data);
  const q = useQuery<InstrumentInfo>(`inst:${id}`, () => api.instrument(id), { interval: 5000, staleMs: 4000 });
  const info = q.data;
  const row = info?.row;
  const [chartPx, setChartPx] = useState<number | null>(null);
  const streamPx = useLivePrice(id, info?.last);
  const live = chartPx ?? streamPx;
  const [detail, setDetail] = useState<DetailKey | null>(null);
  const [scrollOn, setScrollOn] = useState(true);
  const tall = useSettings((s) => s.chartTall);
  const now = useNow();

  useEffect(() => { track("instrument.open", { id }); }, [id]);

  const star = async () => {
    if (!info) return;
    const on = !info.watch;
    mutate<InstrumentInfo>(`inst:${id}`, (d) => (d ? { ...d, watch: on } : d));
    try {
      await toggleWatch(id, on);
    } catch (e) {
      mutate<InstrumentInfo>(`inst:${id}`, (d) => (d ? { ...d, watch: !on } : d));
      toastError("未能更新自选", e);
    }
  };

  const ret24 = row?.values?.ret_24h, ret4 = row?.values?.ret_4h;
  const score = row?.scores?.[ruleId];
  const ruleName = meta.data?.rules.find((r) => r.id === ruleId)?.name ?? ruleId;
  const spread = info && info.bid && info.ask ? ((info.ask - info.bid) / ((info.ask + info.bid) / 2)) * 1e4 : null;
  const countdown = info?.fundingTime ? fmtCountdown(info.fundingTime - now) : "—";
  const openChart = () => {
    track("chart.fullscreen");
    router.push({ pathname: "/chart/[id]", params: { id, spot: info?.spotId ? "1" : "0", oi: info && info.oiUsd > 0 ? "1" : "0" } });
  };

  return (
    <Screen>
      <TopBar
        left={
          <View style={{ flex: 1, flexDirection: "row", alignItems: "center", gap: 8 }}>
            <CoinIcon instId={id} size={26} />
            <View style={{ flexShrink: 1 }}>
              <Text style={{ fontSize: fs.xl, fontWeight: "700", color: colors.text }} numberOfLines={1}>{sym(id)}<Text style={{ fontSize: fs.md, fontWeight: "500", color: colors.textFaint }}>/USDT</Text></Text>
              <Text style={{ fontSize: fs.xs, color: colors.textFaint }} numberOfLines={1}>永续 · {catLabel[info?.category ?? "1"] ?? info?.category}{info?.tickTs ? ` · ${ago(info.tickTs)}前` : ""}</Text>
            </View>
          </View>
        }
        right={
          <>
            <Pressable hitSlop={10} accessibilityLabel="问 AI" onPress={() => { tap(); askAbout(id); router.navigate("/ai"); }} style={{ flexDirection: "row", alignItems: "center", gap: 3, paddingHorizontal: 8, paddingVertical: 4, borderRadius: 12, backgroundColor: colors.primarySoft }}>
              <Ionicons name="sparkles" size={14} color={colors.primary} />
              <Text style={{ fontSize: fs.sm, fontWeight: "600", color: colors.primary }}>问 AI</Text>
            </Pressable>
            <Pressable hitSlop={10} accessibilityLabel={info?.watch ? "取消自选" : "加入自选"} onPress={() => { tap(); void star(); }}>
              <Ionicons name={info?.watch ? "star" : "star-outline"} size={22} color={info?.watch ? colors.gold : colors.textSoft} />
            </Pressable>
          </>
        }
      />
      {!info ? (
        q.error ? <ErrorBox text={q.error} onRetry={q.refresh} /> : <Loading text="加载合约" />
      ) : (
        <ScrollView
          scrollEnabled={scrollOn}
          contentContainerStyle={{ padding: 8, gap: 8, paddingBottom: 40 }}
          refreshControl={<Pull onRefresh={q.refresh} />}
        >
          <Card style={{ paddingHorizontal: 12, paddingVertical: 10 }}>
            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, marginBottom: 8 }}>
              <Tag text={statusText[row?.status ?? ""] ?? "未知"} tone={row?.status === "live" ? "good" : "warn"} icon={<Dot tone={row?.status === "live" ? "ok" : "warn"} />} />
              {row?.active ? <Tag text={`活跃 #${row.rank}`} tone="info" /> : info.eligible ? <Tag text="非活跃" /> : <Tag text="未录制" />}
              {info.syncing ? <Tag text="同步中" tone="info" /> : null}
            </View>
            <View style={{ flexDirection: "row", alignItems: "flex-start", gap: 8 }}>
              <View style={{ flex: 1, gap: 6 }}>
                <Text style={[{ fontSize: 28, fontWeight: "700", color: toneColor(sign(ret24)), letterSpacing: -0.3 }, tnum]} numberOfLines={1} adjustsFontSizeToFit>{price(live)}</Text>
                <View style={{ flexDirection: "row", gap: 6 }}>
                  <Chg label="24h" v={ret24} />
                  <Chg label="4h" v={ret4} />
                </View>
              </View>
              <View style={{ gap: 1 }}>
                <KeyNum k={`资金费 ${info.fundingIntervalH || "?"}h`} v={`${(info.fundingRate * 100).toFixed(4)}%`} color={fundingTone(info.fundingRate, info.fundingIntervalH)} x={countdown} onPress={() => setDetail("funding")} />
                <KeyNum k="持仓量" v={usd(info.oiUsd)} x={logPct(row?.values?.oi_chg_4h)} xColor={toneColor(sign(row?.values?.oi_chg_4h), colors.textFaint)} onPress={() => setDetail("oi")} />
                <KeyNum k="12h 额" v={usd(row?.turnover12h)} x={row?.values?.vol_surprise_4h != null ? "×" + Math.exp(row.values.vol_surprise_4h).toFixed(2) : "—"} onPress={() => setDetail("turnover")} />
              </View>
            </View>
            <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ marginTop: 10, marginHorizontal: -12, borderTopWidth: 1, borderColor: colors.border }} contentContainerStyle={{ paddingHorizontal: 6, paddingTop: 8, gap: 4 }}>
              <StatCard label="评分" icon="analytics-outline" onPress={() => setDetail("score")}
                value={<Text style={[{ fontSize: fs.md, fontWeight: "600", color: colors.text }, tnum]}>
                  <Text style={sl}>多 </Text><Text style={{ color: (score?.long.score ?? 0) >= 50 ? colors.up : colors.textSoft }}>{score?.long.score?.toFixed(0) ?? "—"}</Text>
                  <Text style={{ color: colors.textFaint }}> · </Text>
                  <Text style={sl}>空 </Text><Text style={{ color: (score?.short.score ?? 0) >= 50 ? colors.down : colors.textSoft }}>{score?.short.score?.toFixed(0) ?? "—"}</Text>
                </Text>}
                sub={score ? (score.gate === "PASS" ? ruleName : score.gate.replace("BLOCKED:", "门槛：")) : "预热中"} />
              <StatCard label="总市值" icon="pie-chart-outline" onPress={() => setDetail("supply")}
                value={<Text style={valStyle}>{info.coin ? usd(info.coin.marketCap) : "—"}{info.coin ? <Text style={{ fontSize: fs.xs, color: colors.textFaint }}> USD</Text> : null}</Text>}
                sub={info.coin && info.coin.total ? `已流通 ${((info.coin.circulating / info.coin.total) * 100).toFixed(1)}%` : info.category === "1" ? "无市值数据" : "非代币"} />
              <StatCard label="买一 / 卖一" icon="reorder-four-outline" onPress={() => setDetail("book")}
                value={<Text style={valStyle}>{price(info.bid)} <Text style={{ color: colors.textFaint }}>/</Text> {price(info.ask)}</Text>}
                sub={`点差 ${num(spread, 1)} bps`} />
              <StatCard label="OKX 现货" icon="swap-horizontal-outline" onPress={() => setDetail("spot")}
                value={<Text style={valStyle}>{info.spotId ? price(info.spotLast) : "—"}</Text>}
                sub={info.spotId ? `基差 ${fieldValue("basis", row?.values?.basis)}` : "无同名 USDT 现货"} />
            </ScrollView>
          </Card>

          <ChartView
            id={id}
            hasSpot={!!info.spotId}
            hasOi={info.oiUsd > 0}
            style={{ height: tall ? 560 : 470, borderRadius: radius.md, borderWidth: 1, borderColor: colors.border }}
            onLive={setChartPx}
            onFullscreen={(enter) => enter && openChart()}
            onTouchActive={(a) => setScrollOn(!a)}
          />

          <Card>
            <PanelTitle title="实时盘口" hint="USDT · 每秒刷新" />
            <OrderBook id={id} />
          </Card>
          <ComparePanel id={id} />
          <OIPanel id={id} />
          <FundingPanel id={id} />
          <LiqPanel id={id} />
          <Card>
            <PanelTitle title="新闻 / 解锁 / 链上持有人" right={<Tag text="未接入" />} />
            <Text style={styles.note}>计划接入可靠来源（项目公告、解锁日程、链上地址分布），保留发布时间与修订。现在显示“未接入”——不代表没有事件或没有风险。股票/商品永续本身没有链上数据。</Text>
          </Card>
        </ScrollView>
      )}
      {info ? <DetailSheet k={detail} onClose={() => setDetail(null)} info={info} row={row} ruleId={ruleId} countdown={countdown} spread={spread} /> : null}
    </Screen>
  );
}

const sl = { fontSize: fs.xs, fontWeight: "500" as const, color: colors.textFaint };
const valStyle = [{ fontSize: fs.md, fontWeight: "600" as const, color: colors.text }, tnum];

function useNow() {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  return now;
}

function fmtCountdown(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000));
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(Math.floor(s / 3600))}:${p(Math.floor((s % 3600) / 60))}:${p(s % 60)}`;
}

function fundingTone(rate: number, intervalH: number) {
  const l = fundingLevel(rate, intervalH);
  return l === 3 ? colors.warn : l ? (rate > 0 ? colors.up : colors.down) : colors.text;
}

function Chg({ label, v }: { label: string; v?: number | null }) {
  const t = sign(v);
  return (
    <View style={{ flexDirection: "row", alignItems: "baseline", gap: 3, paddingHorizontal: 6, paddingVertical: 1, borderRadius: 4, backgroundColor: t === "up" ? colors.upBg : t === "down" ? colors.downBg : colors.cardMuted }}>
      <Text style={{ fontSize: fs.xs, color: toneColor(t, colors.textSoft), opacity: 0.75 }}>{label}</Text>
      <Text style={[{ fontSize: fs.sm, fontWeight: "600", color: toneColor(t, colors.textSoft) }, tnum]}>{logPct(v)}</Text>
    </View>
  );
}

function KeyNum({ k, v, x, color, xColor, onPress }: { k: string; v: string; x?: string; color?: string; xColor?: string; onPress: () => void }) {
  return (
    <Pressable onPress={() => { tap(); onPress(); }} style={({ pressed }) => ({ flexDirection: "row", alignItems: "baseline", justifyContent: "flex-end", gap: 6, paddingHorizontal: 4, paddingVertical: 2, borderRadius: 6, backgroundColor: pressed ? colors.raised : "transparent" })}>
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{k}</Text>
      <Text style={[{ fontSize: fs.sm, fontWeight: "600", color: color ?? colors.text, minWidth: 64, textAlign: "right" }, tnum]}>{v}</Text>
      {x ? <Text style={[{ fontSize: fs.xs, color: xColor ?? colors.textFaint, minWidth: 54, textAlign: "right" }, tnum]}>{x}</Text> : null}
    </Pressable>
  );
}

function StatCard({ label, icon, value, sub, onPress }: { label: string; icon: keyof typeof Ionicons.glyphMap; value: ReactNode; sub: string; onPress: () => void }) {
  return (
    <Pressable onPress={() => { tap(); onPress(); }} style={({ pressed }) => ({ width: 148, paddingHorizontal: 8, paddingVertical: 6, borderRadius: 8, backgroundColor: pressed ? colors.raised : "transparent", gap: 2 })}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 4 }}>
        <Ionicons name={icon} size={12} color={colors.textFaint} />
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{label}</Text>
        <Ionicons name="chevron-forward" size={11} color={colors.textDim} />
      </View>
      {value}
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }} numberOfLines={1}>{sub}</Text>
    </Pressable>
  );
}

// ---------- stat details (web components/details/*) ----------

function DetailSheet({ k, onClose, info, row, ruleId, countdown, spread }: { k: DetailKey | null; onClose: () => void; info: InstrumentInfo; row?: Row; ruleId: string; countdown: string; spread: number | null }) {
  const title = { score: "评分", supply: "总市值与解锁", book: "盘口", spot: "OKX 现货与基差", funding: "资金费", oi: "持仓量", turnover: "成交额" }[k ?? "score"];
  return (
    <Sheet visible={!!k} onClose={onClose} title={`${sym(info.instId)}/USDT · ${title}`} scroll>
      {k === "score" ? <ScoreDetail row={row} ruleId={ruleId} /> : null}
      {k === "book" ? <OrderBook id={info.instId} ladder={10} /> : null}
      {k === "supply" ? <SupplyDetail info={info} /> : null}
      {k === "spot" ? (
        info.spotId ? (
          <>
            <KvRow label="现货" value={info.spotId} />
            <KvRow label="现货价" value={price(info.spotLast)} />
            <KvRow label="永续价" value={price(info.last)} />
            <KvRow label="基差" value={fieldValue("basis", row?.values?.basis)} />
            <Text style={[styles.note, { marginTop: 10 }]}>基差 = 永续收盘 ÷ 同时刻 OKX 同名 USDT 现货收盘 − 1。正值 = 永续比现货贵（多头情绪/资金费偏正时常见），负值相反。只是 OKX 一家现货，不代表全市场。K 线图可叠加现货线。</Text>
          </>
        ) : <Text style={styles.note}>该合约没有 OKX 同名 USDT 现货（股票/商品永续或未上现货的币），无法计算基差。</Text>
      ) : null}
      {k === "funding" ? (
        <>
          <KvRow label="本期费率" value={`${(info.fundingRate * 100).toFixed(4)}%`} />
          <KvRow label="结算间隔" value={`${info.fundingIntervalH || "?"} 小时`} />
          <KvRow label="距结算" value={countdown} />
          <KvRow label="折合每小时" value={fieldValue("funding_hourly", row?.values?.funding_hourly)} />
          <KvRow label="预测每小时" value={fieldValue("funding_pred_hourly", row?.values?.funding_pred_hourly)} />
          <KvRow label="年化（简单）" value={info.fundingIntervalH ? `${((info.fundingRate * (24 / info.fundingIntervalH) * 365) * 100).toFixed(1)}%` : "—"} />
          <KvRow label="水平" value={fundingLevelText[fundingLevel(info.fundingRate, info.fundingIntervalH)]} />
          <Text style={[styles.note, { marginTop: 10 }]}>正值 = 多头付给空头，负值 = 空头付给多头。OKX 各合约结算间隔不同（4h/8h），比较时看“折合每小时”。年化只是按本期费率简单换算，不代表未来。</Text>
          <View style={{ marginTop: 12 }}><FundingPanel id={info.instId} /></View>
        </>
      ) : null}
      {k === "oi" ? (
        <>
          <KvRow label="持仓量（USD）" value={usd(info.oiUsd)} />
          <KvRow label="持仓量（币）" value={usd(info.oiCcy)} />
          <KvRow label="4h 变化（币）" value={logPct(row?.values?.oi_chg_4h)} />
          <KvRow label="采样时间" value={time(info.oiTs, true)} />
          <View style={{ marginTop: 12 }}><OIPanel id={info.instId} /></View>
        </>
      ) : null}
      {k === "turnover" ? (
        <>
          <KvRow label="12h 成交额" value={`${usd(row?.turnover12h)} USDT`} />
          <KvRow label="24h 成交额" value={info.turnover24h ? `${usd(info.turnover24h)} USDT` : "—"} />
          <KvRow label="4h 放量" value={fieldValue("vol_surprise_4h", row?.values?.vol_surprise_4h)} />
          <KvRow label="点差" value={`${num(spread, 1)} bps`} />
          <Text style={[styles.note, { marginTop: 10 }]}>12h 成交额 = 最近 720 根已确认 1m K 线 volCcyQuote 之和，严格大于阈值才进监控池。放量 = 近 4 小时成交额相对 7 天中位数的倍数。</Text>
        </>
      ) : null}
    </Sheet>
  );
}

function ScoreDetail({ row, ruleId }: { row?: Row; ruleId: string }) {
  const meta = useMeta();
  const [pick, setPick] = useState(false);
  const label = (key: string) => meta.data?.fields.find((f) => f.key === key)?.label ?? key;
  const score = row?.scores?.[ruleId];
  const rule = meta.data?.rules.find((r) => r.id === ruleId);
  const router = useRouter();
  const fields = useMemo(() => meta.data?.fields ?? [], [meta.data]);
  return (
    <View>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 12 }}>
        <Pressable onPress={() => { tap(); setPick(true); }} style={{ flex: 1, flexDirection: "row", alignItems: "center", gap: 6, height: 34, paddingHorizontal: 10, borderRadius: 8, borderWidth: 1, borderColor: colors.borderStrong }}>
          <Text style={{ flex: 1, color: colors.text, fontSize: fs.md }} numberOfLines={1}>{rule?.name ?? "评分规则"}</Text>
          <Ionicons name="chevron-down" size={14} color={colors.textFaint} />
        </Pressable>
        <Pressable hitSlop={8} onPress={() => { tap(); router.push("/rules"); }}><Text style={{ color: colors.primary, fontSize: fs.sm }}>战绩 →</Text></Pressable>
      </View>
      {score ? (["long", "short"] as const).map((side) => (
        <View key={side} style={{ marginBottom: 16 }}>
          <View style={{ flexDirection: "row", alignItems: "baseline", gap: 10, paddingBottom: 8, marginBottom: 6, borderBottomWidth: 1, borderColor: colors.border }}>
            <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>{side === "long" ? "多头分" : "空头分"}</Text>
            <Text style={[{ fontSize: 28, fontWeight: "700", color: (score[side].score ?? 0) >= 50 ? (side === "long" ? colors.up : colors.down) : colors.textSoft }, tnum]}>{score[side].score != null ? score[side].score!.toFixed(0) : "—"}</Text>
            {score.gate !== "PASS" ? <Tag text={score.gate.replace("BLOCKED:", "门槛：")} tone="bad" /> : null}
          </View>
          {(score[side].contrib ?? []).map((c) => (
            <View key={c.field} style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 3 }}>
              <Text style={{ flex: 1.5, fontSize: fs.sm, color: colors.text }} numberOfLines={1}>{label(c.field)}</Text>
              <Text style={[{ flex: 1, fontSize: fs.sm, color: colors.textSoft }, tnum]} numberOfLines={1}>{fieldValue(c.field, row?.values?.[c.field])}</Text>
              <Text style={[{ width: 48, fontSize: fs.sm, color: colors.textFaint }, tnum]}>分位 {c.pct.toFixed(0)}</Text>
              <View style={{ width: 40, height: 4, backgroundColor: colors.cardMuted, borderRadius: 2, overflow: "hidden" }}>
                <View style={{ height: 4, width: `${Math.min(100, Math.abs(c.points) * 2)}%`, backgroundColor: c.points >= 0 ? colors.up : colors.down }} />
              </View>
              <Text style={[{ width: 40, textAlign: "right", fontSize: fs.sm, fontWeight: "600", color: c.points >= 0 ? colors.up : colors.down }, tnum]}>{c.points >= 0 ? "+" : ""}{c.points.toFixed(1)}</Text>
            </View>
          ))}
          {score[side].missing?.length ? <Text style={styles.note}>缺少：{score[side].missing!.map(label).join("、")} → 未评分（不是 0）</Text> : null}
        </View>
      )) : <Text style={styles.note}>暂无评分（数据预热中或缺少输入）</Text>}
      <Text style={[styles.title, { marginTop: 4, marginBottom: 6 }]}>全部字段</Text>
      {fields.map((f) => (
        <View key={f.key} style={{ flexDirection: "row", alignItems: "center", paddingVertical: 6, borderBottomWidth: 1, borderColor: colors.border, gap: 8 }}>
          <Text style={{ flex: 1.4, fontSize: fs.sm, color: colors.text }} numberOfLines={1}>{f.label}{!f.replayable ? <Text style={{ color: colors.textFaint, fontSize: fs.xs }}> 不可回放</Text> : null}</Text>
          <Text style={[{ flex: 1, textAlign: "right", fontSize: fs.sm, color: colors.text }, tnum]}>{fieldValue(f.key, row?.values?.[f.key])}</Text>
          <Text style={[{ width: 44, textAlign: "right", fontSize: fs.sm, color: colors.textFaint }, tnum]}>{row?.pct?.[f.key] != null ? row.pct[f.key].toFixed(0) : "—"}</Text>
        </View>
      ))}
      <Text style={[styles.note, { marginTop: 8 }]}>分数 = 各字段在活跃池内的分位映射到 ±1 后按规则权重加权：50 + 50·Σwx/Σ|w|。规则都未经验证，只有战绩（样本外 Rank IC、扣成本收益）能支持它。缺失显示“—”，不当作 0。</Text>
      <PickerSheet visible={pick} onClose={() => setPick(false)} title="评分规则" value={ruleId} onPick={setRule}
        options={(meta.data?.rules ?? []).map((r) => ({ value: r.id, label: r.name, badge: "未验证", group: r.preset ? "对照规则" : "我的规则", desc: `v${r.version} · ${r.description}` }))} />
    </View>
  );
}

function SupplyDetail({ info }: { info: InstrumentInfo }) {
  const c = info.coin;
  if (!c) return <Text style={styles.note}>{info.category === "1" ? "没有找到该币的市值数据（CoinGecko 前 1500 名之外或符号不匹配），或数据还在加载。" : "股票 / 商品类永续没有代币市值与解锁数据。"}</Text>;
  const locked = c.total && c.circulating ? c.total - c.circulating : null;
  return (
    <>
      <KvRow label="总市值" value={`${usd(c.marketCap)} USD`} />
      <KvRow label="完全稀释" value={c.fdv ? `${usd(c.fdv)} USD` : "—"} />
      <KvRow label="排名" value={c.rank ? `#${c.rank}` : "—"} />
      <KvRow label="流通量" value={usd(c.circulating)} />
      <KvRow label="总量" value={usd(c.total)} />
      <KvRow label="最大供应" value={c.max ? usd(c.max) : "无上限/未知"} />
      <KvRow label="已流通" value={c.total ? pct(c.circulating / c.total).replace("+", "") : "—"} />
      <Text style={[styles.note, { marginTop: 10 }]}>
        未解锁约为 总量 − 流通量（{locked != null ? `${usd(locked)} 枚` : "—"}），按现价折合约 {locked != null && c.marketCap && c.circulating ? `${usd(locked * (c.marketCap / c.circulating))} USD` : "—"}。数据来自 CoinGecko（{c.name} / {c.id}），约每 2 小时刷新。只用于展示，不参与监控池与评分。
      </Text>
    </>
  );
}

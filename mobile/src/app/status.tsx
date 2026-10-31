// 系统状态 (web views/Status.vue): health summary with jump-to-section issues, KPIs, run phase and
// background jobs, coverage, OKX connections, storage (quota and retention per tier), process.
// Polls every 3 s while focused (storage every 60 s), like the web page.
import { useMemo, useRef, useState, type ReactNode } from "react";
import { Pressable, ScrollView, Text, View, type ScrollViewInstance } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { ago, time, usd } from "@/lib/fmt";
import { useQuery } from "@/lib/query";
import { bytes, count, sysApi, tableLabel, tierCols, type Storage, type SystemStatus } from "@/lib/sysApi";
import { colors, fs, tnum } from "@/lib/theme";
import { B, InfoTip, LinkText, Meter, P } from "@/components/Form";
import { Card, Chip, ChipRow, Dot, ErrorBox, Loading, PanelTitle, Pull, Screen, Tag, TopBar, tap, styles } from "@/components/ui";

type Tone = "ok" | "warn" | "bad";
const worst = (...t: Tone[]): Tone => (t.includes("bad") ? "bad" : t.includes("warn") ? "warn" : "ok");
const phaseLabel: Record<string, string> = { live: "实时", "backfill-1m": "1m 回填中", "bootstrap-5m": "快速建池", starting: "启动中" };
const jobState = (m: string): { t: string; tone?: "good" | "info" } =>
  m.startsWith("完成") ? { t: "完成", tone: "good" } : m.startsWith("等待") || m.startsWith("排队") ? { t: "等待" } : { t: "进行中", tone: "info" };
const toneColor = (t?: Tone) => (t === "bad" ? colors.down : t === "warn" ? colors.warn : colors.up);

export default function StatusScreen() {
  const st = useQuery<SystemStatus>("sys:status", () => sysApi.status(), { interval: 3000, staleMs: 2000 });
  const sto = useQuery<Storage | null>("sys:storage", () => sysApi.storage(), { interval: 60_000, staleMs: 30_000 });
  const s = st.data;
  const storage = sto.data ?? null;
  const scroll = useRef<ScrollViewInstance>(null);
  const [ys] = useState<Record<string, number>>(() => ({})); // section offsets, filled by onLayout
  const go = (id: string) => { tap(); scroll.current?.scrollTo({ y: Math.max(0, (ys[id] ?? 0) - 8), animated: true }); };
  const at = (id: string) => ({ onLayout: (e: { nativeEvent: { layout: { y: number } } }) => { ys[id] = e.nativeEvent.layout.y; } });

  const h = useMemo(() => {
    if (!s) return null;
    const now = st.at; // fetch time of this status
    const live = s.phase === "live";
    const lagMs = now - (s.asOfMinute + 60_000);
    const ws = s.ws ?? [];
    const wsUp = ws.filter((w) => w.connected).length;
    const wsSlow = ws.filter((w) => w.connected && w.lastMsgAgoMs > 30_000);
    const cov = s.coverage;
    const errRate = s.restRequests ? s.restErrors / s.restRequests : 0;
    const used = storage ? storage.usedBytes / storage.capBytes : 0;
    const tight = storage?.tables?.filter((t) => t.tight).length ?? 0;
    const issues: { tone: Tone; text: string; section: string }[] = [];
    if (!live) issues.push({ tone: "warn", text: phaseLabel[s.phase] || s.phase, section: "run" });
    if (live && lagMs > 180_000) issues.push({ tone: "bad", text: `数据已 ${ago(s.asOfMinute + 60_000)} 未更新`, section: "run" });
    if (wsUp < ws.length) issues.push({ tone: "bad", text: `${ws.length - wsUp} 条推送断开`, section: "conn" });
    if (wsSlow.length) issues.push({ tone: "warn", text: `${wsSlow.map((w) => w.name).join("/")} 推送延迟`, section: "conn" });
    if (cov.stale) issues.push({ tone: cov.stale > cov.eligible * 0.05 ? "bad" : "warn", text: `${cov.stale} 个合约数据缺口`, section: "coverage" });
    if (errRate > 0.01) issues.push({ tone: "warn", text: `REST 错误率 ${(errRate * 100).toFixed(1)}%`, section: "conn" });
    if (used >= 0.9) issues.push({ tone: used >= 1 ? "bad" : "warn", text: `存储已用 ${(used * 100).toFixed(0)}%`, section: "storage" });
    if (tight) issues.push({ tone: "warn", text: `${tight} 类数据保留期被收紧`, section: "storage" });
    const tone = (sec: string) => worst("ok", ...issues.filter((i) => i.section === sec).map((i) => i.tone));
    const lagS = Math.max(0, Math.round(lagMs / 1000));
    return {
      issues, overall: worst("ok", ...issues.map((i) => i.tone)), tone,
      lagText: lagS < 120 ? lagS + "s" : Math.round(lagS / 60) + "m",
      lagTone: (!live ? "warn" : lagMs > 180_000 ? "bad" : lagMs > 90_000 ? "warn" : "ok") as Tone,
      wsUp, wsTotal: ws.length, lastMsg: Math.max(0, ...ws.map((w) => w.lastMsgAgoMs)),
    };
  }, [s, storage, st.at]);

  const nav: { id: string; label: string; tone?: Tone }[] = h
    ? [{ id: "overview", label: "概览", tone: h.overall }, { id: "run", label: "运行", tone: h.tone("run") }, { id: "coverage", label: "覆盖", tone: h.tone("coverage") }, { id: "conn", label: "OKX 连接", tone: h.tone("conn") }, { id: "storage", label: "数据存储", tone: h.tone("storage") }, { id: "proc", label: "进程" }]
    : [];

  return (
    <Screen>
      <TopBar title="系统状态" sub={s ? `每 3 秒刷新 · 已运行 ${s.uptime}` : undefined} />
      {!s || !h ? (
        st.error ? <ErrorBox text={"后端不可用：" + st.error} onRetry={st.refresh} /> : <Loading text="加载系统状态" />
      ) : (
        <>
          <ChipRow style={{ paddingVertical: 8, borderBottomWidth: 1, borderColor: colors.border }}>
            {nav.map((n) => <Chip key={n.id} label={n.label} icon={n.tone && n.tone !== "ok" ? "alert-circle" : undefined} onPress={() => go(n.id)} />)}
          </ChipRow>
          <ScrollView ref={scroll} contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: 60 }} refreshControl={<Pull onRefresh={() => Promise.all([st.refresh(), sto.refresh()])} />}>
            {/* 概览 */}
            <View {...at("overview")}>
              <Card style={{ borderColor: h.overall === "ok" ? colors.border : h.overall === "bad" ? colors.downBg : colors.warnBg }}>
                <View style={{ flexDirection: "row", alignItems: "flex-start", gap: 10 }}>
                  <Ionicons name={h.overall === "ok" ? "checkmark-circle-outline" : h.overall === "warn" ? "warning-outline" : "close-circle-outline"} size={24} color={toneColor(h.overall)} />
                  <View style={{ flex: 1, gap: 4 }}>
                    <Text style={{ fontSize: fs.xl, fontWeight: "600", color: colors.text }}>{h.overall === "ok" ? "运行正常" : `${h.issues.length} 项需要关注`}</Text>
                    <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                      <Tag text={phaseLabel[s.phase] || s.phase} tone={s.phase === "live" ? "good" : "warn"} />
                      <Text style={styles.faint}>已运行 {s.uptime}</Text>
                      {st.error ? <Text style={{ fontSize: fs.sm, color: colors.down }}>刷新失败：{st.error}</Text> : null}
                    </View>
                  </View>
                  <InfoTip title="健康检查">
                    <P><B>健康检查</B>（每 3 秒刷新）：</P>
                    <P>· 阶段不是“实时”、最新确认分钟超过 3 分钟没更新 → 需关注 / 异常；</P>
                    <P>· WS 推送断开 → 异常；已连接但 30 秒没有消息 → 需关注；</P>
                    <P>· 有合约出现数据缺口（缺分钟不参与排名）→ 需关注，超过 5% → 异常；</P>
                    <P>· REST 错误率超过 1%、存储用量 ≥ 90% 或有数据类型的保留期被收紧 → 需关注。</P>
                    <P>点按问题标签跳到对应分区。</P>
                  </InfoTip>
                </View>
                {h.issues.length ? (
                  <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, marginTop: 10 }}>
                    {h.issues.map((i) => (
                      <Pressable key={i.text} onPress={() => go(i.section)}>
                        <Tag text={`${i.text} →`} tone={i.tone === "bad" ? "bad" : "warn"} />
                      </Pressable>
                    ))}
                  </View>
                ) : null}
                <View style={{ flexDirection: "row", flexWrap: "wrap", marginTop: 14, paddingTop: 12, borderTopWidth: 1, borderColor: colors.border, rowGap: 14 }}>
                  <Kpi tone={h.lagTone} label="数据新鲜度" value={<>{h.lagText}<Small> 前</Small></>} sub={`最新确认 ${time(s.asOfMinute + 60_000)}`} />
                  <Kpi tone="ok" label="活跃池" value={<>{s.coverage.active}<Small> / {s.config.topN}</Small></>} sub={`12h > ${usd(s.config.threshold)}`} />
                  <Kpi tone={h.tone("coverage")} label="数据缺口" value={s.coverage.stale} valueColor={s.coverage.stale ? colors.down : undefined} sub={`完整 ${s.coverage.ready} / ${s.coverage.eligible}`} />
                  <Kpi tone={h.tone("conn")} label="实时推送" value={<>{h.wsUp}<Small> / {h.wsTotal} 在线</Small></>} sub={`最近消息 ${(h.lastMsg / 1000).toFixed(1)}s`} />
                  <View style={{ width: "100%", gap: 4 }}>
                    <KpiLabel tone={h.tone("storage")} label="存储" />
                    {storage ? (
                      <>
                        <Text style={[kv, tnum]}>{bytes(storage.usedBytes)}</Text>
                        <Meter value={storage.usedBytes} max={storage.capBytes} height={4} />
                        <Text style={ks}>上限 {bytes(storage.capBytes)}</Text>
                      </>
                    ) : <Text style={ks}>首次检查中</Text>}
                  </View>
                </View>
              </Card>
            </View>

            {/* 运行 */}
            <View {...at("run")}>
              <Card>
                <PanelTitle title="运行" right={<InfoTip title="运行">
                  <P><B>1m 回填</B>：启动时为全部可交易合约补齐最近 12h 的 1m K 线，完成后进入“实时”。</P>
                  <P><B>历史回放</B>：用近 {Math.round(s.config.replayHours / 24)} 天的整点横截面重算各规则的战绩（改设置或规则后后台重跑）。</P>
                  <P><B>1m 历史回补</B>：为活跃池与自选补齐近 7 天 1m K 线，供图表与研究使用。</P>
                </InfoTip>} />
                <KvRow k="1m 回填">
                  <Text style={[val, tnum]}>{s.backfillDone} / {s.backfillTotal}</Text>
                  {s.backfillTotal && s.backfillDone >= s.backfillTotal ? <Tag text="完成" tone="good" /> : <View style={{ flex: 1, maxWidth: 160 }}><Meter value={s.backfillDone} max={s.backfillTotal} height={4} tone="ok" /></View>}
                </KvRow>
                <KvRow k="最新确认分钟"><Text style={[val, tnum]}>{time(s.asOfMinute + 60_000)} <Text style={ks}>计算于 {ago(s.computedAt)} 前</Text></Text></KvRow>
                <Job k="历史回放" m={s.replay} />
                <Job k="1m 历史回补" m={s.history} />
                <Job k="1H 历史回补" m={s.hourHistory || "等待"} last />
              </Card>
            </View>

            {/* 覆盖 */}
            <View {...at("coverage")}>
              <Coverage s={s} />
            </View>

            {/* OKX 连接 */}
            <View {...at("conn")}>
              <Card>
                <PanelTitle title="OKX 连接" right={<InfoTip title="OKX 连接">
                  <P>仅使用 OKX v5 <B>公共</B>接口（无凭据、无下单）。</P>
                  <P><B>business</B> WS：全池 1m K 线；<B>public</B> WS：全池 OI 与资金费，盘口报价只订阅活跃池和自选（全池订阅约 40GB/天，没必要）。</P>
                  <P>REST：现货行情 15 秒、永续 ticker 30 秒兜底，历史回填与补洞走独立限频。时间戳按交易所时间记录，缺失分钟不当 0。</P>
                </InfoTip>} />
                <View style={{ gap: 8 }}>
                  {(s.ws ?? []).map((w) => (
                    <ConnCard key={w.name} tone={!w.connected ? "bad" : w.lastMsgAgoMs > 30_000 ? "warn" : "ok"} name={w.name} kind="WS"
                      right={<Text style={[{ fontSize: fs.sm, color: w.connected ? colors.textSoft : colors.down }, tnum]}>{w.connected ? (w.lastMsgAgoMs / 1000).toFixed(1) + "s" : "断开"}</Text>}
                      stats={[["订阅", String(w.subscriptions)], ["消息", count(w.messages)], ["重连", String(w.reconnects), w.reconnects ? colors.warn : undefined]]}
                      note={w.subErrors ? `订阅错误 ${w.subErrors}` : undefined} />
                  ))}
                  <ConnCard tone={h.tone("conn") === "ok" || !s.restErrors ? "ok" : "warn"} name="REST" kind="HTTP"
                    stats={[["请求", count(s.restRequests)], ["错误", String(s.restErrors)], ["限频", String(s.restRateLimited), s.restRateLimited ? colors.warn : undefined]]} />
                </View>
              </Card>
            </View>

            {/* 数据存储 */}
            <View {...at("storage")}>
              <Card>
                <PanelTitle title="数据存储" right={<InfoTip title="数据存储">
                  <P><B>分层</B>（取最高）：<B>自选</B> ＞ <B>活跃池</B>（在池内或离池不满 7 天）＞ <B>最近查看</B>（3 天内打开过详情）＞ <B>全池录制</B>（设置类别内、未进池）＞ <B>未跟踪</B>（已下线/类别外且未查看）。各层下为保留期，层名旁为当前合约数。</P>
                  <P><B>按周期分别维护</B>：1m K 线、持仓量、1H K 线、资金费、订单流行数差几个数量级，各自有保留期和容量配额（上限 × 1m 45% / 持仓量 10% / 1H 10% / 资金费 5% / 逐笔订单流 18% / 主动买卖量 2%，其余 10% 给规则与评分快照）。订单流只录活跃池、自选和正在查看的合约。</P>
                  <P><B>淘汰</B>：每 30 分钟先按保留期删除；某类数据超出配额时，从最低层开始逐步缩短保留期（每次 ×2/3），直到回到配额的 90%，但不低于保底（1m/持仓量 26 小时，自选 3 天；1H 21 天），被收紧的会标出。</P>
                  <P><B>下载</B>：打开详情或加自选时按需补近 7 天 1m（不超过该层保留期），离开列表的币按新层级自动淘汰。评分快照（战绩）永不删除。</P>
                  <P>删除后的空间在库文件内复用，文件本身不缩小。全池 1m 约 60 MB/天。</P>
                </InfoTip>} />
                {storage ? <StorageDetail storage={storage} rows={s.db} /> : sto.loading || !sto.at ? <Loading text="读取存储占用" /> : <Text style={styles.note}>存储检查在服务启动约 2 分钟后首次运行。</Text>}
              </Card>
            </View>

            {/* 进程 */}
            <View {...at("proc")}>
              <Card>
                <PanelTitle title="进程" right={<InfoTip title="进程"><P>Go 进程内存与协程，用于排查泄漏；“内存中合约”为保留 1m 环形缓冲的合约数。</P></InfoTip>} />
                {s.mem ? (
                  <Grid items={[
                    ["堆内存", `${s.mem.heapAllocMB} MB`], ["进程内存", `${s.mem.sysMB} MB`], ["协程", String(s.mem.goroutines)],
                    ["内存中合约", String(s.mem.insts)], ["GC 次数", count(s.mem.numGC)], ["订单流解码错误", String(s.mem.flowDecodeErrors), s.mem.flowDecodeErrors ? colors.warn : undefined],
                  ]} />
                ) : <Text style={styles.note}>—</Text>}
              </Card>
            </View>
          </ScrollView>
        </>
      )}
    </Screen>
  );
}

const kv = { fontSize: 24, fontWeight: "700" as const, color: colors.text };
const ks = { fontSize: fs.xs, color: colors.textSoft };
const val = { fontSize: fs.lg, color: colors.text };
const Small = ({ children }: { children: ReactNode }) => <Text style={{ fontSize: fs.md, fontWeight: "500", color: colors.textFaint }}>{children}</Text>;

function KpiLabel({ tone, label }: { tone: Tone; label: string }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
      <Dot tone={tone} />
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{label}</Text>
    </View>
  );
}

function Kpi({ tone, label, value, sub, valueColor }: { tone: Tone; label: string; value: ReactNode; sub: string; valueColor?: string }) {
  return (
    <View style={{ width: "50%", gap: 4, paddingRight: 8 }}>
      <KpiLabel tone={tone} label={label} />
      <Text style={[kv, tnum, valueColor ? { color: valueColor } : null]} numberOfLines={1}>{value}</Text>
      <Text style={ks} numberOfLines={1}>{sub}</Text>
    </View>
  );
}

function KvRow({ k, children, last }: { k: string; children: ReactNode; last?: boolean }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 10, paddingVertical: 9, borderBottomWidth: last ? 0 : 1, borderColor: colors.border }}>
      <Text style={{ width: 96, fontSize: fs.sm, color: colors.textFaint }}>{k}</Text>
      <View style={{ flex: 1, flexDirection: "row", alignItems: "center", gap: 8 }}>{children}</View>
    </View>
  );
}

function Job({ k, m, last }: { k: string; m: string; last?: boolean }) {
  const j = jobState(m);
  return (
    <View style={{ paddingVertical: 9, gap: 4, borderBottomWidth: last ? 0 : 1, borderColor: colors.border }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
        <Text style={{ width: 96, fontSize: fs.sm, color: colors.textFaint }}>{k}</Text>
        <Tag text={j.t} tone={j.tone} />
      </View>
      <Text style={{ marginLeft: 106, fontSize: fs.xs, lineHeight: 17, color: colors.textSoft }}>{m}</Text>
    </View>
  );
}

function Coverage({ s }: { s: SystemStatus }) {
  const c = s.coverage;
  const segs = [
    { k: "ready", label: "12h 窗口完整", n: c.ready, color: colors.up },
    { k: "warming", label: "预热中", n: c.warming, color: colors.info },
    { k: "stale", label: "数据缺口", n: c.stale, color: colors.down },
    { k: "new", label: "上市不足 12h", n: c.new, color: colors.textFaint },
  ];
  return (
    <Card>
      <PanelTitle title="覆盖" hint={`${c.eligible} 个可交易合约`} right={<InfoTip title="覆盖">
        <P><B>12h 窗口完整</B>：连续 720 根已确认 1m K 线齐全，可以参与排名。</P>
        <P><B>预热中</B>：刚开始录制，窗口还没补齐。<B>数据缺口</B>：窗口内缺分钟（缺失不当 0），暂不参与排名。</P>
        <P><B>上市不足 12h</B>：新合约，满 12h 才参与。</P>
        <P><B>活跃池</B>：12h 成交额严格大于阈值、按成交额取前 N（在“设置 → 监控池”调整）。</P>
      </InfoTip>} />
      <View style={{ flexDirection: "row", gap: 2, height: 10, borderRadius: 5, overflow: "hidden", backgroundColor: colors.cardMuted }}>
        {segs.filter((g) => g.n).map((g) => <View key={g.k} style={{ flexGrow: g.n, minWidth: 3, backgroundColor: g.color }} />)}
      </View>
      <View style={{ flexDirection: "row", flexWrap: "wrap", marginTop: 12, rowGap: 8 }}>
        {segs.map((g) => (
          <View key={g.k} style={{ width: "50%", flexDirection: "row", alignItems: "center", gap: 6, paddingRight: 12, opacity: g.n ? 1 : 0.5 }}>
            <View style={{ width: 8, height: 8, borderRadius: 2, backgroundColor: g.color }} />
            <Text style={{ flex: 1, fontSize: fs.sm, color: colors.textFaint }} numberOfLines={1}>{g.label}</Text>
            <Text style={[{ fontSize: fs.sm, fontWeight: "700", color: g.k === "stale" && g.n ? colors.down : colors.text }, tnum]}>{g.n}</Text>
          </View>
        ))}
      </View>
      <View style={{ gap: 6, marginTop: 14, paddingTop: 12, borderTopWidth: 1, borderColor: colors.border }}>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>活跃池</Text>
        <Text style={[{ fontSize: fs.md, color: colors.textSoft }, tnum]}><Text style={{ fontSize: fs.xl, fontWeight: "700", color: colors.text }}>{c.active}</Text> / {s.config.topN}  <Text style={ks}>12h 成交额 &gt; {usd(s.config.threshold)}</Text></Text>
        <Meter value={c.active} max={s.config.topN} height={4} tone="ok" />
      </View>
    </Card>
  );
}

function ConnCard({ tone, name, kind, right, stats, note }: { tone: Tone; name: string; kind: string; right?: ReactNode; stats: [string, string, string?][]; note?: string }) {
  return (
    <View style={{ backgroundColor: colors.bg, borderWidth: 1, borderColor: colors.border, borderRadius: 8, padding: 10 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Dot tone={tone} />
        <Text style={{ fontSize: fs.md, fontWeight: "700", color: colors.text }}>{name}</Text>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{kind}</Text>
        <View style={{ flex: 1 }} />
        {right}
      </View>
      <Grid items={stats} small />
      {note ? <Text style={{ fontSize: fs.sm, color: colors.down, marginTop: 6 }}>{note}</Text> : null}
    </View>
  );
}

function Grid({ items, small }: { items: [string, string, string?][]; small?: boolean }) {
  return (
    <View style={{ flexDirection: "row", flexWrap: "wrap", marginTop: 10, rowGap: 12 }}>
      {items.map(([k, v, c]) => (
        <View key={k} style={{ width: "33.33%", gap: 2, paddingRight: 6 }}>
          <Text style={{ fontSize: fs.xs, color: colors.textFaint }} numberOfLines={1}>{k}</Text>
          <Text style={[{ fontSize: small ? fs.lg : fs.xl, fontWeight: "600", color: c ?? colors.text }, tnum]}>{v}</Text>
        </View>
      ))}
    </View>
  );
}

// ---------- storage (web components/status/StorageDetail.vue) ----------

function StorageDetail({ storage, rows }: { storage: Storage; rows: Record<string, number> | null }) {
  const router = useRouter();
  const [open, setOpen] = useState<string | null>(null);
  const sortedRows = Object.entries(rows ?? {}).sort((a, b) => b[1] - a[1]);
  const tierCount = (i: number) => storage.tiers?.[storage.tierNames?.[i]] ?? 0;
  return (
    <View style={{ gap: 14 }}>
      <View style={{ gap: 8 }}>
        <View style={{ flexDirection: "row", alignItems: "baseline", gap: 6 }}>
          <Text style={[{ fontSize: fs.x2, fontWeight: "700", color: colors.text }, tnum]}>{bytes(storage.usedBytes)}</Text>
          <Text style={{ fontSize: fs.md, color: colors.textFaint }}>/ {bytes(storage.capBytes)}</Text>
          <View style={{ flex: 1 }} />
          <LinkText label="调整上限 →" onPress={() => router.push("/settings")} />
        </View>
        <Meter value={storage.usedBytes} max={storage.capBytes} height={8} />
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>库文件 {bytes(storage.fileBytes)} · 可复用 {bytes(storage.freeBytes)} · {time(storage.at)} 检查</Text>
      </View>

      <View>
        <Text style={{ fontSize: fs.sm, fontWeight: "600", color: colors.textSoft, marginBottom: 4 }}>各类数据 <Text style={{ fontWeight: "400", color: colors.textFaint, fontSize: fs.xs }}>点按看各层保留期</Text></Text>
        {(storage.tables ?? []).map((t) => (
          <Pressable key={t.name} onPress={() => { tap(); setOpen(open === t.name ? null : t.name); }} style={{ paddingVertical: 9, borderTopWidth: 1, borderColor: colors.border, gap: 5 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
              <Text style={{ flex: 1, fontSize: fs.md, color: colors.text }} numberOfLines={1}>{tableLabel[t.name] || t.name}</Text>
              {t.tight ? <Tag text="已收紧" tone="warn" /> : null}
              {t.name === "funding_obs" ? <Text style={ks}>同上</Text> : <Text style={[{ fontSize: fs.sm, color: colors.textSoft }, tnum]}>{bytes(t.bytes)} <Text style={{ color: colors.textFaint }}>/ {bytes(t.budget)}</Text></Text>}
              <Ionicons name={open === t.name ? "chevron-up" : "chevron-down"} size={14} color={colors.textFaint} />
            </View>
            {t.name !== "funding_obs" ? <Meter value={t.bytes} max={t.budget} height={3} /> : null}
            {open === t.name ? (
              <View style={{ flexDirection: "row", marginTop: 4 }}>
                {tierCols.map(([i, l]) => (
                  <View key={i} style={{ flex: 1, alignItems: "center", gap: 2 }}>
                    <Text style={{ fontSize: 10, color: colors.textFaint }}>{l}</Text>
                    <Text style={[{ fontSize: 10, color: colors.textDim }, tnum]}>{tierCount(i)} 个</Text>
                    <Text style={[{ fontSize: fs.sm, color: colors.textSoft }, tnum]}>{t.ages?.[i] ?? "长期"}</Text>
                  </View>
                ))}
              </View>
            ) : null}
          </Pressable>
        ))}
        {storage.error ? <Text style={{ fontSize: fs.sm, color: colors.warn, marginTop: 6 }}>{storage.error}</Text> : null}
      </View>

      <View>
        <Text style={{ fontSize: fs.sm, fontWeight: "600", color: colors.textSoft, marginBottom: 4 }}>记录数 <Text style={{ fontWeight: "400", color: colors.textFaint, fontSize: fs.xs }}>每分钟统计</Text></Text>
        {rows ? sortedRows.map(([k, v]) => (
          <View key={k} style={{ flexDirection: "row", justifyContent: "space-between", paddingVertical: 6, borderTopWidth: 1, borderColor: colors.border }}>
            <Text style={{ fontSize: fs.sm, color: colors.textFaint }}>{tableLabel[k] || k}</Text>
            <Text style={[{ fontSize: fs.sm, color: colors.text }, tnum]}>{count(v)}</Text>
          </View>
        )) : <Text style={styles.note}>行数统计中…</Text>}
      </View>
    </View>
  );
}

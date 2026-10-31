// Dev (web views/Dev.vue): access credentials for the external read-only API / MCP (create, edit,
// revoke, delete; the secret is shown once), connection details and snippets, recent external
// requests, and the endpoint reference. The interactive tester stays on the web page.
import { useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { WEB_ORIGIN } from "@/lib/api";
import { copy, openPage } from "@/lib/device";
import { ago, time } from "@/lib/fmt";
import { mutate, useQuery } from "@/lib/query";
import { sysApi, type ApiKey, type DevConfig, type DevEndpoints, type DevLog, type KeyKind } from "@/lib/sysApi";
import { track } from "@/lib/track";
import { colors, fs, tnum } from "@/lib/theme";
import { B, Code, Field, InfoTip, Input, LinkText, NumInput, P } from "@/components/Form";
import { CodeBlock, mono } from "@/components/Markdown";
import { DEV_CONFIG } from "@/components/settings/DevSection";
import { Card, Dot, EmptyState, ErrorBox, IconButton, Loading, PanelTitle, PillButton, Pull, Screen, Segmented, Sheet, Tag, TopBar, confirm, tap, toast, toastError, styles } from "@/components/ui";

const KEY_PLACEHOLDER = "<YOUR_KEY>";
const restBase = `${WEB_ORIGIN}/api/open/v1`;
const mcpUrl = `${WEB_ORIGIN}/mcp`;
const curlSnippet = (key = KEY_PLACEHOLDER, path = "universe?limit=20") => `curl -H "Authorization: Bearer ${key}" \\\n  "${restBase}/${path}"`;
const claudeCodeSnippet = (key = KEY_PLACEHOLDER) => `claude mcp add --transport http okx-helper ${mcpUrl} \\\n  --header "Authorization: Bearer ${key}"`;
const mcpJsonSnippet = (key = KEY_PLACEHOLDER) => JSON.stringify({ mcpServers: { "okx-helper": { type: "http", url: mcpUrl, headers: { Authorization: `Bearer ${key}` } } } }, null, 2);
const ttlLabel = (h: number) => (h === 0 ? "永不过期" : h < 24 ? `${h} 小时` : `${h / 24} 天`);
const statusTag = (k: ApiKey) => (k.status === "active" ? { t: "有效", tone: "good" as const } : k.status === "expired" ? { t: "已过期", tone: "warn" as const } : { t: "已吊销", tone: "bad" as const });
const logTone = (s: number) => (s === 200 ? "good" : s === 429 ? "warn" : "bad") as "good" | "warn" | "bad";
const KEYS = "dev:keys";

export default function DevScreen() {
  const router = useRouter();
  const keys = useQuery<ApiKey[]>(KEYS, async () => (await sysApi.devKeys()) ?? [], { interval: 10_000, staleMs: 5000 });
  const logs = useQuery<DevLog[]>("dev:log", async () => (await sysApi.devLog()) ?? [], { interval: 10_000, staleMs: 5000 });
  const cfg = useQuery<DevConfig>(DEV_CONFIG, () => sysApi.devConfig(), { staleMs: 30_000 }).data;
  const eps = useQuery<DevEndpoints>("dev:endpoints", () => sysApi.devEndpoints(), { staleMs: 10 * 60_000 }).data;
  const [form, setForm] = useState<{ open: boolean; edit: ApiKey | null }>({ open: false, edit: null });
  const [fresh, setFresh] = useState(""); // a just-created secret, for the snippets on this page only
  const [snip, setSnip] = useState<"claude" | "json">("claude");
  const [shown, setShown] = useState(30);
  const [openEp, setOpenEp] = useState<string | null>(null);

  const upsert = (k: ApiKey) => mutate<ApiKey[]>(KEYS, (l = []) => (l.some((x) => x.id === k.id) ? l.map((x) => (x.id === k.id ? k : x)) : [k, ...l]));
  const revoke = (k: ApiKey) => confirm("吊销凭证", `吊销「${k.name}」？使用它的 Agent 会立即收到 401，无法恢复。`, "吊销", async () => {
    try { upsert(await sysApi.updateKey(k.id, { revoke: true })); toast("已吊销", k.name); } catch (e) { toastError("未能吊销", e); }
  }, true);
  const remove = (k: ApiKey) => confirm("删除凭证", `删除「${k.name}」及其用量统计？`, "删除", async () => {
    try { await sysApi.deleteKey(k.id); mutate<ApiKey[]>(KEYS, (l = []) => l.filter((x) => x.id !== k.id)); toast("已删除", k.name); } catch (e) { toastError("未能删除", e); }
  }, true);
  const active = (keys.data ?? []).filter((k) => k.status === "active").length;

  return (
    <Screen>
      <TopBar title="Dev" sub="外部 API 与 MCP" right={<IconButton name="add" size={26} color={colors.primary} label="新建凭证" onPress={() => setForm({ open: true, edit: null })} />} />
      <ScrollView contentContainerStyle={{ padding: 12, gap: 12, paddingBottom: 60 }} refreshControl={<Pull onRefresh={() => Promise.all([keys.refresh(), logs.refresh()])} />}>
        {cfg && !cfg.enabled ? (
          <Pressable onPress={() => { tap(); router.push("/settings"); }} style={{ flexDirection: "row", alignItems: "center", gap: 8, padding: 12, borderRadius: 8, backgroundColor: colors.warnBg }}>
            <Ionicons name="warning-outline" size={16} color={colors.warn} />
            <Text style={{ flex: 1, fontSize: fs.sm, color: colors.warn }}>外部 API 与 MCP 已关闭，所有 Key 调用会返回 503。去设置开启 →</Text>
          </Pressable>
        ) : null}

        <Card>
          <PanelTitle title="访问凭证" hint={keys.data ? `${active} 个有效` : undefined} right={<View style={{ flexDirection: "row", alignItems: "center", gap: 12 }}>
            <InfoTip title="访问凭证">
              <P><B>API Key</B>：长期凭证（可选过期），给常驻 Agent / 服务用。<B>Token</B>：短期凭证，必须设置有效期（≤ 30 天）。两者用法相同：<Code>Authorization: Bearer &lt;key&gt;</Code> 或 <Code>X-API-Key</Code> 请求头。</P>
              <P>服务器只保存密钥的 SHA-256 哈希，明文只在创建时显示一次。</P>
              <P><B>限流</B>：每个凭证一个令牌桶——容量 = 突发，按“频率上限/分钟”匀速补充；每次调用扣除该接口的消耗（多数为 1，较重的接口 2–3）。另有全服务器总上限（设置 → 开发者）保护行情记录。</P>
              <P>每个响应都带 <Code>X-RateLimit-Limit / Burst / Remaining / Reset / Cost</Code>；超限返回 429 与 <Code>Retry-After</Code>（秒），MCP 工具则返回带 <Code>retryAfterMs</Code> 的错误结果。Agent 可调用 <Code>get_usage</Code>（免费）查看剩余额度。</P>
            </InfoTip>
            <PillButton small icon="add" label="新建" onPress={() => setForm({ open: true, edit: null })} />
          </View>} />
          {!keys.data ? (
            keys.error ? <ErrorBox text={keys.error} onRetry={keys.refresh} /> : <Loading text="加载凭证" />
          ) : !keys.data.length ? (
            <EmptyState icon="key-outline" text="还没有凭证，新建一个给 Agent 使用" />
          ) : keys.data.map((k, i) => {
            const st = statusTag(k);
            return (
              <View key={k.id} style={{ paddingVertical: 10, gap: 5, borderTopWidth: i ? 1 : 0, borderColor: colors.border, opacity: k.status === "active" ? 1 : 0.55 }}>
                <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                  <Ionicons name={k.kind === "token" ? "timer-outline" : "key-outline"} size={15} color={colors.textFaint} />
                  <Text style={{ fontSize: fs.lg, fontWeight: "600", color: colors.text, flexShrink: 1 }} numberOfLines={1}>{k.name}</Text>
                  <Tag text={k.kind === "token" ? "Token" : "API Key"} />
                  <Tag text={st.t} tone={st.tone} />
                  <View style={{ flex: 1 }} />
                  {k.status !== "revoked" ? <IconButton name="create-outline" size={17} label={`编辑 ${k.name}`} onPress={() => setForm({ open: true, edit: k })} /> : null}
                  {k.status !== "revoked" ? <IconButton name="ban-outline" size={17} label={`吊销 ${k.name}`} onPress={() => revoke(k)} /> : null}
                  <IconButton name="trash-outline" size={17} label={`删除 ${k.name}`} onPress={() => remove(k)} />
                </View>
                <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>
                  <Text style={{ fontFamily: mono }}>{k.prefix}…</Text> · {k.ratePerMin}/分钟 · 突发 {k.burst} · {k.expiresAt ? `${k.status === "expired" ? "已于" : ""}${time(k.expiresAt, true)}${k.status === "expired" ? "过期" : " 过期"}` : "永不过期"}
                </Text>
                <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>
                  请求 <Text style={{ color: colors.textSoft }}>{k.requests}</Text> · 限流 <Text style={{ color: k.limited ? colors.warn : colors.textSoft }}>{k.limited}</Text> · 最近使用 <Text style={{ color: colors.textSoft }}>{k.lastUsedAt ? ago(k.lastUsedAt) + "前" : "—"}</Text>
                </Text>
              </View>
            );
          })}
        </Card>

        <Card>
          <PanelTitle title="接入" right={<InfoTip title="接入">
            <P><B>REST</B>：只读 JSON 接口，完整文档见 OpenAPI（无需 Key 即可读取文档）。</P>
            <P><B>MCP</B>：Streamable HTTP（无状态，仅 POST JSON-RPC），工具与 REST 接口一一对应，适合 Claude Code、Cursor 等 Agent 直接调用。</P>
            <P>只开放行情与研究数据（监控池、事实面板、规则分数、K 线、持仓量、资金费、盘口、订单流、强平、战绩）和数据新鲜度；不开放设置、推送渠道、存储与内存等系统细节，也不能修改任何东西。</P>
          </InfoTip>} />
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6, marginBottom: 6 }}>
            <Text style={{ width: 44, fontSize: fs.sm, color: colors.textFaint }}>状态</Text>
            {cfg ? <><Dot tone={cfg.enabled ? "ok" : "bad"} /><Text style={{ fontSize: fs.md, color: colors.text }}>{cfg.enabled ? "已开放" : "已关闭"}</Text><Text style={{ fontSize: fs.sm, color: colors.textFaint }}> · 全服务器 {cfg.globalPerMin}/分钟</Text></> : <Text style={styles.faint}>—</Text>}
            <View style={{ flex: 1 }} />
            <LinkText label="设置" onPress={() => router.push("/settings")} />
          </View>
          <CopyRow label="REST 基础地址" text={restBase} />
          <CopyRow label="MCP 地址" text={mcpUrl} />
          <Pressable onPress={() => { tap(); void openPage(`${WEB_ORIGIN}/api/open/v1/openapi.json`); }} style={{ flexDirection: "row", alignItems: "center", gap: 6, paddingVertical: 8 }}>
            <Ionicons name="document-text-outline" size={14} color={colors.primary} />
            <Text style={{ fontSize: fs.md, color: colors.primary }}>OpenAPI 文档（openapi.json）</Text>
          </Pressable>
          <Segmented small value={snip} onChange={setSnip} options={[{ value: "claude", label: "Claude Code" }, { value: "json", label: "MCP JSON" }]} style={{ marginTop: 4 }} />
          <CodeBlock text={snip === "claude" ? claudeCodeSnippet(fresh || undefined) : mcpJsonSnippet(fresh || undefined)} lang={snip === "claude" ? "shell" : "json"} />
        </Card>

        <Card>
          <PanelTitle title="最近请求" right={<InfoTip title="最近请求"><P>最近 200 次外部调用（REST 与 MCP 工具，仅内存，重启清空），每 10 秒刷新。</P></InfoTip>} />
          {!logs.data ? <Loading text="加载" /> : !logs.data.length ? <Text style={styles.note}>还没有外部调用</Text> : logs.data.slice(0, shown).map((l, i) => (
            <View key={i} style={{ paddingVertical: 7, gap: 3, borderTopWidth: i ? 1 : 0, borderColor: colors.border }}>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                <Tag text={String(l.status)} tone={logTone(l.status)} />
                <Tag text={l.via.toUpperCase()} />
                <Text style={{ flex: 1, fontFamily: mono, fontSize: fs.sm, color: colors.text }} numberOfLines={1}>{l.endpoint}</Text>
                <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{l.ms}ms{l.cost ? ` · ${l.cost}` : ""}</Text>
              </View>
              <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(l.ts, true)} · {l.keyName || "未认证"}{l.error ? ` · ${l.error}` : ""}</Text>
            </View>
          ))}
          {logs.data && logs.data.length > shown ? <PillButton small variant="ghost" label={`显示更多（共 ${logs.data.length} 条）`} onPress={() => setShown((n) => n + 50)} style={{ marginTop: 8 }} /> : null}
        </Card>

        <Card>
          <PanelTitle title="接口" hint={eps ? `${eps.endpoints.length} 个 · MCP 工具同名` : undefined} />
          {!eps ? <Loading text="加载接口" /> : eps.endpoints.map((e, i) => {
            const on = openEp === e.name;
            return (
              <View key={e.name} style={{ borderTopWidth: i ? 1 : 0, borderColor: colors.border }}>
                <Pressable onPress={() => { tap(); setOpenEp(on ? null : e.name); }} style={{ flexDirection: "row", alignItems: "center", gap: 6, paddingVertical: 9 }}>
                  <Ionicons name={on ? "chevron-down" : "chevron-forward"} size={13} color={colors.textFaint} />
                  <View style={{ flex: 1, gap: 2 }}>
                    <Text style={{ fontFamily: mono, fontSize: fs.sm, color: colors.text }}>/{e.path}</Text>
                    <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>{e.summary}</Text>
                  </View>
                  {e.cost && e.cost !== "1" ? <Tag text={`消耗 ${e.cost}`} /> : null}
                </Pressable>
                {on ? (
                  <View style={{ paddingLeft: 19, paddingBottom: 10, gap: 6 }}>
                    <Text style={{ fontSize: fs.sm, lineHeight: 19, color: colors.textSoft }}>{e.desc}</Text>
                    {(e.params ?? []).map((p) => (
                      <View key={p.name} style={{ paddingVertical: 4, borderTopWidth: 1, borderColor: colors.border, gap: 2 }}>
                        <Text style={{ fontSize: fs.sm, color: colors.text }}>
                          <Text style={{ fontFamily: mono }}>{p.name}</Text>{p.required || p.in === "path" ? <Text style={{ color: colors.down }}>*</Text> : null}
                          <Text style={{ color: colors.textFaint }}>  {p.enum ? p.enum.join(" | ") : p.type}{p.max ? ` (${p.min}–${p.max})` : ""}{p.default != null ? ` · 默认 ${String(p.default)}` : ""}</Text>
                        </Text>
                        <Text style={{ fontSize: fs.xs, lineHeight: 16, color: colors.textFaint }}>{p.desc}</Text>
                      </View>
                    ))}
                    <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                      <Text style={{ flex: 1, fontSize: fs.xs, color: colors.textFaint }}>MCP 工具 <Text style={{ fontFamily: mono }}>{e.name}</Text></Text>
                      <LinkText label="在网页版试用 →" onPress={() => void openPage(`${WEB_ORIGIN}/dev`)} />
                    </View>
                  </View>
                ) : null}
              </View>
            );
          })}
        </Card>
      </ScrollView>
      <KeyForm open={form.open} edit={form.edit} maxRate={eps?.limits.maxRatePerMin ?? 6000} maxBurst={eps?.limits.maxBurst ?? 1000}
        onClose={() => setForm((f) => ({ ...f, open: false }))}
        onSaved={(k) => { upsert(k); setForm((f) => ({ ...f, open: false })); }}
        onCreated={(k) => { const { secret, ...rest } = k; upsert(rest); setFresh(secret); }} />
    </Screen>
  );
}

function CopyRow({ label, text, secret }: { label: string; text: string; secret?: boolean }) {
  return (
    <Pressable onPress={() => { tap(); void copy(text).then(() => toast(`已复制${label}`)); }} style={({ pressed }) => ({ gap: 3, paddingVertical: 7, opacity: pressed ? 0.7 : 1 })}>
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{label}</Text>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, padding: 9, borderRadius: 6, borderWidth: 1, borderColor: secret ? colors.warn : colors.border, backgroundColor: colors.bg }}>
        <Text selectable style={{ flex: 1, fontFamily: mono, fontSize: fs.sm, color: colors.text }}>{text}</Text>
        <Ionicons name="copy-outline" size={15} color={colors.textFaint} />
      </View>
    </Pressable>
  );
}

// ---------- create / edit (web components/dev/KeyForm.vue) ----------

function KeyForm({ open, edit, maxRate, maxBurst, onClose, onSaved, onCreated }: {
  open: boolean; edit: ApiKey | null; maxRate: number; maxBurst: number; onClose: () => void; onSaved: (k: ApiKey) => void; onCreated: (k: ApiKey & { secret: string }) => void;
}) {
  const [created, setCreated] = useState<(ApiKey & { secret: string }) | null>(null);
  const close = () => { setCreated(null); onClose(); };
  return (
    <Sheet visible={open} onClose={close} title={created ? "已创建：请立即复制" : edit ? "编辑访问凭证" : "新建访问凭证"} scroll>
      {created ? (
        <View style={{ gap: 6, paddingBottom: 8 }}>
          <View style={{ flexDirection: "row", gap: 8, padding: 10, borderRadius: 8, backgroundColor: colors.warnBg }}>
            <Ionicons name="warning-outline" size={15} color={colors.warn} />
            <Text style={{ flex: 1, fontSize: fs.sm, lineHeight: 19, color: colors.warn }}>密钥<Text style={{ fontWeight: "700" }}>只显示这一次</Text>，服务器只保存哈希，关闭后无法再查看；遗失请吊销后重建。</Text>
          </View>
          <CopyRow label={`${created.kind === "token" ? "Token" : "API Key"} · ${created.name}`} text={created.secret} secret />
          <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginTop: 4 }}>REST 调用示例</Text>
          <CodeBlock text={curlSnippet(created.secret)} lang="shell" />
          <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>接入 Claude Code（MCP）</Text>
          <CodeBlock text={claudeCodeSnippet(created.secret)} lang="shell" />
          <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>通用 MCP 配置（JSON）</Text>
          <CodeBlock text={mcpJsonSnippet(created.secret)} lang="json" />
          <PillButton label="我已保存" onPress={close} />
        </View>
      ) : open ? (
        <KeyFormBody key={edit?.id ?? "new"} edit={edit} maxRate={maxRate} maxBurst={maxBurst} onClose={close} onSaved={onSaved}
          onCreated={(k) => { setCreated(k); onCreated(k); }} />
      ) : null}
    </Sheet>
  );
}

function KeyFormBody({ edit, maxRate, maxBurst, onClose, onSaved, onCreated }: { edit: ApiKey | null; maxRate: number; maxBurst: number; onClose: () => void; onSaved: (k: ApiKey) => void; onCreated: (k: ApiKey & { secret: string }) => void }) {
  const [kind, setKind] = useState<KeyKind>(edit?.kind ?? "key");
  const [name, setName] = useState(edit?.name ?? "");
  const [rate, setRate] = useState(edit?.ratePerMin ?? 60);
  const [burst, setBurst] = useState<number>(edit ? edit.burst : NaN);
  const [ttl, setTtl] = useState(0);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState("");
  const ttlOptions = (kind === "token" ? [1, 24, 168, 720] : [0, 168, 720, 2160, 8760]).map((h) => ({ value: String(h), label: ttlLabel(h) }));
  const defaultBurst = Math.max(1, Math.min(rate || 1, Math.max(5, Math.floor((rate || 0) / 3)), maxBurst));
  const errors: Record<string, string> = {};
  if (!name.trim()) errors.name = "请输入名称";
  if (!Number.isInteger(rate) || rate < 1 || rate > maxRate) errors.rate = `1–${maxRate} 的整数`;
  if (!Number.isNaN(burst) && (!Number.isInteger(burst) || burst < 1 || burst > maxBurst)) errors.burst = `1–${maxBurst} 的整数`;
  const valid = !Object.keys(errors).length;

  const save = async () => {
    if (!valid) return;
    setSaving(true);
    setErr("");
    try {
      const b = Number.isNaN(burst) ? defaultBurst : burst;
      if (edit) onSaved(await sysApi.updateKey(edit.id, { name: name.trim(), ratePerMin: rate, burst: b }));
      else {
        const k = await sysApi.createKey({ kind, name: name.trim(), ratePerMin: rate, burst: b, ttlHours: ttl });
        track("dev.key.create", { kind });
        onCreated(k);
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <View style={{ paddingBottom: 8 }}>
      {!edit ? (
        <Field label="类型" style={{ borderTopWidth: 0, paddingTop: 0 }} desc={kind === "key" ? "长期凭证，适合常驻 Agent / 服务；可选过期时间。" : "短期凭证，必须设置有效期（最长 30 天），适合临时分析或分享给他人。"}>
          <Segmented value={kind} onChange={(k) => { setKind(k); setTtl(k === "token" ? 24 : 0); }} options={[{ value: "key", label: "API Key" }, { value: "token", label: "Token" }]} />
        </Field>
      ) : null}
      <Field label="名称" error={name !== "" ? errors.name : undefined} style={edit ? { borderTopWidth: 0, paddingTop: 0 } : undefined}>
        <Input value={name} maxLength={64} placeholder="例如：research-agent" onChangeText={setName} />
      </Field>
      <Field label="频率上限（次 / 分钟）" error={errors.rate}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          <NumInput value={rate} width={90} decimal={false} onChange={setRate} />
          <Segmented small style={{ flex: 1 }} value={String(rate)} onChange={(v) => setRate(Number(v))} options={[30, 60, 120, 300, 600].map((v) => ({ value: String(v), label: String(v) }))} />
        </View>
      </Field>
      <Field label="突发容量（令牌）" desc={`留空 = ${defaultBurst}；空闲时最多可连续调用这么多次，之后按 ${rate}/分钟 匀速恢复`} error={errors.burst}>
        <NumInput value={burst} width={110} decimal={false} placeholder={String(defaultBurst)} onChange={setBurst} />
      </Field>
      {!edit ? (
        <Field label="有效期">
          <Segmented small value={String(ttl)} onChange={(v) => setTtl(Number(v))} options={ttlOptions} />
        </Field>
      ) : null}
      <View style={{ flexDirection: "row", gap: 10, marginTop: 8 }}>
        <PillButton variant="soft" label="取消" onPress={onClose} style={{ flex: 1 }} />
        <PillButton label={edit ? "保存" : "创建"} loading={saving} disabled={!valid} onPress={() => void save()} style={{ flex: 1 }} />
      </View>
      {err ? <Text style={{ color: colors.down, fontSize: fs.sm, marginTop: 8 }}>{err}</Text> : null}
    </View>
  );
}

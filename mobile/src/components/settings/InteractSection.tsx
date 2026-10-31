// 交互通道 (web components/settings/InteractSection.vue): the two-way trading channel — Telegram bot
// (notifications, confirmations, commands; allowed user ids, pushed event kinds, quiet) and an
// outgoing Webhook. Tokens are never returned: inputs start empty with the masked value as
// placeholder; saving an empty one keeps it. Saves apply at once; writes need the owner key.
import { useState } from "react";
import { Text, View } from "react-native";
import { ago } from "@/lib/fmt";
import { useQuery } from "@/lib/query";
import { eventKinds, sysApi, type InteractConfig } from "@/lib/sysApi";
import { colors, fs } from "@/lib/theme";
import { B, Field, Input, LinkText, P, Result, SectionHead, Toggle } from "../Form";
import { Card, Chip, ErrorBox, PillButton, Tag } from "../ui";

const KEY = "interact:config";
const parseUsers = (s: string) => s.split(/[\s,，]+/).filter(Boolean).map(Number);
const sameSet = (a: string[], b: string[]) => [...a].sort().join() === [...b].sort().join();

interface Form { enabled: boolean; token: string; users: string; kinds: string[]; quiet: boolean; whOn: boolean; whUrl: string; whSecret: string; whKinds: string[] }

function formOf(c: InteractConfig, all: string[]): Form {
  return {
    enabled: c.telegram.enabled, token: "", users: (c.telegram.allowUserIds || []).join(", "),
    kinds: c.kinds?.length ? [...c.kinds] : all, quiet: c.quiet,
    whOn: !!c.webhook?.enabled, whUrl: c.webhook?.url || "", whSecret: "", whKinds: c.webhook?.kinds?.length ? [...c.webhook.kinds] : all,
  };
}

export function InteractSection() {
  const q = useQuery<InteractConfig>(KEY, () => sysApi.interactConfig(), { interval: 15_000, staleMs: 10_000 });
  const cfg = q.data;
  const kindOpts = cfg?.allKinds?.length ? cfg.allKinds.map((k) => ({ value: k.kind, label: k.label })) : eventKinds;
  const allKeys = kindOpts.map((k) => k.value);
  // the form follows the server until edited; polling then only refreshes the status
  const [edit, setEdit] = useState<Form | null>(null);
  const form = edit ?? (cfg ? formOf(cfg, allKeys) : null);
  const set = (p: Partial<Form>) => form && setEdit({ ...form, ...p });
  const [busy, setBusy] = useState("");
  const [res, setRes] = useState<{ ok: boolean; t: string } | null>(null);
  const [exp, setExp] = useState(false);

  const botState = (() => {
    const s = cfg?.status;
    if (!cfg?.telegram.configured) return { t: "未配置" };
    switch (s?.state) {
      case "ok": return { t: s.bot ? "@" + s.bot.replace(/^@/, "") : "运行中", c: "good" as const };
      case "error": return { t: "出错", c: "bad" as const };
      case "starting": return { t: "启动中", c: "warn" as const };
      case "disabled": return { t: "未启用" };
      default: return { t: "已停止" };
    }
  })();

  const usersErr = form && parseUsers(form.users).some((n) => !Number.isInteger(n) || n <= 0) ? "用户 ID 是正整数，用逗号分隔" : "";
  const urlErr = form && form.whUrl.trim() && !/^https?:\/\/\S+$/.test(form.whUrl.trim()) ? "需为 http(s):// 开头的地址" : form?.whOn && !form.whUrl.trim() ? "启用前需填写地址" : "";
  const dirty = !!edit && !!cfg && JSON.stringify(edit) !== JSON.stringify(formOf(cfg, allKeys));
  const err = (e: unknown) => ({ ok: false, t: e instanceof Error ? e.message : String(e) });

  const save = async () => {
    if (!cfg || !form || usersErr || urlErr) return false;
    setBusy("save");
    setRes(null);
    try {
      await sysApi.saveInteractConfig({
        telegram: { enabled: form.enabled, botToken: form.token, configured: cfg.telegram.configured, allowUserIds: parseUsers(form.users) },
        kinds: sameSet(form.kinds, allKeys) ? [] : form.kinds, quiet: form.quiet,
        webhook: { enabled: form.whOn, url: form.whUrl.trim(), secret: form.whSecret, configured: !!cfg.webhook?.configured, kinds: sameSet(form.whKinds, allKeys) ? [] : form.whKinds },
      });
      await q.refresh();
      setEdit(null);
      setRes({ ok: true, t: "已保存" });
      return true;
    } catch (e) {
      setRes(err(e));
      return false;
    } finally {
      setBusy("");
    }
  };
  const test = async () => {
    if (dirty && !(await save())) return;
    setBusy("test");
    setRes(null);
    try {
      const r = await sysApi.interactTest();
      const bad = Object.entries(r.channels || {}).filter(([, v]) => v !== "ok" && v !== "未启用").map(([k, v]) => `${k === "telegram" ? "Telegram" : "Webhook"}：${v}`);
      setRes(r.ok ? { ok: true, t: "测试消息已发送，随后会收到一条测试确认（也会出现在“待确认”）" } : { ok: false, t: bad.join("；") || r.error || "发送失败" });
    } catch (e) {
      setRes(err(e));
    } finally {
      setBusy("");
    }
  };
  const toggleKind = (list: string[], k: string) => (list.includes(k) ? list.filter((x) => x !== k) : [...list, k]);
  const kindChips = (list: string[], on: (l: string[]) => void) => (
    <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
      {kindOpts.map((k) => <Chip key={k.value} label={k.label} active={list.includes(k.value)} onPress={() => on(toggleKind(list, k.value))} />)}
    </View>
  );

  return (
    <Card>
      <SectionHead icon="chatbubbles-outline" title="交互通道" right={cfg ? <Tag text={botState.t} tone={botState.c} /> : null} tip={<>
        <P>交易的<B>双向</B>通道：成交、止损、熔断等事件推送给你；开启人工确认的信号在 Telegram 里用按钮回答（执行 / 半仓 / 跳过），也可以用命令查看持仓、暂停策略。“交易 → 待确认”是同一套确认，任一处回答即可。</P>
        <P><B>Bot Token</B>：在 @BotFather 新建一个<B>专用</B>机器人得到。不要和“监控”的推送机器人共用：这里会持续轮询消息，同一个 Token 只能有一个轮询者。</P>
        <P><B>允许的用户 ID</B>：只有这些 Telegram 用户能收到消息、确认和下命令，其他人的消息一律忽略。不知道 ID：保存 Token 并启用后，在私聊里给机器人发 /start，会出现在下方“陌生人”里，一键加入。</P>
        <P><B>推送的事件</B>：未选的事件只记录不推送；需要确认的信号无论如何都会推送。<B>静音</B>：除确认、止损、强平、熔断外，消息不响铃。</P>
        <P>Token 只保存在服务器数据库，页面只显示末 4 位；留空保存 = 不修改。</P>
      </>} />
      {!cfg || !form ? (
        q.error ? <ErrorBox text={"读取失败：" + q.error} onRetry={q.refresh} /> : <Text style={{ color: colors.textFaint, fontSize: fs.sm, paddingVertical: 8 }}>读取中…</Text>
      ) : (
        <>
          <Field label="启用 Telegram" changed={form.enabled !== cfg.telegram.enabled} right={<Toggle value={form.enabled} onChange={(v) => set({ enabled: v })} />}
            desc={cfg.status?.state === "error" && cfg.status.lastError ? <Text style={{ color: colors.down }}>{cfg.status.lastError}</Text>
              : cfg.status?.lastPollAt ? `最近轮询 ${ago(cfg.status.lastPollAt)}前 · 接收人 ${cfg.status.recipients}${cfg.status.backlog ? ` · 待发 ${cfg.status.backlog}` : ""}`
              : "关闭后不推送也不接收命令，App 与网页确认照常"} />
          <Field label="Telegram Bot Token" desc="@BotFather 新建的专用机器人" changed={!!form.token}>
            <Input value={form.token} secureTextEntry mono onChangeText={(v) => set({ token: v })} placeholder={cfg.telegram.botToken ? `已保存 ${cfg.telegram.botToken}，留空不变` : "123456789:AA…"} />
          </Field>
          <Field label="允许的用户 ID" desc="只有这些 Telegram 用户能确认和下命令（最多 10 个）" error={usersErr}>
            <Input value={form.users} keyboardType="numbers-and-punctuation" onChangeText={(v) => set({ users: v })} placeholder="123456789, 987654321" />
            {cfg.status?.strangers?.length ? (
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
                <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>陌生人：</Text>
                {cfg.status.strangers.map((s) => (
                  <Chip key={s.id} icon="person-add-outline" label={`${s.name || s.id} ${s.id}`} onPress={() => {
                    const ids = parseUsers(form.users);
                    if (!ids.includes(s.id)) set({ users: [...ids, s.id].join(", ") });
                  }} />
                ))}
              </View>
            ) : null}
          </Field>
          <Field label="推送的事件" desc="未选的只记录不推送" error={form.kinds.length ? "" : "至少选择一种（确认消息总会推送）"} right={<LinkText label="全选" onPress={() => set({ kinds: allKeys })} />}>
            {kindChips(form.kinds, (l) => set({ kinds: l }))}
          </Field>
          <Field label="静音" desc="除确认、止损、强平、熔断外不响铃" right={<Toggle value={form.quiet} onChange={(v) => set({ quiet: v })} />} />
          <Field label="Webhook" desc={`把事件以 JSON POST 到你的地址（策略的 ctx.notify 也走这里）${cfg.webhook?.backlog ? ` · 待发 ${cfg.webhook.backlog}` : ""}`} right={<Toggle value={form.whOn} onChange={(v) => { set({ whOn: v }); if (v) setExp(true); }} />} />
          {form.whOn || exp ? (
            <>
              <Field label="Webhook 地址" error={urlErr}>
                <Input value={form.whUrl} keyboardType="url" onChangeText={(v) => set({ whUrl: v })} placeholder="https://example.com/hook" />
              </Field>
              <Field label="签名密钥" desc="请求头 X-CTG-Signature = sha256=HMAC(密钥, 请求体)；留空不签名">
                <Input value={form.whSecret} secureTextEntry onChangeText={(v) => set({ whSecret: v })} placeholder={cfg.webhook?.configured ? `已保存 ${cfg.webhook.secret}，留空不变` : "可选"} />
              </Field>
              <Field label="Webhook 推送的事件" desc="可以和 Telegram 不同，例如只推「策略消息」" error={form.whKinds.length ? "" : "至少选择一种"} right={<LinkText label="全选" onPress={() => set({ whKinds: allKeys })} />}>
                {kindChips(form.whKinds, (l) => set({ whKinds: l }))}
              </Field>
            </>
          ) : null}
        </>
      )}
      <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap", paddingTop: 10, borderTopWidth: 1, borderColor: colors.border }}>
        <PillButton small label="保存" loading={busy === "save"} disabled={!!busy || !dirty || !!usersErr || !!urlErr || !form?.kinds.length || !form?.whKinds.length} onPress={() => void save()} />
        <PillButton small variant="soft" label="发送测试" loading={busy === "test"} disabled={!!busy || (!cfg?.telegram.configured && !form?.token && !form?.whUrl)} onPress={() => void test()} />
        {dirty ? <PillButton small variant="ghost" label="撤销" disabled={!!busy} onPress={() => { setEdit(null); setRes(null); }} /> : null}
      </View>
      <View style={{ marginTop: res ? 8 : 0 }}><Result r={res} /></View>
    </Card>
  );
}

// AI 授权 (web components/settings/AiAuthSection.vue, server internal/aigate/http.go): the Claude
// credential for AI review (subscription token or API key), worker/proxy state, model, test, revoke.
// Writes need the owner key (api.ts asks for it).
import { useState, type ReactNode } from "react";
import { Text, View } from "react-native";
import { useRouter } from "expo-router";
import { ago } from "@/lib/fmt";
import { mutate, useQuery } from "@/lib/query";
import { aiState, sysApi, type AiStatus } from "@/lib/sysApi";
import { colors, fs, tnum } from "@/lib/theme";
import { B, Code, Field, Input, LinkText, P, Result, SectionHead } from "../Form";
import { Card, Dot, ErrorBox, PillButton, Segmented, Tag, confirm } from "../ui";

const KEY = "ai:status";

export function AiAuthSection() {
  const router = useRouter();
  const q = useQuery<AiStatus>(KEY, () => sysApi.aiStatus(), { interval: 15_000, staleMs: 10_000 });
  const st = q.data;
  const [kind, setKind] = useState<"api_key" | "oauth_token" | "">("");
  const k = kind || st?.kind || "oauth_token";
  const [value, setValue] = useState("");
  const [model, setModel] = useState("");
  const [busy, setBusy] = useState("");
  const [res, setRes] = useState<{ ok: boolean; t: string } | null>(null);
  const state = st ? aiState(st) : null;
  const put = (s: AiStatus) => mutate<AiStatus>(KEY, () => s);
  const err = (e: unknown) => ({ ok: false, t: e instanceof Error ? e.message : String(e) });

  const modelErr = model && !/^[a-z0-9][a-z0-9.-]{1,62}(\[1m\])?$/.test(model.trim()) ? "模型名只能含小写字母、数字、点和横线" : "";
  const v = value.trim();
  const fmtErr = !v ? "" : k === "api_key" && !v.startsWith("sk-ant-api") ? "API Key 通常以 sk-ant-api 开头" : k === "oauth_token" && !v.startsWith("sk-ant-oat") ? "订阅 Token 通常以 sk-ant-oat 开头" : "";
  const placeholder = st?.configured && st.kind === k && st.masked ? `已保存 ${st.masked}，留空不变` : k === "api_key" ? "sk-ant-api03-…" : "sk-ant-oat01-…";

  const test = async () => {
    setBusy("test");
    setRes(null);
    try {
      const r = await sysApi.aiTest();
      setRes(r.ok
        ? { ok: true, t: `验证通过${r.model ? " · " + r.model : ""}${r.latencyMs ? ` · ${(r.latencyMs / 1000).toFixed(1)}s` : ""}${r.costUsd ? ` · $${r.costUsd.toFixed(4)}` : ""}${r.reply ? ` · 回复：${r.reply.slice(0, 60)}` : ""}` }
        : { ok: false, t: r.error || "验证失败" });
    } catch (e) {
      setRes(err(e));
    } finally {
      setBusy("");
      void q.refresh();
    }
  };
  const save = async () => {
    if (!v) return;
    setBusy("save");
    setRes(null);
    try {
      put(await sysApi.aiSetCredential(k, v));
      setValue("");
      setRes({ ok: true, t: "已保存，正在验证…" });
      await test();
    } catch (e) {
      setRes(err(e));
      setBusy("");
    }
  };
  const saveModel = async () => {
    if (modelErr || !model.trim()) return;
    setBusy("model");
    setRes(null);
    try {
      const s = await sysApi.aiSetModel(model.trim());
      put(s);
      setModel("");
      setRes({ ok: true, t: `模型已设为 ${s.model}` });
    } catch (e) {
      setRes(err(e));
    } finally {
      setBusy("");
    }
  };
  const revoke = () =>
    confirm("撤销 AI 凭证", "撤销后“AI 审核”模式的信号都会被跳过，直到重新授权。", "撤销", async () => {
      setBusy("revoke");
      try {
        put(await sysApi.aiRevoke());
        setRes({ ok: true, t: "已撤销" });
      } catch (e) {
        setRes(err(e));
      } finally {
        setBusy("");
      }
    }, true);

  return (
    <Card>
      <SectionHead icon="sparkles-outline" title="AI 授权" right={state ? <Tag text={state.t} tone={state.c} /> : null} tip={<>
        <P>“AI 审核”执行方式需要一个 Claude 凭证，二选一：</P>
        <P><B>订阅 Token</B>：在<B>你自己的电脑</B>上安装 Claude Code 后运行 <Code>claude setup-token</Code>，按提示在浏览器登录你的 Claude 订阅，终端会打印一个长期有效的 Token（<Code>sk-ant-oat…</Code>），粘贴到这里。用量计入你的订阅额度。</P>
        <P><B>Anthropic API Key</B>：在 console.anthropic.com 创建（<Code>sk-ant-api…</Code>），按调用量计费。</P>
        <P>本应用<B>不提供</B> claude.ai 登录，也不会经手你的账号密码；凭证只保存在服务器数据库，页面只显示末 4 位，只交给隔离的 AI 进程使用。</P>
        <P><B>AI 进程</B>：独立运行的 AI 审核进程（Claude Agent SDK）是否在线并就绪；离线时 AI 审核的信号按“跳过”处理。</P>
        <P><B>代理</B>：AI 进程只经服务器代理访问 Anthropic。代理不可用时 AI 审核<B>暂停</B>（信号按跳过处理），<B>绝不直连</B>。</P>
        <P><B>费用</B>：按 API 价格计算；用订阅 Token 时是估算，实际计入订阅额度。</P>
        <P><B>模型</B>：留空恢复服务器默认。</P>
      </>} />
      {!st ? (
        q.error ? <ErrorBox text={"读取失败：" + q.error} onRetry={q.refresh} /> : <Text style={{ color: colors.textFaint, fontSize: fs.sm, paddingVertical: 8 }}>读取中…</Text>
      ) : (
        <>
          <Field label="状态" desc={st.configured ? `${st.kind === "api_key" ? "Anthropic API Key" : "订阅 Token"} ${st.masked ?? ""}` : "尚未授权"}>
            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, columnGap: 14 }}>
              <Stat dot={!st.workerOnline ? "bad" : st.workerReady ? "ok" : "warn"} text={`AI 进程${!st.workerOnline ? "离线" : st.workerReady ? "就绪" : "未就绪"}`} />
              <Stat dot={st.workerOnline ? (st.proxyOk ? "ok" : "bad") : "off"} text={`代理${!st.workerOnline ? "未知" : st.proxyOk ? "正常" : "不可用"}`} />
              <Stat text={<>最近调用 <Text style={{ color: colors.text, fontWeight: "600" }}>{st.lastCallAt ? ago(st.lastCallAt) + "前" : "—"}</Text></>} />
              <Stat text={<>今日 <Text style={{ color: colors.text, fontWeight: "600" }}>{st.callsToday ?? 0}</Text> 次 · <Text style={{ color: colors.text, fontWeight: "600" }}>${(st.costToday ?? 0).toFixed(2)}</Text></>} />
            </View>
            {st.worker ? <Text style={{ fontSize: fs.xs, color: colors.textDim }}>worker {st.worker.version}{st.worker.sdk ? " · SDK " + st.worker.sdk : ""}{st.worker.cli ? " · CLI " + st.worker.cli : ""}</Text> : null}
            {st.pendingProposals ? <LinkText label={`${st.pendingProposals} 条参数建议待批 →`} onPress={() => router.push("/trade")} /> : null}
            {st.workerOnline && !st.proxyOk ? <Warn t={`代理不可用，AI 审核已暂停（不会直连）${st.proxyError ? "：" + st.proxyError : ""}`} /> : null}
            {st.credentialError ? <Warn t={`凭证问题：${st.credentialError}`} /> : null}
            {st.lastError ? <Warn t={`最近一次调用失败：${st.lastError}`} /> : null}
            {st.storage === "plain" ? <Warn t="凭证未加密保存（服务器未设置 CTG_SECRET_KEY）" warn /> : null}
          </Field>
          <Field label="模型" desc={`当前 ${st.model || "—"}`} changed={!!model} error={modelErr}>
            <View style={{ flexDirection: "row", gap: 8 }}>
              <Input value={model} onChangeText={setModel} placeholder={st.model || "claude-…"} mono style={{ flex: 1 }} onSubmitEditing={() => void saveModel()} />
              <PillButton small variant="soft" label="保存" loading={busy === "model"} disabled={!!busy || !!modelErr || !model.trim()} onPress={() => void saveModel()} />
            </View>
          </Field>
          <Field label="凭证类型">
            <Segmented small value={k} onChange={setKind} options={[{ value: "oauth_token", label: "订阅 Token" }, { value: "api_key", label: "Anthropic API Key" }]} />
          </Field>
          <Field label={k === "api_key" ? "API Key" : "订阅 Token"} desc={k === "oauth_token" ? "在自己电脑上运行 claude setup-token 获得" : "console.anthropic.com → API Keys"} changed={!!value} error={fmtErr}>
            <Input value={value} onChangeText={setValue} secureTextEntry placeholder={placeholder} mono onSubmitEditing={() => void save()} />
          </Field>
        </>
      )}
      <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap", paddingTop: 10, borderTopWidth: 1, borderColor: colors.border }}>
        <PillButton small label="保存并验证" loading={busy === "save"} disabled={!!busy || !v} onPress={() => void save()} />
        <PillButton small variant="soft" label="测试" loading={busy === "test"} disabled={!!busy || !st?.configured} onPress={() => void test()} />
        {st?.configured ? <PillButton small variant="ghost" label="撤销" disabled={!!busy} onPress={revoke} /> : null}
      </View>
      <View style={{ marginTop: res ? 8 : 0 }}><Result r={res} /></View>
    </Card>
  );
}

function Stat({ dot, text }: { dot?: "ok" | "bad" | "warn" | "off"; text: ReactNode }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
      {dot ? <Dot tone={dot} /> : null}
      <Text style={[{ fontSize: fs.sm, color: colors.textSoft }, tnum]}>{text}</Text>
    </View>
  );
}

const Warn = ({ t, warn }: { t: string; warn?: boolean }) => <Text style={{ fontSize: fs.xs, lineHeight: 16, color: warn ? colors.warn : colors.down }}>{t}</Text>;

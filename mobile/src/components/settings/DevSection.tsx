// 开发者 (web Settings.vue #dev): open the external read-only API / MCP and its server-wide rate
// limit (applies at once); keys are managed on the Dev page.
import { useState } from "react";
import { View } from "react-native";
import { useRouter } from "expo-router";
import { mutate, useQuery } from "@/lib/query";
import { sysApi, type DevConfig } from "@/lib/sysApi";
import { B, Code, Field, LinkText, NumInput, P, SectionHead, Toggle } from "../Form";
import { Card, Segmented, toastError } from "../ui";

export const DEV_CONFIG = "dev:config";

export function DevSection() {
  const router = useRouter();
  const q = useQuery<DevConfig>(DEV_CONFIG, () => sysApi.devConfig(), { staleMs: 30_000 });
  const dev = q.data;
  const [rate, setRate] = useState<number | null>(null);
  const r = rate ?? dev?.globalPerMin ?? 0;
  const rateErr = !Number.isInteger(r) || r < 10 || r > 60000 ? "请输入 10–60000 的整数" : "";

  const saveDev = async (patch: Partial<DevConfig>) => {
    if (!dev) return;
    try {
      const c = await sysApi.saveDevConfig({ ...dev, ...patch });
      mutate<DevConfig>(DEV_CONFIG, () => c);
      setRate(null);
    } catch (e) {
      toastError("保存失败", e);
    }
  };
  const commitRate = (v: number) => {
    setRate(v);
    if (dev && Number.isInteger(v) && v >= 10 && v <= 60000 && v !== dev.globalPerMin) void saveDev({ globalPerMin: v });
  };

  return (
    <Card>
      <SectionHead icon="code-slash-outline" title="开发者" right={<LinkText label="管理凭证 →" onPress={() => router.push("/dev")} />} tip={<>
        <P>给外部 Agent 的只读数据接口：REST（<Code>/api/open/v1</Code>）与 MCP 服务（<Code>/mcp</Code>），用 API Key / Token 认证、按凭证限流。凭证在 Dev 页里创建和管理。</P>
        <P><B>全服务器上限</B>：所有凭证加起来每分钟最多调用多少次（令牌桶，突发 = 1/6），用来保护行情记录不被外部查询拖慢；单个凭证的频率在创建时设置。</P>
        <P>开关与上限改完立即生效。</P>
      </>} />
      <Field label="开放外部 API 与 MCP" desc="关闭后所有凭证的调用返回 503，凭证本身保留" right={dev ? <Toggle value={dev.enabled} onChange={(v) => void saveDev({ enabled: v })} /> : undefined} />
      <Field label="全服务器频率上限" desc="所有凭证合计，次/分钟" error={dev ? rateErr : undefined}>
        {dev ? (
          <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
            <Segmented small style={{ flex: 1 }} value={String(r)} onChange={(v) => commitRate(Number(v))} options={[300, 600, 1200, 3000].map((v) => ({ value: String(v), label: String(v) }))} />
            <NumInput value={r} unit="次/分" width={110} decimal={false} onChange={setRate} invalid={!!rateErr} />
          </View>
        ) : null}
        {dev && rate != null && !rateErr && rate !== dev.globalPerMin ? <LinkText label={`保存为 ${rate} 次/分`} onPress={() => commitRate(rate)} /> : null}
      </Field>
    </Card>
  );
}

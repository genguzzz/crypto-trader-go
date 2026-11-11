// 帮助 → API 参考 (web views/help/ApiRef.vue): the strategy SDK reference, generated from the Python
// SDK (web/src/docs/sdk.json → src/content/sdkRef.ts via scripts/gen-docs.mjs).
import { Text, View } from "react-native";
import { sdkRef } from "@/content/sdkRef";
import { colors, fs } from "@/lib/theme";
import { mono } from "./Markdown";

export const API_ARTICLE = { slug: "api", title: "API 参考", summary: "所有类、方法、字段（从 SDK 源码生成）" };

const GROUPS: { key: string; title: string; note: string }[] = [
  { key: "Strategy", title: "Strategy：策略类", note: "类属性与可重写的回调（handler）。" },
  { key: "params", title: "参数类型", note: "写成类属性，实例表单按它们生成。" },
  { key: "Signal", title: "Signal：信号", note: "回调返回的东西（或 ctx.submit 发出）。" },
  { key: "ctx", title: "ctx：数据与工具", note: "每个回调的第一个参数。" },
  { key: "data", title: "数据对象", note: "Bar / Bars / Series 的字段。" },
  { key: "ta", title: "ta：指标", note: "numpy 数组进，等长数组出；预热期为 NaN。" },
  { key: "portfolio", title: "组合工具", note: "多币种策略的观察名单和名额分配。" },
];
const KIND: Record<string, string> = { attr: "属性", handler: "回调", method: "方法", function: "函数", class: "类" };
const prefix = (group: string, kind: string) => (group === "ctx" && kind !== "class" ? "ctx." : group === "Signal" ? "Signal." : group === "ta" ? "ta." : "");

export function ApiRef() {
  return (
    <View>
      <Text style={{ fontSize: 26, lineHeight: 35, fontWeight: "700", color: colors.text, marginBottom: 8 }}>API 参考</Text>
      <Text style={{ fontSize: 15, lineHeight: 25, color: colors.textSoft }}>本页由 SDK 源码自动生成（签名和英文说明来自代码），和编辑器的自动补全是同一份数据。</Text>
      {GROUPS.map((g) => (
        <View key={g.key}>
          <View style={{ marginTop: 26, marginBottom: 6, paddingBottom: 6, borderBottomWidth: 1, borderColor: colors.border }}>
            <Text style={{ fontSize: 20, lineHeight: 27, fontWeight: "700", color: colors.text }}>{g.title}</Text>
          </View>
          <Text style={{ fontSize: fs.md, color: colors.textFaint, marginBottom: 2 }}>{g.note}</Text>
          {sdkRef.filter((e) => e.group === g.key).map((e) => (
            <View key={e.group + e.name} style={{ marginTop: 12, gap: 3 }}>
              <View style={{ flexDirection: "row", alignItems: "center", flexWrap: "wrap", gap: 8 }}>
                <Text selectable style={{ flexShrink: 1, fontFamily: mono, fontSize: 13, lineHeight: 19, fontWeight: "600", color: colors.text }}>
                  {prefix(e.group, e.kind)}{e.name}{e.kind === "attr" ? null : <Text style={{ fontWeight: "400", color: colors.textFaint }}>{e.sig}</Text>}
                </Text>
                <View style={{ paddingHorizontal: 4, borderRadius: 4, borderWidth: 1, borderColor: colors.borderStrong }}>
                  <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{KIND[e.kind] ?? e.kind}</Text>
                </View>
              </View>
              <Text selectable style={{ fontSize: fs.md, lineHeight: 20, color: colors.textSoft }}>{e.doc || "—"}</Text>
            </View>
          ))}
        </View>
      ))}
    </View>
  );
}

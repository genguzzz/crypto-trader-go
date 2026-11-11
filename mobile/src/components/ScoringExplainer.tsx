// How rule scores are computed (web components/ScoringExplainer.vue), for info sheets.
import { Text, View } from "react-native";
import type { ReactNode } from "react";
import { colors, fs } from "@/lib/theme";
import { B, Code, H4, P } from "./Form";

const Li = ({ n, children }: { n?: number; children: ReactNode }) => (
  <View style={{ flexDirection: "row", gap: 6 }}>
    <Text style={{ width: 16, fontSize: fs.lg, lineHeight: 23, color: n ? colors.textFaint : colors.primary, textAlign: "right" }}>{n ? `${n}.` : "•"}</Text>
    <P style={{ flex: 1 }}>{children}</P>
  </View>
);

export function ScoringExplainer() {
  return (
    <View style={{ gap: 6 }}>
      <H4>分数是怎么算的</H4>
      <Li n={1}><B>事实 → 分位</B>：每个字段在<B>当前活跃池</B>内排序，得到分位 p ∈ [0, 100]（并列取中间名次）。</Li>
      <Li n={2}><B>分位 → ±1</B>：x = 2·p/100 − 1。池内最高 = +1，中位 = 0，最低 = −1。</Li>
      <Li n={3}><B>加权平均</B>：<Code>分数 = 50 + 50 × Σ(wᵢ·xᵢ) ÷ Σ|wᵢ|</Code>，范围 0–100，50 为中性。</Li>
      <Li n={4}><B>多空独立</B>：多头、空头各有一套权重 w，两者不要求相加为 100；任一加权字段缺失 → 该方向“未评分”，不补 0。</Li>
      <Li n={5}><B>门槛</B>：点差等硬条件单独判断，分数再高也不能覆盖。</Li>
      <H4>例子</H4>
      <P>规则：多头 = 4h 趋势 0.45 + OI 变化 0.30 + 放量 0.25。某币三项分位 99/99/98 → x ≈ 0.98/0.98/0.96 → Σwx ≈ 0.975 → 多头 ≈ 50 + 50×0.975 = <B>98.7</B>。</P>
      <H4>数学背景</H4>
      <Li><B>横截面排名标准化</B>：只比较“同一时刻池内谁更强”，对极端值稳健（×450 放量和 ×5 放量都只是第 1 名），代价是丢失幅度信息。</Li>
      <Li><B>线性多因子打分</B>：因子投资常用的“标准化后加权合成”（参见 Grinold & Kahn《Active Portfolio Management》）。</Li>
      <Li><B>检验用 Rank IC</B>：分数排名与未来 h 小时收益排名的 Spearman 相关；看均值、t 值（按周期不重叠抽样）与扣手续费/点差后的前 20% 收益。多次试参数会产生假阳性（Harvey, Liu & Zhu 2016；Bailey 等 2014 回测过拟合）。</Li>
      <P style={{ color: colors.warn, marginTop: 6 }}>分数只是“候选排序”，不是胜率，也不是下单建议。权重是你设定的，是否有效只看“规则与战绩”页的样本外记录。</P>
    </View>
  );
}

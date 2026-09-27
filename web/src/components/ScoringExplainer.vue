<template>
  <div class="ex">
    <h4>分数是怎么算的</h4>
    <ol>
      <li><b>事实 → 分位</b>：每个字段在<b>当前活跃池</b>内排序，得到分位 p ∈ [0, 100]（并列取中间名次）。</li>
      <li><b>分位 → ±1</b>：x = 2·p/100 − 1。池内最高 = +1，中位 = 0，最低 = −1。</li>
      <li><b>加权平均</b>：<code>分数 = 50 + 50 × Σ(wᵢ·xᵢ) ÷ Σ|wᵢ|</code>，范围 0–100，50 为中性。</li>
      <li><b>多空独立</b>：多头、空头各有一套权重 w，两者不要求相加为 100；任一加权字段缺失 → 该方向“未评分”，不补 0。</li>
      <li><b>门槛</b>：点差等硬条件单独判断，分数再高也不能覆盖。</li>
    </ol>
    <h4>例子</h4>
    <p>规则：多头 = 4h 趋势 0.45 + OI 变化 0.30 + 放量 0.25。某币三项分位 99/99/98 → x ≈ 0.98/0.98/0.96 → Σwx ≈ 0.975 → 多头 ≈ 50 + 50×0.975 = <b>98.7</b>。</p>
    <h4>数学背景</h4>
    <ul>
      <li><b>横截面排名标准化</b>：只比较“同一时刻池内谁更强”，对极端值稳健（×450 放量和 ×5 放量都只是第 1 名），代价是丢失幅度信息。</li>
      <li><b>线性多因子打分</b>：因子投资常用的“标准化后加权合成”（参见 Grinold & Kahn《Active Portfolio Management》）。</li>
      <li><b>检验用 Rank IC</b>：分数排名与未来 h 小时收益排名的 Spearman 相关；看均值、t 值（按周期不重叠抽样）与扣手续费/点差后的前 20% 收益。多次试参数会产生假阳性（Harvey, Liu & Zhu 2016；Bailey 等 2014 回测过拟合）。</li>
    </ul>
    <p class="warn">分数只是“候选排序”，不是胜率，也不是下单建议。权重是你设定的，是否有效只看“规则与战绩”页的样本外记录。</p>
  </div>
</template>

<style scoped>
.ex h4 { margin: 10px 0 4px; font-size: var(--fs-md); color: var(--text-1); }
.ex h4:first-child { margin-top: 0; }
.ex ol, .ex ul { margin: 0; padding-left: 18px; }
.ex li { margin: 2px 0; }
.ex p { margin: 4px 0; }
.ex b { color: var(--text-1); }
.ex code { background: var(--bg-3); padding: 1px 6px; border-radius: 4px; color: var(--text-1); font-size: var(--fs-sm); }
.warn { margin-top: 10px !important; color: var(--warn); }
</style>

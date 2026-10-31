import { Placeholder } from "@/components/Placeholder";
import { Screen, TabHeader } from "@/components/ui";

export default function TradeTab() {
  return (
    <Screen>
      <TabHeader title="交易" />
      <Placeholder tab icon="trending-up-outline" title="模拟交易" intro="只做模拟（纸面）交易，不连接真实账户、不下真实订单。" webPath="/trading"
        items={["模拟账户：权益曲线、持仓、成交与委托", "策略实例：启动/暂停、信号与待确认提示（推送到本机通知）", "回测报告与策略编辑（策略代码在网页版编辑）", "AI 审核记录"]} />
    </Screen>
  );
}

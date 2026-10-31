import { Placeholder } from "@/components/Placeholder";
import { Screen, TopBar } from "@/components/ui";

export default function Page() {
  return (
    <Screen>
      <TopBar title="监控告警" />
      <Placeholder icon="notifications-outline" title="监控告警" intro="价格异动、评分、资金费等规则每个已确认分钟检查一次，触发后推送。" webPath="/alerts"
        items={["告警规则：新建、启停、冷却时间、作用范围（活跃池 / 自选 / 单个合约）", "触发历史与投递状态", "Telegram / Webhook 通道设置；本机通知"]} />
    </Screen>
  );
}

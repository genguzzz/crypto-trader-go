import { Placeholder } from "@/components/Placeholder";
import { Screen, TopBar } from "@/components/ui";

export default function Page() {
  return (
    <Screen>
      <TopBar title="系统状态" />
      <Placeholder icon="pulse-outline" title="系统状态" intro="录制与计算是否正常：阶段、覆盖率、连接、存储、回放与回填进度。" webPath="/status"
        items={["阶段与覆盖率、WebSocket 连接、REST 用量", "存储占用与分层保留", "回放、1m 回填、1H 历史与逐笔回补进度"]} />
    </Screen>
  );
}

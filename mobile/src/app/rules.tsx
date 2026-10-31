import { Placeholder } from "@/components/Placeholder";
import { Screen, TopBar } from "@/components/ui";

export default function Page() {
  return (
    <Screen>
      <TopBar title="规则与战绩" />
      <Placeholder icon="flask-outline" title="规则与战绩" intro="编辑多空权重与点差门槛，看每条规则在样本外的真实表现。" webPath="/rules"
        items={["规则列表：对照规则与我的规则，每次保存生成新版本", "战绩：1h/4h/24h Rank IC、t 值、前 20% 超额与扣成本收益，14 天回放 + 上线后记录", "权重编辑（需要所有者密钥）"]} />
    </Screen>
  );
}

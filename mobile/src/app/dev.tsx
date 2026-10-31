import { Placeholder } from "@/components/Placeholder";
import { Screen, TopBar } from "@/components/ui";

export default function Page() {
  return (
    <Screen>
      <TopBar title="Dev" />
      <Placeholder icon="code-slash-outline" title="Dev" intro="对外开放的 REST / MCP 接口：密钥、限流和调用记录。" webPath="/dev"
        items={["API 密钥与令牌：创建、限流、吊销", "最近 200 次外部调用", "接口列表与请求测试"]} />
    </Screen>
  );
}

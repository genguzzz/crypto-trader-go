import { Placeholder } from "@/components/Placeholder";
import { Screen, TabHeader } from "@/components/ui";

export default function AiTab() {
  return (
    <Screen>
      <TabHeader title="AI" />
      <Placeholder tab icon="sparkles-outline" title="AI 助手" intro="和网页版右下角的 AI 一样：问行情、问规则、让它分析某个合约，回答会引用本机的数据。"
        items={["对话列表与流式回答（运行中的回答切到后台也继续）", "按住说话：录音转文字后发送（麦克风权限已在安装包里）", "在合约详情页一键带上当前合约作为上下文", "AI 记忆、笔记与模型设置（需要所有者密钥）"]} />
    </Screen>
  );
}

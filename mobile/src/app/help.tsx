import { Placeholder } from "@/components/Placeholder";
import { Screen, TopBar } from "@/components/ui";

export default function Page() {
  return (
    <Screen>
      <TopBar title="帮助" />
      <Placeholder icon="book-outline" title="帮助" intro="字段、评分、图表和策略的说明文章，与网页版同一份内容。" webPath="/help"
        items={["使用入门、评分原理、各图表怎么读", "策略编写与回测说明"]} />
    </Screen>
  );
}

// 自选: the scanner's rows that are on the watchlist.
import { ScannerList } from "@/components/ScannerList";
import { Screen, TabHeader } from "@/components/ui";

export default function WatchTab() {
  return (
    <Screen>
      <TabHeader title="自选" sub="自选合约实时录制，数据保留更久" />
      <ScannerList watchOnly />
    </Screen>
  );
}

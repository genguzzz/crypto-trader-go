// Coin icon through the backend's icon proxy (/api/v1/icons/<ccy>.png); a coloured initial when missing.
import { useState } from "react";
import { Text, View } from "react-native";
import { Image } from "expo-image";
import { API_ORIGIN, WEB_ORIGIN } from "@/lib/api";

export function CoinIcon({ instId, size = 18 }: { instId: string; size?: number }) {
  const ccy = instId.split("-")[0];
  const [failed, setFailed] = useState<string | null>(null);
  const hue = [...ccy].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 360, 7);
  if (failed === ccy) {
    return (
      <View style={{ width: size, height: size, borderRadius: size / 2, backgroundColor: `hsl(${hue},45%,35%)`, alignItems: "center", justifyContent: "center" }}>
        <Text style={{ color: "#fff", fontWeight: "700", fontSize: size * 0.42 }}>{ccy.slice(0, 2)}</Text>
      </View>
    );
  }
  return (
    <Image
      source={{ uri: `${API_ORIGIN || WEB_ORIGIN}/api/v1/icons/${ccy.toLowerCase()}.png` }}
      style={{ width: size, height: size, borderRadius: size / 2, backgroundColor: "#222" }}
      cachePolicy="disk"
      recyclingKey={ccy}
      onError={() => setFailed(ccy)}
    />
  );
}

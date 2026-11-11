// Rule score chip (web ScoreChip.vue): above 50 the side colour with strength-scaled tint, else quiet.
import { Text } from "react-native";
import { colors, fs, tnum } from "@/lib/theme";

export function ScoreChip({ v, side, size = "md" }: { v: number | null | undefined; side: "long" | "short"; size?: "md" | "sm" }) {
  let color = colors.textFaint;
  let bg = "transparent";
  if (v != null && v < 50) bg = colors.raised;
  if (v != null && v >= 50) {
    const a = (v - 50) / 50;
    const rgb = side === "long" ? "14,203,129" : "246,70,93";
    color = a > 0.55 ? "#fff" : side === "long" ? colors.up : colors.down;
    bg = `rgba(${rgb},${(0.1 + a * 0.75).toFixed(2)})`;
  }
  const h = size === "sm" ? 20 : 22;
  return (
    <Text style={[{ minWidth: size === "sm" ? 30 : 34, height: h, lineHeight: h, textAlign: "center", borderRadius: 4, overflow: "hidden", paddingHorizontal: 5, fontWeight: "700", fontSize: size === "sm" ? fs.sm : fs.md, color, backgroundColor: bg }, tnum]}>
      {v == null ? "—" : v.toFixed(0)}
    </Text>
  );
}

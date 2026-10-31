// Live voice waveform (hold-to-talk capsule, composer mic): bars mirrored around the centre whose
// heights follow the input level, tallest in the middle, each bar springing to its new height with
// its own wobble so speech reads as a lively wave rather than a scrolling bar strip. Heights animate
// as scaleY on the native thread.
import { useEffect, useState } from "react";
import { Animated, Easing, View } from "react-native";
import { LinearGradient } from "expo-linear-gradient";
import { colors } from "@/lib/theme";

export function VoiceWave({ level, bars = 27, height = 40, barWidth = 3.5, gap = 3, danger, active = true }: {
  level: number; bars?: number; height?: number; barWidth?: number; gap?: number; danger?: boolean; active?: boolean;
}) {
  const [vals] = useState(() => Array.from({ length: bars }, () => new Animated.Value(0.08)));
  // bell-shaped envelope: centre bars swing most, edges a little
  const [env] = useState(() => Array.from({ length: bars }, (_, i) => {
    const x = (i - (bars - 1) / 2) / ((bars - 1) / 2);
    return 0.28 + 0.72 * Math.exp(-x * x * 2.2);
  }));
  // idle shimmer so the wave never looks dead between words
  const [t, setT] = useState(0);
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => setT((n) => n + 1), 90);
    return () => clearInterval(id);
  }, [active]);
  useEffect(() => {
    const anims = vals.map((v, i) => {
      const wobble = 0.55 + 0.45 * Math.abs(Math.sin(t * 0.9 + i * 1.7) * Math.cos(t * 0.37 + i * 0.6));
      const floor = 0.08 + 0.05 * Math.abs(Math.sin(t * 0.5 + i * 0.8));
      const to = active ? Math.max(floor, Math.min(1, level * env[i] * wobble * 1.25)) : 0.08;
      return Animated.timing(v, { toValue: to, duration: 110, easing: Easing.out(Easing.quad), useNativeDriver: true });
    });
    Animated.parallel(anims).start();
  }, [t, level, active, vals, env]);

  const from = danger ? colors.down : colors.primary;
  const to = danger ? "#ff7a59" : "#2fe0ff";
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap, height }}>
      {vals.map((v, i) => (
        <Animated.View key={i} style={{ width: barWidth, height, borderRadius: barWidth, overflow: "hidden", transform: [{ scaleY: v }] }}>
          <LinearGradient colors={[to, from, to]} style={{ flex: 1 }} />
        </Animated.View>
      ))}
    </View>
  );
}

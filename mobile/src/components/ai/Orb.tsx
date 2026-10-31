// The AI orb (web AiOrb.vue / .big-orb / .mini-orb): a rotating aurora ring around a dark core.
// Drawn with react-native-svg (a gradient circle rotated by Animated); conic gradients don't exist here.
import { useEffect, useState, type ReactNode } from "react";
import { Animated, Easing, View, type ViewProps } from "react-native";
import Svg, { Circle, Defs, LinearGradient, Stop } from "react-native-svg";

const STOPS = ["#bcff2f", "#2fe0ff", "#9b7df0", "#ff5fa2", "#f5c542"];
const RED = ["#ca3f64", "#ff7a59", "#ca3f64"];

export function Orb({ size, ring = 3, period = 5000, scale = 1, glow = 0.35, danger, children, ...rest }: { size: number; ring?: number; period?: number; scale?: number; glow?: number; danger?: boolean; children?: ReactNode } & ViewProps) {
  const [spin] = useState(() => new Animated.Value(0));
  useEffect(() => {
    spin.setValue(0);
    const l = Animated.loop(Animated.timing(spin, { toValue: 1, duration: period, easing: Easing.linear, useNativeDriver: true }));
    l.start();
    return () => l.stop();
  }, [spin, period]);
  const rotate = spin.interpolate({ inputRange: [0, 1], outputRange: ["0deg", "360deg"] });
  const stops = danger ? RED : STOPS;
  const id = `g${size}${danger ? "r" : ""}`;
  return (
    <View {...rest} style={[{ width: size, height: size, alignItems: "center", justifyContent: "center", transform: [{ scale }] }, rest.style]}>
      <Animated.View style={{ position: "absolute", width: size, height: size, borderRadius: size / 2, transform: [{ rotate }], shadowColor: danger ? "#ca3f64" : "#bcff2f", shadowOpacity: glow, shadowRadius: size / 4, shadowOffset: { width: 0, height: 0 } }}>
        <Svg width={size} height={size}>
          <Defs>
            <LinearGradient id={id} x1="0" y1="0" x2="1" y2="1">
              {stops.map((c, i) => (
                <Stop key={i} offset={i / (stops.length - 1)} stopColor={c} />
              ))}
            </LinearGradient>
          </Defs>
          <Circle cx={size / 2} cy={size / 2} r={size / 2} fill={`url(#${id})`} />
        </Svg>
      </Animated.View>
      {ring * 2 < size ? <View style={{ position: "absolute", width: size - ring * 2, height: size - ring * 2, borderRadius: size, backgroundColor: "#0b0d0a" }} /> : null}
      {children ? <View style={{ position: "absolute", top: 0, left: 0, right: 0, bottom: 0, alignItems: "center", justifyContent: "center" }}>{children}</View> : null}
    </View>
  );
}

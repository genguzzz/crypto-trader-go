// Date field + calendar sheet for yyyy-mm-dd values (the web uses <input type="date">). Pure JS — no
// native date picker module in the binary, and this ships over OTA. Days are calendar dates (the
// caller decides the zone, e.g. UTC+8 for backtests); `range` shades the span between two dates.
import { useState } from "react";
import { Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { haptic } from "@/lib/device";
import { colors, fs, radius, tnum } from "@/lib/theme";
import { IconButton, Sheet, tap } from "../ui";

const p2 = (n: number) => String(n).padStart(2, "0");
const ymd = (y: number, m: number, d: number) => `${y}-${p2(m + 1)}-${p2(d)}`; // m: 0-based
const parse = (s: string) => {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s.trim());
  return m ? { y: +m[1], m: +m[2] - 1, d: +m[3] } : null;
};
const WEEK = ["一", "二", "三", "四", "五", "六", "日"];

export function DateField({ value, onChange, title, min, max, range }: {
  value: string; onChange: (v: string) => void; title: string; min?: string; max?: string; range?: [string, string];
}) {
  const [open, setOpen] = useState(false);
  const ok = !!parse(value);
  return (
    <>
      <Pressable accessibilityLabel={title} onPress={() => { tap(); setOpen(true); }}
        style={({ pressed }) => ({ height: 38, flexDirection: "row", alignItems: "center", gap: 6, paddingHorizontal: 10, borderRadius: radius.sm, borderWidth: 1, borderColor: colors.borderStrong, backgroundColor: pressed ? colors.raised : colors.cardMuted })}>
        <Ionicons name="calendar-outline" size={15} color={colors.textFaint} />
        <Text style={[{ flex: 1, fontSize: fs.lg, color: ok ? colors.text : colors.textDim }, tnum]} numberOfLines={1}>{ok ? value : "选择日期"}</Text>
      </Pressable>
      <Sheet visible={open} onClose={() => setOpen(false)} title={title}>
        {open ? <Calendar value={value} min={min} max={max} range={range} onPick={(v) => { onChange(v); setOpen(false); }} /> : null}
      </Sheet>
    </>
  );
}

function Calendar({ value, onPick, min, max, range }: { value: string; onPick: (v: string) => void; min?: string; max?: string; range?: [string, string] }) {
  const start = parse(value) ?? parse(max ?? "") ?? (() => { const d = new Date(); return { y: d.getFullYear(), m: d.getMonth(), d: d.getDate() }; })();
  const [ym, setYm] = useState({ y: start.y, m: start.m });
  const shift = (dm: number) => { void haptic("select"); setYm(({ y, m }) => { const k = y * 12 + m + dm; return { y: Math.floor(k / 12), m: k % 12 }; }); };
  const days = new Date(Date.UTC(ym.y, ym.m + 1, 0)).getUTCDate();
  const lead = (new Date(Date.UTC(ym.y, ym.m, 1)).getUTCDay() + 6) % 7; // Monday first
  const cells: (number | null)[] = [...Array(lead).fill(null), ...Array.from({ length: days }, (_, i) => i + 1)];
  while (cells.length % 7) cells.push(null);
  const [r0, r1] = range ? [range[0] < range[1] ? range[0] : range[1], range[0] < range[1] ? range[1] : range[0]] : ["", ""];
  const today = (() => { const d = new Date(); return ymd(d.getFullYear(), d.getMonth(), d.getDate()); })();
  const canPrev = !min || ymd(ym.y, ym.m, 1) > min;
  const canNext = !max || ymd(ym.y, ym.m, days) < max;
  return (
    <View style={{ gap: 8, paddingBottom: 6 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 14 }}>
        <IconButton name="play-back-outline" size={18} label="上一年" onPress={() => shift(-12)} />
        <IconButton name="chevron-back" size={20} label="上个月" color={canPrev ? undefined : colors.textDim} onPress={() => canPrev && shift(-1)} />
        <Text style={[{ flex: 1, textAlign: "center", fontSize: fs.lg, fontWeight: "700", color: colors.text }, tnum]}>{ym.y} 年 {ym.m + 1} 月</Text>
        <IconButton name="chevron-forward" size={20} label="下个月" color={canNext ? undefined : colors.textDim} onPress={() => canNext && shift(1)} />
        <IconButton name="play-forward-outline" size={18} label="下一年" onPress={() => shift(12)} />
      </View>
      <View style={{ flexDirection: "row" }}>
        {WEEK.map((w) => <Text key={w} style={{ flex: 1, textAlign: "center", fontSize: fs.xs, color: colors.textFaint }}>{w}</Text>)}
      </View>
      <View style={{ flexDirection: "row", flexWrap: "wrap" }}>
        {cells.map((d, i) => {
          if (d == null) return <View key={`x${i}`} style={{ width: `${100 / 7}%`, height: 40 }} />;
          const s = ymd(ym.y, ym.m, d);
          const off = (!!min && s < min) || (!!max && s > max);
          const on = s === value;
          const inRange = !!r0 && s >= r0 && s <= r1;
          return (
            <View key={s} style={{ width: `${100 / 7}%`, height: 40, alignItems: "center", justifyContent: "center", backgroundColor: inRange && !on ? colors.primarySoft : "transparent" }}>
              <Pressable disabled={off} accessibilityLabel={s} onPress={() => { void haptic("select"); onPick(s); }}
                style={({ pressed }) => ({ width: 36, height: 36, borderRadius: 18, alignItems: "center", justifyContent: "center", backgroundColor: on ? colors.primary : pressed ? colors.raised : "transparent", borderWidth: s === today && !on ? 1 : 0, borderColor: colors.borderStrong })}>
                <Text style={[{ fontSize: fs.md, fontWeight: on ? "700" : "400", color: on ? colors.onPrimary : off ? colors.textDim : colors.text }, tnum]}>{d}</Text>
              </Pressable>
            </View>
          );
        })}
      </View>
    </View>
  );
}

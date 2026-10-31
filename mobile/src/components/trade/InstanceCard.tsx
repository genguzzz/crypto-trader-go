// One strategy instance (web InstanceCard.vue): state, status line, exec mode, universe, params,
// lifecycle buttons and its run log (ctx.log / ctx.notify / errors) in a sheet.
import { useState } from "react";
import { Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { ago, time } from "@/lib/fmt";
import { useQuery } from "@/lib/query";
import { tradeApi, type Instance, type InstanceAction, type LogLine, type StrategyVersion } from "@/lib/tradeApi";
import { execLabel, paramsLine, stateLabel, universeText } from "@/lib/tradeMeta";
import { colors, fs, tnum } from "@/lib/theme";
import { Sheet, Tag, tap } from "../ui";
import { LTag, MoreButton, Muted, SmallButton, mono } from "./common";

export function InstanceCard({ inst, busy, readonly, newer, ruleName, onAct, onEdit, onUpgrade, onViewCode, first }: {
  inst: Instance; busy?: boolean; readonly?: boolean; newer?: StrategyVersion; ruleName: (id: string) => string; first?: boolean;
  onAct: (a: InstanceAction) => void; onEdit: () => void; onUpgrade: (v: StrategyVersion) => void; onViewCode: (versionId: string) => void;
}) {
  const [logOpen, setLogOpen] = useState(false);
  const st = inst.state;
  return (
    <View style={{ gap: 6, paddingVertical: 10, borderTopWidth: first ? 0 : 1, borderColor: colors.border }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Text style={{ flexShrink: 1, fontWeight: "600", color: colors.text, fontSize: fs.lg }} numberOfLines={1}>{inst.name}</Text>
        <Text style={[mono, { color: colors.textFaint }]}>{inst.versionId.slice(0, 7)} · v{inst.configVersion}</Text>
        <View style={{ flex: 1 }} />
        <LTag l={stateLabel[st]} fallback={st} />
      </View>
      {inst.stateReason ? <Text style={{ fontSize: fs.xs, color: st === "faulted" ? colors.down : colors.warn }}>{inst.stateReason}</Text> : null}
      {newer && !readonly ? (
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
          <Ionicons name="arrow-up-circle-outline" size={14} color={colors.primary} />
          <Text style={{ fontSize: fs.xs, color: colors.primary }}>策略有新版本 {newer.id.slice(0, 8)}</Text>
          <Pressable hitSlop={6} onPress={() => { tap(); onViewCode(newer.id); }}><Text style={{ fontSize: fs.xs, color: colors.textSoft, textDecorationLine: "underline" }}>看代码</Text></Pressable>
          <SmallButton label="升级" onPress={() => onUpgrade(newer)} disabled={busy} />
        </View>
      ) : null}
      {inst.status ? (
        <View style={{ flexDirection: "row", gap: 6 }}>
          <Ionicons name="pulse" size={12} color={colors.primary} style={{ marginTop: 2 }} />
          <Text style={{ flex: 1, fontSize: fs.xs, color: colors.textSoft }}>{inst.status}</Text>
        </View>
      ) : null}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Tag text={execLabel[inst.exec]?.t || inst.exec} />
        <Text style={{ flex: 1, fontSize: fs.sm, color: colors.textSoft }} numberOfLines={1}>{universeText(inst.universe, ruleName)}</Text>
      </View>
      {Object.keys(inst.params || {}).length ? <Text style={[mono, { color: colors.textFaint }]}>{paramsLine(inst.params)}</Text> : null}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint, marginRight: "auto" }}>{inst.heartbeat ? `心跳 ${ago(inst.heartbeat)}前` : st === "created" ? "尚未运行" : ""}</Text>
        {!readonly ? (
          <>
            {st === "created" || st === "stopped" ? <SmallButton label="启动" icon="play" onPress={() => onAct("start")} disabled={busy} /> : null}
            {st === "paused" || st === "faulted" ? <SmallButton label="恢复" icon="refresh" onPress={() => onAct("resume")} disabled={busy} /> : null}
            {st === "running" ? <SmallButton label="暂停" icon="pause" onPress={() => onAct("pause")} disabled={busy} /> : null}
            {st === "running" || st === "paused" || st === "faulted" ? <SmallButton label="停止" icon="stop" onPress={() => onAct("stop")} disabled={busy} /> : null}
            <SmallButton label="参数" icon="create-outline" onPress={onEdit} disabled={busy} />
          </>
        ) : null}
        <SmallButton label="日志" icon="document-text-outline" onPress={() => setLogOpen(true)} />
      </View>
      <RunLogSheet inst={inst} open={logOpen} onClose={() => setLogOpen(false)} />
    </View>
  );
}

function RunLogSheet({ inst, open, onClose }: { inst: Instance; open: boolean; onClose: () => void }) {
  const q = useQuery<LogLine[]>(open ? `trade:runlog:${inst.id}:${inst.heartbeat}` : null, () => tradeApi.runLog(inst.id), { staleMs: 5000 });
  return (
    <Sheet visible={open} onClose={onClose} title={`运行日志 · ${inst.name}`} scroll>
      <Muted style={{ marginBottom: 8 }}>ctx.log、ctx.notify 和报错（服务重启后清空）</Muted>
      {q.error ? <Text style={{ color: colors.down, fontSize: fs.sm }}>{q.error}</Text> : q.data ? <RunLog lines={q.data} limit={30} empty="还没有日志：策略没调用 ctx.log / ctx.notify，或服务刚重启" /> : <Muted>加载中…</Muted>}
    </Sheet>
  );
}

/** A run log, newest first; tracebacks fold to first … last line, tap to expand (web RunLog). */
export function RunLog({ lines, limit = 50, empty = "还没有日志" }: { lines: LogLine[]; limit?: number; empty?: string }) {
  const [shown, setShown] = useState(limit);
  const [open, setOpen] = useState<Set<number>>(new Set());
  const list = lines.map((l, i) => ({ ...l, i })).reverse();
  if (!list.length) return <Muted>{empty}</Muted>;
  const first = (m: string) => {
    const ls = m.trim().split("\n");
    return ls.length > 1 ? ls[0] + " … " + ls[ls.length - 1] : ls[0];
  };
  return (
    <View style={{ gap: 2 }}>
      {list.slice(0, shown).map((l) => {
        const multi = l.msg.includes("\n");
        const c = l.level === "error" ? colors.down : l.level === "warn" ? colors.warn : l.kind === "notify" ? colors.primary : colors.textSoft;
        return (
          <Pressable
            key={l.i}
            disabled={!multi}
            onPress={() => { tap(); setOpen((s) => { const n = new Set(s); if (n.has(l.i)) n.delete(l.i); else n.add(l.i); return n; }); }}
            style={{ flexDirection: "row", gap: 8, paddingVertical: 3, paddingHorizontal: 6, borderRadius: 4, backgroundColor: l.level === "warn" ? "rgba(255,180,0,0.07)" : l.level === "error" ? "rgba(255,80,80,0.08)" : "transparent" }}
          >
            <Text style={[mono, tnum, { color: colors.textFaint }]}>{time(l.clock || l.ts, true)}</Text>
            {l.kind === "notify" ? <Ionicons name="notifications-outline" size={12} color={colors.primary} style={{ marginTop: 1 }} /> : null}
            <Text style={[mono, { flex: 1, color: c }]}>{open.has(l.i) || !multi ? l.msg : first(l.msg)}</Text>
          </Pressable>
        );
      })}
      {list.length > shown ? <MoreButton label={`再显示 ${Math.min(100, list.length - shown)} 条`} onPress={() => setShown((n) => n + 100)} /> : null}
    </View>
  );
}

// List rows of the trading pages, phone-shaped versions of the web tables: account cards,
// positions, orders, fills, ledger, signals (web AccountCard, Account.vue tables, OrdersTable,
// FillsTable, SignalList).
import { useMemo, useState, type ReactNode } from "react";
import { Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { price, time } from "@/lib/fmt";
import type { Account, Fill, Instance, LedgerEntry, Order, Position, Signal } from "@/lib/tradeApi";
import { accountStatus, actionLabel, amt, day, ledgerLabel, modeLabel, orderStateLabel, orderTypeLabel, outcomeLabel, pctf, qty, tone } from "@/lib/tradeMeta";
import { colors, fs, radius, tnum } from "@/lib/theme";
import { CoinIcon } from "../CoinIcon";
import { Card, Segmented, Tag } from "../ui";
import { LTag, MoreButton, Muted, SmallButton, Stat, toneText } from "./common";

const symOf = (id: string) => id.replace("-USDT-SWAP", "");
const rowStyle = { paddingVertical: 10, borderTopWidth: 1, borderColor: colors.border, gap: 4 } as const;

export function AccountCard({ a, running, paused, onPress }: { a: Account; running?: number; paused?: number; onPress: () => void }) {
  const st = accountStatus(a, running, paused);
  const total = a.equity - a.deposit;
  return (
    <Card onPress={onPress} style={[{ gap: 10 }, a.archived && { opacity: 0.6 }, a.halted && { borderColor: colors.downBg }]}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <LTag l={modeLabel[a.mode]} />
        <Text style={{ flex: 1, fontSize: fs.lg, fontWeight: "600", color: colors.text }} numberOfLines={1}>{a.name}</Text>
        <LTag l={st} />
        <Ionicons name="chevron-forward" size={15} color={colors.textFaint} />
      </View>
      <View style={{ gap: 2 }}>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>权益</Text>
        <Text style={[{ fontSize: fs.x2, fontWeight: "700", color: colors.text }, tnum]}>{amt(a.equity)} <Text style={{ fontSize: fs.xs, fontWeight: "500", color: colors.textFaint }}>USDT</Text></Text>
      </View>
      <View style={{ flexDirection: "row", gap: 8 }}>
        {a.mode !== "backtest" ? <Stat style={{ flex: 1 }} label="今日盈亏" value={amt(a.dayPnl, 2, true)} tone={tone(a.dayPnl)} /> : null}
        <Stat style={{ flex: 1 }} label="累计收益" value={pctf(a.deposit > 0 ? total / a.deposit : null, 2, true)} tone={tone(total)} />
        <Stat style={{ flex: 1 }} label="持仓" value={String(a.openCount)} />
        <Stat style={{ flex: 1 }} label={running != null ? "运行策略" : "策略"} value={running != null ? `${running}/${a.instances}` : String(a.instances)} />
      </View>
      <View style={{ borderTopWidth: 1, borderColor: colors.border, paddingTop: 8 }}>
        <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>
          {a.mode === "backtest" ? `${day(a.from)} → ${day(a.to ? a.to - 1 : undefined)}` : `入金 ${amt(a.deposit, 0)} USDT`}
        </Text>
      </View>
    </Card>
  );
}

const instNameOf = (insts: Instance[] | undefined, id: number) => insts?.find((i) => i.id === id)?.name || (id ? `#${id}` : "手动");

export function PositionRow({ p, insts, onClose, busy }: { p: Position; insts?: Instance[]; onClose?: () => void; busy?: boolean }) {
  const pnlPct = p.margin ? p.unrealizedPnl / p.margin : null;
  const liqDist = p.markPx && p.liqPx ? Math.abs(p.markPx - p.liqPx) / p.markPx : null;
  return (
    <View style={rowStyle}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <CoinIcon instId={p.instId} size={18} />
        <Text style={{ fontSize: fs.lg, fontWeight: "700", color: colors.text }}>{symOf(p.instId)}</Text>
        <Tag text={`${p.posSide === "long" ? "多" : "空"} ${p.leverage}x`} tone={p.posSide === "long" ? "good" : "bad"} />
        <Text style={[{ fontSize: fs.sm, color: colors.textFaint }, tnum]}>{qty(p.qty)} 张</Text>
        <View style={{ flex: 1 }} />
        <View style={{ alignItems: "flex-end" }}>
          <Text style={[{ fontSize: fs.lg, fontWeight: "700", color: toneText(tone(p.unrealizedPnl)) }, tnum]}>{amt(p.unrealizedPnl, 2, true)}</Text>
          <Text style={[{ fontSize: fs.xs, color: toneText(tone(p.unrealizedPnl)) }, tnum]}>{pctf(pnlPct, 1, true)}</Text>
        </View>
      </View>
      <View style={{ flexDirection: "row", gap: 8 }}>
        <Stat style={{ flex: 1 }} label="开仓价" value={price(p.entryPx)} />
        <Stat style={{ flex: 1 }} label="标记价" value={price(p.markPx)} />
        <Stat style={{ flex: 1 }} label={`强平价 ${pctf(liqDist, 1)}`} value={price(p.liqPx)} tone="warn" />
      </View>
      <View style={{ flexDirection: "row", gap: 8, alignItems: "flex-end" }}>
        <View style={{ flex: 1, gap: 1 }}>
          <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>止损 / 止盈</Text>
          <Text style={[{ fontSize: fs.md }, tnum]}><Text style={{ color: colors.down }}>{price(p.stopLoss)}</Text><Text style={{ color: colors.textFaint }}> / </Text><Text style={{ color: colors.up }}>{price(p.takeProfit)}</Text></Text>
        </View>
        <Stat style={{ flex: 1 }} label="保证金" value={amt(p.margin)} />
        <View style={{ flex: 1, gap: 1 }}>
          <Text style={{ fontSize: fs.xs, color: colors.textFaint }} numberOfLines={1}>{instNameOf(insts, p.instanceId)}</Text>
          <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>{time(p.openedAt, true)}</Text>
        </View>
      </View>
      {onClose ? <View style={{ alignItems: "flex-end" }}><SmallButton label="平仓" icon="close-circle-outline" onPress={onClose} disabled={busy} /></View> : null}
    </View>
  );
}

export function OrdersList({ orders, empty }: { orders: Order[] | null; empty?: string }) {
  const rows = useMemo(() => (orders || []).slice().sort((a, b) => b.createdAt - a.createdAt || b.id - a.id), [orders]);
  const [shown, setShown] = useState(30);
  if (!rows.length) return <Muted style={{ paddingVertical: 8 }}>{orders ? empty || "没有委托" : "加载中…"}</Muted>;
  const dir = (o: Order) => (o.reduceOnly ? (o.posSide === "long" ? "平多" : "平空") : o.posSide === "long" ? "开多" : "开空");
  return (
    <View>
      {rows.slice(0, shown).map((o) => (
        <View key={o.id} style={rowStyle}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <CoinIcon instId={o.instId} size={16} />
            <Text style={{ fontWeight: "600", color: colors.text, fontSize: fs.md }}>{symOf(o.instId)}</Text>
            <Text style={{ fontSize: fs.md, fontWeight: "600", color: o.side === "buy" ? colors.up : colors.down }}>{dir(o)}</Text>
            <Text style={{ fontSize: fs.sm, color: colors.textSoft }}>{orderTypeLabel[o.type] || o.type}{o.leverage ? ` ${o.leverage}x` : ""}</Text>
            <View style={{ flex: 1 }} />
            <LTag l={orderStateLabel[o.state]} fallback={o.state} />
          </View>
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Stat style={{ flex: 1.2 }} label="时间" value={time(o.createdAt, true)} />
            <Stat style={{ flex: 1 }} label="触发/价格" value={o.triggerPx ? price(o.triggerPx) : price(o.price)} />
            <Stat style={{ flex: 1 }} label="数量/已成交" value={`${qty(o.qty)}/${qty(o.filled)}`} />
            <Stat style={{ flex: 1 }} label="均价" value={price(o.avgPx)} />
          </View>
          {o.reason ? <Muted>{o.reason}</Muted> : null}
        </View>
      ))}
      {rows.length > shown ? <MoreButton label={`显示更多（共 ${rows.length} 条）`} onPress={() => setShown((n) => n + 50)} /> : null}
    </View>
  );
}

export function FillsList({ fills, insts }: { fills: Fill[] | null; insts?: Instance[] }) {
  const rows = useMemo(() => (fills || []).slice().sort((a, b) => b.ts - a.ts || b.id - a.id), [fills]);
  const [shown, setShown] = useState(30);
  if (!rows.length) return <Muted style={{ paddingVertical: 8 }}>{fills ? "还没有成交" : "加载中…"}</Muted>;
  const what = (f: Fill) => (f.liquidation ? "强平" : (f.side === "buy") === (f.posSide === "long") ? (f.posSide === "long" ? "开多" : "开空") : f.posSide === "long" ? "平多" : "平空");
  return (
    <View>
      {rows.slice(0, shown).map((f) => (
        <View key={f.id} style={rowStyle}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <CoinIcon instId={f.instId} size={16} />
            <Text style={{ fontWeight: "600", color: colors.text, fontSize: fs.md }}>{symOf(f.instId)}</Text>
            <Text style={{ fontSize: fs.md, fontWeight: "600", color: f.liquidation ? colors.down : f.side === "buy" ? colors.up : colors.down }}>{what(f)}</Text>
            <Text style={[{ fontSize: fs.sm, color: colors.textSoft }, tnum]}>{qty(f.qty)} 张 @ {price(f.px)}</Text>
            <View style={{ flex: 1 }} />
            <Text style={[{ fontSize: fs.md, fontWeight: "600", color: toneText(tone(f.realizedPnl)) }, tnum]}>{f.realizedPnl ? amt(f.realizedPnl, 2, true) : "—"}</Text>
          </View>
          <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>
            {time(f.ts, true)} · 手续费 {amt(f.fee, 3)} · 滑点 {f.slippageBp.toFixed(1)}bp · {instNameOf(insts, f.instanceId)}
          </Text>
        </View>
      ))}
      {rows.length > shown ? <MoreButton label={`显示更多（共 ${rows.length} 条）`} onPress={() => setShown((n) => n + 50)} /> : null}
    </View>
  );
}

export function LedgerList({ ledger }: { ledger: LedgerEntry[] | null }) {
  const rows = useMemo(() => (ledger || []).slice().sort((x, y) => y.ts - x.ts), [ledger]);
  const [shown, setShown] = useState(40);
  if (!rows.length) return <Muted style={{ paddingVertical: 8 }}>{ledger ? "没有资金流水" : "加载中…"}</Muted>;
  return (
    <View>
      {rows.slice(0, shown).map((l) => (
        <View key={l.id} style={[rowStyle, { flexDirection: "row", alignItems: "center", gap: 10 }]}>
          <Text style={[{ width: 82, fontSize: fs.sm, color: colors.textFaint }, tnum]}>{time(l.ts, true)}</Text>
          <Text style={{ flex: 1, fontSize: fs.md, color: colors.textSoft }}>{ledgerLabel[l.kind] || l.kind}{l.instId ? ` · ${symOf(l.instId)}` : ""}</Text>
          <Text style={[{ fontSize: fs.md, fontWeight: "600", color: toneText(tone(l.amount)) }, tnum]}>{amt(l.amount, 4, true)}</Text>
        </View>
      ))}
      {rows.length > shown ? <MoreButton label={`显示更多（共 ${rows.length} 条）`} onPress={() => setShown((n) => n + 80)} /> : null}
    </View>
  );
}

/** Signal stream: newest first, outcome tag and reasons (web SignalList). */
export function SignalList({ signals, insts, limit = 20 }: { signals: Signal[] | null; insts?: Instance[]; limit?: number }) {
  const [filter, setFilter] = useState<"all" | "ordered" | "not">("all");
  const [shown, setShown] = useState(limit);
  const list = useMemo(
    () => (signals || []).filter((s) => filter === "all" || (filter === "ordered") === (s.outcome === "ordered")).slice().sort((a, b) => b.ts - a.ts || b.id - a.id),
    [signals, filter],
  );
  const instName = (id: number) => insts?.find((i) => i.id === id)?.name;
  return (
    <View style={{ gap: 6 }}>
      <Segmented small value={filter} onChange={setFilter} options={[{ value: "all", label: "全部" }, { value: "ordered", label: "已下单" }, { value: "not", label: "未执行" }]} style={{ alignSelf: "flex-start" }} />
      {list.length ? (
        list.slice(0, shown).map((s, i) => {
          const act = actionLabel[s.action];
          return (
            <View key={s.id} style={[rowStyle, i === 0 && { borderTopWidth: 0 }]}>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                <CoinIcon instId={s.instId} size={16} />
                <Text style={{ fontWeight: "600", color: colors.text, fontSize: fs.md }}>{symOf(s.instId)}</Text>
                <Text style={{ fontWeight: "600", fontSize: fs.sm, color: toneText(act?.tone) }}>{act?.t || s.action}</Text>
                <Text style={[{ fontSize: fs.sm, color: colors.textSoft }, tnum]}>@ {price(s.price)}</Text>
                <View style={{ flex: 1 }} />
                <LTag l={outcomeLabel[s.outcome]} fallback={s.outcome} />
              </View>
              <Text style={[{ fontSize: fs.xs, color: colors.textFaint }, tnum]}>
                {time(s.ts, true)}
                {instName(s.instanceId) ? <Text style={{ color: colors.textSoft }}>{`  ${instName(s.instanceId)}`}<Text style={{ color: colors.textFaint }}>{` · v${s.configVersion}`}</Text></Text> : null}
                {s.stopLoss ? `  止损 ${price(s.stopLoss)}` : ""}
                {s.takeProfit ? `  止盈 ${price(s.takeProfit)}` : ""}
                {s.strength != null && s.strength !== 1 ? `  强度 ${s.strength.toFixed(2)}` : ""}
              </Text>
              {s.reason ? <Why k="策略" text={s.reason} /> : null}
              {s.outcomeReason ? <Why k="结果" text={s.outcomeReason} bad={s.outcome === "rejected"} /> : null}
            </View>
          );
        })
      ) : (
        <Muted style={{ paddingVertical: 6 }}>{signals ? "还没有信号" : "加载中…"}</Muted>
      )}
      {list.length > shown ? <MoreButton label={`显示更多（共 ${list.length} 条）`} onPress={() => setShown((n) => n + 40)} /> : null}
    </View>
  );
}

export function Why({ k, text, bad }: { k: string; text: string; bad?: boolean }) {
  return (
    <Text style={{ fontSize: fs.sm, color: bad ? colors.down : colors.textSoft, lineHeight: 18 }}>
      <Text style={{ fontSize: fs.xs, color: colors.textFaint }}>{k}  </Text>{text}
    </Text>
  );
}

/** Rounded inner box (web .pcard / bg-2 panels). */
export function InnerBox({ children }: { children: ReactNode }) {
  return <View style={{ padding: 12, borderRadius: radius.md, backgroundColor: colors.raised, borderWidth: 1, borderColor: colors.borderStrong, gap: 8 }}>{children}</View>;
}

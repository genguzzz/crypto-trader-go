package trade

import (
	"database/sql"
	"fmt"
	"math"
	"time"
)

// Trade is one round trip of an instance in an instrument: from flat to flat (or still open).
// It is derived from fills, orders, signals and funding ledger rows; nothing is stored.
type Trade struct {
	ID          int64       `json:"id"` // first entry fill id
	InstanceID  int64       `json:"instanceId"`
	InstID      string      `json:"instId"`
	PosSide     string      `json:"posSide"`
	OpenTs      int64       `json:"openTs"`
	CloseTs     int64       `json:"closeTs,omitempty"` // 0 = still open
	EntryPx     float64     `json:"entryPx"`           // volume-weighted
	ExitPx      float64     `json:"exitPx,omitempty"`
	Qty         float64     `json:"qty"` // largest position size (contracts)
	CtVal       float64     `json:"ctVal"`
	Notional    float64     `json:"notional"` // USDT at entry for Qty
	Leverage    float64     `json:"leverage"`
	Margin      float64     `json:"margin"` // USDT
	Pnl         float64     `json:"pnl"`    // realized, before fees and funding
	Fees        float64     `json:"fees"`
	Funding     float64     `json:"funding"` // signed, as booked
	Net         float64     `json:"net"`
	ReturnPct   float64     `json:"returnPct"` // net / margin
	ExitKind    string      `json:"exitKind"`  // signal | stop | target | time | liquidation | manual | open
	OpenReason  string      `json:"openReason,omitempty"`
	CloseReason string      `json:"closeReason,omitempty"`
	StopLoss    float64     `json:"stopLoss,omitempty"`
	TakeProfit  float64     `json:"takeProfit,omitempty"`
	AIDecision  int64       `json:"aiDecisionId,omitempty"`
	Fills       []TradeFill `json:"fills"`
}

type TradeFill struct {
	ID    int64   `json:"id"`
	Ts    int64   `json:"ts"`
	Side  string  `json:"side"`
	Qty   float64 `json:"qty"`
	Px    float64 `json:"px"`
	Fee   float64 `json:"fee"`
	Pnl   float64 `json:"pnl"`
	Kind  string  `json:"kind"` // open | add | reduce | close
	Order string  `json:"orderType"`
}

type fillJoin struct {
	f                   Fill
	ct                  float64
	reduce              bool
	otype               string
	lever, stop, target float64
	oreason             string
	signalID            int64
	reason              string
	aiID                int64
	action              string
}

// Trades returns the account's round trips, oldest first.
func (s *Service) Trades(accountID int64) ([]Trade, error) {
	if _, err := s.book(accountID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT f.id, f.order_id, f.instance_id, f.inst_id, f.side, f.pos_side, f.qty, f.px, f.ct_val, f.fee,
		f.realized_pnl, f.liquidation, f.reduce, f.ts, IFNULL(o.type,''), IFNULL(o.leverage,0), IFNULL(o.signal_id,0),
		IFNULL(json_extract(sg.body,'$.reason'),''), IFNULL(sg.ai_decision_id,0), IFNULL(sg.action,''),
		IFNULL(json_extract(sg.body,'$.stopLoss'),0), IFNULL(json_extract(sg.body,'$.takeProfit'),0), IFNULL(o.reason,'')
		FROM trade_fills f LEFT JOIN trade_orders o ON o.id=f.order_id LEFT JOIN trade_signals sg ON sg.id=o.signal_id AND o.signal_id>0
		WHERE f.account_id=? ORDER BY f.ts, f.id`, accountID)
	if err != nil {
		return nil, err
	}
	var fs []fillJoin
	for rows.Next() {
		var j fillJoin
		var liq, red int
		if err := rows.Scan(&j.f.ID, &j.f.OrderID, &j.f.InstanceID, &j.f.InstID, &j.f.Side, &j.f.PosSide, &j.f.Qty, &j.f.Px, &j.ct, &j.f.Fee,
			&j.f.RealizedPnl, &liq, &red, &j.f.Ts, &j.otype, &j.lever, &j.signalID, &j.reason, &j.aiID, &j.action, &j.stop, &j.target, &j.oreason); err != nil {
			rows.Close()
			return nil, err
		}
		j.f.Liquidation, j.reduce = liq == 1, red == 1
		fs = append(fs, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type key struct {
		inst int64
		id   string
	}
	open := map[key]*Trade{}
	held := map[key]float64{}
	exitQty := map[*Trade]float64{}
	var out []*Trade
	for _, j := range fs {
		k := key{j.f.InstanceID, j.f.InstID}
		t := open[k]
		tf := TradeFill{ID: j.f.ID, Ts: j.f.Ts, Side: j.f.Side, Qty: j.f.Qty, Px: j.f.Px, Fee: j.f.Fee, Pnl: j.f.RealizedPnl, Order: j.otype}
		if !j.reduce {
			if t == nil {
				t = &Trade{ID: j.f.ID, InstanceID: j.f.InstanceID, InstID: j.f.InstID, PosSide: j.f.PosSide, OpenTs: j.f.Ts, CtVal: j.ct,
					Leverage: j.lever, OpenReason: j.reason, AIDecision: j.aiID, StopLoss: j.stop, TakeProfit: j.target, ExitKind: "open"}
				open[k], held[k] = t, 0
				out = append(out, t)
				tf.Kind = "open"
			} else {
				tf.Kind = "add"
			}
			t.EntryPx = (t.EntryPx*held[k] + j.f.Px*j.f.Qty) / (held[k] + j.f.Qty)
			held[k] += j.f.Qty
			t.Qty = math.Max(t.Qty, held[k])
		} else {
			if t == nil {
				continue // close of a position opened before this account's records (should not happen)
			}
			held[k] -= j.f.Qty
			t.ExitPx = (t.ExitPx*exitQty[t] + j.f.Px*j.f.Qty) / (exitQty[t] + j.f.Qty)
			exitQty[t] += j.f.Qty
			t.Pnl += j.f.RealizedPnl
			tf.Kind = "reduce"
			if held[k] <= qtyEps {
				tf.Kind = "close"
				t.CloseTs = j.f.Ts
				t.CloseReason = j.reason
				switch {
				case j.f.Liquidation:
					t.ExitKind = "liquidation"
				case j.otype == string(OrdStop):
					t.ExitKind = "stop"
				case j.otype == string(OrdTarget):
					t.ExitKind = "target"
				case j.oreason == reasonExpired:
					t.ExitKind = "time"
				case j.signalID > 0:
					t.ExitKind = "signal"
				default:
					t.ExitKind = "manual"
				}
				delete(open, k)
			}
		}
		t.Fees += j.f.Fee
		t.Fills = append(t.Fills, tf)
	}
	if err := s.attachFunding(accountID, out); err != nil {
		return nil, err
	}
	res := make([]Trade, 0, len(out))
	for _, t := range out {
		t.Notional = t.Qty * t.CtVal * t.EntryPx
		if t.Leverage > 0 {
			t.Margin = t.Notional / t.Leverage
		}
		t.Net = t.Pnl - t.Fees + t.Funding
		if t.Margin > 0 {
			t.ReturnPct = t.Net / t.Margin
		}
		res = append(res, *t)
	}
	return res, nil
}

// attachFunding books each funding ledger row to the trade holding that instrument at that time.
func (s *Service) attachFunding(accountID int64, ts []*Trade) error {
	rows, err := s.db.Query(`SELECT inst_id, amount, ts FROM trade_ledger WHERE account_id=? AND kind='funding' ORDER BY ts`, accountID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var inst string
		var amt float64
		var at int64
		if err := rows.Scan(&inst, &amt, &at); err != nil {
			return err
		}
		for _, t := range ts {
			if t.InstID == inst && t.OpenTs <= at && (t.CloseTs == 0 || at <= t.CloseTs) {
				t.Funding += amt
				break
			}
		}
	}
	return rows.Err()
}

// CandleResp is an instrument's chart over the account's period, for drawing trades on candles.
type CandleResp struct {
	InstID string       `json:"instId"`
	Bar    string       `json:"bar"`
	From   int64        `json:"from"`
	To     int64        `json:"to"`
	Rows   [][7]float64 `json:"rows"` // ts, o, h, l, c, vol (contracts), volQuote (USDT)
}

var candleTFs = []string{"1m", "5m", "15m", "1H", "4H", "1D"}

// AccountCandles returns candles of instID covering the account's period (backtest range, or
// creation → now for live accounts) with some lead-in. bar "" picks the finest timeframe that keeps
// the chart under ~1500 candles and is available in the database.
func (s *Service) AccountCandles(accountID int64, instID, bar string) (CandleResp, error) {
	b, err := s.book(accountID)
	if err != nil {
		return CandleResp{}, err
	}
	a := b.snapshot()
	from, to := a.From, a.To
	if a.Mode != ModeBacktest || from == 0 {
		from, to = a.CreatedAt, time.Now().UnixMilli()
	}
	if to <= from {
		to = from + hourMs
	}
	if first, last, ok := s.tradeSpan(accountID, instID); ok {
		from, to = min(from, first), max(to, last)
	}
	span := to - from
	if bar == "" {
		for _, tf := range candleTFs {
			ms, _ := TFMillis(tf)
			if span/ms <= 1500 {
				bar = tf
				break
			}
		}
		if bar == "" {
			bar = "1D"
		}
	}
	ms, ok := TFMillis(bar)
	if !ok {
		return CandleResp{}, errBad("bar 需为 1m/5m/15m/1H/4H/1D")
	}
	lead := min(span/6, 60*ms)
	from = (from - lead) / ms * ms
	to = (to/ms + 1) * ms
	if (to-from)/ms > 5000 {
		return CandleResp{}, errBad(fmt.Sprintf("区间过长：%s 粒度会超过 5000 根，请用更粗的 bar", bar))
	}
	table := "candles_1m"
	if ms >= hourMs {
		table = "candles_1h"
	}
	bars, err := s.st.Bars(table, instID, from, to)
	if err != nil {
		return CandleResp{}, err
	}
	if table == "candles_1m" && (len(bars) == 0 || bars[0].Ts > from+ms) {
		// 1m retention is short; fall back to hourly data when it doesn't cover the period
		if ms < hourMs {
			return s.AccountCandles(accountID, instID, "1H")
		}
	}
	resp := CandleResp{InstID: instID, Bar: bar, From: from, To: to, Rows: [][7]float64{}}
	var cur *[7]float64
	for _, x := range bars {
		open := x.Ts / ms * ms
		if cur == nil || int64(cur[0]) != open {
			resp.Rows = append(resp.Rows, [7]float64{float64(open), x.O, x.H, x.L, x.C, x.Vol, x.Quote})
			cur = &resp.Rows[len(resp.Rows)-1]
			continue
		}
		cur[2], cur[3], cur[4] = math.Max(cur[2], x.H), math.Min(cur[3], x.L), x.C
		cur[5] += x.Vol
		cur[6] += x.Quote
	}
	return resp, nil
}

func (s *Service) tradeSpan(accountID int64, instID string) (first, last int64, ok bool) {
	var a, b sql.NullInt64
	if err := s.db.QueryRow(`SELECT MIN(ts), MAX(ts) FROM trade_fills WHERE account_id=? AND inst_id=?`, accountID, instID).Scan(&a, &b); err != nil || !a.Valid {
		return 0, 0, false
	}
	return a.Int64, b.Int64, true
}

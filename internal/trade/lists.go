package trade

import (
	"encoding/json"
	"math"
)

// Page selects the newest records with id < Before (0 = newest), at most Limit (default 100, ≤ 500).
type Page struct {
	Limit  int
	Before int64
}

func (p Page) norm() (int, int64) {
	l := p.Limit
	if l <= 0 || l > 500 {
		l = 100
	}
	b := p.Before
	if b <= 0 {
		b = math.MaxInt64
	}
	return l, b
}

func (s *Service) Fills(id int64, p Page) ([]Fill, error) {
	if _, err := s.book(id); err != nil {
		return nil, err
	}
	limit, before := p.norm()
	rows, err := s.db.Query(`SELECT id, order_id, account_id, instance_id, inst_id, side, pos_side, qty, px, fee, slippage_bp, realized_pnl, liquidation, ts
		FROM trade_fills WHERE account_id=? AND id<? ORDER BY id DESC LIMIT ?`, id, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Fill{}
	for rows.Next() {
		var f Fill
		var liq int
		if err := rows.Scan(&f.ID, &f.OrderID, &f.AccountID, &f.InstanceID, &f.InstID, &f.Side, &f.PosSide, &f.Qty, &f.Px, &f.Fee, &f.SlippageBp,
			&f.RealizedPnl, &liq, &f.Ts); err != nil {
			return nil, err
		}
		f.Liquidation = liq == 1
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Service) Orders(id int64, p Page) ([]Order, error) {
	if _, err := s.book(id); err != nil {
		return nil, err
	}
	limit, before := p.norm()
	rows, err := s.db.Query(`SELECT `+orderCols+` FROM trade_orders WHERE account_id=? AND id<? ORDER BY id DESC LIMIT ?`, id, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Service) Signals(id int64, p Page) ([]Signal, error) {
	if _, err := s.book(id); err != nil {
		return nil, err
	}
	limit, before := p.norm()
	rows, err := s.db.Query(`SELECT id, outcome, outcome_reason, ai_decision_id, body FROM trade_signals WHERE account_id=? AND id<? ORDER BY id DESC LIMIT ?`, id, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Signal{}
	for rows.Next() {
		var sg Signal
		var sid, ai int64
		var outc, reason, body string
		if err := rows.Scan(&sid, &outc, &reason, &ai, &body); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(body), &sg)
		sg.ID, sg.Outcome, sg.OutcomeReason, sg.AIDecisionID = sid, SignalOutcome(outc), reason, ai
		out = append(out, sg)
	}
	return out, rows.Err()
}

func (s *Service) Ledger(id int64, p Page) ([]LedgerEntry, error) {
	if _, err := s.book(id); err != nil {
		return nil, err
	}
	limit, before := p.norm()
	rows, err := s.db.Query(`SELECT id, account_id, kind, amount, inst_id, ref_id, ts FROM trade_ledger WHERE account_id=? AND id<? ORDER BY id DESC LIMIT ?`, id, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerEntry{}
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.ID, &e.AccountID, &e.Kind, &e.Amount, &e.InstID, &e.RefID, &e.Ts); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Equity returns [[ts, equity]] in [from, to] (0 = open ends), at most 2000 points.
func (s *Service) Equity(id, from, to int64) ([][2]float64, error) {
	if _, err := s.book(id); err != nil {
		return nil, err
	}
	if to <= 0 {
		to = math.MaxInt64
	}
	return s.equityPoints(id, from, to, 2000)
}

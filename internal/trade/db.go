package trade

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func (s *Service) tx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// ---------- accounts ----------

func scanAccount(sc interface{ Scan(...any) error }) (Account, dayState, error) {
	var a Account
	var d dayState
	var limits, bt string
	var archived, halted int
	err := sc.Scan(&a.ID, &a.Name, &a.Mode, &limits, &archived, &a.From, &a.To, &a.CreatedAt, &bt, &d.key, &d.start, &d.deposits, &halted)
	if err != nil {
		return a, d, err
	}
	a.Archived, d.halted = archived == 1, halted == 1
	a.Limits = DefaultLimits()
	json.Unmarshal([]byte(limits), &a.Limits)
	if bt != "" {
		var st BacktestStatus
		if json.Unmarshal([]byte(bt), &st) == nil {
			a.Backtest = &st
		}
	}
	return a, d, nil
}

const accountCols = `id, name, mode, limits, archived, from_ts, to_ts, created_at, backtest, day_key, day_start, day_deposits, halted`

type dayState struct {
	key             int64
	start, deposits float64
	halted          bool
}

func insertAccount(x execer, a Account) (int64, error) {
	bt := ""
	if a.Backtest != nil {
		bt = mustJSON(a.Backtest)
	}
	res, err := x.Exec(`INSERT INTO trade_accounts(name, mode, limits, archived, from_ts, to_ts, created_at, backtest) VALUES(?,?,?,?,?,?,?,?)`,
		a.Name, a.Mode, mustJSON(a.Limits), b2i(a.Archived), a.From, a.To, a.CreatedAt, bt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func saveDayState(x execer, id int64, d dayState) error {
	_, err := x.Exec(`UPDATE trade_accounts SET day_key=?, day_start=?, day_deposits=?, halted=? WHERE id=?`, d.key, d.start, d.deposits, b2i(d.halted), id)
	return err
}

// ---------- ledger ----------

func insertLedger(x execer, e LedgerEntry) (int64, error) {
	res, err := x.Exec(`INSERT INTO trade_ledger(account_id, kind, amount, inst_id, ref_id, ts) VALUES(?,?,?,?,?,?)`,
		e.AccountID, e.Kind, e.Amount, e.InstID, e.RefID, e.Ts)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ---------- signals ----------

func insertSignal(x execer, sg *Signal) error {
	res, err := x.Exec(`INSERT INTO trade_signals(account_id, instance_id, inst_id, action, ts, recv_ts, outcome, outcome_reason, ai_decision_id, body)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, sg.AccountID, sg.InstanceID, sg.InstID, sg.Action, sg.Ts, sg.RecvTs, sg.Outcome, sg.OutcomeReason, sg.AIDecisionID, mustJSON(sg))
	if err != nil {
		return err
	}
	sg.ID, err = res.LastInsertId()
	return err
}

func updateSignal(x execer, sg Signal) error {
	_, err := x.Exec(`UPDATE trade_signals SET outcome=?, outcome_reason=?, ai_decision_id=?, body=? WHERE id=?`,
		sg.Outcome, sg.OutcomeReason, sg.AIDecisionID, mustJSON(sg), sg.ID)
	return err
}

// ---------- orders ----------

const orderCols = `id, account_id, instance_id, signal_id, inst_id, side, pos_side, type, qty, price, trigger_px, reduce_only,
	oco_group, leverage, state, filled, avg_px, reason, created_at, updated_at`

func scanOrder(sc interface{ Scan(...any) error }) (Order, error) {
	var o Order
	var ro int
	err := sc.Scan(&o.ID, &o.AccountID, &o.InstanceID, &o.SignalID, &o.InstID, &o.Side, &o.PosSide, &o.Type, &o.Qty, &o.Price, &o.TriggerPx, &ro,
		&o.OCOGroup, &o.Leverage, &o.State, &o.Filled, &o.AvgPx, &o.Reason, &o.CreatedAt, &o.UpdatedAt)
	o.ReduceOnly = ro == 1
	return o, err
}

func insertOrder(x execer, o *Order) error {
	res, err := x.Exec(`INSERT INTO trade_orders(account_id, instance_id, signal_id, inst_id, side, pos_side, type, qty, price, trigger_px,
		reduce_only, oco_group, leverage, state, filled, avg_px, reason, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.AccountID, o.InstanceID, o.SignalID, o.InstID, o.Side, o.PosSide, o.Type, o.Qty, o.Price, o.TriggerPx, b2i(o.ReduceOnly),
		o.OCOGroup, o.Leverage, o.State, o.Filled, o.AvgPx, o.Reason, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return err
	}
	o.ID, err = res.LastInsertId()
	return err
}

func updateOrder(x execer, o Order) error {
	_, err := x.Exec(`UPDATE trade_orders SET qty=?, state=?, filled=?, avg_px=?, reason=?, updated_at=? WHERE id=?`,
		o.Qty, o.State, o.Filled, o.AvgPx, o.Reason, o.UpdatedAt, o.ID)
	return err
}

// ---------- fills ----------

func insertFill(x execer, f *Fill, ctVal float64, reduce, closes bool) error {
	res, err := x.Exec(`INSERT INTO trade_fills(order_id, account_id, instance_id, inst_id, side, pos_side, qty, px, ct_val, fee, slippage_bp,
		realized_pnl, liquidation, reduce, closes, ts) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.OrderID, f.AccountID, f.InstanceID, f.InstID, f.Side, f.PosSide, f.Qty, f.Px, ctVal, f.Fee, f.SlippageBp, f.RealizedPnl,
		b2i(f.Liquidation), b2i(reduce), b2i(closes), f.Ts)
	if err != nil {
		return err
	}
	f.ID, err = res.LastInsertId()
	return err
}

// ---------- positions ----------

func savePosition(x execer, p *posState) error {
	_, err := x.Exec(`INSERT OR REPLACE INTO trade_positions VALUES(?,?,?,?)`, p.AccountID, p.InstanceID, p.InstID, mustJSON(p))
	return err
}

func deletePosition(x execer, accountID, instanceID int64, instID string) error {
	_, err := x.Exec(`DELETE FROM trade_positions WHERE account_id=? AND instance_id=? AND inst_id=?`, accountID, instanceID, instID)
	return err
}

// ---------- instances ----------

const instanceCols = `id, account_id, version_id, name, params, universe, exec, state, state_reason, config_version, heartbeat, created_at`

func scanInstance(sc interface{ Scan(...any) error }) (Instance, error) {
	var in Instance
	var params, uni string
	err := sc.Scan(&in.ID, &in.AccountID, &in.VersionID, &in.Name, &params, &uni, &in.Exec, &in.State, &in.StateReason, &in.ConfigVersion, &in.Heartbeat, &in.CreatedAt)
	if err != nil {
		return in, err
	}
	in.Params = map[string]float64{}
	json.Unmarshal([]byte(params), &in.Params)
	json.Unmarshal([]byte(uni), &in.Universe)
	return in, nil
}

func (s *Service) loadInstance(id int64) (Instance, error) {
	in, err := scanInstance(s.db.QueryRow(`SELECT `+instanceCols+` FROM trade_instances WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return in, errNotFound("实例不存在")
	}
	return in, err
}

func (s *Service) accountInstances(accountID int64) ([]Instance, error) {
	rows, err := s.db.Query(`SELECT `+instanceCols+` FROM trade_instances WHERE account_id=? ORDER BY id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		in, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *Service) setInstanceState(id int64, st InstanceState, reason string) error {
	_, err := s.db.Exec(`UPDATE trade_instances SET state=?, state_reason=? WHERE id=?`, st, reason, id)
	return err
}

func (s *Service) loadState(id int64) []byte {
	var b []byte
	s.db.QueryRow(`SELECT data FROM trade_instance_state WHERE instance_id=?`, id).Scan(&b)
	return b
}

func (s *Service) saveState(id int64, data []byte, now int64) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO trade_instance_state VALUES(?,?,?)`, id, data, now)
	return err
}

// ---------- strategy versions ----------

const versionCols = `id, name, file, description, timeframe, params, universe, code, created_at, meta`

func scanVersion(sc interface{ Scan(...any) error }) (StrategyVersion, error) {
	var v StrategyVersion
	var params, uni, meta string
	err := sc.Scan(&v.ID, &v.Name, &v.File, &v.Description, &v.Timeframe, &params, &uni, &v.Code, &v.CreatedAt, &meta)
	if err != nil {
		return v, err
	}
	json.Unmarshal([]byte(params), &v.Params)
	json.Unmarshal([]byte(uni), &v.DefaultUniverse)
	json.Unmarshal([]byte(meta), &v.Meta)
	return v, nil
}

// migrate adds the columns listed in migrations when a table lacks them.
func migrate(db *sql.DB) error {
	for _, m := range migrations {
		rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, m.table)
		if err != nil {
			return err
		}
		have := false
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil && n == m.column {
				have = true
			}
		}
		rows.Close()
		if have {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE ` + m.table + ` ADD COLUMN ` + m.column + ` ` + m.def); err != nil {
			return fmt.Errorf("add %s.%s: %w", m.table, m.column, err)
		}
	}
	return nil
}

func (s *Service) version(id string) (StrategyVersion, error) {
	v, err := scanVersion(s.db.QueryRow(`SELECT `+versionCols+` FROM trade_strategy_versions WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return v, errNotFound("策略版本不存在")
	}
	return v, err
}

// wipeAccount deletes every record of a (backtest) account except the account row and instances.
func wipeAccount(tx *sql.Tx, id int64) error {
	for _, t := range []string{"trade_signals", "trade_orders", "trade_fills", "trade_positions", "trade_ledger", "trade_equity"} {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE account_id=?`, id); err != nil {
			return fmt.Errorf("wipe %s: %w", t, err)
		}
	}
	_, err := tx.Exec(`DELETE FROM trade_instance_state WHERE instance_id IN (SELECT id FROM trade_instances WHERE account_id=?)`, id)
	return err
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

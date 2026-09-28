package store

import (
	"database/sql"
	"encoding/json"
)

// AlertRule is one monitoring rule (internal/alerts). Params and Channels are opaque JSON here;
// the alerts package owns their meaning and validation.
type AlertRule struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Enabled     bool            `json:"enabled"`
	Type        string          `json:"type"`
	Params      json.RawMessage `json:"params"`
	Scope       string          `json:"scope"`            // active | watch | inst
	InstID      string          `json:"instId,omitempty"` // scope = inst
	CooldownMin int             `json:"cooldownMin"`
	Channels    json.RawMessage `json:"channels"`
	CreatedAt   int64           `json:"createdAt"`
	UpdatedAt   int64           `json:"updatedAt"`
}

func (s *Store) AlertRules() ([]AlertRule, error) {
	rows, err := s.DB.Query(`SELECT id,name,enabled,type,params,scope,inst_id,cooldown_min,channels,created_at,updated_at FROM alert_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertRule
	for rows.Next() {
		var r AlertRule
		var params, ch string
		if err := rows.Scan(&r.ID, &r.Name, &r.Enabled, &r.Type, &params, &r.Scope, &r.InstID, &r.CooldownMin, &ch, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Params, r.Channels = json.RawMessage(params), json.RawMessage(ch)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PutAlertRule inserts (ID == 0; the new ID is returned) or updates a rule.
func (s *Store) PutAlertRule(r AlertRule) (int64, error) {
	if r.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO alert_rules(name,enabled,type,params,scope,inst_id,cooldown_min,channels,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			r.Name, r.Enabled, r.Type, string(r.Params), r.Scope, r.InstID, r.CooldownMin, string(r.Channels), r.CreatedAt, r.UpdatedAt)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	res, err := s.DB.Exec(`UPDATE alert_rules SET name=?,enabled=?,type=?,params=?,scope=?,inst_id=?,cooldown_min=?,channels=?,updated_at=? WHERE id=?`,
		r.Name, r.Enabled, r.Type, string(r.Params), r.Scope, r.InstID, r.CooldownMin, string(r.Channels), r.UpdatedAt, r.ID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, sql.ErrNoRows
	}
	return r.ID, nil
}

func (s *Store) DeleteAlertRule(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM alert_rules WHERE id=?`, id)
	return err
}

// AlertEvent is one fired alert. Delivery maps channel → "ok" / "pending" / error text.
type AlertEvent struct {
	ID       int64             `json:"id"`
	Ts       int64             `json:"ts"`
	RuleID   int64             `json:"ruleId"`
	RuleName string            `json:"ruleName"`
	InstID   string            `json:"instId"`
	Type     string            `json:"type"`
	Value    float64           `json:"value"`
	Message  string            `json:"message"`
	Delivery map[string]string `json:"delivery"`
}

func (s *Store) AddAlertEvent(ev AlertEvent) (int64, error) {
	d, _ := json.Marshal(ev.Delivery)
	res, err := s.DB.Exec(`INSERT INTO alert_events(ts,rule_id,rule_name,inst_id,type,value,message,delivery) VALUES(?,?,?,?,?,?,?,?)`,
		ev.Ts, ev.RuleID, ev.RuleName, ev.InstID, ev.Type, ev.Value, ev.Message, string(d))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) SetAlertDelivery(id int64, delivery map[string]string) error {
	d, _ := json.Marshal(delivery)
	_, err := s.DB.Exec(`UPDATE alert_events SET delivery=? WHERE id=?`, string(d), id)
	return err
}

// AlertEvents returns the newest events first.
func (s *Store) AlertEvents(limit int) ([]AlertEvent, error) {
	rows, err := s.DB.Query(`SELECT id,ts,rule_id,rule_name,inst_id,type,value,message,delivery FROM alert_events ORDER BY ts DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertEvent{}
	for rows.Next() {
		var ev AlertEvent
		var v sql.NullFloat64
		var d string
		if err := rows.Scan(&ev.ID, &ev.Ts, &ev.RuleID, &ev.RuleName, &ev.InstID, &ev.Type, &v, &ev.Message, &d); err != nil {
			return nil, err
		}
		ev.Value = v.Float64
		json.Unmarshal([]byte(d), &ev.Delivery)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// AlertKey identifies a cooldown / edge-trigger slot.
type AlertKey struct {
	RuleID int64
	InstID string
}

// LastAlertFires returns the latest fire time per (rule, instrument) since `since` (cooldowns survive restarts).
func (s *Store) LastAlertFires(since int64) (map[AlertKey]int64, error) {
	rows, err := s.DB.Query(`SELECT rule_id, inst_id, MAX(ts) FROM alert_events WHERE ts>=? GROUP BY rule_id, inst_id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[AlertKey]int64{}
	for rows.Next() {
		var k AlertKey
		var ts int64
		if err := rows.Scan(&k.RuleID, &k.InstID, &ts); err != nil {
			return nil, err
		}
		out[k] = ts
	}
	return out, rows.Err()
}

// PruneAlertEvents deletes events older than `before` and keeps at most maxRows of the newest.
func (s *Store) PruneAlertEvents(before int64, maxRows int) error {
	if _, err := s.DB.Exec(`DELETE FROM alert_events WHERE ts < ?`, before); err != nil {
		return err
	}
	_, err := s.DB.Exec(`DELETE FROM alert_events WHERE id NOT IN (SELECT id FROM alert_events ORDER BY ts DESC, id DESC LIMIT ?)`, maxRows)
	return err
}

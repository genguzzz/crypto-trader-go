package realtrade

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Two owner switches over what the agent may do with a proposal (owner decision 2026-11-22,
// docs/trading-design.md §7 "Hands-free execution and locked positions"). Both are the owner's only:
// REST with the owner key, no agent tool.
//
//   - Hands-free execution: a new proposal is approved by the server at once (by and via "auto") and
//     goes through the same Approve → execute path as the owner's own answer, so every re-validation
//     and cap still applies and the audit trail shows it.
//   - Position locks: the agent may not act on a locked instrument and side at all — no proposal is
//     accepted for it, and one made before the lock cannot execute, whoever approves it.

// ByAuto is who resolved a proposal that hands-free execution approved (resolved_by and via).
const ByAuto = "auto"

const autoSetting = "realtrade.auto"

const lockSchema = `CREATE TABLE IF NOT EXISTS trade_locks(inst_id TEXT NOT NULL, side TEXT NOT NULL, ts INTEGER NOT NULL,
  PRIMARY KEY(inst_id, side))`

// AutoState is the hands-free switch. It is bound to the execution mode it was switched on in: after
// a change of CTG_TRADE_MODE it is off again, so a switch tried in a dry run never sends real orders.
type AutoState struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode,omitempty"`  // the mode it was switched on in
	Since   int64  `json:"since,omitempty"` // ms
}

// Auto is the switch as it applies now.
func (s *Service) Auto() AutoState {
	var a AutoState
	if raw, ok := s.st.Setting(autoSetting); ok && raw != "" {
		json.Unmarshal([]byte(raw), &a)
	}
	if !a.Enabled || a.Mode != string(s.Mode()) {
		return AutoState{}
	}
	return a
}

// SetAuto switches hands-free execution on (for the current mode) or off. Proposals already waiting
// stay waiting for the owner.
func (s *Service) SetAuto(on bool) (AutoState, error) {
	a := AutoState{}
	if on {
		a = AutoState{Enabled: true, Mode: string(s.Mode()), Since: s.now().UnixMilli()}
	}
	raw, _ := json.Marshal(a)
	if err := s.st.PutSetting(autoSetting, string(raw)); err != nil {
		return AutoState{}, err
	}
	s.logf("realtrade: hands-free execution %v (%s)", on, s.Mode())
	s.changed()
	return a, nil
}

// Lock is one instrument and side the agent may not act on.
type Lock struct {
	InstID string `json:"instId"`
	Side   string `json:"side"` // long | short
	Ts     int64  `json:"ts"`
}

// Locks lists the locked positions, newest first.
func (s *Service) Locks() []Lock {
	out := []Lock{}
	rows, err := s.db.Query(`SELECT inst_id, side, ts FROM trade_locks ORDER BY ts DESC, inst_id, side`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var l Lock
		if rows.Scan(&l.InstID, &l.Side, &l.Ts) == nil {
			out = append(out, l)
		}
	}
	return out
}

// SetLock locks or unlocks an instrument and side. A lock does not need a position: it also keeps the
// agent from opening one there, and it stays until the owner removes it.
func (s *Service) SetLock(inst, side string, on bool) error {
	inst, side = NormalizeInst(inst), strings.ToLower(strings.TrimSpace(side))
	if !strings.HasSuffix(inst, "-USDT-SWAP") || (side != "long" && side != "short") {
		return errors.New("a lock needs an instrument (BTC-USDT-SWAP) and a side (long | short)")
	}
	var err error
	if on {
		_, err = s.db.Exec(`INSERT OR IGNORE INTO trade_locks(inst_id, side, ts) VALUES(?,?,?)`, inst, side, s.now().UnixMilli())
	} else {
		_, err = s.db.Exec(`DELETE FROM trade_locks WHERE inst_id=? AND side=?`, inst, side)
	}
	if err == nil {
		s.changed()
	}
	return err
}

// Locked reports whether the owner locked the instrument and side.
func (s *Service) Locked(inst, side string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM trade_locks WHERE inst_id=? AND side=?`, inst, side).Scan(&n)
	return n > 0
}

// lockErr is the refusal for an action on a locked position (nil when it is not locked).
func (s *Service) lockErr(inst, side string) error {
	if !s.Locked(inst, side) {
		return nil
	}
	return fmt.Errorf("the owner locked %s %s: the AI may not act on it (no open, add, reduce, close, TP/SL or order change) until he unlocks it in the app — tell him what you would do instead of proposing it",
		strings.TrimSuffix(inst, "-USDT-SWAP"), side)
}

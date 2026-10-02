package trade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// VersionID is the first 12 hex digits of sha256(code).
func VersionID(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])[:12]
}

// ReloadStrategies asks the runner to describe strategies/library and stores every new code
// version. It returns the number of versions added.
func (s *Service) ReloadStrategies(ctx context.Context) (int, error) {
	runner, _, _ := s.deps()
	if runner == nil || !runner.Available() {
		return 0, errUnavailable("策略运行器未连接")
	}
	vs, err := runner.Describe(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UnixMilli()
	added := 0
	for _, v := range vs {
		if v.Name == "" || v.Code == "" {
			continue
		}
		if v.ID == "" { // the runner disambiguates several classes sharing one file
			v.ID = VersionID(v.Code)
		}
		v.Meta.Builtin = true
		n, err := s.insertVersion(v, now)
		if err != nil {
			return added, err
		}
		added += n
	}
	if added > 0 {
		s.changed(0, "strategy")
	}
	return added, nil
}

// insertVersion stores a version; an existing id (same code) only refreshes its meta, which older
// releases did not record. It returns 1 when the version is new.
func (s *Service) insertVersion(v StrategyVersion, now int64) (int, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO trade_strategy_versions(`+versionCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.Name, v.File, v.Description, v.Timeframe, mustJSON(v.Params), mustJSON(v.DefaultUniverse), v.Code, now, mustJSON(v.Meta))
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return 1, nil
	}
	_, err = s.db.Exec(`UPDATE trade_strategy_versions SET meta=? WHERE id=?`, mustJSON(v.Meta), v.ID)
	return 0, err
}

// StrategyEntry is one strategy name: its latest version and the version history (newest first).
type StrategyEntry struct {
	Name     string            `json:"name"`
	Latest   StrategyVersion   `json:"latest"`
	Versions []StrategyVersion `json:"versions"`
}

// Strategies lists strategies by name (versions without code).
func (s *Service) Strategies() ([]StrategyEntry, error) {
	rows, err := s.db.Query(`SELECT ` + versionCols + ` FROM trade_strategy_versions ORDER BY name, created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StrategyEntry{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		v.Code = ""
		if v.Meta.Draft {
			continue
		}
		if n := len(out); n == 0 || out[n-1].Name != v.Name {
			out = append(out, StrategyEntry{Name: v.Name, Latest: v})
		}
		out[len(out)-1].Versions = append(out[len(out)-1].Versions, v)
	}
	return out, rows.Err()
}

// StrategyVersion returns one version including its code.
func (s *Service) StrategyVersion(id string) (StrategyVersion, error) { return s.version(id) }

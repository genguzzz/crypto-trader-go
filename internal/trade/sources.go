package trade

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// User strategies: source code written in the editor. A source is a mutable draft; publishing it
// checks the code and stores an immutable version (like the built-in library files), which
// backtests and instances use. Trial runs replay a draft over recent data in a throwaway account.

// Source is one user strategy.
type Source struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`                // strategy name declared by the code at the last save
	Code      string `json:"code,omitempty"`      // draft
	Published string `json:"published,omitempty"` // version id of the last publish
	Dirty     bool   `json:"dirty"`               // the draft differs from the published version
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

const (
	maxSourceBytes = 256 << 10
	maxSources     = 200
)

func (s *Service) scanSource(sc interface{ Scan(...any) error }, withCode bool) (Source, error) {
	var x Source
	if err := sc.Scan(&x.ID, &x.Name, &x.Code, &x.Published, &x.CreatedAt, &x.UpdatedAt); err != nil {
		return x, err
	}
	x.Dirty = x.Published == "" || x.Published != VersionID(x.Code)
	if !withCode {
		x.Code = ""
	}
	return x, nil
}

const sourceCols = `id, name, code, published, created_at, updated_at`

// Sources lists user strategies (without code), newest first.
func (s *Service) Sources() ([]Source, error) {
	rows, err := s.db.Query(`SELECT ` + sourceCols + ` FROM trade_strategy_sources ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		x, err := s.scanSource(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Source returns one user strategy with its draft code.
func (s *Service) Source(id int64) (Source, error) {
	x, err := s.scanSource(s.db.QueryRow(`SELECT `+sourceCols+` FROM trade_strategy_sources WHERE id=?`, id), true)
	if errors.Is(err, sql.ErrNoRows) {
		return x, errNotFound("策略不存在")
	}
	return x, err
}

func validCode(code string) error {
	if strings.TrimSpace(code) == "" {
		return errBad("代码为空")
	}
	if len(code) > maxSourceBytes {
		return errBad(fmt.Sprintf("代码最多 %d KB", maxSourceBytes>>10))
	}
	if !utf8.ValidString(code) {
		return errBad("代码不是有效的 UTF-8")
	}
	return nil
}

var (
	nameAttrRe = regexp.MustCompile(`(?m)^\s+name\s*(?::\s*str\s*)?=\s*["']([^"'\n]+)["']`)
	classRe    = regexp.MustCompile(`(?m)^class\s+(\w+)\s*\(`)
)

// sourceName is the strategy name the code declares, read without running it (publish checks it
// properly): the first `name = "..."` in a class body, else the kebab-case of the first class.
func sourceName(code string) string {
	if m := nameAttrRe.FindStringSubmatch(code); m != nil {
		return clip(strings.TrimSpace(m[1]), 40)
	}
	if m := classRe.FindStringSubmatch(code); m != nil {
		return kebab(m[1])
	}
	return "未命名策略"
}

func kebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('-')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// copyCode is a built-in strategy's code under a new name, as the start of a user strategy.
func copyCode(code, name string) string {
	if loc := nameAttrRe.FindStringSubmatchIndex(code); loc != nil {
		return code[:loc[2]] + name + code[loc[3]:]
	}
	return code
}

// CreateSource stores a new draft: the given code, or a copy of a stored version renamed to
// "<name>-copy" (built-in strategies are read-only; this is how one starts from them).
func (s *Service) CreateSource(code, fromVersion string) (Source, error) {
	if fromVersion != "" {
		v, err := s.version(fromVersion)
		if err != nil {
			return Source{}, err
		}
		code = copyCode(v.Code, v.Name+"-copy")
	}
	if err := validCode(code); err != nil {
		return Source{}, err
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM trade_strategy_sources`).Scan(&n)
	if n >= maxSources {
		return Source{}, errBad(fmt.Sprintf("最多 %d 个自建策略", maxSources))
	}
	now := time.Now().UnixMilli()
	res, err := s.db.Exec(`INSERT INTO trade_strategy_sources(name, code, created_at, updated_at) VALUES(?,?,?,?)`,
		sourceName(code), code, now, now)
	if err != nil {
		return Source{}, err
	}
	id, _ := res.LastInsertId()
	s.changed(0, "strategy")
	return s.Source(id)
}

// SaveSource replaces a draft's code (autosave). It never touches published versions.
func (s *Service) SaveSource(id int64, code string) (Source, error) {
	if _, err := s.Source(id); err != nil {
		return Source{}, err
	}
	if err := validCode(code); err != nil {
		return Source{}, err
	}
	if _, err := s.db.Exec(`UPDATE trade_strategy_sources SET code=?, name=?, updated_at=? WHERE id=?`,
		code, sourceName(code), time.Now().UnixMilli(), id); err != nil {
		return Source{}, err
	}
	s.changed(0, "strategy")
	return s.Source(id)
}

// DeleteSource removes a draft. Published versions stay: instances and backtests keep using them.
func (s *Service) DeleteSource(id int64) error {
	res, err := s.db.Exec(`DELETE FROM trade_strategy_sources WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound("策略不存在")
	}
	s.changed(0, "strategy")
	return nil
}

// Check inspects code without storing it.
func (s *Service) Check(ctx context.Context, code string) (CheckResult, error) {
	if err := validCode(code); err != nil {
		return CheckResult{}, err
	}
	runner, _, _ := s.deps()
	if runner == nil || !runner.Available() {
		return CheckResult{}, errUnavailable("策略运行器未连接，无法检查")
	}
	return runner.Check(ctx, code)
}

// checkedVersion checks code and builds its version (not stored). The strategy name must not be
// taken by a built-in strategy or by another user strategy.
func (s *Service) checkedVersion(ctx context.Context, code string, sourceID int64) (StrategyVersion, CheckResult, error) {
	r, err := s.Check(ctx, code)
	if err != nil {
		return StrategyVersion{}, r, err
	}
	if r.Fatal() {
		return StrategyVersion{}, r, errBad("代码检查未通过")
	}
	if len(r.Strategies) != 1 {
		return StrategyVersion{}, r, errBad("一个文件需正好定义一个策略类")
	}
	v := r.Strategies[0]
	var owner int64
	var builtin bool
	rows, err := s.db.Query(`SELECT meta FROM trade_strategy_versions WHERE name=?`, v.Name)
	if err != nil {
		return v, r, err
	}
	for rows.Next() {
		var raw string
		var m StrategyMeta
		if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &m) == nil {
			builtin = builtin || m.Builtin
			if m.SourceID != 0 && !m.Draft {
				owner = m.SourceID
			}
		}
	}
	rows.Close()
	if owner != 0 && owner != sourceID { // a deleted user strategy no longer holds its name
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM trade_strategy_sources WHERE id=?`, owner).Scan(&n)
		if n == 0 {
			owner = 0
		}
	}
	if builtin || (owner != 0 && owner != sourceID) {
		return v, r, errBad(fmt.Sprintf("策略名 %q 已被%s使用，请改 name", v.Name, map[bool]string{true: "内置策略", false: "另一个自建策略"}[builtin]))
	}
	v.ID, v.Code, v.File = VersionID(code), code, fmt.Sprintf("user/%d.py", sourceID)
	v.Meta.SourceID = sourceID
	return v, r, nil
}

// PublishResult is a publish attempt: the version on success, the check either way.
type PublishResult struct {
	Version *StrategyVersion `json:"version,omitempty"`
	Check   CheckResult      `json:"check"`
}

// PublishSource checks the draft and stores it as an immutable version.
func (s *Service) PublishSource(ctx context.Context, id int64) (PublishResult, error) {
	src, err := s.Source(id)
	if err != nil {
		return PublishResult{}, err
	}
	v, chk, err := s.checkedVersion(ctx, src.Code, id)
	if err != nil {
		return PublishResult{Check: chk}, err
	}
	now := time.Now().UnixMilli()
	if _, err := s.insertVersion(v, now); err != nil {
		return PublishResult{Check: chk}, err
	}
	if _, err := s.db.Exec(`UPDATE trade_strategy_sources SET published=?, name=? WHERE id=?`, v.ID, v.Name, id); err != nil {
		return PublishResult{Check: chk}, err
	}
	stored, err := s.version(v.ID)
	if err != nil {
		return PublishResult{Check: chk}, err
	}
	stored.Code = ""
	s.changed(0, "strategy")
	return PublishResult{Version: &stored, Check: chk}, nil
}

// ---------- trial runs ----------

// TrialInput is a trial run: code (or a stored version) with params and universe over the last Days.
type TrialInput struct {
	Code      string             `json:"code"`
	VersionID string             `json:"versionId"`
	SourceID  int64              `json:"sourceId"`
	Params    map[string]float64 `json:"params"`
	Universe  *Universe          `json:"universe"`
	Days      int                `json:"days"`
}

// TrialResult is everything a trial produced; the throwaway account is gone by the time it returns.
type TrialResult struct {
	Check    *CheckResult `json:"check,omitempty"`
	Error    string       `json:"error,omitempty"`
	From     int64        `json:"from"`
	To       int64        `json:"to"`
	Strategy string       `json:"strategy"`
	Report   *Report      `json:"report,omitempty"`
	Signals  []Signal     `json:"signals"`
	Trades   []Trade      `json:"trades"`
	Logs     []LogLine    `json:"logs"`
	Candles  *CandleResp  `json:"candles,omitempty"`
	Seconds  float64      `json:"seconds"`
}

const (
	trialPrefix  = "〔试运行〕"
	trialTimeout = 90 * time.Second
	trialDeposit = 10_000
)

// Trial runs a strategy over recent history in a hidden backtest account and returns the outcome.
func (s *Service) Trial(ctx context.Context, in TrialInput) (TrialResult, error) {
	start := time.Now()
	days := in.Days
	if days == 0 {
		days = 14
	}
	if days < 1 || days > 60 {
		return TrialResult{}, errBad("试运行天数需在 1–60")
	}
	var ver StrategyVersion
	res := TrialResult{Signals: []Signal{}, Trades: []Trade{}, Logs: []LogLine{}}
	switch {
	case in.VersionID != "":
		v, err := s.version(in.VersionID)
		if err != nil {
			return res, err
		}
		ver = v
	default:
		code := in.Code
		if code == "" && in.SourceID > 0 {
			src, err := s.Source(in.SourceID)
			if err != nil {
				return res, err
			}
			code = src.Code
		}
		v, chk, err := s.checkedVersion(ctx, code, in.SourceID)
		res.Check = &chk
		if err != nil {
			res.Error = err.Error()
			return res, nil // diagnostics are the answer
		}
		if old, err := s.version(v.ID); err == nil {
			ver = old // same code was published or tried before
		} else {
			v.Meta.Draft = true
			if _, err := s.insertVersion(v, time.Now().UnixMilli()); err != nil {
				return res, err
			}
			if ver, err = s.version(v.ID); err != nil {
				return res, err
			}
		}
	}
	res.Strategy = ver.Name
	now := time.Now().UnixMilli()
	if s.md != nil {
		now = s.md.Now()
	}
	to := now / hourMs * hourMs
	from := to - int64(days)*dayMs
	res.From, res.To = from, to
	name := []rune(trialPrefix + ver.Name)
	acc, err := s.CreateAccount(AccountInput{Name: string(name[:min(len(name), 40)]), Mode: ModeBacktest, Deposit: trialDeposit, From: from, To: to})
	if err != nil {
		return res, err
	}
	defer func() {
		if err := s.deleteAccount(acc.ID); err != nil {
			log.Printf("trade: delete trial account %d: %v", acc.ID, err)
		}
	}()
	inst, err := s.CreateInstance(acc.ID, InstanceInput{VersionID: ver.ID, Params: in.Params, Universe: in.Universe, Exec: ExecAuto})
	if err != nil {
		return res, err
	}
	rctx, cancel := context.WithTimeout(ctx, trialTimeout)
	defer cancel()
	if err := s.RunBacktest(rctx, acc.ID); err != nil {
		return res, err
	}
	b, _ := s.book(acc.ID)
	b.mu.Lock()
	run := b.bt
	b.mu.Unlock()
	if run != nil {
		select {
		case <-run.done:
		case <-rctx.Done():
			run.cancel()
			<-run.done
		}
	}
	if st := b.snapshot().Backtest; st != nil && st.State == "failed" {
		res.Error = st.Error
		if rctx.Err() == context.DeadlineExceeded {
			res.Error = fmt.Sprintf("超过 %d 秒未跑完，已停止（可缩短天数或减少品种）", int(trialTimeout.Seconds()))
		}
	}
	if rep, err := s.Report(acc.ID); err == nil {
		res.Report = &rep
	}
	res.Signals, _ = s.Signals(acc.ID, Page{Limit: 500})
	res.Trades, _ = s.Trades(acc.ID)
	res.Logs = s.RunLog(inst.ID)
	if res.Signals == nil {
		res.Signals = []Signal{}
	}
	if res.Trades == nil {
		res.Trades = []Trade{}
	}
	// chart the instrument with the most signals (or the first of the universe)
	chart := ""
	counts := map[string]int{}
	for _, sg := range res.Signals {
		counts[sg.InstID]++
		if counts[sg.InstID] > counts[chart] {
			chart = sg.InstID
		}
	}
	if chart == "" {
		if u := inst.Universe.Static; len(u) > 0 {
			chart = u[0]
		}
	}
	if chart != "" {
		if c, err := s.AccountCandles(acc.ID, chart, ""); err == nil {
			res.Candles = &c
		}
	}
	res.Seconds = round(time.Since(start).Seconds(), 1)
	return res, nil
}

// deleteAccount removes an account and everything recorded for it (trial runs only).
func (s *Service) deleteAccount(id int64) error {
	insts, _ := s.accountInstances(id)
	err := s.tx(func(tx *sql.Tx) error {
		if err := wipeAccount(tx, id); err != nil {
			return err
		}
		for _, in := range insts {
			for _, q := range []string{`DELETE FROM trade_instance_configs WHERE instance_id=?`, `DELETE FROM trade_instance_state WHERE instance_id=?`} {
				if _, err := tx.Exec(q, in.ID); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(`DELETE FROM trade_instances WHERE account_id=?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM trade_accounts WHERE id=?`, id)
		return err
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.books, id)
	s.mu.Unlock()
	s.logs.mu.Lock()
	for _, in := range insts {
		delete(s.logs.lines, in.ID)
	}
	s.logs.mu.Unlock()
	return nil
}

// dropTrialAccounts deletes trial accounts left behind by a restart during a trial.
func (s *Service) dropTrialAccounts() {
	s.mu.RLock()
	var ids []int64
	for id, b := range s.books {
		if isTrial(b.snapshot().Name) {
			ids = append(ids, id)
		}
	}
	s.mu.RUnlock()
	for _, id := range ids {
		if err := s.deleteAccount(id); err != nil {
			log.Printf("trade: drop trial account %d: %v", id, err)
		}
	}
}

func isTrial(name string) bool { return strings.HasPrefix(name, trialPrefix) }

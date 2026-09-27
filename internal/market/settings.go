package market

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"
)

// Settings are the user-editable runtime parameters (settings page), persisted in SQLite.
// Command-line flags only provide first-run defaults; saved settings take precedence.
type Settings struct {
	Threshold   float64  `json:"threshold"`   // 12h USDT turnover, strictly greater
	TopN        int      `json:"topN"`        // pool cap
	Categories  []string `json:"categories"`  // instCategory: 1 crypto, 3 stock/ETF, 4 commodity
	DefaultRule string   `json:"defaultRule"` // rule used by default in the scanner
}

var allCategories = []string{"1", "3", "4"}

func (e *Engine) conf() Config {
	e.cfgMu.RLock()
	defer e.cfgMu.RUnlock()
	return e.cfg
}

func (e *Engine) Settings() Settings {
	c := e.conf()
	cats := []string{}
	if c.Categories == nil {
		cats = append(cats, allCategories...)
	} else {
		for k := range c.Categories {
			cats = append(cats, k)
		}
		sort.Strings(cats)
	}
	e.cfgMu.RLock()
	def := e.defaultRule
	e.cfgMu.RUnlock()
	if _, ok := e.Rule(def); !ok {
		if rs := e.Rules(); len(rs) > 0 {
			def = rs[0].ID
		}
	}
	return Settings{Threshold: c.Threshold, TopN: c.TopN, Categories: cats, DefaultRule: def}
}

func (e *Engine) loadSettings() {
	raw, ok := e.st.Setting("runtime")
	if !ok {
		return
	}
	var s Settings
	if err := json.Unmarshal([]byte(raw), &s); err != nil || s.validate(e) != nil {
		log.Printf("ignoring invalid saved settings: %v", err)
		return
	}
	e.applySettings(s)
	log.Printf("settings: threshold %.0f, top %d, categories %v, default rule %s", s.Threshold, s.TopN, s.Categories, s.DefaultRule)
}

func (s *Settings) validate(e *Engine) error {
	if s.Threshold < 100_000 || s.Threshold > 10_000_000_000 {
		return fmt.Errorf("阈值需在 10 万 ~ 100 亿 USDT 之间")
	}
	if s.TopN < 1 || s.TopN > 500 {
		return fmt.Errorf("上限 N 需在 1 ~ 500 之间")
	}
	if len(s.Categories) == 0 {
		return fmt.Errorf("至少选择一个类别")
	}
	for _, c := range s.Categories {
		if c != "1" && c != "3" && c != "4" {
			return fmt.Errorf("未知类别 %q", c)
		}
	}
	if s.DefaultRule != "" {
		if _, ok := e.Rule(s.DefaultRule); !ok {
			return fmt.Errorf("规则 %q 不存在", s.DefaultRule)
		}
	}
	return nil
}

func (e *Engine) applySettings(s Settings) (poolChanged, catsChanged bool) {
	cats := map[string]bool{}
	for _, c := range s.Categories {
		cats[c] = true
	}
	if len(cats) == len(allCategories) {
		cats = nil // all
	}
	e.cfgMu.Lock()
	old := e.cfg
	e.cfg.Threshold, e.cfg.TopN, e.cfg.Categories = s.Threshold, s.TopN, cats
	if s.DefaultRule != "" {
		e.defaultRule = s.DefaultRule
	}
	e.cfgMu.Unlock()
	catsChanged = fmt.Sprint(sortedKeys(old.Categories)) != fmt.Sprint(sortedKeys(cats))
	poolChanged = old.Threshold != s.Threshold || old.TopN != s.TopN || catsChanged
	return
}

func sortedKeys(m map[string]bool) []string {
	if m == nil {
		return []string{"*"}
	}
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// UpdateSettings validates, persists and applies settings; the pool is recomputed immediately and
// the historical replay re-runs if pool parameters changed.
func (e *Engine) UpdateSettings(ctx context.Context, s Settings) (Settings, error) {
	if err := s.validate(e); err != nil {
		return e.Settings(), err
	}
	b, _ := json.Marshal(s)
	if err := e.st.PutSetting("runtime", string(b)); err != nil {
		return e.Settings(), err
	}
	poolChanged, catsChanged := e.applySettings(s)
	if catsChanged {
		if err := e.refreshInstruments(ctx); err == nil {
			e.updateSubscriptions()
			go e.backfillNew(context.Background())
		}
	}
	if poolChanged && e.Phase() == "live" {
		e.mu.Lock()
		e.lastMin = 0 // force
		e.mu.Unlock()
		e.computeUniverse(time.Now().UnixMilli()/minuteMs*minuteMs - minuteMs)
		e.requestReplay()
	} else {
		e.rescore()
	}
	return e.Settings(), nil
}

// requestReplay (re)runs the hourly replay; concurrent requests coalesce into one extra run.
func (e *Engine) requestReplay() {
	if !e.replayRunning.CompareAndSwap(false, true) {
		e.replayPending.Store(true)
		return
	}
	go func() {
		for {
			e.runReplay(context.Background())
			if !e.replayPending.Swap(false) {
				break
			}
		}
		e.replayRunning.Store(false)
	}()
}

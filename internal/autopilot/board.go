package autopilot

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
)

// The pool board is what a patrol (a scheduled or manual run) is given to look for trades in: the
// coins of the monitored pool that stand out now, computed by the server from the fact panel, and what
// changed in the pool since the last patrol. Without it a run with no signal had nothing in front of
// it but the positions, and answered "nothing changed" after looking at BTC.

// PoolCoin is one active coin of the monitored pool with its fact-panel values.
type PoolCoin struct {
	InstID   string
	Rank     int    // by 12 h turnover, 1 = the most traded
	Category string // "1" = crypto
	Values   map[string]float64
}

const (
	boardSetting = "autopilot.board" // {instId: rank} of the pool at the last patrol
	boardMovers  = 5                 // gainers and losers listed
	boardExtra   = 4                 // further coins: volume, OI
	boardClimb   = 15                // ranks gained since the last patrol worth a mention
)

// pct turns a log change into percent.
func pct(v float64) float64 { return (math.Exp(v) - 1) * 100 }

func boardLine(c PoolCoin) string {
	v := c.Values
	var parts []string
	add := func(key, format string, f func(float64) float64) {
		if x, ok := v[key]; ok && !math.IsNaN(x) && !math.IsInf(x, 0) {
			parts = append(parts, fmt.Sprintf(format, f(x)))
		}
	}
	add("ret_1h", "1h %+.1f%%", pct)
	add("ret_4h", "4h %+.1f%%", pct)
	add("ret_24h", "24h %+.1f%%", pct)
	add("oi_chg_1h", "OI 1h %+.1f%%", pct)
	add("vol_surprise_4h", "量 %.1f×", math.Exp)
	add("funding_hourly", "费率 %+.3f%%/h", func(x float64) float64 { return x * 100 })
	return fmt.Sprintf("· %s（池内第 %d）%s", coin(c.InstID), c.Rank, strings.Join(parts, " · "))
}

// board renders the block and remembers the pool for the next patrol. "" while the pool has no data.
func (s *Service) board() string {
	var coins []PoolCoin
	for _, c := range s.mkt.Pool() {
		if c.Category == "" || c.Category == "1" { // the desk trades crypto only
			coins = append(coins, c)
		}
	}
	if len(coins) == 0 {
		return ""
	}
	val := func(c PoolCoin, key string) (float64, bool) {
		x, ok := c.Values[key]
		return x, ok && !math.IsNaN(x) && !math.IsInf(x, 0)
	}
	top := func(key string, n int, desc bool, keep func(float64) bool) []PoolCoin {
		var xs []PoolCoin
		for _, c := range coins {
			if x, ok := val(c, key); ok && keep(x) {
				xs = append(xs, c)
			}
		}
		sort.SliceStable(xs, func(i, j int) bool {
			a, _ := val(xs[i], key)
			b, _ := val(xs[j], key)
			if desc {
				return a > b
			}
			return a < b
		})
		return xs[:min(n, len(xs))]
	}
	var b strings.Builder
	shown := map[string]bool{}
	section := func(title string, list []PoolCoin) {
		var lines []string
		for _, c := range list {
			if !shown[c.InstID] {
				shown[c.InstID] = true
				lines = append(lines, boardLine(c))
			}
		}
		if len(lines) > 0 {
			b.WriteString(title + "\n" + strings.Join(lines, "\n") + "\n")
		}
	}
	section("1 小时涨幅前列：", top("ret_1h", boardMovers, true, func(x float64) bool { return x > 0 }))
	section("1 小时跌幅前列：", top("ret_1h", boardMovers, false, func(x float64) bool { return x < 0 }))
	section("放量最明显（近 4 小时成交额 ÷ 7 天中位数）：", top("vol_surprise_4h", boardExtra, true, func(x float64) bool { return x > math.Log(2) }))
	section("持仓 1 小时变化最大：", top("oi_chg_1h", boardExtra, true, func(x float64) bool { return x > 0.03 }))

	// what changed in the pool since the last patrol
	prev := map[string]int{}
	if raw, ok := s.st.Setting(boardSetting); ok && raw != "" {
		json.Unmarshal([]byte(raw), &prev)
	}
	now := map[string]int{}
	var fresh, climbed []string
	for _, c := range coins {
		now[c.InstID] = c.Rank
		if len(prev) == 0 {
			continue
		}
		if was, ok := prev[c.InstID]; !ok {
			fresh = append(fresh, fmt.Sprintf("%s（第 %d）", coin(c.InstID), c.Rank))
		} else if was-c.Rank >= boardClimb {
			climbed = append(climbed, fmt.Sprintf("%s（%d → %d）", coin(c.InstID), was, c.Rank))
		}
	}
	var gone []string
	for id := range prev {
		if _, ok := now[id]; !ok {
			gone = append(gone, coin(id))
		}
	}
	slices.Sort(gone)
	cut := func(xs []string) string { return strings.Join(xs[:min(8, len(xs))], "、") }
	if len(fresh)+len(climbed)+len(gone) > 0 {
		b.WriteString("自上次巡检以来池子的变化：")
		if len(fresh) > 0 {
			b.WriteString("新进池 " + cut(fresh) + "；")
		}
		if len(climbed) > 0 {
			b.WriteString("成交额排名快速上升 " + cut(climbed) + "；")
		}
		if len(gone) > 0 {
			b.WriteString("掉出池 " + cut(gone) + "；")
		}
		b.WriteString("\n")
	} else if len(prev) > 0 {
		b.WriteString("自上次巡检以来池子成员没有变化。\n")
	}
	if raw, err := json.Marshal(now); err == nil {
		s.st.PutSetting(boardSetting, string(raw))
	}
	return fmt.Sprintf("（监控池 %d 个币，数字是这一刻的事实面板；榜上的币不等于机会，要用最新数据核对）\n", len(coins)) + b.String()
}

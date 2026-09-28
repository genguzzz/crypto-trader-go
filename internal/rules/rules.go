// Package rules implements user-defined, versioned scoring rules over the fact panel.
// A rule carries no claim of validity; its usefulness is judged only by its tracked record.
package rules

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"

	"crypto-trader-go/internal/features"
)

type Gates struct {
	MaxSpreadBps float64 `json:"maxSpreadBps,omitempty"` // 0 = no gate
}

type Rule struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Version     int                `json:"version"`
	Description string             `json:"description"`
	Preset      bool               `json:"preset"`
	Long        map[string]float64 `json:"long"`
	Short       map[string]float64 `json:"short"`
	Gates       Gates              `json:"gates"`
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,40}$`)

func (r *Rule) Validate() error {
	if !idRe.MatchString(r.ID) {
		return fmt.Errorf("id 需为 2–41 位小写字母/数字/短横线")
	}
	if r.Name == "" {
		return fmt.Errorf("name 不能为空")
	}
	if len(r.Long) == 0 && len(r.Short) == 0 {
		return fmt.Errorf("至少需要一个多头或空头权重")
	}
	for _, side := range []map[string]float64{r.Long, r.Short} {
		for k, w := range side {
			f, ok := features.Lookup(k)
			if !ok || f.Key == "turnover12h" {
				return fmt.Errorf("未知或不可用于评分的字段 %q", k)
			}
			if math.IsNaN(w) || math.IsInf(w, 0) || math.Abs(w) > 10 {
				return fmt.Errorf("字段 %q 权重需在 [-10,10]", k)
			}
		}
	}
	if r.Gates.MaxSpreadBps < 0 {
		return fmt.Errorf("maxSpreadBps 不能为负")
	}
	return nil
}

func (r *Rule) JSON() string { b, _ := json.Marshal(r); return string(b) }

// Replayable reports whether every weighted field exists in hourly replay.
func (r *Rule) Replayable() bool {
	for _, side := range []map[string]float64{r.Long, r.Short} {
		for k := range side {
			if f, _ := features.Lookup(k); !f.Replayable {
				return false
			}
		}
	}
	return true
}

type Contribution struct {
	Field  string  `json:"field"`
	Weight float64 `json:"weight"`
	Pct    float64 `json:"pct"`    // pool percentile of the raw value
	Points float64 `json:"points"` // signed points added to 50
}

type SideScore struct {
	Score   *float64       `json:"score"` // nil = unrated (missing input)
	Contrib []Contribution `json:"contrib"`
	Missing []string       `json:"missing,omitempty"`
}

type Score struct {
	Long  SideScore `json:"long"`
	Short SideScore `json:"short"`
	Gate  string    `json:"gate"` // PASS / BLOCKED:<reason>
}

// Apply scores one instrument. pct(field) returns its pool percentile and whether the value exists.
// Each side: 50 + 50·Σ wᵢ·xᵢ / Σ|wᵢ|, xᵢ = 2·pctᵢ/100 − 1 ∈ [−1, 1]. Missing any weighted input → unrated.
func (r *Rule) Apply(pct func(string) (float64, bool), vals features.Values) Score {
	side := func(ws map[string]float64) SideScore {
		var s SideScore
		if len(ws) == 0 {
			return s
		}
		keys := make([]string, 0, len(ws))
		norm := 0.0
		for k, w := range ws {
			keys = append(keys, k)
			norm += math.Abs(w)
		}
		sort.Strings(keys)
		total := 50.0
		for _, k := range keys {
			p, ok := pct(k)
			if !ok {
				s.Missing = append(s.Missing, k)
				continue
			}
			pts := 50 * ws[k] * (2*p/100 - 1) / norm
			total += pts
			s.Contrib = append(s.Contrib, Contribution{Field: k, Weight: ws[k], Pct: p, Points: pts})
		}
		if len(s.Missing) == 0 && norm > 0 {
			v := math.Round(math.Max(0, math.Min(100, total))*10) / 10
			s.Score = &v
		}
		return s
	}
	out := Score{Long: side(r.Long), Short: side(r.Short), Gate: "PASS"}
	if r.Gates.MaxSpreadBps > 0 {
		if sp, ok := vals["spread_bps"]; ok && sp > r.Gates.MaxSpreadBps {
			out.Gate = fmt.Sprintf("BLOCKED:点差 %.1fbps > %.1f", sp, r.Gates.MaxSpreadBps)
		}
	}
	return out
}

// Presets are comparison baselines, all unvalidated. They are NOT recommendations.
func Presets() []Rule {
	return []Rule{
		{ID: "trend-oi-confirm", Name: "趋势+OI 确认（旧 v0 方向）", Version: 1, Preset: true,
			Description: "旧蓝图 entry-core-v0 的方向：价涨+币本位 OI 扩张+放量看多，对称看空。2026-09-26 实测 1h/4h Rank IC 为负，保留作对照。",
			Long:        map[string]float64{"trend_z_4h": 0.45, "oi_chg_4h": 0.30, "vol_surprise_4h": 0.25},
			Short:       map[string]float64{"trend_z_4h": -0.45, "oi_chg_4h": 0.30, "vol_surprise_4h": 0.25},
			Gates:       Gates{MaxSpreadBps: 15}},
		{ID: "short-term-reversal", Name: "短期反转（相对 BTC）", Version: 1, Preset: true,
			Description: "相对 BTC 4h 超额收益越低越看多、越高越看空。来自 51 天探索性研究（4h IC −0.042 的反向），未经样本外验证。",
			Long:        map[string]float64{"excess_btc_4h": -1},
			Short:       map[string]float64{"excess_btc_4h": 1},
			Gates:       Gates{MaxSpreadBps: 15}},
		{ID: "funding-tilt", Name: "资金费倾向", Version: 1, Preset: true,
			Description: "已结算资金费（按小时换算）越高越看多。探索性研究中与未来收益正相关（4h IC +0.034），与“高费率=拥挤”直觉相反，未验证。",
			Long:        map[string]float64{"funding_hourly": 1},
			Short:       map[string]float64{"funding_hourly": -1},
			Gates:       Gates{MaxSpreadBps: 15}},
		// 2026-09-28 research baselines (docs/research-trend-signals.md), also unvalidated.
		{ID: "resid-reversal", Name: "残差反转（β 中性）", Version: 1, Preset: true,
			Description: "剔除 BTC 带动（按 48h β）后，自身 1h/4h 跌得多的看多、涨得多的看空。短期反转 + 残差化（Blitz 等 2011），用来检验山寨相对大盘的超涨超跌是否回归。",
			Long:        map[string]float64{"resid_4h": -0.6, "resid_1h": -0.4},
			Short:       map[string]float64{"resid_4h": 0.6, "resid_1h": 0.4},
			Gates:       Gates{MaxSpreadBps: 15}},
		{ID: "ema-trend", Name: "趋势跟随（EMA + 效率比）", Version: 1, Preset: true,
			Description: "时间序列动量：波动率归一化的 EMA8−EMA24（Baz 等 2015 的 MACD 类信号），并偏好走得干脆（Kaufman 效率比高）的币。经典 CTA 做法，文献中在日/周级有效，小时级待检验。",
			Long:        map[string]float64{"trend_ema": 0.7, "eff_ratio_24h": 0.3},
			Short:       map[string]float64{"trend_ema": -0.7, "eff_ratio_24h": 0.3},
			Gates:       Gates{MaxSpreadBps: 15}},
		{ID: "fast-resid-momentum", Name: "快速残差动量（15m，仅实时）", Version: 1, Preset: true,
			Description: "15 分钟与 1 小时 β 中性残差收益越强越看多。反应最快的一条，但 15m 字段回放中没有，只能积累上线后战绩；若战绩显示方向相反，就是短线反转。",
			Long:        map[string]float64{"resid_15m": 0.6, "resid_1h": 0.4},
			Short:       map[string]float64{"resid_15m": -0.6, "resid_1h": -0.4},
			Gates:       Gates{MaxSpreadBps: 15}},
	}
}

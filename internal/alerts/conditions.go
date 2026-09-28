// Package alerts evaluates user monitoring rules on every confirmed minute and delivers fired
// alerts to Telegram and/or a webhook. It only reads the engine's public-market snapshot.
//
// A rule type is a Condition parsed from JSON params; adding a type = one parse function in
// Types. Every type is edge-triggered (fires when its condition turns true for an instrument)
// and additionally rate-limited by the rule's cooldown per instrument. A rule's first evaluation
// after startup or save only records state, so conditions that already hold never fire.
package alerts

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/rules"
)

// Env is what rule parsing needs from the engine (market.Engine satisfies it).
type Env interface {
	Rule(id string) (rules.Rule, bool)
}

// Condition is a parsed rule type. Check reports the measured value, whether it is known (missing
// inputs leave the edge state unchanged) and whether the condition holds.
type Condition interface {
	Check(r *market.Row) (v float64, known, hit bool)
	Message(instID string, v float64) string // one line for the notification
	Summary() string                         // params in words, for the rules list
}

// Type is one registered rule type.
type Type struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// parse validates raw params and returns the condition plus the normalised params to store.
	parse func(raw json.RawMessage, env Env) (Condition, any, error)
}

var Types = []Type{
	{Key: "price_move", Label: "价格异动", parse: parsePriceMove},
	{Key: "score", Label: "规则分数", parse: parseScore},
	{Key: "funding", Label: "资金费", parse: parseFunding},
}

func lookupType(key string) (Type, bool) {
	for _, t := range Types {
		if t.Key == key {
			return t, true
		}
	}
	return Type{}, false
}

func decodeStrict(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("参数格式错误：%v", err)
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func dirHit(dir string, v, th float64) bool {
	switch dir {
	case "up":
		return v >= th
	case "down":
		return v <= -th
	default:
		return math.Abs(v) >= th
	}
}

var dirWord = map[string]string{"up": "涨", "down": "跌", "both": "涨跌"}

// ---------- price_move: |simple return over a window| ≥ X% ----------

type PriceMove struct {
	Window string  `json:"window"` // 15m | 1h | 4h | 24h (fact-panel ret_* fields)
	Pct    float64 `json:"pct"`    // threshold in percent, > 0
	Dir    string  `json:"dir"`    // up | down | both
}

var priceWindows = map[string]string{"15m": "ret_15m", "1h": "ret_1h", "4h": "ret_4h", "24h": "ret_24h"}

func parsePriceMove(raw json.RawMessage, _ Env) (Condition, any, error) {
	p := PriceMove{Window: "1h", Dir: "both"}
	if err := decodeStrict(raw, &p); err != nil {
		return nil, nil, err
	}
	if _, ok := priceWindows[p.Window]; !ok {
		return nil, nil, fmt.Errorf("窗口需为 15m / 1h / 4h / 24h")
	}
	if !finite(p.Pct) || p.Pct <= 0 || p.Pct > 100 {
		return nil, nil, fmt.Errorf("涨跌幅阈值需在 (0, 100]%%")
	}
	if _, ok := dirWord[p.Dir]; !ok {
		return nil, nil, fmt.Errorf("方向需为 up / down / both")
	}
	return p, p, nil
}

func (p PriceMove) Check(r *market.Row) (float64, bool, bool) {
	lr, ok := r.Values[priceWindows[p.Window]]
	if !ok || !finite(lr) {
		return 0, false, false
	}
	v := (math.Exp(lr) - 1) * 100 // simple % change, as traders read it
	return v, true, dirHit(p.Dir, v, p.Pct)
}

func (p PriceMove) Message(inst string, v float64) string {
	return fmt.Sprintf("%s %s %+.2f%%（阈值 %s %g%%）", inst, p.Window, v, dirWord[p.Dir], p.Pct)
}

func (p PriceMove) Summary() string {
	return fmt.Sprintf("%s %s ≥ %g%%", p.Window, dirWord[p.Dir], p.Pct)
}

// ---------- score: a scoring rule's long/short score crosses up through a threshold ----------

type ScoreParams struct {
	RuleID string  `json:"ruleId"`
	Side   string  `json:"side"` // long | short
	Min    float64 `json:"min"`  // 0–100
	name   string
}

func parseScore(raw json.RawMessage, env Env) (Condition, any, error) {
	p := ScoreParams{Side: "long", Min: 80}
	if err := decodeStrict(raw, &p); err != nil {
		return nil, nil, err
	}
	if p.RuleID == "" {
		return nil, nil, fmt.Errorf("请选择评分规则")
	}
	if env != nil {
		r, ok := env.Rule(p.RuleID)
		if !ok {
			return nil, nil, fmt.Errorf("评分规则 %q 不存在", p.RuleID)
		}
		p.name = r.Name
	}
	if p.Side != "long" && p.Side != "short" {
		return nil, nil, fmt.Errorf("方向需为 long / short")
	}
	if !finite(p.Min) || p.Min <= 0 || p.Min > 100 {
		return nil, nil, fmt.Errorf("分数阈值需在 (0, 100]")
	}
	return p, p, nil
}

var sideWord = map[string]string{"long": "多", "short": "空"}

func (p ScoreParams) Check(r *market.Row) (float64, bool, bool) {
	sc, ok := r.Scores[p.RuleID]
	if !ok {
		return 0, false, false
	}
	s := sc.Long
	if p.Side == "short" {
		s = sc.Short
	}
	if s.Score == nil {
		return 0, false, false
	}
	return *s.Score, true, *s.Score >= p.Min
}

func (p ScoreParams) ruleName() string {
	if p.name != "" {
		return p.name
	}
	return p.RuleID
}

func (p ScoreParams) Message(inst string, v float64) string {
	return fmt.Sprintf("%s 「%s」%s头分 %.1f（≥ %g）", inst, p.ruleName(), sideWord[p.Side], v, p.Min)
}

func (p ScoreParams) Summary() string {
	return fmt.Sprintf("「%s」%s头分 ≥ %g", p.ruleName(), sideWord[p.Side], p.Min)
}

// ---------- funding: |funding rate per hour| ≥ X% ----------

type FundingParams struct {
	Source     string  `json:"source"`     // settled (funding_hourly) | predicted (funding_pred_hourly, live)
	PctPerHour float64 `json:"pctPerHour"` // threshold, % per hour
	Dir        string  `json:"dir"`        // up (positive) | down (negative) | both
}

var fundingFields = map[string]string{"settled": "funding_hourly", "predicted": "funding_pred_hourly"}
var fundingWord = map[string]string{"settled": "已结算资金费", "predicted": "预测资金费"}
var fundingDir = map[string]string{"up": "正", "down": "负", "both": "±"}

func parseFunding(raw json.RawMessage, _ Env) (Condition, any, error) {
	p := FundingParams{Source: "settled", Dir: "both"}
	if err := decodeStrict(raw, &p); err != nil {
		return nil, nil, err
	}
	if _, ok := fundingFields[p.Source]; !ok {
		return nil, nil, fmt.Errorf("来源需为 settled / predicted")
	}
	if !finite(p.PctPerHour) || p.PctPerHour <= 0 || p.PctPerHour > 1 {
		return nil, nil, fmt.Errorf("资金费阈值需在 (0, 1]%%/小时")
	}
	if _, ok := fundingDir[p.Dir]; !ok {
		return nil, nil, fmt.Errorf("方向需为 up / down / both")
	}
	return p, p, nil
}

func (p FundingParams) Check(r *market.Row) (float64, bool, bool) {
	f, ok := r.Values[fundingFields[p.Source]]
	if !ok || !finite(f) {
		return 0, false, false
	}
	v := f * 100 // % per hour
	return v, true, dirHit(p.Dir, v, p.PctPerHour)
}

func (p FundingParams) Message(inst string, v float64) string {
	return fmt.Sprintf("%s %s %+.4f%%/小时（阈值 %s%g%%）", inst, fundingWord[p.Source], v, fundingDir[p.Dir], p.PctPerHour)
}

func (p FundingParams) Summary() string {
	return fmt.Sprintf("%s %s%g%%/小时", fundingWord[p.Source], fundingDir[p.Dir], p.PctPerHour)
}

package signals

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"crypto-trader-go/internal/features"
)

// Spec is one condition: a leaf (price, zone, field, moverisk, alert, time, plan) or a composite
// (all / any of Of). A subscription fires when its condition turns from false to true (an edge), so a
// condition that already holds when subscribing is rejected: it would never be news.
//
//	price    {instId, op: above|below, level, bar?}   bar "" = last trade price; 1m|5m|15m|1H|4H = that bar's confirmed close
//	zone     {instId, lo, hi}                          last price inside [lo, hi], or a closed 1m bar that traded into it
//	field    {instId, field, op: >|>=|<|<=, value}    a fact-panel field (features.Fields), e.g. funding_hourly > 0
//	moverisk {instId, side: top|bottom, metric: pTail|pBig3|extended, op, value}
//	                                                   continuation risk on confirmed 1H bars; extended: value 1 = enters, 0 = leaves the extended state
//	alert    {ruleId, instId?}                         an alert rule fires (for that instrument)
//	time     {at: "2026-11-02 10:00" (UTC+8) | afterMin}
//	plan     {planId, state: active|tp1|done|stopped|invalid|missed|expired|settled}
//	script   {code}                                    a ctg.Condition class run on its timeframe's bar close (script.go);
//	                                                   validation stores what it declares in script; one per subscription
//	all|any  {of: [...]}                               at most 6 leaves, 2 levels deep
type Spec struct {
	Type   string      `json:"type"`
	InstID string      `json:"instId,omitempty"`
	Op     string      `json:"op,omitempty"`
	Level  float64     `json:"level,omitempty"`
	Bar    string      `json:"bar,omitempty"`
	Lo     float64     `json:"lo,omitempty"`
	Hi     float64     `json:"hi,omitempty"`
	Field  string      `json:"field,omitempty"`
	Value  *float64    `json:"value,omitempty"`
	Side   string      `json:"side,omitempty"`
	Metric string      `json:"metric,omitempty"`
	RuleID int64       `json:"ruleId,omitempty"`
	At     string      `json:"at,omitempty"`       // time input (UTC+8 or RFC 3339); normalised to AtMs
	After  int         `json:"afterMin,omitempty"` // time input: minutes from now; normalised to AtMs
	AtMs   int64       `json:"atMs,omitempty"`
	PlanID int64       `json:"planId,omitempty"`
	State  string      `json:"state,omitempty"`
	Code   string      `json:"code,omitempty"`   // script: Python source
	Script *ScriptMeta `json:"script,omitempty"` // script: its declarations, filled by validation
	Of     []Spec      `json:"of,omitempty"`
	// Every (root only) is how often it is checked: tick (each ~2 s price broadcast), 1m, 5m, 15m or 1H
	// (on that confirmed minute). "" = tick when a leaf reads the last price (and there is no script),
	// else 1m. Coarser = cheaper and calmer; a brief spike between checks can be missed. A script runs
	// on its own bar close, so its effective cadence is the slower of every and its timeframe.
	Every string `json:"every,omitempty"`
}

// everyMs are the check intervals coarser than a tick.
var everyMs = map[string]int64{"1m": 60_000, "5m": 300_000, "15m": 900_000, "1H": 3_600_000}

// interval is the effective check interval: "tick" or a key of everyMs.
func (s Spec) interval() string {
	if s.Every == "" {
		if s.live() && s.scripts() == 0 {
			return "tick"
		}
		return "1m"
	}
	return s.Every
}

// due reports whether a minute event (open time of the confirmed 1m bar) is a check for this spec.
func (s Spec) due(minute int64) bool {
	ms, ok := everyMs[s.interval()]
	return !ok || ms == 60_000 || (minute+60_000)%ms == 0
}

var everyText = map[string]string{"tick": "实时", "1m": "每分钟", "5m": "每 5 分钟", "15m": "每 15 分钟", "1H": "每小时"}

const (
	maxLeaves = 6
	maxDepth  = 2
)

// Bars a price condition may close on, and their length.
var barMs = map[string]int64{"1m": 60_000, "5m": 300_000, "15m": 900_000, "1H": 3_600_000, "4H": 14_400_000}

var planStates = map[string]string{"active": "触及入场区", "tp1": "到达 TP1", "done": "止盈完成", "stopped": "止损",
	"invalid": "入场前破止损作废", "missed": "未入场先到 TP1（错过）", "expired": "过期", "settled": "结束（任一结局）"}

var tz8 = time.FixedZone("UTC+8", 8*3600)

// ParseSpec decodes a spec strictly: unknown keys are an error (the agent learns the shape from it).
func ParseSpec(raw string) (Spec, error) {
	var s Spec
	dec := json.NewDecoder(bytes.NewReader([]byte(strings.TrimSpace(raw))))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return s, fmt.Errorf("spec is not valid JSON for a signal: %v", err)
	}
	return s, nil
}

// NormalizeInst accepts "AAVE", "aave-usdt" or "AAVE-USDT-SWAP".
func NormalizeInst(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch {
	case s == "":
		return ""
	case strings.HasSuffix(s, "-USDT-SWAP"):
		return s
	case strings.HasSuffix(s, "-USDT"):
		return s + "-SWAP"
	case !strings.Contains(s, "-"):
		return s + "-USDT-SWAP"
	}
	return s
}

// normalize validates the spec against what exists (instruments, fields, rules, plans) and puts it in
// canonical form (instIds, ops, absolute times). now is the subscription time.
func (s *Spec) normalize(src Sources, now time.Time, depth int) error {
	s.Type = strings.ToLower(strings.TrimSpace(s.Type))
	if s.Every != "" {
		if depth > 0 {
			return errors.New("every belongs to the whole subscription, not to a condition inside all/any")
		}
		if e := strings.TrimSpace(s.Every); e == "1h" {
			s.Every = "1H"
		} else {
			s.Every = e
		}
		if _, ok := everyText[s.Every]; !ok {
			return fmt.Errorf("every must be tick, 1m, 5m, 15m or 1H (got %q)", s.Every)
		}
	}
	if s.Type == "all" || s.Type == "any" {
		if depth >= maxDepth {
			return fmt.Errorf("%s: composites nest at most %d levels", s.Type, maxDepth)
		}
		if len(s.Of) < 2 {
			return fmt.Errorf("%s needs at least two conditions in of", s.Type)
		}
		if s.InstID != "" || s.Op != "" || s.Value != nil || s.Level != 0 {
			return fmt.Errorf("%s takes only of", s.Type)
		}
		for i := range s.Of {
			if err := s.Of[i].normalize(src, now, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if len(s.Of) > 0 {
		return fmt.Errorf("%s: of is only for all / any", s.Type)
	}
	inst := func(required bool) error {
		s.InstID = NormalizeInst(s.InstID)
		if s.InstID == "" {
			if required {
				return fmt.Errorf("%s needs instId", s.Type)
			}
			return nil
		}
		if !src.Known(s.InstID) {
			return fmt.Errorf("unknown instrument %s (OKX USDT perpetual ids like AAVE-USDT-SWAP)", s.InstID)
		}
		return nil
	}
	positive := func(name string, v float64) error {
		if !(v > 0) || math.IsInf(v, 0) {
			return fmt.Errorf("%s: %s must be a positive price", s.Type, name)
		}
		return nil
	}
	switch s.Type {
	case "price":
		if err := inst(true); err != nil {
			return err
		}
		switch s.Op {
		case "above", ">", ">=":
			s.Op = "above"
		case "below", "<", "<=":
			s.Op = "below"
		default:
			return errors.New("price: op must be above or below")
		}
		if err := positive("level", s.Level); err != nil {
			return err
		}
		if _, ok := barMs[s.Bar]; s.Bar != "" && !ok {
			return errors.New("price: bar must be empty (last price) or one of 1m, 5m, 15m, 1H, 4H (confirmed close)")
		}
		if px, _, ok := src.Quote(s.InstID); ok && px > 0 && math.Abs(s.Level-px)/px > 0.5 {
			return fmt.Errorf("price: level %s is more than 50%% away from the current price %s", fmtPx(s.Level), fmtPx(px))
		}
	case "zone":
		if err := inst(true); err != nil {
			return err
		}
		if err := positive("lo", s.Lo); err != nil {
			return err
		}
		if err := positive("hi", s.Hi); err != nil {
			return err
		}
		if s.Lo > s.Hi {
			s.Lo, s.Hi = s.Hi, s.Lo
		}
		if (s.Hi-s.Lo)/s.Lo > 0.2 {
			return errors.New("zone: lo–hi is wider than 20%; use a price condition instead")
		}
		if px, _, ok := src.Quote(s.InstID); ok && px > 0 && math.Abs((s.Lo+s.Hi)/2-px)/px > 0.5 {
			return fmt.Errorf("zone: %s–%s is more than 50%% away from the current price %s", fmtPx(s.Lo), fmtPx(s.Hi), fmtPx(px))
		}
	case "field":
		if err := inst(true); err != nil {
			return err
		}
		f, ok := features.Lookup(s.Field)
		if !ok {
			keys := make([]string, 0, len(features.Fields))
			for _, f := range features.Fields {
				keys = append(keys, f.Key)
			}
			return fmt.Errorf("field: unknown field %q (one of %s)", s.Field, strings.Join(keys, ", "))
		}
		if f.Key == "turnover12h" {
			return errors.New("field: turnover12h is not a signal field")
		}
		if err := cmpOp(s); err != nil {
			return err
		}
		if s.Value == nil || math.IsNaN(*s.Value) || math.IsInf(*s.Value, 0) {
			return errors.New("field: value is required (in the field's own unit: log returns, rate per hour, z, bps…)")
		}
	case "moverisk":
		if err := inst(true); err != nil {
			return err
		}
		if s.Side != "top" && s.Side != "bottom" {
			return errors.New("moverisk: side must be top (the move up a short would fade) or bottom (the move down)")
		}
		switch s.Metric {
		case "pTail", "pBig3":
			if err := cmpOp(s); err != nil {
				return err
			}
			if s.Value == nil || *s.Value <= 0 || *s.Value >= 1 {
				return errors.New("moverisk: value is a probability between 0 and 1 (e.g. 0.4 for 40%)")
			}
		case "extended":
			if s.Value == nil || (*s.Value != 0 && *s.Value != 1) {
				return errors.New("moverisk: extended takes value 1 (enters the extended state) or 0 (leaves it)")
			}
			s.Op = ""
		default:
			return errors.New("moverisk: metric must be pTail, pBig3 or extended")
		}
	case "alert":
		if err := inst(false); err != nil {
			return err
		}
		if _, ok := src.AlertRule(s.RuleID); !ok {
			return fmt.Errorf("alert: unknown alert rule %d", s.RuleID)
		}
	case "time":
		switch {
		case s.After > 0 && s.At == "":
			s.AtMs = now.Add(time.Duration(s.After) * time.Minute).UnixMilli()
		case s.At != "" && s.After == 0:
			t, err := parseAt(s.At)
			if err != nil {
				return err
			}
			s.AtMs = t.UnixMilli()
		case s.AtMs > 0 && s.At == "" && s.After == 0: // stored form
		default:
			return errors.New("time: give either at (\"2026-11-02 10:00\", UTC+8) or afterMin")
		}
		s.At, s.After = "", 0
		if s.AtMs <= now.UnixMilli() {
			return errors.New("time: the time is already past")
		}
	case "script":
		if s.InstID != "" || s.Op != "" || s.Value != nil || s.Level != 0 || s.Bar != "" {
			return errors.New("script takes only code: its instruments and timeframe are declared in the code")
		}
		if strings.TrimSpace(s.Code) == "" {
			return errors.New("script: code is required (a ctg Condition class; test it with test_signal_script)")
		}
		if len(s.Code) > MaxScriptBytes {
			return fmt.Errorf("script: the code is %d bytes; the cap is %d", len(s.Code), MaxScriptBytes)
		}
		if s.Script == nil || len(s.Script.InstIDs) == 0 {
			return errors.New("script: not validated")
		}
		if _, ok := barMs[s.Script.Timeframe]; !ok {
			return errors.New("script: timeframe must be 1m, 5m, 15m, 1H or 4H")
		}
		for i, id := range s.Script.InstIDs {
			s.Script.InstIDs[i] = NormalizeInst(id)
			if !src.Known(s.Script.InstIDs[i]) {
				return fmt.Errorf("unknown instrument %s (OKX USDT perpetual ids like AAVE-USDT-SWAP)", id)
			}
		}
	case "plan":
		p, ok := src.Plan(s.PlanID)
		if !ok {
			return fmt.Errorf("plan: unknown plan %d", s.PlanID)
		}
		if _, ok := planStates[s.State]; !ok {
			return errors.New("plan: state must be one of active, tp1, done, stopped, invalid, missed, expired, settled")
		}
		if !p.Open() {
			return fmt.Errorf("plan: plan #%d is already settled (%s)", p.ID, p.Status)
		}
		s.InstID = p.InstID
	default:
		return fmt.Errorf("unknown signal type %q (price, zone, field, moverisk, alert, time, plan, script, all, any)", s.Type)
	}
	return nil
}

func cmpOp(s *Spec) error {
	switch s.Op {
	case ">", ">=", "<", "<=":
		return nil
	case "above":
		s.Op = ">"
		return nil
	case "below":
		s.Op = "<"
		return nil
	}
	return fmt.Errorf("%s: op must be >, >=, < or <=", s.Type)
}

func parseAt(v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, v, tz8); err == nil {
			return t, nil
		}
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("time: cannot read at %q (use \"2026-11-02 10:00\", UTC+8)", v)
}

// leaves returns the leaf conditions in order.
func (s Spec) leaves() []Spec {
	if s.Type == "all" || s.Type == "any" {
		var out []Spec
		for _, c := range s.Of {
			out = append(out, c.leaves()...)
		}
		return out
	}
	return []Spec{s}
}

// InstIDs are the instruments the spec is about, in order of appearance.
// slowOK are the coins a subscription may check less often than once a minute; any other coin (alts
// move fast) is checked at least every minute, whatever was asked (owner, 2026-11-01).
var slowOK = map[string]bool{"BTC-USDT-SWAP": true, "ETH-USDT-SWAP": true}

// capEvery lowers a 5m / 15m / 1H interval to 1m when the subscription watches anything but BTC / ETH
// (scripts excepted: their timeframe sets their cadence).
func (s *Spec) capEvery() bool {
	if ms, ok := everyMs[s.Every]; !ok || ms <= 60_000 {
		return false
	}
	for _, l := range s.leaves() {
		if l.Type == "script" { // a script runs on its own timeframe's close; every only spaces those runs
			return false
		}
	}
	for _, id := range s.InstIDs() {
		if !slowOK[id] {
			s.Every = "1m"
			return true
		}
	}
	return false
}

// Label is the subscription's one-line description: the condition, plus the check interval when it
// isn't the default.
func (s Spec) Label() string {
	desc := s.Describe()
	auto := s
	auto.Every = ""
	if s.Every != "" && s.Every != auto.interval() {
		desc += "（" + everyText[s.Every] + "检查）"
	}
	return desc
}

func (s Spec) InstIDs() []string {
	var out []string
	for _, l := range s.leaves() {
		ids := []string{l.InstID}
		if l.Script != nil {
			ids = l.Script.InstIDs
		}
		for _, id := range ids {
			if id != "" && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// ---------- Chinese descriptions ----------

func coin(inst string) string { return strings.Split(inst, "-")[0] }

// fmtPx prints a price with up to 6 significant digits and no trailing zeros.
func fmtPx(v float64) string {
	if v == 0 {
		return "0"
	}
	p := math.Pow(10, float64(min(max(0, 5-int(math.Floor(math.Log10(math.Abs(v))))), 10)))
	return strconv.FormatFloat(math.Round(v*p)/p, 'f', -1, 64)
}

var opText = map[string]string{">": ">", ">=": "≥", "<": "<", "<=": "≤"}

// fieldText formats a field value in its unit for display (log returns as %, rates as %/h).
func fieldText(key string, v float64) string {
	f, _ := features.Lookup(key)
	switch f.Unit {
	case "log":
		if key == "vol_surprise_4h" {
			return strconv.FormatFloat(math.Exp(v), 'f', 2, 64) + "×"
		}
		return fmt.Sprintf("%+.2f%%", (math.Exp(v)-1)*100)
	case "rate/h":
		return fmt.Sprintf("%.4f%%/h", v*100)
	case "bps":
		return fmt.Sprintf("%.1f bps", v)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// Describe is the condition in Chinese for the owner ("AAVE 1H 收盘站上 140.2", "AAVE 进入 139.5–140.2").
func (s Spec) Describe() string {
	switch s.Type {
	case "all", "any":
		sep := " 且 "
		if s.Type == "any" {
			sep = " 或 "
		}
		parts := make([]string, len(s.Of))
		for i, c := range s.Of {
			parts[i] = c.Describe()
			if c.Type == "all" || c.Type == "any" {
				parts[i] = "（" + parts[i] + "）"
			}
		}
		return strings.Join(parts, sep)
	case "price":
		verb := map[bool]string{true: "站上", false: "跌破"}[s.Op == "above"]
		if s.Bar == "" {
			verb = map[bool]string{true: "上破", false: "跌破"}[s.Op == "above"]
			return fmt.Sprintf("%s 价格%s %s", coin(s.InstID), verb, fmtPx(s.Level))
		}
		return fmt.Sprintf("%s %s 收盘%s %s", coin(s.InstID), s.Bar, verb, fmtPx(s.Level))
	case "zone":
		return fmt.Sprintf("%s 进入 %s–%s", coin(s.InstID), fmtPx(s.Lo), fmtPx(s.Hi))
	case "field":
		v := 0.0
		if s.Value != nil {
			v = *s.Value
		}
		if s.Field == "funding_hourly" || s.Field == "funding_pred_hourly" {
			name := map[bool]string{true: "资金费", false: "预测资金费"}[s.Field == "funding_hourly"]
			if v == 0 && (s.Op == ">" || s.Op == ">=") {
				return coin(s.InstID) + " " + name + "转正"
			}
			if v == 0 && (s.Op == "<" || s.Op == "<=") {
				return coin(s.InstID) + " " + name + "转负"
			}
		}
		f, _ := features.Lookup(s.Field)
		return fmt.Sprintf("%s %s %s %s", coin(s.InstID), f.Label, opText[s.Op], fieldText(s.Field, v))
	case "moverisk":
		side := map[bool]string{true: "上涨端", false: "下跌端"}[s.Side == "top"]
		v := 0.0
		if s.Value != nil {
			v = *s.Value
		}
		if s.Metric == "extended" {
			if v == 1 {
				return fmt.Sprintf("%s %s进入超伸状态", coin(s.InstID), side)
			}
			return fmt.Sprintf("%s %s离开超伸状态", coin(s.InstID), side)
		}
		name := map[bool]string{true: "P_tail", false: "P_big3"}[s.Metric == "pTail"]
		return fmt.Sprintf("%s %s %s %s %.0f%%", coin(s.InstID), side, name, opText[s.Op], v*100)
	case "alert":
		if s.InstID != "" {
			return fmt.Sprintf("告警规则 #%d 在 %s 触发", s.RuleID, coin(s.InstID))
		}
		return fmt.Sprintf("告警规则 #%d 触发", s.RuleID)
	case "time":
		return "到 " + time.UnixMilli(s.AtMs).In(tz8).Format("01-02 15:04")
	case "plan":
		return fmt.Sprintf("计划 #%d %s", s.PlanID, planStates[s.State])
	case "script":
		if s.Script == nil {
			return "脚本信号"
		}
		return fmt.Sprintf("脚本 %s · %s", s.Script.Timeframe, s.Script.Desc)
	}
	return s.Type
}

// Level is a price to draw on the K-line for a subscription.
type Level struct {
	InstID string  `json:"instId"`
	Px     float64 `json:"px"`
	Label  string  `json:"label"`
}

// levels are the price lines of the spec's price and zone conditions.
func (s Spec) levels() []Level {
	var out []Level
	for _, l := range s.leaves() {
		switch l.Type {
		case "price":
			lab := map[bool]string{true: "上破", false: "跌破"}[l.Op == "above"]
			if l.Bar != "" {
				lab = l.Bar + " " + lab
			}
			out = append(out, Level{l.InstID, l.Level, lab})
		case "zone":
			out = append(out, Level{l.InstID, l.Hi, "区间上沿"}, Level{l.InstID, l.Lo, "区间下沿"})
		}
	}
	return out
}

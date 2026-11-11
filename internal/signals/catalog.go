package signals

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"crypto-trader-go/internal/features"
)

// The catalog is the one description of every condition type: its parameters, how often it is
// checked, whether it can be replayed, and an example. The app's editor, the agent's tool text and
// the docs read it (GET /api/v1/signals/catalog), so a new type is described in one place.

type ParamInfo struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"` // string | number | integer | []string | object
	Required bool     `json:"required,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Desc     string   `json:"desc"`
}

type TypeInfo struct {
	Type       string      `json:"type"`
	Label      string      `json:"label"`
	Desc       string      `json:"desc"`
	Params     []ParamInfo `json:"params"`
	Checked    string      `json:"checked"` // when it is evaluated
	Replayable bool        `json:"replayable"`
	Example    string      `json:"example"`
}

// Catalog lists the condition types.
func Catalog() []TypeInfo {
	inst := ParamInfo{Name: "instId", Type: "string", Required: true, Desc: "OKX USDT perpetual (AAVE, AAVE-USDT-SWAP)"}
	cmp := ParamInfo{Name: "op", Type: "string", Required: true, Enum: []string{">", ">=", "<", "<="}}
	fields := make([]string, 0, len(features.Fields))
	for _, f := range features.Fields {
		if f.Key != "turnover12h" {
			fields = append(fields, f.Key)
		}
	}
	return []TypeInfo{
		{Type: "price", Label: "价格", Desc: "最新价上破 / 跌破某价，或某周期 K 线收盘站上 / 跌破", Checked: "tick (bar empty) or that bar's close", Replayable: true,
			Params: []ParamInfo{inst, {Name: "op", Type: "string", Required: true, Enum: []string{"above", "below"}},
				{Name: "level", Type: "number", Required: true, Desc: "price, within 50% of now"},
				{Name: "bar", Type: "string", Enum: []string{"", "1m", "5m", "15m", "1H", "4H"}, Desc: "empty = last price; else the confirmed close"}},
			Example: `{"type":"price","instId":"AAVE-USDT-SWAP","op":"above","level":140.2,"bar":"1H"}`},
		{Type: "zone", Label: "区间", Desc: "价格进入 lo–hi（或 1m K 线触及）", Checked: "tick + each minute (wick)", Replayable: true,
			Params:  []ParamInfo{inst, {Name: "lo", Type: "number", Required: true}, {Name: "hi", Type: "number", Required: true, Desc: "≤ 20% wide"}},
			Example: `{"type":"zone","instId":"AAVE-USDT-SWAP","lo":139.5,"hi":140.2}`},
		{Type: "field", Label: "指标字段", Desc: "单个币的事实面板字段越过阈值", Checked: "each minute",
			Params: []ParamInfo{inst, {Name: "field", Type: "string", Required: true, Enum: fields}, cmp,
				{Name: "value", Type: "number", Required: true, Desc: "in the field's unit (log returns, rate per hour, …)"}},
			Example: `{"type":"field","instId":"AAVE-USDT-SWAP","field":"funding_hourly","op":">","value":0}`},
		{Type: "moverisk", Label: "延续风险", Desc: "rev 的 P_tail / P_big3 越过概率，或进入 / 离开超伸", Checked: "each confirmed 1H bar",
			Params: []ParamInfo{inst, {Name: "side", Type: "string", Required: true, Enum: []string{"top", "bottom"}},
				{Name: "metric", Type: "string", Required: true, Enum: []string{"pTail", "pBig3", "extended"}}, {Name: "op", Type: "string", Enum: []string{">", ">=", "<", "<="}},
				{Name: "value", Type: "number", Required: true, Desc: "probability 0–1; extended: 1 enters, 0 leaves"}},
			Example: `{"type":"moverisk","instId":"AAVE-USDT-SWAP","side":"top","metric":"pTail","op":"<","value":0.4}`},
		{Type: "alert", Label: "告警规则", Desc: "一条告警规则触发（可限定币）", Checked: "when the rule fires",
			Params:  []ParamInfo{{Name: "ruleId", Type: "integer", Required: true}, {Name: "instId", Type: "string"}},
			Example: `{"type":"alert","ruleId":12}`},
		{Type: "time", Label: "定时", Desc: "到某个时间（UTC+8）或若干分钟后", Checked: "tick", Replayable: true,
			Params:  []ParamInfo{{Name: "at", Type: "string", Desc: "2026-11-02 10:00 (UTC+8)"}, {Name: "afterMin", Type: "integer"}},
			Example: `{"type":"time","afterMin":120}`},
		{Type: "plan", Label: "AI 计划", Desc: "AI 计划到达某状态", Checked: "each minute",
			Params: []ParamInfo{{Name: "planId", Type: "integer", Required: true},
				{Name: "state", Type: "string", Required: true, Enum: []string{"active", "tp1", "done", "stopped", "invalid", "missed", "expired", "settled"}}},
			Example: `{"type":"plan","planId":34,"state":"active"}`},
		{Type: "script", Label: "脚本", Desc: "ctg.Condition 类（Python SDK），在自己的周期收盘时运行", Checked: "its timeframe's close",
			Params:  []ParamInfo{{Name: "code", Type: "string", Required: true, Desc: "tested with test_signal_script"}},
			Example: `{"type":"script"} + code`},
		{Type: "pool", Label: "全池扫描", Desc: "扫描一组币（监控池 / 前 N / 自选 / 列表），每个币各自判断涨跌幅、振幅或字段，同一分钟触发的币合并投递",
			Checked: "each confirmed minute (every 1m) or each tick", Replayable: true,
			Params: []ParamInfo{{Name: "scope", Type: "string", Enum: []string{"pool", "watch", "list"}, Desc: "default pool (the active pool)"},
				{Name: "top", Type: "integer", Desc: "scope pool: only ranks ≤ top"},
				{Name: "instIds", Type: "[]string", Desc: "scope list: 1–50 instruments"},
				{Name: "exclude", Type: "[]string", Desc: "never these"},
				{Name: "metric", Type: "string", Required: true, Enum: []string{"move", "range", "field", "combo"}},
				{Name: "legs", Type: "object", Desc: "metric combo: 2–4 {metric: move|range|field, …} that must all hold for the same coin (the first leg's value ranks the hits)"},
				{Name: "windowMin", Type: "integer", Desc: "move / range: 1–1440 minutes"},
				{Name: "dir", Type: "string", Enum: []string{"up", "down", "both"}, Desc: "move only, default both"},
				{Name: "pct", Type: "number", Desc: "move / range threshold in percent"},
				{Name: "field", Type: "string", Enum: fields, Desc: "metric field"}, {Name: "op", Type: "string", Enum: []string{">", ">=", "<", "<="}},
				{Name: "value", Type: "number", Desc: "metric field"}},
			Example: `{"type":"pool","scope":"pool","top":50,"metric":"move","windowMin":15,"dir":"up","pct":3}`},
		{Type: "all / any", Label: "组合", Desc: "全部成立 / 任一成立（≤ 6 个条件，2 层；pool 不能放进组合）", Checked: "as its leaves",
			Params:  []ParamInfo{{Name: "of", Type: "object", Required: true, Desc: "[spec, …]"}},
			Example: `{"type":"all","of":[{…},{…}]}`},
	}
}

// TestSpecTool is test_signal: replay a built-in spec over recent history.
func (s *Service) TestSpecTool(ctx context.Context, _ int64, a map[string]string) (any, error) {
	spec, err := ParseSpec(a["spec"])
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(a["every"]) != "" {
		spec.Every = a["every"]
	}
	hours, _ := strconv.ParseFloat(a["hours"], 64)
	cool, _ := strconv.Atoi(a["cooldownMin"])
	r, err := s.TestSpec(ctx, spec, hours, cool)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"from": fmtTime(r.From), "to": fmtTime(r.To), "minutes": r.Minutes, "fireCount": r.FireCount, "perDay": r.PerDay,
		"note": r.Note}
	if r.Members > 0 {
		out["members"] = r.Members
	}
	if r.Unknown > 0 {
		out["unknownMinutes"] = r.Unknown
	}
	if len(r.ByInst) > 0 {
		type kv struct {
			Inst string `json:"instId"`
			N    int    `json:"fires"`
		}
		var top []kv
		for k, v := range r.ByInst {
			top = append(top, kv{k, v})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].N > top[j].N || (top[i].N == top[j].N && top[i].Inst < top[j].Inst) })
		out["byInst"] = top[:min(len(top), 15)]
	}
	fires := r.Fires[max(0, len(r.Fires)-15):]
	rows := make([]map[string]any, 0, len(fires))
	for _, f := range fires {
		row := map[string]any{"at": fmtTime(f.T)}
		if f.Matched != "" {
			row["matched"] = f.Matched
		} else {
			texts := []string{}
			for _, h := range f.Hits[:min(len(f.Hits), 5)] {
				texts = append(texts, h.Text)
			}
			if len(f.Hits) > 5 {
				texts = append(texts, "…共 "+strconv.Itoa(len(f.Hits))+" 个")
			}
			row["hits"] = texts
		}
		rows = append(rows, row)
	}
	out["lastFires"] = rows
	return out, nil
}

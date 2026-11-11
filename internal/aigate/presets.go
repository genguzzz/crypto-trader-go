package aigate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Preset questions under the chat's empty state: a short label on the button and the prompt that is
// sent (empty = the label itself). {sym} is replaced by the coin in context (BTC). Two lists: Coin
// when a coin is in context, Market otherwise. Configured on the server (setting ai.presets, via the
// PUT below); without a saved list the defaults below apply.
//
//	GET    /api/v1/ai/presets   {presets, defaults, custom}
//	PUT    /api/v1/ai/presets   {coin:[{label,prompt}], market:[…]}
//	DELETE /api/v1/ai/presets   back to the defaults

type Preset struct {
	Label  string `json:"label"`
	Prompt string `json:"prompt,omitempty"`
}

type Presets struct {
	Coin   []Preset `json:"coin"`
	Market []Preset `json:"market"`
}

const presetsSetting = "ai.presets"

const (
	maxPresets      = 8
	maxPresetLabel  = 40
	maxPresetPrompt = 2000
)

var (
	presetPositions = Preset{Label: "我的仓位：止损和调整建议",
		Prompt: "先用 get_positions 看我当前所有仓位（没有仓位就直接告诉我）。逐个检查：入场以来的走势、浮盈浮亏、离强平多远、现有止盈止损是否合理；" +
			"结合最新的结构、资金费、OI、清算密集区和主动买卖，给出每个仓位的建议：继续持有、止损移到哪、减仓多少、止盈还是平仓，并写清失效条件。" +
			"需要调整的给出具体价格，问我要不要提交调整提议。"}
	presetHotList = Preset{Label: "想开仓：从热榜找能做的合约",
		Prompt: "我想开仓，帮我从热榜里找能做的合约。先看反转热榜（rev scan，4 小时内涨跌最大的币）和监控池里资金费、OI、成交异动最大的，" +
			"挑出 3–5 个最值得做的，逐个给方向（多或空）、现在做的理由、入场区间、止损、两个止盈、建议杠杆和仓位；" +
			"延续风险高（P_tail 高）、有逼空或踩踏风险、盘口太薄的直接排除并说明原因。" +
			"最后选一个最优先的，问我要不要提交交易提议；都不值得做就直说，并给出要等的条件。"}
	DefaultPresets = Presets{
		Coin: []Preset{{Label: "{sym} 资金费和 OI 最近有什么变化？"}, {Label: "{sym} 上方和下方的清算密集区在哪？"},
			{Label: "{sym} 最近有什么新闻或利空？"}, presetPositions, presetHotList},
		Market: []Preset{{Label: "今天哪些币逼空风险高、别空？"}, {Label: "大盘怎么样？BTC 和 ETH 的资金费与 OI"},
			{Label: "监控池里哪些币多头最拥挤？"}, presetPositions, presetHotList},
	}
)

// Presets returns the saved presets (or the defaults) and whether they are customised.
func (g *Gate) Presets() (Presets, bool) {
	if raw, ok := g.st.Setting(presetsSetting); ok && raw != "" {
		var p Presets
		if json.Unmarshal([]byte(raw), &p) == nil {
			return p, true
		}
	}
	return DefaultPresets, false
}

func cleanPresets(in []Preset, what string) ([]Preset, error) {
	if len(in) > maxPresets {
		return nil, fmt.Errorf("%s 最多 %d 条", what, maxPresets)
	}
	out := make([]Preset, 0, len(in))
	for i, p := range in {
		p.Label, p.Prompt = strings.TrimSpace(p.Label), strings.TrimSpace(p.Prompt)
		switch n := utf8.RuneCountInString(p.Label); {
		case n < 2:
			return nil, fmt.Errorf("%s 第 %d 条：按钮文字至少 2 个字", what, i+1)
		case n > maxPresetLabel:
			return nil, fmt.Errorf("%s 第 %d 条：按钮文字最多 %d 个字", what, i+1, maxPresetLabel)
		}
		if utf8.RuneCountInString(p.Prompt) > maxPresetPrompt {
			return nil, fmt.Errorf("%s 第 %d 条：提示词最多 %d 个字", what, i+1, maxPresetPrompt)
		}
		if p.Prompt == p.Label {
			p.Prompt = ""
		}
		out = append(out, p)
	}
	return out, nil
}

// SetPresets validates and saves the presets.
func (g *Gate) SetPresets(p Presets) (Presets, error) {
	var err error
	if p.Coin, err = cleanPresets(p.Coin, "有币种时"); err != nil {
		return p, err
	}
	if p.Market, err = cleanPresets(p.Market, "无币种时"); err != nil {
		return p, err
	}
	b, _ := json.Marshal(p)
	return p, g.st.PutSetting(presetsSetting, string(b))
}

func (g *Gate) registerPresets(mux *http.ServeMux) {
	reply := func(w http.ResponseWriter) {
		p, custom := g.Presets()
		writeJSON(w, 200, map[string]any{"presets": p, "defaults": DefaultPresets, "custom": custom})
	}
	mux.HandleFunc("GET /api/v1/ai/presets", func(w http.ResponseWriter, r *http.Request) { reply(w) })
	mux.HandleFunc("PUT /api/v1/ai/presets", func(w http.ResponseWriter, r *http.Request) {
		var in Presets
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			writeErr(w, 400, "invalid JSON")
			return
		}
		if _, err := g.SetPresets(in); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		reply(w)
	})
	mux.HandleFunc("DELETE /api/v1/ai/presets", func(w http.ResponseWriter, r *http.Request) {
		if err := g.st.PutSetting(presetsSetting, ""); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		reply(w)
	})
}

package interact

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const configKey = "interact.config"

// Config is stored as JSON in the settings table under configKey. The bot token is a secret: it is
// never returned by the API or logged (View masks it).
type Config struct {
	Telegram TelegramConfig `json:"telegram"`
	Webhook  WebhookConfig  `json:"webhook"`
	Kinds    []string       `json:"kinds"` // event kinds delivered to remote channels; empty = all
	Quiet    bool           `json:"quiet"` // deliver silently, except prompts and critical kinds
}

// TelegramConfig is a dedicated bot talking to the owner in a private chat. Messages go to every
// allowed user id (in a private chat the chat id equals the user id); everyone else is ignored.
type TelegramConfig struct {
	Enabled      bool    `json:"enabled"`
	BotToken     string  `json:"botToken"`
	AllowUserIDs []int64 `json:"allowUserIds"`
}

// polls reports whether the bot should run (also with an empty allowlist, so /start can show ids).
func (t TelegramConfig) polls() bool { return t.Enabled && t.BotToken != "" }

// delivers reports whether messages can be sent to someone.
func (t TelegramConfig) delivers() bool { return t.polls() && len(t.AllowUserIDs) > 0 }

// Kinds is the list of event kinds the UI can toggle (trade.Event.Kind values).
var Kinds = []KindInfo{
	{"signal", "信号"}, {"order", "下单"}, {"fill", "成交"}, {"cancel", "撤单"}, {"stop", "止损"},
	{"target", "止盈"}, {"time", "持仓到期"}, {"liquidation", "强平"}, {"reject", "拒单"}, {"breaker", "熔断"},
	{"instance", "策略实例"}, {"strategy", "策略消息"}, {"ai", "AI"}, {"daily", "日报"},
}

type KindInfo struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

func kindLabel(k string) string {
	for _, x := range Kinds {
		if x.Kind == k {
			return x.Label
		}
	}
	if k == "" {
		return "通知"
	}
	return k
}

// critical kinds always ring, even in quiet mode.
func critical(kind string) bool {
	return kind == "liquidation" || kind == "breaker" || kind == "stop" || kind == "prompt"
}

// wants reports whether an event kind is delivered to remote channels.
func (c Config) wants(kind string) bool {
	if len(c.Kinds) == 0 || kind == "test" {
		return true
	}
	for _, k := range c.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// ConfigView is the API form. The token is masked; a masked or empty token on PUT means
// "unchanged" (ClearToken removes it). Status and AllKinds are read-only.
type ConfigView struct {
	Telegram struct {
		Enabled      bool    `json:"enabled"`
		BotToken     string  `json:"botToken"`
		Configured   bool    `json:"configured"`
		AllowUserIDs []int64 `json:"allowUserIds"`
		ClearToken   bool    `json:"clearToken,omitempty"`
	} `json:"telegram"`
	Webhook struct {
		Enabled     bool     `json:"enabled"`
		URL         string   `json:"url"`
		Secret      string   `json:"secret"`
		Configured  bool     `json:"configured"` // a secret is stored
		ClearSecret bool     `json:"clearSecret,omitempty"`
		Kinds       []string `json:"kinds"`
		Backlog     int      `json:"backlog"`
	} `json:"webhook"`
	Kinds    []string       `json:"kinds"`
	Quiet    bool           `json:"quiet"`
	Status   TelegramStatus `json:"status"`
	AllKinds []KindInfo     `json:"allKinds"`
}

const maskDots = "••••"

// Mask keeps only the last 4 characters (none for short values).
func Mask(s string) string {
	if s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) <= 8 {
		return maskDots
	}
	r := []rune(s)
	return maskDots + string(r[len(r)-4:])
}

func (c Config) view() ConfigView {
	var v ConfigView
	v.Telegram.Enabled = c.Telegram.Enabled
	v.Telegram.BotToken = Mask(c.Telegram.BotToken)
	v.Telegram.Configured = c.Telegram.BotToken != ""
	v.Telegram.AllowUserIDs = append([]int64{}, c.Telegram.AllowUserIDs...)
	v.Webhook.Enabled, v.Webhook.URL = c.Webhook.Enabled, c.Webhook.URL
	v.Webhook.Secret, v.Webhook.Configured = Mask(c.Webhook.Secret), c.Webhook.Secret != ""
	v.Webhook.Kinds = append([]string{}, c.Webhook.Kinds...)
	v.Kinds = append([]string{}, c.Kinds...)
	v.Quiet = c.Quiet
	v.AllKinds = Kinds
	return v
}

var tokenRe = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{20,}$`)

const maxAllowed = 10

// merge applies an update and validates the result.
func (c Config) merge(in ConfigView) (Config, error) {
	out := Config{Quiet: in.Quiet}
	out.Telegram.Enabled = in.Telegram.Enabled
	out.Telegram.BotToken = c.Telegram.BotToken
	tok := strings.TrimSpace(in.Telegram.BotToken)
	switch {
	case in.Telegram.ClearToken:
		out.Telegram.BotToken = ""
	case tok != "" && tok != Mask(c.Telegram.BotToken) && !strings.HasPrefix(tok, maskDots):
		if !tokenRe.MatchString(tok) {
			return c, fmt.Errorf("Telegram Bot Token 格式不对（应形如 123456:ABC…，从 @BotFather 获取）")
		}
		out.Telegram.BotToken = tok
	}
	seen := map[int64]bool{}
	for _, id := range in.Telegram.AllowUserIDs {
		if id <= 0 {
			return c, fmt.Errorf("Telegram 用户 ID 需为正整数（私聊用户，不能是群组）")
		}
		if !seen[id] {
			seen[id] = true
			out.Telegram.AllowUserIDs = append(out.Telegram.AllowUserIDs, id)
		}
	}
	if len(out.Telegram.AllowUserIDs) > maxAllowed {
		return c, fmt.Errorf("白名单最多 %d 个用户", maxAllowed)
	}
	if out.Telegram.Enabled && out.Telegram.BotToken == "" {
		return c, fmt.Errorf("启用 Telegram 前需填写 Bot Token")
	}
	var err error
	if out.Kinds, err = validKinds(in.Kinds); err != nil {
		return c, err
	}
	wh := in.Webhook
	out.Webhook = WebhookConfig{Enabled: wh.Enabled, URL: strings.TrimSpace(wh.URL), Secret: c.Webhook.Secret}
	sec := strings.TrimSpace(wh.Secret)
	switch {
	case wh.ClearSecret:
		out.Webhook.Secret = ""
	case sec != "" && sec != Mask(c.Webhook.Secret) && !strings.HasPrefix(sec, maskDots):
		if len(sec) > 200 {
			return c, fmt.Errorf("Webhook 密钥最长 200 个字符")
		}
		out.Webhook.Secret = sec
	}
	if out.Webhook.URL != "" {
		if err := validWebhookURL(out.Webhook.URL); err != nil {
			return c, err
		}
	} else if out.Webhook.Enabled {
		return c, fmt.Errorf("启用 Webhook 前需填写地址")
	}
	if out.Webhook.Kinds, err = validKinds(wh.Kinds); err != nil {
		return c, err
	}
	return out, nil
}

func validKinds(in []string) ([]string, error) {
	ks := map[string]bool{}
	for _, k := range in {
		ok := false
		for _, x := range Kinds {
			ok = ok || x.Kind == k
		}
		if !ok {
			return nil, fmt.Errorf("未知事件类型 %q", k)
		}
		ks[k] = true
	}
	var out []string
	for k := range ks {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func parseConfig(raw string) (Config, error) {
	var c Config
	err := json.Unmarshal([]byte(raw), &c)
	return c, err
}

func sameWebhook(a, b WebhookConfig) bool {
	return a.Enabled == b.Enabled && a.URL == b.URL && a.Secret == b.Secret
}

// sameTelegram reports whether the running bot can be kept as is.
func sameTelegram(a, b TelegramConfig) bool {
	if a.Enabled != b.Enabled || a.BotToken != b.BotToken || len(a.AllowUserIDs) != len(b.AllowUserIDs) {
		return false
	}
	for i := range a.AllowUserIDs {
		if a.AllowUserIDs[i] != b.AllowUserIDs[i] {
			return false
		}
	}
	return true
}

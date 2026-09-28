package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// ChannelConfig holds delivery secrets. It is stored in the settings table and is NEVER returned
// by the API or logged as is: View() masks it.
type ChannelConfig struct {
	Telegram TelegramConfig `json:"telegram"`
	Webhook  WebhookConfig  `json:"webhook"`
}

type TelegramConfig struct {
	BotToken string `json:"botToken"`
	ChatID   string `json:"chatId"`
}

type WebhookConfig struct {
	URL    string `json:"url"`
	Secret string `json:"secret"` // optional; sent as X-CTG-Secret
}

func (t TelegramConfig) Configured() bool { return t.BotToken != "" && t.ChatID != "" }
func (w WebhookConfig) Configured() bool  { return w.URL != "" }

// ChannelView is the masked form served by the API (and accepted back on save: a masked or empty
// secret means "unchanged").
type ChannelView struct {
	Telegram struct {
		Configured bool   `json:"configured"`
		BotToken   string `json:"botToken"` // masked
		ChatID     string `json:"chatId"`
	} `json:"telegram"`
	Webhook struct {
		Configured bool   `json:"configured"`
		URL        string `json:"url"`    // masked (hook URLs often embed a token)
		Secret     string `json:"secret"` // masked
	} `json:"webhook"`
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

// MaskURL shows scheme://host and the last 4 characters of the rest.
func MaskURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return Mask(s)
	}
	rest := strings.TrimPrefix(s, u.Scheme+"://"+u.Host)
	if rest == "" || rest == "/" {
		return s
	}
	r := []rune(rest)
	if len(r) <= 4 {
		return u.Scheme + "://" + u.Host + "/" + maskDots
	}
	return u.Scheme + "://" + u.Host + "/" + maskDots + string(r[len(r)-4:])
}

func (c ChannelConfig) View() ChannelView {
	var v ChannelView
	v.Telegram.Configured = c.Telegram.Configured()
	v.Telegram.BotToken = Mask(c.Telegram.BotToken)
	v.Telegram.ChatID = c.Telegram.ChatID
	v.Webhook.Configured = c.Webhook.Configured()
	v.Webhook.URL = MaskURL(c.Webhook.URL)
	v.Webhook.Secret = Mask(c.Webhook.Secret)
	return v
}

// keep reports whether an incoming secret field means "unchanged": empty or the current masked form.
func keep(in, masked string) bool {
	in = strings.TrimSpace(in)
	return in == "" || in == masked || strings.HasPrefix(in, maskDots)
}

var (
	tokenRe  = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{20,}$`)
	chatIDRe = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z0-9_]{5,64})$`)
)

// Merge applies an update (masked/empty secrets = unchanged) and validates the result.
func (c ChannelConfig) Merge(in ChannelView) (ChannelConfig, error) {
	out := c
	if !keep(in.Telegram.BotToken, Mask(c.Telegram.BotToken)) {
		out.Telegram.BotToken = strings.TrimSpace(in.Telegram.BotToken)
	}
	out.Telegram.ChatID = strings.TrimSpace(in.Telegram.ChatID)
	if !keep(in.Webhook.URL, MaskURL(c.Webhook.URL)) {
		out.Webhook.URL = strings.TrimSpace(in.Webhook.URL)
	}
	if !keep(in.Webhook.Secret, Mask(c.Webhook.Secret)) {
		out.Webhook.Secret = strings.TrimSpace(in.Webhook.Secret)
	}
	return out, out.validate()
}

func (c ChannelConfig) validate() error {
	if t := c.Telegram.BotToken; t != "" && !tokenRe.MatchString(t) {
		return fmt.Errorf("Telegram Bot Token 格式不对（应形如 123456:ABC…，从 @BotFather 获取）")
	}
	if id := c.Telegram.ChatID; id != "" && !chatIDRe.MatchString(id) {
		return fmt.Errorf("Telegram Chat ID 需为数字（群组为负数）或 @频道名")
	}
	if c.Webhook.URL != "" {
		u, err := url.Parse(c.Webhook.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(c.Webhook.URL) > 1000 {
			return fmt.Errorf("Webhook 地址需为 http(s)://… 的完整 URL")
		}
	}
	if len(c.Webhook.Secret) > 200 {
		return fmt.Errorf("Webhook 密钥最长 200 字符")
	}
	return nil
}

// ---------- senders ----------

// Payload is the webhook JSON body.
type Payload struct {
	RuleID  int64   `json:"ruleId"`
	Rule    string  `json:"rule"`
	InstID  string  `json:"instId"`
	Type    string  `json:"type"`
	Value   float64 `json:"value"`
	Message string  `json:"message"`
	Ts      int64   `json:"ts"`
	Test    bool    `json:"test,omitempty"`
}

type sender struct {
	client       *http.Client // Telegram (fixed host)
	hook         *http.Client // webhooks: user-supplied URLs, public addresses only
	telegramBase string
	attempts     int
	timeout      time.Duration
	backoff      time.Duration
}

// retryable errors are network failures, 429 and 5xx.
type httpStatusErr struct {
	code int
	body string
}

func (e *httpStatusErr) Error() string {
	if e.body != "" {
		return fmt.Sprintf("HTTP %d：%s", e.code, e.body)
	}
	return fmt.Sprintf("HTTP %d", e.code)
}

func (s *sender) post(ctx context.Context, client *http.Client, u string, body []byte, hdr map[string]string, secrets ...string) error {
	var last error
	for i := 0; i < s.attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return scrub(ctx.Err(), secrets...)
			case <-time.After(s.backoff * time.Duration(i)):
			}
		}
		last = s.once(ctx, client, u, body, hdr)
		if last == nil {
			return nil
		}
		var he *httpStatusErr
		if errors.As(last, &he) && he.code != 429 && he.code < 500 {
			break // client error: retrying will not help
		}
	}
	return scrub(last, secrets...)
}

func (s *sender) once(ctx context.Context, client *http.Client, u string, body []byte, hdr map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return errors.New("无效地址")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "okx-helper-alerts/1")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // *url.Error embeds the full URL (Telegram token): keep only the cause
			err = ue.Err
		}
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode/100 != 2 {
		var tg struct {
			Description string `json:"description"`
		}
		msg := ""
		if json.Unmarshal(b, &tg) == nil && tg.Description != "" {
			msg = tg.Description
		}
		return &httpStatusErr{code: resp.StatusCode, body: msg}
	}
	return nil
}

// scrub removes secrets from an error text (defence in depth: never surface a token).
func scrub(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "***")
		}
	}
	return errors.New(msg)
}

// telegramMaxLen stays under Telegram's 4096-character message limit.
const telegramMaxLen = 3900

func (s *sender) telegram(ctx context.Context, c TelegramConfig, text string) error {
	if !c.Configured() {
		return errors.New("未配置")
	}
	var firstErr error
	for _, part := range splitText(text, telegramMaxLen) {
		body, _ := json.Marshal(map[string]any{"chat_id": c.ChatID, "text": part, "disable_web_page_preview": true})
		if err := s.post(ctx, s.client, s.telegramBase+"/bot"+c.BotToken+"/sendMessage", body, nil, c.BotToken); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *sender) webhook(ctx context.Context, c WebhookConfig, p Payload) error {
	if !c.Configured() {
		return errors.New("未配置")
	}
	body, _ := json.Marshal(p)
	hdr := map[string]string{}
	if c.Secret != "" {
		hdr["X-CTG-Secret"] = c.Secret
	}
	return s.post(ctx, s.hook, c.URL, body, hdr, c.URL, c.Secret)
}

// splitText cuts on line boundaries into chunks of at most n runes.
func splitText(s string, n int) []string {
	var out []string
	var cur strings.Builder
	curLen := 0
	for _, line := range strings.Split(s, "\n") {
		l := utf8.RuneCountInString(line) + 1
		if curLen > 0 && curLen+l > n {
			out = append(out, strings.TrimRight(cur.String(), "\n"))
			cur.Reset()
			curLen = 0
		}
		if l > n { // single overlong line
			r := []rune(line)
			line, l = string(r[:n-1]), n
		}
		cur.WriteString(line + "\n")
		curLen += l
	}
	if curLen > 0 {
		out = append(out, strings.TrimRight(cur.String(), "\n"))
	}
	return out
}

// PublicOnlyClient is the webhook HTTP client. Webhook URLs are user-supplied and the site has
// no login yet, so it refuses loopback, private, link-local, CGNAT, multicast and unspecified
// addresses — checked on the resolved IP at connect time (redirects included), so DNS rebinding
// can't route around it — and ignores proxy settings.
func PublicOnlyClient() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
			return fmt.Errorf("拒绝连接本机/内网地址 %s", host)
		}
		return nil
	}}
	return &http.Client{Transport: &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, MaxIdleConns: 4, IdleConnTimeout: time.Minute}}
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip))
}

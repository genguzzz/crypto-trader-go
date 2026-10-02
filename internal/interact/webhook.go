package interact

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"crypto-trader-go/internal/trade"
)

// WebhookConfig posts events as JSON to one URL. With a secret, every request carries
// X-CTG-Signature: sha256=<hex HMAC-SHA256 of the body>. Kinds narrows the delivered event kinds
// (empty = the global list).
type WebhookConfig struct {
	Enabled bool     `json:"enabled"`
	URL     string   `json:"url"`
	Secret  string   `json:"secret"`
	Kinds   []string `json:"kinds,omitempty"`
}

func (w WebhookConfig) delivers() bool { return w.Enabled && w.URL != "" }

// wants reports whether the webhook takes an event kind (given the global choice).
func (w WebhookConfig) wants(kind string, global bool) bool {
	if len(w.Kinds) == 0 || kind == "test" {
		return global
	}
	for _, k := range w.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func validWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("Webhook 地址需为 http(s)://… 形式")
	}
	return nil
}

// webhook is the Channel. Prompts are posted as events (answers come back through Telegram or the
// web page); resolutions are posted too so a receiver can close its copy.
type webhook struct {
	cfg WebhookConfig
	hc  *http.Client
}

func (w *webhook) Name() string { return "webhook" }

// WebhookPayload is the JSON body of every webhook request.
type WebhookPayload struct {
	Type    string         `json:"type"` // event | prompt | resolved
	Kind    string         `json:"kind,omitempty"`
	Title   string         `json:"title,omitempty"`
	Lines   []string       `json:"lines,omitempty"`
	Account string         `json:"account,omitempty"`
	Mode    trade.Mode     `json:"mode,omitempty"`
	Ref     string         `json:"ref,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	Prompt  string         `json:"prompt,omitempty"` // prompt id (prompt / resolved)
	Options []string       `json:"options,omitempty"`
	Choice  string         `json:"choice,omitempty"`
	By      string         `json:"by,omitempty"`
	Ts      int64          `json:"ts"`
}

func eventPayload(typ string, ev trade.Event) WebhookPayload {
	return WebhookPayload{Type: typ, Kind: ev.Kind, Title: ev.Title, Lines: ev.Lines, Account: ev.Account, Mode: ev.Mode,
		Ref: ev.Ref, Data: ev.Data, Ts: ev.Ts}
}

func (w *webhook) Notify(ctx context.Context, ev trade.Event) error {
	return w.post(ctx, eventPayload("event", ev))
}

func (w *webhook) Ask(ctx context.Context, p trade.Prompt) error {
	x := eventPayload("prompt", p.Event)
	x.Prompt = p.ID
	for _, o := range p.Options {
		x.Options = append(x.Options, o.Label)
	}
	return w.post(ctx, x)
}

func (w *webhook) Resolve(ctx context.Context, promptID, choice, by string) error {
	return w.post(ctx, WebhookPayload{Type: "resolved", Prompt: promptID, Choice: choice, By: by, Ts: time.Now().UnixMilli()})
}

func (w *webhook) Run(ctx context.Context, _ chan<- Answer) { <-ctx.Done() }

// webhookErr classifies a failed request for the sender: 4xx other than 408/429 are dropped.
type webhookErr struct {
	msg       string
	permanent bool
}

func (e webhookErr) Error() string   { return e.msg }
func (e webhookErr) Permanent() bool { return e.permanent }

func (w *webhook) post(ctx context.Context, x WebhookPayload) error {
	body, err := json.Marshal(x)
	if err != nil {
		return webhookErr{msg: err.Error(), permanent: true}
	}
	ctx, cancel := context.WithTimeout(ctx, callTO)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return webhookErr{msg: err.Error(), permanent: true}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ctg-webhook/1")
	req.Header.Set("X-CTG-Event", x.Kind)
	if w.cfg.Secret != "" {
		m := hmac.New(sha256.New, []byte(w.cfg.Secret))
		m.Write(body)
		req.Header.Set("X-CTG-Signature", "sha256="+hex.EncodeToString(m.Sum(nil)))
	}
	resp, err := w.hc.Do(req)
	if err != nil {
		return webhookErr{msg: strings.ReplaceAll(err.Error(), w.cfg.URL, "<webhook>")}
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	perm := resp.StatusCode/100 == 4 && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusRequestTimeout
	return webhookErr{msg: fmt.Sprintf("HTTP %d", resp.StatusCode), permanent: perm}
}

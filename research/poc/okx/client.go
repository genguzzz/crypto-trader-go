// Package okx is a minimal, public-only OKX v5 REST client used by research probes.
// It is intentionally not the production adapter: no auth, no private endpoints.
package okx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const BaseURL = "https://www.okx.com/api/v5"

// Limiter is a simple token bucket: `n` requests per `per` window.
type Limiter struct {
	mu     sync.Mutex
	tokens float64
	max    float64
	rate   float64 // tokens per second
	last   time.Time
}

func NewLimiter(n int, per time.Duration) *Limiter {
	return &Limiter{tokens: float64(n), max: float64(n), rate: float64(n) / per.Seconds(), last: time.Now()}
}

func (l *Limiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := time.Now()
		l.tokens = min(l.max, l.tokens+now.Sub(l.last).Seconds()*l.rate)
		l.last = now
		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		wait := time.Duration((1 - l.tokens) / l.rate * float64(time.Second))
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

type Client struct {
	HTTP *http.Client
	// Stats
	mu       sync.Mutex
	Requests int
	Errors   map[string]int
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second}, Errors: map[string]int{}}
}

type envelope struct {
	Code string          `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// Get calls path with query and decodes `data` into out. Retries 50011 (rate limit) and 5xx with backoff.
func (c *Client) Get(ctx context.Context, lim *Limiter, path string, q url.Values, out any) error {
	u := BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	backoff := 500 * time.Millisecond
	for attempt := 0; ; attempt++ {
		if lim != nil {
			if err := lim.Wait(ctx); err != nil {
				return err
			}
		}
		err, retry := c.once(ctx, u, out)
		if err == nil {
			return nil
		}
		c.mu.Lock()
		c.Errors[err.Error()]++
		c.mu.Unlock()
		if !retry || attempt >= 5 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

func (c *Client) once(ctx context.Context, u string, out any) (error, bool) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	c.mu.Lock()
	c.Requests++
	c.mu.Unlock()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("transport"), true
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 429 {
		return fmt.Errorf("http429"), true
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("http%d", resp.StatusCode), true
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decode http%d", resp.StatusCode), false
	}
	if env.Code != "0" {
		return fmt.Errorf("okx%s", env.Code), env.Code == "50011" || env.Code == "50013"
	}
	return json.Unmarshal(env.Data, out), false
}

// ---- typed helpers ----

type Instrument struct {
	InstID       string `json:"instId"`
	InstType     string `json:"instType"`
	State        string `json:"state"`
	SettleCcy    string `json:"settleCcy"`
	CtVal        string `json:"ctVal"`
	CtValCcy     string `json:"ctValCcy"`
	CtType       string `json:"ctType"`
	RuleType     string `json:"ruleType"`
	InstCategory string `json:"instCategory"`
	ListTime     string `json:"listTime"`
	BaseCcy      string `json:"baseCcy"`
	QuoteCcy     string `json:"quoteCcy"`
	Uly          string `json:"uly"`
}

type Ticker struct {
	InstID   string `json:"instId"`
	Last     string `json:"last"`
	BidPx    string `json:"bidPx"`
	AskPx    string `json:"askPx"`
	VolCcy24 string `json:"volCcy24h"`
	Vol24    string `json:"vol24h"`
	Ts       string `json:"ts"`
}

// Candle is [ts, o, h, l, c, vol, volCcy, volCcyQuote, confirm].
type Candle struct {
	Ts                       int64
	O, H, L, C               float64
	Vol, VolCcy, VolCcyQuote float64
	Confirmed                bool
}

func ParseCandles(raw [][]string) []Candle {
	out := make([]Candle, 0, len(raw))
	for _, r := range raw {
		if len(r) < 9 {
			continue
		}
		f := func(i int) float64 { v, _ := strconv.ParseFloat(r[i], 64); return v }
		ts, _ := strconv.ParseInt(r[0], 10, 64)
		out = append(out, Candle{Ts: ts, O: f(1), H: f(2), L: f(3), C: f(4), Vol: f(5), VolCcy: f(6), VolCcyQuote: f(7), Confirmed: r[8] == "1"})
	}
	return out
}

func F(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }

// Eligible applies the default universe filter. includeCategories nil = all.
func Eligible(in []Instrument, includeCategories map[string]bool) []Instrument {
	var out []Instrument
	for _, i := range in {
		if i.State != "live" || i.SettleCcy != "USDT" || i.CtType != "linear" || i.RuleType == "pre_market" {
			continue
		}
		if includeCategories != nil && !includeCategories[i.InstCategory] {
			continue
		}
		out = append(out, i)
	}
	return out
}

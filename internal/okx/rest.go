// Package okx is the narrow OKX v5 public adapter (REST + WebSocket). No private endpoints,
// no credentials, no order placement. Business code depends on the types here, never on raw JSON.
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
	"sync/atomic"
	"time"
)

const DefaultBaseURL = "https://www.okx.com/api/v5"

// Limiter is a token bucket: n requests per window.
type Limiter struct {
	mu     sync.Mutex
	tokens float64
	max    float64
	rate   float64
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

// REST is a rate-limited public REST client. Budgets are ~50–75% of OKX's documented IP limits.
type REST struct {
	BaseURL string
	HTTP    *http.Client

	candles  *Limiter // /market/candles 40/2s
	history  *Limiter // /market/history-candles 20/2s
	books    *Limiter // /market/books 40/2s
	booksAll *Limiter // /market/books-full 10/2s
	rubik    *Limiter // /rubik/* 5/2s
	fundHist *Limiter // /public/funding-rate-history 10/2s
	trades   *Limiter // /market/history-trades 20/2s
	misc     *Limiter // instruments/tickers/oi/funding 20/2s

	Requests atomic.Int64
	Errors   atomic.Int64
	RateHits atomic.Int64
}

func NewREST() *REST {
	return &REST{
		BaseURL:  DefaultBaseURL,
		HTTP:     &http.Client{Timeout: 15 * time.Second, Transport: restTransport()},
		candles:  NewLimiter(30, 2*time.Second),
		history:  NewLimiter(14, 2*time.Second),
		books:    NewLimiter(20, 2*time.Second),
		booksAll: NewLimiter(6, 2*time.Second),
		rubik:    NewLimiter(4, 2*time.Second),
		fundHist: NewLimiter(8, 2*time.Second),
		trades:   NewLimiter(12, 2*time.Second),
		misc:     NewLimiter(10, 2*time.Second),
	}
}

func restTransport() http.RoundTripper {
	if d := currentDial(); d != nil {
		return &http.Transport{DialContext: d, ForceAttemptHTTP2: true, MaxIdleConnsPerHost: 16,
			IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second}
	}
	return http.DefaultTransport
}

// HTTPClient returns a client using the configured network path (proxy/DoH), for auxiliary fetches.
func HTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, Transport: restTransport()}
}

type APIError struct{ Code, Msg string }

func (e *APIError) Error() string { return "okx " + e.Code + ": " + e.Msg }

func (c *REST) get(ctx context.Context, lim *Limiter, path string, q url.Values, out any) error {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	backoff := 500 * time.Millisecond
	for attempt := 0; ; attempt++ {
		if err := lim.Wait(ctx); err != nil {
			return err
		}
		retry, err := c.once(ctx, u, out)
		if err == nil {
			return nil
		}
		c.Errors.Add(1)
		if !retry || attempt >= 4 {
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

func (c *REST) once(ctx context.Context, u string, out any) (retry bool, err error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	c.Requests.Add(1)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 429 {
		c.RateHits.Add(1)
		return true, fmt.Errorf("http 429")
	}
	if resp.StatusCode >= 500 {
		return true, fmt.Errorf("http %d", resp.StatusCode)
	}
	var env struct {
		Code string          `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return false, fmt.Errorf("decode (http %d): %w", resp.StatusCode, err)
	}
	if env.Code != "0" {
		if env.Code == "50011" {
			c.RateHits.Add(1)
		}
		return env.Code == "50011" || env.Code == "50013", &APIError{env.Code, env.Msg}
	}
	return false, json.Unmarshal(env.Data, out)
}

// ---- types ----

type Instrument struct {
	InstID    string
	InstType  string
	State     string
	SettleCcy string
	CtType    string
	CtVal     float64
	TickSz    float64
	RuleType  string
	Category  string // instCategory: 1 crypto, 3 stock/ETF, 4 commodity
	ListTime  int64
	BaseCcy   string
	QuoteCcy  string
	Uly       string
}

type Ticker struct {
	InstID            string
	Last, Bid, Ask    float64
	VolCcy24h, Vol24h float64
	Open24h           float64
	Ts                int64
}

type Candle struct {
	Ts                       int64 // bar open, ms
	O, H, L, C               float64
	Vol, VolCcy, VolCcyQuote float64
	Confirmed                bool
}

type OpenInterest struct {
	InstID           string
	Oi, OiCcy, OiUsd float64
	Ts               int64
}

type Funding struct {
	InstID          string
	Rate            float64 // current period rate
	FundingTime     int64
	NextFundingTime int64
	Ts              int64
}

func f(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
func i64(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }

// ParseCandles parses OKX candle arrays [ts,o,h,l,c,vol,volCcy,volCcyQuote,confirm].
func ParseCandles(raw [][]string) []Candle {
	out := make([]Candle, 0, len(raw))
	for _, r := range raw {
		if len(r) < 9 {
			continue
		}
		out = append(out, Candle{Ts: i64(r[0]), O: f(r[1]), H: f(r[2]), L: f(r[3]), C: f(r[4]),
			Vol: f(r[5]), VolCcy: f(r[6]), VolCcyQuote: f(r[7]), Confirmed: r[8] == "1"})
	}
	return out
}

// ---- endpoints ----

func (c *REST) Instruments(ctx context.Context, instType string) ([]Instrument, error) {
	var raw []map[string]any
	if err := c.get(ctx, c.misc, "/public/instruments", url.Values{"instType": {instType}}, &raw); err != nil {
		return nil, err
	}
	s := func(m map[string]any, k string) string { v, _ := m[k].(string); return v }
	out := make([]Instrument, 0, len(raw))
	for _, m := range raw {
		out = append(out, Instrument{InstID: s(m, "instId"), InstType: s(m, "instType"), State: s(m, "state"),
			SettleCcy: s(m, "settleCcy"), CtType: s(m, "ctType"), CtVal: f(s(m, "ctVal")), TickSz: f(s(m, "tickSz")), RuleType: s(m, "ruleType"),
			Category: s(m, "instCategory"), ListTime: i64(s(m, "listTime")), BaseCcy: s(m, "baseCcy"),
			QuoteCcy: s(m, "quoteCcy"), Uly: s(m, "uly")})
	}
	return out, nil
}

func (c *REST) Tickers(ctx context.Context, instType string) ([]Ticker, error) {
	var raw []map[string]string
	if err := c.get(ctx, c.misc, "/market/tickers", url.Values{"instType": {instType}}, &raw); err != nil {
		return nil, err
	}
	out := make([]Ticker, 0, len(raw))
	for _, m := range raw {
		out = append(out, Ticker{InstID: m["instId"], Last: f(m["last"]), Bid: f(m["bidPx"]), Ask: f(m["askPx"]),
			VolCcy24h: f(m["volCcy24h"]), Vol24h: f(m["vol24h"]), Open24h: f(m["open24h"]), Ts: i64(m["ts"])})
	}
	return out, nil
}

// Candles returns bars older than `after` (ms, exclusive; 0 = latest), newest first.
func (c *REST) Candles(ctx context.Context, instID, bar string, after int64, limit int) ([]Candle, error) {
	q := url.Values{"instId": {instID}, "bar": {bar}, "limit": {strconv.Itoa(limit)}}
	if after > 0 {
		q.Set("after", strconv.FormatInt(after, 10))
	}
	var raw [][]string
	if err := c.get(ctx, c.candles, "/market/candles", q, &raw); err != nil {
		return nil, err
	}
	return ParseCandles(raw), nil
}

// HistoryCandles pages older bars (/market/history-candles, up to 300 per page, years of depth).
func (c *REST) HistoryCandles(ctx context.Context, instID, bar string, after int64, limit int) ([]Candle, error) {
	q := url.Values{"instId": {instID}, "bar": {bar}, "limit": {strconv.Itoa(limit)}}
	if after > 0 {
		q.Set("after", strconv.FormatInt(after, 10))
	}
	var raw [][]string
	if err := c.get(ctx, c.history, "/market/history-candles", q, &raw); err != nil {
		return nil, err
	}
	return ParseCandles(raw), nil
}

// BookLevel is one price level: price and size in contracts.
type BookLevel struct{ Px, Sz float64 }

type Book struct {
	Asks, Bids []BookLevel // asks ascending, bids descending (as OKX returns them)
	Ts         int64
}

// Books returns the order book snapshot: depth ≤ 400 per side from /market/books, larger depths
// (≤ 5000) from /market/books-full (OKX's deepest public book; lower rate limit).
func (c *REST) Books(ctx context.Context, instID string, depth int) (Book, error) {
	var raw []struct {
		Asks [][]string `json:"asks"`
		Bids [][]string `json:"bids"`
		Ts   string     `json:"ts"`
	}
	path, lim := "/market/books", c.books
	if depth > 400 {
		path, lim = "/market/books-full", c.booksAll
	}
	if err := c.get(ctx, lim, path, url.Values{"instId": {instID}, "sz": {strconv.Itoa(depth)}}, &raw); err != nil {
		return Book{}, err
	}
	if len(raw) == 0 {
		return Book{}, fmt.Errorf("empty book")
	}
	conv := func(xs [][]string) []BookLevel {
		out := make([]BookLevel, 0, len(xs))
		for _, x := range xs {
			if len(x) >= 2 {
				out = append(out, BookLevel{f(x[0]), f(x[1])})
			}
		}
		return out
	}
	return Book{Asks: conv(raw[0].Asks), Bids: conv(raw[0].Bids), Ts: i64(raw[0].Ts)}, nil
}

func (c *REST) OpenInterestAll(ctx context.Context) ([]OpenInterest, error) {
	var raw []map[string]string
	if err := c.get(ctx, c.misc, "/public/open-interest", url.Values{"instType": {"SWAP"}}, &raw); err != nil {
		return nil, err
	}
	out := make([]OpenInterest, 0, len(raw))
	for _, m := range raw {
		out = append(out, OpenInterest{InstID: m["instId"], Oi: f(m["oi"]), OiCcy: f(m["oiCcy"]), OiUsd: f(m["oiUsd"]), Ts: i64(m["ts"])})
	}
	return out, nil
}

func (c *REST) FundingAll(ctx context.Context) ([]Funding, error) {
	var raw []map[string]string
	if err := c.get(ctx, c.misc, "/public/funding-rate", url.Values{"instId": {"ANY"}}, &raw); err != nil {
		return nil, err
	}
	out := make([]Funding, 0, len(raw))
	for _, m := range raw {
		out = append(out, ParseFunding(m))
	}
	return out, nil
}

func ParseFunding(m map[string]string) Funding {
	return Funding{InstID: m["instId"], Rate: f(m["fundingRate"]), FundingTime: i64(m["fundingTime"]),
		NextFundingTime: i64(m["nextFundingTime"]), Ts: i64(m["ts"])}
}

// OIHistory returns coin-denominated OI snapshots [ts, oiCcy] (newest first) from rubik stats.
// period: 5m (≈5 days depth) or 1H (≈60 days). end: ms exclusive, 0 = latest.
func (c *REST) OIHistory(ctx context.Context, instID, period string, end int64, limit int) ([]OpenInterest, error) {
	q := url.Values{"instId": {instID}, "period": {period}, "limit": {strconv.Itoa(limit)}}
	if end > 0 {
		q.Set("end", strconv.FormatInt(end, 10))
	}
	var raw [][]string
	if err := c.get(ctx, c.rubik, "/rubik/stat/contracts/open-interest-history", q, &raw); err != nil {
		return nil, err
	}
	out := make([]OpenInterest, 0, len(raw))
	for _, r := range raw {
		if len(r) >= 4 {
			out = append(out, OpenInterest{InstID: instID, Ts: i64(r[0]), Oi: f(r[1]), OiCcy: f(r[2]), OiUsd: f(r[3])})
		}
	}
	return out, nil
}

// FundingHistory returns settled funding (newest first) older than `after` (0 = latest).
func (c *REST) FundingHistory(ctx context.Context, instID string, after int64, limit int) ([]Funding, error) {
	q := url.Values{"instId": {instID}, "limit": {strconv.Itoa(limit)}}
	if after > 0 {
		q.Set("after", strconv.FormatInt(after, 10))
	}
	var raw []map[string]string
	if err := c.get(ctx, c.fundHist, "/public/funding-rate-history", q, &raw); err != nil {
		return nil, err
	}
	out := make([]Funding, 0, len(raw))
	for _, m := range raw {
		rate := m["realizedRate"]
		if rate == "" {
			rate = m["fundingRate"]
		}
		out = append(out, Funding{InstID: m["instId"], Rate: f(rate), FundingTime: i64(m["fundingTime"])})
	}
	return out, nil
}

// TakerVolume is one rubik period of taker buy/sell volume (USD notional).
type TakerVolume struct {
	Ts        int64 // period start, ms
	Buy, Sell float64
}

// TakerVolumeHistory returns taker buy/sell volume for a perpetual (newest first), periods older than
// `end` (ms, 0 = latest). OKX keeps 5m for a few days and 1H for weeks; missing history is zeros.
func (c *REST) TakerVolumeHistory(ctx context.Context, instID, period string, end int64, limit int) ([]TakerVolume, error) {
	q := url.Values{"instId": {instID}, "period": {period}, "unit": {"2"}, "limit": {strconv.Itoa(limit)}}
	if end > 0 {
		q.Set("end", strconv.FormatInt(end, 10))
	}
	var raw [][]string
	if err := c.get(ctx, c.rubik, "/rubik/stat/taker-volume-contract", q, &raw); err != nil {
		return nil, err
	}
	out := make([]TakerVolume, 0, len(raw))
	for _, r := range raw {
		if len(r) >= 3 { // [ts, sellVol, buyVol]
			out = append(out, TakerVolume{Ts: i64(r[0]), Sell: f(r[1]), Buy: f(r[2])})
		}
	}
	return out, nil
}

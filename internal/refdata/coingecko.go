// Package refdata holds slow-changing coin reference data from outside OKX: market cap, FDV and
// supply (circulating / total / max) from the CoinGecko public API (no key). It is display-only —
// never an input to the pool, fact panel or scores.
//
// OKX's documented public API has no market cap or supply (its website renders them from
// undocumented internal endpoints), so CoinGecko stays the source. Coverage: every OKX base
// currency is queried by symbol (`symbols=` + `include_tokens=top` returns the largest coin for a
// shared ticker), not just the top of the market-cap list; the result is persisted so a restart
// serves the last snapshot at once.
package refdata

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	baseURL = "https://api.coingecko.com/api/v3/coins/markets"
	batch   = 50            // symbols per request
	every   = 2 * time.Hour // CoinGecko updates these every few minutes; supply changes slowly
)

// Coin is one CoinGecko market entry (USD). Zero values mean CoinGecko has no figure.
type Coin struct {
	ID          string  `json:"id"`
	Symbol      string  `json:"symbol"`
	Name        string  `json:"name"`
	Rank        int     `json:"rank"`
	MarketCap   float64 `json:"marketCap"`
	FDV         float64 `json:"fdv"`
	Circulating float64 `json:"circulating"`
	Total       float64 `json:"total"`
	Max         float64 `json:"max"`
	UpdatedAt   string  `json:"updatedAt"`
	FetchedAt   int64   `json:"fetchedAt"`
}

type Cache struct {
	HTTP    *http.Client
	URL     string        // markets endpoint (tests)
	Pause   time.Duration // between requests (free tier ~30 calls/min)
	Backoff time.Duration // first retry delay after 429 / errors (doubles)
	// Symbols lists the base currencies to cover (OKX crypto perpetuals); required.
	Symbols func() []string
	// Load / Save persist the last snapshot (JSON); optional.
	Load func() (string, bool)
	Save func(string) error

	mu sync.RWMutex
	by map[string]Coin // upper-case symbol → coin
}

func New(client *http.Client) *Cache {
	return &Cache{HTTP: client, URL: baseURL, Pause: 4 * time.Second, Backoff: 20 * time.Second, by: map[string]Coin{}}
}

// Get returns the coin for an OKX base currency (e.g. "BTC").
func (c *Cache) Get(symbol string) (Coin, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	x, ok := c.by[strings.ToUpper(symbol)]
	return x, ok
}

func (c *Cache) Run(ctx context.Context) {
	if c.Load != nil {
		if s, ok := c.Load(); ok {
			var by map[string]Coin
			if json.Unmarshal([]byte(s), &by) == nil {
				c.mu.Lock()
				c.by = by
				c.mu.Unlock()
				log.Printf("coingecko: %d symbols from last snapshot", len(by))
			}
		}
	}
	var retry []string // symbols of rate-limited batches: retried soon, alone
	for {
		wait := every
		if len(retry) > 0 {
			retry, _ = c.refresh(ctx, retry)
		} else if syms := c.Symbols(); len(syms) > 0 { // empty until the instrument list is loaded
			var err error
			if retry, err = c.refresh(ctx, syms); err != nil {
				log.Printf("coingecko: %v", err)
			}
		} else {
			wait = 30 * time.Second
		}
		if len(retry) > 0 {
			wait = 3 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// refresh queries the symbols in batches and returns those of failed batches. A failed batch
// keeps its previous entries, so a rate-limited run never blanks figures already known.
func (c *Cache) refresh(ctx context.Context, syms []string) (failedSyms []string, err error) {
	uniq := map[string]bool{}
	for _, s := range syms {
		uniq[strings.ToLower(s)] = true
	}
	list := make([]string, 0, len(uniq))
	for s := range uniq {
		list = append(list, s)
	}
	sort.Strings(list)

	c.mu.RLock()
	by := make(map[string]Coin, len(c.by))
	for k, v := range c.by {
		by[k] = v
	}
	c.mu.RUnlock()
	now := time.Now().UnixMilli()
	got, failed := 0, 0
	for i := 0; i < len(list); i += batch {
		part := list[i:min(i+batch, len(list))]
		var raw []struct {
			ID          string   `json:"id"`
			Symbol      string   `json:"symbol"`
			Name        string   `json:"name"`
			Rank        int      `json:"market_cap_rank"`
			MarketCap   float64  `json:"market_cap"`
			FDV         float64  `json:"fully_diluted_valuation"`
			Circulating float64  `json:"circulating_supply"`
			Total       *float64 `json:"total_supply"`
			Max         *float64 `json:"max_supply"`
			Updated     string   `json:"last_updated"`
		}
		q := url.Values{"vs_currency": {"usd"}, "symbols": {strings.Join(part, ",")}, "include_tokens": {"top"}, "per_page": {"250"}}
		if err := c.get(ctx, c.URL+"?"+q.Encode(), &raw); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			failed++
			failedSyms = append(failedSyms, part...)
			log.Printf("coingecko batch %d/%d: %v (keeping previous entries)", i/batch+1, (len(list)+batch-1)/batch, err)
			continue
		}
		for _, r := range raw {
			x := Coin{ID: r.ID, Symbol: strings.ToUpper(r.Symbol), Name: r.Name, Rank: r.Rank, MarketCap: r.MarketCap, FDV: r.FDV,
				Circulating: r.Circulating, UpdatedAt: r.Updated, FetchedAt: now}
			if r.Total != nil {
				x.Total = *r.Total
			}
			if r.Max != nil {
				x.Max = *r.Max
			}
			if old, ok := by[x.Symbol]; ok && old.FetchedAt == now && old.MarketCap >= x.MarketCap {
				continue // include_tokens=top already picks one; keep the larger if two slip through
			}
			by[x.Symbol] = x
			got++
		}
		select { // free tier: ~30 calls/min; stay far below
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.Pause):
		}
	}
	if got == 0 && failed > 0 {
		return failedSyms, fmt.Errorf("all %d batches failed", failed)
	}
	c.mu.Lock()
	c.by = by
	c.mu.Unlock()
	if c.Save != nil {
		if b, err := json.Marshal(by); err == nil {
			if err := c.Save(string(b)); err != nil {
				log.Printf("coingecko: save snapshot: %v", err)
			}
		}
	}
	log.Printf("coingecko: %d/%d symbols (%d batches failed)", got, len(list), failed)
	return failedSyms, nil
}

func (c *Cache) get(ctx context.Context, u string, out any) error {
	var last error
	for try := 0; try < 3; try++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		resp, err := c.HTTP.Do(req)
		if err == nil {
			if resp.StatusCode == 200 {
				err = json.NewDecoder(resp.Body).Decode(out)
				resp.Body.Close()
				if err == nil {
					return nil
				}
			} else {
				resp.Body.Close()
				err = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		}
		last = err
		select { // 429 / transient: back off, then twice as long
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.Backoff * time.Duration(1<<try)):
		}
	}
	return last
}

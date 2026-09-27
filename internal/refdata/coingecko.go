// Package refdata holds slow-changing coin reference data from outside OKX: market cap, FDV and
// supply (circulating / total / max) from the CoinGecko public API (no key). It is display-only —
// never an input to the pool, fact panel or scores.
package refdata

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	baseURL = "https://api.coingecko.com/api/v3/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=250&page=%d"
	pages   = 6             // top 1500 by market cap
	every   = 2 * time.Hour // CoinGecko updates these every few minutes; supply changes slowly
)

// Coin is one CoinGecko market entry (USD).
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
	HTTP  *http.Client
	URL   string        // page URL format (tests)
	Pause time.Duration // between pages (free tier ~30 calls/min)

	mu  sync.RWMutex
	by  map[string]Coin // upper-case symbol → largest market cap coin with that symbol
	at  time.Time
	err string
}

func New(client *http.Client) *Cache {
	return &Cache{HTTP: client, URL: baseURL, Pause: 4 * time.Second, by: map[string]Coin{}}
}

// Get returns the coin for an OKX base currency (e.g. "BTC").
func (c *Cache) Get(symbol string) (Coin, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	x, ok := c.by[strings.ToUpper(symbol)]
	return x, ok
}

func (c *Cache) Run(ctx context.Context) {
	for {
		if err := c.refresh(ctx); err != nil {
			log.Printf("coingecko: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (c *Cache) refresh(ctx context.Context) error {
	by := map[string]Coin{}
	now := time.Now().UnixMilli()
	for p := 1; p <= pages; p++ {
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
		if err := c.page(ctx, p, &raw); err != nil {
			if len(by) == 0 {
				return err
			}
			log.Printf("coingecko page %d: %v (keeping %d coins)", p, err, len(by))
			break
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
			if old, ok := by[x.Symbol]; !ok || x.MarketCap > old.MarketCap {
				by[x.Symbol] = x
			}
		}
		if len(raw) == 0 {
			break
		}
		select { // free tier: ~30 calls/min; stay far below
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.Pause):
		}
	}
	c.mu.Lock()
	c.by, c.at = by, time.Now()
	c.mu.Unlock()
	log.Printf("coingecko: %d symbols", len(by))
	return nil
}

func (c *Cache) page(ctx context.Context, p int, out any) error {
	var last error
	for try := 0; try < 4; try++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf(c.URL, p), nil)
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
		select { // 429 / transient: back off 20s, 40s, 60s
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(20*(try+1)) * time.Second):
		}
	}
	return last
}

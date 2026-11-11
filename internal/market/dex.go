package market

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"crypto-trader-go/internal/okx"
)

// ---------- on-chain DEX activity (GeckoTerminal public API: free, no key, ~30 calls/min) ----------
//
// The coin's largest DEX pools (matched by symbol and checked against the OKX price so look-alike
// tokens are dropped), their 24h buy/sell counts and volume, and recent swaps above a size from
// the pools' trade feeds (GeckoTerminal keeps the last 300 per pool, 24h max). Wallet = the
// transaction sender, so a router or aggregator can stand for many users.

const geckoBase = "https://api.geckoterminal.com/api/v2"

var gecko = struct {
	sync.Mutex
	lim   *okx.Limiter
	cache map[string]geckoEntry
}{lim: okx.NewLimiter(5, 12*time.Second), cache: map[string]geckoEntry{}}

type geckoEntry struct {
	body []byte
	at   time.Time
}

func geckoGet(ctx context.Context, path string, q url.Values, out any) error {
	u := geckoBase + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	gecko.Lock()
	if c, ok := gecko.cache[u]; ok && time.Since(c.at) < time.Minute {
		gecko.Unlock()
		return json.Unmarshal(c.body, out)
	}
	for k, c := range gecko.cache {
		if time.Since(c.at) > 10*time.Minute {
			delete(gecko.cache, k)
		}
	}
	gecko.Unlock()
	if err := gecko.lim.Wait(ctx); err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := okx.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("geckoterminal %s: http %d", path, resp.StatusCode)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return err
	}
	gecko.Lock()
	gecko.cache[u] = geckoEntry{raw, time.Now()}
	gecko.Unlock()
	return json.Unmarshal(raw, out)
}

type DexPool struct {
	Network     string  `json:"network"`
	Dex         string  `json:"dex"`
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	LiquidityUs float64 `json:"liquidityUsd"`
	PriceUsd    float64 `json:"priceUsd"`
	Vol24h      float64 `json:"vol24hUsd"`
	Chg24hPct   float64 `json:"chg24hPct"`
	Buys24h     int     `json:"buys24h"`
	Sells24h    int     `json:"sells24h"`
	Buyers24h   int     `json:"buyers24h"`
	Sellers24h  int     `json:"sellers24h"`
	Buys1h      int     `json:"buys1h"`
	Sells1h     int     `json:"sells1h"`
}

type DexTrade struct {
	T       int64   `json:"t"`
	Pool    string  `json:"pool"`
	Side    string  `json:"side"`
	Usd     float64 `json:"usd"`
	Px      float64 `json:"px"`
	Wallet  string  `json:"wallet"`
	Tx      string  `json:"tx"`
	Network string  `json:"network"`
}

type DexWallet struct {
	Wallet string  `json:"wallet"`
	BuyUsd float64 `json:"buyUsd"`
	SelUsd float64 `json:"sellUsd"`
	Net    float64 `json:"netUsd"`
	N      int     `json:"n"`
}

type DexReport struct {
	Coin       string      `json:"coin"`
	OkxPrice   float64     `json:"okxPrice"`
	Pools      []DexPool   `json:"pools"`
	MinUsd     float64     `json:"minUsd"`
	BigBuyUsd  float64     `json:"bigBuyUsd"`
	BigSellUsd float64     `json:"bigSellUsd"`
	BigBuyN    int         `json:"bigBuyN"`
	BigSellN   int         `json:"bigSellN"`
	Wallets    []DexWallet `json:"topWallets,omitempty"` // by |net|, among the big swaps
	Trades     []DexTrade  `json:"trades,omitempty"`
	Skipped    []string    `json:"skipped,omitempty"` // pools dropped (price far from OKX: another token)
	Note       string      `json:"note"`
}

func pf(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }

// DexActivity looks up the coin's DEX pools and their recent large swaps.
func (e *Engine) DexActivity(ctx context.Context, coin string, minUsd float64, pools, top int) (*DexReport, error) {
	coin = strings.ToUpper(strings.TrimSuffix(strings.TrimSuffix(strings.ToUpper(coin), "-SWAP"), "-USDT"))
	r := &DexReport{Coin: coin, MinUsd: minUsd,
		Note: "GeckoTerminal 公共数据：每个池子只保留最近 300 笔、最多 24h；wallet 是交易发起地址（可能是路由/聚合器）。CEX 上市币的链上成交通常远小于交易所。"}
	if sw, sp, err := e.ResolvePair(coin); err == nil {
		if sp != "" {
			r.OkxPrice = e.lastPrice(sp)
		}
		if r.OkxPrice == 0 && sw != "" {
			r.OkxPrice = e.lastPrice(sw)
		}
	}
	var res struct {
		Data []struct {
			ID         string `json:"id"` // "<network>_<address>"
			Attributes struct {
				Name              string                    `json:"name"`
				Address           string                    `json:"address"`
				BaseTokenPriceUsd string                    `json:"base_token_price_usd"`
				ReserveInUsd      string                    `json:"reserve_in_usd"`
				VolumeUsd         map[string]string         `json:"volume_usd"`
				PriceChange       map[string]string         `json:"price_change_percentage"`
				Transactions      map[string]map[string]any `json:"transactions"`
			}
			Relationships struct {
				Network struct{ Data struct{ ID string } } `json:"network"`
				Dex     struct{ Data struct{ ID string } } `json:"dex"`
			}
		}
	}
	if err := geckoGet(ctx, "/search/pools", url.Values{"query": {coin}}, &res); err != nil {
		return nil, err
	}
	for _, d := range res.Data {
		at := d.Attributes
		base := strings.TrimSpace(strings.SplitN(at.Name, "/", 2)[0])
		if !strings.EqualFold(base, coin) {
			continue
		}
		netw := d.Relationships.Network.Data.ID
		if i := strings.Index(d.ID, "_"); netw == "" && i > 0 {
			netw = d.ID[:i]
		}
		p := DexPool{Network: netw, Dex: d.Relationships.Dex.Data.ID, Name: at.Name, Address: at.Address,
			LiquidityUs: math.Round(pf(at.ReserveInUsd)), PriceUsd: pf(at.BaseTokenPriceUsd), Vol24h: math.Round(pf(at.VolumeUsd["h24"])),
			Chg24hPct: pf(at.PriceChange["h24"])}
		num := func(k, f string) int {
			if v, ok := at.Transactions[k][f].(float64); ok {
				return int(v)
			}
			return 0
		}
		p.Buys24h, p.Sells24h, p.Buyers24h, p.Sellers24h = num("h24", "buys"), num("h24", "sells"), num("h24", "buyers"), num("h24", "sellers")
		p.Buys1h, p.Sells1h = num("h1", "buys"), num("h1", "sells")
		if r.OkxPrice > 0 && (p.PriceUsd <= 0 || math.Abs(p.PriceUsd/r.OkxPrice-1) > 0.1) {
			r.Skipped = append(r.Skipped, fmt.Sprintf("%s %s %s (price %.6g)", p.Network, p.Dex, p.Name, p.PriceUsd))
			continue
		}
		if p.LiquidityUs < 10_000 {
			continue
		}
		r.Pools = append(r.Pools, p)
	}
	sort.Slice(r.Pools, func(i, j int) bool { return r.Pools[i].LiquidityUs > r.Pools[j].LiquidityUs })
	r.Pools = r.Pools[:min(len(r.Pools), pools)]
	wallets := map[string]*DexWallet{}
	for _, p := range r.Pools {
		var tr struct {
			Data []struct {
				Attributes struct {
					BlockTimestamp string `json:"block_timestamp"`
					Kind           string `json:"kind"`
					VolumeInUsd    string `json:"volume_in_usd"`
					PriceToInUsd   string `json:"price_to_in_usd"`
					PriceFromInUsd string `json:"price_from_in_usd"`
					TxFromAddress  string `json:"tx_from_address"`
					TxHash         string `json:"tx_hash"`
				}
			}
		}
		q := url.Values{"trade_volume_in_usd_greater_than": {strconv.FormatFloat(minUsd, 'f', 0, 64)}}
		if err := geckoGet(ctx, "/networks/"+p.Network+"/pools/"+p.Address+"/trades", q, &tr); err != nil {
			r.Skipped = append(r.Skipped, p.Name+" trades: "+err.Error())
			continue
		}
		for _, d := range tr.Data {
			at := d.Attributes
			ts, _ := time.Parse(time.RFC3339, at.BlockTimestamp)
			t := DexTrade{T: ts.UnixMilli(), Pool: p.Name, Side: at.Kind, Usd: math.Round(pf(at.VolumeInUsd)), Wallet: at.TxFromAddress, Tx: at.TxHash, Network: p.Network}
			if t.Side == "buy" {
				t.Px = pf(at.PriceToInUsd)
				r.BigBuyUsd += t.Usd
				r.BigBuyN++
			} else {
				t.Px = pf(at.PriceFromInUsd)
				r.BigSellUsd += t.Usd
				r.BigSellN++
			}
			r.Trades = append(r.Trades, t)
			w := wallets[t.Wallet]
			if w == nil {
				w = &DexWallet{Wallet: t.Wallet}
				wallets[t.Wallet] = w
			}
			w.N++
			if t.Side == "buy" {
				w.BuyUsd += t.Usd
			} else {
				w.SelUsd += t.Usd
			}
		}
	}
	for _, w := range wallets {
		w.Net = w.BuyUsd - w.SelUsd
		r.Wallets = append(r.Wallets, *w)
	}
	sort.Slice(r.Wallets, func(i, j int) bool { return math.Abs(r.Wallets[i].Net) > math.Abs(r.Wallets[j].Net) })
	r.Wallets = r.Wallets[:min(len(r.Wallets), 8)]
	sort.Slice(r.Trades, func(i, j int) bool { return r.Trades[i].Usd > r.Trades[j].Usd })
	r.Trades = r.Trades[:min(len(r.Trades), top)]
	return r, nil
}

// lastPrice: latest swap or spot price known to the engine (0 = unknown).
func (e *Engine) lastPrice(id string) float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if in := e.insts[id]; in != nil && in.last > 0 {
		return in.last
	}
	if t, ok := e.tickers[id]; ok {
		return t.Last
	}
	if t, ok := e.spotTickers[id]; ok {
		return t.Last
	}
	return 0
}

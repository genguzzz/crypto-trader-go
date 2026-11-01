package okxacct

// Positions are read on demand (REST, ~3 s cache, one request in flight) rather than from the private
// WebSocket: the orders-algo channel pushes changes only, never a snapshot of the pending TP/SL, so a
// socket would still need these REST calls on every reconnect, and polling stops costing anything
// when nobody is looking at a chart or asking the agent.

import (
	"context"
	"errors"
	"log"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Trigger is one take-profit or stop-loss level resting on OKX.
type Trigger struct {
	AlgoID    string  `json:"algoId"`
	Px        float64 `json:"px"`                  // trigger price
	PxType    string  `json:"pxType,omitempty"`    // last | mark | index
	Kind      string  `json:"kind"`                // conditional | oco | position (attached to the position)
	Full      bool    `json:"full"`                // closes the whole position
	Contracts float64 `json:"contracts,omitempty"` // partial close size (contracts)
}

// Position is one open swap position with its TP/SL. Zero numbers are unknown (omitted).
type Position struct {
	InstID      string    `json:"instId"`
	PosSide     string    `json:"posSide"` // long | short | net (OKX position mode)
	Side        string    `json:"side"`    // long | short: the direction actually held
	MgnMode     string    `json:"mgnMode"` // cross | isolated
	Contracts   float64   `json:"contracts"`
	Coins       float64   `json:"coins,omitempty"`       // contracts × contract value (base coin)
	NotionalUsd float64   `json:"notionalUsd,omitempty"` // USD value at the mark price
	AvgPx       float64   `json:"avgPx"`
	MarkPx      float64   `json:"markPx,omitempty"`
	LiqPx       float64   `json:"liqPx,omitempty"`
	Lever       float64   `json:"lever,omitempty"`
	Upl         float64   `json:"upl"`                // unrealized PnL, USDT
	UplRatio    float64   `json:"uplRatio"`           // upl / initial margin
	MgnRatio    float64   `json:"mgnRatio,omitempty"` // maintenance margin ratio (OKX: liquidation at ≤ 100%)
	Margin      float64   `json:"margin,omitempty"`   // isolated margin, or the cross initial margin
	TP          []Trigger `json:"tp"`
	SL          []Trigger `json:"sl"`
	CTime       int64     `json:"cTime"`
	UTime       int64     `json:"uTime"`
}

// Equity is the account summary from /account/balance (USD).
type Equity struct {
	TotalEq     float64 `json:"totalEq"`
	AvailEq     float64 `json:"availEq,omitempty"`
	Upl         float64 `json:"upl,omitempty"`
	MgnRatio    float64 `json:"mgnRatio,omitempty"`
	NotionalUsd float64 `json:"notionalUsd,omitempty"`
}

// Snapshot is what the endpoint and the agent tool return.
type Snapshot struct {
	Enabled   bool       `json:"enabled"`
	Ts        int64      `json:"ts,omitempty"` // receive time (ms)
	Equity    *Equity    `json:"equity,omitempty"`
	Positions []Position `json:"positions"`
}

// ---- OKX payloads (strings, as OKX sends them) ----

type rawAttached struct {
	AlgoID          string `json:"algoId"`
	TpTriggerPx     string `json:"tpTriggerPx"`
	TpTriggerPxType string `json:"tpTriggerPxType"`
	SlTriggerPx     string `json:"slTriggerPx"`
	SlTriggerPxType string `json:"slTriggerPxType"`
	CloseFraction   string `json:"closeFraction"`
}

type rawPosition struct {
	InstID         string        `json:"instId"`
	PosSide        string        `json:"posSide"`
	MgnMode        string        `json:"mgnMode"`
	Pos            string        `json:"pos"`
	AvgPx          string        `json:"avgPx"`
	MarkPx         string        `json:"markPx"`
	LiqPx          string        `json:"liqPx"`
	Lever          string        `json:"lever"`
	Upl            string        `json:"upl"`
	UplRatio       string        `json:"uplRatio"`
	MgnRatio       string        `json:"mgnRatio"`
	Margin         string        `json:"margin"`
	Imr            string        `json:"imr"`
	NotionalUsd    string        `json:"notionalUsd"`
	CTime          string        `json:"cTime"`
	UTime          string        `json:"uTime"`
	CloseOrderAlgo []rawAttached `json:"closeOrderAlgo"`
}

type rawAlgo struct {
	AlgoID          string `json:"algoId"`
	InstID          string `json:"instId"`
	OrdType         string `json:"ordType"`
	Side            string `json:"side"`
	PosSide         string `json:"posSide"`
	Sz              string `json:"sz"`
	CloseFraction   string `json:"closeFraction"`
	TpTriggerPx     string `json:"tpTriggerPx"`
	TpTriggerPxType string `json:"tpTriggerPxType"`
	SlTriggerPx     string `json:"slTriggerPx"`
	SlTriggerPxType string `json:"slTriggerPxType"`
}

type rawBalance struct {
	TotalEq     string `json:"totalEq"`
	AvailEq     string `json:"availEq"`
	Upl         string `json:"upl"`
	MgnRatio    string `json:"mgnRatio"`
	NotionalUsd string `json:"notionalUsd"`
}

func num(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func ms(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }

// merge builds the positions with their TP/SL. ctVal gives the contract size in coins (0 = unknown).
// An algo order counts for a position when it is on the same instrument and position side and trades
// against it (a sell for a long): stop-entry orders that would open or add are not TP/SL.
func merge(ps []rawPosition, algos []rawAlgo, ctVal func(string) float64) []Position {
	out := make([]Position, 0, len(ps))
	for _, r := range ps {
		pos := num(r.Pos)
		if pos == 0 {
			continue
		}
		side := r.PosSide
		if side != "long" && side != "short" { // net mode: the sign says
			side = "long"
			if pos < 0 {
				side = "short"
			}
		}
		p := Position{InstID: r.InstID, PosSide: r.PosSide, Side: side, MgnMode: r.MgnMode, Contracts: math.Abs(pos),
			NotionalUsd: math.Abs(num(r.NotionalUsd)), AvgPx: num(r.AvgPx), MarkPx: num(r.MarkPx), LiqPx: num(r.LiqPx),
			Lever: num(r.Lever), Upl: num(r.Upl), UplRatio: num(r.UplRatio), MgnRatio: num(r.MgnRatio), Margin: num(r.Margin),
			CTime: ms(r.CTime), UTime: ms(r.UTime), TP: []Trigger{}, SL: []Trigger{}}
		if p.Margin == 0 {
			p.Margin = num(r.Imr)
		}
		if ctVal != nil {
			if cv := ctVal(r.InstID); cv > 0 {
				p.Coins = p.Contracts * cv
			}
		}
		closing := "sell"
		if side == "short" {
			closing = "buy"
		}
		seen := map[string]bool{}
		add := func(id, kind, tp, tpType, sl, slType string, full bool, sz float64) {
			if seen[id] {
				return
			}
			seen[id] = true
			if !full && sz >= p.Contracts {
				full, sz = true, 0
			}
			if full {
				sz = 0
			}
			if px := num(tp); px > 0 {
				p.TP = append(p.TP, Trigger{AlgoID: id, Px: px, PxType: tpType, Kind: kind, Full: full, Contracts: sz})
			}
			if px := num(sl); px > 0 {
				p.SL = append(p.SL, Trigger{AlgoID: id, Px: px, PxType: slType, Kind: kind, Full: full, Contracts: sz})
			}
		}
		for _, a := range algos {
			if a.InstID != r.InstID || a.PosSide != r.PosSide || a.Side != closing {
				continue
			}
			add(a.AlgoID, a.OrdType, a.TpTriggerPx, a.TpTriggerPxType, a.SlTriggerPx, a.SlTriggerPxType, a.CloseFraction == "1", num(a.Sz))
		}
		for _, a := range r.CloseOrderAlgo {
			add(a.AlgoID, "position", a.TpTriggerPx, a.TpTriggerPxType, a.SlTriggerPx, a.SlTriggerPxType, a.CloseFraction == "1" || a.CloseFraction == "", 0)
		}
		// nearest level first
		long := side == "long"
		sort.Slice(p.TP, func(i, j int) bool { return (p.TP[i].Px < p.TP[j].Px) == long })
		sort.Slice(p.SL, func(i, j int) bool { return (p.SL[i].Px > p.SL[j].Px) == long })
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NotionalUsd > out[j].NotionalUsd })
	return out
}

// Service caches the account snapshot for a few seconds and lets one fetch run at a time.
type Service struct {
	c     *Client // nil = disabled
	CtVal func(instID string) float64

	ttl    time.Duration
	mu     sync.Mutex
	snap   Snapshot
	err    error
	at     time.Time
	flight chan struct{}
	logged string // last refresh error logged ("" = ok)
	balLog string // same for the optional balance call (only touched by the one running refresh)
}

// New returns a service; without a complete Config it is disabled and makes no requests.
func New(cfg Config) *Service {
	s := &Service{ttl: 3 * time.Second}
	if cfg.Enabled() {
		s.c = NewClient(cfg)
	}
	return s
}

func (s *Service) Enabled() bool { return s != nil && s.c != nil }

// Read is one uncached GET of any allowed private endpoint (deposits, withdrawals, bills, fills, order
// history …) for the agent: the response's data, decoded generically.
func (s *Service) Read(ctx context.Context, path string, q url.Values) (any, error) {
	if !s.Enabled() {
		return nil, errors.New("okx account not configured")
	}
	var out any
	if err := s.c.get(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Snapshot returns the open positions (all, or one instrument's) with TP/SL and equity.
func (s *Service) Snapshot(ctx context.Context, instID string) (Snapshot, error) {
	if !s.Enabled() {
		return Snapshot{Positions: []Position{}}, nil
	}
	for {
		s.mu.Lock()
		if !s.at.IsZero() && time.Since(s.at) < s.ttl {
			snap, err := s.snap, s.err
			s.mu.Unlock()
			return filter(snap, instID), err
		}
		if s.flight == nil {
			s.flight = make(chan struct{})
			go s.refresh(s.flight)
		}
		wait := s.flight
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		case <-wait:
		}
	}
}

// refresh runs detached from any one caller, so a cancelled request doesn't fail the others.
func (s *Service) refresh(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	snap, err := s.fetch(ctx)
	s.mu.Lock()
	s.snap, s.err, s.at, s.flight = snap, err, time.Now(), nil
	if msg := errText(err); msg != s.logged { // log each new failure once, and the recovery
		if msg != "" {
			log.Printf("okx account: %s", msg)
		} else {
			log.Printf("okx account: ok again")
		}
		s.logged = msg
	}
	s.mu.Unlock()
	close(done)
}

func (s *Service) fetch(ctx context.Context) (Snapshot, error) {
	var ps []rawPosition
	if err := s.c.get(ctx, "/api/v5/account/positions", url.Values{"instType": {"SWAP"}}, &ps); err != nil {
		return Snapshot{}, err
	}
	var algos []rawAlgo
	if len(ps) > 0 { // TP/SL are required: a missing stop must never read as "no stop"
		for _, t := range []string{"conditional", "oco"} {
			var xs []rawAlgo
			if err := s.c.get(ctx, "/api/v5/trade/orders-algo-pending", url.Values{"instType": {"SWAP"}, "ordType": {t}}, &xs); err != nil {
				return Snapshot{}, err
			}
			algos = append(algos, xs...)
		}
	}
	snap := Snapshot{Enabled: true, Ts: time.Now().UnixMilli(), Positions: merge(ps, algos, s.CtVal)}
	var bal []rawBalance // equity is optional: left out when the call fails
	err := s.c.get(ctx, "/api/v5/account/balance", nil, &bal)
	if err == nil && len(bal) > 0 {
		b := bal[0]
		snap.Equity = &Equity{TotalEq: num(b.TotalEq), AvailEq: num(b.AvailEq), Upl: num(b.Upl), MgnRatio: num(b.MgnRatio), NotionalUsd: num(b.NotionalUsd)}
	}
	if msg := errText(err); msg != s.balLog {
		if msg != "" {
			log.Printf("okx account: balance: %s", msg)
		}
		s.balLog = msg
	}
	return snap, nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func filter(s Snapshot, instID string) Snapshot {
	if instID == "" {
		return s
	}
	out := s
	out.Positions = []Position{}
	for _, p := range s.Positions {
		if strings.EqualFold(p.InstID, instID) {
			out.Positions = append(out.Positions, p)
		}
	}
	return out
}

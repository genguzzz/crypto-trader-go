package okxacct

// History pages through one of the account's history endpoints on the server and returns totals and
// groups, so the agent can answer "this year's P&L / deposits / funding fees" in one call instead of
// paging 100 rows at a time through okx_api (and stopping early).

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var tz8 = time.FixedZone("UTC+8", 8*3600)

// histKind is one history endpoint: its path, fixed query, cursor (the field passed back as after=),
// the time field, the numbers to sum and the default grouping.
type histKind struct {
	path    string
	query   url.Values
	cursor  string
	timeKey string
	sums    []string
	group   string
	keep    func(r map[string]any) bool // rows that count (e.g. credited deposits only); nil = all
	note    string
}

var histKinds = map[string]histKind{
	"positions": {path: "/api/v5/account/positions-history", query: url.Values{"instType": {"SWAP"}}, cursor: "uTime", timeKey: "uTime",
		sums: []string{"realizedPnl", "pnl", "fee", "fundingFee", "liqPenalty"}, group: "inst",
		note: "closed swap positions; realizedPnl = pnl + fee + fundingFee + liqPenalty"},
	"fills": {path: "/api/v5/trade/fills-history", query: url.Values{"instType": {"SWAP"}}, cursor: "billId", timeKey: "ts",
		sums: []string{"fee", "fillPnl", "notionalUsd"}, group: "inst", note: "swap fills (OKX keeps about 3 months); one row per fill, so busy periods are long — prefer positions for P&L"},
	"orders": {path: "/api/v5/trade/orders-history-archive", query: url.Values{"instType": {"SWAP"}}, cursor: "ordId", timeKey: "cTime",
		sums: []string{"fee", "pnl"}, group: "state", note: "swap orders as far back as OKX returns them"},
	"bills": {path: "/api/v5/account/bills-archive", cursor: "billId", timeKey: "ts",
		sums: []string{"balChg", "pnl", "fee"}, group: "type", note: "trading-account bills (about 3 months; every fill, fee and funding payment is a row, so long periods may be partial)"},
	"deposits": {path: "/api/v5/asset/deposit-history", cursor: "ts", timeKey: "ts", sums: []string{"amt"}, group: "ccy",
		keep: func(r map[string]any) bool { return str(r, "state") == "2" }, note: "credited deposits (state 2) incl. internal transfers in"},
	"withdrawals": {path: "/api/v5/asset/withdrawal-history", cursor: "ts", timeKey: "ts", sums: []string{"amt", "fee"}, group: "ccy",
		keep: func(r map[string]any) bool { return str(r, "state") == "2" }, note: "completed withdrawals (state 2) incl. internal transfers out"},
	"funding": {path: "/api/v5/asset/bills-history", cursor: "ts", timeKey: "ts", sums: []string{"balChg"}, group: "type",
		note: "funding-account bills (about 1 year): deposits, withdrawals, C2C (116/117/118), transfers, Earn, rebates"},
}

// fundingTypes names the funding-bill types seen on this account plus the common ones.
var fundingTypes = map[string]string{"1": "deposit", "2": "withdrawal", "13": "withdrawal cancelled", "20": "to sub-account",
	"21": "from sub-account", "47": "system reversal", "48": "received", "75": "Simple Earn subscribe", "76": "Simple Earn redeem",
	"116": "C2C order placed", "117": "C2C order filled", "118": "C2C order cancelled", "130": "from trading account",
	"131": "to trading account", "150": "fee rebate", "173": "fee rebate", "458": "creator reward"}

// accountTypes names trading-account bill types.
var accountTypes = map[string]string{"1": "transfer", "2": "trade", "3": "delivery", "5": "liquidation", "7": "interest",
	"8": "funding fee", "9": "ADL", "15": "pnl adjustment", "22": "repay", "24": "spread trading", "26": "structured", "27": "bonus",
	"250": "copy-trading profit share", "300": "fee rebate"}

func str(r map[string]any, k string) string { s, _ := r[k].(string); return s }
func fnum(r map[string]any, k string) float64 {
	v, _ := strconv.ParseFloat(str(r, k), 64)
	return v
}

// HistoryQuery selects a kind, a period (ms, 0 = open) and optional filters.
type HistoryQuery struct {
	Kind, InstID, Ccy, Group string
	Begin, End               int64
	Sample                   int // newest rows to include as-is (0–30)
}

type HistGroup struct {
	Key  string             `json:"key"`
	Name string             `json:"name,omitempty"`
	N    int                `json:"n"`
	Wins int                `json:"wins,omitempty"`
	Sums map[string]float64 `json:"sums"`
}

type History struct {
	Kind     string             `json:"kind"`
	Note     string             `json:"note"`
	From     string             `json:"from,omitempty"` // oldest and newest counted row, Beijing time
	To       string             `json:"to,omitempty"`
	Complete bool               `json:"complete"` // reached the period start or the end of OKX's history
	Rows     int                `json:"rows"`
	Totals   map[string]float64 `json:"totals"`
	Wins     int                `json:"wins,omitempty"`
	Losses   int                `json:"losses,omitempty"`
	GroupBy  string             `json:"groupBy"`
	Groups   []HistGroup        `json:"groups"`
	Sample   []map[string]any   `json:"sample,omitempty"`
}

const (
	histMaxPages = 150
	histBudget   = 45 * time.Second // the agent's tool call must return; very busy accounts get a partial period
)

// getRetry backs off on OKX's per-endpoint rate limit (50011) instead of failing the whole history.
func (s *Service) getRetry(ctx context.Context, path string, q url.Values, out any) error {
	for i := 0; ; i++ {
		err := s.c.get(ctx, path, q, out)
		var ae *APIError
		if i == 5 || !errors.As(err, &ae) || ae.Code != "50011" {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i+1) * 700 * time.Millisecond):
		}
	}
}

// History pages backwards from End (or now) to Begin and aggregates. Rate: ~6 requests/s.
func (s *Service) History(ctx context.Context, q HistoryQuery) (History, error) {
	if !s.Enabled() {
		return History{}, errors.New("okx account not configured")
	}
	k, ok := histKinds[q.Kind]
	if !ok {
		return History{}, fmt.Errorf("unknown kind %q (positions, fills, orders, bills, deposits, withdrawals, funding)", q.Kind)
	}
	group := q.Group
	if group == "" {
		group = k.group
	}
	h := History{Kind: q.Kind, Note: k.note, Totals: map[string]float64{}, GroupBy: group}
	groups := map[string]*HistGroup{}
	var oldest, newest int64
	cursor := ""
	deadline := time.Now().Add(histBudget)
	for page := 0; ; page++ {
		if page == histMaxPages || time.Now().After(deadline) {
			break // complete stays false: the reply says how far back it got
		}
		v := url.Values{"limit": {"100"}}
		for kk, vv := range k.query {
			v[kk] = vv
		}
		if q.InstID != "" && (q.Kind == "positions" || q.Kind == "fills" || q.Kind == "orders" || q.Kind == "bills") {
			v.Set("instId", q.InstID)
		}
		if q.Ccy != "" && (q.Kind == "bills" || q.Kind == "deposits" || q.Kind == "withdrawals" || q.Kind == "funding") {
			v.Set("ccy", q.Ccy)
		}
		if cursor != "" {
			v.Set("after", cursor)
		} else if q.End > 0 && k.cursor == k.timeKey {
			v.Set("after", strconv.FormatInt(q.End+1, 10))
		}
		var rows []map[string]any
		if page > 0 {
			select {
			case <-ctx.Done():
				return h, ctx.Err()
			case <-time.After(170 * time.Millisecond):
			}
		}
		if err := s.getRetry(ctx, k.path, v, &rows); err != nil {
			return h, err
		}
		if len(rows) == 0 {
			h.Complete = true
			break
		}
		past := false
		for _, r := range rows {
			t, _ := strconv.ParseInt(str(r, k.timeKey), 10, 64)
			if q.End > 0 && t > q.End {
				continue
			}
			if q.Begin > 0 && t < q.Begin {
				past = true
				continue
			}
			if k.keep != nil && !k.keep(r) {
				continue
			}
			if q.Kind == "fills" && s.CtVal != nil {
				r["notionalUsd"] = fmt.Sprint(fnum(r, "fillSz") * fnum(r, "fillPx") * s.CtVal(str(r, "instId")))
			}
			h.Rows++
			if oldest == 0 || t < oldest {
				oldest = t
			}
			if t > newest {
				newest = t
			}
			key, name := groupKey(q.Kind, group, r, t)
			g := groups[key]
			if g == nil {
				g = &HistGroup{Key: key, Name: name, Sums: map[string]float64{}}
				groups[key] = g
			}
			g.N++
			for _, f := range k.sums {
				x := fnum(r, f)
				g.Sums[f] += x
				h.Totals[f] += x
			}
			if q.Kind == "positions" {
				if fnum(r, "realizedPnl") > 0 {
					g.Wins++
					h.Wins++
				} else {
					h.Losses++
				}
			}
			if len(h.Sample) < min(max(q.Sample, 0), 30) {
				h.Sample = append(h.Sample, compactRow(r))
			}
		}
		if past {
			h.Complete = true
			break
		}
		cursor = str(rows[len(rows)-1], k.cursor)
		if cursor == "" || len(rows) < 100 {
			h.Complete = len(rows) < 100
			break
		}
	}
	if oldest > 0 {
		h.From = time.UnixMilli(oldest).In(tz8).Format("2006-01-02 15:04")
		h.To = time.UnixMilli(newest).In(tz8).Format("2006-01-02 15:04")
	}
	for _, g := range groups {
		for f, x := range g.Sums {
			g.Sums[f] = round6(x)
		}
		h.Groups = append(h.Groups, *g)
	}
	for f, x := range h.Totals {
		h.Totals[f] = round6(x)
	}
	main := k.sums[0]
	sort.Slice(h.Groups, func(i, j int) bool {
		if group == "month" || group == "day" {
			return h.Groups[i].Key > h.Groups[j].Key
		}
		return math.Abs(h.Groups[i].Sums[main]) > math.Abs(h.Groups[j].Sums[main])
	})
	if len(h.Groups) > 60 {
		h.Groups = h.Groups[:60]
	}
	return h, nil
}

func groupKey(kind, group string, r map[string]any, t int64) (string, string) {
	switch group {
	case "inst":
		return str(r, "instId"), ""
	case "ccy":
		return str(r, "ccy"), ""
	case "month":
		return time.UnixMilli(t).In(tz8).Format("2006-01"), ""
	case "day":
		return time.UnixMilli(t).In(tz8).Format("2006-01-02"), ""
	case "side":
		return str(r, "direction") + str(r, "side"), ""
	case "state":
		return str(r, "state"), ""
	case "type":
		ty := str(r, "type")
		if kind == "funding" {
			return ty + " " + str(r, "ccy"), fundingTypes[ty]
		}
		key := ty
		if st := str(r, "subType"); st != "" {
			key += "/" + st
		}
		return key + " " + str(r, "ccy"), accountTypes[ty]
	}
	return "all", ""
}

func compactRow(r map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range r {
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		if a, ok := v.([]any); ok && len(a) == 0 {
			continue
		}
		out[k] = v
	}
	return out
}

func round6(x float64) float64 {
	p := math.Pow(10, 6-math.Ceil(math.Log10(math.Abs(x)+1e-12)))
	if math.Abs(x) < 1e-9 || math.IsInf(p, 0) {
		return 0
	}
	return math.Round(x*p) / p
}

// ParseDay accepts "2026-01-01" (Beijing), "2026-01-01 08:00" or unix ms; "" = 0.
func ParseDay(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return ms, nil
	}
	for _, f := range []string{"2006-01-02 15:04", "2006-01-02", "2006-01"} {
		if t, err := time.ParseInLocation(f, s, tz8); err == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("bad date %q (use 2026-01-01)", s)
}

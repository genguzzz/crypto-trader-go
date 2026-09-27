package okx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ---------- historical trades (order-flow backfill) ----------
//
// Two public sources, both taker-side trades with sz in contracts for SWAP:
//   - /market/history-trades: last ~3 months, 100 per page, newest first — used for the recent
//     hours not yet published as a file.
//   - daily trade files (OKX "historical market data"): one zip per instrument per UTC+8 day,
//     published the next day — used for complete days.

// Trade is one public fill (taker side).
type Trade struct {
	ID  int64
	Px  float64
	Sz  float64 // contracts for SWAP
	Buy bool
	Ts  int64
}

// HistoryTrades returns up to `limit` (≤100) trades older than `beforeID` (tradeId) — or, when
// beforeID is 0, older than `beforeTs` (0 = latest). Newest first.
func (c *REST) HistoryTrades(ctx context.Context, instID string, beforeID, beforeTs int64, limit int) ([]Trade, error) {
	q := url.Values{"instId": {instID}, "limit": {strconv.Itoa(limit)}}
	switch {
	case beforeID > 0:
		q.Set("type", "1")
		q.Set("after", strconv.FormatInt(beforeID, 10))
	case beforeTs > 0:
		q.Set("type", "2")
		q.Set("after", strconv.FormatInt(beforeTs, 10))
	}
	var raw []struct {
		TradeID, Px, Sz, Side, Ts string
	}
	if err := c.get(ctx, c.trades, "/market/history-trades", q, &raw); err != nil {
		return nil, err
	}
	out := make([]Trade, 0, len(raw))
	for _, r := range raw {
		out = append(out, Trade{ID: i64(r.TradeID), Px: f(r.Px), Sz: f(r.Sz), Buy: r.Side == "buy", Ts: i64(r.Ts)})
	}
	return out, nil
}

// TradeFile is one published daily trade file. Start is the UTC+8 midnight it begins at.
type TradeFile struct {
	Start  int64
	URL    string
	SizeMB float64
}

// TradeFiles lists the daily trade files of a USDT perpetual whose day starts in [begin, end] (ms).
func (c *REST) TradeFiles(ctx context.Context, instID string, begin, end int64) ([]TradeFile, error) {
	family := strings.TrimSuffix(instID, "-SWAP")
	q := url.Values{"module": {"1"}, "instType": {"SWAP"}, "instFamilyList": {family}, "dateAggrType": {"daily"},
		"begin": {strconv.FormatInt(begin, 10)}, "end": {strconv.FormatInt(end, 10)}}
	var raw []struct {
		Details []struct {
			GroupDetails []struct{ DateTs, Filename, SizeMB, URL string }
		}
	}
	if err := c.get(ctx, c.misc, "/public/market-data-history", q, &raw); err != nil {
		return nil, err
	}
	var out []TradeFile
	for _, d := range raw {
		for _, det := range d.Details {
			for _, g := range det.GroupDetails {
				if strings.HasPrefix(g.Filename, instID+"-trades-") {
					out = append(out, TradeFile{Start: i64(g.DateTs), URL: g.URL, SizeMB: f(g.SizeMB)})
				}
			}
		}
	}
	return out, nil
}

// ReadTradeFile downloads a daily trade zip and calls fn for every trade (file order: oldest first).
func (c *REST) ReadTradeFile(ctx context.Context, u string, fn func(Trade)) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	hc := &http.Client{Timeout: 5 * time.Minute, Transport: c.HTTP.Transport}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("trade file: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}
	if len(zr.File) == 0 {
		return errors.New("trade file: empty zip")
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return parseTradeCSV(rc, fn)
}

// parseTradeCSV reads "instrument_name,trade_id,side,price,size,created_time[,source]".
func parseTradeCSV(r io.Reader, fn func(Trade)) error {
	cr := csv.NewReader(r)
	cr.ReuseRecord = true
	cr.FieldsPerRecord = -1
	head, err := cr.Read()
	if err != nil {
		return err
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF"))] = i
	}
	ci, cs, cp, cz, ct := col["trade_id"], col["side"], col["price"], col["size"], col["created_time"]
	for _, k := range []string{"trade_id", "side", "price", "size", "created_time"} {
		if _, ok := col[k]; !ok {
			return fmt.Errorf("trade file: missing column %s", k)
		}
	}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if len(rec) <= max(ci, cs, cp, cz, ct) {
			continue
		}
		fn(Trade{ID: i64(rec[ci]), Px: f(rec[cp]), Sz: f(rec[cz]), Buy: rec[cs] == "buy", Ts: i64(rec[ct])})
	}
}

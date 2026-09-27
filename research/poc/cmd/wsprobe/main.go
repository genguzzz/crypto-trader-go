// wsprobe subscribes the whole eligible USDT swap pool on OKX public WebSockets and measures
// whether the blueprint's "WS-maintained 1m window for the full pool" is realistic:
//   - business WS candle1m: does every instrument get a confirm=1 bar each minute, and how late?
//   - public WS tickers / open-interest / funding-rate: message rates and update intervals
//   - subscribe acks/errors, reconnects
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"crypto-trader-go/research/poc/okx"
)

const (
	publicURL   = "wss://ws.okx.com:8443/ws/v5/public"
	businessURL = "wss://ws.okx.com:8443/ws/v5/business"
)

type stats struct {
	mu sync.Mutex
	// candle: key=barTs -> instId -> first confirm receive lag (ms after bar close)
	confirmLag map[int64]map[string]int64
	candleMsgs int
	// channel -> instId -> receive times
	updates   map[string]map[string][]time.Time
	msgs      map[string]int
	events    map[string]int
	bytes     int
	subErrors []string
}

func main() {
	dur := flag.Duration("d", 4*time.Minute, "run duration")
	out := flag.String("out", "", "json report")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()
	c := okx.NewClient()
	var ins []okx.Instrument
	if err := c.Get(ctx, nil, "/public/instruments", url.Values{"instType": {"SWAP"}}, &ins); err != nil {
		panic(err)
	}
	el := okx.Eligible(ins, nil)
	ids := make([]string, len(el))
	for i, x := range el {
		ids[i] = x.InstID
	}
	fmt.Printf("subscribing %d instruments for %s\n", len(ids), *dur)

	st := &stats{confirmLag: map[int64]map[string]int64{}, updates: map[string]map[string][]time.Time{}, msgs: map[string]int{}, events: map[string]int{}}
	args := func(ch string) []map[string]string {
		a := make([]map[string]string, len(ids))
		for i, id := range ids {
			a[i] = map[string]string{"channel": ch, "instId": id}
		}
		return a
	}
	var wg sync.WaitGroup
	run := func(name, u string, a []map[string]string) {
		defer wg.Done()
		runConn(ctx, name, u, a, st)
	}
	wg.Add(4)
	go run("business-candle1m", businessURL, args("candle1m"))
	go run("public-tickers", publicURL, args("tickers"))
	go run("public-oi", publicURL, args("open-interest"))
	go run("public-funding", publicURL, args("funding-rate"))
	wg.Wait()

	report(st, ids, *out)
}

func runConn(ctx context.Context, name, u string, args []map[string]string, st *stats) {
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := d.DialContext(ctx, u, nil)
	if err != nil {
		st.mu.Lock()
		st.subErrors = append(st.subErrors, name+": dial "+err.Error())
		st.mu.Unlock()
		return
	}
	defer conn.Close()
	// Batch subscribe (OKX caps a single request payload at 64KB).
	for i := 0; i < len(args); i += 100 {
		j := min(i+100, len(args))
		if err := conn.WriteJSON(map[string]any{"op": "subscribe", "args": args[i:j]}); err != nil {
			st.mu.Lock()
			st.subErrors = append(st.subErrors, name+": write "+err.Error())
			st.mu.Unlock()
			return
		}
	}
	go func() { // ping every 25s, close on ctx done
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				conn.Close()
				return
			case <-t.C:
				conn.WriteMessage(websocket.TextMessage, []byte("ping"))
			}
		}
	}()
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				st.mu.Lock()
				st.subErrors = append(st.subErrors, name+": read "+err.Error())
				st.mu.Unlock()
			}
			return
		}
		now := time.Now()
		if string(msg) == "pong" {
			continue
		}
		var m struct {
			Event string `json:"event"`
			Code  string `json:"code"`
			Msg   string `json:"msg"`
			Arg   struct {
				Channel string `json:"channel"`
				InstID  string `json:"instId"`
			} `json:"arg"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(msg, &m) != nil {
			continue
		}
		st.mu.Lock()
		st.bytes += len(msg)
		if m.Event != "" {
			st.events[name+":"+m.Event]++
			if m.Event == "error" {
				st.subErrors = append(st.subErrors, fmt.Sprintf("%s: %s %s", name, m.Code, m.Msg))
			}
			st.mu.Unlock()
			continue
		}
		st.msgs[m.Arg.Channel]++
		if st.updates[m.Arg.Channel] == nil {
			st.updates[m.Arg.Channel] = map[string][]time.Time{}
		}
		st.updates[m.Arg.Channel][m.Arg.InstID] = append(st.updates[m.Arg.Channel][m.Arg.InstID], now)
		if m.Arg.Channel == "candle1m" {
			var raw [][]string
			if json.Unmarshal(m.Data, &raw) == nil {
				for _, k := range okx.ParseCandles(raw) {
					st.candleMsgs++
					if k.Confirmed {
						if st.confirmLag[k.Ts] == nil {
							st.confirmLag[k.Ts] = map[string]int64{}
						}
						if _, ok := st.confirmLag[k.Ts][m.Arg.InstID]; !ok {
							st.confirmLag[k.Ts][m.Arg.InstID] = now.UnixMilli() - (k.Ts + 60_000)
						}
					}
				}
			}
		}
		st.mu.Unlock()
	}
}

func pct(xs []int64, p float64) int64 {
	if len(xs) == 0 {
		return -1
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	return xs[int(float64(len(xs)-1)*p)]
}

func report(st *stats, ids []string, out string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	fmt.Printf("events %v\nsubscribe/read errors %v\nbytes received %d\n", st.events, st.subErrors, st.bytes)
	res := map[string]any{"instruments": len(ids), "events": st.events, "errors": st.subErrors, "bytes": st.bytes}
	var bars []int64
	for ts := range st.confirmLag {
		bars = append(bars, ts)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i] < bars[j] })
	var perMin []map[string]any
	for _, ts := range bars {
		var lags []int64
		for _, l := range st.confirmLag[ts] {
			lags = append(lags, l)
		}
		row := map[string]any{"barOpen": time.UnixMilli(ts).UTC().Format("15:04"), "confirmed": len(lags), "of": len(ids),
			"lagP50ms": pct(lags, 0.5), "lagP95ms": pct(lags, 0.95), "lagMaxms": pct(lags, 1)}
		perMin = append(perMin, row)
		fmt.Printf("candle1m bar %s: confirm=1 received for %d/%d  lag p50 %dms p95 %dms max %dms\n", row["barOpen"], len(lags), len(ids), row["lagP50ms"], row["lagP95ms"], row["lagMaxms"])
	}
	res["candleConfirmPerMinute"] = perMin
	chans := map[string]any{}
	for ch, m := range st.updates {
		var gaps []int64
		for _, ts := range m {
			for i := 1; i < len(ts); i++ {
				gaps = append(gaps, ts[i].Sub(ts[i-1]).Milliseconds())
			}
		}
		info := map[string]any{"messages": st.msgs[ch], "instrumentsSeen": len(m), "intervalP50ms": pct(gaps, 0.5), "intervalP95ms": pct(gaps, 0.95)}
		chans[ch] = info
		fmt.Printf("%-14s msgs %6d  instruments seen %3d  per-inst interval p50 %6dms p95 %6dms\n", ch, st.msgs[ch], len(m), info["intervalP50ms"], info["intervalP95ms"])
	}
	res["channels"] = chans
	if out != "" {
		b, _ := json.MarshalIndent(res, "", " ")
		os.WriteFile(out, b, 0o644)
	}
}

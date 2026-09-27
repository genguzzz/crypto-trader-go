package market

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

// tradeMsg builds a push in OKX's real shape (all strings except the numeric seqId).
func tradeMsg(id string, trades ...[3]any) okx.Message { // [ts ms, px, "buy"/"sell"] with sz 1
	var xs []map[string]any
	for i, t := range trades {
		xs = append(xs, map[string]any{"instId": id, "tradeId": fmt.Sprint(1000 + i), "ts": fmt.Sprint(t[0]), "px": fmt.Sprint(t[1]),
			"sz": "1", "side": t[2].(string), "count": "1", "source": "0", "seqId": 123456789 + i})
	}
	b, _ := json.Marshal(xs)
	return okx.Message{Arg: okx.Arg{Channel: "trades", InstID: id}, Data: b}
}

func TestTradesAggregatePerCompleteMinute(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "f.db"))
	e := New(Config{Threshold: 1, TopN: 10}, okx.NewREST(), st)
	id := "X-USDT-SWAP"
	in := newInst(id)
	in.CtVal, in.TickSz = 0.01, 0.1
	e.insts[id] = in
	m0 := int64(1_800_000_000_000) / minuteMs * minuteMs
	in.flow = &flowState{minute: m0, since: m0 + 5_000, levels: map[int64][2]float64{}} // subscribed 5s into m0

	e.onTrades(tradeMsg(id, [3]any{m0 + 6_000, 100.0, "buy"}))                                                   // partial minute: never stored
	e.onTrades(tradeMsg(id, [3]any{m0 + minuteMs + 1, 100.0, "buy"}, [3]any{m0 + minuteMs + 2, 100.04, "sell"})) // rolls m0
	e.onTrades(tradeMsg(id, [3]any{m0 + 3*minuteMs, 101.0, "buy"}))                                              // closes m1, quiet m2
	var rows []store.FlowRow
	for len(e.flowCh) > 0 {
		rows = append(rows, <-e.flowCh)
	}
	if len(rows) != 2 || rows[0].Ts != m0+minuteMs || rows[1].Ts != m0+2*minuteMs {
		t.Fatalf("rows: %+v", rows)
	}
	r := rows[0]
	if r.Buy != 1 || math.Abs(r.Sell-1.0004) > 1e-9 || r.N != 2 { // q = px × sz × ctVal
		t.Fatalf("m1 buy %v sell %v n %d", r.Buy, r.Sell, r.N)
	}
	var lv [][3]float64
	eachLevel(r.Levels, r.Step, func(px, b, s float64) { lv = append(lv, [3]float64{px, b, s}) })
	if r.Step != 0.1 || len(lv) != 1 || lv[0][1] != 1 || lv[0][2] < 1.0003 { // 100 and 100.04 share the 0.1 level
		t.Fatalf("levels step %v %+v", r.Step, lv)
	}
	if rows[1].N != 0 || rows[1].Buy != 0 { // quiet but fully covered minute is stored as zero (coverage)
		t.Fatalf("quiet minute: %+v", rows[1])
	}
}

func TestMergeLevelsBoundsLevelCount(t *testing.T) {
	m := map[int64][2]float64{}
	for k := int64(0); k < 500; k++ {
		m[1000+k] = [2]float64{1, 2}
	}
	lv, step := mergeLevels([]store.FlowRow{{Step: 0.1, Levels: packLevels(m)}})
	if len(lv) > flowLevelsMax || step <= 0.1 {
		t.Fatalf("%d levels step %v", len(lv), step)
	}
	var b, s float64
	for _, x := range lv {
		b, s = b+x[1], s+x[2]
	}
	if b < 499.9 || s < 999.9 {
		t.Fatalf("volume lost in merge: %v %v", b, s)
	}
}

// Bars fully covered by recorded trades use them; other ≥5m bars fall back to rubik taker volume;
// partially recorded bars are never mixed.
func TestOrderFlowMergesTradesAndRubik(t *testing.T) {
	now := time.Now().UnixMilli()
	cur := now / (5 * minuteMs) * (5 * minuteMs)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data [][]string
		if r.URL.Query().Get("end") == "" {
			for k := int64(1); k <= 12; k++ { // 12 closed 5m periods before the forming one
				data = append(data, []string{fmt.Sprint(cur - k*5*minuteMs), "3", "7"})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"code": "0", "data": data})
	}))
	defer srv.Close()
	st, _ := store.Open(filepath.Join(t.TempDir(), "o.db"))
	rest := okx.NewREST()
	rest.BaseURL = srv.URL
	e := New(Config{Threshold: 1, TopN: 10}, rest, st)
	id := "X-USDT-SWAP"
	e.catalog[id] = okx.Instrument{InstID: id, CtVal: 1}
	full := cur - 2*5*minuteMs // this 5m bar is fully recorded
	var rows []store.FlowRow
	for k := int64(0); k < 5; k++ {
		rows = append(rows, store.FlowRow{InstID: id, Ts: full + k*minuteMs, Buy: 100, Sell: 1, Step: 1})
	}
	rows = append(rows, store.FlowRow{InstID: id, Ts: cur - 5*minuteMs, Buy: 50, Sell: 50, Step: 1}) // partial bar
	st.PutFlow(rows)
	resp, err := e.OrderFlow(context.Background(), id, "5m", 20, false)
	if err != nil {
		t.Fatal(err)
	}
	by := map[int64]FlowBar{}
	for _, b := range resp.Bars {
		by[b.T] = b
	}
	if b := by[full]; b.Src != "trades" || b.Buy != 500 {
		t.Fatalf("recorded bar: %+v", b)
	}
	if b := by[cur-5*minuteMs]; b.Src != "rubik" || b.Buy != 7 || b.Sell != 3 {
		t.Fatalf("partially recorded bar should come from rubik: %+v", b)
	}
	if _, ok := by[cur]; ok {
		t.Fatal("forming bar without trades must be unknown")
	}
}

package realtrade

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"crypto-trader-go/internal/okxacct"
	"crypto-trader-go/internal/okxorder"
)

// steerSink is the chat agent: it records every message and can refuse (busy) for a while.
type steerSink struct {
	mu   sync.Mutex
	msgs []string
	busy int // refuse this many deliveries with ErrSteerLater
}

func (k *steerSink) steer(chat int64, msg string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.busy > 0 {
		k.busy--
		return ErrSteerLater
	}
	k.msgs = append(k.msgs, msg)
	return nil
}

func (k *steerSink) all() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.msgs...)
}

// wait until n messages arrived (deliveries run in goroutines) and return the last.
func (k *steerSink) wait(t *testing.T, e *env, n int) string {
	t.Helper()
	for i := 0; i < 300 && len(k.all()) < n; i++ {
		e.s.deliverSteers(context.Background())
		time.Sleep(5 * time.Millisecond)
	}
	got := k.all()
	if len(got) < n {
		t.Fatalf("waited for %d chat messages, got %d: %q", n, len(got), got)
	}
	return got[n-1]
}

func withSteer(e *env) *steerSink {
	k := &steerSink{}
	e.s.opts.Settle = -1
	e.s.SetSteerer(k.steer)
	return k
}

func approve(t *testing.T, e *env, in ProposeInput) Proposal {
	t.Helper()
	p, err := e.s.Propose(context.Background(), in)
	if err != nil {
		t.Fatalf("propose %s: %v", in.Kind, err)
	}
	got, err := e.s.Approve(context.Background(), p.ID, "owner", "app")
	if err != nil || got.Status != StatusExecuted {
		t.Fatalf("approve %s: %+v %v", in.Kind, got, err)
	}
	return got
}

func paths(p Proposal) []string {
	var reqs []okxorder.Request
	json.Unmarshal(p.Request, &reqs)
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = strings.TrimPrefix(r.Path, "/api/v5/")
	}
	return out
}

// One AI position through its whole chain in a dry run: open → add → reduce by a resting limit →
// stop to break-even → close; each execution tells the chat, the closed position cancels its signals
// and a leftover resting add.
func TestPositionChainDryRun(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	sink := withSteer(e)
	var cancelled []int64
	e.s.SetSignals(func(chat int64, inst string, pos int64) string { return "#4 BTC ≥ 103000（到了先减一半）" },
		func(pos int64, reason string) []int64 { cancelled = append(cancelled, pos); return []int64{4} })

	open := approve(t, e, input()) // 0.1 BTC at 100000, SL 98000 / TP 104000
	if open.PosID != open.ID || open.CurContracts != 0.1 || open.CurAvgPx != 100000 || open.OrdState != OrdFilled {
		t.Fatalf("open %+v", open)
	}
	msg := sink.wait(t, e, 1)
	for _, want := range []string{"✅ [DRY RUN] #1 已成交 · 开仓 BTC 多单 · 市价 0.1 张", "逐仓 5×", "仓位：BTC 做多 0.1 张 · 均价 100000",
		"止损 98000（ctg 0.1 张）", "此仓位的操作：", "#1 ", "此仓位的信号：#4", "get_positions", "tradeId"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("open message lacks %q:\n%s", want, msg)
		}
	}

	e.setPx(101000)
	add := approve(t, e, ProposeInput{ChatID: 7, Kind: KindAdd, InstID: "BTC", Side: "long", MarginUSDT: 20, SL: 98500, TP: 104500,
		Rationale: "回踩 101000 站稳，OI 继续上升", Invalidation: "跌回 100000 下方"})
	if add.PosID != open.ID || add.Leverage != 5 || add.MarginMode != "isolated" || add.Contracts != 0.09 ||
		strings.Join(paths(add), ",") != "trade/order-algo,trade/cancel-algos,trade/order" {
		t.Fatalf("add %+v %v", add, paths(add))
	}
	root, _ := e.s.get(open.ID)
	if root.CurContracts != 0.19 || root.CurSL != 98500 || root.CurTP != 104500 || root.curAlgo != "ctgt2" {
		t.Fatalf("root after add %+v", root)
	}
	msg = sink.wait(t, e, 2)
	if !strings.Contains(msg, "#2 已成交 · 补仓 BTC 多单") || !strings.Contains(msg, "整仓") || !strings.Contains(msg, "0.19 张") {
		t.Fatalf("add message:\n%s", msg)
	}

	// a limit reduce rests, then fills when the price gets there
	red := approve(t, e, ProposeInput{ChatID: 7, Kind: KindReduce, InstID: "BTC", Side: "long", Fraction: 0.5, OrdType: "limit", Px: 102500,
		OrderMin: 120, Rationale: "到前高 102500 先减一半锁定利润"})
	if red.OrdState != OrdLive || red.Contracts != 0.09 || red.OrderUntil != e.clock().Add(2*time.Hour).UnixMilli() {
		t.Fatalf("reduce %+v", red)
	}
	if msg := sink.wait(t, e, 3); !strings.Contains(msg, "📌 [DRY RUN] #3 限价单已挂出") {
		t.Fatalf("placed message:\n%s", msg)
	}
	if _, err := e.s.Propose(context.Background(), ProposeInput{ChatID: 7, Kind: KindAdjust, InstID: "BTC", Side: "long", SL: 100100, TP: 104500,
		Rationale: "先不动，等减仓成交后再提"}); err == nil || !strings.Contains(err.Error(), "still resting") {
		t.Fatalf("adjust while a limit rests: %v", err)
	}
	e.setPx(102600)
	e.s.follow(context.Background())
	red, _ = e.s.get(red.ID)
	root, _ = e.s.get(open.ID)
	if red.OrdState != OrdFilled || red.FillPx != 102500 || root.CurContracts != 0.1 || root.realized < 1.82 || root.realized > 1.83 {
		t.Fatalf("reduce filled %+v root %+v", red, root) // 0.09 × (102500 − 100473.68) × 0.01 ≈ 1.824
	}
	if msg := sink.wait(t, e, 4); !strings.Contains(msg, "#3 限价单已成交 · 减仓 BTC 多单 · 0.09 张 @102500") {
		t.Fatalf("filled message:\n%s", msg)
	}

	adj := approve(t, e, ProposeInput{ChatID: 7, Kind: KindAdjust, InstID: "BTC", Side: "long", SL: 101000, TP: 104500, Rationale: "减仓后止损上移到保本上方"})
	root, _ = e.s.get(open.ID)
	if root.CurSL != 101000 || root.curAlgo != "ctgt2" || strings.Join(paths(adj), ",") != "trade/amend-algos" { // the add's whole-position TP/SL is amended
		t.Fatalf("adjust %v root %+v", paths(adj), root)
	}

	// a resting add, then the close: the add is cancelled first, the position ends with its P&L
	e.setPx(102000)
	approve(t, e, ProposeInput{ChatID: 7, Kind: KindAdd, InstID: "BTC", Side: "long", MarginUSDT: 20, OrdType: "limit", Px: 101500,
		SL: 101000, TP: 104500, Rationale: "回踩 101500 再补一点", Invalidation: "跌破 101000"})
	cl := approve(t, e, ProposeInput{ChatID: 7, Kind: KindClose, InstID: "BTC", Side: "long", Rationale: "资金费转负，提前全部了结"})
	if strings.Join(paths(cl), ",") != "trade/close-position,trade/cancel-algos" {
		t.Fatalf("close %v", paths(cl))
	}
	root, _ = e.s.get(open.ID)
	if root.ClosedAt == 0 || root.CloseKind != "manual" || root.PnlUSDT < 3 || len(cancelled) != 1 || cancelled[0] != open.ID {
		t.Fatalf("closed root %+v cancelled %v", root, cancelled)
	}
	if late, _ := e.s.get(5); late.Kind != KindAdd || late.OrdState != OrdCanceled {
		t.Fatalf("resting add not cancelled: %+v", late)
	}
	sink.wait(t, e, 9)
	all := strings.Join(sink.all(), "\n---\n")
	for _, want := range []string{"⌛ [DRY RUN] #5 限价单未成交，已撤单", "📕 [DRY RUN] 仓位 #1 已结束", "复盘"} {
		if !strings.Contains(all, want) {
			t.Fatalf("messages lack %q:\n%s", want, all)
		}
	}
	// the position is gone: nothing left to act on
	if _, err := e.s.Propose(context.Background(), ProposeInput{ChatID: 7, Kind: KindReduce, InstID: "BTC", Side: "long", Fraction: 0.5,
		Rationale: "position should be gone by now"}); err == nil {
		t.Fatal("reduce of a closed position accepted")
	}
}

// fakeLive stands in for the live client: it records every order and answers like OKX.
type fakeLive struct {
	mu   sync.Mutex
	sent []okxorder.Request
	fail string // path to fail
}

func (*fakeLive) Mode() okxorder.Mode { return okxorder.Live }
func (f *fakeLive) Place(_ context.Context, o okxorder.Order) (okxorder.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res := okxorder.Result{Mode: okxorder.Live, Requests: o.Requests()}
	for _, r := range o.Requests() {
		if r.Path == f.fail {
			return res, errors.New("okx " + r.Path + " rejected: 51008 Insufficient margin")
		}
		f.sent = append(f.sent, r)
		if r.Path == "/api/v5/trade/order" {
			res.OrdID = "900"
		}
	}
	return res, nil
}
func (f *fakeLive) paths() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.sent {
		out = append(out, strings.TrimPrefix(r.Path, "/api/v5/"))
	}
	return strings.Join(out, ",")
}

// liveAcct is the owner's OKX account: positions, and the orders by client id.
type liveAcct struct {
	mu      sync.Mutex
	pos     []okxacct.Position
	pending map[string]map[string]any
	history map[string]map[string]any
}

func (*liveAcct) Enabled() bool { return true }
func (a *liveAcct) Snapshot(_ context.Context, inst string) (okxacct.Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []okxacct.Position{}
	for _, p := range a.pos {
		if inst == "" || p.InstID == inst {
			out = append(out, p)
		}
	}
	return okxacct.Snapshot{Enabled: true, Positions: out, Equity: &okxacct.Equity{TotalEq: 1000, AvailEq: 800}}, nil
}
func (a *liveAcct) Read(_ context.Context, path string, _ url.Values) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rows := []any{}
	switch path {
	case "/api/v5/account/config":
		return []any{map[string]any{"posMode": "long_short_mode"}}, nil
	case "/api/v5/trade/orders-pending":
		for _, o := range a.pending {
			rows = append(rows, o)
		}
	case "/api/v5/trade/orders-history":
		for _, o := range a.history {
			rows = append(rows, o)
		}
	}
	return rows, nil
}
func (a *liveAcct) set(f func(a *liveAcct)) { a.mu.Lock(); f(a); a.mu.Unlock() }

// A live limit open: it rests, fills partly, expires — the server cancels the rest by its client id —
// and the filled part becomes the AI position; the chat hears about the placement and the partial fill.
func TestLimitLifecycleLive(t *testing.T) {
	f := &fakeLive{}
	e := newEnv(t, f)
	acct := &liveAcct{pending: map[string]map[string]any{}, history: map[string]map[string]any{}}
	e.s.SetAccount(acct)
	sink := withSteer(e)
	in := input()
	in.OrdType, in.Px, in.OrderMin, in.MarginMode = "limit", 99500, 30, "cross"
	p := approve(t, e, in)
	if p.OrdState != OrdLive || p.MarginMode != "cross" || f.paths() != "account/set-leverage,trade/order" {
		t.Fatalf("placed %+v %s", p, f.paths())
	}
	if !strings.Contains(string(f.sent[0].Body), `"mgnMode":"cross"`) || !strings.Contains(string(f.sent[1].Body), `"ordType":"limit","sz":"0.10","px":"99500.0"`) {
		t.Fatalf("bodies %s %s", f.sent[0].Body, f.sent[1].Body)
	}
	sink.wait(t, e, 1)
	acct.set(func(a *liveAcct) {
		a.pending["ctgp1"] = map[string]any{"clOrdId": "ctgp1", "state": "partially_filled", "accFillSz": "0.04", "avgPx": "99500"}
	})
	e.s.follow(context.Background())
	if got, _ := e.s.get(p.ID); got.OrdState != OrdPartial || got.Filled != 0.04 {
		t.Fatalf("partial %+v", got)
	}
	e.advance(31 * time.Minute)
	e.s.follow(context.Background())
	if !strings.HasSuffix(f.paths(), "trade/cancel-order") || !strings.Contains(string(f.sent[len(f.sent)-1].Body), `"clOrdId":"ctgp1"`) {
		t.Fatalf("expiry cancel %s", f.paths())
	}
	acct.set(func(a *liveAcct) {
		delete(a.pending, "ctgp1")
		a.history["ctgp1"] = map[string]any{"clOrdId": "ctgp1", "state": "canceled", "accFillSz": "0.04", "avgPx": "99500"}
		a.pos = []okxacct.Position{{InstID: "BTC-USDT-SWAP", Side: "long", PosSide: "long", MgnMode: "cross", Contracts: 0.04, AvgPx: 99500, Lever: 5,
			SL: []okxacct.Trigger{{AlgoID: "A1", ClientID: "ctga1", Px: 98000, Contracts: 0.04}}, TP: []okxacct.Trigger{{AlgoID: "A1", ClientID: "ctga1", Px: 104000, Contracts: 0.04}}}}
	})
	e.s.follow(context.Background())
	got, _ := e.s.get(p.ID)
	if got.OrdState != OrdCanceled || got.CurContracts != 0.04 || got.ClosedAt != 0 {
		t.Fatalf("settled %+v", got)
	}
	msg := sink.wait(t, e, 2)
	for _, want := range []string{"⚠️ #1 限价单部分成交 0.04/0.1 张 @99500，剩余已撤单", "仓位：BTC 做多 0.04 张", "全仓", "止损 98000（ctg 0.04 张）"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q:\n%s", want, msg)
		}
	}
	// now a live reduce: ctg's size-bound TP/SL becomes whole-position first, then the reduce-only order
	e.advance(2 * time.Minute)
	r := approve(t, e, ProposeInput{ChatID: 7, Kind: KindReduce, InstID: "BTC", Side: "long", Fraction: 0.5, Rationale: "到 4H 阻力位先减一半仓位"})
	if r.PosID != p.ID || !strings.HasSuffix(f.paths(), "trade/order-algo,trade/cancel-algos,trade/order") {
		t.Fatalf("reduce %+v %s", r, f.paths())
	}
	if b := string(f.sent[len(f.sent)-1].Body); !strings.Contains(b, `"side":"sell","posSide":"long","ordType":"market","sz":"0.02"`) || !strings.Contains(b, `"tdMode":"cross"`) {
		t.Fatalf("reduce body %s", b)
	}
}

// An unfilled limit open frees its slot; the owner's own position can be closed, never opened over.
func TestUnfilledAndOwnerPosition(t *testing.T) {
	f := &fakeLive{}
	e := newEnv(t, f)
	acct := &liveAcct{pending: map[string]map[string]any{}, history: map[string]map[string]any{}}
	e.s.SetAccount(acct)
	in := input()
	in.OrdType, in.Px = "limit", 99000
	p := approve(t, e, in)
	acct.set(func(a *liveAcct) {
		a.history["ctgp1"] = map[string]any{"clOrdId": "ctgp1", "state": "canceled", "accFillSz": "0", "avgPx": ""}
	})
	e.s.follow(context.Background())
	if got, _ := e.s.get(p.ID); got.OrdState != OrdCanceled || got.ClosedAt == 0 || got.CloseKind != "unfilled" {
		t.Fatalf("unfilled %+v", got)
	}
	acct.set(func(a *liveAcct) {
		a.pos = []okxacct.Position{{InstID: "ETH-USDT-SWAP", Side: "short", PosSide: "short", MgnMode: "isolated", Contracts: 2, AvgPx: 3000, Lever: 10}}
	})
	eth := ProposeInput{ChatID: 7, InstID: "ETH", Side: "short", MarginUSDT: 20, Leverage: 5, SL: 102000, TP: 96000, Rationale: "a fresh short on ETH here", Invalidation: "back above 3100"}
	if _, err := e.s.Propose(context.Background(), eth); err == nil || !strings.Contains(err.Error(), "already holds") {
		t.Fatalf("open over the owner's position: %v", err)
	}
	c := approve(t, e, ProposeInput{ChatID: 7, Kind: KindClose, InstID: "ETH", Side: "short", Rationale: "他让我帮他把 ETH 空单平掉"})
	if c.PosID != 0 || !strings.HasSuffix(f.paths(), "trade/close-position") || !strings.Contains(string(f.sent[len(f.sent)-1].Body), `"posSide":"short"`) {
		t.Fatalf("close owner's %+v %s", c, f.paths())
	}
}

// Re-validation at approval for the kinds that act on a position, and the caps by kind.
func TestKindRevalidationAndCaps(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	approve(t, e, input())
	add := ProposeInput{ChatID: 7, Kind: KindAdd, InstID: "BTC", Side: "long", MarginUSDT: 20, SL: 98500, TP: 104000, Rationale: "加仓理由足够长的一句话", Invalidation: "跌破 99000"}
	wider := add
	wider.SL = 97000
	if _, err := e.s.Propose(context.Background(), wider); err == nil || !strings.Contains(err.Error(), "farther away") {
		t.Fatalf("wider stop: %v", err)
	}
	// the position closes between proposal and approval: the add is invalidated, nothing sent
	p, err := e.s.Propose(context.Background(), add)
	if err != nil {
		t.Fatal(err)
	}
	e.setPx(97900)
	e.s.follow(context.Background()) // stop hit
	e.setPx(100000)
	if got, _ := e.s.Approve(context.Background(), p.ID, "owner", "app"); got.Status != StatusInvalidated || !strings.Contains(got.Error, "no long position") {
		t.Fatalf("add after the stop: %+v", got)
	}
	// after the daily loss stop: no open, no add — but closes, reduces and adjustments still go
	e.s.db.Exec(`INSERT INTO trade_proposals(created, updated, source, inst_id, side, margin, leverage, ref_px, sl, tp, expires_at, status, status_at, mode, closed_at, pnl)
		VALUES(1,1,'chat','SOL-USDT-SWAP','long',10,2,1,1,1,1,'executed',1,'dryrun',?,-60)`, e.clock().UnixMilli())
	e2 := ProposeInput{ChatID: 7, InstID: "ETH", Side: "long", MarginUSDT: 20, Leverage: 5, SL: 98000, TP: 104000, Rationale: "after the loss stop here", Invalidation: "back below 99k"}
	if _, err := e.s.Propose(context.Background(), e2); err == nil || !strings.Contains(err.Error(), "daily loss") {
		t.Fatalf("open after the loss stop: %v", err)
	}
	e.s.db.Exec(`DELETE FROM trade_proposals WHERE inst_id='SOL-USDT-SWAP'`)
	approve(t, e, e2)
	e.s.db.Exec(`INSERT INTO trade_proposals(created, updated, source, inst_id, side, margin, leverage, ref_px, sl, tp, expires_at, status, status_at, mode, closed_at, pnl)
		VALUES(1,1,'chat','SOL-USDT-SWAP','long',10,2,1,1,1,1,'executed',1,'dryrun',?,-60)`, e.clock().UnixMilli())
	add.InstID = "ETH"
	if _, err := e.s.Propose(context.Background(), add); err == nil || !strings.Contains(err.Error(), "daily loss") {
		t.Fatalf("add after the loss stop: %v", err)
	}
	approve(t, e, ProposeInput{ChatID: 7, Kind: KindReduce, InstID: "ETH", Side: "long", Fraction: 0.5, Rationale: "亏损日先降低一半风险敞口"})
	// adds per position are capped
	e.s.db.Exec(`DELETE FROM trade_proposals WHERE inst_id='SOL-USDT-SWAP'`)
	e.s.opts.Limits.MaxAdds = 1
	approve(t, e, add)
	if _, err := e.s.Propose(context.Background(), add); err == nil || !strings.Contains(err.Error(), "cap is 1") {
		t.Fatalf("second add: %v", err)
	}
	// reduce of more than there is: a close
	if _, err := e.s.Propose(context.Background(), ProposeInput{ChatID: 7, Kind: KindReduce, InstID: "ETH", Side: "long", Fraction: 1, Rationale: "全部减掉的理由"}); err == nil {
		t.Fatal("reduce of all accepted")
	}
	// a limit that would fill at once is a market order
	if _, err := e.s.Propose(context.Background(), ProposeInput{ChatID: 7, Kind: KindReduce, InstID: "ETH", Side: "long", Fraction: 0.5, OrdType: "limit", Px: 99000,
		Rationale: "sell limit below the price"}); err == nil || !strings.Contains(err.Error(), "fill at once") {
		t.Fatalf("marketable limit: %v", err)
	}
	if _, err := e.s.Propose(context.Background(), ProposeInput{ChatID: 7, Kind: KindReduce, InstID: "ETH", Side: "long", Fraction: 0.5, OrdType: "limit", Px: 101000,
		OrderMin: 2000, Rationale: "resting a day and more"}); err == nil || !strings.Contains(err.Error(), "orderExpiryMin") {
		t.Fatalf("order expiry cap: %v", err)
	}
}

// A failed execution and a busy chat: the message waits (queued) until the chat takes it, in order.
func TestSteerQueueAndFailure(t *testing.T) {
	f := &fakeLive{fail: "/api/v5/trade/order"}
	e := newEnv(t, f)
	e.s.SetAccount(&liveAcct{})
	sink := withSteer(e)
	sink.busy = 3
	p, err := e.s.Propose(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := e.s.Approve(context.Background(), p.ID, "owner", "telegram")
	if got.Status != StatusFailed {
		t.Fatalf("failed %+v", got)
	}
	msg := sink.wait(t, e, 1)
	if !strings.Contains(msg, "❌ #1 执行失败 · 开仓 BTC 多单：okx /api/v5/trade/order rejected") || !strings.Contains(msg, "get_positions") {
		t.Fatalf("failure message:\n%s", msg)
	}
	var state string
	var tries int
	e.s.db.QueryRow(`SELECT state, tries FROM trade_steers WHERE proposal_id=1`).Scan(&state, &tries)
	if state != "delivered" || tries < 4 {
		t.Fatalf("steer row %s %d", state, tries)
	}
	// over 2 h in the queue: failed, not delivered
	e.s.db.Exec(`INSERT INTO trade_steers(proposal_id, chat_id, kind, ts, due, state) VALUES(1, 7, 'failed', ?, ?, 'queued')`,
		e.clock().Add(-3*time.Hour).UnixMilli(), e.clock().Add(-3*time.Hour).UnixMilli())
	sink.busy = 1
	e.s.deliverSteers(context.Background())
	e.s.db.QueryRow(`SELECT state FROM trade_steers ORDER BY id DESC LIMIT 1`).Scan(&state)
	if state != "failed" {
		t.Fatalf("old steer %s", state)
	}
}

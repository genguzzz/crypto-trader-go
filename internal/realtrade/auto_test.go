package realtrade

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crypto-trader-go/internal/okxacct"
	"crypto-trader-go/internal/okxorder"
)

// Hands-free execution: a proposal runs at once through the same approval path, marked as auto; the
// owner is not asked; switched off, proposals wait for him again.
func TestHandsFreeExecutes(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	ctx := context.Background()
	if e.s.Auto().Enabled {
		t.Fatal("hands-free execution must be off by default")
	}
	if _, err := e.s.SetAuto(true); err != nil {
		t.Fatal(err)
	}
	p, err := e.s.Propose(ctx, input())
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != StatusExecuted || p.By != ByAuto || p.Via != ByAuto || p.PosID != p.ID || p.CurContracts != 0.1 {
		t.Fatalf("auto-executed proposal %+v", p)
	}
	if got := strings.Join(e.events, ","); got != "approved,executed" {
		t.Fatalf("events %s: the owner must not be asked (no proposed)", got)
	}
	full, _ := e.s.Get(p.ID)
	if len(full.Events) != 3 || full.Events[1].By != ByAuto || !strings.Contains(full.Events[1].Text, "免确认") {
		t.Fatalf("audit %+v", full.Events)
	}
	var steers int
	e.s.db.QueryRow(`SELECT COUNT(*) FROM trade_steers WHERE proposal_id=? AND kind=?`, p.ID, StatusExecuted).Scan(&steers)
	if steers != 1 {
		t.Fatalf("%d steers: the chat must hear about the execution as for an approved proposal", steers)
	}
	if line := eventLine(StatusApproved, p); !strings.Contains(line, "免确认自动执行") {
		t.Fatalf("push line %q", line)
	}
	// the agent is told what happened, not that it waits
	out, err := e.s.ProposeTool(ctx, 7, map[string]string{"kind": "close", "instId": "BTC", "side": "long", "rationale": "逻辑走完，平掉这笔多单"})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["status"] != StatusExecuted || !strings.Contains(m["note"].(string), "hands-free") || strings.Contains(m["note"].(string), "Waiting for the owner") {
		t.Fatalf("tool result %+v", m)
	}
	// off again: the next proposal waits and the owner is asked
	if _, err := e.s.SetAuto(false); err != nil {
		t.Fatal(err)
	}
	e.events = nil
	q, err := e.s.Propose(ctx, input())
	if err != nil || q.Status != StatusPending || strings.Join(e.events, ",") != "proposed" {
		t.Fatalf("after switching off: %+v %v %v", q, err, e.events)
	}
}

// Every cap and check still applies without the owner: a proposal over a cap is refused, an order
// OKX refuses ends as failed, and neither is retried.
func TestHandsFreeKeepsTheCaps(t *testing.T) {
	f := &fakeLive{fail: "/api/v5/trade/order"}
	e := newEnv(t, f)
	e.s.SetAccount(&liveAcct{})
	ctx := context.Background()
	e.s.SetAuto(true)
	in := input()
	in.Leverage = 50
	if _, err := e.s.Propose(ctx, in); err == nil || !strings.Contains(err.Error(), "leverage") {
		t.Fatalf("leverage over the cap: %v", err)
	}
	p, err := e.s.Propose(ctx, input())
	if err != nil || p.Status != StatusFailed || !strings.Contains(p.Error, "Insufficient margin") || p.Via != ByAuto {
		t.Fatalf("an order OKX refuses: %+v %v", p, err)
	}
	out, _ := e.s.ProposeTool(ctx, 7, map[string]string{"instId": "ETH", "side": "long", "marginUsdt": "20", "leverage": "5", "sl": "98000", "tp": "104000",
		"rationale": "breakout above the range with rising OI", "invalidation": "back below 99k"})
	if m := out.(map[string]any); m["status"] != StatusFailed || !strings.Contains(m["error"].(string), "Insufficient margin") {
		t.Fatalf("the agent must see the failure: %+v", m)
	}
	f.fail = ""
	for _, inst := range []string{"SOL", "DOGE"} { // MaxOpen 2
		in := input()
		in.InstID = inst
		if p, err := e.s.Propose(ctx, in); err != nil || p.Status != StatusExecuted {
			t.Fatalf("open %s: %+v %v", inst, p, err)
		}
	}
	in = input()
	in.InstID = "XRP"
	if _, err := e.s.Propose(ctx, in); err == nil || !strings.Contains(err.Error(), "cap is 2") {
		t.Fatalf("a third open: %v", err)
	}
}

// The switch belongs to the mode it was made in: tried in a dry run, it is off after going live.
func TestHandsFreeIsBoundToTheMode(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	e.s.SetAuto(true)
	live, err := New(e.s.st, noSend{t}, e.s.opts)
	if err != nil {
		t.Fatal(err)
	}
	live.SetAccount(&liveAcct{})
	if live.Auto().Enabled {
		t.Fatal("a switch made in a dry run is on in live mode")
	}
	p, err := live.Propose(context.Background(), input())
	if err != nil || p.Status != StatusPending {
		t.Fatalf("live proposal %+v %v", p, err)
	}
	if !e.s.Auto().Enabled {
		t.Fatal("the dry run lost its switch")
	}
}

// A locked position: the agent can propose nothing on that instrument and side — whatever the kind,
// hands-free or not — and a proposal made before the lock cannot run. The other side and other coins
// are untouched; unlocking restores everything.
func TestLockedPosition(t *testing.T) {
	f := &fakeLive{}
	e := newEnv(t, f)
	acct := &liveAcct{pos: []okxacct.Position{
		{InstID: "ETH-USDT-SWAP", Side: "short", PosSide: "short", MgnMode: "isolated", Contracts: 2, AvgPx: 100000, Lever: 5},
		{InstID: "SOL-USDT-SWAP", Side: "long", PosSide: "long", MgnMode: "isolated", Contracts: 2, AvgPx: 100000, Lever: 5},
	}}
	e.s.SetAccount(acct)
	ctx := context.Background()
	closeOf := func(inst, side string) ProposeInput {
		return ProposeInput{ChatID: 7, Kind: KindClose, InstID: inst, Side: side, Rationale: "他让我帮他把这个仓位平掉"}
	}
	for _, bad := range [][2]string{{"", "long"}, {"ETH", "both"}, {"ETH-USD-SWAP", "long"}} {
		if err := e.s.SetLock(bad[0], bad[1], true); err == nil {
			t.Fatalf("lock %v accepted", bad)
		}
	}
	waiting, err := e.s.Propose(ctx, closeOf("ETH", "short")) // made before the lock
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetLock("eth", "Short", true); err != nil {
		t.Fatal(err)
	}
	e.s.SetLock("ETH", "short", true) // twice is once
	if ls := e.s.Locks(); len(ls) != 1 || ls[0].InstID != "ETH-USDT-SWAP" || ls[0].Side != "short" || ls[0].Ts == 0 {
		t.Fatalf("locks %+v", ls)
	}
	got, err := e.s.Approve(ctx, waiting.ID, "owner", "app")
	if err != nil || got.Status != StatusInvalidated || !strings.Contains(got.Error, "锁定") || f.paths() != "" {
		t.Fatalf("a proposal on a position locked meanwhile: %+v %v sent %q", got, err, f.paths())
	}
	e.s.SetAuto(true)
	for _, kind := range []string{KindOpen, KindAdd, KindReduce, KindClose, KindAdjust, KindCancel, KindAmend} {
		in := ProposeInput{ChatID: 7, Kind: kind, InstID: "ETH", Side: "short", MarginUSDT: 20, Leverage: 5, SL: 102000, TP: 96000, Fraction: 0.5, Px: 101000,
			Rationale: "trying to act on the locked position", Invalidation: "back above 101k"}
		if _, err := e.s.Propose(ctx, in); err == nil || !strings.Contains(err.Error(), "locked ETH short") {
			t.Fatalf("%s on a locked position: %v", kind, err)
		}
	}
	if f.paths() != "" {
		t.Fatalf("requests were sent for a locked position: %s", f.paths())
	}
	// another coin is not locked
	if p, err := e.s.Propose(ctx, closeOf("SOL", "long")); err != nil || p.Status != StatusExecuted {
		t.Fatalf("close of an unlocked position: %+v %v", p, err)
	}
	// one coin's trade record
	one, _ := e.s.ListTool(ctx, map[string]string{"instId": "sol"})
	if rows := one.(map[string]any)["proposals"].([]map[string]any); len(rows) != 1 || rows[0]["instId"] != "SOL-USDT-SWAP" {
		t.Fatalf("proposals of one coin: %+v", rows)
	}
	// what the agent reads marks the lock, so it does not plan around that position
	pos, err := e.s.PositionsTool(ctx, "")
	raw, _ := json.Marshal(pos)
	if err != nil || strings.Count(string(raw), `"locked":true`) != 1 || strings.Count(string(raw), `"instId"`) != 2 || !strings.Contains(string(raw), `"equity"`) {
		t.Fatalf("positions tool %s %v", raw, err)
	}
	if b := e.s.PositionBrief(ctx, 7, "ETH-USDT-SWAP", "short", 0); !strings.Contains(b, "🔒") {
		t.Fatalf("position brief of a locked position: %s", b)
	}
	out, _ := e.s.ListTool(ctx, map[string]string{})
	if m := out.(map[string]any); m["handsFree"] != true || len(m["locked"].([]Lock)) != 1 {
		t.Fatalf("list tool %+v", m)
	}
	if err := e.s.SetLock("ETH", "short", false); err != nil || len(e.s.Locks()) != 0 {
		t.Fatalf("unlock: %v %+v", err, e.s.Locks())
	}
	if p, err := e.s.Propose(ctx, closeOf("ETH", "short")); err != nil || p.Status != StatusExecuted {
		t.Fatalf("after unlocking: %+v %v", p, err)
	}
}

// A lock is per side: the other side of the same coin stays the agent's.
func TestLockIsPerSide(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	e.s.SetLock("BTC", "short", true)
	if p, err := e.s.Propose(context.Background(), input()); err != nil || p.Status != StatusPending {
		t.Fatalf("a long while the short side is locked: %+v %v", p, err)
	}
}

func TestOwnerSwitchesHTTP(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	mux := http.NewServeMux()
	e.s.Register(mux)
	changes := 0
	e.s.SetOnChange(func() { changes++ })
	do := func(path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("PUT", path, strings.NewReader(body)))
		return w
	}
	if w := do("/api/v1/realtrade/auto", `{"enabled":true}`); w.Code != 400 || e.s.Auto().Enabled {
		t.Fatalf("switching on without confirm: %d", w.Code)
	}
	if w := do("/api/v1/realtrade/auto", `{"enabled":true,"confirm":true}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"auto":{"enabled":true,"mode":"dryrun"`) {
		t.Fatalf("switch on: %d %s", w.Code, w.Body)
	}
	if w := do("/api/v1/realtrade/auto", `{"enabled":false}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"auto":{"enabled":false}`) {
		t.Fatalf("switch off: %d %s", w.Code, w.Body)
	}
	if w := do("/api/v1/realtrade/locks", `{"instId":"BTC","side":"up","locked":true}`); w.Code != 400 {
		t.Fatalf("bad lock: %d", w.Code)
	}
	if w := do("/api/v1/realtrade/locks", `{"instId":"BTC","side":"long","locked":true}`); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"locks":[{"instId":"BTC-USDT-SWAP","side":"long"`) {
		t.Fatalf("lock: %d %s", w.Code, w.Body)
	}
	if w := do("/api/v1/realtrade/locks", `{"instId":"BTC-USDT-SWAP","side":"long","locked":false}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"locks":[]`) {
		t.Fatalf("unlock: %d %s", w.Code, w.Body)
	}
	if changes != 4 {
		t.Fatalf("%d change events for 4 changes (the app follows them)", changes)
	}
}

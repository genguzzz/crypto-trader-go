package realtrade

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"crypto-trader-go/internal/okxorder"
)

// Amend and cancel of a live resting open: the price and exits go to OKX as amend-order on ctg's client
// ids, the row keeps the new values; a partly filled order refuses a new price; cancel sends cancel-order.
func TestAmendCancelRestingLive(t *testing.T) {
	f := &fakeLive{}
	e := newEnv(t, f)
	e.s.SetAccount(&liveAcct{pending: map[string]map[string]any{}, history: map[string]map[string]any{}})
	in := input()
	in.OrdType, in.Px, in.OrderMin = "limit", 99500, 30
	open := approve(t, e, in)
	n := len(f.sent)

	am := approve(t, e, ProposeInput{ChatID: 7, Kind: KindAmend, InstID: "BTC", Side: "long", Px: 99300, SL: 97800, TP: 103500, OrderMin: 45,
		Rationale: "支撑下移，把挂单价和止损一起下调"})
	if am.TargetID != open.ID || am.Status != StatusExecuted || len(f.sent) != n+1 || !strings.HasSuffix(f.paths(), "trade/amend-order") {
		t.Fatalf("amend %+v %s", am, f.paths())
	}
	b := string(f.sent[n].Body)
	for _, want := range []string{`"clOrdId":"ctgp1"`, `"newPx":"99300.0"`, `"attachAlgoClOrdId":"ctga1"`, `"newSlTriggerPx":"97800.0"`, `"newTpTriggerPx":"103500.0"`} {
		if !strings.Contains(b, want) {
			t.Fatalf("amend body lacks %s: %s", want, b)
		}
	}
	got, _ := e.s.get(open.ID)
	if got.Px != 99300 || got.SL != 97800 || got.TP != 103500 || got.OrderMin != 45 || got.OrdState != OrdLive {
		t.Fatalf("target after amend %+v", got)
	}

	// rest time only: nothing is sent
	n = len(f.sent)
	approve(t, e, ProposeInput{ChatID: 7, Kind: KindAmend, InstID: "BTC", Side: "long", OrderMin: 90, Rationale: "再多挂一会儿等回踩确认"})
	if len(f.sent) != n {
		t.Fatalf("expiry-only amend sent %s", f.paths())
	}

	// a price that would fill at once is refused
	if _, err := e.s.Propose(context.Background(), ProposeInput{ChatID: 7, Kind: KindAmend, InstID: "BTC", Side: "long", Px: 101000, Rationale: "抬高挂单价追一下"}); err == nil {
		t.Fatal("amend above the market must be refused")
	}

	c := approve(t, e, ProposeInput{ChatID: 7, Kind: KindCancel, InstID: "BTC", Side: "long", Rationale: "结构破坏，撤掉这张挂单"})
	if c.TargetID != open.ID || !strings.HasSuffix(f.paths(), "trade/cancel-order") || !strings.Contains(string(f.sent[len(f.sent)-1].Body), `"clOrdId":"ctgp1"`) {
		t.Fatalf("cancel %+v %s", c, f.paths())
	}
}

// In a dry run a cancel settles the target at once and frees the slot; with nothing resting both are refused.
func TestCancelRestingDryRun(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	ctx := context.Background()
	if _, err := e.s.Propose(ctx, ProposeInput{ChatID: 7, Kind: KindCancel, InstID: "BTC", Side: "long", Rationale: "nothing rests here at all"}); err == nil {
		t.Fatal("cancel with nothing resting must be refused")
	}
	in := input()
	in.OrdType, in.Px, in.OrderMin = "limit", 99500, 30
	open := approve(t, e, in)
	c := approve(t, e, ProposeInput{ChatID: 7, Kind: KindCancel, InstID: "BTC", Side: "long", Rationale: "结构破坏，撤掉这张挂单"})
	got, _ := e.s.get(open.ID)
	if c.TargetID != open.ID || got.OrdState != OrdCanceled || got.CloseKind != "unfilled" {
		t.Fatalf("cancel %+v target %+v", c, got)
	}
	if _, err := e.s.Propose(ctx, ProposeInput{ChatID: 7, Kind: KindAmend, InstID: "BTC", Side: "long", Px: 99000, Rationale: "nothing rests any more at all"}); err == nil {
		t.Fatal("amend of a cancelled order must be refused")
	}
}

func TestOpenDefaultsToCross(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	in := input()
	in.MarginMode = ""
	if p := approve(t, e, in); p.MarginMode != "cross" {
		t.Fatalf("default margin mode %q", p.MarginMode)
	}
}

// A rejected or expired proposal wakes its chat too: the agent that waits for the owner must hear "no" or "too late".
func TestRejectExpireSteerChat(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	sink := withSteer(e)
	p, err := e.s.Propose(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Reject(p.ID, "owner", "app"); err != nil {
		t.Fatal(err)
	}
	if msg := sink.wait(t, e, 1); !strings.Contains(msg, "🚫") || !strings.Contains(msg, "拒绝") {
		t.Fatalf("reject message %q", msg)
	}
	in := input()
	in.ExpiresMin = 3
	q, err := e.s.Propose(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	e.advance(4 * time.Minute)
	e.s.expireDue()
	if msg := sink.wait(t, e, 2); !strings.Contains(msg, "⌛") || !strings.Contains(msg, fmt.Sprintf("#%d", q.ID)) {
		t.Fatalf("expire message %q", msg)
	}
}

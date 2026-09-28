package alerts

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/rules"
	"crypto-trader-go/internal/store"
)

type fakeEnv map[string]rules.Rule

func (f fakeEnv) Rule(id string) (rules.Rule, bool) { r, ok := f[id]; return r, ok }

var env = fakeEnv{"resid-reversal": {ID: "resid-reversal", Name: "残差反转"}}

func newSvc(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	s, err := New(st, env, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

const t0 = int64(1_790_000_000_000) // minute aligned

func snap(min int, rows ...market.Row) *market.Snapshot {
	m := t0 + int64(min)*60_000
	return &market.Snapshot{AsOfMinute: m, DecisionTs: m + 60_000, Rows: rows}
}

func ret1h(id string, pct float64) market.Row {
	return market.Row{InstID: id, Active: true, Values: map[string]float64{"ret_1h": math.Log(1 + pct/100)}}
}

func mustSave(t *testing.T, s *Service, r Rule) Rule {
	t.Helper()
	out, err := s.SaveRule(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func priceRule(cool int) Rule {
	return Rule{Name: "1h 异动", Enabled: true, Type: "price_move", Params: json.RawMessage(`{"window":"1h","pct":3,"dir":"both"}`),
		Scope: "active", CooldownMin: cool}
}

func TestPriceMoveEdgeTrigger(t *testing.T) {
	s, _ := newSvc(t)
	mustSave(t, s, priceRule(0))
	steps := []struct {
		pct  float64
		want int
	}{{1, 0}, {3.5, 1}, {4, 0}, {-5, 0}, {1, 0}, {-3.2, 1}}
	for i, st := range steps {
		got := s.evaluate(snap(i, ret1h("A-USDT-SWAP", st.pct)))
		if len(got) != st.want {
			t.Fatalf("step %d (%.1f%%): fired %d, want %d", i, st.pct, len(got), st.want)
		}
		if st.want == 1 && !strings.Contains(got[0].Message, "A-USDT-SWAP 1h") {
			t.Fatalf("message %q", got[0].Message)
		}
	}
	// the same minute twice is ignored
	if got := s.evaluate(snap(5, ret1h("A-USDT-SWAP", 9))); got != nil {
		t.Fatal("re-evaluated an old minute")
	}
}

func TestCooldown(t *testing.T) {
	s, _ := newSvc(t)
	mustSave(t, s, priceRule(10))
	fired := 0
	for i := 0; i < 30; i++ { // toggles every minute: crosses up at 1, 3, 5, …
		pct := 0.0
		if i%2 == 1 {
			pct = 4
		}
		fired += len(s.evaluate(snap(i, ret1h("A-USDT-SWAP", pct))))
	}
	// crossings at minutes 1..29 (15); cooldown 10 min allows 1, 11, 21 → 3
	if fired != 3 {
		t.Fatalf("fired %d, want 3", fired)
	}
}

func TestUnknownKeepsStateAndScope(t *testing.T) {
	s, _ := newSvc(t)
	mustSave(t, s, priceRule(0))
	if n := len(s.evaluate(snap(0, ret1h("A-USDT-SWAP", 5)))); n != 1 {
		t.Fatal("first crossing should fire")
	}
	missing := market.Row{InstID: "A-USDT-SWAP", Active: true} // no values: unknown
	s.evaluate(snap(1, missing))
	if n := len(s.evaluate(snap(2, ret1h("A-USDT-SWAP", 5)))); n != 0 {
		t.Fatal("unknown value must not reset the edge state")
	}
	inactive := ret1h("A-USDT-SWAP", 5)
	inactive.Active = false
	s.evaluate(snap(3, inactive)) // leaves the pool
	if n := len(s.evaluate(snap(4, ret1h("A-USDT-SWAP", 5)))); n != 1 {
		t.Fatal("re-entering the scope counts as a new crossing")
	}
	// watch / single-instrument scopes
	w := mustSave(t, s, Rule{Name: "w", Enabled: true, Type: "price_move", Params: json.RawMessage(`{"window":"1h","pct":1}`), Scope: "watch"})
	one := mustSave(t, s, Rule{Name: "one", Enabled: true, Type: "price_move", Params: json.RawMessage(`{"window":"1h","pct":1}`), Scope: "inst", InstID: "b-usdt-swap"})
	b := ret1h("B-USDT-SWAP", 2)
	b.Active, b.Watch = false, true
	got := s.evaluate(snap(5, b))
	if len(got) != 2 || got[0].RuleID != w.ID || got[1].RuleID != one.ID {
		t.Fatalf("scopes: %+v", got)
	}
}

func TestScoreCrossUp(t *testing.T) {
	s, _ := newSvc(t)
	mustSave(t, s, Rule{Name: "高分", Enabled: true, Type: "score", Params: json.RawMessage(`{"ruleId":"resid-reversal","side":"short","min":80}`), Scope: "active"})
	row := func(v float64) market.Row {
		return market.Row{InstID: "A-USDT-SWAP", Active: true, Scores: map[string]rules.Score{"resid-reversal": {Short: rules.SideScore{Score: &v}}}}
	}
	want := []int{0, 1, 0, 0, 1}
	for i, v := range []float64{70, 85, 90, 60, 81} {
		got := s.evaluate(snap(i, row(v)))
		if len(got) != want[i] {
			t.Fatalf("step %d: fired %d, want %d", i, len(got), want[i])
		}
		if want[i] == 1 && !strings.Contains(got[0].Message, "残差反转") {
			t.Fatalf("message %q", got[0].Message)
		}
	}
}

func TestLoadedRulesArePrimedFirst(t *testing.T) {
	s, st := newSvc(t)
	mustSave(t, s, Rule{Name: "费率", Enabled: true, Type: "funding", Params: json.RawMessage(`{"pctPerHour":0.01}`), Scope: "active", CooldownMin: 60})
	fund := market.Row{InstID: "A-USDT-SWAP", Active: true, Values: map[string]float64{"funding_hourly": -0.0002}}
	s2, err := New(st, env, http.DefaultClient) // restart
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s2.evaluate(snap(0, fund))); n != 0 {
		t.Fatal("a restart must not re-fire conditions that already held")
	}
	fund.Values["funding_hourly"] = 0
	s2.evaluate(snap(1, fund))
	fund.Values["funding_hourly"] = 0.00015
	if n := len(s2.evaluate(snap(2, fund))); n != 1 {
		t.Fatal("a new crossing after the restart fires")
	}
}

func TestValidation(t *testing.T) {
	s, _ := newSvc(t)
	bad := []Rule{
		{Name: "", Type: "price_move", Scope: "active"},
		{Name: "x", Type: "nope", Scope: "active"},
		{Name: "x", Type: "price_move", Params: json.RawMessage(`{"window":"2h","pct":3}`), Scope: "active"},
		{Name: "x", Type: "price_move", Params: json.RawMessage(`{"window":"1h","pct":0}`), Scope: "active"},
		{Name: "x", Type: "price_move", Params: json.RawMessage(`{"window":"1h","pct":3,"extra":1}`), Scope: "active"},
		{Name: "x", Type: "price_move", Params: json.RawMessage(`{"window":"1h","pct":3}`), Scope: "everything"},
		{Name: "x", Type: "price_move", Params: json.RawMessage(`{"window":"1h","pct":3}`), Scope: "inst", InstID: "BTC-USDT"},
		{Name: "x", Type: "price_move", Params: json.RawMessage(`{"window":"1h","pct":3}`), Scope: "active", CooldownMin: -1},
		{Name: "x", Type: "score", Params: json.RawMessage(`{"ruleId":"missing","side":"long","min":80}`), Scope: "active"},
		{Name: "x", Type: "score", Params: json.RawMessage(`{"ruleId":"resid-reversal","side":"up","min":80}`), Scope: "active"},
		{Name: "x", Type: "funding", Params: json.RawMessage(`{"pctPerHour":5}`), Scope: "active"},
	}
	for i, r := range bad {
		if _, err := s.SaveRule(r); err == nil {
			t.Errorf("case %d: expected a validation error", i)
		}
	}
	if _, err := s.SaveRule(Rule{ID: 99, Name: "x", Type: "funding", Params: json.RawMessage(`{"pctPerHour":0.01}`), Scope: "active"}); err == nil {
		t.Error("updating a missing rule must fail")
	}
	// defaults are filled in and stored normalised
	r := mustSave(t, s, Rule{Name: "  默认 ", Type: "price_move", Params: json.RawMessage(`{"pct":2}`), Scope: "inst", InstID: " eth-usdt-swap "})
	if r.Name != "默认" || r.InstID != "ETH-USDT-SWAP" || string(r.Params) != `{"window":"1h","pct":2,"dir":"both"}` {
		t.Fatalf("normalised %+v %s", r, r.Params)
	}
}

func TestMasking(t *testing.T) {
	const token = "123456789:AAH-secret-token-value-xyz9"
	if m := Mask(token); m != "••••xyz9" || strings.Contains(m, "secret") {
		t.Fatalf("mask %q", m)
	}
	if Mask("short") != "••••" || Mask("") != "" {
		t.Fatal("short values are fully masked")
	}
	if m := MaskURL("https://hooks.example.com/services/T000/B000/abcdefgh"); m != "https://hooks.example.com/••••efgh" {
		t.Fatalf("mask url %q", m)
	}
	c := ChannelConfig{Telegram: TelegramConfig{BotToken: token, ChatID: "-100123"}, Webhook: WebhookConfig{URL: "http://127.0.0.1:9/hook/abcdef", Secret: "s3cr3t-value!"}}
	v := c.View()
	b, _ := json.Marshal(v)
	for _, secret := range []string{token, "secret-token", "s3cr3t", "hook/abcdef"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("view leaks %q: %s", secret, b)
		}
	}
	// saving the masked view back changes nothing
	same, err := c.Merge(v)
	if err != nil || same != c {
		t.Fatalf("masked round trip: %v %+v", err, same)
	}
	// empty secrets = unchanged; new values replace
	v.Telegram.BotToken, v.Webhook.Secret, v.Webhook.URL = "", "new-secret", "https://example.com/x"
	next, err := c.Merge(v)
	if err != nil || next.Telegram.BotToken != token || next.Webhook.Secret != "new-secret" || next.Webhook.URL != "https://example.com/x" {
		t.Fatalf("merge: %v %+v", err, next)
	}
	for _, in := range []ChannelView{
		func() ChannelView { x := v; x.Telegram.BotToken = "not-a-token"; return x }(),
		func() ChannelView { x := v; x.Telegram.ChatID = "abc"; return x }(),
		func() ChannelView { x := v; x.Webhook.URL = "ftp://example.com"; return x }(),
	} {
		if _, err := c.Merge(in); err == nil {
			t.Errorf("expected validation error for %+v", in)
		}
	}
}

func TestDeliveryAndScrub(t *testing.T) {
	var mu sync.Mutex
	var hooks []Payload
	var secrets []string
	tgCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasPrefix(r.URL.Path, "/bot"):
			tgCalls++
			if tgCalls == 1 { // first attempt fails: retried
				w.WriteHeader(502)
				return
			}
			var m map[string]any
			json.Unmarshal(b, &m)
			if m["chat_id"] != "-100123" || !strings.Contains(m["text"].(string), "A-USDT-SWAP") {
				w.WriteHeader(400)
			}
		case r.URL.Path == "/hook":
			var p Payload
			json.Unmarshal(b, &p)
			hooks = append(hooks, p)
			secrets = append(secrets, r.Header.Get("X-CTG-Secret"))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"ok":false,"description":"Not Found"}`))
		}
	}))
	defer srv.Close()

	s, st := newSvc(t)
	s.send.telegramBase, s.send.backoff = srv.URL, time.Millisecond
	const token = "123456789:AAH-secret-token-value-xyz9"
	if _, err := s.SaveChannels(ChannelView{}); err != nil {
		t.Fatal(err)
	}
	var in ChannelView
	in.Telegram.BotToken, in.Telegram.ChatID = token, "-100123"
	in.Webhook.URL, in.Webhook.Secret = srv.URL+"/hook", "hook-secret"
	if _, err := s.SaveChannels(in); err != nil {
		t.Fatal(err)
	}
	mustSave(t, s, Rule{Name: "r", Enabled: true, Type: "price_move", Params: json.RawMessage(`{"window":"1h","pct":3}`), Scope: "active",
		Channels: Channels{Telegram: true, Webhook: true}})
	s.OnSnapshot(snap(0, ret1h("A-USDT-SWAP", 5)))
	s.deliver(context.Background(), <-s.queue)
	evs, _ := st.AlertEvents(10)
	if len(evs) != 1 || evs[0].Delivery["telegram"] != "ok" || evs[0].Delivery["webhook"] != "ok" {
		t.Fatalf("history %+v", evs)
	}
	if len(hooks) != 1 || hooks[0].InstID != "A-USDT-SWAP" || hooks[0].Type != "price_move" || secrets[0] != "hook-secret" || tgCalls != 2 {
		t.Fatalf("webhook %+v secrets %v telegram calls %d", hooks, secrets, tgCalls)
	}
	if r := s.Rules(); r[0].LastFired != evs[0].Ts {
		t.Fatalf("last fired %d", r[0].LastFired)
	}

	// a failing Telegram call never surfaces the token
	s.send.telegramBase = srv.URL + "/nowhere"
	err := s.Test(context.Background(), "telegram")
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error %v", err)
	}
	s.send.telegramBase = "http://127.0.0.1:1" // connection refused: *url.Error carries the URL
	if err := s.Test(context.Background(), "telegram"); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("network error leaks: %v", err)
	}
	if err := s.Test(context.Background(), "webhook"); err != nil {
		t.Fatalf("webhook test: %v", err)
	}
	if v, _ := s.ClearChannel("telegram"); v.Telegram.Configured {
		t.Fatal("cleared")
	}
	if err := s.Test(context.Background(), "telegram"); err == nil || err.Error() != "未配置" {
		t.Fatalf("unconfigured: %v", err)
	}
}

func TestSplitText(t *testing.T) {
	long := strings.Repeat("一二三四五六七八九十\n", 1000)
	parts := splitText(long, telegramMaxLen)
	total := 0
	for _, p := range parts {
		if n := len([]rune(p)); n > telegramMaxLen {
			t.Fatalf("part of %d runes", n)
		}
		total += strings.Count(p, "一")
	}
	if total != 1000 || len(parts) < 3 {
		t.Fatalf("parts %d total %d", len(parts), total)
	}
}

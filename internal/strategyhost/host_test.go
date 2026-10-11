package strategyhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crypto-trader-go/internal/trade"
)

// ---------- fakes for the Go side ----------

type fakeMD struct{}

func (fakeMD) Now() int64 { return 1_700_000_000_000 }
func (fakeMD) Spec(instID string) (trade.Spec, bool) {
	return trade.Spec{InstID: instID, CtVal: 0.01, TickSz: 0.1, LotSz: 0.01, MinSz: 0.01, MaxLever: 100}, true
}
func (fakeMD) Quote(context.Context, string) (trade.Quote, error) { return trade.Quote{}, nil }
func (fakeMD) Bars(instID, tf string, n int) ([]trade.Bar, error) {
	out := make([]trade.Bar, n)
	for i := range out {
		out[i] = trade.Bar{Ts: int64(i) * 3_600_000, O: 100, H: 101, L: 99, C: 100 + float64(i), Vol: 1, VolQ: 100}
	}
	return out, nil
}
func (fakeMD) Factors(string) (map[string]float64, bool) {
	return map[string]float64{"ret_1h": 0.01, "beta": math.NaN()}, true
}
func (fakeMD) Screener(q trade.ScreenerQuery) ([]trade.ScreenRow, error) {
	return []trade.ScreenRow{{InstID: "SOL-USDT-SWAP", Rank: 1, Long: 90, Short: math.NaN()}}, nil
}
func (fakeMD) Score(rule, instID string) (float64, float64, bool) { return 70, 30, rule == "r" }
func (fakeMD) FundingRate(string, int64) (float64, bool)          { return 0, false }
func (fakeMD) Keep(string, bool)                                  {}

type fakeSink struct {
	mu   sync.Mutex
	sigs []trade.Signal
}

func (f *fakeSink) Submit(_ context.Context, s trade.Signal) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s.InstID == "BAD-USDT-SWAP" {
		return 0, errors.New("no spec")
	}
	f.sigs = append(f.sigs, s)
	return int64(len(f.sigs)), nil
}
func (f *fakeSink) Position(instanceID int64, instID string) (trade.Position, bool) {
	if instID == "BTC-USDT-SWAP" {
		return trade.Position{InstanceID: instanceID, InstID: instID, PosSide: "long", Qty: 2}, true
	}
	return trade.Position{}, false
}
func (f *fakeSink) Positions(int64) []trade.Position { return nil }
func (f *fakeSink) Series(int64, string, string, string, int) ([]trade.SeriesPoint, error) {
	return []trade.SeriesPoint{{Ts: 1, V: 2}}, nil
}
func (f *fakeSink) Notify(int64, string, string, map[string]any) {}
func (f *fakeSink) Log(int64, string, string)                    {}
func (f *fakeSink) AccountView(int64) (trade.Account, bool) {
	return trade.Account{ID: 1, Equity: 1000}, true
}

// ---------- fake Python side over the real socket ----------

type client struct {
	w   *wire
	qid int
}

func dial(t *testing.T, path string) *client {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &client{w: newWire(c)}
}

func (c *client) send(v any) error { return c.w.send(v) }

func (c *client) recv() (map[string]any, error) {
	line, err := c.w.recv()
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(line, &m)
}

// call sends a query or signal and returns the matching reply.
func (c *client) call(m map[string]any) (map[string]any, error) {
	c.qid++
	m["qid"] = c.qid
	if err := c.send(m); err != nil {
		return nil, err
	}
	r, err := c.recv()
	if err != nil {
		return nil, err
	}
	if r["qid"] != float64(c.qid) {
		return nil, errors.New("qid mismatch")
	}
	return r, nil
}

// instanceFunc handles one event; returning false skips the done (to provoke a timeout).
type instanceFunc func(c *client, ev map[string]any) bool

type fakeSup struct {
	c      *client
	mu     sync.Mutex
	spawns []map[string]any
	kills  chan map[string]any
	handle instanceFunc
	// onSpawn overrides the default behavior (connect and serve with handle).
	onSpawn func(sp *fakeSup, cmd map[string]any)
	path    string
	t       *testing.T
}

func startSup(t *testing.T, path string, handle instanceFunc) *fakeSup {
	sp := &fakeSup{c: dial(t, path), kills: make(chan map[string]any, 8), handle: handle, path: path, t: t}
	if err := sp.c.send(map[string]any{"type": "supervise"}); err != nil {
		t.Fatal(err)
	}
	go sp.loop()
	return sp
}

func (sp *fakeSup) loop() {
	for {
		m, err := sp.c.recv()
		if err != nil {
			return
		}
		switch m["type"] {
		case "spawn":
			sp.mu.Lock()
			sp.spawns = append(sp.spawns, m)
			sp.mu.Unlock()
			if sp.onSpawn != nil {
				go sp.onSpawn(sp, m)
			} else {
				go sp.serve(m)
			}
		case "kill":
			sp.kills <- m
		case "describe":
			_ = sp.c.send(map[string]any{"type": "describe_result", "id": m["id"], "strategies": []map[string]any{
				{"file": "library/a.py", "class": "A", "name": "a", "timeframe": "4H", "code": "x = 1",
					"params":   []map[string]any{{"name": "fast", "default": 20, "aiTunable": true}},
					"universe": map[string]any{"static": []string{"BTC-USDT-SWAP"}}},
				{"file": "library/a.py", "class": "B", "name": "b", "timeframe": "1H", "code": "x = 1",
					"universe": map[string]any{"screener": map[string]any{"rule": "r", "top": 5}}},
				{"file": "library/broken.py", "error": "SyntaxError"},
			}})
		}
	}
}

func (sp *fakeSup) serve(cmd map[string]any) {
	c := dial(sp.t, sp.path)
	_ = c.send(map[string]any{"type": "hello", "instance": cmd["instance"], "token": cmd["token"], "sdk": "1"})
	for {
		ev, err := c.recv()
		if err != nil {
			return
		}
		if !sp.handle(c, ev) {
			continue
		}
		if c.send(map[string]any{"type": "done", "seq": ev["seq"]}) != nil {
			return
		}
		if ev["type"] == "stop" {
			c.w.close()
			_ = sp.c.send(map[string]any{"type": "exited", "instance": cmd["instance"], "token": cmd["token"], "code": 0})
			return
		}
	}
}

func ack(*client, map[string]any) bool { return true }

func newHost(t *testing.T, opts Options) (*Host, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "sh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	opts.Socket = filepath.Join(dir, "strategy.sock")
	if opts.Logger == nil {
		opts.Logger = log.New(io.Discard, "", 0)
	}
	h := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- h.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-errc; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	waitFor(t, func() bool { _, err := os.Stat(opts.Socket); return err == nil })
	return h, opts.Socket
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var inst = trade.Instance{ID: 7, AccountID: 3, Name: "t", ConfigVersion: 2, Params: map[string]float64{"fast": 10}}
var ver = trade.StrategyVersion{ID: "abc", Name: "majors-trend", File: "library/majors_trend.py", Timeframe: "4H", Code: "code"}

// ---------- tests ----------

func TestSessionRoundTrip(t *testing.T) {
	h, path := newHost(t, Options{})
	if h.Available() {
		t.Fatal("available before supervisor")
	}
	if _, err := h.Start(context.Background(), inst, ver, trade.ModeLive, nil, fakeMD{}, &fakeSink{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("start without supervisor: %v", err)
	}

	var mu sync.Mutex
	var got []string
	fail := func(format string, a ...any) { t.Errorf(format, a...) }
	sp := startSup(t, path, func(c *client, ev map[string]any) bool {
		mu.Lock()
		got = append(got, ev["type"].(string))
		mu.Unlock()
		switch ev["type"] {
		case "init":
			if ev["seq"] != 0.0 || ev["mode"] != "live" {
				fail("init: %v", ev)
			}
			if st := ev["state"].(map[string]any); st["n"] != 1.0 {
				fail("init state: %v", ev["state"])
			}
			if s := ev["strategy"].(map[string]any); s["code"] != nil {
				fail("init leaks code")
			}
			_ = c.send(map[string]any{"type": "state", "data": map[string]any{"n": 2}})
		case "bars":
			r, err := c.call(map[string]any{"type": "query", "method": "bars", "args": map[string]any{"instId": "BTC-USDT-SWAP", "n": 3}})
			if err != nil || len(r["data"].([]any)) != 3 {
				fail("bars: %v %v", r, err)
			}
			r, _ = c.call(map[string]any{"type": "query", "method": "factors", "args": map[string]any{"instId": "BTC-USDT-SWAP"}})
			if f := r["data"].(map[string]any); f["beta"] != nil || f["ret_1h"] != 0.01 {
				fail("factors: %v", r)
			}
			r, _ = c.call(map[string]any{"type": "query", "method": "position", "args": map[string]any{"instId": "BTC-USDT-SWAP"}})
			if p := r["data"].(map[string]any); p["posSide"] != "long" || p["instanceId"] != 7.0 {
				fail("position: %v", r)
			}
			r, _ = c.call(map[string]any{"type": "query", "method": "position", "args": map[string]any{"instId": "ETH-USDT-SWAP"}})
			if r["type"] != "result" || r["data"] != nil {
				fail("no position: %v", r)
			}
			r, _ = c.call(map[string]any{"type": "query", "method": "score", "args": map[string]any{"rule": "r", "instId": "X"}})
			if d := r["data"].(map[string]any); d["long"] != 70.0 {
				fail("score: %v", r)
			}
			r, _ = c.call(map[string]any{"type": "query", "method": "screener", "args": map[string]any{"rule": "r", "top": 5}})
			if rows := r["data"].([]any); len(rows) != 1 || rows[0].(map[string]any)["short"] != nil {
				fail("screener: %v", r)
			}
			r, _ = c.call(map[string]any{"type": "query", "method": "nope"})
			if r["type"] != "error" {
				fail("unknown method: %v", r)
			}
			r, _ = c.call(map[string]any{"type": "signal", "instId": "BTC-USDT-SWAP", "action": "open_long",
				"stopLoss": 95.5, "reason": "cross"})
			if r["type"] != "signal_ack" || r["id"] != 1.0 {
				fail("signal: %v", r)
			}
			r, _ = c.call(map[string]any{"type": "signal", "instId": "BAD-USDT-SWAP", "action": "close"})
			if r["type"] != "error" || !strings.Contains(r["error"].(string), "no spec") {
				fail("bad signal: %v", r)
			}
			r, _ = c.call(map[string]any{"type": "signal", "instId": "BTC-USDT-SWAP", "action": "buy"})
			if r["type"] != "error" {
				fail("bad action: %v", r)
			}
			r, _ = c.call(map[string]any{"type": "query", "method": "series", "args": map[string]any{"instId": "BTC-USDT-SWAP", "field": "oi"}})
			if rows := r["data"].([]any); len(rows) != 1 || rows[0].(map[string]any)["v"] != 2.0 {
				fail("series: %v", r)
			}
			_ = c.send(map[string]any{"type": "log", "level": "info", "msg": "hi"})
			_ = c.send(map[string]any{"type": "status", "msg": "waiting for a cross"})
		}
		return true
	})
	waitFor(t, h.Available)

	sink := &fakeSink{}
	s, err := h.Start(context.Background(), inst, ver, trade.ModeLive, []byte(`{"n":1}`), fakeMD{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if string(s.State()) != `{"n":2}` {
		t.Fatalf("state after init: %s", s.State())
	}
	sp.mu.Lock()
	spawn := sp.spawns[0]
	sp.mu.Unlock()
	if spawn["code"] != "code" || spawn["file"] != ver.File || spawn["instance"] != 7.0 || len(spawn["token"].(string)) < 32 {
		t.Fatalf("spawn: %v", spawn)
	}
	if _, err := h.Start(context.Background(), inst, ver, trade.ModeLive, nil, fakeMD{}, sink); err == nil {
		t.Fatal("second start of the same instance accepted")
	}

	if err := s.Bars(context.Background(), "4H", []trade.InstBar{{InstID: "BTC-USDT-SWAP", Bar: trade.Bar{Ts: 1, C: 123.5}}}); err != nil {
		t.Fatal(err)
	}
	if len(sink.sigs) != 1 {
		t.Fatalf("signals: %v", sink.sigs)
	}
	if st := s.Status(); st != "waiting for a cross" {
		t.Fatalf("status: %q", st)
	}
	sig := sink.sigs[0]
	if sig.InstanceID != 7 || sig.AccountID != 3 || sig.Price != 123.5 || sig.Strength != 1 || sig.StopLoss != 95.5 ||
		sig.ConfigVersion != 2 || sig.Ts != (fakeMD{}).Now() || sig.Action != trade.OpenLong {
		t.Fatalf("signal: %+v", sig)
	}
	ctx := context.Background()
	for _, err := range []error{
		s.Universe(ctx, []string{"A"}, nil, []string{"A"}),
		s.Fill(ctx, trade.Fill{ID: 1}),
		s.SignalResult(ctx, 1, trade.OutOrdered, ""),
		s.Funding(ctx, "A", 0.0001, -0.5),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("not done after stop")
	}
	if s.Err() != nil {
		t.Fatalf("err after stop: %v", s.Err())
	}
	if err := s.Bars(ctx, "4H", []trade.InstBar{{InstID: "A", Bar: trade.Bar{}}}); !errors.Is(err, trade.ErrFaulted) {
		t.Fatalf("bar after stop: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := "init bars universe fill signal_result funding stop"
	if strings.Join(got, " ") != want {
		t.Fatalf("events %v", got)
	}
}

func TestDescribe(t *testing.T) {
	h, path := newHost(t, Options{})
	sp := startSup(t, path, ack)
	waitFor(t, h.Available)
	vs, err := h.Describe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0].ID == vs[1].ID || len(vs[0].ID) != 12 {
		t.Fatalf("versions: %+v", vs)
	}
	if vs[0].Params[0].Name != "fast" || !vs[0].Params[0].AITunable || vs[0].DefaultUniverse.Static[0] != "BTC-USDT-SWAP" {
		t.Fatalf("a: %+v", vs[0])
	}
	if vs[1].DefaultUniverse.Screener == nil || vs[1].DefaultUniverse.Screener.Top != 5 || vs[1].Params == nil {
		t.Fatalf("b: %+v", vs[1])
	}
	s, err := h.Start(context.Background(), inst, vs[1], trade.ModeBacktest, nil, fakeMD{}, &fakeSink{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background())
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.spawns[0]["class"] != "B" {
		t.Fatalf("spawn class: %v", sp.spawns[0])
	}
}

func TestEventTimeoutKills(t *testing.T) {
	h, path := newHost(t, Options{EventTimeout: 150 * time.Millisecond})
	sp := startSup(t, path, func(c *client, ev map[string]any) bool { return ev["type"] != "bars" })
	waitFor(t, h.Available)
	s, err := h.Start(context.Background(), inst, ver, trade.ModeLive, nil, fakeMD{}, &fakeSink{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = s.Bars(context.Background(), "4H", []trade.InstBar{{InstID: "BTC-USDT-SWAP", Bar: trade.Bar{}}})
	if !errors.Is(err, trade.ErrFaulted) || time.Since(start) > 2*time.Second {
		t.Fatalf("bar: %v after %s", err, time.Since(start))
	}
	select {
	case k := <-sp.kills:
		if k["instance"] != 7.0 {
			t.Fatalf("kill: %v", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no kill sent")
	}
	<-s.Done()
	if !errors.Is(s.Err(), trade.ErrFaulted) {
		t.Fatalf("err: %v", s.Err())
	}
	// The instance slot is free again for a restart.
	if _, err := h.Start(context.Background(), inst, ver, trade.ModeLive, nil, fakeMD{}, &fakeSink{}); err != nil {
		t.Fatalf("restart: %v", err)
	}
}

func TestUniverseBudgetGrowsWithAdded(t *testing.T) {
	h, path := newHost(t, Options{EventTimeout: 100 * time.Millisecond})
	startSup(t, path, func(c *client, ev map[string]any) bool {
		if ev["type"] == "universe" {
			time.Sleep(200 * time.Millisecond) // within 100 ms + 30 × 10 ms
		}
		return true
	})
	waitFor(t, h.Available)
	s, err := h.Start(context.Background(), inst, ver, trade.ModeLive, nil, fakeMD{}, &fakeSink{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background())
	added := make([]string, 30)
	for i := range added {
		added[i] = fmt.Sprintf("X%d-USDT-SWAP", i)
	}
	if err := s.Universe(context.Background(), added, nil, added); err != nil {
		t.Fatalf("universe: %v", err)
	}
}

func TestProcessExitReportsStderr(t *testing.T) {
	h, path := newHost(t, Options{})
	var sp *fakeSup
	sp = startSup(t, path, func(c *client, ev map[string]any) bool {
		if ev["type"] == "bars" {
			c.w.close()
			_ = sp.c.send(map[string]any{"type": "exited", "instance": 7, "code": 1, "stderr": "Traceback: boom"})
			return false
		}
		return true
	})
	waitFor(t, h.Available)
	s, err := h.Start(context.Background(), inst, ver, trade.ModeLive, nil, fakeMD{}, &fakeSink{})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Bars(context.Background(), "4H", []trade.InstBar{{InstID: "BTC-USDT-SWAP", Bar: trade.Bar{}}})
	if !errors.Is(err, trade.ErrFaulted) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("bar: %v", err)
	}
	<-s.Done()
	if !strings.Contains(s.Err().Error(), "code 1") {
		t.Fatalf("err: %v", s.Err())
	}
}

func TestStartFailsWhenProcessDiesEarly(t *testing.T) {
	h, path := newHost(t, Options{})
	sp := startSup(t, path, ack)
	sp.onSpawn = func(sp *fakeSup, cmd map[string]any) {
		_ = sp.c.send(map[string]any{"type": "exited", "instance": cmd["instance"], "token": cmd["token"], "code": 1,
			"stderr": "ImportError: no module"})
	}
	waitFor(t, h.Available)
	_, err := h.Start(context.Background(), inst, ver, trade.ModeLive, nil, fakeMD{}, &fakeSink{})
	if !errors.Is(err, trade.ErrFaulted) || !strings.Contains(err.Error(), "ImportError") {
		t.Fatalf("start: %v", err)
	}
}

func TestAuthRejection(t *testing.T) {
	h, path := newHost(t, Options{StartTimeout: 300 * time.Millisecond})
	sp := startSup(t, path, ack)
	tokens := make(chan string, 1)
	sp.onSpawn = func(sp *fakeSup, cmd map[string]any) { tokens <- cmd["token"].(string) }
	waitFor(t, h.Available)

	errc := make(chan error, 1)
	go func() {
		_, err := h.Start(context.Background(), inst, ver, trade.ModeLive, nil, fakeMD{}, &fakeSink{})
		errc <- err
	}()
	token := <-tokens
	for _, hello := range []map[string]any{
		{"type": "hello", "instance": 7, "token": token + "x"},
		{"type": "hello", "instance": 8, "token": token},
		{"type": "bar"},
	} {
		c := dial(t, path)
		_ = c.send(hello)
		r, err := c.recv()
		if err != nil || r["type"] != "error" {
			t.Fatalf("%v: %v %v", hello, r, err)
		}
		if _, err := c.recv(); err == nil {
			t.Fatalf("%v: connection left open", hello)
		}
	}
	if err := <-errc; !errors.Is(err, trade.ErrFaulted) {
		t.Fatalf("start: %v", err)
	}
}

func TestHandlerErrorsAreCounted(t *testing.T) {
	h, path := newHost(t, Options{})
	var fails atomic.Bool
	fails.Store(true)
	startSup(t, path, func(c *client, ev map[string]any) bool {
		if ev["type"] == "bars" && fails.Load() {
			_ = c.send(map[string]any{"type": "done", "seq": ev["seq"], "error": "ZeroDivisionError"})
			return false
		}
		return true
	})
	waitFor(t, h.Available)
	s, err := h.Start(context.Background(), inst, ver, trade.ModeLive, nil, fakeMD{}, &fakeSink{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 1; i <= 2; i++ {
		var he *HandlerError
		if err := s.Bars(ctx, "4H", []trade.InstBar{{InstID: "A", Bar: trade.Bar{}}}); !errors.As(err, &he) || he.Consecutive != i || errors.Is(err, trade.ErrFaulted) {
			t.Fatalf("bar %d: %v", i, err)
		}
	}
	fails.Store(false)
	if err := s.Bars(ctx, "4H", []trade.InstBar{{InstID: "A", Bar: trade.Bar{}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestOversizedLineFaults(t *testing.T) {
	h, path := newHost(t, Options{ExitGrace: 50 * time.Millisecond})
	startSup(t, path, func(c *client, ev map[string]any) bool {
		if ev["type"] == "bars" {
			_ = c.send(map[string]any{"type": "log", "msg": string(bytes.Repeat([]byte("x"), maxLine+10))})
			return false
		}
		return true
	})
	waitFor(t, h.Available)
	s, err := h.Start(context.Background(), inst, ver, trade.ModeLive, nil, fakeMD{}, &fakeSink{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Bars(context.Background(), "4H", []trade.InstBar{{InstID: "A", Bar: trade.Bar{}}}); !errors.Is(err, trade.ErrFaulted) {
		t.Fatalf("bar: %v", err)
	}
	<-s.Done()
}

func TestSocketPermissions(t *testing.T) {
	_, path := newHost(t, Options{})
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o666 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

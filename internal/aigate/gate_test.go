package aigate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crypto-trader-go/internal/store"
	"crypto-trader-go/internal/trade"
)

const testKey = "sk-ant-api03-SECRETSECRETSECRET-abcd1234WXYZ"

type logSink struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *logSink) logf(f string, a ...any) {
	l.mu.Lock()
	fmt.Fprintf(&l.buf, f+"\n", a...)
	l.mu.Unlock()
}

func (l *logSink) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type env struct {
	g    *Gate
	st   *store.Store
	sock string
	logs *logSink
}

func newEnv(t *testing.T, opts Options) *env {
	t.Helper()
	dir, err := os.MkdirTemp("", "ag") // short: unix socket paths are limited to ~108 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	logs := &logSink{}
	opts.SocketPath = filepath.Join(dir, "ai.sock")
	opts.Logf = logs.logf
	if opts.Heartbeat == 0 {
		opts.Heartbeat = time.Hour
	}
	g, err := New(st, opts)
	if err != nil {
		t.Fatal(err)
	}
	return &env{g: g, st: st, sock: opts.SocketPath, logs: logs}
}

func (e *env) run(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.g.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

type fakeWorker struct {
	t *testing.T
	c net.Conn
	r *bufio.Reader
}

func (e *env) dial(t *testing.T, ready bool) *fakeWorker {
	t.Helper()
	var c net.Conn
	var err error
	for i := 0; i < 200; i++ {
		if c, err = net.Dial("unix", e.sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	w := &fakeWorker{t: t, c: c, r: bufio.NewReader(c)}
	w.send(map[string]any{"type": "hello", "worker": "1", "sdk": "0.2.163", "ready": ready, "proxyOk": ready})
	if m := w.recv(); m["type"] != "config" {
		t.Fatalf("first message %v, want config", m)
	}
	return w
}

func (w *fakeWorker) send(v any) {
	b, _ := json.Marshal(v)
	if _, err := w.c.Write(append(b, '\n')); err != nil {
		w.t.Fatalf("worker send: %v", err)
	}
}

func (w *fakeWorker) recv() map[string]any {
	w.t.Helper()
	m, err := w.tryRecv(5 * time.Second)
	if err != nil {
		w.t.Fatalf("worker recv: %v", err)
	}
	return m
}

func (w *fakeWorker) tryRecv(d time.Duration) (map[string]any, error) {
	w.c.SetReadDeadline(time.Now().Add(d))
	line, err := w.r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(line, &m)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func f64(v float64) *float64 { return &v }

func sampleRequest() trade.ReviewRequest {
	return trade.ReviewRequest{
		Signal: trade.Signal{ID: 7, InstanceID: 3, AccountID: 2, InstID: "BTC-USDT-SWAP", Action: trade.OpenLong,
			Strength: 1, StopLoss: 95, TakeProfit: 110, Price: 100, Reason: "ema cross"},
		Account:  trade.Account{ID: 2, Name: "paper", Equity: 10000, Limits: trade.DefaultLimits()},
		Instance: trade.Instance{ID: 3, AccountID: 2, Params: map[string]float64{"fast": 20, "slow": 50}},
		Strategy: trade.StrategyVersion{ID: "abc", Name: "majors-trend", Timeframe: "4H", Code: "secret code",
			Params: []trade.ParamSpec{{Name: "fast", Default: 20, Min: f64(5), Max: f64(100), AITunable: true},
				{Name: "slow", Default: 50}}},
		Market: map[string]any{"rsi14": 61.2},
	}
}

func TestReviewRoundTrip(t *testing.T) {
	e := newEnv(t, Options{})
	if _, err := e.g.SetCredential(KindAPIKey, testKey); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []Proposal
	e.g.SetOnProposal(func(p Proposal) { mu.Lock(); got = append(got, p); mu.Unlock() })
	// a memory document to be attached
	if _, err := e.st.DB.Exec(`INSERT INTO ai_memory(strategy,version,ts,doc,source) VALUES('majors-trend',1,1,'avoid chasing','reflect')`); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	if e.g.Available() {
		t.Fatal("available without a worker")
	}
	w := e.dial(t, true)
	waitFor(t, "available", e.g.Available)

	type res struct {
		d   trade.Decision
		err error
	}
	ch := make(chan res, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d, err := e.g.Review(ctx, sampleRequest())
		ch <- res{d, err}
	}()
	m := w.recv()
	if m["type"] != "review" || m["id"] == "" || m["deadlineMs"] == nil {
		t.Fatalf("review message: %v", m)
	}
	req := m["request"].(map[string]any)
	if req["memory"] != "avoid chasing" {
		t.Fatalf("memory not attached: %v", req["memory"])
	}
	if strings.Contains(fmt.Sprint(req), "secret code") {
		t.Fatal("strategy code must not be sent")
	}
	w.send(map[string]any{"type": "decision", "id": m["id"], "decision": map[string]any{
		"action": "execute", "sizeScale": 0.5, "stopLoss": 101, "takeProfit": 120, "confidence": 0.7,
		"rationale": "趋势确认", "paramChange": map[string]any{"fast": 30, "slow": 80, "bogus": 1},
		"model": "claude-sonnet-5", "costUsd": 0.012, "latencyMs": 900},
		"meta": map[string]any{"promptVersion": "review_v1", "toolCalls": []any{}}})
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	d := r.d
	if d.ID == 0 || d.Action != "execute" || d.SizeScale != 0.5 || d.Confidence != 0.7 || d.Model != "claude-sonnet-5" {
		t.Fatalf("decision %+v", d)
	}
	if d.StopLoss != 0 {
		t.Fatalf("stop above a long entry must be dropped, got %v", d.StopLoss)
	}
	if d.TakeProfit != 120 {
		t.Fatalf("target %v", d.TakeProfit)
	}
	if len(d.ParamChange) != 1 || d.ParamChange["fast"] != 30 {
		t.Fatalf("param change must keep only tunable params: %v", d.ParamChange)
	}

	rec, err := e.g.Decision(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Kind != "review" || rec.AccountID != 2 || rec.InstanceID != 3 || rec.SignalID != 7 || rec.Strategy != "majors-trend" ||
		rec.PromptVersion != "review_v1" || rec.CostUSD != 0.012 || rec.MemoryVersion != 1 || rec.Action != "execute" {
		t.Fatalf("audit row %+v", rec)
	}
	if rec.RequestSHA256 != sha256hex(rec.Request) {
		t.Fatal("request hash does not match the stored request")
	}
	if !strings.Contains(string(rec.Decision), "wrong side") {
		t.Fatalf("sanitize notes missing: %s", rec.Decision)
	}
	waitFor(t, "proposal hook", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 })
	if got[0].InstanceID != 3 || got[0].Params["fast"] != 30 || got[0].Current["fast"] != 20 || got[0].State != PropPending {
		t.Fatalf("proposal %+v", got[0])
	}
	if err := e.g.RecordOutcome(d.ID, "+1.2% in 6h"); err != nil {
		t.Fatal(err)
	}
	list, err := e.g.Decisions(DecisionQuery{AccountID: 2})
	if err != nil || len(list) != 1 || len(list[0].Outcomes) != 1 || list[0].Request != nil {
		t.Fatalf("decisions list %+v %v", list, err)
	}
	if st := e.g.Status(); st.CallsToday != 1 || st.CostToday != 0.012 || st.PendingProposals != 1 || !st.ProxyOK {
		t.Fatalf("status %+v", st)
	}
}

func TestReviewUnavailable(t *testing.T) {
	e := newEnv(t, Options{})
	e.run(t)
	e.dial(t, true)
	time.Sleep(50 * time.Millisecond)
	if e.g.Available() {
		t.Fatal("available without a credential")
	}
	if _, err := e.g.Review(context.Background(), sampleRequest()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err %v", err)
	}
}

func TestReviewTimeout(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.SetCredential(KindAPIKey, testKey)
	e.run(t)
	w := e.dial(t, true)
	waitFor(t, "available", e.g.Available)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	errc := make(chan error, 1)
	go func() { _, err := e.g.Review(ctx, sampleRequest()); errc <- err }()
	m := w.recv()
	if m["type"] != "review" {
		t.Fatalf("got %v", m)
	}
	err := <-errc
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err %v", err)
	}
	c := w.recv()
	if c["type"] != "cancel" || c["id"] != m["id"] {
		t.Fatalf("expected cancel for %v, got %v", m["id"], c)
	}
	// a late answer is ignored
	w.send(map[string]any{"type": "decision", "id": m["id"], "decision": map[string]any{"action": "execute"}})
	list, _ := e.g.Decisions(DecisionQuery{})
	if len(list) != 1 || list[0].Error != "timeout" || list[0].Action != "" {
		t.Fatalf("audit %+v", list)
	}
}

func TestWorkerReconnect(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.SetCredential(KindOAuthToken, "sk-ant-oat01-TOKENTOKENTOKENTOKEN")
	e.run(t)
	w1 := e.dial(t, true)
	waitFor(t, "available", e.g.Available)

	// a call in flight on the old worker fails when a newer worker replaces it
	errc := make(chan error, 1)
	go func() { _, err := e.g.Review(context.Background(), sampleRequest()); errc <- err }()
	w1.recv()
	w2 := e.dial(t, true)
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("in-flight call on replaced worker: %v", err)
	}
	if _, err := w1.tryRecv(time.Second); err == nil {
		t.Fatal("old worker connection should be closed")
	}
	waitFor(t, "available", e.g.Available)
	go func() {
		m := w2.recv()
		w2.send(map[string]any{"type": "decision", "id": m["id"], "decision": map[string]any{"action": "skip", "confidence": 0.9, "rationale": "late"}})
	}()
	d, err := e.g.Review(context.Background(), sampleRequest())
	if err != nil || d.Action != "skip" || d.SizeScale != 0 {
		t.Fatalf("review on new worker: %+v %v", d, err)
	}
	// disconnect → unavailable; reconnect → available
	w2.c.Close()
	waitFor(t, "unavailable", func() bool { return !e.g.Available() })
	e.dial(t, true)
	waitFor(t, "available again", e.g.Available)
}

func TestWorkerReadiness(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.SetCredential(KindAPIKey, testKey)
	e.run(t)
	w := e.dial(t, false)
	time.Sleep(50 * time.Millisecond)
	if e.g.Available() {
		t.Fatal("not-ready worker must not be available")
	}
	w.send(map[string]any{"type": "status", "ready": false, "proxyOk": false, "proxyError": "proxy check: HTTP 502"})
	waitFor(t, "proxy error", func() bool { return e.g.Status().ProxyError == "proxy check: HTTP 502" })
	if r := e.g.Test(context.Background()); r.OK || !strings.Contains(r.Error, "not ready") {
		t.Fatalf("test on a not-ready worker: %+v", r)
	}
	w.send(map[string]any{"type": "status", "ready": true, "proxyOk": true})
	waitFor(t, "ready", e.g.Available)
	st := e.g.Status()
	if !st.ProxyOK || st.ProxyError != "" || !st.WorkerReady {
		t.Fatalf("status %+v", st)
	}
}

func TestHeartbeatDropsSilentWorker(t *testing.T) {
	e := newEnv(t, Options{Heartbeat: 30 * time.Millisecond})
	e.g.SetCredential(KindAPIKey, testKey)
	e.run(t)
	w := e.dial(t, true)
	if m := w.recv(); m["type"] != "ping" {
		t.Fatalf("got %v", m)
	}
	waitFor(t, "silent worker dropped", func() bool { return !e.g.Status().WorkerOnline })
}

func TestCredentialMasking(t *testing.T) {
	t.Run("encrypted", func(t *testing.T) {
		e := newEnv(t, Options{SecretKey: "owner-secret"})
		st, err := e.g.SetCredential(KindAPIKey, "  "+testKey+" ")
		if err != nil {
			t.Fatal(err)
		}
		if st.Masked != "••••WXYZ" || st.Kind != KindAPIKey || st.Storage != "encrypted" || st.StorageNote != "" {
			t.Fatalf("status %+v", st)
		}
		raw, _ := e.st.Setting(credSetting)
		if strings.Contains(raw, testKey) || strings.Contains(raw, "WXYZ") {
			t.Fatal("credential stored in clear despite CTG_SECRET_KEY")
		}
		// reload with the right key
		g2, err := New(e.st, Options{SocketPath: e.sock, SecretKey: "owner-secret", Logf: e.logs.logf})
		if err != nil || !g2.Status().Configured || g2.cred.Value != testKey {
			t.Fatalf("reload: %v %+v", err, g2.Status())
		}
		// and with a wrong or missing key
		g3, _ := New(e.st, Options{SocketPath: e.sock, SecretKey: "other", Logf: e.logs.logf})
		if s := g3.Status(); s.Configured || !strings.Contains(s.CredentialError, "decrypted") {
			t.Fatalf("wrong key status %+v", s)
		}
		os.Unsetenv("CTG_SECRET_KEY")
		g4, _ := New(e.st, Options{SocketPath: e.sock, Logf: e.logs.logf})
		if s := g4.Status(); s.Configured || !strings.Contains(s.CredentialError, "CTG_SECRET_KEY") {
			t.Fatalf("missing key status %+v", s)
		}
		// HTTP never shows it
		mux := http.NewServeMux()
		e.g.Register(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/ai/status", nil))
		if rec.Code != 200 || strings.Contains(rec.Body.String(), testKey) || !strings.Contains(rec.Body.String(), "WXYZ") {
			t.Fatalf("status body %s", rec.Body)
		}
		if strings.Contains(e.logs.String(), testKey) {
			t.Fatal("credential logged")
		}
		st, _ = e.g.DeleteCredential()
		if st.Configured {
			t.Fatal("still configured after delete")
		}
		if _, ok := e.st.Setting(credSetting); ok {
			t.Fatal("setting not removed")
		}
	})
	t.Run("plain", func(t *testing.T) {
		os.Unsetenv("CTG_SECRET_KEY")
		e := newEnv(t, Options{})
		st, _ := e.g.SetCredential(KindOAuthToken, "sk-ant-oat01-abcdefghijklmnopqrstu")
		if st.Storage != "plain" || st.StorageNote == "" || st.Masked != "••••rstu" {
			t.Fatalf("status %+v", st)
		}
	})
	t.Run("validation", func(t *testing.T) {
		e := newEnv(t, Options{})
		for _, c := range []struct{ kind, value string }{
			{"password", testKey}, {KindAPIKey, "short"}, {KindAPIKey, "sk-ant-oat01-abcdefghijklmnopqrstu"},
			{KindOAuthToken, testKey}, {KindAPIKey, "has space in the middle of it ok"},
		} {
			if _, err := e.g.SetCredential(c.kind, c.value); err == nil {
				t.Errorf("accepted %s %q", c.kind, c.value)
			} else if strings.Contains(err.Error(), c.value) {
				t.Errorf("error echoes the value: %v", err)
			}
		}
	})
	t.Run("worker error scrubbed", func(t *testing.T) {
		e := newEnv(t, Options{})
		e.g.SetCredential(KindAPIKey, testKey)
		e.run(t)
		w := e.dial(t, true)
		waitFor(t, "available", e.g.Available)
		go func() {
			m := w.recv()
			w.send(map[string]any{"type": "error", "id": m["id"], "error": "401 invalid x-api-key " + testKey})
		}()
		_, err := e.g.Review(context.Background(), sampleRequest())
		if err == nil || strings.Contains(err.Error(), testKey) {
			t.Fatalf("err %v", err)
		}
		list, _ := e.g.Decisions(DecisionQuery{})
		if len(list) != 1 || strings.Contains(list[0].Error, testKey) || !strings.Contains(list[0].Error, "***") {
			t.Fatalf("audit error %+v", list)
		}
		if strings.Contains(e.g.Status().LastError, testKey) {
			t.Fatal("lastError leaks the credential")
		}
	})
	t.Run("config pushed on change", func(t *testing.T) {
		e := newEnv(t, Options{})
		e.run(t)
		w := e.dial(t, true)
		e.g.SetCredential(KindAPIKey, testKey)
		m := w.recv()
		cred, _ := m["credential"].(map[string]any)
		if m["type"] != "config" || cred["value"] != testKey || cred["kind"] != KindAPIKey || m["model"] != DefaultModel {
			t.Fatalf("config %v", m)
		}
		e.g.SetModel("claude-haiku-4-5")
		if m := w.recv(); m["model"] != "claude-haiku-4-5" {
			t.Fatalf("model config %v", m)
		}
		e.g.DeleteCredential()
		if m := w.recv(); m["credential"] != nil {
			t.Fatalf("credential not cleared: %v", m)
		}
		if _, err := e.g.SetModel("bad model!"); err == nil {
			t.Fatal("invalid model accepted")
		}
	})
}

func reviewWith(t *testing.T, e *env, w *fakeWorker, req trade.ReviewRequest, decision map[string]any) trade.Decision {
	t.Helper()
	go func() {
		m := w.recv()
		w.send(map[string]any{"type": "decision", "id": m["id"], "decision": decision})
	}()
	d, err := e.g.Review(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestProposalsFlow(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.SetCredential(KindAPIKey, testKey)
	e.run(t)
	w := e.dial(t, true)
	waitFor(t, "available", e.g.Available)
	propose := func(v float64) {
		reviewWith(t, e, w, sampleRequest(), map[string]any{"action": "skip", "rationale": "x", "paramChange": map[string]any{"fast": v}})
	}
	propose(25)
	propose(40) // supersedes the first one
	pend, _ := e.g.Proposals(PropPending, 0)
	if len(pend) != 1 || pend[0].Params["fast"] != 40 {
		t.Fatalf("pending %+v", pend)
	}
	rej, _ := e.g.Proposals(PropRejected, 0)
	if len(rej) != 1 || !strings.Contains(rej[0].Note, "superseded") {
		t.Fatalf("rejected %+v", rej)
	}
	id := pend[0].ID
	if _, err := e.g.ApproveProposal(id, true); err == nil {
		t.Fatal("approval without ApplyParams must fail")
	}
	var applied map[string]float64
	var appliedInst int64
	e.g.SetApplyParams(func(inst int64, p map[string]float64) error {
		if p["fast"] == 99 {
			return errors.New("boom")
		}
		appliedInst, applied = inst, p
		return nil
	})

	mux := http.NewServeMux()
	e.g.Register(mux)
	post := func(id int64, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", fmt.Sprintf("/api/v1/ai/proposals/%d", id), strings.NewReader(body)))
		return rec
	}
	if rec := post(id, `{}`); rec.Code != 400 {
		t.Fatalf("missing approve: %d", rec.Code)
	}
	if rec := post(id, `{"approve":true}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"approved"`) {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	if appliedInst != 3 || applied["fast"] != 40 || len(applied) != 1 {
		t.Fatalf("applied %d %v", appliedInst, applied)
	}
	if rec := post(id, `{"approve":false}`); rec.Code != 409 {
		t.Fatalf("second decision: %d", rec.Code)
	}
	if rec := post(9999, `{"approve":true}`); rec.Code != 404 {
		t.Fatalf("unknown: %d", rec.Code)
	}
	// apply failure keeps it pending
	propose(99)
	pend, _ = e.g.Proposals(PropPending, 0)
	if rec := post(pend[0].ID, `{"approve":true}`); rec.Code != 502 {
		t.Fatalf("apply failure: %d %s", rec.Code, rec.Body)
	}
	if p, _ := e.g.Proposal(pend[0].ID); p.State != PropPending {
		t.Fatalf("state after failed apply %s", p.State)
	}
	if rec := post(pend[0].ID, `{"approve":false}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"rejected"`) {
		t.Fatalf("reject: %d %s", rec.Code, rec.Body)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/ai/proposals?state=approved", nil))
	var list []Proposal
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("approved list %s", rec.Body)
	}
}

func TestReflectAndMemory(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.SetCredential(KindAPIKey, testKey)
	e.run(t)
	w := e.dial(t, true)
	waitFor(t, "available", e.g.Available)
	d1 := reviewWith(t, e, w, sampleRequest(), map[string]any{"action": "execute", "sizeScale": 1, "confidence": 0.6, "rationale": "a"})
	reviewWith(t, e, w, sampleRequest(), map[string]any{"action": "skip", "confidence": 0.8, "rationale": "b"})
	e.g.RecordOutcome(d1.ID, "stopped -0.8%")
	if due, _ := e.g.strategiesDue(); len(due) != 1 || due[0] != "majors-trend" {
		t.Fatalf("due %v", due)
	}

	go func() {
		m := w.recv()
		req := m["request"].(map[string]any)
		ds := req["decisions"].([]any)
		first := ds[0].(map[string]any)
		ok := m["type"] == "reflect" && len(ds) == 2 && first["rationale"] == "a" && first["signalReason"] == "ema cross" &&
			first["outcomes"] != nil && req["currentVersion"].(float64) == 0
		doc := "bad request"
		if ok {
			doc = "## Lessons\n- 止损太近"
		}
		w.send(map[string]any{"type": "result", "id": m["id"], "result": map[string]any{"memory": doc, "model": "claude-sonnet-5",
			"costUsd": 0.05, "promptVersion": "reflect_v1"}})
	}()
	m, err := e.g.Reflect(context.Background(), "majors-trend")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 || m.Doc != "## Lessons\n- 止损太近" || m.Decisions != 2 || m.AuditID == 0 {
		t.Fatalf("memory %+v", m)
	}
	if due, _ := e.g.strategiesDue(); len(due) != 0 {
		t.Fatalf("nothing should be due, got %v", due)
	}
	// the next review carries memory v1
	go func() {
		m := w.recv()
		req := m["request"].(map[string]any)
		w.send(map[string]any{"type": "decision", "id": m["id"], "decision": map[string]any{"action": "skip", "rationale": req["memory"]}})
	}()
	d, err := e.g.Review(context.Background(), sampleRequest())
	if err != nil || d.Rationale != m.Doc {
		t.Fatalf("memory not attached: %+v %v", d, err)
	}

	mux := http.NewServeMux()
	e.g.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/ai/memory/majors-trend", nil))
	var body struct {
		Current  *MemoryDoc
		Versions []MemoryDoc
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || body.Current == nil || body.Current.Version != 1 || len(body.Versions) != 1 || body.Versions[0].Doc != "" {
		t.Fatalf("memory endpoint %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/ai/memory/majors-trend?version=7", nil))
	if rec.Code != 404 {
		t.Fatalf("unknown version: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/ai/decisions?kind=reflect", nil))
	if !strings.Contains(rec.Body.String(), "reflect_v1") {
		t.Fatalf("reflect audit row missing: %s", rec.Body)
	}
	if _, err := e.g.Reflect(context.Background(), "unknown-strategy"); !errors.Is(err, ErrNoDecisions) {
		t.Fatalf("err %v", err)
	}
}

func TestTestCall(t *testing.T) {
	e := newEnv(t, Options{})
	if r := e.g.Test(context.Background()); r.OK || r.Error != "no credential configured" {
		t.Fatalf("%+v", r)
	}
	e.g.SetCredential(KindAPIKey, testKey)
	e.run(t)
	w := e.dial(t, true)
	waitFor(t, "available", e.g.Available)
	go func() {
		m := w.recv()
		w.send(map[string]any{"type": "result", "id": m["id"], "result": map[string]any{"reply": "OK", "model": "claude-sonnet-5", "costUsd": 0.001}})
	}()
	mux := http.NewServeMux()
	e.g.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/ai/test", nil))
	var r TestResult
	json.Unmarshal(rec.Body.Bytes(), &r)
	if !r.OK || r.Reply != "OK" || r.CostUSD != 0.001 {
		t.Fatalf("test %s", rec.Body)
	}
	list, _ := e.g.Decisions(DecisionQuery{Kind: "test"})
	if len(list) != 1 {
		t.Fatalf("test call not audited: %+v", list)
	}
}

func TestHTTPDecisionsFilter(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.SetCredential(KindAPIKey, testKey)
	e.run(t)
	w := e.dial(t, true)
	waitFor(t, "available", e.g.Available)
	for i := 0; i < 3; i++ {
		req := sampleRequest()
		req.Signal.AccountID, req.Account.ID = int64(10+i%2), int64(10+i%2)
		reviewWith(t, e, w, req, map[string]any{"action": "skip", "rationale": fmt.Sprint(i)})
	}
	mux := http.NewServeMux()
	e.g.Register(mux)
	get := func(q string) []DecisionRecord {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/ai/decisions"+q, nil))
		var out []DecisionRecord
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %s", q, rec.Body)
		}
		return out
	}
	if l := get("?accountId=10"); len(l) != 2 || l[0].ID < l[1].ID {
		t.Fatalf("account filter %+v", l)
	}
	all := get("")
	if l := get(fmt.Sprintf("?before=%d&limit=1", all[0].ID)); len(l) != 1 || l[0].ID != all[1].ID {
		t.Fatalf("paging %+v", l)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", fmt.Sprintf("/api/v1/ai/decisions/%d", all[0].ID), nil))
	if !strings.Contains(rec.Body.String(), `"request":{`) {
		t.Fatalf("single decision lacks request: %s", rec.Body)
	}
}

func TestSanitize(t *testing.T) {
	req := sampleRequest()
	cases := []struct {
		name string
		in   workerDecision
		want trade.Decision
	}{
		{"clamp", workerDecision{Action: "EXECUTE", SizeScale: 3, Confidence: -1, StopLoss: 90, TakeProfit: 130},
			trade.Decision{Action: "execute", SizeScale: 1, StopLoss: 90, TakeProfit: 130}},
		{"unknown action", workerDecision{Action: "yolo", SizeScale: 1, Confidence: 0.5}, trade.Decision{Action: "skip", Confidence: 0.5}},
		{"zero size", workerDecision{Action: "execute", SizeScale: 0}, trade.Decision{Action: "skip"}},
		{"wrong side target", workerDecision{Action: "execute", SizeScale: 0.3, TakeProfit: 90}, trade.Decision{Action: "execute", SizeScale: 0.3}},
	}
	for _, c := range cases {
		got, _ := sanitize(c.in, req)
		if got.Action != c.want.Action || got.SizeScale != c.want.SizeScale || got.Confidence != c.want.Confidence ||
			got.StopLoss != c.want.StopLoss || got.TakeProfit != c.want.TakeProfit {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
	short := sampleRequest()
	short.Signal.Action = trade.OpenShort
	got, _ := sanitize(workerDecision{Action: "execute", SizeScale: 1, StopLoss: 105, TakeProfit: 90}, short)
	if got.StopLoss != 105 || got.TakeProfit != 90 {
		t.Errorf("short stops %+v", got)
	}
	got, notes := sanitize(workerDecision{Action: "skip", ParamChange: map[string]float64{"fast": 500, "slow": 60}}, req)
	if got.ParamChange != nil || len(notes) != 2 {
		t.Errorf("params %v notes %v", got.ParamChange, notes)
	}
}

func TestNextReflect(t *testing.T) {
	at := func(s string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, cst)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	for _, c := range [][2]string{
		{"2026-10-02 00:04", "2026-10-02 00:05"},
		{"2026-10-02 00:05", "2026-10-03 00:05"},
		{"2026-10-02 23:59", "2026-10-03 00:05"},
	} {
		if got := nextReflect(at(c[0])); !got.Equal(at(c[1])) {
			t.Errorf("%s → %s, want %s", c[0], got.In(cst), c[1])
		}
	}
}

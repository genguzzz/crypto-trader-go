package strategyhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"crypto-trader-go/internal/trade"
)

// ErrHandler marks a HandlerError.
var ErrHandler = errors.New("strategy handler raised")

// HandlerError is returned by Session methods when the Python handler raised. The event was still
// acknowledged and the process keeps running; Consecutive counts handler errors in a row so the
// caller can pause the instance (3 → paused).
type HandlerError struct {
	Event       string
	Msg         string
	Consecutive int
}

func (e *HandlerError) Error() string {
	return fmt.Sprintf("strategy %s handler raised (%d in a row): %s", e.Event, e.Consecutive, truncate(e.Msg, 500))
}

func (e *HandlerError) Unwrap() error { return ErrHandler }

type doneMsg struct {
	seq int64
	err string
}

// session is one instance process; it implements trade.Session.
type session struct {
	h      *Host
	sup    *supervisor
	inst   trade.Instance
	ver    trade.StrategyVersion
	mode   trade.Mode
	md     trade.MarketData
	sink   trade.SignalSink
	token  string
	prefix string
	tf     string // the instance's bar timeframe

	attached chan struct{} // closed after an authenticated hello
	exit     chan struct{} // closed by finish
	connDead chan struct{} // closed when the reader stops
	doneCh   chan doneMsg

	evMu sync.Mutex // one event in flight (lockstep)

	mu          sync.Mutex
	w           *wire
	seq         int64
	waitSeq     int64
	stopSeq     int64
	stopped     bool // stop acknowledged
	evCtx       context.Context
	evCloses    map[string]float64 // close of each bar in flight (signal reference price)
	status      string
	state       []byte
	handlerErrs int
	err         error
	exitOnce    sync.Once
}

var _ trade.Session = (*session)(nil)

func newSession(h *Host, inst trade.Instance, ver trade.StrategyVersion, mode trade.Mode, state []byte,
	md trade.MarketData, sink trade.SignalSink, token string) *session {
	st := []byte(`{}`)
	if len(state) > 0 && json.Valid(state) {
		st = append([]byte(nil), state...)
	}
	return &session{h: h, inst: inst, ver: ver, mode: mode, md: md, sink: sink, token: token,
		prefix: fmt.Sprintf("strategy %d %s", inst.ID, ver.Name), tf: trade.InstanceTimeframe(inst, ver),
		attached: make(chan struct{}), exit: make(chan struct{}), connDead: make(chan struct{}),
		doneCh: make(chan doneMsg, 1), waitSeq: -1, stopSeq: -1, state: st}
}

func (s *session) claim(w *wire) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.exit:
		return false
	default:
	}
	if s.w != nil {
		return false
	}
	s.w = w
	return true
}

func (s *session) isAttached() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w != nil
}

// ---------- trade.Session ----------

func (s *session) Bars(ctx context.Context, tf string, bars []trade.InstBar) error {
	items := make([]map[string]any, len(bars))
	closes := make(map[string]float64, len(bars))
	for i, x := range bars {
		items[i] = map[string]any{"instId": x.InstID, "bar": barJSON(x.Bar)}
		closes[x.InstID] = x.Bar.C
	}
	return s.event(ctx, "bars", map[string]any{"tf": tf, "bars": items, "_closes": closes})
}

func (s *session) Timer(ctx context.Context, name string) error {
	return s.event(ctx, "timer", map[string]any{"name": name})
}

func (s *session) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *session) Universe(ctx context.Context, added, removed, current []string) error {
	return s.event(ctx, "universe", map[string]any{"added": strs(added), "removed": strs(removed),
		"current": strs(current)})
}

func (s *session) Fill(ctx context.Context, f trade.Fill) error {
	return s.event(ctx, "fill", map[string]any{"fill": f})
}

func (s *session) SignalResult(ctx context.Context, signalID int64, outcome trade.SignalOutcome, reason string) error {
	return s.event(ctx, "signal_result", map[string]any{"signalId": signalID, "outcome": outcome, "reason": reason})
}

func (s *session) Funding(ctx context.Context, instID string, rate, amount float64) error {
	return s.event(ctx, "funding", map[string]any{"instId": instID, "rate": num(rate), "amount": num(amount)})
}

// Stop delivers stop (on_stop), then waits briefly for the process to exit and kills it otherwise.
func (s *session) Stop(ctx context.Context) error {
	select {
	case <-s.exit:
		return nil
	default:
	}
	err := s.event(ctx, "stop", nil)
	if err != nil && !errors.Is(err, ErrHandler) {
		return err
	}
	t := time.NewTimer(s.h.opts.EventTimeout)
	defer t.Stop()
	select {
	case <-s.exit:
	case <-t.C:
		s.kill(nil)
	case <-ctx.Done():
		s.kill(nil)
	}
	return nil
}

func (s *session) State() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.state...)
}

func (s *session) Done() <-chan struct{} { return s.exit }

func (s *session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// ---------- lifecycle ----------

// event sends one seq-numbered message and waits for its done.
func (s *session) event(ctx context.Context, typ string, fields map[string]any) error {
	s.evMu.Lock()
	defer s.evMu.Unlock()
	if err := s.deadErr(); err != nil {
		return err
	}
	msg := map[string]any{}
	var closes map[string]float64
	for k, v := range fields {
		if k == "_closes" {
			closes = v.(map[string]float64)
			continue
		}
		msg[k] = v
	}
	// a batch of bars gets the per-event budget plus a little per instrument
	evCtx, cancel := context.WithTimeout(ctx, s.h.opts.EventTimeout+time.Duration(len(closes))*s.h.opts.EventTimeout/10)
	defer cancel()

	s.mu.Lock()
	seq := s.seq
	s.seq++
	s.waitSeq = seq
	if typ == "stop" {
		s.stopSeq = seq
	}
	s.evCtx, s.evCloses = evCtx, closes
	w := s.w
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.waitSeq, s.evCtx, s.evCloses = -1, nil, nil
		s.mu.Unlock()
	}()

	msg["type"], msg["seq"], msg["now"] = typ, seq, s.md.Now()
	if err := w.send(msg); err != nil {
		err = fmt.Errorf("%w: send %s: %v", trade.ErrFaulted, typ, err)
		s.kill(err)
		return err
	}
	select {
	case d := <-s.doneCh:
		s.mu.Lock()
		if d.err == "" {
			s.handlerErrs = 0
			s.mu.Unlock()
			return nil
		}
		s.handlerErrs++
		n := s.handlerErrs
		s.mu.Unlock()
		return &HandlerError{Event: typ, Msg: d.err, Consecutive: n}
	case <-evCtx.Done():
		var err error
		if ctx.Err() != nil {
			err = fmt.Errorf("%w: %s event: %w", trade.ErrFaulted, typ, ctx.Err())
		} else {
			err = fmt.Errorf("%w: %s event not acknowledged within %s", trade.ErrFaulted, typ, s.h.opts.EventTimeout)
		}
		s.kill(err)
		return err
	case <-s.connDead:
		// Give the supervisor's exit report (with stderr) a moment to land.
		select {
		case <-s.exit:
		case <-time.After(s.h.opts.ExitGrace):
		}
		if err := s.deadErr(); err != nil {
			return err
		}
		return fmt.Errorf("%w: connection closed", trade.ErrFaulted)
	}
}

// deadErr returns the fault once the session ended or its connection dropped.
func (s *session) deadErr() error {
	select {
	case <-s.exit:
		if err := s.Err(); err != nil {
			return err
		}
		return fmt.Errorf("%w: process stopped", trade.ErrFaulted)
	default:
	}
	return nil
}

// kill asks the supervisor to kill the process and ends the session with err (nil = clean stop).
func (s *session) kill(err error) {
	if err != nil {
		s.h.log.Printf("%s: killing: %v", s.prefix, err)
	}
	s.h.sendKill(s)
	s.finish(err)
}

func (s *session) finish(err error) {
	s.exitOnce.Do(func() {
		s.mu.Lock()
		s.err = err
		w := s.w
		s.mu.Unlock()
		if w != nil {
			w.close()
		}
		s.h.remove(s)
		close(s.exit)
	})
}

func (s *session) processExited(code int, stderr string) {
	// The report travels on the supervisor connection and can overtake the process's last
	// messages (e.g. the done for stop); let the reader drain them first.
	if s.isAttached() {
		t := time.NewTimer(s.h.opts.ExitGrace)
		select {
		case <-s.connDead:
		case <-t.C:
		}
		t.Stop()
	}
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped && code == 0 {
		s.finish(nil)
		return
	}
	if stopped {
		s.h.log.Printf("%s: exited with code %d after stop: %s", s.prefix, code, truncate(stderr, 2000))
		s.finish(nil)
		return
	}
	err := fmt.Errorf("%w: process exited with code %d", trade.ErrFaulted, code)
	if stderr != "" {
		err = fmt.Errorf("%w: process exited with code %d: %s", trade.ErrFaulted, code, truncate(stderr, 4000))
	}
	s.h.log.Printf("%s: %v", s.prefix, err)
	s.finish(err)
}

func (s *session) readLoop() {
	defer func() {
		close(s.connDead)
		s.mu.Lock()
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			s.finish(nil)
			return
		}
		time.AfterFunc(s.h.opts.ExitGrace, func() {
			s.finish(fmt.Errorf("%w: connection closed", trade.ErrFaulted))
		})
	}()
	for {
		line, err := s.w.recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				s.h.log.Printf("%s: read: %v", s.prefix, err)
			}
			return
		}
		var m inMsg
		if err := json.Unmarshal(line, &m); err != nil {
			s.h.log.Printf("%s: bad message: %v", s.prefix, err)
			continue
		}
		switch m.Type {
		case "done":
			s.mu.Lock()
			if m.Seq == s.waitSeq {
				s.waitSeq = -1
				if m.Seq == s.stopSeq {
					s.stopped = true
				}
				select {
				case s.doneCh <- doneMsg{seq: m.Seq, err: m.Error}:
				default:
				}
			} else {
				s.h.log.Printf("%s: unexpected done seq %d", s.prefix, m.Seq)
			}
			s.mu.Unlock()
		case "query":
			data, err := s.query(m.Method, m.Args)
			s.reply(m.QID, "result", data, err)
		case "signal":
			var sm signalMsg
			if err := json.Unmarshal(line, &sm); err != nil {
				s.reply(m.QID, "signal_ack", nil, err)
				continue
			}
			id, err := s.submit(sm)
			s.reply(m.QID, "signal_ack", id, err)
		case "state":
			s.saveState(m.Data)
		case "log":
			s.h.log.Printf("%s: [%s] %s", s.prefix, m.Level, truncate(m.Msg, 2000))
			s.sink.Log(s.inst.ID, m.Level, m.Msg)
		case "notify":
			var n notifyMsg
			if json.Unmarshal(line, &n) == nil {
				s.sink.Notify(s.inst.ID, n.Level, n.Text, n.Data)
			}
		case "status":
			s.mu.Lock()
			s.status = truncate(m.Msg, 300)
			s.mu.Unlock()
		default:
			s.h.log.Printf("%s: unknown message %q", s.prefix, m.Type)
		}
	}
}

func (s *session) reply(qid json.RawMessage, typ string, data any, err error) {
	if qid == nil {
		qid = json.RawMessage("null")
	}
	if err == nil {
		msg := map[string]any{"type": typ, "qid": qid}
		if typ == "signal_ack" {
			msg["id"] = data
		} else {
			msg["data"] = data
		}
		if err = s.w.send(msg); err == nil {
			return
		}
	}
	_ = s.w.send(map[string]any{"type": "error", "qid": qid, "error": err.Error()})
}

func (s *session) saveState(data json.RawMessage) {
	if len(data) > maxState {
		s.h.log.Printf("%s: state of %d bytes exceeds %d, not saved", s.prefix, len(data), maxState)
		return
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		s.h.log.Printf("%s: state is not a JSON object, not saved", s.prefix)
		return
	}
	s.mu.Lock()
	s.state = append([]byte(nil), data...)
	s.mu.Unlock()
}

// callCtx is the context of the event in flight, so sink calls inherit its deadline.
func (s *session) callCtx() (context.Context, context.CancelFunc) {
	s.mu.Lock()
	ctx := s.evCtx
	s.mu.Unlock()
	if ctx != nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.Background(), s.h.opts.EventTimeout)
}

func (s *session) submit(sm signalMsg) (int64, error) {
	switch sm.Action {
	case trade.OpenLong, trade.OpenShort, trade.Close, trade.Reduce:
	default:
		return 0, fmt.Errorf("unknown action %q", sm.Action)
	}
	if sm.InstID == "" {
		return 0, errors.New("instId required")
	}
	if sm.Strength <= 0 {
		sm.Strength = 1
	}
	sig := trade.Signal{
		InstanceID: s.inst.ID, AccountID: s.inst.AccountID, InstID: sm.InstID, Action: sm.Action,
		Strength: sm.Strength, StopLoss: sm.StopLoss, TakeProfit: sm.TakeProfit, MaxSlippageBp: sm.MaxSlippageBp,
		Leverage: sm.Leverage, ReduceFrac: sm.ReduceFrac, TTLms: sm.TTLms, Reason: sm.Reason,
		Reverse: sm.Reverse, TrailDist: sm.TrailDist, TimeLimitMs: sm.TimeLimitMs,
		Ts: s.md.Now(), RecvTs: time.Now().UnixMilli(), ConfigVersion: s.inst.ConfigVersion,
	}
	s.mu.Lock()
	if c, ok := s.evCloses[sm.InstID]; ok {
		sig.Price = c
	}
	s.mu.Unlock()
	ctx, cancel := s.callCtx()
	defer cancel()
	return s.sink.Submit(ctx, sig)
}

const maxBars = 5000

func (s *session) query(method string, raw json.RawMessage) (any, error) {
	var a struct {
		InstID string `json:"instId"`
		TF     string `json:"tf"`
		N      int    `json:"n"`
		Rule   string `json:"rule"`
		Field  string `json:"field"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("bad args: %v", err)
		}
	}
	switch method {
	case "series":
		if a.TF == "" {
			a.TF = s.tf
		}
		if a.N <= 0 {
			a.N = 100
		}
		return s.sink.Series(s.inst.ID, a.InstID, a.Field, a.TF, a.N)
	case "bars":
		if a.TF == "" {
			a.TF = s.tf
		}
		if a.N <= 0 {
			a.N = 100
		}
		a.N = min(a.N, maxBars)
		bars, err := s.md.Bars(a.InstID, a.TF, a.N)
		if err != nil {
			return nil, err
		}
		rows := make([][7]any, len(bars))
		for i, b := range bars {
			rows[i] = [7]any{b.Ts, num(b.O), num(b.H), num(b.L), num(b.C), num(b.Vol), num(b.VolQ)}
		}
		return rows, nil
	case "factors":
		f, ok := s.md.Factors(a.InstID)
		if !ok {
			return nil, nil
		}
		return numMap(f), nil
	case "screener":
		var q trade.ScreenerQuery
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &q); err != nil {
				return nil, fmt.Errorf("bad args: %v", err)
			}
		}
		rows, err := s.md.Screener(q)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, len(rows))
		for i, r := range rows {
			out[i] = map[string]any{"instId": r.InstID, "rank": r.Rank, "last": num(r.Last), "long": num(r.Long),
				"short": num(r.Short), "values": numMap(r.Values)}
		}
		return out, nil
	case "score":
		l, sh, ok := s.md.Score(a.Rule, a.InstID)
		if !ok {
			return nil, nil
		}
		return map[string]any{"long": num(l), "short": num(sh)}, nil
	case "position":
		p, ok := s.sink.Position(s.inst.ID, a.InstID)
		if !ok {
			return nil, nil
		}
		return p, nil
	case "positions":
		ps := s.sink.Positions(s.inst.ID)
		if ps == nil {
			ps = []trade.Position{}
		}
		return ps, nil
	case "account":
		acc, ok := s.sink.AccountView(s.inst.ID)
		if !ok {
			return nil, nil
		}
		return acc, nil
	case "spec":
		sp, ok := s.md.Spec(a.InstID)
		if !ok {
			return nil, nil
		}
		return map[string]any{"instId": sp.InstID, "ctVal": sp.CtVal, "tickSz": sp.TickSz, "lotSz": sp.LotSz,
			"minSz": sp.MinSz, "maxLever": sp.MaxLever}, nil
	}
	return nil, fmt.Errorf("unknown method %q", method)
}

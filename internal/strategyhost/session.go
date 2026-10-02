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
	evInst      string // instrument of the bar in flight
	evClose     float64
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
		prefix:   fmt.Sprintf("strategy %d %s", inst.ID, ver.Name),
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

func (s *session) Bar(ctx context.Context, instID, tf string, b trade.Bar) error {
	return s.event(ctx, "bar", map[string]any{"instId": instID, "tf": tf, "bar": barJSON(b), "_close": b.C})
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
	var barClose float64
	for k, v := range fields {
		if k == "_close" {
			barClose = v.(float64)
			continue
		}
		msg[k] = v
	}
	evCtx, cancel := context.WithTimeout(ctx, s.h.opts.EventTimeout)
	defer cancel()

	s.mu.Lock()
	seq := s.seq
	s.seq++
	s.waitSeq = seq
	if typ == "stop" {
		s.stopSeq = seq
	}
	s.evCtx = evCtx
	if typ == "bar" {
		s.evInst, s.evClose = msg["instId"].(string), barClose
	}
	w := s.w
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.waitSeq, s.evCtx, s.evInst, s.evClose = -1, nil, "", 0
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
		Ts: s.md.Now(), RecvTs: time.Now().UnixMilli(), ConfigVersion: s.inst.ConfigVersion,
	}
	s.mu.Lock()
	if s.evInst == sm.InstID {
		sig.Price = s.evClose
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
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("bad args: %v", err)
		}
	}
	switch method {
	case "bars":
		if a.TF == "" {
			a.TF = s.ver.Timeframe
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

// Package strategyhost runs strategy processes: a Unix-socket server that the Python supervisor
// (strategies/runner.py) and every per-instance process connect to. It implements trade.Runner; see
// docs/trading-design.md §2 for the protocol.
package strategyhost

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"crypto-trader-go/internal/trade"
)

// ErrUnavailable is returned by Describe and Start while no supervisor is connected.
var ErrUnavailable = errors.New("strategy supervisor not connected")

// Options configure a Host. Zero values select the defaults.
type Options struct {
	Socket          string        // default $CTG_RUN_DIR/strategy.sock (CTG_RUN_DIR default data/run)
	SocketMode      os.FileMode   // default 0666 so a container uid can connect
	EventTimeout    time.Duration // per event, default 5 s
	StartTimeout    time.Duration // spawn until hello, default 20 s
	DescribeTimeout time.Duration // default 60 s
	ExitGrace       time.Duration // wait for the exit report after a connection drops, default 2 s
	Logger          *log.Logger   // default log.Default()
}

// DefaultSocketPath is $CTG_RUN_DIR/strategy.sock.
func DefaultSocketPath() string {
	dir := os.Getenv("CTG_RUN_DIR")
	if dir == "" {
		dir = filepath.Join("data", "run")
	}
	return filepath.Join(dir, "strategy.sock")
}

// Host implements trade.Runner.
type Host struct {
	opts Options
	log  *log.Logger

	mu       sync.Mutex
	sup      *supervisor
	sessions map[int64]*session // by instance id, from spawn until exit
	classes  map[string]string  // version id → class name, learned from Describe
	conns    map[net.Conn]struct{}
	nextID   int64
	closed   bool
}

var _ trade.Runner = (*Host)(nil)

func New(opts Options) *Host {
	if opts.Socket == "" {
		opts.Socket = DefaultSocketPath()
	}
	if opts.SocketMode == 0 {
		opts.SocketMode = 0o666
	}
	if opts.EventTimeout <= 0 {
		opts.EventTimeout = 5 * time.Second
	}
	if opts.StartTimeout <= 0 {
		opts.StartTimeout = 20 * time.Second
	}
	if opts.DescribeTimeout <= 0 {
		opts.DescribeTimeout = 60 * time.Second
	}
	if opts.ExitGrace <= 0 {
		opts.ExitGrace = 2 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	return &Host{opts: opts, log: opts.Logger, sessions: map[int64]*session{}, classes: map[string]string{},
		conns: map[net.Conn]struct{}{}}
}

// Socket returns the socket path the host listens on.
func (h *Host) Socket() string { return h.opts.Socket }

// Run listens until ctx is cancelled, then closes every connection (which ends every strategy
// process) and removes the socket.
func (h *Host) Run(ctx context.Context) error {
	path := h.opts.Socket
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("strategyhost: %w", err)
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("strategyhost: %s exists and is not a socket", path)
		}
		_ = os.Remove(path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("strategyhost: %w", err)
	}
	if err := os.Chmod(path, h.opts.SocketMode); err != nil {
		ln.Close()
		return fmt.Errorf("strategyhost: %w", err)
	}
	h.log.Printf("strategyhost: listening on %s", path)
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()
	defer h.shutdown()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return fmt.Errorf("strategyhost: accept: %w", err)
		}
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			c.Close()
			continue
		}
		h.conns[c] = struct{}{}
		h.mu.Unlock()
		go h.handle(c)
	}
}

func (h *Host) shutdown() {
	h.mu.Lock()
	h.closed = true
	conns := h.conns
	h.conns = map[net.Conn]struct{}{}
	sessions := make([]*session, 0, len(h.sessions))
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.mu.Unlock()
	for c := range conns {
		c.Close()
	}
	for _, s := range sessions {
		s.finish(fmt.Errorf("%w: host shut down", trade.ErrFaulted))
	}
}

func (h *Host) handle(c net.Conn) {
	defer func() {
		c.Close()
		h.mu.Lock()
		delete(h.conns, c)
		h.mu.Unlock()
	}()
	w := newWire(c)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := w.recv()
	if err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	var m inMsg
	if err := json.Unmarshal(line, &m); err != nil {
		_ = w.send(map[string]any{"type": "error", "error": "bad first message"})
		return
	}
	switch m.Type {
	case "supervise":
		h.supervise(w)
	case "hello":
		h.attach(w, m)
	default:
		_ = w.send(map[string]any{"type": "error", "error": "expected hello or supervise"})
	}
}

// ---------- supervisor ----------

type supervisor struct {
	w       *wire
	mu      sync.Mutex
	pending map[int64]chan []describedClass
	gone    chan struct{}
}

// Available reports whether a supervisor is connected.
func (h *Host) Available() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sup != nil
}

func (h *Host) supervise(w *wire) {
	sp := &supervisor{w: w, pending: map[int64]chan []describedClass{}, gone: make(chan struct{})}
	h.mu.Lock()
	old := h.sup
	h.sup = sp
	h.mu.Unlock()
	if old != nil {
		h.log.Printf("strategyhost: new supervisor replaces the previous one")
		old.w.close()
	}
	h.log.Printf("strategyhost: supervisor connected")
	defer func() {
		close(sp.gone)
		h.mu.Lock()
		if h.sup == sp {
			h.sup = nil
		}
		// Instances that never said hello cannot arrive anymore.
		var waiting []*session
		for _, s := range h.sessions {
			if s.sup == sp && !s.isAttached() {
				waiting = append(waiting, s)
			}
		}
		h.mu.Unlock()
		for _, s := range waiting {
			s.finish(fmt.Errorf("%w: supervisor disconnected before the process connected", trade.ErrFaulted))
		}
		h.log.Printf("strategyhost: supervisor disconnected")
	}()
	for {
		line, err := w.recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				h.log.Printf("strategyhost: supervisor: %v", err)
			}
			return
		}
		var m inMsg
		if err := json.Unmarshal(line, &m); err != nil {
			h.log.Printf("strategyhost: supervisor: bad message: %v", err)
			continue
		}
		switch m.Type {
		case "describe_result":
			sp.mu.Lock()
			ch := sp.pending[m.ID]
			delete(sp.pending, m.ID)
			sp.mu.Unlock()
			if ch != nil {
				ch <- m.Strategies
			}
		case "exited":
			h.exited(m.Instance, m.Token, m.Code, m.Stderr)
		case "log":
			h.log.Printf("strategy supervisor: [%s] %s", m.Level, truncate(m.Msg, 2000))
		default:
			h.log.Printf("strategyhost: supervisor: unknown message %q", m.Type)
		}
	}
}

func (h *Host) exited(instance int64, token string, code int, stderr string) {
	h.mu.Lock()
	s := h.sessions[instance]
	h.mu.Unlock()
	if s == nil || (token != "" && token != s.token) {
		return
	}
	go s.processExited(code, stderr)
}

func (h *Host) supervisorConn() *supervisor {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sup
}

// Describe asks the supervisor to import every strategies/library/*.py and returns one version per
// class. ID = first 12 hex of sha256(code); classes sharing a file also hash their class name.
func (h *Host) Describe(ctx context.Context) ([]trade.StrategyVersion, error) {
	h.mu.Lock()
	sp := h.sup
	h.nextID++
	id := h.nextID
	h.mu.Unlock()
	if sp == nil {
		return nil, ErrUnavailable
	}
	ch := make(chan []describedClass, 1)
	sp.mu.Lock()
	sp.pending[id] = ch
	sp.mu.Unlock()
	defer func() {
		sp.mu.Lock()
		delete(sp.pending, id)
		sp.mu.Unlock()
	}()
	if err := sp.w.send(map[string]any{"type": "describe", "id": id}); err != nil {
		return nil, fmt.Errorf("describe: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, h.opts.DescribeTimeout)
	defer cancel()
	var got []describedClass
	select {
	case got = <-ch:
	case <-sp.gone:
		return nil, ErrUnavailable
	case <-ctx.Done():
		return nil, fmt.Errorf("describe: %w", ctx.Err())
	}

	perFile := map[string]int{}
	for _, d := range got {
		if d.Error == "" {
			perFile[d.File]++
		}
	}
	out := make([]trade.StrategyVersion, 0, len(got))
	classes := map[string]string{}
	for _, d := range got {
		if d.Error != "" {
			h.log.Printf("strategyhost: describe %s: %s", d.File, truncate(d.Error, 2000))
			continue
		}
		sum := sha256.New()
		sum.Write([]byte(d.Code))
		if perFile[d.File] > 1 {
			sum.Write([]byte("\x00" + d.Class))
		}
		v := trade.StrategyVersion{
			ID:              hex.EncodeToString(sum.Sum(nil))[:12],
			Name:            d.Name,
			File:            d.File,
			Description:     d.Description,
			Timeframe:       d.Timeframe,
			Params:          d.Params,
			DefaultUniverse: d.Universe,
			Code:            d.Code,
		}
		if v.Params == nil {
			v.Params = []trade.ParamSpec{}
		}
		classes[v.ID] = d.Class
		out = append(out, v)
	}
	h.mu.Lock()
	for k, v := range classes {
		h.classes[k] = v
	}
	h.mu.Unlock()
	return out, nil
}

// Start spawns a process for inst through the supervisor, waits for its authenticated hello and
// delivers init (on_start). The process runs ver.Code when present, otherwise ver.File from disk.
func (h *Host) Start(ctx context.Context, inst trade.Instance, ver trade.StrategyVersion, mode trade.Mode,
	state []byte, md trade.MarketData, sink trade.SignalSink) (trade.Session, error) {
	if md == nil || sink == nil {
		return nil, errors.New("strategyhost: Start needs MarketData and SignalSink")
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	s := newSession(h, inst, ver, mode, state, md, sink, token)

	h.mu.Lock()
	sp := h.sup
	if sp == nil || h.closed {
		h.mu.Unlock()
		return nil, ErrUnavailable
	}
	if old := h.sessions[inst.ID]; old != nil {
		h.mu.Unlock()
		return nil, fmt.Errorf("strategyhost: instance %d is already running", inst.ID)
	}
	s.sup = sp
	h.sessions[inst.ID] = s
	class := h.classes[ver.ID]
	h.mu.Unlock()

	err = sp.w.send(map[string]any{"type": "spawn", "instance": inst.ID, "token": token, "file": ver.File,
		"class": class, "name": ver.Name, "code": ver.Code})
	if err != nil {
		s.finish(fmt.Errorf("%w: spawn: %v", trade.ErrFaulted, err))
		return nil, s.Err()
	}

	wait, cancel := context.WithTimeout(ctx, h.opts.StartTimeout)
	defer cancel()
	select {
	case <-s.attached:
	case <-s.exit:
		return nil, fmt.Errorf("start instance %d: %w", inst.ID, s.Err())
	case <-wait.Done():
		err := fmt.Errorf("%w: no hello from instance %d: %v", trade.ErrFaulted, inst.ID, wait.Err())
		s.kill(err)
		return nil, err
	}

	verNoCode := ver
	verNoCode.Code = ""
	st := json.RawMessage(`{}`)
	if len(state) > 0 && json.Valid(state) {
		st = json.RawMessage(state)
	}
	err = s.event(ctx, "init", map[string]any{"instance": inst, "strategy": verNoCode, "state": st, "mode": mode})
	var he *HandlerError
	if errors.As(err, &he) {
		h.log.Printf("%s: on_start raised: %s", s.prefix, truncate(he.Msg, 2000))
	} else if err != nil {
		return nil, fmt.Errorf("start instance %d: %w", inst.ID, err)
	}
	return s, nil
}

// attach authenticates an instance hello and hands the connection to its session.
func (h *Host) attach(w *wire, m inMsg) {
	h.mu.Lock()
	s := h.sessions[m.Instance]
	ok := s != nil && subtle.ConstantTimeCompare([]byte(s.token), []byte(m.Token)) == 1 && s.claim(w)
	h.mu.Unlock()
	if !ok {
		h.log.Printf("strategyhost: rejected hello for instance %d", m.Instance)
		_ = w.send(map[string]any{"type": "error", "error": "unauthorized"})
		return
	}
	if m.SDK != "" && m.SDK != "1" {
		h.log.Printf("%s: sdk version %q (host speaks 1)", s.prefix, m.SDK)
	}
	close(s.attached)
	s.readLoop()
}

func (h *Host) remove(s *session) {
	h.mu.Lock()
	if h.sessions[s.inst.ID] == s {
		delete(h.sessions, s.inst.ID)
	}
	h.mu.Unlock()
}

func (h *Host) sendKill(s *session) {
	if sp := s.sup; sp != nil {
		_ = sp.w.send(map[string]any{"type": "kill", "instance": s.inst.ID, "token": s.token})
	}
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

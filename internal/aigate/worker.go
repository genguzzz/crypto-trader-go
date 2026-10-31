package aigate

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Protocol (NDJSON over $CTG_RUN_DIR/ai.sock, docs/trading-design.md §5).
//
// Worker → Go: hello {worker, sdk, cli, ready, proxyOk, proxyError} (first line) · status {ready,
// proxyOk, proxyError} (on change) · decision {id, decision, meta} · result {id, result} (test and
// reflect) · error {id, error} · pong.
// Go → worker: config {credential, model} · review {id, request, deadlineMs} · test {id, deadlineMs} ·
// reflect {id, request, deadlineMs} · chat {id, chatId, sessionId, message, instId, deadlineMs} ·
// cancel {id} · ping. Worker → Go also: event {id, event} (chat progress, the call stays pending).
//
// Additive to §5: reflect/result, cancel, status and the ready/proxy fields.

const maxLine = 16 << 20 // one message; review requests carry the full context pack

// ErrUnavailable is returned when no ready worker is connected or no credential is configured.
var ErrUnavailable = errors.New("AI 审核不可用（未授权、worker 未连接或代理不可用）")

// ErrWorkerGone is returned for calls that were in flight when the worker disconnected.
var ErrWorkerGone = errors.New("AI worker 已断开")

type inbound struct {
	Type       string          `json:"type"`
	ID         string          `json:"id,omitempty"`
	Worker     string          `json:"worker,omitempty"`
	SDK        string          `json:"sdk,omitempty"`
	CLI        string          `json:"cli,omitempty"`
	Ready      *bool           `json:"ready,omitempty"`
	ProxyOK    *bool           `json:"proxyOk,omitempty"`
	ProxyError string          `json:"proxyError,omitempty"`
	Error      string          `json:"error,omitempty"`
	Decision   json.RawMessage `json:"decision,omitempty"`
	Meta       json.RawMessage `json:"meta,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Event      json.RawMessage `json:"event,omitempty"`
}

// WorkerInfo is what the connected worker reported.
type WorkerInfo struct {
	Version     string `json:"version"`
	SDK         string `json:"sdk,omitempty"`
	CLI         string `json:"cli,omitempty"`
	Ready       bool   `json:"ready"`
	ProxyOK     bool   `json:"proxyOk"`
	ProxyError  string `json:"proxyError,omitempty"`
	ConnectedAt int64  `json:"connectedAt"`
}

type workerConn struct {
	c       net.Conn
	wmu     sync.Mutex
	mu      sync.Mutex
	pending map[string]chan inbound
	info    WorkerInfo
	lastIn  time.Time
	done    chan struct{}
	once    sync.Once
}

func (w *workerConn) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	w.wmu.Lock()
	defer w.wmu.Unlock()
	w.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = w.c.Write(b)
	return err
}

func (w *workerConn) close() {
	w.once.Do(func() {
		w.c.Close()
		close(w.done)
		w.mu.Lock()
		for id, ch := range w.pending {
			ch <- inbound{Type: "error", ID: id, Error: ErrWorkerGone.Error()}
			delete(w.pending, id)
		}
		w.mu.Unlock()
	})
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// SocketPath resolves the socket location: Options.SocketPath, else $CTG_RUN_DIR/ai.sock, else
// data/run/ai.sock.
func SocketPath(o Options) string {
	if o.SocketPath != "" {
		return o.SocketPath
	}
	dir := os.Getenv("CTG_RUN_DIR")
	if dir == "" {
		dir = filepath.Join("data", "run")
	}
	return filepath.Join(dir, "ai.sock")
}

// Run serves the worker socket and the daily reflection schedule until ctx is cancelled.
func (g *Gate) Run(ctx context.Context) error {
	path := g.sock
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("aigate: run dir: %w", err)
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
		os.Remove(path) // stale socket from a previous run
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("aigate: listen: %w", err)
	}
	if err := os.Chmod(path, g.opts.SocketMode); err != nil {
		ln.Close()
		return fmt.Errorf("aigate: chmod socket: %w", err)
	}
	g.logf("aigate: listening on %s", path)
	go g.reflectLoop(ctx)
	go g.planLoop(ctx)
	go func() {
		<-ctx.Done()
		ln.Close()
		g.mu.Lock()
		w := g.w
		g.mu.Unlock()
		if w != nil {
			w.close()
		}
	}()
	defer os.Remove(path)
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
			return fmt.Errorf("aigate: accept: %w", err)
		}
		go g.serve(ctx, c)
	}
}

func (g *Gate) serve(ctx context.Context, c net.Conn) {
	r := bufio.NewReaderSize(c, 64<<10)
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := readLine(r)
	if err != nil {
		c.Close()
		return
	}
	var h inbound
	if json.Unmarshal(line, &h) != nil || h.Type != "hello" {
		c.Close()
		return
	}
	c.SetReadDeadline(time.Time{})
	w := &workerConn{c: c, pending: map[string]chan inbound{}, done: make(chan struct{}), lastIn: time.Now()}
	w.info = WorkerInfo{Version: h.Worker, SDK: h.SDK, CLI: h.CLI, ConnectedAt: g.now().UnixMilli()}
	applyStatus(&w.info, h)

	// Newest wins: replace any previous worker.
	g.mu.Lock()
	old := g.w
	g.w = w
	cfg := g.configMsgLocked()
	g.mu.Unlock()
	if old != nil {
		old.close()
	}
	g.logf("aigate: worker connected (version %s, sdk %s, ready %v)", h.Worker, h.SDK, w.info.Ready)
	if err := w.send(cfg); err != nil {
		g.drop(w)
		return
	}
	go g.heartbeat(ctx, w)
	for {
		line, err := readLine(r)
		if err != nil {
			break
		}
		var m inbound
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		w.mu.Lock()
		w.lastIn = time.Now()
		w.mu.Unlock()
		switch m.Type {
		case "pong":
		case "status", "hello":
			g.mu.Lock()
			applyStatus(&w.info, m)
			g.mu.Unlock()
		case "event": // a streaming call's progress; the call stays pending
			w.mu.Lock()
			ch := w.pending[m.ID]
			w.mu.Unlock()
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
		case "decision", "result", "error":
			w.mu.Lock()
			ch := w.pending[m.ID]
			delete(w.pending, m.ID)
			w.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
	g.drop(w)
}

func applyStatus(info *WorkerInfo, m inbound) {
	if m.Ready != nil {
		info.Ready = *m.Ready
	}
	if m.ProxyOK != nil {
		info.ProxyOK = *m.ProxyOK
	}
	info.ProxyError = m.ProxyError
}

func (g *Gate) drop(w *workerConn) {
	w.close()
	g.mu.Lock()
	if g.w == w {
		g.w = nil
		g.logf("aigate: worker disconnected")
	}
	g.mu.Unlock()
}

// heartbeat pings every Options.Heartbeat and drops a worker silent for 3 intervals.
func (g *Gate) heartbeat(ctx context.Context, w *workerConn) {
	t := time.NewTicker(g.opts.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-t.C:
			w.mu.Lock()
			silent := time.Since(w.lastIn)
			w.mu.Unlock()
			if silent > 3*g.opts.Heartbeat {
				g.drop(w)
				return
			}
			if w.send(map[string]string{"type": "ping"}) != nil {
				g.drop(w)
				return
			}
		}
	}
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		buf = append(buf, chunk...)
		if len(buf) > maxLine {
			return nil, errors.New("line too long")
		}
		if !isPrefix {
			return buf, nil
		}
	}
}

// call sends one request and waits for its answer, ctx cancel or worker loss. On ctx cancel the
// worker is told to cancel the call.
func (g *Gate) call(ctx context.Context, msg map[string]any) (inbound, error) {
	g.mu.Lock()
	w := g.w
	g.mu.Unlock()
	if w == nil {
		return inbound{}, ErrUnavailable
	}
	id := newID()
	msg["id"] = id
	if dl, ok := ctx.Deadline(); ok {
		msg["deadlineMs"] = dl.UnixMilli()
	}
	ch := make(chan inbound, 1)
	w.mu.Lock()
	w.pending[id] = ch
	w.mu.Unlock()
	select {
	case <-w.done:
		return inbound{}, ErrWorkerGone
	default:
	}
	if err := w.send(msg); err != nil {
		w.mu.Lock()
		delete(w.pending, id)
		w.mu.Unlock()
		g.drop(w)
		return inbound{}, ErrWorkerGone
	}
	select {
	case m := <-ch:
		if m.Type == "error" {
			return m, errors.New(m.Error)
		}
		return m, nil
	case <-ctx.Done():
		w.mu.Lock()
		delete(w.pending, id)
		w.mu.Unlock()
		w.send(map[string]string{"type": "cancel", "id": id})
		return inbound{}, ctx.Err()
	}
}

// pushConfig sends the current credential and model to the connected worker (after a change).
func (g *Gate) pushConfig() {
	g.mu.Lock()
	w := g.w
	cfg := g.configMsgLocked()
	g.mu.Unlock()
	if w != nil {
		if err := w.send(cfg); err != nil {
			g.drop(w)
		}
	}
}

func (g *Gate) configMsgLocked() map[string]any {
	var cred any
	if g.cred.configured() {
		cred = map[string]string{"kind": g.cred.Kind, "value": g.cred.Value}
	}
	return map[string]any{"type": "config", "credential": cred, "model": g.model, "agent": g.agent}
}

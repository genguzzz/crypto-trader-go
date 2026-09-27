package okx

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	PublicWSURL   = "wss://ws.okx.com:8443/ws/v5/public"
	BusinessWSURL = "wss://ws.okx.com:8443/ws/v5/business"
)

type Arg struct {
	Channel string `json:"channel"`
	InstID  string `json:"instId"`
}

// Message is one pushed data message.
type Message struct {
	Arg      Arg
	Data     json.RawMessage
	Received time.Time
}

// WS is a self-healing OKX WebSocket connection with a desired subscription set.
// After every (re)connect it resubscribes the full set and calls OnConnect with the time the
// previous connection dropped (zero on first connect) so callers can gap-fill.
type WS struct {
	Name      string
	URL       string
	OnMessage func(Message)
	OnConnect func(downSince time.Time)

	mu      sync.Mutex
	subs    map[Arg]bool
	conn    *websocket.Conn
	writeMu sync.Mutex

	Connected  atomic.Bool
	Reconnects atomic.Int64
	Messages   atomic.Int64
	LastMsgAt  atomic.Int64 // unix ms
	SubErrors  atomic.Int64
}

func NewWS(name, url string) *WS { return &WS{Name: name, URL: url, subs: map[Arg]bool{}} }

// SetSubscriptions replaces the desired set, sending only the difference when connected.
func (w *WS) SetSubscriptions(args []Arg) {
	want := make(map[Arg]bool, len(args))
	for _, a := range args {
		want[a] = true
	}
	w.mu.Lock()
	var add, del []Arg
	for a := range want {
		if !w.subs[a] {
			add = append(add, a)
		}
	}
	for a := range w.subs {
		if !want[a] {
			del = append(del, a)
		}
	}
	w.subs = want
	conn := w.conn
	w.mu.Unlock()
	if conn != nil {
		w.send(conn, "unsubscribe", del)
		w.send(conn, "subscribe", add)
	}
}

func (w *WS) Subscriptions() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.subs)
}

func (w *WS) send(conn *websocket.Conn, op string, args []Arg) {
	// OKX caps one request at 64KB; 100 args stays far below.
	for i := 0; i < len(args); i += 100 {
		j := min(i+100, len(args))
		w.writeMu.Lock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		err := conn.WriteJSON(map[string]any{"op": op, "args": args[i:j]})
		w.writeMu.Unlock()
		if err != nil {
			return
		}
	}
}

// Run keeps the connection alive until ctx ends.
func (w *WS) Run(ctx context.Context) {
	var downSince time.Time
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := w.session(ctx, downSince)
		w.Connected.Store(false)
		if ctx.Err() != nil {
			return
		}
		downSince = time.Now()
		w.Reconnects.Add(1)
		log.Printf("ws %s: disconnected: %v", w.Name, err)
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (w *WS) session(ctx context.Context, downSince time.Time) error {
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	if nd := currentDial(); nd != nil {
		d.NetDialContext = nd
	}
	conn, _, err := d.DialContext(ctx, w.URL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	w.mu.Lock()
	w.conn = conn
	args := make([]Arg, 0, len(w.subs))
	for a := range w.subs {
		args = append(args, a)
	}
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.conn = nil
		w.mu.Unlock()
	}()
	w.send(conn, "subscribe", args)
	w.Connected.Store(true)
	if w.OnConnect != nil {
		go w.OnConnect(downSince)
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				conn.Close()
				return
			case <-t.C:
				w.writeMu.Lock()
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				conn.WriteMessage(websocket.TextMessage, []byte("ping"))
				w.writeMu.Unlock()
			}
		}
	}()
	for {
		conn.SetReadDeadline(time.Now().Add(45 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		now := time.Now()
		w.LastMsgAt.Store(now.UnixMilli())
		if len(msg) == 4 && string(msg) == "pong" {
			continue
		}
		var m struct {
			Event string          `json:"event"`
			Code  string          `json:"code"`
			Msg   string          `json:"msg"`
			Arg   Arg             `json:"arg"`
			Data  json.RawMessage `json:"data"`
		}
		if json.Unmarshal(msg, &m) != nil {
			continue
		}
		if m.Event != "" {
			if m.Event == "error" {
				w.SubErrors.Add(1)
				log.Printf("ws %s: error %s %s", w.Name, m.Code, m.Msg)
			}
			continue
		}
		w.Messages.Add(1)
		if w.OnMessage != nil && len(m.Data) > 0 {
			w.OnMessage(Message{Arg: m.Arg, Data: m.Data, Received: now})
		}
	}
}

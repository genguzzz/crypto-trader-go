package strategyhost

import (
	"bufio"
	"encoding/json"
	"io"
	"math"
	"net"
	"sync"
	"time"

	"crypto-trader-go/internal/trade"
)

// maxLine bounds one NDJSON message in either direction.
const maxLine = 1 << 20

// maxState bounds the persisted ctx.state document.
const maxState = 64 << 10

// wire is one NDJSON connection. recv is used by a single reader goroutine; send is safe for
// concurrent use.
type wire struct {
	c   net.Conn
	sc  *bufio.Scanner
	wmu sync.Mutex
}

func newWire(c net.Conn) *wire {
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	return &wire{c: c, sc: sc}
}

// recv returns the next line; the slice is only valid until the next call.
func (w *wire) recv() ([]byte, error) {
	if !w.sc.Scan() {
		if err := w.sc.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return w.sc.Bytes(), nil
}

func (w *wire) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	w.wmu.Lock()
	defer w.wmu.Unlock()
	// A peer that stops reading must not block the host forever.
	_ = w.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = w.c.Write(b)
	return err
}

func (w *wire) close() { _ = w.c.Close() }

// inMsg is the union of every message a peer may send; unused fields stay zero.
type inMsg struct {
	Type string `json:"type"`
	Seq  int64  `json:"seq"`

	// instance → host
	QID    json.RawMessage `json:"qid"`
	Method string          `json:"method"`
	Args   json.RawMessage `json:"args"`
	Data   json.RawMessage `json:"data"`
	Level  string          `json:"level"`
	Msg    string          `json:"msg"`
	Error  string          `json:"error"`

	// hello
	Instance int64  `json:"instance"`
	Token    string `json:"token"`
	SDK      string `json:"sdk"`

	// supervisor → host
	ID         int64            `json:"id"`
	Code       int              `json:"code"`
	Stderr     string           `json:"stderr"`
	Strategies []describedClass `json:"strategies"`
	Result     *checkResult     `json:"result"`
}

type checkResult struct {
	Diagnostics []trade.Diagnostic `json:"diagnostics"`
	Strategies  []describedClass   `json:"strategies"`
}

type signalMsg struct {
	InstID        string       `json:"instId"`
	Action        trade.Action `json:"action"`
	Strength      float64      `json:"strength"`
	StopLoss      float64      `json:"stopLoss"`
	TakeProfit    float64      `json:"takeProfit"`
	MaxSlippageBp float64      `json:"maxSlippageBp"`
	Leverage      float64      `json:"leverage"`
	ReduceFrac    float64      `json:"reduceFrac"`
	TTLms         int64        `json:"ttlMs"`
	Reverse       bool         `json:"reverse"`
	TrailDist     float64      `json:"trailDist"`
	TimeLimitMs   int64        `json:"timeLimitMs"`
	Reason        string       `json:"reason"`
}

// notifyMsg is ctx.notify; statusMsg carries the strategy's status() line.
type notifyMsg struct {
	Level string         `json:"level"`
	Text  string         `json:"text"`
	Data  map[string]any `json:"data"`
}

// describedClass is one strategy class reported by the supervisor's describe.
type describedClass struct {
	File        string             `json:"file"`
	Class       string             `json:"class"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Timeframe   string             `json:"timeframe"`
	Params      []trade.ParamSpec  `json:"params"`
	Universe    trade.Universe     `json:"universe"`
	Meta        trade.StrategyMeta `json:"meta"`
	Code        string             `json:"code"`
	Error       string             `json:"error"`
}

// num maps NaN/Inf (not representable in JSON) to null.
func num(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return f
}

func numMap(m map[string]float64) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = num(v)
	}
	return out
}

func barJSON(b trade.Bar) map[string]any {
	return map[string]any{"ts": b.Ts, "o": num(b.O), "h": num(b.H), "l": num(b.L), "c": num(b.C),
		"vol": num(b.Vol), "volq": num(b.VolQ)}
}

func strs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

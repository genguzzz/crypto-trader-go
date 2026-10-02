//go:build pyintegration

package strategyhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crypto-trader-go/internal/trade"
)

// crossMD serves 4H bars that end on the bar where EMA(5) crosses above EMA(20).
type crossMD struct {
	fakeMD
	bars []trade.Bar
}

func (m crossMD) Bars(instID, tf string, n int) ([]trade.Bar, error) {
	if n > len(m.bars) {
		n = len(m.bars)
	}
	return m.bars[len(m.bars)-n:], nil
}

func ema(x []float64, n int) []float64 {
	out := make([]float64, len(x))
	alpha := 2 / float64(n+1)
	for i := range x {
		switch {
		case i < n-1:
		case i == n-1:
			s := 0.0
			for _, v := range x[:n] {
				s += v
			}
			out[i] = s / float64(n)
		default:
			out[i] = alpha*x[i] + (1-alpha)*out[i-1]
		}
	}
	return out
}

func crossBars(t *testing.T) []trade.Bar {
	var closes []float64
	for i := 0; i < 30; i++ {
		closes = append(closes, 100-0.5*float64(i))
	}
	for j := 0; j < 30; j++ {
		closes = append(closes, 85+2*float64(j))
	}
	f, s := ema(closes, 5), ema(closes, 20)
	for k := 20; k < len(closes); k++ {
		if f[k] > s[k] && f[k-1] <= s[k-1] {
			bars := make([]trade.Bar, k+1)
			for i := range bars {
				c := closes[i]
				bars[i] = trade.Bar{Ts: int64(i) * 4 * 3_600_000, O: c, H: c + 1, L: c - 1, C: c, Vol: 10, VolQ: 10 * c}
			}
			return bars
		}
	}
	t.Fatal("no crossover in the synthetic series")
	return nil
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	if line := strings.TrimRight(string(p), "\n"); line != "" {
		w.t.Log(line)
	}
	return len(p), nil
}

func TestPythonRunner(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	if exec.Command(py, "-c", "import numpy").Run() != nil {
		t.Skip("numpy not installed")
	}
	h, path := newHost(t, Options{EventTimeout: 10 * time.Second})
	runner, _ := filepath.Abs("../../strategies/runner.py")
	cmd := exec.Command(py, "-u", runner)
	cmd.Env = append(os.Environ(), "CTG_SOCKET="+path)
	cmd.Stderr = testWriter{t}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	deadline := time.Now().Add(15 * time.Second)
	for !h.Available() {
		if time.Now().After(deadline) {
			t.Fatal("supervisor did not connect")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ctx := context.Background()
	vs, err := h.Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ver trade.StrategyVersion
	for _, v := range vs {
		if v.Name == "majors-trend" {
			ver = v
		}
	}
	if ver.ID == "" || ver.Timeframe != "4H" || len(ver.DefaultUniverse.Static) != 2 || ver.Code == "" {
		t.Fatalf("majors-trend not described: %+v", vs)
	}

	bars := crossBars(t)
	md := crossMD{bars: bars}
	sink := &fakeSink{}
	inst := trade.Instance{ID: 42, AccountID: 1, Params: map[string]float64{"fast": 5, "slow": 20}}
	s, err := h.Start(ctx, inst, ver, trade.ModeBacktest, nil, md, sink)
	if err != nil {
		t.Fatal(err)
	}
	last := bars[len(bars)-1]
	if err := s.Bar(ctx, "ETH-USDT-SWAP", "4H", last); err != nil {
		t.Fatal(err)
	}
	if len(sink.sigs) != 1 {
		t.Fatalf("signals: %+v", sink.sigs)
	}
	sig := sink.sigs[0]
	if sig.Action != trade.OpenLong || sig.InstID != "ETH-USDT-SWAP" || !(sig.StopLoss < last.C && last.C < sig.TakeProfit) {
		t.Fatalf("signal: %+v", sig)
	}
	if !strings.Contains(string(s.State()), "reenter") {
		t.Fatalf("state: %s", s.State())
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	<-s.Done()
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
}

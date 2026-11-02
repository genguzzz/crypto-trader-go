package signals

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Script signals end to end with the real Python worker (ctg.condworker through runner.CondWorker) on
// real OKX history: common SDK conditions are validated at subscribe (compile + replay), then checked
// live minute by minute exactly as production schedules them, and must fire exactly where the
// worker's own replay of the same bars has its false→true edges (after cooldown and maxFires).
// Skipped when python3 with numpy is not available.

type pyBackend struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	in   *json.Encoder
	out  *bufio.Reader
	f    *liveFake
	bars map[string][]fixtureCandle
}

func startPyBackend(t *testing.T, f *liveFake) *pyBackend {
	t.Helper()
	if exec.Command("python3", "-c", "import numpy").Run() != nil {
		t.Skip("python3 with numpy not available")
	}
	cmd := exec.Command("python3", "-u", "testdata/condbridge.py", "../../strategies")
	cmd.Stderr = os.Stderr
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Skip("python3: ", err)
	}
	b := &pyBackend{cmd: cmd, in: json.NewEncoder(stdin), out: bufio.NewReaderSize(stdout, 1<<20), f: f}
	b.bars, _ = loadFixture(t)
	t.Cleanup(func() { stdin.Close(); cmd.Wait() })
	line, err := b.out.ReadBytes('\n')
	if err != nil || !strings.Contains(string(line), "ready") {
		t.Fatalf("bridge did not start: %v %s", err, line)
	}
	return b
}

func (b *pyBackend) Conditions(ctx context.Context, req any) (json.RawMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.in.Encode(req); err != nil {
		return nil, err
	}
	line, err := b.out.ReadBytes('\n')
	return json.RawMessage(line), err
}

// Bars aggregates the fixture's 1m candles into tf buckets (UTC-aligned) closed by the clock.
func (b *pyBackend) Bars(id, tf string, n int) ([]Candle, error) {
	step := barMs[tf]
	b.f.mu.Lock()
	now := b.f.now
	b.f.mu.Unlock()
	var out []Candle
	for _, c := range b.bars[id] {
		bucket := c.T / step * step
		if bucket+step > now {
			break
		}
		if k := len(out); k > 0 && out[k-1].Ts == bucket {
			x := &out[k-1]
			x.H, x.L, x.C = max(x.H, c.H), min(x.L, c.L), c.C
			x.Vol += c.Vol
			x.VQ += c.VQ
			continue
		}
		out = append(out, Candle{Ts: bucket, O: c.O, H: c.H, L: c.L, C: c.C, Vol: c.Vol, VQ: c.VQ})
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

// Common conditions people write. Each must fire at least once in the 10 hours checked live.
var commonScripts = []struct{ name, code string }{
	{"sma-cross", `from ctg import Condition, Fire, ta

class EnaSmaCross(Condition):
    """ENA 5m 收盘上穿 SMA20"""
    instruments = ["ENA-USDT-SWAP"]
    timeframe = "5m"
    lookback = 60

    def check(self, ctx, bars):
        c = bars["ENA-USDT-SWAP"].c
        m = ta.sma(c, 20)
        return Fire(c[-2] <= m[-2] and c[-1] > m[-1], values={"close": c[-1], "sma": m[-1]})
`},
	{"donchian-breakout", `from ctg import Condition, Fire, ta

class PepeBreakout(Condition):
    """PEPE 15m 收盘突破前 12 根高点"""
    instruments = ["PEPE-USDT-SWAP"]
    timeframe = "15m"
    lookback = 40

    def check(self, ctx, bars):
        b = bars["PEPE-USDT-SWAP"]
        hi = ta.highest(b.h, 12)
        return Fire(b.c[-1] > hi[-2], values={"close": b.c[-1], "high12": hi[-2]})
`},
	{"volume-spike", `from ctg import Condition, Fire

class EnaVolumeSpike(Condition):
    """ENA 5m 放量（成交额 > 前 20 根均值 2.5 倍）且收阳"""
    instruments = ["ENA-USDT-SWAP"]
    timeframe = "5m"
    lookback = 30

    def check(self, ctx, bars):
        b = bars["ENA-USDT-SWAP"]
        avg = b.volq[-21:-1].mean()
        return Fire(b.volq[-1] > 2.5 * avg and b.c[-1] > b.o[-1], values={"x": b.volq[-1] / avg})
`},
	{"relative-strength", `from ctg import Condition, Fire

class EnaVsBtc(Condition):
    """ENA 1 小时内跑赢 BTC 1.5% 以上"""
    instruments = ["ENA-USDT-SWAP", "BTC-USDT-SWAP"]
    timeframe = "15m"
    lookback = 20

    def check(self, ctx, bars):
        e, b = bars["ENA-USDT-SWAP"].c, bars["BTC-USDT-SWAP"].c
        d = (e[-1] / e[-5] - 1) - (b[-1] / b[-5] - 1)
        return Fire(d > 0.015, values={"excess": d * 100}, note=f"超额 {d*100:.2f}%")
`},
	{"rsi-reclaim", `from ctg import Condition, Fire, ta

class PepeRsiReclaim(Condition):
    """PEPE 5m RSI 从 35 以下收回"""
    instruments = ["PEPE-USDT-SWAP"]
    timeframe = "5m"
    lookback = 120

    def check(self, ctx, bars):
        r = ta.rsi(bars["PEPE-USDT-SWAP"].c, 14)
        return Fire(r[-2] < 35 <= r[-1], values={"rsi": r[-1]})
`},
	{"any-of-three-1m-jump", `from ctg import Condition, Fire

class Jump(Condition):
    """ENA / PEPE / SOL 任一 1m 涨 0.6% 以上"""
    instruments = ["ENA-USDT-SWAP", "PEPE-USDT-SWAP", "SOL-USDT-SWAP"]
    timeframe = "1m"
    lookback = 10

    def check(self, ctx, bars):
        best = max((bars[i].c[-1] / bars[i].c[-2] - 1, i) for i in self.instruments)
        return Fire(best[0] > 0.006, values={"pct": best[0] * 100}, note=best[1])
`},
}

func TestScriptsLiveMatchReplayRealWorker(t *testing.T) {
	_, from, to := fixtureHistory(t)
	for _, sc := range commonScripts {
		t.Run(sc.name, func(t *testing.T) {
			start := from + 12*60*60_000 // every script has its lookback of bars by then
			s, f, _ := newLiveEnv(t, start, Options{ScriptPerDay: 1000})
			py := startPyBackend(t, f)
			s.SetScripts(py)
			ctx := context.Background()
			// subscribe at the first minute where the condition does not hold
			var sub Subscription
			var err error
			for k := 0; k < 120; k++ {
				f.minuteAt(s, start)
				sub, err = s.SubscribeContext(ctx, SubscribeInput{ChatID: 12, Spec: Spec{Type: "script", Code: sc.code}, Intent: "脚本信号测试：触发后复核",
					ExpiresHours: 48, MaxFires: 5, CooldownMin: 5})
				if err == nil {
					break
				}
				if !strings.Contains(err.Error(), "holds") {
					t.Fatalf("subscribe: %v", err)
				}
				start += 60_000
			}
			if err != nil {
				t.Fatal(err)
			}
			info := sub.Script
			if info == nil || info.Replay == nil || !info.Replay.OK || info.Replay.Bars == 0 {
				t.Fatalf("subscribe replay %+v", info)
			}
			end := to - 60_000
			for m := start + 60_000; m <= end; m += 60_000 {
				f.minuteAt(s, m)
				if jobs := s.scriptsDue(m, false); len(jobs) > 0 {
					s.applyScripts(s.runScripts(ctx, jobs))
				}
			}
			got, _ := s.Get(sub.ID)
			// the worker's replay over the same bars: false→true edges after the subscription's close
			m := info.ScriptMeta
			step := barMs[m.Timeframe]
			bars := map[string][][7]float64{}
			for _, id := range m.InstIDs {
				cs, _ := py.Bars(id, m.Timeframe, 1<<20) // the whole history: the comparison spans more than 1000 1m bars
				bars[id] = rows(cs)
			}
			subClose := info.Replay.To
			var rep struct {
				Edges     []ReplayFire `json:"edges"`
				EdgeCount int          `json:"edgeCount"`
				Errors    int          `json:"errors"`
				States    string       `json:"states"`
			}
			if err := s.call(ctx, time.Minute, map[string]any{"op": "replay", "code": sc.code, "bars": bars, "start": subClose,
				"end": (end + 60_000) / step * step, "budgetMs": 1000, "maxTotalS": 60}, &rep); err != nil {
				t.Fatal(err)
			}
			if rep.Errors > 0 || !strings.HasPrefix(rep.States, "0") {
				t.Fatalf("replay from the subscribed close: errors %d states %.20s…", rep.Errors, rep.States)
			}
			var want []int64
			for _, e := range rep.Edges {
				if len(want) == 5 {
					break
				}
				if len(want) == 0 || e.T-want[len(want)-1] >= 5*60_000 {
					want = append(want, e.T)
				}
			}
			if len(want) == 0 {
				t.Fatalf("%s never fires in this history: pick a looser condition (states %s)", sc.name, rep.States)
			}
			if len(got.History) != len(want) {
				t.Fatalf("live fired %d times, replay edges give %d (%v)", len(got.History), len(want), want)
			}
			for i, fr := range got.History {
				if fr.Ts != want[i] {
					t.Fatalf("fire %d live at %s, replay edge at %s", i, fmtTime(fr.Ts), fmtTime(want[i]))
				}
				if fr.Delivery == nil || fr.Delivery.Type != "script" || !strings.Contains(fr.Matched, "收盘") {
					t.Fatalf("fire %d: %+v", i, fr)
				}
			}
			if info2 := got.Script; info2.Errors != 0 || info2.Checks == 0 {
				t.Fatalf("live checks %d errors %d (%s)", info2.Checks, info2.Errors, info2.LastError)
			}
			t.Logf("%s: %d live fires = replay edges; %d checks, avg %.2f ms", sc.name, len(want), got.Script.Checks, got.Script.AvgMs)
		})
	}
}

// Validation outcomes with the real worker: a broken script, one that raises, one that would fire too
// often, and one that holds now are rejected with the reason; nothing is stored.
func TestScriptValidationRealWorker(t *testing.T) {
	_, from, _ := fixtureHistory(t)
	s, f, _ := newLiveEnv(t, from+10*60*60_000, Options{})
	s.SetScripts(startPyBackend(t, f))
	ctx := context.Background()
	head := "from ctg import Condition, Fire, ta\n\nclass X(Condition):\n    \"\"\"测试\"\"\"\n    instruments = [\"ENA-USDT-SWAP\"]\n    timeframe = \"1m\"\n    lookback = 20\n\n    def check(self, ctx, bars):\n"
	for name, c := range map[string]struct{ body, problem string }{
		"compile": {"        return (", "compile"},
		"raises":  {"        return 1 / 0", "error"},
		"often":   {"        c = bars[\"ENA-USDT-SWAP\"].c\n        return c[-1] > c[-2]", "often"},
		"holds":   {"        return True", "holds"},
		"import":  {"        import os\n        return False", "compile"},
	} {
		got, err := s.TestScript(ctx, head+c.body+"\n")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.OK || got.Problem != c.problem {
			t.Errorf("%s: ok %v problem %q (%s), want %q", name, got.OK, got.Problem, got.Reason, c.problem)
		}
	}
	// lookback longer than the history: unknown everywhere, with the reason (not an IndexError)
	long := strings.Replace(head, "lookback = 20", "lookback = 500", 1) + "        c = bars[\"ENA-USDT-SWAP\"].c\n        return c[-1] > c[-400]\n"
	got, err := s.TestScript(ctx, strings.Replace(long, "timeframe = \"1m\"", "timeframe = \"15m\"", 1))
	if err != nil || got.OK || got.Problem != "unknown" || !strings.Contains(got.Reason, "lookback") {
		t.Fatalf("long lookback: %+v %v", got, err)
	}

	if subs, _ := s.List(ListQuery{Status: "all"}); len(subs) != 0 {
		t.Fatalf("test-script stored %d subscriptions", len(subs))
	}
}

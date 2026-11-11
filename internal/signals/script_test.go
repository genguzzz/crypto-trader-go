package signals

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crypto-trader-go/internal/store"
)

// fakeScripts is the strategy host's script worker and the bar history. A script's code is "# " + its
// declarations as JSON, so each test writes the meta it needs; replays and evals answer what the test set.
type fakeScripts struct {
	mu     sync.Mutex
	f      *fake
	down   error                                   // Conditions fails (runner not connected)
	block  bool                                    // Conditions waits for ctx (a hung runner)
	replay func(req map[string]any) map[string]any // nil: a quiet week that does not hold now
	eval   map[int64]map[string]any                // results by subscription id (default: false)
	evals  []map[string]any                        // eval requests seen
	noBars bool                                    // Bars has nothing
	stale  time.Duration                           // the newest bar is this much older
	newer  bool                                    // Bars returns one bar past the close asked about
}

func code(meta string) string { return "# " + meta }

const rsiMeta = `{"class":"AaveRsi","desc":"AAVE RSI 跌破 30","instIds":["AAVE-USDT-SWAP"],"timeframe":"15m","lookback":50,"hash":"h1"}`

func (x *fakeScripts) Conditions(ctx context.Context, req any) (json.RawMessage, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.down != nil {
		return nil, x.down
	}
	if x.block {
		x.mu.Unlock()
		<-ctx.Done()
		x.mu.Lock()
		return nil, ctx.Err()
	}
	b, _ := json.Marshal(req) // what really travels
	var m map[string]any
	json.Unmarshal(b, &m)
	var out any
	switch m["op"] {
	case "check":
		c := m["code"].(string)
		var meta map[string]any
		if !strings.HasPrefix(c, "# ") || json.Unmarshal([]byte(c[2:]), &meta) != nil {
			out = map[string]any{"ok": false, "diagnostics": []map[string]any{{"line": 3, "col": 0, "severity": "error", "message": "语法错误：invalid syntax"}}}
			break
		}
		out = map[string]any{"ok": true, "diagnostics": []any{}, "meta": meta}
	case "replay":
		if x.replay != nil {
			out = x.replay(m)
			break
		}
		end := int64(m["end"].(float64))
		out = map[string]any{"states": strings.Repeat("0", 665) + "0001100", "edges": []map[string]any{{"t": end - 3*900_000, "values": map[string]float64{"rsi": 28.5}, "note": "RSI 28.5"}},
			"edgeCount": 1, "errors": 0, "unknown": 0, "avgMs": 0.4, "maxMs": 1.2, "last": map[string]any{"t": end, "fire": false, "values": map[string]float64{"rsi": 41.2}, "note": "RSI 41.2"}}
	case "eval":
		items := m["items"].([]any)
		x.evals = append(x.evals, m)
		var res []map[string]any
		for i, it := range items {
			key := int64(it.(map[string]any)["key"].(float64))
			r := map[string]any{"i": i, "key": key, "fire": false, "values": map[string]float64{"rsi": 40}, "note": "RSI 40", "ms": 0.5}
			for k, v := range x.eval[key] {
				r[k] = v
			}
			res = append(res, r)
		}
		out = map[string]any{"items": res}
	}
	return json.Marshal(out)
}

func (x *fakeScripts) Bars(id, tf string, n int) ([]Candle, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.noBars {
		return nil, errors.New("no bars")
	}
	step := barMs[tf]
	end := (x.f.clock().UnixMilli()-x.stale.Milliseconds())/step*step - step // open of the newest closed bar
	if x.newer {
		end += step
	}
	out := make([]Candle, n)
	for i := range out {
		out[i] = Candle{Ts: end - int64(n-1-i)*step, O: 100, H: 101, L: 99, C: 100, Vol: 1, VQ: 100}
	}
	return out, nil
}

func (x *fakeScripts) setEval(id int64, r map[string]any) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.eval == nil {
		x.eval = map[int64]map[string]any{}
	}
	x.eval[id] = r
}

func scriptEnv(t *testing.T, opts Options) (*Service, *fake, *fakeScripts) {
	s, f, _ := newEnv(t, opts)
	x := &fakeScripts{f: f}
	s.SetScripts(x)
	return s, f, x
}

func scriptSub(t *testing.T, s *Service, chat int64, specRaw, meta string, mod ...func(*SubscribeInput)) Subscription {
	t.Helper()
	sp := spec(t, specRaw)
	sp.fillCode(code(meta))
	in := SubscribeInput{ChatID: chat, Spec: sp, Intent: "RSI 收回 30 再评估做多 AAVE"}
	for _, m := range mod {
		m(&in)
	}
	x, err := s.Subscribe(in)
	if err != nil {
		t.Fatalf("subscribe %s: %v", specRaw, err)
	}
	return x
}

// minuteClosing is the minute event (open of the confirmed 1m bar) whose close is h:m UTC on the fake's day.
func minuteClosing(h, m int) int64 {
	return time.Date(2026, 11, 1, h, m, 0, 0, time.UTC).UnixMilli() - 60_000
}

// Validation compiles, replays and judges the script; the subscription keeps the declarations and the replay.
func TestScriptSubscribeValidates(t *testing.T) {
	s, f, x := scriptEnv(t, Options{})
	sub := scriptSub(t, s, 12, `{"type":"script"}`, rsiMeta)
	if sub.Desc != "脚本 15m · AAVE RSI 跌破 30" || len(sub.InstIDs) != 1 || sub.InstIDs[0] != aave || f.kept[0] != aave {
		t.Fatalf("subscription %+v kept %v", sub, f.kept)
	}
	sc := sub.Script
	if sc == nil || sc.Timeframe != "15m" || sc.Lookback != 50 || !strings.HasPrefix(sc.Code, "# ") || sc.Replay == nil {
		t.Fatalf("script info %+v", sc)
	}
	r := sc.Replay
	if !r.OK || r.FireCount != 1 || r.Bars != 672 || r.Days != 7 || !strings.HasSuffix(r.States, "0001100") || len(r.Fires) != 1 || r.Fires[0].Note != "RSI 28.5" ||
		r.To != f.now.UnixMilli() || r.From != r.To-int64(672-1)*900_000 || r.Now == nil || *r.Now.Fire {
		t.Fatalf("replay %+v", r)
	}
	if sub.Progress == nil || !strings.Contains(sub.Progress.Text, "15m 收盘") || !strings.Contains(sub.Progress.Text, "未成立 · RSI 41.2") {
		t.Fatalf("progress %+v", sub.Progress)
	}
	if sub.Spec.Script == nil || sub.Spec.Script.Hash != "h1" {
		t.Fatalf("spec meta %+v", sub.Spec)
	}

	// the replay asks for 7 days of 15m closes after the lookback
	x.replay = func(m map[string]any) map[string]any {
		bars := m["bars"].(map[string]any)[aave].([]any)
		if len(bars) != 672+50-1 {
			t.Errorf("replay bars %d", len(bars))
		}
		return map[string]any{"states": "0", "edges": []any{}, "last": map[string]any{"t": m["end"], "fire": false}}
	}
	scriptSub(t, s, 12, `{"type":"script"}`, rsiMeta)

	bad := []struct {
		name, spec, meta, every, want string
		setup                         func()
	}{
		{"compile", `{"type":"script"}`, "", "", "script rejected (compile): line 3: 语法错误", nil},
		{"unknown inst", `{"type":"script"}`, `{"instIds":["NOPE-USDT-SWAP"],"timeframe":"15m","lookback":50,"desc":"x"}`, "", "unknown instrument NOPE", nil},
		{"tick", `{"type":"script"}`, rsiMeta, "tick", "every tick is not allowed", nil},
		{"two scripts", `{"type":"any","of":[{"type":"script"},{"type":"script"}]}`, rsiMeta, "", "at most one script", nil},
		{"script with instId", `{"type":"script","instId":"AAVE"}`, rsiMeta, "", "script takes only code", nil},
		{"errors", `{"type":"script"}`, rsiMeta, "", "(error): the script raised on 2 of 5 closes, first at 11-01 20:00: ZeroDivisionError", func() {
			x.replay = func(map[string]any) map[string]any {
				return map[string]any{"states": "0--00", "errors": 2, "unknown": 2, "firstError": map[string]any{"t": f.now.UnixMilli(), "error": "ZeroDivisionError: x（第 9 行）"},
					"last": map[string]any{"t": f.now.UnixMilli(), "fire": false}}
			}
		}},
		{"timeouts", `{"type":"script"}`, rsiMeta, "", "(slow): checks ran out of the 200 ms CPU budget on 1 of 3 closes", func() {
			x.replay = func(map[string]any) map[string]any {
				return map[string]any{"states": "0-0", "errors": 1, "timeouts": 1, "unknown": 1, "firstError": map[string]any{"t": 1, "error": "超出 CPU 时间预算 200 ms"},
					"last": map[string]any{"t": f.now.UnixMilli(), "fire": false}}
			}
		}},
		{"slow", `{"type":"script"}`, rsiMeta, "", "(slow): a check takes 61.0 ms on average; the cap is 50 ms", func() {
			x.replay = func(map[string]any) map[string]any {
				return map[string]any{"states": "000", "avgMs": 61, "last": map[string]any{"t": f.now.UnixMilli(), "fire": false}}
			}
		}},
		{"often", `{"type":"script"}`, rsiMeta, "", "(often): it would have fired 50 times in 7.0 days (7.1 a day; the cap is 6)", func() {
			x.replay = func(map[string]any) map[string]any {
				return map[string]any{"states": strings.Repeat("0", 672), "edgeCount": 50, "last": map[string]any{"t": f.now.UnixMilli(), "fire": false}}
			}
		}},
		{"holds now", `{"type":"script"}`, rsiMeta, "", "(holds): the condition already holds on the newest close (11-01 20:00: RSI 28 · rsi=28)", func() {
			x.replay = func(map[string]any) map[string]any {
				return map[string]any{"states": strings.Repeat("0", 671) + "1", "edgeCount": 1, "last": map[string]any{"t": f.now.UnixMilli(), "fire": true, "note": "RSI 28", "values": map[string]float64{"rsi": 28}}}
			}
		}},
		{"unknown now", `{"type":"script"}`, rsiMeta, "", "(unknown): the script has no value on the newest close (11-01 20:00): bars missing for AAVE-USDT-SWAP", func() {
			x.replay = func(map[string]any) map[string]any {
				return map[string]any{"states": "00-", "unknown": 1, "last": map[string]any{"t": f.now.UnixMilli(), "fire": nil, "missing": []string{aave}}}
			}
		}},
		{"always None", `{"type":"script"}`, rsiMeta, "", "(unknown): the script returned no value (None, or bars missing) on every close", func() {
			x.replay = func(map[string]any) map[string]any {
				return map[string]any{"states": "---", "unknown": 3, "last": map[string]any{"t": f.now.UnixMilli(), "fire": nil}}
			}
		}},
		{"no bars", `{"type":"script"}`, rsiMeta, "", "(nodata): no 15m bars for AAVE-USDT-SWAP", func() { x.replay, x.noBars = nil, true }},
		{"stale bars", `{"type":"script"}`, rsiMeta, "", "(nodata): the newest 15m bar of every instrument closed at 11-01 19:00", func() { x.noBars, x.stale = false, time.Hour }},
	}
	for _, c := range bad {
		if c.setup != nil {
			c.setup()
		}
		sp := spec(t, c.spec)
		if c.meta != "" {
			sp.fillCode(code(c.meta))
		} else {
			sp.fillCode("def broken(:")
		}
		_, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: sp, Intent: "RSI 收回 30 再评估做多", Every: c.every})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: spec(t, `{"type":"script"}`), Intent: "RSI 收回 30 再评估做多"}); err == nil || !strings.Contains(err.Error(), "code is required") {
		t.Fatalf("no code: %v", err)
	}
	s.SetScripts(nil)
	sp := spec(t, `{"type":"script"}`)
	sp.fillCode(code(rsiMeta))
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: sp, Intent: "RSI 收回 30 再评估做多"}); !errors.Is(err, ErrNoScripts) {
		t.Fatalf("no runner: %v", err)
	}
	s.SetScripts(&fakeScripts{f: f, down: errors.New("strategy supervisor not connected")})
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: sp, Intent: "RSI 收回 30 再评估做多"}); err == nil || !strings.Contains(err.Error(), "script runner: strategy supervisor not connected") {
		t.Fatalf("runner down: %v", err)
	}
}

func TestScriptCap(t *testing.T) {
	s, _, _ := scriptEnv(t, Options{MaxActive: 50})
	for i := range MaxScripts {
		scriptSub(t, s, int64(12+i%2), `{"type":"script"}`, rsiMeta, func(in *SubscribeInput) { in.ChatID = 0 })
	}
	sp := spec(t, `{"type":"script"}`)
	sp.fillCode(code(rsiMeta))
	if _, err := s.Subscribe(SubscribeInput{Spec: sp, Intent: "RSI 收回 30 再评估做多"}); err == nil || !strings.Contains(err.Error(), "10 script signals are active") {
		t.Fatalf("cap: %v", err)
	}
	if c := s.Caps(); c.Scripts.ActiveScripts != MaxScripts || c.Scripts.BudgetMs != DefaultScriptMs || c.Scripts.MaxFiresDay != 6 {
		t.Fatalf("caps %+v", c.Scripts)
	}
}

// Each confirmed minute sends one batch with the scripts whose bar closes then, respecting every as a
// minimum interval; their leaves are unknown until the answer arrives.
func TestScriptCadence(t *testing.T) {
	s, f, _ := scriptEnv(t, Options{})
	a := scriptSub(t, s, 12, `{"type":"script"}`, rsiMeta) // 15m, checked each minute
	b := scriptSub(t, s, 12, `{"type":"script"}`, strings.Replace(rsiMeta, `"15m"`, `"5m"`, 1), func(in *SubscribeInput) { in.Every = "15m" })
	c := scriptSub(t, s, 12, `{"type":"script"}`, strings.Replace(rsiMeta, `"15m"`, `"1H"`, 1), func(in *SubscribeInput) { in.Every = "5m" })
	f.vals[aave] = map[string]float64{"funding_hourly": -0.0001}
	sub(t, s, 12, `{"type":"field","instId":"AAVE","field":"funding_hourly","op":">","value":0}`)
	ids := func(js []scriptJob) (out []int64) {
		for _, j := range js {
			out = append(out, j.id)
		}
		return out
	}
	cases := []struct {
		h, m int
		want []int64
	}{
		{6, 5, nil},                       // 5m closes, but b checks every 15m
		{6, 15, []int64{a.ID, b.ID}},      // a 15m close; b's 5m bar closes too
		{6, 50, nil},                      //
		{7, 0, []int64{a.ID, b.ID, c.ID}}, // the hour
	}
	for _, x := range cases {
		got := ids(s.scriptsDue(minuteClosing(x.h, x.m), false))
		if len(got) != len(x.want) || (len(got) > 0 && !equalIDs(got, x.want)) {
			t.Errorf("%02d:%02d: due %v, want %v", x.h, x.m, got, x.want)
		}
	}
	js := s.scriptsDue(minuteClosing(7, 0), false)
	if js[0].close != time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("close %d", js[0].close)
	}
	if n := len(s.scriptsDue(minuteClosing(13, 1), true)); n != 3 { // after the closes their validation checked
		t.Fatalf("catch-up lists %d", n)
	}
	// a's leaf is unknown from its close until the answer: no evaluation in between can fire it
	s.evaluate(event{kind: "minute", minute: minuteClosing(7, 0)})
	s.mu.Lock()
	la := s.live[a.ID]
	s.mu.Unlock()
	if la.root.val != unknown {
		t.Fatalf("leaf after its close %v", la.root.val)
	}
	if desc := status(t, s, b.ID).Desc; desc != "脚本 5m · AAVE RSI 跌破 30（每 15 分钟检查）" {
		t.Fatalf("desc %q", desc)
	}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The batch carries each script's code, the bars up to its close (a newer bar is cut), finite factors;
// a result that turns true fires once, and the cooldown holds like for any other type.
func TestScriptBatchFiresOnEdge(t *testing.T) {
	s, f, x := scriptEnv(t, Options{})
	sc := scriptSub(t, s, 12, `{"type":"script"}`, rsiMeta, func(in *SubscribeInput) { in.MaxFires, in.CooldownMin = 2, 30 })
	f.vals[aave] = map[string]float64{"funding_hourly": 0.0001, "basis": math.NaN()}
	x.newer = true
	run := func(fire any, note string) {
		t.Helper()
		x.setEval(sc.ID, map[string]any{"fire": fire, "note": note, "values": map[string]float64{"rsi": 31}})
		m := f.clock().UnixMilli()/900_000*900_000 - 60_000
		jobs := s.scriptsDue(m, false)
		if len(jobs) != 1 {
			t.Fatalf("due %d", len(jobs))
		}
		s.evaluate(event{kind: "minute", minute: m})
		s.applyScripts(s.runScripts(context.Background(), jobs))
	}
	run(false, "RSI 29")
	req := x.evals[0]
	it := req["items"].([]any)[0].(map[string]any)
	bars := it["bars"].(map[string]any)[aave].([]any)
	last := bars[len(bars)-1].([]any)
	if req["budgetMs"].(float64) != 200 || it["code"] != code(rsiMeta) || len(bars) != 50 || int64(last[0].(float64))+900_000 != int64(it["now"].(float64)) {
		t.Fatalf("request %v bars %d last %v", req["budgetMs"], len(bars), last)
	}
	if fs := it["factors"].(map[string]any)[aave].(map[string]any); len(fs) != 1 || fs["funding_hourly"] != 0.0001 {
		t.Fatalf("factors %v", fs)
	}
	f.advance(15 * time.Minute)
	run(true, "RSI 收回 31")
	if f.wokenN() != 1 || !strings.Contains(f.woken[0], "触发值：脚本 15m · AAVE RSI 跌破 30：15m 收盘") || !strings.Contains(f.woken[0], "RSI 收回 31 · rsi=31") {
		t.Fatalf("woken %v", f.woken)
	}
	f.advance(15 * time.Minute)
	run(false, "")
	f.advance(10 * time.Minute)
	run(true, "") // within the 30 min cooldown
	if f.wokenN() != 1 {
		t.Fatalf("cooldown: woken %d", f.wokenN())
	}
	f.advance(15 * time.Minute)
	run(false, "")
	f.advance(15 * time.Minute)
	run(true, "")
	got := status(t, s, sc.ID)
	if f.wokenN() != 2 || got.Status != StatusFired || got.Script.Checks != 6 || got.Script.Last == nil || !*got.Script.Last.Fire {
		t.Fatalf("second fire: woken %d %+v", f.wokenN(), got.Script)
	}
}

// Script errors count; N in a row end the subscription with a push. Missing data and a runner that is
// down are not the script's fault: they are recorded, not counted, and a long outage is pushed once.
func TestScriptErrorsEndSubscription(t *testing.T) {
	s, f, x := scriptEnv(t, Options{PublicURL: "https://ctg.example"})
	sc := scriptSub(t, s, 12, `{"type":"script"}`, rsiMeta)
	step := func(r map[string]any) {
		x.setEval(sc.ID, r)
		f.advance(15 * time.Minute)
		s.applyScripts(s.runScripts(context.Background(), s.scriptsDue(f.clock().UnixMilli()/900_000*900_000-60_000, false)))
	}
	step(map[string]any{"fire": nil, "error": "ValueError: x"})
	step(map[string]any{"fire": nil, "error": "ValueError: x"})
	step(map[string]any{"fire": false})
	if got := status(t, s, sc.ID); got.Script.Errors != 0 || got.Script.LastError != "ValueError: x" || got.Status != StatusActive {
		t.Fatalf("reset %+v", got.Script)
	}
	step(map[string]any{"fire": nil, "missing": []string{aave}})
	if got := status(t, s, sc.ID); got.Script.Errors != 0 || !strings.Contains(got.Progress.Text, "缺少 K 线：AAVE") {
		t.Fatalf("missing %+v %+v", got.Script, got.Progress)
	}
	x.down = errors.New("strategy supervisor not connected")
	for range 3 {
		step(nil)
	}
	got := status(t, s, sc.ID)
	if got.Status != StatusActive || got.Script.Errors != 0 || !strings.Contains(got.Script.LastError, "脚本未运行：script runner: strategy supervisor not connected") {
		t.Fatalf("infra %+v", got.Script)
	}
	f.mu.Lock()
	n := len(f.notes)
	f.mu.Unlock()
	if n != 1 || !strings.Contains(f.notes[0], "信号脚本已连续 30 分钟无法运行") {
		t.Fatalf("infra notice %v", f.notes)
	}
	x.down = nil
	step(map[string]any{"fire": nil, "error": "脚本让执行进程超时（2 秒无响应），已强制结束", "crash": true})
	step(map[string]any{"fire": nil, "error": "ValueError: y"})
	step(map[string]any{"fire": nil, "error": "ValueError: z（第 9 行）"})
	got = status(t, s, sc.ID)
	if got.Status != StatusCancelled || got.EndedBy != "system" || got.Reason != "脚本连续 3 次出错，已停止：ValueError: z（第 9 行）" {
		t.Fatalf("ended %+v", got)
	}
	if len(f.notes) != 2 || !strings.Contains(f.notes[1], "信号 #1 已停止：脚本 15m · AAVE RSI 跌破 30") || !strings.HasSuffix(f.notes[1], "|https://ctg.example/go/chat/12") {
		t.Fatalf("end notice %v", f.notes)
	}
	if len(s.scriptsDue(f.clock().UnixMilli()/900_000*900_000-60_000, true)) != 0 {
		t.Fatal("an ended script is still scheduled")
	}
}

// A runner that does not answer in time is an outage, not a script error.
func TestScriptBatchTimeout(t *testing.T) {
	s, f, x := scriptEnv(t, Options{})
	sc := scriptSub(t, s, 12, `{"type":"script"}`, rsiMeta)
	f.advance(15 * time.Minute)
	m := f.clock().UnixMilli() - 60_000 // closes the next 15m bar
	x.block = true
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	b := s.runScripts(ctx, s.scriptsDue(m, false))
	if !strings.Contains(b.infra, "deadline exceeded") || len(b.results) != 0 {
		t.Fatalf("batch %+v", b)
	}
	s.applyScripts(b)
	if got := status(t, s, sc.ID); got.Script.Errors != 0 || got.Status != StatusActive {
		t.Fatalf("after timeout %+v", got.Script)
	}
	// startScripts skips a batch while one runs, and the event loop gets the result
	x.block = false
	s.scriptBusy.Store(true)
	s.startScripts(context.Background(), s.scriptsDue(m, false))
	select {
	case <-s.scriptRes:
		t.Fatal("ran while busy")
	case <-time.After(50 * time.Millisecond):
	}
	s.scriptBusy.Store(false)
	s.startScripts(context.Background(), s.scriptsDue(m, false))
	select {
	case b := <-s.scriptRes:
		if len(b.results) != 1 {
			t.Fatalf("results %+v", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no batch result")
	}
}

// A script inside all / any combines with built-in leaves; each keeps its own cadence.
func TestScriptInComposites(t *testing.T) {
	s, f, x := scriptEnv(t, Options{})
	f.bars[aave+"|15m"] = Bar{T: 0, C: 139}
	all := scriptSub(t, s, 12, `{"type":"all","of":[{"type":"price","instId":"AAVE","op":"above","level":140,"bar":"15m"},{"type":"script"}]}`, rsiMeta)
	anyx := scriptSub(t, s, 13, `{"type":"any","of":[{"type":"time","afterMin":600},{"type":"script"}]}`, rsiMeta)
	if all.Desc != "AAVE 15m 收盘站上 140 且 脚本 15m · AAVE RSI 跌破 30" || all.Spec.interval() != "1m" {
		t.Fatalf("desc %q interval %s", all.Desc, all.Spec.interval())
	}
	if anyx.Spec.interval() != "1m" {
		t.Fatal("a script subscription must not run on ticks")
	}
	m := f.clock().UnixMilli()/900_000*900_000 + 15*60_000 - 60_000 // the next 15m close
	f.advance(15 * time.Minute)
	x.setEval(all.ID, map[string]any{"fire": true, "note": "RSI 31"})
	x.setEval(anyx.ID, map[string]any{"fire": true, "note": "RSI 31"})
	s.evaluate(event{kind: "minute", minute: m}) // price still below: all stays false
	s.applyScripts(s.runScripts(context.Background(), s.scriptsDue(m, false)))
	if f.wokenN() != 1 || !strings.Contains(f.woken[0], "#2") {
		t.Fatalf("any should fire alone: %v", f.woken)
	}
	f.bars[aave+"|15m"] = Bar{T: m - 14*60_000 + 900_000, C: 141}
	m += 15 * 60_000
	f.advance(15 * time.Minute)
	s.evaluate(event{kind: "minute", minute: m}) // the price closes above while the script's new answer is pending
	if f.wokenN() != 1 {
		t.Fatal("fired before the script answered for this close")
	}
	s.applyScripts(s.runScripts(context.Background(), s.scriptsDue(m, false)))
	if f.wokenN() != 2 || !strings.Contains(f.woken[1], "15m 收盘站上 140：15m 收盘 141") || !strings.Contains(f.woken[1], "RSI 31") {
		t.Fatalf("all: %v", f.woken)
	}
}

// A restart reloads the script state (consecutive errors included) and the catch-up batch fires a
// script that turned true while the server was down.
func TestScriptRestart(t *testing.T) {
	s, f, x := scriptEnv(t, Options{})
	sc := scriptSub(t, s, 12, `{"type":"script"}`, rsiMeta)
	x.setEval(sc.ID, map[string]any{"fire": nil, "error": "ValueError: x"})
	if n := len(s.scriptsDue(f.clock().UnixMilli()-120_000, true)); n != 0 {
		t.Fatalf("the close validation just checked is due again: %d", n)
	}
	f.advance(15 * time.Minute)
	s.applyScripts(s.runScripts(context.Background(), s.scriptsDue(f.clock().UnixMilli()-60_000, false)))
	f.advance(17 * time.Minute) // down over the 12:30 close
	s2, err := New(s.st, f, Options{Now: f.clock, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	s2.SetWaker(f)
	s2.SetScripts(x)
	if l := s2.live[sc.ID]; l == nil || l.script == nil || l.script.Errors != 1 || l.sub.state != no {
		t.Fatalf("reloaded %+v", l)
	}
	x.setEval(sc.ID, map[string]any{"fire": true, "note": "RSI 31"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s2.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for f.wokenN() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	got := status(t, s2, sc.ID)
	if f.wokenN() != 1 || got.Script.Errors != 0 || got.Script.Last.At != time.Date(2026, 11, 1, 12, 30, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("catch-up woken %d %+v", f.wokenN(), got.Script.Last)
	}
	// a later restart does not re-check that close or an older one
	if n := len(s2.scriptsDue(got.Script.Last.At-60_000, true)); n != 0 {
		t.Fatalf("re-checks an old close: %d", n)
	}
	if n := len(s2.scriptsDue(got.Script.Last.At+15*60_000-60_000, true)); n != 0 {
		t.Fatal("the fired subscription is still scheduled")
	}
}

func TestScriptHTTPAndTools(t *testing.T) {
	s, _, x := scriptEnv(t, Options{})
	mux := http.NewServeMux()
	s.Register(mux)
	do := func(method, path, body string) (int, string) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	body, _ := json.Marshal(map[string]any{"code": code(rsiMeta)})
	code0, out := do("POST", "/api/v1/signals/test-script", string(body))
	if code0 != 200 || !strings.Contains(out, `"ok":true`) || !strings.Contains(out, `"fireCount":1`) || !strings.Contains(out, `"maxFiresPerDay":6`) {
		t.Fatalf("test %d %s", code0, out)
	}
	if code0, out = do("POST", "/api/v1/signals/test-script", `{"code":"def x(:"}`); code0 != 200 || !strings.Contains(out, `"problem":"compile"`) || !strings.Contains(out, `"line":3`) {
		t.Fatalf("test compile %d %s", code0, out)
	}
	if code0, out = do("GET", "/api/v1/signals/script-template", ""); code0 != 200 || !strings.Contains(out, "class MySignal(Condition)") {
		t.Fatalf("template %d %s", code0, out)
	}
	body, _ = json.Marshal(map[string]any{"code": code(rsiMeta), "intent": "RSI 收回 30 提醒我看多", "expiresHours": 12, "every": "15m"})
	if code0, out = do("POST", "/api/v1/signals", string(body)); code0 != 200 || !strings.Contains(out, `"type":"script"`) || !strings.Contains(out, `"replay":{`) {
		t.Fatalf("create %d %s", code0, out)
	}
	if code0, out = do("GET", "/api/v1/signals?status=active", ""); code0 != 200 || !strings.Contains(out, `"script":{"class":"AaveRsi"`) || !strings.Contains(out, `"code":"# {`) {
		t.Fatalf("list %d %s", code0, out)
	}
	x.down = errors.New("strategy supervisor not connected")
	if code0, _ = do("POST", "/api/v1/signals/test-script", `{"code":"x"}`); code0 != 503 {
		t.Fatalf("runner down %d", code0)
	}
	x.down = nil

	ctx := context.Background()
	res, err := s.TestScriptTool(ctx, 12, map[string]string{"code": code(rsiMeta)})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	rep := m["replay"].(map[string]any)
	if m["ok"] != true || rep["fires"] != 1 || len(rep["lastFires"].([]map[string]any)) != 1 || rep["holdsShare"] != "0%" || !strings.Contains(m["next"].(string), "subscribe_signal") {
		t.Fatalf("tool %v", m)
	}
	x.replay = func(map[string]any) map[string]any {
		return map[string]any{"states": strings.Repeat("0", 671) + "1", "edgeCount": 1, "last": map[string]any{"t": 1, "fire": true}}
	}
	res, _ = s.TestScriptTool(ctx, 12, map[string]string{"code": code(rsiMeta)})
	if m = res.(map[string]any); m["ok"] != false || m["problem"] != "holds" {
		t.Fatalf("tool holds %v", m)
	}
	x.replay = nil
	out1, err := s.SubscribeTool(ctx, 12, map[string]string{"spec": `{"type":"script"}`, "code": code(rsiMeta), "intent": "RSI 收回 30 再评估做多", "every": "15m"})
	if err != nil {
		t.Fatal(err)
	}
	if out1.(map[string]any)["script"].(map[string]any)["replay"] != "1 fires in 7.0 days" {
		t.Fatalf("subscribe tool %v", out1)
	}
	if _, err := s.SubscribeTool(ctx, 12, map[string]string{"code": code(rsiMeta), "intent": "RSI 收回 30 再评估做多"}); err != nil {
		t.Fatalf("code without spec: %v", err)
	}
	list, _ := s.ListTool(ctx, 12, map[string]string{})
	if rows := list.(map[string]any)["signals"].([]map[string]any); len(rows) != 2 || rows[0]["script"] == nil {
		t.Fatalf("list tool %v", rows)
	}
}

// A database from before script signals gets the column and keeps its subscriptions.
func TestScriptColumnMigration(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	if _, err := st.DB.Exec(`CREATE TABLE signal_subs(id INTEGER PRIMARY KEY AUTOINCREMENT, created INTEGER NOT NULL, updated INTEGER NOT NULL,
  kind TEXT NOT NULL, chat_id INTEGER NOT NULL DEFAULT 0, label TEXT NOT NULL DEFAULT '', inst_ids TEXT NOT NULL DEFAULT '',
  spec TEXT NOT NULL, descr TEXT NOT NULL, intent TEXT NOT NULL DEFAULT '', plan_id INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL, max_fires INTEGER NOT NULL DEFAULT 1, cooldown_min INTEGER NOT NULL DEFAULT 60,
  fires INTEGER NOT NULL DEFAULT 0, last_fired_at INTEGER NOT NULL DEFAULT 0, state INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL, status_at INTEGER NOT NULL, ended_by TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '');
INSERT INTO signal_subs(created, updated, kind, spec, descr, expires_at, status, status_at)
  VALUES(1, 1, 'owner', '{"type":"time","atMs":9999999999999}', '到 x', 9999999999999, 'active', 1);`); err != nil {
		t.Fatal(err)
	}
	f := &fake{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), px: map[string]float64{}}
	s, err := New(st, f, Options{Now: f.clock, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if got := status(t, s, 1); got.Status != StatusActive || got.Script != nil || len(s.live) != 1 {
		t.Fatalf("migrated %+v", got)
	}
}

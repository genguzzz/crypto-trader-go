package interact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crypto-trader-go/internal/trade"
)

func ev(kind, ref string) trade.Event {
	return trade.Event{Kind: kind, AccountID: 1, Account: "主账户", Mode: trade.ModeLive, Title: "开多成交",
		Lines: []string{"BTC-USDT-SWAP  12 张 @ 61,250", "手续费 0.37 USDT"}, Ts: 1_790_000_000_000, Ref: ref}
}

func configured(t *testing.T, h *Hub, allow ...int64) {
	t.Helper()
	if _, err := h.SaveConfig(tgView(true, testToken, allow...)); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyOrderFormatAndKinds(t *testing.T) {
	f, logs := newFakeTG(t), &logBuf{}
	h := newHub(t, openStore(t), f, logs)
	configured(t, h, 42)
	run(t, h)

	e1 := ev("fill", "ord-1")
	e1.Title = "开多成交 <BTC> & co"
	h.Notify(e1)
	h.Notify(ev("stop", "ord-2"))
	h.Notify(ev("signal", "sig-3"))
	eventually(t, "3 messages", func() bool { return len(f.sentMsgs()) == 3 })
	ms := f.sentMsgs()
	for i, ref := range []string{"ord-1", "ord-2", "sig-3"} {
		lines := strings.Split(ms[i].Text, "\n")
		if ms[i].Chat != 42 || !strings.HasSuffix(lines[len(lines)-1], "UTC+8 · "+ref) || len(lines) > maxLines {
			t.Fatalf("message %d: %+v", i, ms[i])
		}
	}
	if !strings.HasPrefix(ms[0].Text, "<b>🟢 [模拟·主账户] 开多成交 &lt;BTC&gt; &amp; co</b>\n") {
		t.Fatalf("header not escaped/marked: %q", ms[0].Text)
	}
	if !strings.HasPrefix(ms[1].Text, "<b>🔴 ") || !strings.HasPrefix(ms[2].Text, "<b>⚪ ") {
		t.Fatalf("markers: %q / %q", ms[1].Text, ms[2].Text)
	}

	// kind filter + quiet mode (no restart of the bot needed)
	v := tgView(true, Mask(testToken), 42)
	v.Kinds = []string{"fill", "liquidation"}
	v.Quiet = true
	if _, err := h.SaveConfig(v); err != nil {
		t.Fatal(err)
	}
	h.Notify(ev("signal", "skip-me"))
	h.Notify(ev("fill", "quiet-fill"))
	h.Notify(ev("liquidation", "loud-liq"))
	eventually(t, "5 messages", func() bool { return len(f.sentMsgs()) == 5 })
	ms = f.sentMsgs()
	if !strings.Contains(ms[3].Text, "quiet-fill") || !ms[3].Silent || !strings.Contains(ms[4].Text, "loud-liq") || ms[4].Silent {
		t.Fatalf("filter/quiet: %+v %+v", ms[3], ms[4])
	}
}

func TestOutboxRetryAfterAndBackoff(t *testing.T) {
	f, logs := newFakeTG(t), &logBuf{}
	h := newHub(t, openStore(t), f, logs)
	configured(t, h, 42)
	f.failNext(429, 500, 400) // 429 (retry_after 1 s), then 5xx (backoff), then a bad request (dropped)
	h.Notify(ev("fill", "a")) // hit by 429, 500, then 400 → dropped
	h.Notify(ev("fill", "b"))
	h.Notify(ev("fill", "c"))
	start := time.Now()
	run(t, h)
	eventually(t, "b and c", func() bool { return len(f.sentMsgs()) == 2 })
	ms := f.sentMsgs()
	if !strings.Contains(ms[0].Text, " · b") || !strings.Contains(ms[1].Text, " · c") {
		t.Fatalf("order: %q / %q", ms[0].Text, ms[1].Text)
	}
	f.mu.Lock()
	att := append([]time.Time{}, f.attempts...)
	f.mu.Unlock()
	if gap := att[1].Sub(att[0]); gap < 900*time.Millisecond {
		t.Fatalf("retry_after not honoured: second attempt after %s", gap)
	}
	if time.Since(start) > 6*time.Second {
		t.Fatal("too slow")
	}
	if logs.count("dropping undeliverable") != 1 {
		t.Fatalf("logs:\n%s", logs.all())
	}
	eventually(t, "empty outbox", func() bool { return h.backlog("telegram") == 0 })
}

func TestBacklogMergedIntoSummary(t *testing.T) {
	f, logs := newFakeTG(t), &logBuf{}
	h := newHub(t, openStore(t), f, logs)
	configured(t, h, 42)
	for i := 0; i < 40; i++ {
		e := ev("fill", fmt.Sprintf("r%d", i))
		if i == 7 {
			e.Kind = "liquidation"
		}
		e.Ts += int64(i) * 60_000
		h.Notify(e)
	}
	run(t, h)
	eventually(t, "summary", func() bool { return len(f.sentMsgs()) == 1 })
	h.Notify(ev("fill", "after"))
	eventually(t, "next", func() bool { return len(f.sentMsgs()) == 2 })
	ms := f.sentMsgs()
	if !strings.Contains(ms[0].Text, "🔴 积压 40 条通知") || !strings.Contains(ms[0].Text, "成交 ×39") || len(strings.Split(ms[0].Text, "\n")) > maxLines {
		t.Fatalf("summary: %q", ms[0].Text)
	}
	if !strings.Contains(ms[1].Text, "after") {
		t.Fatalf("after: %q", ms[1].Text)
	}
}

func prompt(id string, d time.Duration) trade.Prompt {
	return trade.Prompt{ID: id, Default: "skip", Deadline: time.Now().Add(d),
		Event: trade.Event{Kind: "signal", Account: "主账户", Mode: trade.ModeLive, Title: "待确认：开多",
			Lines: []string{"BTC-USDT-SWAP  12 张 ≈ 735 USDT · 5x", "止损 60,410 · 止盈 62,900"}, Ref: "sig-" + id},
		Options: []trade.PromptOption{{Key: "execute", Label: "执行"}, {Key: "half", Label: "半仓"}, {Key: "skip", Label: "跳过"}}}
}

type askResult struct {
	choice, by string
	err        error
}

func askAsync(h *Hub, ctx context.Context, p trade.Prompt) chan askResult {
	ch := make(chan askResult, 1)
	go func() {
		c, b, err := h.Ask(ctx, p)
		ch <- askResult{c, b, err}
	}()
	return ch
}

func waitAsk(t *testing.T, ch chan askResult) askResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(8 * time.Second):
		t.Fatal("Ask did not return")
	}
	return askResult{}
}

func TestAskTelegramFirstAnswerWins(t *testing.T) {
	f, logs := newFakeTG(t), &logBuf{}
	h := newHub(t, openStore(t), f, logs)
	changes := make(chan struct{}, 10)
	h.SetOnChange(func() { changes <- struct{}{} })
	configured(t, h, 42)
	run(t, h)

	res := askAsync(h, context.Background(), prompt("p1", 30*time.Second))
	eventually(t, "prompt message", func() bool { return len(f.sentMsgs()) == 1 })
	m := f.sentMsgs()[0]
	if m.Markup == nil || len(m.Markup.Rows) != 1 || len(m.Markup.Rows[0]) != 3 || m.Markup.Rows[0][0].Text != "执行" {
		t.Fatalf("keyboard: %+v", m.Markup)
	}
	if !strings.HasPrefix(m.Text, "<b>🟡 [模拟·主账户] 待确认：开多</b>") || !strings.Contains(m.Text, "内未回复将跳过") {
		t.Fatalf("prompt text: %q", m.Text)
	}
	data := m.Markup.Rows[0][0].Data
	if len(data) > 64 || !strings.HasSuffix(data, ":execute") {
		t.Fatalf("callback data %q", data)
	}
	f.callback(42, "private", data)
	r := waitAsk(t, res)
	if r.err != nil || r.choice != "execute" || r.by != "telegram:42" {
		t.Fatalf("ask: %+v", r)
	}
	// later answers are ignored
	if _, err := h.Answer("p1", "skip", "web"); !errors.Is(err, ErrClosed) {
		t.Fatalf("second answer: %v", err)
	}
	f.callback(42, "private", strings.Replace(data, ":execute", ":skip", 1))
	eventually(t, "edit + callback answers", func() bool { return len(f.editMsgs()) == 1 && len(f.cbAnswers()) == 2 })
	if e := f.editMsgs()[0]; e.ID != m.ID || e.Markup != nil || !strings.Contains(e.Text, "结果：执行 · Telegram 42") || !strings.HasPrefix(e.Text, "<b>⚪ ") {
		t.Fatalf("edit: %+v", e)
	}
	cbs := map[any]any{} // replies are asynchronous: match by callback id
	for _, c := range f.cbAnswers() {
		cbs[c["callback_query_id"]] = c["text"]
	}
	if cbs["cb101"] != "已记录：执行" || cbs["cb102"] != "已结束：执行" {
		t.Fatalf("callback answers: %v", cbs)
	}
	rec, _ := h.record("p1")
	if rec.State != StateAnswered || rec.Choice != "execute" || rec.By != "telegram:42" || rec.AnsweredAt == 0 {
		t.Fatalf("record: %+v", rec)
	}
	if len(changes) < 2 {
		t.Fatalf("OnChange called %d times", len(changes))
	}
	// asking again with the same id returns the stored result
	if c, b, err := h.Ask(context.Background(), prompt("p1", time.Minute)); err != nil || c != "execute" || b != "telegram:42" {
		t.Fatalf("re-ask: %s %s %v", c, b, err)
	}
}

func TestAllowlistAndGroups(t *testing.T) {
	f, logs := newFakeTG(t), &logBuf{}
	h := newHub(t, openStore(t), f, logs)
	configured(t, h, 42)
	run(t, h)
	res := askAsync(h, context.Background(), prompt("p2", 30*time.Second))
	eventually(t, "prompt", func() bool { return len(f.sentMsgs()) == 1 })
	data := f.sentMsgs()[0].Markup.Rows[0][0].Data

	f.callback(99, "private", data) // stranger presses a button
	f.message(99, 99, "private", "/status")
	f.message(42, -100123, "group", "/status") // owner in a group
	f.message(99, 99, "private", "/start")
	eventually(t, "stranger /start reply", func() bool { return len(f.sentMsgs()) == 2 })
	reply := f.sentMsgs()[1]
	if reply.Chat != 99 || !strings.Contains(reply.Text, "未授权") || !strings.Contains(reply.Text, "用户 ID：99") {
		t.Fatalf("stranger reply: %+v", reply)
	}
	f.message(99, 99, "private", "/start") // rate-limited: no second reply
	eventually(t, "callback answered", func() bool { return len(f.cbAnswers()) == 1 })
	if rec, _ := h.record("p2"); rec.State != StateOpen {
		t.Fatalf("stranger resolved the prompt: %+v", rec)
	}
	if v := h.Config(); len(v.Status.Strangers) != 1 || v.Status.Strangers[0].ID != 99 || v.Status.Bot != "@ctg_test_bot" {
		t.Fatalf("status: %+v", v.Status)
	}
	if _, err := h.Answer("p2", "half", "web"); err != nil {
		t.Fatal(err)
	}
	if r := waitAsk(t, res); r.choice != "half" || r.by != "web" {
		t.Fatalf("ask: %+v", r)
	}
	time.Sleep(100 * time.Millisecond)
	for _, m := range f.sentMsgs() {
		if m.Chat != 42 && m.Chat != 99 {
			t.Fatalf("message to %d", m.Chat)
		}
	}
	if n := len(f.sentMsgs()); n != 2 {
		t.Fatalf("%d messages sent", n)
	}
	if logs.count("ignoring user 99") != 1 || logs.count("ignoring chat -100123") != 1 {
		t.Fatalf("logs:\n%s", logs.all())
	}
}

func TestAskTimeoutAndCancel(t *testing.T) {
	logs := &logBuf{}
	h, err := New(openStore(t), nil, testOpts(nil, logs))
	if err != nil {
		t.Fatal(err)
	}
	c, b, err := h.Ask(context.Background(), prompt("t1", 150*time.Millisecond))
	if err != nil || c != "skip" || b != "timeout" {
		t.Fatalf("timeout: %s %s %v", c, b, err)
	}
	if rec, _ := h.record("t1"); rec.State != StateExpired || rec.Choice != "skip" {
		t.Fatalf("record: %+v", rec)
	}
	ctx, cancel := context.WithCancel(context.Background())
	res := askAsync(h, ctx, prompt("t2", time.Minute))
	eventually(t, "open", func() bool { rs, _ := h.Prompts("open", 10); return len(rs) == 1 })
	cancel()
	if r := waitAsk(t, res); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("cancel: %+v", r)
	}
	rec, _ := h.record("t2")
	if rec.State != StateExpired || rec.By != byCancelled {
		t.Fatalf("record: %+v", rec)
	}
	if _, err := h.Answer("t2", "execute", "web"); !errors.Is(err, ErrClosed) {
		t.Fatalf("answer after cancel: %v", err)
	}
	if _, _, err := h.Ask(context.Background(), trade.Prompt{ID: "bad"}); err == nil {
		t.Fatal("prompt without options accepted")
	}
}

func TestRestartRecovery(t *testing.T) {
	f, logs := newFakeTG(t), &logBuf{}
	st := openStore(t)
	h1 := newHub(t, st, f, logs)
	configured(t, h1, 42)
	// a prompt that was sent before the crash and expired while down
	old := Record{ID: "old", Short: newShort(), Event: prompt("old", 0).Event, Options: prompt("old", 0).Options,
		Deadline: time.Now().Add(-time.Minute).UnixMilli(), Default: "skip", State: StateOpen, CreatedAt: time.Now().Add(-3 * time.Minute).UnixMilli()}
	if ok, err := h1.insertPrompt(old); !ok || err != nil {
		t.Fatal(ok, err)
	}
	h1.putMessage("old", "telegram", "42", "777")
	// an orphan that expires after the restart (nobody waits on it any more)
	orphan := old
	orphan.ID, orphan.Short, orphan.Deadline = "orphan", newShort(), time.Now().Add(300*time.Millisecond).UnixMilli()
	h1.insertPrompt(orphan)
	h1.Notify(ev("fill", "queued-before-restart")) // never delivered by h1 (not running)

	h2 := newHub(t, st, f, logs)
	if rec, _ := h2.record("old"); rec.State != StateExpired || rec.Choice != "skip" || rec.By != "timeout" {
		t.Fatalf("expired on restart: %+v", rec)
	}
	run(t, h2)
	eventually(t, "queued event + edit", func() bool { return len(f.sentMsgs()) == 1 && len(f.editMsgs()) >= 1 })
	if !strings.Contains(f.sentMsgs()[0].Text, "queued-before-restart") || f.editMsgs()[0].ID != 777 || !strings.Contains(f.editMsgs()[0].Text, "超时默认") {
		t.Fatalf("sent %+v edits %+v", f.sentMsgs(), f.editMsgs())
	}
	eventually(t, "orphan expired", func() bool { r, _ := h2.record("orphan"); return r.State == StateExpired })

	// the update offset survives restarts
	f.message(42, 42, "private", "/help")
	eventually(t, "help reply", func() bool { return len(f.sentMsgs()) == 2 })
	eventually(t, "offset saved", func() bool { raw, _ := st.Setting(offsetKey); return strings.Contains(raw, `"offset":`) })
	h3 := newHub(t, st, f, logs)
	t3 := newTelegram(h3, h3.config().Telegram)
	if off := t3.loadOffset(); off != f.nextUpd+1 {
		t.Fatalf("offset %d, want %d", off, f.nextUpd+1)
	}
}

type fakeCmds struct{}

func (fakeCmds) Status() string           { return "账户 2 个 · 持仓 1" }
func (fakeCmds) Positions() string        { return "BTC-USDT-SWAP long 12 <x>" }
func (fakeCmds) Pause(arg string) string  { return "paused " + arg }
func (fakeCmds) Resume(arg string) string { return "resumed " + arg }

func TestCommands(t *testing.T) {
	f, logs := newFakeTG(t), &logBuf{}
	h := newHub(t, openStore(t), f, logs)
	configured(t, h, 42)
	run(t, h)
	send := func(text string) string {
		t.Helper()
		n := len(f.sentMsgs())
		f.message(42, 42, "private", text)
		eventually(t, "reply to "+text, func() bool { return len(f.sentMsgs()) == n+1 })
		return f.sentMsgs()[n].Text
	}
	if r := send("/status"); r != notAvailable {
		t.Fatalf("no commands: %q", r)
	}
	if r := send("/start"); !strings.Contains(r, "已授权") || !strings.Contains(r, "用户 ID：42") {
		t.Fatalf("start: %q", r)
	}
	h.SetCommands(fakeCmds{})
	for in, want := range map[string]string{
		"/status@ctg_test_bot": "账户 2 个 · 持仓 1",
		"/positions":           "BTC-USDT-SWAP long 12 &lt;x&gt;",
		"/pause 12":            "paused 12",
		"/resume  majors ":     "resumed majors",
		"/pause":               "用法：/pause &lt;实例 ID 或名称&gt;",
	} {
		if r := send(in); r != want {
			t.Fatalf("%s → %q, want %q", in, r, want)
		}
	}
	if r := send("/help"); !strings.Contains(r, "/positions") {
		t.Fatalf("help: %q", r)
	}
}

func TestConfigRestartAndSecrets(t *testing.T) {
	f, logs := newFakeTG(t), &logBuf{}
	h := newHub(t, openStore(t), f, logs)
	run(t, h)
	if v := h.Config(); v.Status.State != "disabled" {
		t.Fatalf("status %+v", v.Status)
	}
	// wrong token: the poller fails, the error never contains the token
	bad := "654321:WRONGtokenWRONGtokenWRONGtoken00"
	if _, err := h.SaveConfig(tgView(true, bad, 42)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "poll error", func() bool { return h.Config().Status.State == "error" })
	if s := h.Config().Status.LastError + logs.all(); strings.Contains(s, bad) || strings.Contains(s, "WRONGtoken") {
		t.Fatalf("token leaked: %s", s)
	}
	// fixing the token takes effect without a restart
	if _, err := h.SaveConfig(tgView(true, testToken, 42)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "poll ok", func() bool { return h.Config().Status.State == "ok" })
	h.Notify(ev("fill", "x"))
	eventually(t, "delivered", func() bool { return len(f.sentMsgs()) == 1 })
	// disabling drops the queue and stops the bot
	if _, err := h.SaveConfig(tgView(false, "", 42)); err != nil {
		t.Fatal(err)
	}
	h.Notify(ev("fill", "y"))
	if h.backlog("telegram") != 0 || h.Config().Status.State != "disabled" || h.config().Telegram.BotToken != testToken {
		t.Fatalf("after disable: %+v", h.Config().Status)
	}
}

func TestREST(t *testing.T) {
	f, logs := newFakeTG(t), &logBuf{}
	h := newHub(t, openStore(t), f, logs)
	run(t, h)
	mux := http.NewServeMux()
	h.Register(mux)
	do := func(method, path, body string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}

	if code, body := do("PUT", "/api/v1/interact/config", `{"telegram":{"enabled":true,"botToken":"nope"}}`); code != 400 {
		t.Fatalf("bad token: %d %s", code, body)
	}
	if code, body := do("PUT", "/api/v1/interact/config", `{"telegram":{"enabled":true}}`); code != 400 {
		t.Fatalf("enabled without token: %d %s", code, body)
	}
	if code, body := do("PUT", "/api/v1/interact/config", `{"telegram":{"enabled":true,"allowUserIds":[-5],"botToken":"`+testToken+`"}}`); code != 400 {
		t.Fatalf("group id: %d %s", code, body)
	}
	code, body := do("PUT", "/api/v1/interact/config", `{"telegram":{"enabled":true,"botToken":"`+testToken+`","allowUserIds":[42,42]},"kinds":["fill"]}`)
	if code != 200 || strings.Contains(body, testToken) {
		t.Fatalf("put: %d %s", code, body)
	}
	code, body = do("GET", "/api/v1/interact/config", "")
	var v ConfigView
	json.Unmarshal([]byte(body), &v)
	if code != 200 || strings.Contains(body, testToken) || v.Telegram.BotToken != Mask(testToken) || !v.Telegram.Configured ||
		len(v.Telegram.AllowUserIDs) != 1 || len(v.AllKinds) == 0 {
		t.Fatalf("get: %d %s", code, body)
	}
	// masked token back = unchanged
	v.Kinds = nil
	b, _ := json.Marshal(v)
	if code, body := do("PUT", "/api/v1/interact/config", string(b)); code != 200 || h.config().Telegram.BotToken != testToken {
		t.Fatalf("masked put: %d %s", code, body)
	}

	// test endpoint: message + prompt
	code, body = do("POST", "/api/v1/interact/test", "")
	if code != 200 || !strings.Contains(body, `"telegram":"ok"`) {
		t.Fatalf("test: %d %s", code, body)
	}
	eventually(t, "test msg + prompt", func() bool { return len(f.sentMsgs()) == 2 })
	if code, _ := do("POST", "/api/v1/interact/test", ""); code != 409 {
		t.Fatalf("second test while open: %d", code)
	}

	res := askAsync(h, context.Background(), prompt("w1", time.Minute))
	eventually(t, "open prompts", func() bool {
		_, body := do("GET", "/api/v1/interact/prompts?state=open", "")
		return strings.Contains(body, `"id":"w1"`)
	})
	code, body = do("GET", "/api/v1/interact/prompts?state=all&limit=1", "")
	var list struct{ Prompts []Record }
	json.Unmarshal([]byte(body), &list)
	if code != 200 || len(list.Prompts) != 1 || list.Prompts[0].ID != "w1" || list.Prompts[0].State != StateOpen {
		t.Fatalf("list: %d %s", code, body)
	}
	if code, _ := do("GET", "/api/v1/interact/prompts?state=bogus", ""); code != 400 {
		t.Fatal("bad state accepted")
	}
	if code, _ := do("POST", "/api/v1/interact/prompts/nope", `{"choice":"execute"}`); code != 404 {
		t.Fatalf("unknown: %d", code)
	}
	if code, _ := do("POST", "/api/v1/interact/prompts/w1", `{"choice":"yolo"}`); code != 400 {
		t.Fatalf("bad choice: %d", code)
	}
	if code, body := do("POST", "/api/v1/interact/prompts/w1", `{"choice":"execute"}`); code != 200 || !strings.Contains(body, `"by":"web"`) {
		t.Fatalf("answer: %d %s", code, body)
	}
	if r := waitAsk(t, res); r.choice != "execute" || r.by != "web" {
		t.Fatalf("ask: %+v", r)
	}
	if code, body := do("POST", "/api/v1/interact/prompts/w1", `{"choice":"skip"}`); code != 409 || !strings.Contains(body, `"choice":"execute"`) {
		t.Fatalf("second answer: %d %s", code, body)
	}
	eventually(t, "telegram copy edited", func() bool {
		for _, e := range f.editMsgs() {
			if strings.Contains(e.Text, "结果：执行 · 网页") {
				return true
			}
		}
		return false
	})
}

func TestFormatLimits(t *testing.T) {
	e := ev("fill", "r")
	for i := 0; i < 20; i++ {
		e.Lines = append(e.Lines, strings.Repeat("长", 300))
	}
	lines := renderEvent(e)
	if len(lines) != maxLines || !strings.HasPrefix(lines[maxLines-2], "…（另") {
		t.Fatalf("%d lines: %v", len(lines), lines)
	}
	for _, l := range lines {
		if len([]rune(l)) > maxLineLen+2 {
			t.Fatalf("long line %d", len([]rune(l)))
		}
	}
	r := Record{Event: e, Options: prompt("x", 0).Options, Default: "skip", Deadline: time.Now().Add(2 * time.Minute).UnixMilli()}
	pl := renderPrompt(r, time.Now())
	if len(pl) != maxLines || pl[len(pl)-2] != "2 分钟内未回复将跳过" {
		t.Fatalf("prompt: %v", pl)
	}
}

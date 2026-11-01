package realtrade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Bot is the dedicated Telegram trade bot (CTG_TRADE_TG_TOKEN, CTG_TRADE_TG_CHAT): one message per
// proposal with ✅ 同意 / ❌ 拒绝 (同意 asks to confirm first) and a 🔍 查看 link to the K-line with
// the levels, edited in place as the proposal moves on, plus a short push for every trade event.
// Only the configured chat is served, and only the owner (the chat id is his user id) can answer.
// Messages are queued in memory and retried; a pending proposal whose card was never sent is sent
// again after a restart.
type Bot struct {
	s       *Service
	api     string
	token   string
	botID   string
	chat    int64
	hc      *http.Client
	timeout time.Duration

	queue chan botJob

	mu  sync.Mutex
	st  BotStatus
	run bool
}

// BotStatus is shown in /api/v1/realtrade/config.
type BotStatus struct {
	Enabled   bool   `json:"enabled"`
	State     string `json:"state"` // disabled | starting | ok | error
	Bot       string `json:"bot,omitempty"`
	LastError string `json:"lastError,omitempty"`
}

type botJob struct {
	kind string
	id   int64
	text string // kind "note": a plain push (signal fired) with an optional 打开对话 link
	link string
}

const (
	botCallTO  = 15 * time.Second
	botOffset  = "realtrade.telegram.offset"
	botQueue   = 256
	botRetries = 5
)

// BotFromEnv returns the bot, or nil when CTG_TRADE_TG_TOKEN / CTG_TRADE_TG_CHAT are not both set.
func BotFromEnv(s *Service, client *http.Client) (*Bot, error) {
	token, chat := strings.TrimSpace(os.Getenv("CTG_TRADE_TG_TOKEN")), strings.TrimSpace(os.Getenv("CTG_TRADE_TG_CHAT"))
	if token == "" || chat == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(chat, 10, 64)
	if err != nil || id == 0 {
		return nil, errors.New("CTG_TRADE_TG_CHAT must be the owner's numeric Telegram chat id")
	}
	return NewBot(s, client, "https://api.telegram.org", token, id), nil
}

// NewBot builds a bot against api (tests pass a fake server) and registers it with the service.
func NewBot(s *Service, client *http.Client, api, token string, chat int64) *Bot {
	if client == nil {
		client = http.DefaultClient
	}
	hc := *client
	hc.Timeout = 0
	b := &Bot{s: s, api: strings.TrimRight(api, "/"), token: token, chat: chat, hc: &hc, timeout: 50 * time.Second,
		queue: make(chan botJob, botQueue), st: BotStatus{Enabled: true, State: "starting"}}
	b.botID, _, _ = strings.Cut(token, ":")
	s.AddNotifier(b)
	s.mu.Lock()
	s.bot = b
	s.mu.Unlock()
	return b
}

// Status reports the bot's state.
func (b *Bot) Status() BotStatus {
	if b == nil {
		return BotStatus{State: "disabled"}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st
}

func (b *Bot) setErr(err error) {
	b.mu.Lock()
	b.st.State, b.st.LastError = "error", err.Error()
	b.mu.Unlock()
}

// ProposalEvent queues the card update and push for a proposal event (never blocks).
func (b *Bot) ProposalEvent(kind string, p Proposal) {
	select {
	case b.queue <- botJob{kind: kind, id: p.ID}:
	default:
		b.s.logf("realtrade: telegram queue full; dropped %s #%d", kind, p.ID)
	}
}

// Note queues a plain push (internal/signals: a subscription fired) with an optional link button.
func (b *Bot) Note(text, link string) {
	select {
	case b.queue <- botJob{kind: "note", text: text, link: link}:
	default:
		b.s.logf("realtrade: telegram queue full; dropped a note")
	}
}

// Run sends queued messages and long-polls for button presses until ctx ends.
func (b *Bot) Run(ctx context.Context) {
	b.resend()
	go b.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-b.queue:
			b.deliver(ctx, j)
		}
	}
}

// resend queues the card of every pending proposal that has none yet (e.g. created while the bot was down).
func (b *Bot) resend() {
	ps, err := b.s.List(ListQuery{Status: StatusPending, Limit: 50})
	if err != nil {
		return
	}
	for _, p := range ps {
		if _, _, ok := b.message(p.ID); !ok {
			b.ProposalEvent("proposed", p)
		}
	}
}

func (b *Bot) deliver(ctx context.Context, j botJob) {
	var p Proposal
	var err error
	if j.kind != "note" {
		if p, err = b.s.get(j.id); err != nil {
			return
		}
	}
	for try := 0; try < botRetries && ctx.Err() == nil; try++ {
		if j.kind == "note" {
			err = b.sendNote(ctx, j.text, j.link)
		} else {
			err = b.send(ctx, j.kind, p)
		}
		if err == nil {
			return
		}
		var ae *tgError
		if errors.As(err, &ae) && ae.code == http.StatusBadRequest {
			break
		}
		d := time.Duration(2<<try) * time.Second
		if errors.As(err, &ae) && ae.retry > 0 {
			d = ae.retry
		}
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
	}
	if err != nil {
		b.setErr(err)
		b.s.logf("realtrade: telegram %s #%d: %v", j.kind, j.id, err)
	}
}

// send renders one event: a new card for a proposal; otherwise the card is edited and, except for
// the intermediate "approved", a short push is sent so the phone rings.
func (b *Bot) send(ctx context.Context, kind string, p Proposal) error {
	if kind == "proposed" {
		if _, _, ok := b.message(p.ID); ok {
			return nil
		}
		var m tgMessage
		if err := b.call(ctx, "sendMessage", b.cardParams(p, false), &m); err != nil {
			return err
		}
		_, err := b.s.db.Exec(`INSERT OR REPLACE INTO trade_tg_msgs(proposal_id, chat, message) VALUES(?,?,?)`, p.ID, b.chat, m.MessageID)
		return err
	}
	if err := b.editCard(ctx, p, false); err != nil {
		return err
	}
	return b.call(ctx, "sendMessage", map[string]any{"chat_id": b.chat, "text": html.EscapeString(eventLine(kind, p)), "parse_mode": "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true}}, nil)
}

func (b *Bot) sendNote(ctx context.Context, text, link string) error {
	params := map[string]any{"chat_id": b.chat, "text": html.EscapeString(text), "parse_mode": "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true}}
	if link != "" {
		params["reply_markup"] = map[string]any{"inline_keyboard": [][]tgButton{{{Text: "💬 打开对话", URL: link}}}}
	}
	return b.call(ctx, "sendMessage", params, nil)
}

func (b *Bot) message(id int64) (int64, int64, bool) {
	var chat, msg int64
	err := b.s.db.QueryRow(`SELECT chat, message FROM trade_tg_msgs WHERE proposal_id=?`, id).Scan(&chat, &msg)
	return chat, msg, err == nil
}

type tgButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// cardParams is the proposal message; confirming = the 同意 button was pressed once.
func (b *Bot) cardParams(p Proposal, confirming bool) map[string]any {
	lines := cardLines(p, b.s.now())
	text := "<b>" + html.EscapeString(fmt.Sprintf("交易提议 #%d", p.ID)) + "</b>\n" + html.EscapeString(strings.Join(lines, "\n"))
	var rows [][]tgButton
	id := strconv.FormatInt(p.ID, 10)
	if p.Status == StatusPending {
		if confirming {
			text += "\n\n<b>" + html.EscapeString(fmt.Sprintf("确认以市价%s %s，保证金 %s U，%d× 逐仓？", sideText(p.Side), coin(p.InstID), fmtNum(p.MarginUSDT), p.Leverage)) + "</b>"
			rows = append(rows, []tgButton{{Text: "✅ 确认同意", Data: "go:" + id}, {Text: "↩️ 返回", Data: "back:" + id}})
		} else {
			rows = append(rows, []tgButton{{Text: "✅ 同意", Data: "ok:" + id}, {Text: "❌ 拒绝", Data: "no:" + id}})
		}
	}
	if link := b.s.Link(p); link != "" {
		rows = append(rows, []tgButton{{Text: "🔍 查看", URL: link}})
	}
	params := map[string]any{"chat_id": b.chat, "text": text, "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true},
		"reply_markup": map[string]any{"inline_keyboard": rows}}
	if len(rows) == 0 {
		params["reply_markup"] = map[string]any{"inline_keyboard": [][]tgButton{}}
	}
	return params
}

func (b *Bot) editCard(ctx context.Context, p Proposal, confirming bool) error {
	chat, msg, ok := b.message(p.ID)
	if !ok {
		return nil
	}
	params := b.cardParams(p, confirming)
	params["chat_id"], params["message_id"] = chat, msg
	err := b.call(ctx, "editMessageText", params, nil)
	var ae *tgError
	if errors.As(err, &ae) && ae.code == http.StatusBadRequest && strings.Contains(ae.desc, "not modified") {
		return nil
	}
	return err
}

// ---- updates ----

type tgUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Text string `json:"text"`
}

type tgUpdate struct {
	UpdateID      int64      `json:"update_id"`
	Message       *tgMessage `json:"message"`
	CallbackQuery *struct {
		ID      string     `json:"id"`
		From    tgUser     `json:"from"`
		Message *tgMessage `json:"message"`
		Data    string     `json:"data"`
	} `json:"callback_query"`
}

type offsetState struct {
	Bot    string `json:"bot"`
	Offset int64  `json:"offset"`
}

func (b *Bot) poll(ctx context.Context) {
	var off offsetState
	if raw, ok := b.s.st.Setting(botOffset); ok && json.Unmarshal([]byte(raw), &off) == nil && off.Bot != b.botID {
		off = offsetState{}
	}
	fails := 0
	named := false
	for ctx.Err() == nil {
		if !named {
			var me tgUser
			if err := b.call(ctx, "getMe", map[string]any{}, &me); err != nil {
				fails++
				b.setErr(err)
				sleepCtx(ctx, min(time.Minute, time.Duration(fails)*2*time.Second))
				continue
			}
			named = true
			b.mu.Lock()
			b.st.Bot = "@" + me.Username
			b.mu.Unlock()
		}
		var ups []tgUpdate
		err := b.callTO(ctx, "getUpdates", map[string]any{"offset": off.Offset, "timeout": int(b.timeout / time.Second),
			"allowed_updates": []string{"message", "callback_query"}}, &ups, b.timeout+15*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fails++
			b.setErr(err)
			if fails == 1 || fails%20 == 0 {
				b.s.logf("realtrade: telegram poll (%d): %v", fails, err)
			}
			sleepCtx(ctx, min(time.Minute, time.Duration(fails)*2*time.Second))
			continue
		}
		fails = 0
		b.mu.Lock()
		b.st.State, b.st.LastError = "ok", ""
		b.mu.Unlock()
		for _, u := range ups {
			b.handle(ctx, u)
			off.Offset = max(off.Offset, u.UpdateID+1)
		}
		if len(ups) > 0 {
			off.Bot = b.botID
			raw, _ := json.Marshal(off)
			b.s.st.PutSetting(botOffset, string(raw))
		}
	}
}

// handle serves the owner only: callbacks and commands from anyone else are ignored.
func (b *Bot) handle(ctx context.Context, u tgUpdate) {
	if cb := u.CallbackQuery; cb != nil {
		if cb.From.ID != b.chat || cb.Message == nil || cb.Message.Chat.ID != b.chat {
			b.answer(ctx, cb.ID, "")
			return
		}
		act, idStr, _ := strings.Cut(cb.Data, ":")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		p, err := b.s.get(id)
		if err != nil {
			b.answer(ctx, cb.ID, "提议不存在")
			return
		}
		by := "telegram:" + strconv.FormatInt(cb.From.ID, 10)
		switch act {
		case "ok", "back":
			if p.Status != StatusPending {
				b.answer(ctx, cb.ID, "已结束："+statusText[p.Status])
				b.editCard(ctx, p, false)
				return
			}
			b.answer(ctx, cb.ID, "")
			b.editCard(ctx, p, act == "ok")
		case "go":
			b.answer(ctx, cb.ID, "已同意，复核价格后执行…")
			go func() { // approval executes the order; keep polling meanwhile
				actx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if _, err := b.s.Approve(actx, id, by, "telegram"); errors.Is(err, ErrClosed) {
					q, _ := b.s.get(id)
					b.editCard(actx, q, false)
				}
			}()
		case "no":
			if _, err := b.s.Reject(id, by, "telegram"); errors.Is(err, ErrClosed) {
				b.answer(ctx, cb.ID, "已结束："+statusText[p.Status])
				b.editCard(ctx, p, false)
				return
			}
			b.answer(ctx, cb.ID, "已拒绝")
		default:
			b.answer(ctx, cb.ID, "无法识别")
		}
		return
	}
	m := u.Message
	if m == nil || m.From == nil || m.From.ID != b.chat || m.Chat.ID != b.chat {
		return
	}
	cmd, _, _ := strings.Cut(strings.TrimSpace(m.Text), " ")
	cmd, _, _ = strings.Cut(cmd, "@")
	var text string
	switch cmd {
	case "/pending":
		ps, _ := b.s.List(ListQuery{Status: StatusPending, Limit: 20})
		if len(ps) == 0 {
			text = "没有待审核的交易提议"
		}
		for _, p := range ps {
			text += fmt.Sprintf("#%d %s\n", p.ID, summary(p))
		}
	default:
		text = fmt.Sprintf("交易机器人 · 模式 %s\nAI 的交易提议会发到这里，点 ✅ 同意（再确认一次）或 ❌ 拒绝；超时自动过期，不会执行。\n/pending 待审核列表", modeText(string(b.s.Mode())))
	}
	b.call(ctx, "sendMessage", map[string]any{"chat_id": b.chat, "text": html.EscapeString(strings.TrimSpace(text)), "parse_mode": "HTML"}, nil)
}

func (b *Bot) answer(ctx context.Context, id, text string) {
	p := map[string]any{"callback_query_id": id}
	if text != "" {
		p["text"] = text
	}
	b.call(ctx, "answerCallbackQuery", p, nil)
}

// ---- Bot API ----

type tgError struct {
	code  int
	desc  string
	retry time.Duration
}

func (e *tgError) Error() string { return fmt.Sprintf("Telegram %d：%s", e.code, e.desc) }

func (b *Bot) call(ctx context.Context, method string, params, out any) error {
	return b.callTO(ctx, method, params, out, botCallTO)
}

func (b *Bot) callTO(ctx context.Context, method string, params, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.api+"/bot"+b.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return b.scrub(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // the URL carries the token: keep only the cause
			err = ue.Err
		}
		return b.scrub(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var r struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(raw, &r) != nil || !r.OK {
		e := &tgError{code: resp.StatusCode, desc: b.scrub(errors.New(r.Description)).Error(), retry: time.Duration(r.Parameters.RetryAfter) * time.Second}
		if r.ErrorCode != 0 {
			e.code = r.ErrorCode
		}
		return e
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (b *Bot) scrub(err error) error {
	if err == nil || b.token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), b.token, "***"))
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

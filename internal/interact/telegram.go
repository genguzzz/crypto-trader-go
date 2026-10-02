package interact

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
	"strconv"
	"strings"
	"sync"
	"time"

	"crypto-trader-go/internal/trade"
)

// telegram is a dedicated bot in a private chat with the owner: Bot API over net/http, long
// polling getUpdates, inline keyboards for prompts, a few commands. Only allowlisted user ids in
// private chats are served; group chats and everyone else are ignored (logged once).
type telegram struct {
	h       *Hub
	api     string
	token   string
	botID   string // numeric prefix of the token (offsets are per bot)
	users   []int64
	allow   map[int64]bool
	hc      *http.Client
	timeout time.Duration // long-poll timeout

	mu        sync.Mutex
	st        TelegramStatus
	ignored   map[int64]bool      // users/chats already logged
	startSent map[int64]time.Time // last /start reply to a stranger
}

// TelegramStatus is shown on the settings page.
type TelegramStatus struct {
	State      string     `json:"state"` // disabled | stopped | starting | ok | error
	Bot        string     `json:"bot,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
	LastPollAt int64      `json:"lastPollAt,omitempty"`
	Recipients int        `json:"recipients"`
	Backlog    int        `json:"backlog"`
	Strangers  []Stranger `json:"strangers,omitempty"`
}

const (
	tgMaxLen  = 3900 // under Telegram's 4096-character limit
	callTO    = 15 * time.Second
	offsetKey = "interact.telegram.offset"
)

func newTelegram(h *Hub, c TelegramConfig) *telegram {
	t := &telegram{h: h, api: h.opts.TelegramAPI, token: c.BotToken, users: append([]int64{}, c.AllowUserIDs...),
		allow: map[int64]bool{}, hc: h.client, timeout: h.opts.PollTimeout,
		st: TelegramStatus{State: "starting"}, ignored: map[int64]bool{}, startSent: map[int64]time.Time{}}
	t.botID, _, _ = strings.Cut(c.BotToken, ":")
	for _, id := range c.AllowUserIDs {
		t.allow[id] = true
	}
	return t
}

func (t *telegram) Name() string { return "telegram" }

func (t *telegram) status() TelegramStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.st
}

func (t *telegram) setErr(err error) {
	t.mu.Lock()
	t.st.State, t.st.LastError = "error", err.Error()
	t.mu.Unlock()
}

// ---------- Bot API ----------

// APIError is a Bot API failure (token already scrubbed).
type APIError struct {
	Code        int
	Description string
	Retry       time.Duration
}

func (e *APIError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("Telegram %d：%s", e.Code, e.Description)
	}
	return fmt.Sprintf("Telegram HTTP %d", e.Code)
}

func (e *APIError) RetryAfter() time.Duration { return e.Retry }

// Permanent: a malformed request (400) will never succeed. 401/403/404 depend on the owner (wrong
// token, bot not started or blocked) and are retried with backoff.
func (e *APIError) Permanent() bool { return e.Code == http.StatusBadRequest }

func (t *telegram) call(ctx context.Context, method string, params any, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.api+"/bot"+t.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return t.scrub(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // *url.Error embeds the URL with the token: keep only the cause
			err = ue.Err
		}
		return t.scrub(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var r struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(b, &r) != nil || !r.OK {
		e := &APIError{Code: resp.StatusCode, Description: t.scrub(errors.New(r.Description)).Error()}
		if r.ErrorCode != 0 {
			e.Code = r.ErrorCode
		}
		if e.Code/100 == 2 {
			e.Code = http.StatusBadGateway
		}
		if r.Parameters.RetryAfter > 0 {
			e.Retry = time.Duration(r.Parameters.RetryAfter) * time.Second
		}
		return e
	}
	if out != nil {
		if err := json.Unmarshal(r.Result, out); err != nil {
			return fmt.Errorf("Telegram %s: bad result: %v", method, err)
		}
	}
	return nil
}

// scrub removes the token from an error text (defence in depth: never surface it).
func (t *telegram) scrub(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if t.token != "" {
		msg = strings.ReplaceAll(msg, t.token, "***")
	}
	return errors.New(msg)
}

type tgUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type tgChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from"`
	Chat      tgChat  `json:"chat"`
	Text      string  `json:"text"`
}

type tgCallback struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}

type tgUpdate struct {
	UpdateID      int64       `json:"update_id"`
	Message       *tgMessage  `json:"message"`
	CallbackQuery *tgCallback `json:"callback_query"`
}

type button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

type keyboard struct {
	Rows [][]button `json:"inline_keyboard"`
}

func (t *telegram) send(ctx context.Context, chat int64, text string, silent bool, kb *keyboard) (int64, error) {
	p := map[string]any{"chat_id": chat, "text": text, "parse_mode": "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true}}
	if silent {
		p["disable_notification"] = true
	}
	if kb != nil {
		p["reply_markup"] = kb
	}
	var m tgMessage
	if err := t.call(ctx, "sendMessage", p, &m, callTO); err != nil {
		return 0, err
	}
	return m.MessageID, nil
}

// ---------- Channel ----------

// Notify sends to every allowlisted user; it fails only when nobody got the message.
func (t *telegram) Notify(ctx context.Context, ev trade.Event) error {
	silent := t.h.config().Quiet && !critical(ev.Kind)
	text := htmlText(render(ev))
	var firstErr error
	sent := 0
	for _, u := range t.users {
		if _, err := t.send(ctx, u, text, silent, nil); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sent++
	}
	if sent == 0 {
		return firstErr
	}
	if firstErr != nil {
		t.h.logf("interact: telegram: partial delivery: %v", firstErr)
	}
	return nil
}

// Ask sends the prompt with an inline keyboard to every allowlisted user that has not got it yet
// (so retries don't duplicate) and records the message ids for Resolve.
func (t *telegram) Ask(ctx context.Context, p trade.Prompt) error {
	rec, ok := t.h.record(p.ID)
	if !ok {
		return nil
	}
	have, err := t.h.messages(rec.ID, t.Name())
	if err != nil {
		return err
	}
	got := map[string]bool{}
	for _, m := range have {
		got[m.Chat] = true
	}
	text := htmlText(renderPrompt(rec, time.Now()))
	kb := &keyboard{}
	var row []button
	for _, o := range rec.Options {
		label := o.Label
		if label == "" {
			label = o.Key
		}
		row = append(row, button{Text: label, Data: "a:" + rec.Short + ":" + o.Key})
		if len(row) == 3 {
			kb.Rows = append(kb.Rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		kb.Rows = append(kb.Rows, row)
	}
	var firstErr error
	for _, u := range t.users {
		chat := strconv.FormatInt(u, 10)
		if got[chat] {
			continue
		}
		id, err := t.send(ctx, u, text, false, kb)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := t.h.putMessage(rec.ID, t.Name(), chat, strconv.FormatInt(id, 10)); err != nil {
			t.h.logf("interact: telegram: store message id: %v", err)
		}
		got[chat] = true
	}
	if len(got) == 0 {
		return firstErr
	}
	return nil
}

// Resolve edits every sent copy of the prompt to show the result and removes the keyboard.
func (t *telegram) Resolve(ctx context.Context, promptID, choice, by string) error {
	rec, ok := t.h.record(promptID)
	if !ok {
		return nil
	}
	msgs, err := t.h.messages(rec.ID, t.Name())
	if err != nil {
		return err
	}
	text := htmlText(renderResolved(rec))
	var retryErr error
	for _, m := range msgs {
		chat, _ := strconv.ParseInt(m.Chat, 10, 64)
		mid, _ := strconv.ParseInt(m.Message, 10, 64)
		err := t.call(ctx, "editMessageText", map[string]any{"chat_id": chat, "message_id": mid, "text": text,
			"parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true}}, nil, callTO)
		var ae *APIError
		switch {
		case err == nil:
		case errors.As(err, &ae) && ae.Code == http.StatusBadRequest:
			if !strings.Contains(ae.Description, "not modified") {
				t.h.logf("interact: telegram: edit prompt %s: %v", rec.ID, err)
			}
		default:
			if retryErr == nil {
				retryErr = err
			}
		}
	}
	return retryErr
}

// Run long-polls getUpdates until ctx ends. The offset is persisted per bot so a restart neither
// replays nor loses updates.
func (t *telegram) Run(ctx context.Context, answers chan<- Answer) {
	offset := t.loadOffset()
	fails := 0
	named := false
	for ctx.Err() == nil {
		if !named {
			var me tgUser
			if err := t.call(ctx, "getMe", map[string]any{}, &me, callTO); err == nil {
				named = true
				t.mu.Lock()
				t.st.Bot = "@" + me.Username
				t.mu.Unlock()
				t.setCommands(ctx)
			} else if ctx.Err() == nil {
				t.failed(ctx, err, &fails)
				continue
			}
		}
		var ups []tgUpdate
		err := t.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": int(t.timeout / time.Second),
			"allowed_updates": []string{"message", "callback_query"}}, &ups, t.timeout+15*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			t.failed(ctx, err, &fails)
			continue
		}
		fails = 0
		t.mu.Lock()
		t.st.State, t.st.LastError, t.st.LastPollAt = "ok", "", time.Now().UnixMilli()
		t.mu.Unlock()
		for _, u := range ups {
			t.handle(ctx, u, answers)
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
		}
		if len(ups) > 0 {
			t.saveOffset(offset)
		}
	}
}

func (t *telegram) failed(ctx context.Context, err error, fails *int) {
	*fails++
	t.setErr(err)
	d := t.h.opts.Backoff << min(*fails-1, 20)
	if d > time.Minute || d <= 0 {
		d = time.Minute
	}
	var ae *APIError
	if errors.As(err, &ae) && ae.Retry > 0 {
		d = ae.Retry
	}
	if *fails == 1 || *fails%20 == 0 {
		t.h.logf("interact: telegram: poll failed (%d): %v", *fails, err)
	}
	sleep(ctx, d)
}

func (t *telegram) setCommands(ctx context.Context) {
	cmds := []map[string]string{
		{"command": "status", "description": "账户与策略概况"},
		{"command": "positions", "description": "当前持仓"},
		{"command": "pause", "description": "暂停策略实例：/pause <实例>"},
		{"command": "resume", "description": "恢复策略实例：/resume <实例>"},
		{"command": "start", "description": "显示用户 ID 与授权状态"},
		{"command": "help", "description": "帮助"},
	}
	if err := t.call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil, callTO); err != nil && ctx.Err() == nil {
		t.h.logf("interact: telegram: setMyCommands: %v", err)
	}
}

type offsetState struct {
	Bot    string `json:"bot"`
	Offset int64  `json:"offset"`
}

func (t *telegram) loadOffset() int64 {
	raw, ok := t.h.st.Setting(offsetKey)
	if !ok {
		return 0
	}
	var s offsetState
	if json.Unmarshal([]byte(raw), &s) != nil || s.Bot != t.botID {
		return 0
	}
	return s.Offset
}

func (t *telegram) saveOffset(off int64) {
	b, _ := json.Marshal(offsetState{Bot: t.botID, Offset: off})
	if err := t.h.st.PutSetting(offsetKey, string(b)); err != nil {
		t.h.logf("interact: telegram: save offset: %v", err)
	}
}

// ---------- updates ----------

func (t *telegram) ignore(id int64, what string) {
	t.mu.Lock()
	seen := t.ignored[id]
	t.ignored[id] = true
	t.mu.Unlock()
	if !seen {
		t.h.logf("interact: telegram: ignoring %s %d (not allowlisted)", what, id)
	}
}

func (t *telegram) handle(ctx context.Context, u tgUpdate, answers chan<- Answer) {
	switch {
	case u.CallbackQuery != nil:
		cb := u.CallbackQuery
		if cb.Message == nil || cb.Message.Chat.Type != "private" || !t.allow[cb.From.ID] {
			t.ignore(cb.From.ID, "user")
			t.answerCallback(ctx, cb.ID, "")
			return
		}
		parts := strings.SplitN(cb.Data, ":", 3)
		if len(parts) != 3 || parts[0] != "a" {
			t.answerCallback(ctx, cb.ID, "无法识别")
			return
		}
		id := cb.ID
		a := Answer{PromptID: parts[1], Choice: parts[2], By: "telegram:" + strconv.FormatInt(cb.From.ID, 10),
			Reply: func(text string) {
				rctx, cancel := context.WithTimeout(context.Background(), callTO)
				defer cancel()
				t.answerCallback(rctx, id, text)
			}}
		select {
		case answers <- a:
		case <-ctx.Done():
		}
	case u.Message != nil:
		m := u.Message
		if m.From == nil || m.From.IsBot {
			return
		}
		if m.Chat.Type != "private" {
			t.ignore(m.Chat.ID, "chat")
			return
		}
		cmd, arg := parseCommand(m.Text)
		if !t.allow[m.From.ID] {
			t.ignore(m.From.ID, "user")
			if cmd == "/start" {
				t.h.noteStranger(Stranger{ID: m.From.ID, Name: displayName(*m.From), At: time.Now().UnixMilli()})
				t.mu.Lock()
				last := t.startSent[m.From.ID]
				ok := time.Since(last) > time.Hour
				if ok {
					t.startSent[m.From.ID] = time.Now()
				}
				t.mu.Unlock()
				if ok {
					t.reply(ctx, m.Chat.ID, fmt.Sprintf("⚪ 未授权\n用户 ID：%d\n会话 ID：%d\n如果这是你本人，请在网页「交互设置」把该用户 ID 加入白名单。", m.From.ID, m.Chat.ID))
				}
			}
			return
		}
		t.reply(ctx, m.Chat.ID, t.command(cmd, arg, *m.From, m.Chat))
	}
}

func displayName(u tgUser) string {
	if u.Username != "" {
		return "@" + u.Username
	}
	return u.FirstName
}

// parseCommand returns ("/cmd", "arg") with any @botname suffix removed; non-commands give "".
func parseCommand(text string) (string, string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	cmd, arg, _ := strings.Cut(text, " ")
	cmd, _, _ = strings.Cut(cmd, "@")
	return strings.ToLower(cmd), strings.TrimSpace(arg)
}

const helpText = `可用命令
/status 账户与策略概况
/positions 当前持仓
/pause <实例> 暂停策略实例
/resume <实例> 恢复策略实例
/start 显示用户 ID 与授权状态
/help 本帮助
待确认的交易会以按钮消息发来，点选即可。`

const notAvailable = "暂不可用（交易服务未接入）"

func (t *telegram) command(cmd, arg string, from tgUser, chat tgChat) string {
	c := t.h.cmds()
	switch cmd {
	case "/start":
		return fmt.Sprintf("⚪ 已授权\n用户 ID：%d\n会话 ID：%d\n交易通知与待确认请求会发到这里。发送 /help 查看命令。", from.ID, chat.ID)
	case "/help", "":
		return helpText
	case "/status":
		if c == nil {
			return notAvailable
		}
		return c.Status()
	case "/positions":
		if c == nil {
			return notAvailable
		}
		return c.Positions()
	case "/pause", "/resume":
		if arg == "" {
			return "用法：" + cmd + " <实例 ID 或名称>"
		}
		if c == nil {
			return notAvailable
		}
		if cmd == "/pause" {
			return c.Pause(arg)
		}
		return c.Resume(arg)
	}
	return "未知命令。" + "\n" + helpText
}

func (t *telegram) reply(ctx context.Context, chat int64, text string) {
	if strings.TrimSpace(text) == "" {
		text = "（无内容）"
	}
	for _, part := range splitText(text, tgMaxLen) {
		if _, err := t.send(ctx, chat, html.EscapeString(part), false, nil); err != nil {
			if ctx.Err() == nil {
				t.h.logf("interact: telegram: reply: %v", err)
			}
			return
		}
	}
}

func (t *telegram) answerCallback(ctx context.Context, id, text string) {
	p := map[string]any{"callback_query_id": id}
	if text != "" {
		p["text"] = text
	}
	if err := t.call(ctx, "answerCallbackQuery", p, nil, callTO); err != nil && ctx.Err() == nil {
		t.h.logf("interact: telegram: answerCallbackQuery: %v", err)
	}
}

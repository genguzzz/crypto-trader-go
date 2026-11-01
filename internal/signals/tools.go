package signals

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The agent's tools (devapi, owner key only; chat = the X-Ctg-Chat of the call).

// SubscribeTool is subscribe_signal.
func (s *Service) SubscribeTool(ctx context.Context, chat int64, a map[string]string) (any, error) {
	if chat <= 0 {
		return nil, errors.New("subscribe_signal works only inside a conversation (no chat id on this call)")
	}
	raw := a["spec"]
	if strings.TrimSpace(raw) == "" && a["code"] != "" {
		raw = `{"type":"script"}`
	}
	spec, err := ParseSpec(raw)
	if err != nil {
		return nil, err
	}
	spec.fillCode(a["code"])
	hours, _ := strconv.ParseFloat(a["expiresHours"], 64)
	plan, _ := strconv.ParseInt(a["planId"], 10, 64)
	fires, _ := strconv.Atoi(a["maxFires"])
	cool, _ := strconv.Atoi(a["cooldownMin"])
	sub, err := s.SubscribeContext(ctx, SubscribeInput{ChatID: chat, Spec: spec, Intent: a["intent"], ExpiresHours: hours, PlanID: plan, MaxFires: fires, CooldownMin: cool, Every: a["every"]})
	if err != nil {
		return nil, err
	}
	out := toolRow(sub)
	out["note"] = "Subscribed. When the condition turns true this conversation is woken with a ⚡ 信号 message carrying the values and your intent; " +
		"re-check then and propose_trade, re-subscribe or explain. Nothing trades by itself and any trade still needs the owner's approval — " +
		"tell the owner what you are waiting for (signal #id, condition, expiry), never that it will trade automatically."
	return out, nil
}

// ListTool is list_signals: this chat's subscriptions (scope mine) or everyone's (all).
func (s *Service) ListTool(_ context.Context, chat int64, a map[string]string) (any, error) {
	q := ListQuery{Status: a["status"], InstID: a["instId"], Limit: 30}
	if q.Status == "" {
		q.Status = StatusActive
	}
	if a["scope"] != "all" {
		if chat <= 0 {
			return nil, errors.New("scope mine needs a conversation; use scope all")
		}
		q.ChatID = chat
	}
	subs, err := s.List(q)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(subs))
	for _, x := range subs {
		r := toolRow(x)
		r["mine"] = x.Kind == KindChat && x.ChatID == chat
		rows = append(rows, r)
	}
	return map[string]any{"signals": rows, "caps": s.Caps()}, nil
}

// UnsubscribeTool is unsubscribe_signal.
func (s *Service) UnsubscribeTool(_ context.Context, chat int64, a map[string]string) (any, error) {
	id, _ := strconv.ParseInt(a["id"], 10, 64)
	reason := strings.TrimSpace(a["reason"])
	if reason == "" {
		return nil, errors.New("reason is required (why the setup no longer needs watching)")
	}
	sub, err := s.CancelFor(chat, id, reason, a["ownerAsked"] == "true")
	if err != nil {
		return nil, err
	}
	return toolRow(sub), nil
}

func toolRow(x Subscription) map[string]any {
	r := map[string]any{"signalId": x.ID, "status": x.Status, "condition": x.Desc, "instIds": x.InstIDs, "subscriber": x.Subscriber,
		"intent": clip(x.Intent, 300), "expiresAt": time.UnixMilli(x.ExpiresAt).In(tz8).Format("2006-01-02 15:04 UTC+8"),
		"fires": strconv.Itoa(x.FireCount) + "/" + strconv.Itoa(x.MaxFires)}
	if x.PlanID > 0 {
		r["planId"] = x.PlanID
	}
	if x.Progress != nil {
		r["progress"] = x.Progress.Text
	}
	if x.Status != StatusActive {
		r["endedBy"], r["reason"] = x.EndedBy, x.Reason
	}
	if sc := x.Script; sc != nil {
		info := map[string]any{"timeframe": sc.Timeframe, "instIds": sc.InstIDs, "checks": sc.Checks}
		if sc.Replay != nil {
			info["replay"] = fmt.Sprintf("%d fires in %.1f days", sc.Replay.FireCount, sc.Replay.Days)
		}
		if sc.Errors > 0 || sc.LastError != "" {
			info["consecutiveErrors"], info["lastError"] = sc.Errors, sc.LastError
		}
		r["script"] = info
	}
	return r
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

package main

import (
	"errors"

	"crypto-trader-go/internal/aigate"
	"crypto-trader-go/internal/alerts"
	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/signals"
	"crypto-trader-go/internal/strategyhost"
)

// signalSources gives signal conditions their data: the market engine, the AI plans and the alert rules.
type signalSources struct {
	eng  *market.Engine
	gate *aigate.Gate
	al   *alerts.Service
}

func (s signalSources) Known(id string) bool { _, ok := s.eng.InstrumentMeta(id); return ok }

func (s signalSources) Quote(id string) (float64, int64, bool) {
	q, ok := s.eng.TradeQuote(id)
	return q.Last, q.Ts, ok
}

func (s signalSources) Closed(id, bar string) (signals.Bar, bool) {
	cs, err := s.eng.Candles(id, bar, 10)
	if err != nil {
		return signals.Bar{}, false
	}
	for i := len(cs) - 1; i >= 0; i-- {
		if c := cs[i]; c.Confirmed {
			return signals.Bar{T: c.T, H: c.H, L: c.L, C: c.C}, true
		}
	}
	return signals.Bar{}, false
}

func (s signalSources) Values(id string) (map[string]float64, bool) { return s.eng.FieldValues(id) }

func (s signalSources) MoveRisk(id string) (signals.MoveRisk, bool) {
	r := s.eng.MoveRisk(id)
	if r == nil {
		return signals.MoveRisk{}, false
	}
	side := func(x market.MoveRiskSide) signals.MoveSide {
		return signals.MoveSide{Extended: x.InState, PTail: x.PTail, PBig3: x.PBig3}
	}
	return signals.MoveRisk{T: r.T, Top: side(r.Top), Bottom: side(r.Bottom)}, true
}

func (s signalSources) Plan(id int64) (signals.Plan, bool) {
	p, ok := s.gate.PlanByID(id)
	if !ok {
		return signals.Plan{}, false
	}
	return signals.Plan{ID: p.ID, ChatID: p.ChatID, InstID: p.InstID, Side: p.Side, EntryLo: p.EntryLo, EntryHi: p.EntryHi, Stop: p.Stop,
		TP1: p.TP1, TP2: p.TP2, Invalidate: p.Invalidate, Status: p.Status, EnteredAt: p.EnteredAt}, true
}

func (s signalSources) AlertRule(id int64) (string, bool) {
	for _, r := range s.al.Rules() {
		if r.ID == id {
			return r.Name, true
		}
	}
	return "", false
}

func (s signalSources) Keep(ids []string) { s.eng.KeepFor("signals", ids) }

// signalWaker runs the agent for a fired subscription through the chat gate: steered into the chat (a
// new turn, or injected into the one running).
type signalWaker struct{ gate *aigate.Gate }

func (w signalWaker) Wake(chat int64, message string) error {
	return wakeErr(w.gate.SteerChat(chat, aigate.RoleSignal, message))
}

func (w signalWaker) Start(title, message, instID string) (int64, error) {
	id, err := w.gate.StartChat(aigate.ChatInput{Message: message, InstID: instID, Title: title, Role: aigate.RoleSignal})
	return id, wakeErr(err)
}

func (w signalWaker) ChatTitle(chat int64) (string, bool) { return w.gate.ChatTitle(chat) }

// wakeErr maps the gate's retryable errors to the ones signals retries.
func wakeErr(err error) error {
	switch {
	case errors.Is(err, aigate.ErrChatBusy):
		return signals.ErrBusy
	case errors.Is(err, aigate.ErrUnavailable), errors.Is(err, aigate.ErrWorkerGone):
		return signals.ErrAgentDown
	}
	return err
}

// signalScripts runs script conditions in the strategy container (through the strategy host's
// supervisor) on the engine's confirmed bars.
type signalScripts struct {
	*strategyhost.Host
	eng *market.Engine
}

func (s signalScripts) Bars(id, tf string, n int) ([]signals.Candle, error) {
	xs, err := s.eng.BarsTF(id, tf, n)
	if err != nil {
		return nil, err
	}
	out := make([]signals.Candle, len(xs))
	for i, x := range xs {
		out[i] = signals.Candle{Ts: x.Ts, O: x.O, H: x.H, L: x.L, C: x.C, Vol: x.Vol, VQ: x.Quote}
	}
	return out, nil
}

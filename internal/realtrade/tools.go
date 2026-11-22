package realtrade

import (
	"context"
	"errors"
	"strconv"
	"time"

	"crypto-trader-go/internal/okxacct"
)

// PositionsTool is the agent's get_positions: the owner's open OKX positions (inst "" = all), each
// marked when he locked it, so the agent leaves it out of its plans instead of being refused later.
func (s *Service) PositionsTool(ctx context.Context, inst string) (any, error) {
	a := s.account()
	if a == nil {
		return nil, errors.New("OKX account not configured")
	}
	snap, err := a.Snapshot(ctx, inst)
	if err != nil {
		return nil, err
	}
	type position struct {
		okxacct.Position
		Locked bool `json:"locked,omitempty"`
	}
	out := struct {
		okxacct.Snapshot
		Positions []position `json:"positions"`
		Note      string     `json:"note,omitempty"`
	}{Snapshot: snap, Positions: make([]position, len(snap.Positions))}
	for i, p := range snap.Positions {
		out.Positions[i] = position{Position: p, Locked: s.Locked(p.InstID, p.Side)}
		if out.Positions[i].Locked {
			out.Note = "locked: true = the owner locked that position. Do not propose anything on it (it would be refused); you may still report on it."
		}
	}
	return out, nil
}

// ProposeTool is the agent's propose_trade (devapi, owner key only): it creates a proposal linked to
// the agent's chat and returns what the owner will be asked to approve — or, with hands-free
// execution on, what was done with it.
func (s *Service) ProposeTool(ctx context.Context, chat int64, a map[string]string) (any, error) {
	f := func(k string) float64 { v, _ := strconv.ParseFloat(a[k], 64); return v }
	lev, _ := strconv.Atoi(a["leverage"])
	exp, _ := strconv.Atoi(a["expiresMin"])
	om, _ := strconv.Atoi(a["orderExpiryMin"])
	p, err := s.Propose(ctx, ProposeInput{ChatID: chat, Kind: a["kind"], InstID: a["instId"], Side: a["side"], MarginMode: a["marginMode"],
		OrdType: a["orderType"], Px: f("price"), MarginUSDT: f("marginUsdt"), Leverage: lev, SL: f("sl"), TP: f("tp"), Fraction: f("fraction"),
		Rationale: a["rationale"], Invalidation: a["invalidation"], ExpiresMin: exp, OrderMin: om})
	if err != nil {
		return nil, err
	}
	note := "Waiting for the owner's approval; nothing has been sent. Tell him it is pending (#id, valid until) — never that it was placed. " +
		"When he approves (and it runs), rejects it, or it expires unanswered, this conversation gets a message with the outcome (fill and position, or the refusal) — you do not need to poll or wait."
	if p.Via == ByAuto {
		note = "The owner switched on hands-free execution: this proposal was not shown to him for approval, it was re-validated and run at once. " +
			"`status` is the outcome: executed (sent; a limit order now rests), failed (OKX refused — see `error`, do not retry blindly) or invalidated (a check failed, nothing sent — see `error`). " +
			"This conversation also gets a message with the fill and the position; report what happened, never that it waits for approval."
	}
	if s.Mode() != "live" {
		note += " Dry run: it is only recorded and simulated, not sent."
	}
	out := map[string]any{"proposalId": p.ID, "status": p.Status, "mode": p.Mode, "kind": p.Kind, "instId": p.InstID, "side": p.Side,
		"orderType": p.OrdType, "marginMode": p.MarginMode, "contracts": p.Contracts, "refPx": p.RefPx,
		"expiresAt": time.UnixMilli(p.ExpiresAt).In(tz8).Format("2006-01-02 15:04 UTC+8"), "link": s.Link(p), "note": note}
	if p.OrdType == "limit" {
		out["price"], out["orderExpiryMin"] = p.Px, p.OrderMin
	}
	if p.Kind == KindOpen || p.Kind == KindAdd {
		out["marginUsdt"], out["leverage"], out["notionalUsdt"] = p.MarginUSDT, p.Leverage, round(p.NotionalUSDT, 2)
	}
	if p.SL > 0 {
		out["sl"], out["tp"], out["slPct"], out["tpPct"] = p.SL, p.TP, round(p.SLPct, 2), round(p.TPPct, 2)
		if p.Kind != KindAdjust {
			out["rr"] = round(p.RR, 2)
		}
	}
	if p.Kind == KindCancel || p.Kind == KindAmend {
		out["targetId"], out["change"] = p.TargetID, p.TPSL
	}
	if p.Kind != KindOpen {
		out["posId"], out["positionContracts"], out["positionAvgPx"] = p.PosID, p.PosContracts, p.PosAvgPx
	}
	if p.Kind == KindReduce {
		out["fraction"] = p.Fraction
	}
	if p.NewAvgPx > 0 {
		out["newAvgPx"] = round(p.NewAvgPx, 8)
	}
	if p.LiqPx > 0 {
		out["liqPxEstimate"] = round(p.LiqPx, 8)
	}
	if p.TPSL != "" {
		out["tpsl"] = p.TPSL
	}
	if p.Note != "" {
		out["warning"] = p.Note
	}
	if p.Via == ByAuto && p.Error != "" {
		out["error"] = p.Error
	}
	return out, nil
}

// ListTool is get_trade_proposals: recent proposals (or one coin's, or one AI position's) — the record
// of every trade made through ctg — with the mode and the caps.
func (s *Service) ListTool(_ context.Context, a map[string]string) (any, error) {
	limit, _ := strconv.Atoi(a["limit"])
	pos, _ := strconv.ParseInt(a["posId"], 10, 64)
	ps, err := s.List(ListQuery{Status: a["status"], InstID: a["instId"], PosID: pos, Limit: limit})
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		r := map[string]any{"id": p.ID, "created": time.UnixMilli(p.Created).In(tz8).Format("01-02 15:04"), "source": p.Source, "kind": p.Kind,
			"instId": p.InstID, "side": p.Side, "orderType": p.OrdType, "marginMode": p.MarginMode, "contracts": p.Contracts, "refPx": p.RefPx,
			"status": p.Status, "mode": p.Mode}
		if p.PosID > 0 {
			r["posId"] = p.PosID
		}
		if p.TargetID > 0 {
			r["targetId"], r["change"] = p.TargetID, p.TPSL
		}
		if p.Kind == KindOpen || p.Kind == KindAdd {
			r["marginUsdt"], r["leverage"] = p.MarginUSDT, p.Leverage
		}
		if p.SL > 0 {
			r["sl"], r["tp"] = p.SL, p.TP
		}
		if p.OrdType == "limit" {
			r["price"] = p.Px
		}
		if p.OrdState != "" {
			r["ordState"], r["filled"] = p.OrdState, p.Filled
			if p.FillPx > 0 {
				r["fillPx"] = p.FillPx
			}
		}
		if p.Error != "" {
			r["error"] = p.Error
		}
		if p.Kind == KindOpen && p.CurContracts > 0 && p.ClosedAt == 0 {
			r["position"] = map[string]any{"contracts": p.CurContracts, "avgPx": round(p.CurAvgPx, 8), "sl": p.CurSL, "tp": p.CurTP}
		}
		if p.ClosedAt > 0 {
			r["closeKind"], r["pnlUsdt"], r["closed"] = p.CloseKind, round(p.PnlUSDT, 2), time.UnixMilli(p.ClosedAt).In(tz8).Format("01-02 15:04")
		}
		rows = append(rows, r)
	}
	return map[string]any{"mode": s.Mode(), "limits": s.opts.Limits, "handsFree": s.Auto().Enabled, "locked": s.Locks(), "proposals": rows}, nil
}

func round(v float64, d int) float64 {
	p := 1.0
	for range d {
		p *= 10
	}
	return float64(int64(v*p+0.5*sign(v))) / p
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

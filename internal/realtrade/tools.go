package realtrade

import (
	"context"
	"strconv"
	"time"
)

// ProposeTool is the agent's propose_trade (devapi, owner key only): it creates a pending proposal
// linked to the agent's chat and returns what the owner will be asked to approve.
func (s *Service) ProposeTool(ctx context.Context, chat int64, a map[string]string) (any, error) {
	f := func(k string) float64 { v, _ := strconv.ParseFloat(a[k], 64); return v }
	lev, _ := strconv.Atoi(a["leverage"])
	exp, _ := strconv.Atoi(a["expiresMin"])
	p, err := s.Propose(ctx, ProposeInput{ChatID: chat, InstID: a["instId"], Side: a["side"], MarginUSDT: f("marginUsdt"), Leverage: lev,
		SL: f("sl"), TP: f("tp"), Rationale: a["rationale"], Invalidation: a["invalidation"], ExpiresMin: exp})
	if err != nil {
		return nil, err
	}
	note := "Waiting for the owner's approval; nothing has been traded. Tell the owner it is pending and how to approve (Telegram trade bot, or the card in this chat)."
	if s.Mode() != "live" {
		note += " Dry run: even if approved, the order is only recorded, not sent."
	}
	return map[string]any{"proposalId": p.ID, "status": p.Status, "mode": p.Mode, "source": p.Source, "instId": p.InstID, "side": p.Side,
		"marginUsdt": p.MarginUSDT, "leverage": p.Leverage, "contracts": p.Contracts, "notionalUsdt": round(p.NotionalUSDT, 2),
		"refPx": p.RefPx, "sl": p.SL, "tp": p.TP, "slPct": round(p.SLPct, 2), "tpPct": round(p.TPPct, 2), "rr": round(p.RR, 2),
		"expiresAt": time.UnixMilli(p.ExpiresAt).In(tz8).Format("2006-01-02 15:04 UTC+8"), "link": s.Link(p), "note": note}, nil
}

// ListTool is get_trade_proposals: recent proposals, the mode and the caps.
func (s *Service) ListTool(_ context.Context, a map[string]string) (any, error) {
	limit, _ := strconv.Atoi(a["limit"])
	ps, err := s.List(ListQuery{Status: a["status"], Limit: limit})
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		r := map[string]any{"id": p.ID, "created": time.UnixMilli(p.Created).In(tz8).Format("01-02 15:04"), "source": p.Source,
			"instId": p.InstID, "side": p.Side, "marginUsdt": p.MarginUSDT, "leverage": p.Leverage, "refPx": p.RefPx, "sl": p.SL, "tp": p.TP,
			"status": p.Status, "mode": p.Mode}
		if p.Error != "" {
			r["error"] = p.Error
		}
		if p.ExecPx > 0 {
			r["execPx"] = p.ExecPx
		}
		if p.ClosedAt > 0 {
			r["closeKind"], r["pnlUsdt"] = p.CloseKind, round(p.PnlUSDT, 2)
		}
		rows = append(rows, r)
	}
	return map[string]any{"mode": s.Mode(), "limits": s.opts.Limits, "proposals": rows}, nil
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

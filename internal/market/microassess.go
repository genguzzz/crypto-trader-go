package market

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// ---------- heuristic read of the tape + book: dumping, absorption, spot control, spoofing ----------
//
// Each verdict adds up points from transparent rules over the measured numbers and lists the
// evidence (and counter-evidence) as Chinese sentences with the numbers. Heuristics, not proofs:
// the trades carry no account ids, so "one party" is always an inference from sizes and timing.

type Verdict struct {
	Level    int      `json:"level"` // 0 none, 1 weak, 2 moderate, 3 strong
	Label    string   `json:"label"`
	Score    int      `json:"score"`
	Evidence []string `json:"evidence,omitempty"`
	Against  []string `json:"against,omitempty"`
}

func (v *Verdict) add(pts int, f string, a ...any) {
	v.Score += pts
	v.Evidence = append(v.Evidence, fmt.Sprintf(f, a...))
}

func (v *Verdict) against(f string, a ...any) { v.Against = append(v.Against, fmt.Sprintf(f, a...)) }

func (v *Verdict) finish() {
	switch {
	case v.Score >= 5:
		v.Level = 3
	case v.Score >= 3:
		v.Level = 2
	case v.Score >= 1:
		v.Level = 1
	}
	v.Label = [4]string{"无", "弱", "中", "强"}[v.Level]
}

type Assessment struct {
	Dump        Verdict `json:"dump"`        // 砸盘
	Absorption  Verdict `json:"absorption"`  // 吸筹 / 托盘
	SpotControl Verdict `json:"spotControl"` // 现货控盘
	Spoofing    Verdict `json:"spoofing"`    // 挂单诱导 / 假墙
}

func usdS(x float64) string {
	a := math.Abs(x)
	switch {
	case a >= 1e9:
		return fmt.Sprintf("%.2fB", x/1e9)
	case a >= 1e6:
		return fmt.Sprintf("%.2fM", x/1e6)
	case a >= 1e3:
		return fmt.Sprintf("%.1fk", x/1e3)
	}
	return fmt.Sprintf("%.0f", x)
}

func mkt(m string) string {
	if m == "spot" {
		return "现货"
	}
	return "合约"
}

func hhmm(ms int64) string { return time.UnixMilli(ms).In(tz8).Format("01-02 15:04") }

var tz8 = time.FixedZone("UTC+8", 8*3600)

// assess applies the rules. Any input may be nil.
func assess(sw, sp *TapeStats, cross *CrossStats, wsw, wsp *WallReport, bsw, bsp *BookShape) Assessment {
	var a Assessment
	for _, s := range []*TapeStats{sw, sp} {
		if s == nil || len(s.orders) == 0 || s.Summary == nil {
			continue
		}
		m := mkt(s.Market)
		sum := s.Summary
		vol := sum.BuyUsd + sum.SellUsd
		if cov := s.Coverage; (cov.Minutes > 0 && cov.Pct < 50) || sum.Orders < 300 {
			note := fmt.Sprintf("%s成交只覆盖窗口的 %.0f%%（%d 笔），成交类证据未计入", m, cov.Pct, sum.Orders)
			for _, v := range []*Verdict{&a.Dump, &a.Absorption, &a.SpotControl} {
				v.against("%s", note)
			}
			continue
		}
		// large-order delta
		ld := sum.LargeBuyUsd - sum.LargeSellUsd
		switch {
		case math.Abs(ld) < 10*sum.LargeUsd: // too few large orders to mean anything
		case sum.LargeDeltaPct <= -8:
			a.Dump.add(2, "%s大单（≥%s）净卖 %s，占成交 %.1f%%", m, usdS(sum.LargeUsd), usdS(-ld), -sum.LargeDeltaPct)
		case sum.LargeDeltaPct <= -3:
			a.Dump.add(1, "%s大单（≥%s）净卖 %s，占成交 %.1f%%", m, usdS(sum.LargeUsd), usdS(-ld), -sum.LargeDeltaPct)
		case sum.LargeDeltaPct >= 3:
			a.Dump.against("%s大单净买 %s（占成交 %.1f%%）", m, usdS(ld), sum.LargeDeltaPct)
		}
		// clip runs: only runs that matter (a real share of that side's flow meanwhile, or of the day)
		var sellPush, buyPush, buyQuiet int
		var clipUsd float64
		for _, r := range s.clipAll {
			clipUsd += r.TotalUsd
			strong := r.Count >= 5 && (!r.Round || r.Count >= 10) && r.TotalUsd >= max(3*sum.LargeUsd, 0.002*vol) &&
				(r.SideSharePct >= 10 || r.TotalUsd >= 0.01*vol)
			mins := float64(r.T1-r.T0) / 6e4
			switch {
			case strong && r.Side == "sell" && r.DriftBps <= -30 && r.WithTrendPct >= 60 && sellPush < 2:
				sellPush++
				a.Dump.add(1, "%s %s 起 %d 笔同尺寸卖单（每笔≈%s，共 %s）从 %.6g 砸到 %.6g（%+.0fbps，%.0f 分钟，占同期主动卖 %.0f%%，%s）",
					m, hhmm(r.T0), r.Count, usdS(r.UsdEach), usdS(r.TotalUsd), r.Px0, r.Px1, r.DriftBps, mins, r.SideSharePct, r.Pattern)
			case strong && r.Side == "buy" && r.DriftBps >= 30 && r.WithTrendPct >= 60 && buyPush < 2:
				buyPush++
				a.Dump.against("%s %d 笔同尺寸买单（每笔≈%s，共 %s）把价格推高 %+.0fbps", m, r.Count, usdS(r.UsdEach), usdS(r.TotalUsd), r.DriftBps)
			case strong && r.Side == "buy" && math.Abs(r.DriftBps) <= 30 && r.Count >= 8 && buyQuiet < 1:
				buyQuiet++
				a.Absorption.add(1, "%s %s 起 %d 笔同尺寸买单（每笔≈%s，共 %s，占同期主动买 %.0f%%，%s）价格几乎没动（%+.0fbps）：有人在不推价地分批买",
					m, hhmm(r.T0), r.Count, usdS(r.UsdEach), usdS(r.TotalUsd), r.SideSharePct, r.Pattern, r.DriftBps)
			}
		}
		// trend + delta
		if sum.RetPct <= -2 && sum.DeltaUsd < 0 {
			a.Dump.add(1, "%s价格 %+.1f%%，主动净卖 %s（买占 %.0f%%）", m, sum.RetPct, usdS(-sum.DeltaUsd), sum.BuyPct)
		}
		if sum.BuyPct <= 47 && sum.RetPct >= -0.5 {
			a.Absorption.add(2, "%s主动卖占 %.0f%%（净卖 %s），价格却 %+.1f%%：卖单被被动买盘接走", m, 100-sum.BuyPct, usdS(-sum.DeltaUsd), sum.RetPct)
		} else if sum.BuyPct >= 53 && sum.RetPct <= 0.5 {
			a.Absorption.against("%s主动买占 %.0f%% 价格却 %+.1f%%：买单被上方卖盘吃掉（派发迹象）", m, sum.BuyPct, sum.RetPct)
		}
		if sum.LargeDeltaPct >= 3 && math.Abs(sum.RetPct) < 1 {
			a.Absorption.add(1, "%s大单净买 %.1f%% 成交，价格基本横盘（%+.1f%%）", m, sum.LargeDeltaPct, sum.RetPct)
		}
		// sweeps
		if sw := s.Sweeps; sw != nil {
			if sw.SellUsd >= 2*sw.BuyUsd && sw.SellUsd >= vol*0.01 {
				a.Dump.add(1, "%s扫多档的砸单 %d 笔共 %s，是扫单买 %s 的 %.1f 倍", m, sw.SellN, usdS(sw.SellUsd), usdS(sw.BuyUsd), sw.SellUsd/math.Max(sw.BuyUsd, 1))
			} else if sw.BuyUsd >= 2*sw.SellUsd && sw.BuyUsd >= vol*0.01 {
				a.Dump.against("%s扫单以买为主：买 %s / 卖 %s", m, usdS(sw.BuyUsd), usdS(sw.SellUsd))
			}
		}
		// spot-only control evidence
		if s.Market == "spot" {
			switch w := s.wash; {
			case w.PctOfVol >= 8:
				a.SpotControl.add(2, "疑似对敲：%d 对同尺寸反向成交（2 秒内、同价），共 %s，占现货成交 %.1f%%", w.Pairs, usdS(w.Usd), w.PctOfVol)
			case w.PctOfVol >= 3:
				a.SpotControl.add(1, "疑似对敲：%d 对同尺寸反向成交，共 %s，占现货成交 %.1f%%", w.Pairs, usdS(w.Usd), w.PctOfVol)
			}
			if share := clipUsd / vol * 100; share >= 15 {
				a.SpotControl.add(1, "现货 %.0f%% 的成交来自固定尺寸的程序单", share)
			}
			if sum.Top1PctShare >= 35 {
				a.SpotControl.add(1, "现货最大的 1%% 订单贡献了 %.0f%% 成交（筹码集中在少数大单）", sum.Top1PctShare)
			}
		}
	}
	if cross != nil {
		if cross.Leader == "spot" {
			a.SpotControl.add(1, "现货领先合约约 %d 秒（相关 %.2f，反向 %.2f）：价格先在现货动", cross.LeadLagSec, cross.LeadCorr, cross.ReverseCorr)
		} else if cross.Leader == "swap" {
			a.SpotControl.against("合约领先现货约 %d 秒（相关 %.2f）：定价在合约", cross.LeadLagSec, cross.LeadCorr)
		}
		if cross.SpotSharePct < 5 {
			a.SpotControl.against("现货只占两市成交的 %.1f%%，价格主要由合约决定", cross.SpotSharePct)
		}
		if math.Abs(cross.BasisBpsMean) >= 15 {
			kind := "溢价"
			if cross.BasisBpsMean < 0 {
				kind = "折价"
			}
			a.SpotControl.add(1, "现货相对合约平均%s %.0fbps（前半段 %.0f → 后半段 %.0f）", kind, math.Abs(cross.BasisBpsMean), cross.BasisBpsFirst, cross.BasisBpsLast)
		}
		if cross.SwapLambda > 0 && cross.SpotLambda >= 3*cross.SwapLambda {
			a.SpotControl.add(1, "现货很容易被推动：每 10 万 U 净买卖带动 %.1fbps，合约只有 %.1fbps", cross.SpotLambda, cross.SwapLambda)
		}
	}
	if bsp != nil && bsp.Depth2PctTurn > 0 && bsp.Depth2PctTurn < 1 {
		a.SpotControl.add(1, "现货 ±2%% 挂单只有 %s，仅为 24h 成交的 %.2f%%（盘口薄，易拉易砸）", usdS(bandUsd(bsp, 2)), bsp.Depth2PctTurn)
	}
	for _, b := range []*BookShape{bsw, bsp} {
		if b == nil {
			continue
		}
		m := mkt(b.Market)
		for _, w := range b.Walls {
			if w.Side == "bid" && w.AgeMin >= 60 && math.Abs(w.DistPct) <= 2 {
				a.Absorption.add(1, "%s买墙 %.6g（%.2f%%）%s 已挂 %.0f 分钟（%d 单）", m, w.Px, w.DistPct, usdS(w.Usd), w.AgeMin, w.Orders)
				break
			}
		}
		for _, bd := range b.Bands {
			if bd.Pct == 1 && bd.Imb >= 0.3 {
				a.Absorption.add(1, "%s ±1%% 买盘 %s 远厚于卖盘 %s（失衡 %+.2f）", m, usdS(bd.BidUsd), usdS(bd.AskUsd), bd.Imb)
			} else if bd.Pct == 1 && bd.Imb <= -0.3 {
				a.Dump.add(1, "%s ±1%% 卖盘 %s 远厚于买盘 %s（压盘，失衡 %+.2f）", m, usdS(bd.AskUsd), usdS(bd.BidUsd), bd.Imb)
			}
		}
	}
	// Walls: market makers pull big quotes on BOTH sides as price nears; only a one-sided pattern
	// (one side's walls vanish as price comes, the other's do not) reads as fake support / pressure.
	oneSided := func(x, y int) bool { return x >= 5 && x >= 2*y }
	for _, w := range []*WallReport{wsw, wsp} {
		if w == nil {
			continue
		}
		m := mkt(w.Market)
		bp, ap := w.Bid.PulledNearN, w.Ask.PulledNearN
		switch {
		case oneSided(bp, ap):
			a.Spoofing.add(2, "%s买墙在价格靠近途中被撤 %d 次（%s），卖墙只有 %d 次：托单是假的", m, bp, usdS(w.Bid.PulledNearUsd), ap)
			a.Dump.add(1, "%s支撑是假的：买墙价格一靠近就撤（%d 次 vs 卖墙 %d 次）", m, bp, ap)
		case oneSided(ap, bp):
			a.Spoofing.add(2, "%s卖墙在价格靠近途中被撤 %d 次（%s），买墙只有 %d 次：压单是假的", m, ap, usdS(w.Ask.PulledNearUsd), bp)
		case bp+ap >= 5:
			a.Spoofing.against("%s两侧都在价格靠近时撤墙（买 %d / 卖 %d）：更像做市商随价撤挂，不算假墙", m, bp, ap)
		}
		bc, ac := w.Bid.MovingChains, w.Ask.MovingChains
		if ac >= 3 && ac >= 2*bc {
			a.Spoofing.add(1, "%s %d 组卖墙撤了又在附近重挂、跟着价格走（买墙 %d 组）", m, ac, bc)
			a.Dump.add(1, "%s卖墙跟着价格往下挪着压（%d 组，买墙 %d 组）", m, ac, bc)
		} else if bc >= 3 && bc >= 2*ac {
			a.Spoofing.add(1, "%s %d 组买墙撤了又在附近重挂、跟着价格走（卖墙 %d 组）", m, bc, ac)
		}
		if w.Bid.FilledN >= 3 && w.Bid.FilledUsd >= 2*w.Bid.PulledNearUsd && w.Bid.FilledN > w.Ask.FilledN {
			a.Absorption.add(1, "%s买墙被真实成交 %d 次共 %s（卖墙成交 %d 次）：下方有真实接盘", m, w.Bid.FilledN, usdS(w.Bid.FilledUsd), w.Ask.FilledN)
		}
		for _, side := range []struct {
			name string
			s    WallSideStats
		}{{"买墙", w.Bid}, {"卖墙", w.Ask}} {
			if side.s.ApproachedN >= 5 && side.s.FilledN*2 >= side.s.ApproachedN {
				a.Spoofing.against("%s%s：价格逼近的 %d 个里 %d 个被真实成交", m, side.name, side.s.ApproachedN, side.s.FilledN)
			}
		}
	}
	a.Dump.finish()
	a.Absorption.finish()
	a.SpotControl.finish()
	a.Spoofing.finish()
	return a
}

func bandUsd(b *BookShape, pct float64) float64 {
	for _, bd := range b.Bands {
		if bd.Pct == pct {
			return bd.BidUsd + bd.AskUsd
		}
	}
	return 0
}

// ---------- the one-call report ----------

type AssessReport struct {
	Coin    string        `json:"coin"`
	From    int64         `json:"from"`
	To      int64         `json:"to"`
	Verdict Assessment    `json:"verdict"`
	Swap    *TapeBrief    `json:"swap,omitempty"`
	Spot    *TapeBrief    `json:"spot,omitempty"`
	Cross   *CrossStats   `json:"cross,omitempty"`
	Books   []*BookBrief  `json:"books,omitempty"`
	Walls   []*WallReport `json:"walls,omitempty"`
	Errors  []string      `json:"errors,omitempty"`
	Note    string        `json:"note"`
}

// TapeBrief is the compact per-market part of the report.
type TapeBrief struct {
	Coverage TapeCoverage `json:"coverage"`
	Summary  *TapeSummary `json:"summary,omitempty"`
	Sizes    []SizeBucket `json:"sizes,omitempty"`
	Clips    []ClipRun    `json:"topClips,omitempty"`
	Sweeps   *SweepStats  `json:"sweeps,omitempty"`
}

type BookBrief struct {
	Market    string     `json:"market"`
	Mid       float64    `json:"mid"`
	SpreadBps float64    `json:"spreadBps"`
	Bands     []BookBand `json:"bands"`
	Walls     []BookWall `json:"walls"`
	Depth2Pct float64    `json:"depth2PctOfTurnover"`
}

// Assess runs the whole check for one coin over [from, to).
func (e *Engine) Assess(ctx context.Context, coin string, from, to int64) (*AssessReport, error) {
	swapID, spotID, err := e.ResolvePair(coin)
	if err != nil {
		return nil, err
	}
	if swapID != "" {
		e.RequestTape(swapID)
	}
	r := &AssessReport{Coin: strings.TrimSuffix(firstNonEmpty(swapID, spotID), "-USDT-SWAP"), From: from, To: to,
		Note: "启发式判断：成交不带账户 id，“同一方”只能从尺寸和节奏推断；覆盖率 < 80% 时结论要打折。"}
	r.Coin = strings.TrimSuffix(r.Coin, "-USDT")
	q := TapeQuery{From: from, To: to, Sections: map[string]bool{"summary": true, "sizes": true, "clips": true, "sweeps": true},
		ClipMin: 5, ClipGapMin: 30, Top: 5}
	type res struct {
		st   *TapeStats
		book *BookShape
		wall *WallReport
	}
	run := func(id string) res {
		var out res
		if id == "" {
			return out
		}
		if d, err := e.LoadTape(ctx, id, from, to, true); err != nil {
			r.Errors = append(r.Errors, id+" trades: "+err.Error())
		} else {
			out.st = TapeStatsFor(d, q)
		}
		if b, err := e.BookShapeFor(ctx, id, BookQuery{BandPct: 3, Bands: []float64{0.5, 1, 2}, WallMult: 6, Top: 3}); err != nil {
			r.Errors = append(r.Errors, id+" book: "+err.Error())
		} else {
			out.book = b
		}
		if w, err := e.WallHistory(id, from, to, 0, 0, 30, 3, false); err == nil {
			out.wall = w
		}
		return out
	}
	ch := make(chan res, 1)
	go func() { ch <- run(spotID) }()
	sw := run(swapID)
	sp := <-ch
	r.Cross = crossStats(sw.st, sp.st)
	r.Verdict = assess(sw.st, sp.st, r.Cross, sw.wall, sp.wall, sw.book, sp.book)
	brief := func(s *TapeStats) *TapeBrief {
		if s == nil {
			return nil
		}
		b := &TapeBrief{Coverage: s.Coverage, Summary: s.Summary, Sizes: s.Sizes, Sweeps: s.Sweeps}
		b.Clips = s.Clips[:min(3, len(s.Clips))]
		if b.Sweeps != nil {
			b.Sweeps.Top = b.Sweeps.Top[:min(2, len(b.Sweeps.Top))]
		}
		roundSummary(b.Summary)
		return b
	}
	r.Swap, r.Spot = brief(sw.st), brief(sp.st)
	for _, b := range []*BookShape{sw.book, sp.book} {
		if b != nil {
			r.Books = append(r.Books, &BookBrief{Market: b.Market, Mid: b.Mid, SpreadBps: b.SpreadBps, Bands: b.Bands, Walls: b.Walls, Depth2Pct: b.Depth2PctTurn})
		}
	}
	for _, w := range []*WallReport{sw.wall, sp.wall} {
		if w != nil {
			r.Walls = append(r.Walls, w)
		}
	}
	return r, nil
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// roundSummary trims float noise for compact JSON.
func roundSummary(s *TapeSummary) {
	if s == nil {
		return
	}
	for _, p := range []*float64{&s.BuyUsd, &s.SellUsd, &s.DeltaUsd, &s.SizeP50, &s.SizeP90, &s.SizeP99, &s.SizeMax, &s.LargeBuyUsd, &s.LargeSellUsd} {
		*p = math.Round(*p)
	}
	for _, p := range []*float64{&s.BuyPct, &s.RetPct, &s.LargeDeltaPct, &s.RestDeltaPct, &s.Top1PctShare, &s.OrdersPerHour} {
		*p = round2(*p)
	}
	s.Vwap = roundSig(s.Vwap, 6)
	s.Wash.PctOfVol = round2(s.Wash.PctOfVol)
}

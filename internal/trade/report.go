package trade

import (
	"math"
	"sort"
)

// Report is the performance summary of an account (design §4). Money in USDT; ratios as fractions.
//
//   - net = equity − deposits (includes unrealized PnL); fees are paid (positive); funding is the
//     signed sum (positive = received); gross = net + fees − funding (price PnL before costs).
//   - maxDrawdown: largest peak-to-trough fall of the equity samples (1m live, per base bar backtest).
//   - sharpe: mean / stdev of daily (UTC+8 day, last sample per day) equity returns × √365; null
//     with fewer than 5 daily returns.
//   - trades: round trips per (instance, instrument) from open to flat; a trade's PnL is realized
//     PnL minus fees of its fills (funding excluded). winRate = winners / trades; profitFactor =
//     Σ winners / |Σ losers| (null without losers).
//   - turnover: Σ fill notional.
type Report struct {
	AccountID    int64          `json:"accountId"`
	Deposit      float64        `json:"deposit"`
	Equity       float64        `json:"equity"`
	Gross        float64        `json:"gross"`
	Net          float64        `json:"net"`
	NetPct       float64        `json:"netPct"`
	Realized     float64        `json:"realized"`
	Unrealized   float64        `json:"unrealized"`
	MaxDrawdown  float64        `json:"maxDrawdown"`
	Sharpe       *float64       `json:"sharpe"`
	SharpeBasis  string         `json:"sharpeBasis"`
	WinRate      *float64       `json:"winRate"`
	ProfitFactor *float64       `json:"profitFactor"`
	Turnover     float64        `json:"turnover"`
	Fees         float64        `json:"fees"`
	Funding      float64        `json:"funding"`
	Liquidations int            `json:"liquidations"`
	Trades       int            `json:"trades"`
	OpenTrades   int            `json:"openTrades"`
	ByInst       []InstReport   `json:"byInst"`
	Signals      map[string]int `json:"signals"` // ordered, rejected, skipped (AI + user + expired), recorded, pending
	Outcomes     map[string]int `json:"outcomes"`
	From         int64          `json:"from,omitempty"`
	To           int64          `json:"to,omitempty"`
}

type InstReport struct {
	InstID   string  `json:"instId"`
	Trades   int     `json:"trades"`
	Wins     int     `json:"wins"`
	Pnl      float64 `json:"pnl"` // realized − fees + funding
	Fees     float64 `json:"fees"`
	Funding  float64 `json:"funding"`
	Turnover float64 `json:"turnover"`
}

type fillRow struct {
	instance             int64
	inst, posSide        string
	qty, px, ct, fee, rp float64
	liq, reduce, closes  bool
	ts                   int64
}

func (s *Service) Report(id int64) (Report, error) {
	b, err := s.book(id)
	if err != nil {
		return Report{}, err
	}
	b.mu.Lock()
	acc := b.view()
	unreal := 0.0
	for _, p := range b.pos {
		unreal += b.upnl(p)
	}
	b.mu.Unlock()
	r := Report{AccountID: id, Deposit: acc.Deposit, Equity: acc.Equity, Unrealized: round(unreal, 6), SharpeBasis: "daily",
		Signals: map[string]int{"ordered": 0, "rejected": 0, "skipped": 0, "recorded": 0, "pending": 0}, Outcomes: map[string]int{}, ByInst: []InstReport{}}
	if acc.Mode == ModeBacktest {
		r.From, r.To = acc.From, acc.To
	}
	rows, err := s.db.Query(`SELECT instance_id, inst_id, pos_side, qty, px, ct_val, fee, realized_pnl, liquidation, reduce, closes, ts
		FROM trade_fills WHERE account_id=? ORDER BY id`, id)
	if err != nil {
		return r, err
	}
	var fills []fillRow
	for rows.Next() {
		var f fillRow
		var liq, red, cl int
		if err := rows.Scan(&f.instance, &f.inst, &f.posSide, &f.qty, &f.px, &f.ct, &f.fee, &f.rp, &liq, &red, &cl, &f.ts); err != nil {
			rows.Close()
			return r, err
		}
		f.liq, f.reduce, f.closes = liq == 1, red == 1, cl == 1
		fills = append(fills, f)
	}
	rows.Close()
	funding := map[string]float64{}
	lrows, err := s.db.Query(`SELECT inst_id, kind, amount FROM trade_ledger WHERE account_id=? AND kind IN ('funding','fee')`, id)
	if err != nil {
		return r, err
	}
	for lrows.Next() {
		var inst, kind string
		var amt float64
		if lrows.Scan(&inst, &kind, &amt) == nil && kind == "funding" {
			funding[inst] += amt
			r.Funding += amt
		}
	}
	lrows.Close()
	r.computeTrades(fills, funding)
	r.Net = acc.Equity - acc.Deposit
	if acc.Deposit > 0 {
		r.NetPct = r.Net / acc.Deposit
	}
	r.Gross = r.Net + r.Fees - r.Funding
	eq, err := s.equityPoints(id, 0, math.MaxInt64, 0)
	if err != nil {
		return r, err
	}
	r.MaxDrawdown = maxDrawdown(eq)
	r.Sharpe = dailySharpe(eq)
	srows, err := s.db.Query(`SELECT outcome, COUNT(*) FROM trade_signals WHERE account_id=? GROUP BY outcome`, id)
	if err != nil {
		return r, err
	}
	for srows.Next() {
		var o string
		var n int
		if srows.Scan(&o, &n) == nil {
			r.Outcomes[o] = n
			switch SignalOutcome(o) {
			case OutOrdered:
				r.Signals["ordered"] += n
			case OutRejected:
				r.Signals["rejected"] += n
			case OutSkippedAI, OutSkippedUs, OutExpired:
				r.Signals["skipped"] += n
			case OutRecorded:
				r.Signals["recorded"] += n
			case OutPending:
				r.Signals["pending"] += n
			}
		}
	}
	srows.Close()
	r.roundAll()
	return r, nil
}

// computeTrades builds round trips from fills (ordered by id) and the per-instrument table.
func (r *Report) computeTrades(fills []fillRow, funding map[string]float64) {
	type open struct {
		qty, pnl float64
	}
	opens := map[posKey]*open{}
	by := map[string]*InstReport{}
	inst := func(id string) *InstReport {
		if by[id] == nil {
			by[id] = &InstReport{InstID: id}
		}
		return by[id]
	}
	var wins, losses float64
	for _, f := range fills {
		k := posKey{f.instance, f.inst}
		ir := inst(f.inst)
		notional := f.qty * f.px * f.ct
		r.Turnover += notional
		r.Fees += f.fee
		r.Realized += f.rp
		ir.Turnover += notional
		ir.Fees += f.fee
		ir.Pnl += f.rp - f.fee
		if f.liq {
			r.Liquidations++
		}
		t := opens[k]
		if t == nil {
			if f.reduce {
				continue
			}
			t = &open{}
			opens[k] = t
		}
		t.pnl += f.rp - f.fee
		if f.reduce {
			t.qty -= f.qty
		} else {
			t.qty += f.qty
		}
		if f.closes || (f.reduce && t.qty <= qtyEps) {
			r.Trades++
			ir.Trades++
			if t.pnl > 0 {
				ir.Wins++
				wins += t.pnl
			} else {
				losses += -t.pnl
			}
			delete(opens, k)
		}
	}
	r.OpenTrades = len(opens)
	nWins := 0
	for id, ir := range by {
		ir.Funding = funding[id]
		ir.Pnl += funding[id]
		nWins += ir.Wins
	}
	for id, f := range funding {
		if by[id] == nil {
			by[id] = &InstReport{InstID: id, Funding: f, Pnl: f}
		}
	}
	if r.Trades > 0 {
		w := float64(nWins) / float64(r.Trades)
		r.WinRate = &w
	}
	if losses > 0 {
		pf := wins / losses
		r.ProfitFactor = &pf
	}
	for _, ir := range by {
		r.ByInst = append(r.ByInst, *ir)
	}
	sort.Slice(r.ByInst, func(i, j int) bool { return r.ByInst[i].Pnl > r.ByInst[j].Pnl })
}

func maxDrawdown(eq [][2]float64) float64 {
	peak, dd := 0.0, 0.0
	for _, p := range eq {
		if p[1] > peak {
			peak = p[1]
		}
		if peak > 0 {
			dd = math.Max(dd, (peak-p[1])/peak)
		}
	}
	return dd
}

func dailySharpe(eq [][2]float64) *float64 {
	var closes []float64
	lastDay := int64(-1)
	for _, p := range eq {
		d := dayKey(int64(p[0]))
		if d != lastDay {
			closes = append(closes, p[1])
			lastDay = d
		} else {
			closes[len(closes)-1] = p[1]
		}
	}
	var rets []float64
	for i := 1; i < len(closes); i++ {
		if closes[i-1] > 0 {
			rets = append(rets, closes[i]/closes[i-1]-1)
		}
	}
	if len(rets) < 5 {
		return nil
	}
	mean := 0.0
	for _, x := range rets {
		mean += x
	}
	mean /= float64(len(rets))
	v := 0.0
	for _, x := range rets {
		v += (x - mean) * (x - mean)
	}
	sd := math.Sqrt(v / float64(len(rets)-1))
	if sd == 0 {
		return nil
	}
	sh := mean / sd * math.Sqrt(365)
	return &sh
}

func (r *Report) roundAll() {
	for _, p := range []*float64{&r.Gross, &r.Net, &r.Realized, &r.Turnover, &r.Fees, &r.Funding} {
		*p = round(*p, 6)
	}
	r.NetPct, r.MaxDrawdown = round(r.NetPct, 6), round(r.MaxDrawdown, 6)
	for _, p := range []*float64{r.Sharpe, r.WinRate, r.ProfitFactor} {
		if p != nil {
			*p = round(*p, 4)
		}
	}
	for i := range r.ByInst {
		x := &r.ByInst[i]
		x.Pnl, x.Fees, x.Funding, x.Turnover = round(x.Pnl, 6), round(x.Fees, 6), round(x.Funding, 6), round(x.Turnover, 4)
	}
}

// equityPoints returns [ts, equity] in [from, to], downsampled to at most limit points (last
// sample per time bucket; limit 0 = all).
func (s *Service) equityPoints(id, from, to int64, limit int) ([][2]float64, error) {
	var n int
	var lo, hi int64
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(MIN(ts),0), COALESCE(MAX(ts),0) FROM trade_equity WHERE account_id=? AND ts>=? AND ts<=?`, id, from, to).Scan(&n, &lo, &hi)
	q := `SELECT ts, equity FROM trade_equity WHERE account_id=? AND ts>=? AND ts<=? ORDER BY ts`
	args := []any{id, from, to}
	if limit > 0 && n > limit {
		bucket := (hi-lo)/int64(limit) + 1
		q = `SELECT MAX(ts), equity FROM trade_equity WHERE account_id=? AND ts>=? AND ts<=? GROUP BY (ts-?)/? ORDER BY 1`
		args = append(args, lo, bucket)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][2]float64{}
	for rows.Next() {
		var ts int64
		var v float64
		if err := rows.Scan(&ts, &v); err != nil {
			return nil, err
		}
		out = append(out, [2]float64{float64(ts), round(v, 4)})
	}
	return out, rows.Err()
}

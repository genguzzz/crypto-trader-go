package trade

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

type posKey struct {
	inst int64
	id   string
}

// posState is a position plus the bookkeeping the broker needs.
type posState struct {
	Position
	FundedTo int64  `json:"fundedTo"`        // last funding settlement handled (ms)
	Group    string `json:"group,omitempty"` // OCO group of its current stop/target
}

// groupSpec is the stop/target to attach once an entry order fills.
type groupSpec struct {
	stop, target float64
	signal       Signal
}

// book is one account's runtime state. Every mutation happens under mu and is written to the
// database in one transaction before memory is updated.
type book struct {
	s  *Service
	mu sync.Mutex

	acc      Account               // stored fields; derived ones are filled by view()
	name     atomic.Value          // account name for events built outside the lock
	mdp      atomic.Pointer[mdBox] // live data, or the replayer while/after a backtest runs
	barModel bool                  // backtest fill model

	balance   float64 // ledger sum (deposits, realized PnL, fees, funding, liquidations)
	deposit   float64
	pos       map[posKey]*posState
	orders    map[int64]*Order // open orders (waiting_trigger, live, partially_filled)
	groups    map[string]groupSpec
	lastClose map[string]int64 // instrument → last time a position in it was fully closed
	pending   map[posKey]int64 // (instance, instrument) → signal waiting for AI / confirmation / processing
	marks     map[string]float64
	day       dayState

	bt    *btRun
	eqBuf [][2]float64
}

type mdBox struct{ MarketData }

func (b *book) market() MarketData { return b.mdp.Load().MarketData }

func (b *book) setMarket(md MarketData) { b.mdp.Store(&mdBox{md}) }

func newBook(s *Service, a Account, md MarketData) *book {
	b := &book{s: s, acc: a, barModel: a.Mode == ModeBacktest, pos: map[posKey]*posState{}, orders: map[int64]*Order{},
		groups: map[string]groupSpec{}, lastClose: map[string]int64{}, pending: map[posKey]int64{}, marks: map[string]float64{},
	}
	b.setMarket(md)
	b.name.Store(a.Name)
	return b
}

// loadBooks restores every account's runtime state from the database.
func (s *Service) loadBooks() error {
	rows, err := s.db.Query(`SELECT ` + accountCols + ` FROM trade_accounts ORDER BY id`)
	if err != nil {
		return err
	}
	var accs []Account
	var days []dayState
	for rows.Next() {
		a, d, err := scanAccount(rows)
		if err != nil {
			rows.Close()
			return err
		}
		accs = append(accs, a)
		days = append(days, d)
	}
	rows.Close()
	for i, a := range accs {
		if a.Backtest != nil && a.Backtest.State == "running" {
			a.Backtest.State, a.Backtest.Error = "failed", "服务重启，回测中断"
			s.db.Exec(`UPDATE trade_accounts SET backtest=? WHERE id=?`, mustJSON(a.Backtest), a.ID)
		}
		b := newBook(s, a, s.md)
		b.day = days[i]
		if err := b.restore(); err != nil {
			return err
		}
		s.books[a.ID] = b
	}
	return nil
}

func (b *book) restore() error {
	db := b.s.db
	id := b.acc.ID
	db.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM trade_ledger WHERE account_id=?`, id).Scan(&b.balance)
	db.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM trade_ledger WHERE account_id=? AND kind='deposit'`, id).Scan(&b.deposit)
	rows, err := db.Query(`SELECT body FROM trade_positions WHERE account_id=?`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var body string
		var p posState
		if rows.Scan(&body) == nil && json.Unmarshal([]byte(body), &p) == nil {
			b.pos[posKey{p.InstanceID, p.InstID}] = &p
			b.marks[p.InstID] = p.MarkPx
		}
	}
	rows.Close()
	rows, err = db.Query(`SELECT `+orderCols+` FROM trade_orders WHERE account_id=? AND state IN (?,?,?)`, id, OsTrigger, OsLive, OsPartial)
	if err != nil {
		return err
	}
	for rows.Next() {
		if o, err := scanOrder(rows); err == nil {
			b.orders[o.ID] = &o
		}
	}
	rows.Close()
	rows, err = db.Query(`SELECT inst_id, MAX(ts) FROM trade_fills WHERE account_id=? AND closes=1 GROUP BY inst_id`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var inst string
		var ts int64
		if rows.Scan(&inst, &ts) == nil {
			b.lastClose[inst] = ts
		}
	}
	rows.Close()
	return nil
}

// ---------- derived values (caller holds mu) ----------

func dir(posSide string) float64 {
	if posSide == "short" {
		return -1
	}
	return 1
}

func (b *book) mark(inst string, fallback float64) float64 {
	if m := b.marks[inst]; m > 0 {
		return m
	}
	return fallback
}

func (b *book) upnl(p *posState) float64 {
	return (b.mark(p.InstID, p.EntryPx) - p.EntryPx) * p.Qty * p.CtVal * dir(p.PosSide)
}

func (b *book) equity() float64 {
	eq := b.balance
	for _, p := range b.pos {
		eq += b.upnl(p)
	}
	return eq
}

func (b *book) usedMargin() float64 {
	m := 0.0
	for _, p := range b.pos {
		m += p.Margin
	}
	return m
}

func (b *book) instMargin(inst string) float64 {
	m := 0.0
	for _, p := range b.pos {
		if p.InstID == inst {
			m += p.Margin
		}
	}
	return m
}

func (b *book) cash() float64 { return b.balance - b.usedMargin() }

func (b *book) dayPnl() float64 {
	if b.day.key == 0 {
		return 0
	}
	return b.equity() - b.day.start - b.day.deposits
}

func (b *book) view() Account {
	a := b.acc
	if a.Backtest != nil {
		bt := *a.Backtest
		a.Backtest = &bt
	}
	a.Deposit = b.deposit
	a.Equity = round(b.equity(), 4)
	a.Cash = round(b.cash(), 4)
	a.DayPnl = round(b.dayPnl(), 4)
	a.Halted = b.day.halted
	a.OpenCount = len(b.pos)
	return a
}

func (b *book) position(p *posState) Position {
	out := p.Position
	out.MarkPx = b.mark(p.InstID, p.EntryPx)
	out.UnrealizedPnl = round(b.upnl(p), 6)
	return out
}

func (b *book) positionsSorted(instance int64) []Position {
	out := []Position{}
	for k, p := range b.pos {
		if instance == 0 || k.inst == instance {
			out = append(out, b.position(p))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].InstID != out[j].InstID {
			return out[i].InstID < out[j].InstID
		}
		return out[i].InstanceID < out[j].InstanceID
	})
	return out
}

func (b *book) openOrders() []Order {
	out := []Order{}
	for _, o := range b.orders {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// liqPrice is where the isolated position's remaining margin equals maintenance margin
// (linear USDT contracts, fees ignored).
func liqPrice(posSide string, entry, margin, qty, ctVal, mmr float64) float64 {
	if qty <= 0 || ctVal <= 0 {
		return 0
	}
	per := margin / (qty * ctVal)
	if posSide == "short" {
		return (entry + per) / (1 + mmr)
	}
	return math.Max(0, (entry-per)/(1-mmr))
}

// ---------- SignalSink reads ----------

func (s *Service) bookOfInstance(instanceID int64) *book {
	if ri := s.instRT(instanceID); ri != nil {
		return ri.b
	}
	var acct int64
	if s.db.QueryRow(`SELECT account_id FROM trade_instances WHERE id=?`, instanceID).Scan(&acct) != nil {
		return nil
	}
	b, _ := s.book(acct)
	return b
}

func (s *Service) Position(instanceID int64, instID string) (Position, bool) {
	b := s.bookOfInstance(instanceID)
	if b == nil {
		return Position{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.pos[posKey{instanceID, instID}]
	if p == nil {
		return Position{}, false
	}
	return b.position(p), true
}

func (s *Service) Positions(instanceID int64) []Position {
	b := s.bookOfInstance(instanceID)
	if b == nil {
		return []Position{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.positionsSorted(instanceID)
}

func (s *Service) AccountView(instanceID int64) (Account, bool) {
	b := s.bookOfInstance(instanceID)
	if b == nil {
		return Account{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.view(), true
}

// refPrice is the reference (mid or last) price for sizing and slippage.
func refPrice(q Quote) float64 {
	if q.Bid > 0 && q.Ask >= q.Bid {
		return (q.Bid + q.Ask) / 2
	}
	return q.Last
}

func (b *book) quote(ctx context.Context, inst string) (Quote, error) {
	return b.market().Quote(ctx, inst)
}

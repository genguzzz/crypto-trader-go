package trade

import (
	"database/sql"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// AccountInput creates an account.
type AccountInput struct {
	Name    string  `json:"name"`
	Mode    Mode    `json:"mode"`
	Deposit float64 `json:"deposit"`
	Limits  *Limits `json:"limits"`
	From    int64   `json:"from"`
	To      int64   `json:"to"`
}

// AccountPatch edits an account (nil = unchanged).
type AccountPatch struct {
	Name     *string `json:"name"`
	Limits   *Limits `json:"limits"`
	Archived *bool   `json:"archived"`
}

const maxBacktestSpan = 365 * dayMs

func validLimits(l Limits) (Limits, error) {
	bad := func(msg string) (Limits, error) { return l, errBad(msg) }
	for _, x := range []float64{l.MaxLeverage, l.RiskPct, l.MaxInstPct, l.DailyLossPct, l.SlippageBp, l.TakerFeeBp, l.MakerFeeBp} {
		if !finite(x) || x < 0 {
			return bad("限额需为非负有限数")
		}
	}
	switch {
	case l.MaxPositions < 1 || l.MaxPositions > 100:
		return bad("最大持仓数需在 1–100")
	case l.MaxLeverage < 1 || l.MaxLeverage > 100:
		return bad("最大杠杆需在 1–100")
	case l.RiskPct < 0.01 || l.RiskPct > 5:
		return bad("单笔风险 riskPct 需在 1%–500%（0.01–5）")
	case l.MaxInstPct <= 0 || l.MaxInstPct > 1:
		return bad("单合约保证金上限 maxInstPct 需在 (0, 1]")
	case l.DailyLossPct > 1:
		return bad("日内亏损上限 dailyLossPct 需在 [0, 1]（0 = 不熔断）")
	case l.SlippageBp <= 0 || l.SlippageBp > 500:
		return bad("滑点容忍需在 (0, 500] bp")
	case l.CooldownMin < 0 || l.CooldownMin > 7*24*60:
		return bad("冷却时间需在 0–10080 分钟")
	case l.TakerFeeBp > 100 || l.MakerFeeBp > 100:
		return bad("手续费需不超过 100 bp")
	case l.ConfirmTimeoutS < 0 || l.ConfirmTimeoutS > 3600:
		return bad("确认超时需在 0–3600 秒")
	}
	switch l.Confirm {
	case "":
		l.Confirm = ConfirmOff
	case ConfirmOff, ConfirmOpen, ConfirmAll:
	default:
		return bad("确认模式需为 off / open / all")
	}
	switch l.ConfirmDefault {
	case "":
		l.ConfirmDefault = "skip"
	case "skip", "execute":
	default:
		return bad("确认默认需为 skip / execute")
	}
	if l.ConfirmTimeoutS == 0 {
		l.ConfirmTimeoutS = 120
	}
	return l, nil
}

func validName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || utf8.RuneCountInString(n) > 40 {
		return n, errBad("名称需为 1–40 个字")
	}
	return n, nil
}

// CreateAccount creates a live paper or backtest account with its initial deposit.
func (s *Service) CreateAccount(in AccountInput) (Account, error) {
	name, err := validName(in.Name)
	if err != nil {
		return Account{}, err
	}
	switch in.Mode {
	case ModeLive, ModeBacktest:
	case ModeReal:
		return Account{}, errBad("实盘模式未开放：本系统只做模拟交易")
	default:
		return Account{}, errBad("模式需为 live / backtest")
	}
	if !finite(in.Deposit) || in.Deposit <= 0 || in.Deposit > 1e9 {
		return Account{}, errBad("初始资金需在 (0, 1e9] USDT")
	}
	l := DefaultLimits()
	if in.Limits != nil {
		l = *in.Limits
	}
	if l, err = validLimits(l); err != nil {
		return Account{}, err
	}
	now := time.Now().UnixMilli()
	a := Account{Name: name, Mode: in.Mode, Limits: l, CreatedAt: now}
	if in.Mode == ModeBacktest {
		in.From, in.To = in.From/minuteMs*minuteMs, in.To/minuteMs*minuteMs
		if in.From <= 0 || in.To <= in.From {
			return Account{}, errBad("回测区间需满足 from < to")
		}
		if in.To > now {
			return Account{}, errBad("回测结束时间不能晚于现在")
		}
		if in.To-in.From > maxBacktestSpan {
			return Account{}, errBad("回测区间最长 365 天")
		}
		a.From, a.To = in.From, in.To
		a.Backtest = &BacktestStatus{State: "idle"}
	}
	start := now
	if a.Mode == ModeBacktest {
		start = a.From
	} else if s.md != nil {
		start = s.md.Now()
	}
	err = s.tx(func(tx *sql.Tx) error {
		id, err := insertAccount(tx, a)
		if err != nil {
			return err
		}
		a.ID = id
		_, err = insertLedger(tx, LedgerEntry{AccountID: id, Kind: "deposit", Amount: in.Deposit, Ts: start})
		return err
	})
	if err != nil {
		return Account{}, err
	}
	md := s.md
	b := newBook(s, a, md)
	b.balance, b.deposit = in.Deposit, in.Deposit
	if a.Mode == ModeLive {
		b.day = dayState{key: dayKey(start), start: in.Deposit}
		saveDayState(s.db, a.ID, b.day)
	}
	s.mu.Lock()
	s.books[a.ID] = b
	s.mu.Unlock()
	s.changed(a.ID, "account")
	return b.locked(func() Account { return b.view() }), nil
}

func (b *book) locked(fn func() Account) Account {
	b.mu.Lock()
	defer b.mu.Unlock()
	return fn()
}

// snapshot is the stored account fields (read under the lock; safe to use afterwards).
func (b *book) snapshot() Account {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.acc
}

// UpdateAccount renames, changes limits or (un)archives an account. Archiving a live account
// stops its instances; positions stay open until closed.
func (s *Service) UpdateAccount(id int64, p AccountPatch) (Account, error) {
	b, err := s.book(id)
	if err != nil {
		return Account{}, err
	}
	b.mu.Lock()
	next := b.acc
	b.mu.Unlock()
	if p.Name != nil {
		if next.Name, err = validName(*p.Name); err != nil {
			return Account{}, err
		}
	}
	if p.Limits != nil {
		if next.Limits, err = validLimits(*p.Limits); err != nil {
			return Account{}, err
		}
	}
	if p.Archived != nil {
		next.Archived = *p.Archived
	}
	if b.barModel && b.running() && p.Limits != nil {
		return Account{}, errConflict("回测运行中，不能修改限额")
	}
	if _, err := s.db.Exec(`UPDATE trade_accounts SET name=?, limits=?, archived=? WHERE id=?`, next.Name, mustJSON(next.Limits), b2i(next.Archived), id); err != nil {
		return Account{}, err
	}
	b.mu.Lock()
	b.acc.Name, b.acc.Limits, b.acc.Archived = next.Name, next.Limits, next.Archived
	b.name.Store(next.Name)
	b.mu.Unlock()
	if next.Archived {
		for _, ri := range s.accountRTs(id) {
			if !b.barModel {
				s.stopWorker(ri)
				s.setInstanceState(ri.inst.ID, StStopped, "账户已归档")
			}
		}
		s.updateKeep()
	}
	s.changed(id, "account")
	return b.locked(func() Account { return s.withInstances(b.view()) }), nil
}

func (s *Service) accountRTs(id int64) []*instRT {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*instRT
	for _, ri := range s.insts {
		if ri.b.acc.ID == id {
			out = append(out, ri)
		}
	}
	return out
}

func (s *Service) withInstances(a Account) Account {
	s.db.QueryRow(`SELECT COUNT(*) FROM trade_instances WHERE account_id=?`, a.ID).Scan(&a.Instances)
	return a
}

// Deposit adds funds to a live account.
func (s *Service) Deposit(id int64, amount float64) (Account, error) {
	b, err := s.book(id)
	if err != nil {
		return Account{}, err
	}
	if b.barModel {
		return Account{}, errBad("回测账户的资金在创建时设定")
	}
	if !finite(amount) || amount <= 0 || amount > 1e9 {
		return Account{}, errBad("金额需在 (0, 1e9] USDT")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.market().Now()
	b.rollDay(now)
	if _, err := insertLedger(s.db, LedgerEntry{AccountID: id, Kind: "deposit", Amount: amount, Ts: now}); err != nil {
		return Account{}, err
	}
	b.balance += amount
	b.deposit += amount
	b.day.deposits += amount
	saveDayState(s.db, id, b.day)
	s.changed(id, "account")
	return s.withInstances(b.view()), nil
}

// Accounts lists all accounts (archived last).
func (s *Service) Accounts() []Account {
	s.mu.RLock()
	bs := make([]*book, 0, len(s.books))
	for _, b := range s.books {
		bs = append(bs, b)
	}
	s.mu.RUnlock()
	out := make([]Account, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.locked(func() Account { return s.withInstances(b.view()) }))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Archived != out[j].Archived {
			return !out[i].Archived
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// AccountDetail is an account with its positions and open orders.
type AccountDetail struct {
	Account   Account    `json:"account"`
	Positions []Position `json:"positions"`
	Orders    []Order    `json:"orders"`
}

func (s *Service) AccountDetail(id int64) (AccountDetail, error) {
	b, err := s.book(id)
	if err != nil {
		return AccountDetail{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return AccountDetail{Account: s.withInstances(b.view()), Positions: b.positionsSorted(0), Orders: b.openOrders()}, nil
}

func (s *Service) Account(id int64) (Account, error) {
	b, err := s.book(id)
	if err != nil {
		return Account{}, err
	}
	return b.locked(func() Account { return s.withInstances(b.view()) }), nil
}

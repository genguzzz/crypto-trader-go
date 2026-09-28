package market

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"crypto-trader-go/internal/features"
	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/refdata"
	"crypto-trader-go/internal/rules"
	"crypto-trader-go/internal/store"
)

const BTC = "BTC-USDT-SWAP"

type Config struct {
	Threshold   float64         // 12h USDT turnover threshold (strictly greater)
	TopN        int             // cap of the active pool
	Categories  map[string]bool // instCategory filter; nil = all
	ReplayHours int             // hourly replay depth for rule records
	StorageCap  int64           // bytes; SQLite size cap split across tables (retention.go)
}

type Row struct {
	InstID      string                 `json:"instId"`
	Category    string                 `json:"category"`
	Status      string                 `json:"status"` // live | stale | warming | new
	Active      bool                   `json:"active"`
	Rank        int                    `json:"rank,omitempty"`
	Turnover12h float64                `json:"turnover12h"`
	Provisional bool                   `json:"provisional,omitempty"`
	Have        int                    `json:"have"` // bars present in the 720 window
	Last        float64                `json:"last"`
	Values      features.Values        `json:"values"`
	Pct         map[string]float64     `json:"pct"`
	Scores      map[string]rules.Score `json:"scores,omitempty"`
	Watch       bool                   `json:"watch,omitempty"`
	Eligible    bool                   `json:"eligible"` // in the recording universe (else: watched adhoc)
	HasSpot     bool                   `json:"hasSpot"`
	ListTime    int64                  `json:"listTime"`
	Freshness   int64                  `json:"freshnessMs"`    // now - last confirmed bar close
	MarketCap   float64                `json:"mcap,omitempty"` // CoinGecko USD market cap (crypto only; display)
}

type Removed struct {
	InstID string  `json:"instId"`
	Reason string  `json:"reason"`
	At     int64   `json:"at"`
	Last   float64 `json:"turnover12h"`
}

type Coverage struct {
	Eligible int `json:"eligible"`
	Ready    int `json:"ready"`
	Warming  int `json:"warming"`
	Stale    int `json:"stale"`
	New      int `json:"new"`
	Active   int `json:"active"`
}

type Snapshot struct {
	Seq         int64     `json:"seq"`
	AsOfMinute  int64     `json:"asOfMinute"` // open time of the last confirmed 1m bar included
	DecisionTs  int64     `json:"decisionTs"`
	ComputedAt  int64     `json:"computedAt"`
	Provisional bool      `json:"provisional"`
	Preview     bool      `json:"preview"`   // values/scores from the forming minute (repaint)
	PreviewAt   int64     `json:"previewAt"` // when the 10s preview was computed
	Phase       string    `json:"phase"`
	Threshold   float64   `json:"threshold"`
	TopN        int       `json:"topN"`
	Coverage    Coverage  `json:"coverage"`
	Rows        []Row     `json:"rows"`
	Removed     []Removed `json:"removed"`
}

type Event struct {
	Type string
	Data any
}

type Engine struct {
	cfgMu       sync.RWMutex
	cfg         Config
	defaultRule string

	histMu      sync.Mutex
	histRunning bool
	histPending []string

	replayRunning atomic.Bool
	replayPending atomic.Bool
	rest          *okx.REST
	miMu          sync.Mutex
	miCache       map[string]markIdx // mark / index price for open detail views
	st            *store.Store
	pub           *okx.WS
	biz           *okx.WS

	mu       sync.RWMutex
	insts    map[string]*Inst
	spotIDs  map[string]bool
	watch    map[string]bool
	catalog  map[string]okx.Instrument // every live USDT-margined linear swap (search)
	tickers  map[string]okx.Ticker     // latest SWAP tickers (30s REST)
	views    map[string]int64          // detail last opened (retention tier, adhoc streaming)
	lastPool map[string]int64          // last decision minute in the active pool
	lastMin  int64
	removed  map[string]Removed
	prevAct  map[string]float64

	rmu   sync.RWMutex
	rules map[string]*rules.Rule

	snap       atomic.Pointer[Snapshot]
	seq        atomic.Int64
	phase      atomic.Value
	bfDone     atomic.Int64
	bfTotal    atomic.Int64
	started    time.Time
	replay     *replayTable
	replayMu   sync.Mutex
	replayMsg  atomic.Value
	historyMsg atomic.Value

	barCh  chan store.InstBar
	oiCh   chan store.OISample
	fdCh   chan store.FundingSample
	flowCh chan store.FlowRow

	subMu sync.Mutex
	subs  map[chan Event]bool

	trackMu    sync.Mutex
	trackCache map[string]trackEntry

	Ref *refdata.Cache // optional CoinGecko market cap / supply (display only)

	eff       atomic.Pointer[effAges]
	retReport atomic.Pointer[RetentionReport]
	retKick   chan struct{}
	dbIDs     map[string]bool // instruments present in the DB (refreshed every 6h by retention)
	dbIDsAt   time.Time
}

func New(cfg Config, rest *okx.REST, st *store.Store) *Engine {
	e := &Engine{cfg: cfg, rest: rest, st: st, insts: map[string]*Inst{}, spotIDs: map[string]bool{},
		watch: map[string]bool{}, catalog: map[string]okx.Instrument{}, tickers: map[string]okx.Ticker{}, views: map[string]int64{},
		lastPool: map[string]int64{}, retKick: make(chan struct{}, 1), removed: map[string]Removed{}, prevAct: map[string]float64{},
		rules: map[string]*rules.Rule{}, barCh: make(chan store.InstBar, 20000), oiCh: make(chan store.OISample, 20000),
		fdCh: make(chan store.FundingSample, 5000), flowCh: make(chan store.FlowRow, 20000), subs: map[chan Event]bool{}, trackCache: map[string]trackEntry{}, started: time.Now()}
	e.phase.Store("starting")
	e.replayMsg.Store("等待实时数据就绪")
	e.historyMsg.Store("等待实时数据就绪")
	e.pub = okx.NewWS("public", okx.PublicWSURL)
	e.biz = okx.NewWS("business", okx.BusinessWSURL)
	e.pub.OnMessage = e.onPublic
	e.biz.OnMessage = e.onBusiness
	e.biz.OnConnect = e.onReconnect
	e.pub.OnConnect = e.onPublicReconnect
	e.snap.Store(&Snapshot{Phase: "starting", Threshold: cfg.Threshold, TopN: cfg.TopN})
	return e
}

func (e *Engine) Snapshot() *Snapshot { return e.snap.Load() }
func (e *Engine) Phase() string       { return e.phase.Load().(string) }

// ---------- lifecycle ----------

func (e *Engine) Start(ctx context.Context) error {
	if err := e.loadRules(); err != nil {
		return err
	}
	e.loadSettings()
	if w, err := e.st.Watchlist(); err == nil {
		for _, id := range w {
			e.watch[id] = true
		}
	}
	e.migrate()
	if v, err := e.st.Views(); err == nil {
		e.views = v
	}
	if p, err := e.st.LastInPool(time.Now().Add(-PoolGrace).UnixMilli()); err == nil {
		e.lastPool = p
	}
	go e.writer(ctx)
	go e.bootstrap(ctx)
	go e.retentionLoop(ctx)
	go e.flowLoop(ctx)
	return nil
}

func (e *Engine) bootstrap(ctx context.Context) {
	for {
		if err := e.refreshInstruments(ctx); err != nil {
			log.Printf("instruments: %v (retrying)", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}
		break
	}
	e.loadFromDB()
	// WS first so nothing is missed while REST backfills.
	e.updateSubscriptions()
	go e.biz.Run(ctx)
	go e.pub.Run(ctx)
	e.refreshTickers(ctx)
	e.refreshOIFunding(ctx)

	e.phase.Store("bootstrap-5m")
	e.fastPool(ctx)
	go e.universeLoop(ctx)
	go e.previewLoop(ctx)
	go e.periodic(ctx)

	e.phase.Store("backfill-1m")
	e.backfill(ctx)
	e.phase.Store("live")
	e.historyMsg.Store("排队中")
	log.Printf("bootstrap complete in %s, REST requests %d", time.Since(e.started).Round(time.Second), e.rest.Requests.Load())
	e.requestReplay()
}

func (e *Engine) refreshInstruments(ctx context.Context) error {
	sw, err := e.rest.Instruments(ctx, "SWAP")
	if err != nil {
		return err
	}
	sp, err := e.rest.Instruments(ctx, "SPOT")
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spotIDs = map[string]bool{}
	for _, s := range sp {
		if s.State == "live" && s.QuoteCcy == "USDT" {
			e.spotIDs[s.InstID] = true
		}
	}
	e.catalog = map[string]okx.Instrument{}
	for _, i := range sw {
		if catalogOK(i) {
			e.catalog[i.InstID] = i
		}
	}
	seen := map[string]bool{}
	for _, i := range sw {
		ok := catalogOK(i) && i.RuleType != "pre_market"
		if e.conf().Categories != nil && !e.conf().Categories[i.Category] {
			ok = false
		}
		if !ok {
			continue
		}
		seen[i.InstID] = true
		in := e.insts[i.InstID]
		if in == nil {
			in = newInst(i.InstID)
			e.insts[i.InstID] = in
		}
		in.Category, in.ListTime, in.Eligible, in.CtVal, in.TickSz = i.Category, i.ListTime, true, i.CtVal, i.TickSz
		if e.spotIDs[spotOf(i.InstID)] {
			in.SpotID = spotOf(i.InstID)
		} else {
			in.SpotID = ""
		}
	}
	for id, in := range e.insts {
		if !seen[id] {
			in.Eligible = false
			if i, ok := e.catalog[id]; ok { // adhoc (search/watch): keep metadata current
				in.Category, in.ListTime, in.CtVal, in.TickSz = i.Category, i.ListTime, i.CtVal, i.TickSz
			}
		}
	}
	return nil
}

func (e *Engine) eligibleIDs() []string {
	var ids []string
	for id, in := range e.insts {
		if in.Eligible {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (e *Engine) loadFromDB() {
	now := time.Now().UnixMilli()
	e.mu.Lock()
	defer e.mu.Unlock()
	n, err := e.st.EachBarSince("candles_1m", now-keep1m*minuteMs, func(id string, b store.Bar) {
		if in := e.insts[id]; in != nil {
			in.putBar(b)
		}
	})
	if err != nil {
		log.Printf("load 1m: %v", err)
	}
	e.st.EachBarSince("candles_1h", now-(HourlyDepth+48)*hourMs, func(id string, b store.Bar) {
		if in := e.insts[id]; in != nil {
			in.hourly[b.Ts] = b
		}
	})
	ois, _ := e.st.AllOISince(now - keepOI)
	for id, xs := range ois {
		if in := e.insts[id]; in != nil {
			for _, x := range xs {
				in.addOI(x)
			}
		}
	}
	for id, in := range e.insts {
		fs, _ := e.st.Funding(id, now-60*24*hourMs, now)
		for _, f := range fs {
			if f.Settled {
				in.fund.addSettled(f)
			}
		}
	}
	log.Printf("restored %d 1m bars from SQLite", n)
}

// fastPool estimates 12h turnover from 144×5m bars (1 request per instrument) so a provisional
// pool is visible within ~30s; instruments whose 1m window is already complete from SQLite skip it.
func (e *Engine) fastPool(ctx context.Context) {
	e.mu.RLock()
	m := time.Now().UnixMilli()/minuteMs*minuteMs - minuteMs
	var todo []string
	for _, id := range e.eligibleIDs() {
		if _, have := e.insts[id].window12h(m); have < 700 {
			todo = append(todo, id)
		}
	}
	e.mu.RUnlock()
	parallel(todo, 8, func(id string) {
		cs, err := e.rest.Candles(ctx, id, "5m", 0, 145)
		if err != nil {
			return
		}
		sum, n := 0.0, 0
		for _, c := range cs {
			if c.Confirmed && n < 144 {
				sum += c.VolCcyQuote
				n++
			}
		}
		e.mu.Lock()
		if in := e.insts[id]; in != nil {
			in.provTurnover = sum
		}
		e.mu.Unlock()
	})
	e.computeUniverse(time.Now().UnixMilli()/minuteMs*minuteMs - minuteMs)
}

// backfill fetches up to 1440 1m bars and 300 1H bars per instrument (priority: provisional
// turnover desc), plus rubik 5m OI and settled funding for likely pool members.
func (e *Engine) backfill(ctx context.Context) {
	e.mu.RLock()
	ids := e.eligibleIDs()
	sort.Slice(ids, func(a, b int) bool {
		return e.insts[ids[a]].provTurnover+e.insts[ids[a]].turnoverHint() > e.insts[ids[b]].provTurnover+e.insts[ids[b]].turnoverHint()
	})
	var likely []string
	for _, id := range ids {
		if e.insts[id].provTurnover+e.insts[id].turnoverHint() > e.conf().Threshold/5 || e.watch[id] {
			likely = append(likely, id)
		}
	}
	e.mu.RUnlock()
	e.bfTotal.Store(int64(len(ids)))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // OI + funding history in parallel with candles (separate rate budgets)
		defer wg.Done()
		parallel(likely, 3, func(id string) {
			e.fetchOI5m(ctx, id)
			e.fetchFundingHistory(ctx, id)
		})
	}()
	parallel(ids, 8, func(id string) {
		e.fillMinutes(ctx, id, 1440)
		e.fillHours(ctx, id)
		e.mu.Lock()
		e.insts[id].backfilled = true
		e.mu.Unlock()
		e.bfDone.Add(1)
	})
	wg.Wait()
}

func (in *Inst) turnoverHint() float64 {
	if in.lastBar == 0 {
		return 0
	}
	s, _ := in.window12h(in.lastBar)
	return s
}

// fillMinutes pages 1m candles backwards (newest first) until the oldest missing minute within
// the last maxMin minutes is covered; with nothing missing it fetches just the latest page.
func (e *Engine) fillMinutes(ctx context.Context, id string, maxMin int) {
	now := time.Now().UnixMilli()
	last := now/minuteMs*minuteMs - minuteMs
	floor := last - int64(maxMin-1)*minuteMs
	e.mu.RLock()
	in := e.insts[id]
	need := last // oldest minute we must reach
	lt := in.ListTime / minuteMs * minuteMs
	for ts := max(floor, lt); ts <= last; ts += minuteMs {
		if !in.bars.has(ts) {
			need = ts
			break
		}
	}
	e.mu.RUnlock()
	after := int64(0)
	for page := 0; page < 6; page++ {
		cs, err := e.rest.Candles(ctx, id, "1m", after, 300)
		if err != nil || len(cs) == 0 {
			return
		}
		var batch []store.InstBar
		e.mu.Lock()
		for _, c := range cs {
			if !c.Confirmed || c.Ts < floor {
				continue
			}
			b := store.Bar{Ts: c.Ts, O: c.O, H: c.H, L: c.L, C: c.C, Vol: c.Vol, VolCcy: c.VolCcy, Quote: c.VolCcyQuote}
			if !in.bars.has(c.Ts) {
				batch = append(batch, store.InstBar{InstID: id, Bar: b})
			}
			in.putBar(b)
		}
		e.mu.Unlock()
		for _, b := range batch {
			e.barCh <- b
		}
		after = cs[len(cs)-1].Ts
		if after <= need || after <= floor {
			return
		}
	}
}

// HourlyDepth is how many 1H bars are kept per instrument (30 days: 14-day replay + 7-day baselines).
const HourlyDepth = 720

func (e *Engine) fillHours(ctx context.Context, id string) {
	e.mu.RLock()
	in := e.insts[id]
	now := time.Now().UnixMilli()
	last := now/hourMs*hourMs - hourMs
	need := int64(0) // oldest missing hour
	for ts := last - (HourlyDepth-1)*hourMs; ts <= last; ts += hourMs {
		if _, ok := in.hourly[ts]; !ok && ts >= in.ListTime/hourMs*hourMs {
			need = ts
			break
		}
	}
	e.mu.RUnlock()
	if need == 0 {
		return
	}
	after := int64(0)
	for page := 0; page < HourlyDepth/300+1; page++ {
		cs, err := e.rest.Candles(ctx, id, "1H", after, 300)
		if err != nil || len(cs) == 0 {
			return
		}
		var batch []store.InstBar
		e.mu.Lock()
		for _, c := range cs {
			if c.Confirmed {
				b := store.Bar{Ts: c.Ts, O: c.O, H: c.H, L: c.L, C: c.C, Vol: c.Vol, VolCcy: c.VolCcy, Quote: c.VolCcyQuote}
				in.hourly[c.Ts] = b
				batch = append(batch, store.InstBar{InstID: id, Bar: b})
			}
		}
		e.mu.Unlock()
		e.st.PutBars("candles_1h", batch)
		after = cs[len(cs)-1].Ts
		if after <= need {
			return
		}
	}
}

func (e *Engine) fetchOI5m(ctx context.Context, id string) {
	xs, err := e.rest.OIHistory(ctx, id, "5m", 0, 100)
	if err != nil {
		return
	}
	var batch []store.OISample
	e.mu.Lock()
	in := e.insts[id]
	for _, x := range xs {
		// rubik snapshot ts marks the start of its 5m period; use it as-is (≤ real sample time).
		s := store.OISample{InstID: id, Ts: x.Ts, OiCcy: x.OiCcy, OiUsd: x.OiUsd}
		in.addOI(s)
		batch = append(batch, s)
	}
	e.mu.Unlock()
	e.st.PutOI(batch)
}

func (e *Engine) fetchFundingHistory(ctx context.Context, id string) {
	xs, err := e.rest.FundingHistory(ctx, id, 0, 100)
	if err != nil {
		return
	}
	var batch []store.FundingSample
	e.mu.Lock()
	in := e.insts[id]
	for _, x := range xs {
		s := store.FundingSample{InstID: id, Ts: x.FundingTime, Rate: x.Rate, FundingTime: x.FundingTime, Settled: true}
		in.fund.addSettled(s)
		batch = append(batch, s)
	}
	e.mu.Unlock()
	e.st.PutFunding(batch)
}

// ---------- WebSocket handlers ----------

func (e *Engine) onBusiness(m okx.Message) {
	if m.Arg.Channel != "candle1m" {
		return
	}
	var raw [][]string
	if json.Unmarshal(m.Data, &raw) != nil {
		return
	}
	e.mu.Lock()
	in := e.insts[m.Arg.InstID]
	if in == nil {
		e.mu.Unlock()
		return
	}
	var batch []store.InstBar
	for _, c := range okx.ParseCandles(raw) {
		b := store.Bar{Ts: c.Ts, O: c.O, H: c.H, L: c.L, C: c.C, Vol: c.Vol, VolCcy: c.VolCcy, Quote: c.VolCcyQuote}
		if c.Confirmed {
			in.putBar(b)
			batch = append(batch, store.InstBar{InstID: in.ID, Bar: b})
		} else if c.Ts >= in.live.Ts {
			in.live, in.liveRecv = b, m.Received.UnixMilli()
		}
	}
	e.mu.Unlock()
	for _, b := range batch {
		select {
		case e.barCh <- b:
		default:
			log.Printf("bar writer queue full; dropping persist of %s %d (memory kept)", b.InstID, b.Ts)
		}
	}
}

func (e *Engine) onPublic(m okx.Message) {
	switch m.Arg.Channel {
	case "trades":
		e.onTrades(m)
	case "tickers":
		var xs []map[string]string
		if json.Unmarshal(m.Data, &xs) != nil {
			return
		}
		e.mu.Lock()
		for _, x := range xs {
			if in := e.insts[x["instId"]]; in != nil {
				ts := atoi(x["ts"])
				if ts >= in.tickTs {
					in.last, in.bid, in.ask, in.tickTs = atof(x["last"]), atof(x["bidPx"]), atof(x["askPx"]), ts
					in.open24h, in.high24h, in.low24h = atof(x["open24h"]), atof(x["high24h"]), atof(x["low24h"])
				}
			}
		}
		e.mu.Unlock()
	case "open-interest":
		var xs []map[string]string
		if json.Unmarshal(m.Data, &xs) != nil {
			return
		}
		e.mu.Lock()
		for _, x := range xs {
			if in := e.insts[x["instId"]]; in != nil {
				ts := atoi(x["ts"])
				if ts >= in.oiNow.Ts {
					in.oiNow = store.OISample{InstID: in.ID, Ts: ts, OiCcy: atof(x["oiCcy"]), OiUsd: atof(x["oiUsd"])}
				}
			}
		}
		e.mu.Unlock()
	case "funding-rate":
		var xs []map[string]string
		if json.Unmarshal(m.Data, &xs) != nil {
			return
		}
		for _, x := range xs {
			e.applyFunding(okx.ParseFunding(x))
		}
	}
}

// applyFunding updates the current period; when the period rolls over, the last rate seen for the
// finished period is recorded as its settled rate (OKX current_period method).
func (e *Engine) applyFunding(f okx.Funding) {
	e.mu.Lock()
	in := e.insts[f.InstID]
	if in == nil || f.Ts < in.fund.Ts {
		e.mu.Unlock()
		return
	}
	var settled *store.FundingSample
	if in.fund.FundingTime != 0 && f.FundingTime > in.fund.FundingTime {
		s := store.FundingSample{InstID: in.ID, Ts: in.fund.FundingTime, Rate: in.fund.Rate, FundingTime: in.fund.FundingTime, Settled: true}
		in.fund.addSettled(s)
		settled = &s
	}
	changed := f.Rate != in.fund.Rate || f.FundingTime != in.fund.FundingTime
	in.fund.Rate, in.fund.FundingTime, in.fund.Next, in.fund.Ts = f.Rate, f.FundingTime, f.NextFundingTime, f.Ts
	if f.NextFundingTime > f.FundingTime {
		in.fund.IntervalH = float64(f.NextFundingTime-f.FundingTime) / float64(hourMs)
	}
	e.mu.Unlock()
	if settled != nil {
		e.fdCh <- *settled
	}
	if changed {
		select {
		case e.fdCh <- store.FundingSample{InstID: f.InstID, Ts: f.Ts, Rate: f.Rate, FundingTime: f.FundingTime, NextFunding: f.NextFundingTime}:
		default:
		}
	}
}

// onReconnect gap-fills 1m bars for every eligible instrument after a business WS drop.
func (e *Engine) onReconnect(downSince time.Time) {
	if downSince.IsZero() || e.Phase() != "live" {
		return
	}
	mins := int(time.Since(downSince).Minutes()) + 3
	log.Printf("business WS reconnected after %s; gap-filling %d minutes", time.Since(downSince).Round(time.Second), mins)
	e.mu.RLock()
	ids := e.eligibleIDs()
	e.mu.RUnlock()
	parallel(ids, 6, func(id string) { e.fillMinutes(context.Background(), id, min(mins, 1440)) })
}

func (e *Engine) updateSubscriptions() {
	e.mu.RLock()
	ids := e.eligibleIDs()
	now := time.Now().UnixMilli()
	for id, in := range e.insts { // adhoc: watched or viewed recently
		if !in.Eligible && e.recording(in, now) {
			ids = append(ids, id)
		}
	}
	snap := e.snap.Load()
	tick := map[string]bool{}
	for id := range e.watch {
		tick[id] = true
	}
	for id, t := range e.views {
		if now-t < RecordViewed.Milliseconds() {
			tick[id] = true
		}
	}
	for _, r := range snap.Rows {
		if r.Active {
			tick[r.InstID] = true
		}
	}
	flow := e.flowIDs(now)
	e.mu.RUnlock()
	e.syncFlowStates(flow)
	var bizArgs, pubArgs []okx.Arg
	for id := range flow {
		pubArgs = append(pubArgs, okx.Arg{Channel: "trades", InstID: id})
	}
	for _, id := range ids {
		bizArgs = append(bizArgs, okx.Arg{Channel: "candle1m", InstID: id})
		pubArgs = append(pubArgs, okx.Arg{Channel: "open-interest", InstID: id}, okx.Arg{Channel: "funding-rate", InstID: id})
		if tick[id] {
			pubArgs = append(pubArgs, okx.Arg{Channel: "tickers", InstID: id})
		}
	}
	e.biz.SetSubscriptions(bizArgs)
	e.pub.SetSubscriptions(pubArgs)
}

// ---------- periodic REST ----------

func (e *Engine) periodic(ctx context.Context) {
	spotT := time.NewTicker(15 * time.Second)
	swapT := time.NewTicker(30 * time.Second)
	instT := time.NewTicker(5 * time.Minute)
	subT := time.NewTicker(5 * time.Minute) // drop adhoc streams whose view expired
	priceT := time.NewTicker(2 * time.Second)
	defer spotT.Stop()
	defer swapT.Stop()
	defer instT.Stop()
	defer subT.Stop()
	defer priceT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-spotT.C:
			e.refreshSpot(ctx)
		case <-swapT.C:
			e.refreshTickers(ctx)
		case <-instT.C:
			if err := e.refreshInstruments(ctx); err == nil {
				e.updateSubscriptions()
				e.backfillNew(ctx)
			}
		case <-subT.C:
			e.updateSubscriptions()
			sweepCaches()
		case <-priceT.C:
			e.broadcastPrices()
		}
	}
}

func (e *Engine) backfillNew(ctx context.Context) {
	e.mu.RLock()
	var todo []string
	for _, id := range e.eligibleIDs() {
		if !e.insts[id].backfilled {
			todo = append(todo, id)
		}
	}
	e.mu.RUnlock()
	if e.Phase() != "live" {
		return
	}
	for _, id := range todo {
		e.fillMinutes(ctx, id, 1440)
		e.fillHours(ctx, id)
		e.mu.Lock()
		e.insts[id].backfilled = true
		e.mu.Unlock()
	}
}

func (e *Engine) refreshTickers(ctx context.Context) {
	ts, err := e.rest.Tickers(ctx, "SWAP")
	if err != nil {
		return
	}
	e.mu.Lock()
	for _, t := range ts {
		e.tickers[t.InstID] = t
		if in := e.insts[t.InstID]; in != nil && t.Ts > in.tickTs {
			in.last, in.bid, in.ask, in.tickTs = t.Last, t.Bid, t.Ask, t.Ts
		}
	}
	e.mu.Unlock()
	e.refreshSpot(ctx)
}

func (e *Engine) refreshSpot(ctx context.Context) {
	ts, err := e.rest.Tickers(ctx, "SPOT")
	if err != nil {
		return
	}
	px := map[string]okx.Ticker{}
	for _, t := range ts {
		px[t.InstID] = t
	}
	e.mu.Lock()
	for _, in := range e.insts {
		if t, ok := px[in.SpotID]; ok && in.SpotID != "" {
			in.spotLast, in.spotTs = t.Last, t.Ts
		}
	}
	e.mu.Unlock()
}

func (e *Engine) refreshOIFunding(ctx context.Context) {
	if ois, err := e.rest.OpenInterestAll(ctx); err == nil {
		e.mu.Lock()
		for _, x := range ois {
			if in := e.insts[x.InstID]; in != nil {
				in.oiNow = store.OISample{InstID: x.InstID, Ts: x.Ts, OiCcy: x.OiCcy, OiUsd: x.OiUsd}
			}
		}
		e.mu.Unlock()
	}
	if fs, err := e.rest.FundingAll(ctx); err == nil {
		for _, f := range fs {
			e.applyFunding(f)
		}
	}
}

// ---------- persistence ----------

func (e *Engine) writer(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	var bars []store.InstBar
	var ois []store.OISample
	var fds []store.FundingSample
	var flows []store.FlowRow
	flush := func() {
		if err := e.st.PutFlow(flows); err != nil {
			log.Printf("persist order flow: %v", err)
		}
		flows = flows[:0]
		if err := e.st.PutBars("candles_1m", bars); err != nil {
			log.Printf("persist bars: %v", err)
		}
		if err := e.st.PutOI(ois); err != nil {
			log.Printf("persist oi: %v", err)
		}
		if err := e.st.PutFunding(fds); err != nil {
			log.Printf("persist funding: %v", err)
		}
		bars, ois, fds = bars[:0], ois[:0], fds[:0]
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case b := <-e.barCh:
			bars = append(bars, b)
			if len(bars) >= 5000 {
				flush()
			}
		case o := <-e.oiCh:
			ois = append(ois, o)
		case f := <-e.fdCh:
			fds = append(fds, f)
		case r := <-e.flowCh:
			flows = append(flows, r)
		case <-t.C:
			flush()
		}
	}
}

// ---------- helpers ----------

func parallel(ids []string, n int, f func(string)) {
	ch := make(chan string)
	var wg sync.WaitGroup
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ch {
				f(id)
			}
		}()
	}
	for _, id := range ids {
		ch <- id
	}
	close(ch)
	wg.Wait()
}

func atof(s string) float64 {
	var v float64
	json.Unmarshal([]byte(s), &v)
	return v
}

func atoi(s string) int64 {
	var v int64
	json.Unmarshal([]byte(s), &v)
	return v
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

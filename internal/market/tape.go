package market

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

// ---------- tape: raw public trades, spot and swap, for microstructure analysis ----------
//
// The order-flow recorder (orderflow.go) keeps only per-minute totals. The tape keeps every trade
// (time, price, size, taker side) of a smaller "deep" set — the top of the active pool, the
// watchlist, held / viewed instruments and anything an agent asked about in the last hours — for
// both the perpetual and its USDT spot pair, so size distributions, repeated clip sizes, sweeps,
// spot-vs-swap lead and wash patterns can be computed over any window of the last two days.
//
// One row per instrument-minute (encoded below). A minute is stored only when every trade in it
// was seen (live: subscribed for the whole minute; backfill: covered by a daily file or by
// history-trades pages); a stored empty minute means "no trades", a missing one means unknown.

const (
	TapeTopN      = 30                 // pool members (by rank) whose trades are taped
	BookTopN      = 15                 // pool members whose order books are tracked (walls.go)
	tapeReqFor    = 6 * time.Hour      // an instrument an agent asked about stays taped this long
	TapeKeep      = 48 * time.Hour     // trades kept (capacity pressure shortens it, never below tapeFloor)
	tapeFloor     = 24 * time.Hour     //
	tapeAuxKeep   = 7 * 24 * time.Hour // wall events and book minutes
	TapeDefaultMB = 1024
	tapeFilePages = 400 // history-trades pages per backfill (≈40k trades, ~70 s at the rate limit)
	tapeFileMaxMB = 40  // daily trade files above this are skipped (BTC-sized days; memory)
	tapeWait      = 25 * time.Second
)

// TapeTrade is one public trade. Sz is in contracts for a swap, in coins for spot.
type TapeTrade struct {
	Ts  int64
	Px  float64
	Sz  float64
	Buy bool
}

// ---------- minute codec ----------
//
// [version][pxDec][szDec] then per trade: uvarint Δts (first from the minute open), zigzag varint
// Δ(px × 10^pxDec), uvarint (sz × 10^szDec)<<1 | buy; the whole thing deflated. Prices and sizes
// are decimal strings on the wire, so a per-minute decimal scale stores them exactly.

func decimals(x float64) int {
	s := strconv.FormatFloat(x, 'f', -1, 64)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return len(s) - i - 1
	}
	return 0
}

func scaleFor(xs []TapeTrade, get func(TapeTrade) float64) int {
	d, hi := 0, 0.0
	for _, t := range xs {
		d = max(d, decimals(get(t)))
		hi = max(hi, get(t))
	}
	for d > 0 && hi*math.Pow10(d) > 1e15 { // keep exact in float64 / int64
		d--
	}
	return min(d, 15)
}

func encodeMinute(minute int64, xs []TapeTrade) []byte {
	if len(xs) == 0 {
		return nil
	}
	pd, sd := scaleFor(xs, func(t TapeTrade) float64 { return t.Px }), scaleFor(xs, func(t TapeTrade) float64 { return t.Sz })
	pm, sm := math.Pow10(pd), math.Pow10(sd)
	raw := make([]byte, 0, len(xs)*8+3)
	raw = append(raw, 1, byte(pd), byte(sd))
	prevT, prevP := minute, int64(0)
	for _, t := range xs {
		p := int64(math.Round(t.Px * pm))
		s := uint64(math.Round(t.Sz * sm))
		raw = binary.AppendUvarint(raw, uint64(max(t.Ts-prevT, 0)))
		raw = binary.AppendVarint(raw, p-prevP)
		b := uint64(0)
		if t.Buy {
			b = 1
		}
		raw = binary.AppendUvarint(raw, s<<1|b)
		prevT, prevP = max(t.Ts, prevT), p
	}
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestSpeed)
	w.Write(raw)
	w.Close()
	return buf.Bytes()
}

func decodeMinute(minute int64, data []byte, out []TapeTrade) ([]TapeTrade, error) {
	if len(data) == 0 {
		return out, nil
	}
	raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(data)))
	if err != nil {
		return out, err
	}
	if len(raw) < 3 || raw[0] != 1 {
		return out, errors.New("tape: bad minute header")
	}
	pm, sm := math.Pow10(int(raw[1])), math.Pow10(int(raw[2]))
	r := raw[3:]
	t, p := minute, int64(0)
	for len(r) > 0 {
		dt, n1 := binary.Uvarint(r)
		if n1 <= 0 {
			return out, errors.New("tape: truncated")
		}
		r = r[n1:]
		dp, n2 := binary.Varint(r)
		if n2 <= 0 {
			return out, errors.New("tape: truncated")
		}
		r = r[n2:]
		sb, n3 := binary.Uvarint(r)
		if n3 <= 0 {
			return out, errors.New("tape: truncated")
		}
		r = r[n3:]
		t += int64(dt)
		p += dp
		out = append(out, TapeTrade{Ts: t, Px: float64(p) / pm, Sz: float64(sb>>1) / sm, Buy: sb&1 == 1})
	}
	return out, nil
}

// ---------- live recording ----------

type tapeBuf struct {
	minute  int64 // open of the oldest minute not yet written
	since   int64 // minutes opening at/after this were fully subscribed
	pending []TapeTrade
}

type tapeState struct {
	st     *store.Tape
	capMB  int
	mu     sync.Mutex
	bufs   map[string]*tapeBuf // instId (swap or spot) → accumulating trades
	req    map[string]int64    // swap id → last agent request (ms)
	ch     chan store.TapeRow
	keep   time.Duration // trade age in force (≤ TapeKeep, shortened under capacity pressure)
	jobsMu sync.Mutex
	jobs   map[string]*tapeJob
}

type tapeJob struct {
	done chan struct{}
	msg  string
	err  error
}

// SetTape enables the tape (call before Start). capMB bounds the tape file.
func (e *Engine) SetTape(t *store.Tape, capMB int) {
	if capMB <= 0 {
		capMB = TapeDefaultMB
	}
	e.tape = &tapeState{st: t, capMB: capMB, bufs: map[string]*tapeBuf{}, req: map[string]int64{},
		ch: make(chan store.TapeRow, 20000), keep: TapeKeep, jobs: map[string]*tapeJob{}}
	e.walls = newWallTracker(e)
}

func (e *Engine) startTape(ctx context.Context) {
	if e.tape == nil {
		return
	}
	go e.tapeLoop(ctx)
	go e.tapeWriter(ctx)
	go e.tapeRetention(ctx)
	go e.walls.run(ctx)
}

// tapeTargets returns the instruments whose trades (swap + spot ids) and books are recorded.
// Caller holds e.mu (read).
func (e *Engine) tapeTargets(now int64) (trades, books map[string]bool) {
	trades, books = map[string]bool{}, map[string]bool{}
	if e.tape == nil {
		return
	}
	base := map[string]bool{}
	for id := range e.watch {
		base[id] = true
	}
	for id := range e.keep {
		base[id] = true
	}
	for _, ids := range e.keepBy {
		for id := range ids {
			base[id] = true
		}
	}
	for id, t := range e.views {
		if now-t < RecordViewed.Milliseconds() {
			base[id] = true
		}
	}
	e.tape.mu.Lock()
	for id, t := range e.tape.req {
		if now-t < tapeReqFor.Milliseconds() {
			base[id] = true
		} else {
			delete(e.tape.req, id)
		}
	}
	e.tape.mu.Unlock()
	add := func(set map[string]bool, id string) {
		in := e.insts[id]
		if in == nil {
			if e.spotIDs[id] { // spot-only request
				set[id] = true
			}
			return
		}
		set[id] = true
		if in.SpotID != "" {
			set[in.SpotID] = true
		}
	}
	for id := range base {
		add(trades, id)
		add(books, id)
	}
	for _, r := range e.snap.Load().Rows {
		if r.Active && r.Rank <= TapeTopN {
			add(trades, r.InstID)
		}
		if r.Active && r.Rank <= BookTopN {
			add(books, r.InstID)
		}
	}
	return
}

// syncTape starts/stops per-instrument tape buffers to match the subscribed set.
func (e *Engine) syncTape(ids map[string]bool) {
	if e.tape == nil {
		return
	}
	now := time.Now().UnixMilli()
	e.tape.mu.Lock()
	defer e.tape.mu.Unlock()
	for id := range ids {
		if e.tape.bufs[id] == nil {
			e.tape.bufs[id] = &tapeBuf{minute: now / minuteMs * minuteMs, since: now + 2000}
		}
	}
	for id := range e.tape.bufs {
		if !ids[id] {
			delete(e.tape.bufs, id)
		}
	}
}

// RequestTape marks an instrument as asked about (taped and book-tracked for tapeReqFor).
func (e *Engine) RequestTape(swapID string) {
	if e.tape == nil {
		return
	}
	e.tape.mu.Lock()
	_, had := e.tape.req[swapID]
	e.tape.req[swapID] = time.Now().UnixMilli()
	e.tape.mu.Unlock()
	if !had {
		go e.updateSubscriptions()
	}
}

func (e *Engine) onTapeTrades(m okx.Message) {
	if e.tape == nil {
		return
	}
	var xs []wsTrade
	if json.Unmarshal(m.Data, &xs) != nil {
		return
	}
	id := m.Arg.InstID
	e.tape.mu.Lock()
	b := e.tape.bufs[id]
	if b == nil {
		e.tape.mu.Unlock()
		return
	}
	got := make([]TapeTrade, 0, len(xs))
	for _, x := range xs {
		t := TapeTrade{Ts: atoi(x.Ts), Px: atof(x.Px), Sz: atof(x.Sz), Buy: x.Side == "buy"}
		if t.Px > 0 && t.Sz > 0 && t.Ts >= b.minute {
			b.pending = append(b.pending, t)
			got = append(got, t)
		}
	}
	e.tape.mu.Unlock()
	if e.walls != nil {
		e.walls.onTrades(id, got)
	}
}

func (e *Engine) tapeReconnect() {
	if e.tape == nil {
		return
	}
	now := time.Now().UnixMilli()
	e.tape.mu.Lock()
	for _, b := range e.tape.bufs {
		b.since = now + 2000
	}
	e.tape.mu.Unlock()
}

// tapeLoop closes minutes (grace for late messages) and hands them to the writer.
func (e *Engine) tapeLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			to := now.Add(-flowGrace).UnixMilli() / minuteMs * minuteMs
			down := !e.pub.Connected.Load()
			var rows []store.TapeRow
			e.tape.mu.Lock()
			for id, b := range e.tape.bufs {
				if down {
					b.since = max(b.since, now.UnixMilli()+2000)
				}
				rows = append(rows, b.close(id, to)...)
			}
			e.tape.mu.Unlock()
			for _, r := range rows {
				select {
				case e.tape.ch <- r:
				default:
				}
			}
		}
	}
}

// close emits every minute before `to` (complete ones as rows, incomplete ones dropped).
func (b *tapeBuf) close(id string, to int64) []store.TapeRow {
	var out []store.TapeRow
	for ; b.minute < to; b.minute += minuteMs {
		end := b.minute + minuteMs
		var cur []TapeTrade
		rest := b.pending[:0]
		for _, t := range b.pending {
			switch {
			case t.Ts < b.minute:
			case t.Ts < end:
				cur = append(cur, t)
			default:
				rest = append(rest, t)
			}
		}
		b.pending = rest
		if b.minute < b.since {
			continue
		}
		sort.SliceStable(cur, func(i, j int) bool { return cur[i].Ts < cur[j].Ts })
		out = append(out, store.TapeRow{InstID: id, Ts: b.minute, N: len(cur), Data: encodeMinute(b.minute, cur)})
	}
	return out
}

func (e *Engine) tapeWriter(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	var rows []store.TapeRow
	flush := func() {
		if len(rows) == 0 {
			return
		}
		if err := e.tape.st.PutTape(rows); err != nil {
			log.Printf("tape: write %d minutes: %v", len(rows), err)
		}
		rows = rows[:0]
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case r := <-e.tape.ch:
			rows = append(rows, r)
			if len(rows) >= 2000 {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}

// tapeRetention ages out trades (48h, shortened while the file is over its cap) and wall/book data (7d).
func (e *Engine) tapeRetention(ctx context.Context) {
	t := time.NewTicker(20 * time.Minute)
	defer t.Stop()
	for {
		now := time.Now()
		keep := e.tapeKeep()
		used, _ := e.tape.st.UsedBytes()
		capB := int64(e.tape.capMB) << 20
		for used > capB && keep > tapeFloor {
			keep -= 6 * time.Hour
			e.tape.st.Prune(now.Add(-keep).UnixMilli(), now.Add(-tapeAuxKeep).UnixMilli())
			used, _ = e.tape.st.UsedBytes()
		}
		if used < capB*7/10 && keep < TapeKeep {
			keep += 6 * time.Hour
		}
		e.tape.mu.Lock()
		e.tape.keep = keep
		e.tape.mu.Unlock()
		if n, err := e.tape.st.Prune(now.Add(-keep).UnixMilli(), now.Add(-tapeAuxKeep).UnixMilli()); err != nil {
			log.Printf("tape: prune: %v", err)
		} else if n > 0 {
			log.Printf("tape: pruned %d rows (trades kept %s, %d MB used)", n, keep, used>>20)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) tapeKeep() time.Duration {
	e.tape.mu.Lock()
	defer e.tape.mu.Unlock()
	return e.tape.keep
}

// ---------- reading, with backfill ----------

// TapeCoverage says which minutes of a window the trades cover.
type TapeCoverage struct {
	From      int64      `json:"from"`
	To        int64      `json:"to"`
	Minutes   int        `json:"minutes"`
	Covered   int        `json:"covered"`
	Pct       float64    `json:"pct"`
	Gaps      [][2]int64 `json:"gaps,omitempty"` // largest missing ranges [from, to) ms
	Recording bool       `json:"recording"`      // taped live now
	Backfill  string     `json:"backfill,omitempty"`
}

// TapeData is one instrument's trades over a window.
type TapeData struct {
	InstID   string       `json:"instId"`
	Market   string       `json:"market"` // swap | spot
	CtVal    float64      `json:"ctVal"`  // USDT = px × sz × ctVal (1 for spot)
	Trades   []TapeTrade  `json:"-"`
	Coverage TapeCoverage `json:"coverage"`
}

var errNoTape = errors.New("trade tape is not enabled on this server")

// ResolvePair maps "ENA", "ENA-USDT", "ENA-USDT-SWAP" to the swap id (may be "" for a spot-only
// coin) and the USDT spot id (may be "").
func (e *Engine) ResolvePair(s string) (swap, spot string, err error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	coin := strings.TrimSuffix(strings.TrimSuffix(s, "-SWAP"), "-USDT")
	if coin == "" || strings.Contains(coin, "-") {
		return "", "", fmt.Errorf("unknown instrument %q: use a coin (ENA) or an OKX id (ENA-USDT-SWAP)", s)
	}
	e.mu.RLock()
	_, inCat := e.catalog[coin+"-USDT-SWAP"]
	_, inInst := e.insts[coin+"-USDT-SWAP"]
	hasSpot := e.spotIDs[coin+"-USDT"]
	e.mu.RUnlock()
	if inCat || inInst {
		swap = coin + "-USDT-SWAP"
	}
	if hasSpot {
		spot = coin + "-USDT"
	}
	if swap == "" && spot == "" {
		return "", "", fmt.Errorf("no live OKX USDT perpetual or spot pair for %s", coin)
	}
	return swap, spot, nil
}

// LoadTape returns the trades of one instrument (swap or spot id) in [from, to), backfilling
// missing minutes from OKX when fetch is set (waits up to tapeWait, then returns what is there).
func (e *Engine) LoadTape(ctx context.Context, id string, from, to int64, fetch bool) (TapeData, error) {
	if e.tape == nil {
		return TapeData{}, errNoTape
	}
	d := TapeData{InstID: id, Market: "spot", CtVal: 1}
	if strings.HasSuffix(id, "-SWAP") {
		in := e.instFor(id)
		if in == nil {
			return d, fmt.Errorf("unknown instrument %s", id)
		}
		e.mu.RLock()
		d.Market, d.CtVal = "swap", in.CtVal
		e.mu.RUnlock()
		if d.CtVal <= 0 {
			return d, fmt.Errorf("no contract value for %s", id)
		}
	}
	from = from / minuteMs * minuteMs
	if fetch {
		job := e.tapeBackfill(id, from, to)
		if job != nil {
			wait := time.NewTimer(tapeWait)
			select {
			case <-job.done:
				d.Coverage.Backfill = job.msg
				if job.err != nil {
					d.Coverage.Backfill = "error: " + job.err.Error()
				}
			case <-wait.C:
				d.Coverage.Backfill = "running: older trades are still being downloaded; ask again in a minute for fuller coverage"
			case <-ctx.Done():
			}
			wait.Stop()
		}
	}
	rows, err := e.tape.st.TapeRows(id, from, to)
	if err != nil {
		return d, err
	}
	have := map[int64]bool{}
	for _, r := range rows {
		have[r.Ts] = true
		if d.Trades, err = decodeMinute(r.Ts, r.Data, d.Trades); err != nil {
			return d, fmt.Errorf("tape %s %d: %w", id, r.Ts, err)
		}
	}
	// the open minute(s) still in memory
	e.tape.mu.Lock()
	if b := e.tape.bufs[id]; b != nil {
		d.Coverage.Recording = true
		for _, t := range b.pending {
			if t.Ts >= from && t.Ts < to && !have[t.Ts/minuteMs*minuteMs] {
				d.Trades = append(d.Trades, t)
			}
		}
	}
	e.tape.mu.Unlock()
	sort.SliceStable(d.Trades, func(i, j int) bool { return d.Trades[i].Ts < d.Trades[j].Ts })
	last := min(to, time.Now().UnixMilli()/minuteMs*minuteMs)
	d.Coverage.From, d.Coverage.To = from, to
	gaps := missingRanges(from, last, have)
	for m := from; m < last; m += minuteMs {
		d.Coverage.Minutes++
		if have[m] {
			d.Coverage.Covered++
		}
	}
	if d.Coverage.Minutes > 0 {
		d.Coverage.Pct = math.Round(float64(d.Coverage.Covered)/float64(d.Coverage.Minutes)*1000) / 10
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i][1]-gaps[i][0] > gaps[j][1]-gaps[j][0] })
	if len(gaps) > 5 {
		gaps = gaps[:5]
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i][0] < gaps[j][0] })
	d.Coverage.Gaps = gaps
	return d, nil
}

// tapeBackfill starts (or joins) a backfill of the missing closed minutes in [from, to); nil when
// nothing is missing.
func (e *Engine) tapeBackfill(id string, from, to int64) *tapeJob {
	now := time.Now().UnixMilli()
	hi := min(to, (now-flowGrace.Milliseconds()-minuteMs)/minuteMs*minuteMs)
	e.tape.mu.Lock()
	if b := e.tape.bufs[id]; b != nil {
		hi = min(hi, (b.since+minuteMs-1)/minuteMs*minuteMs) // live recording owns the rest
	}
	keep := e.tape.keep
	e.tape.mu.Unlock()
	from = max(from, (now-keep.Milliseconds())/minuteMs*minuteMs)
	if hi <= from {
		return nil
	}
	have, err := e.tape.st.TapeMinutes(id, from, hi)
	if err != nil || len(missingRanges(from, hi, have)) == 0 {
		return nil
	}
	e.tape.jobsMu.Lock()
	defer e.tape.jobsMu.Unlock()
	if j := e.tape.jobs[id]; j != nil {
		select {
		case <-j.done:
		default:
			return j // running: join it
		}
	}
	j := &tapeJob{done: make(chan struct{})}
	e.tape.jobs[id] = j
	go func() {
		defer close(j.done)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		j.msg, j.err = e.runTapeBackfill(ctx, id, from, hi)
		if j.err != nil {
			log.Printf("tape backfill %s: %v", id, j.err)
		}
	}()
	return j
}

var tapeBackfillSem = make(chan struct{}, 1) // trade files are large: one download at a time

func (e *Engine) runTapeBackfill(ctx context.Context, id string, from, hi int64) (string, error) {
	select {
	case tapeBackfillSem <- struct{}{}:
		defer func() { <-tapeBackfillSem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	have, err := e.tape.st.TapeMinutes(id, from, hi)
	if err != nil {
		return "", err
	}
	var msgs []string
	put := func(byMin map[int64][]TapeTrade, lo, hi int64) int {
		var rows []store.TapeRow
		for m := lo; m < hi; m += minuteMs {
			if have[m] {
				continue
			}
			xs := byMin[m]
			sort.SliceStable(xs, func(i, j int) bool { return xs[i].Ts < xs[j].Ts })
			rows = append(rows, store.TapeRow{InstID: id, Ts: m, N: len(xs), Data: encodeMinute(m, xs)})
			have[m] = true
		}
		if err := e.tape.st.PutTape(rows); err != nil {
			log.Printf("tape backfill %s: write: %v", id, err)
			return 0
		}
		return len(rows)
	}
	// 1) complete UTC+8 days from the daily trade files
	if files, err := e.rest.TradeFiles(ctx, id, from-utc8DayMs, hi); err == nil {
		n, skipped := 0, 0
		for _, f := range files {
			lo, end := max(f.Start, from), min(f.Start+utc8DayMs, hi)
			if end <= lo || len(missingRanges(lo, end, have)) == 0 {
				continue
			}
			if f.SizeMB > tapeFileMaxMB {
				skipped++
				continue
			}
			byMin := map[int64][]TapeTrade{}
			err := e.rest.ReadTradeFile(ctx, f.URL, func(t okx.Trade) {
				if m := t.Ts / minuteMs * minuteMs; m >= lo && m < end && !have[m] {
					byMin[m] = append(byMin[m], TapeTrade{Ts: t.Ts, Px: t.Px, Sz: t.Sz, Buy: t.Buy})
				}
			})
			if err != nil {
				return "", fmt.Errorf("trade file: %w", err)
			}
			n += put(byMin, lo, end)
		}
		if n > 0 {
			msgs = append(msgs, fmt.Sprintf("%d min from daily files", n))
		}
		if skipped > 0 {
			msgs = append(msgs, fmt.Sprintf("%d daily file(s) over %d MB skipped", skipped, tapeFileMaxMB))
		}
	}
	// 2) the rest, newest gap first, from history-trades (page-capped)
	pages := 0
	gaps := missingRanges(from, hi, have)
	for i := len(gaps) - 1; i >= 0 && pages < tapeFilePages; i-- {
		lo, end := gaps[i][0], gaps[i][1]
		byMin := map[int64][]TapeTrade{}
		oldest, beforeID, beforeTs := end, int64(0), end
		reached := false
		for pages < tapeFilePages {
			xs, err := e.rest.HistoryTrades(ctx, id, beforeID, beforeTs, 100)
			pages++
			if err != nil {
				return strings.Join(msgs, "; "), err
			}
			for _, t := range xs {
				if m := t.Ts / minuteMs * minuteMs; m >= lo && m < end {
					byMin[m] = append(byMin[m], TapeTrade{Ts: t.Ts, Px: t.Px, Sz: t.Sz, Buy: t.Buy})
				}
				oldest = min(oldest, t.Ts)
				beforeID = t.ID
			}
			if len(xs) < 100 || oldest < lo {
				reached = true
				break
			}
		}
		start := lo
		if !reached { // the minute holding the oldest trade may be partial
			start = max(lo, oldest/minuteMs*minuteMs+minuteMs)
		}
		if n := put(byMin, start, end); n > 0 {
			msgs = append(msgs, fmt.Sprintf("%d min from history-trades", n))
		}
	}
	if left := missingRanges(from, hi, have); len(left) > 0 {
		var n int64
		for _, g := range left {
			n += (g[1] - g[0]) / minuteMs
		}
		msgs = append(msgs, fmt.Sprintf("%d min still missing (page cap; very active market — live recording fills it from now on)", n))
	}
	return strings.Join(msgs, "; "), nil
}

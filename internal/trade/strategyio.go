package trade

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// What strategies can do besides signals: read data series, send messages, write log lines.

// ---------- series ----------

// SeriesFields are the fields ctx.series can read.
var SeriesFields = []string{"oi", "funding", "buy", "sell", "delta"}

const maxSeries = 1000

// Series returns up to n complete buckets of field for instID on tf, as of the instance's clock.
//   - oi: open interest in USDT at the bucket close (last sample inside the bucket)
//   - funding: settled funding rates (one point per settlement, tf ignored)
//   - buy / sell / delta: taker buy, sell and buy−sell notional (USDT) from recorded trades,
//     completed with OKX taker-volume history where trades were not recorded
func (s *Service) Series(instanceID int64, instID, field, tf string, n int) ([]SeriesPoint, error) {
	step, ok := TFMillis(tf)
	if !ok {
		return nil, fmt.Errorf("unknown timeframe %q", tf)
	}
	n = max(1, min(n, maxSeries))
	now := time.Now().UnixMilli()
	if ri := s.instRT(instanceID); ri != nil {
		now = ri.b.market().Now()
	}
	end := now / step * step // buckets opening before end are complete
	from := end - int64(n)*step
	out := []SeriesPoint{}
	switch field {
	case "oi":
		xs, err := s.st.OI(instID, from, end-1)
		if err != nil {
			return nil, err
		}
		for _, x := range xs {
			b := x.Ts / step * step
			if len(out) > 0 && out[len(out)-1].Ts == b {
				out[len(out)-1].V = x.OiUsd
			} else {
				out = append(out, SeriesPoint{Ts: b, V: x.OiUsd})
			}
		}
	case "funding":
		xs, err := s.st.Funding(instID, now-int64(n+1)*8*hourMs, now)
		if err != nil {
			return nil, err
		}
		for _, x := range xs {
			if x.Settled && x.FundingTime <= now && (len(out) == 0 || out[len(out)-1].Ts != x.FundingTime) {
				out = append(out, SeriesPoint{Ts: x.FundingTime, V: x.Rate})
			}
		}
		if len(out) > n {
			out = out[len(out)-n:]
		}
	case "buy", "sell", "delta":
		buy, sell, err := s.flow(instID, step, from, end)
		if err != nil {
			return nil, err
		}
		for _, b := range sortedInt64Keys(buy) {
			v := buy[b]
			switch field {
			case "sell":
				v = sell[b]
			case "delta":
				v = buy[b] - sell[b]
			}
			out = append(out, SeriesPoint{Ts: b, V: v})
		}
	default:
		return nil, fmt.Errorf("unknown field %q (have %s)", field, strings.Join(SeriesFields, ", "))
	}
	return out, nil
}

// flow sums taker buy/sell per complete bucket of [from, end): recorded minutes when they cover
// the whole bucket, otherwise OKX taker-volume history (5m, or 1H for buckets of an hour or more).
func (s *Service) flow(instID string, step, from, end int64) (buy, sell map[int64]float64, err error) {
	buy, sell = map[int64]float64{}, map[int64]float64{}
	rows, err := s.st.Flow(instID, from, end-1, false)
	if err != nil {
		return nil, nil, err
	}
	mins := map[int64]int64{}
	for _, r := range rows {
		b := r.Ts / step * step
		buy[b] += r.Buy
		sell[b] += r.Sell
		mins[b]++
	}
	for b, m := range mins {
		if m < step/minuteMs {
			delete(buy, b)
			delete(sell, b)
		}
	}
	if step < 5*minuteMs {
		return buy, sell, nil
	}
	period, pstep := "5m", 5*minuteMs
	if step >= hourMs {
		period, pstep = "1H", hourMs
	}
	tk, err := s.st.Taker(instID, period, from, end-1)
	if err != nil {
		return buy, sell, nil
	}
	tb, ts, cnt := map[int64]float64{}, map[int64]float64{}, map[int64]int64{}
	for _, x := range tk {
		b := x.Ts / step * step
		tb[b] += x.Buy
		ts[b] += x.Sell
		cnt[b]++
	}
	for b, c := range cnt {
		if _, have := buy[b]; !have && c >= step/pstep {
			buy[b], sell[b] = tb[b], ts[b]
		}
	}
	return buy, sell, nil
}

func sortedInt64Keys(m map[int64]float64) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ---------- log and messages ----------

// LogLine is one entry of an instance's run log (ctx.log, ctx.notify, handler errors).
type LogLine struct {
	Ts    int64  `json:"ts"`    // wall clock, ms
	Clock int64  `json:"clock"` // the instance's clock (replay time in backtests), ms
	Level string `json:"level"` // info | warn | error
	Kind  string `json:"kind"`  // log | notify
	Msg   string `json:"msg"`
	Sent  bool   `json:"sent,omitempty"` // notify: delivered to the channels (live only)
}

const (
	logKeep        = 300
	notifyPerHour  = 30
	notifyDupGapMs = 10 * minuteMs
)

// runLog keeps the latest lines of every instance in memory, and the notify rate state.
type runLog struct {
	mu    sync.Mutex
	lines map[int64][]LogLine
	sent  map[int64][]int64          // instance → send times within the last hour
	last  map[int64]map[string]int64 // instance → text → last send time
}

func newRunLog() *runLog {
	return &runLog{lines: map[int64][]LogLine{}, sent: map[int64][]int64{}, last: map[int64]map[string]int64{}}
}

func (l *runLog) add(id int64, x LogLine) {
	l.mu.Lock()
	defer l.mu.Unlock()
	xs := append(l.lines[id], x)
	if len(xs) > logKeep {
		xs = append([]LogLine(nil), xs[len(xs)-logKeep:]...)
	}
	l.lines[id] = xs
}

// allow applies the notify limits: notifyPerHour per instance, identical text once per 10 minutes.
func (l *runLog) allow(id int64, text string, now int64) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.last[id][text]; ok && now-t < notifyDupGapMs {
		return false, "10 分钟内重复的消息"
	}
	recent := l.sent[id][:0]
	for _, t := range l.sent[id] {
		if now-t < hourMs {
			recent = append(recent, t)
		}
	}
	l.sent[id] = recent
	if len(recent) >= notifyPerHour {
		return false, fmt.Sprintf("每小时最多 %d 条", notifyPerHour)
	}
	l.sent[id] = append(recent, now)
	if l.last[id] == nil {
		l.last[id] = map[string]int64{}
	}
	for k, t := range l.last[id] {
		if now-t >= notifyDupGapMs {
			delete(l.last[id], k)
		}
	}
	l.last[id][text] = now
	return true, ""
}

// RunLog returns an instance's recent log lines, oldest first.
func (s *Service) RunLog(instanceID int64) []LogLine {
	s.logs.mu.Lock()
	defer s.logs.mu.Unlock()
	return append([]LogLine{}, s.logs.lines[instanceID]...)
}

func (s *Service) instClock(instanceID int64) (int64, *instRT) {
	ri := s.instRT(instanceID)
	if ri == nil {
		return time.Now().UnixMilli(), nil
	}
	return ri.b.market().Now(), ri
}

// Log records a strategy log line (also written to the server log).
func (s *Service) Log(instanceID int64, level, msg string) {
	clock, ri := s.instClock(instanceID)
	s.logs.add(instanceID, LogLine{Ts: time.Now().UnixMilli(), Clock: clock, Level: normLevel(level), Kind: "log", Msg: clip(msg, 4000)})
	if ri != nil && !ri.b.barModel {
		s.changed(ri.b.acc.ID, "log")
	}
}

// Notify delivers a strategy message to the notification channels (live accounts only; backtests
// and trial runs keep it in the run log).
func (s *Service) Notify(instanceID int64, level, text string, data map[string]any) {
	if r := []rune(strings.TrimSpace(text)); len(r) > 1000 {
		text = string(r[:1000]) + "…"
	} else {
		text = string(r)
	}
	if text == "" {
		return
	}
	clock, ri := s.instClock(instanceID)
	line := LogLine{Ts: time.Now().UnixMilli(), Clock: clock, Level: normLevel(level), Kind: "notify", Msg: text}
	defer func() {
		s.logs.add(instanceID, line)
		if ri != nil && !ri.b.barModel {
			s.changed(ri.b.acc.ID, "log")
		}
	}()
	if ri == nil || ri.b.acc.Mode != ModeLive {
		return
	}
	if ok, why := s.logs.allow(instanceID, text, line.Ts); !ok {
		line.Msg += "（未发送：" + why + "）"
		return
	}
	line.Sent = true
	inst := ri.instance()
	title := "策略消息 · " + inst.Name
	if line.Level != "info" {
		title = "策略" + map[string]string{"warn": "警告", "error": "错误"}[line.Level] + " · " + inst.Name
	}
	lines := strings.Split(text, "\n")
	for _, k := range sortedKeys(data) {
		lines = append(lines, fmt.Sprintf("%s: %v", k, data[k]))
	}
	ev := ri.b.event("strategy", title, fmt.Sprintf("inst-%d", inst.ID), line.Ts, lines...)
	ev.Data = map[string]any{"instance": inst.ID, "strategy": ri.ver.Name, "level": line.Level, "text": text, "data": data}
	s.emit(ev)
	log.Printf("trade: instance %d notify: %s", inst.ID, clip(text, 200))
}

func normLevel(l string) string {
	switch l {
	case "warn", "warning":
		return "warn"
	case "error":
		return "error"
	}
	return "info"
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 { // do not cut a UTF-8 sequence
		n--
	}
	return s[:n] + "…"
}

package market

import (
	"testing"

	"crypto-trader-go/internal/store"
)

func TestWindow12hRequiresAll720(t *testing.T) {
	in := newInst("X-USDT-SWAP")
	m := int64(1_700_000_000_000) / minuteMs * minuteMs
	for k := int64(0); k < 720; k++ {
		in.putBar(store.Bar{Ts: m - k*minuteMs, C: 1, Quote: 10})
	}
	if sum, have := in.window12h(m); have != 720 || sum != 7200 {
		t.Fatalf("sum %v have %d", sum, have)
	}
	in.bars.del(m - 100*minuteMs) // transport gap: must not count as a zero-volume minute
	if _, have := in.window12h(m); have != 719 {
		t.Fatalf("gap not detected: have %d", have)
	}
}

func TestAggregateHourNeedsAllMinutes(t *testing.T) {
	in := newInst("X")
	h := int64(1_700_000_000_000) / hourMs * hourMs
	for k := int64(0); k < 60; k++ {
		in.putBar(store.Bar{Ts: h + k*minuteMs, O: float64(k), H: float64(k) + 1, L: float64(k) - 1, C: float64(k), Quote: 1})
	}
	b, ok := aggregateHour(in, h)
	if !ok || b.O != 0 || b.C != 59 || b.H != 60 || b.L != -1 || b.Quote != 60 {
		t.Fatalf("bad aggregate %+v", b)
	}
	in.bars.del(h + 5*minuteMs)
	if _, ok := aggregateHour(in, h); ok {
		t.Fatalf("incomplete hour must not aggregate")
	}
}

func TestSettledFundingInterval(t *testing.T) {
	var f fundingState
	for i, ts := range []int64{0, 8 * hourMs, 12 * hourMs} {
		f.addSettled(store.FundingSample{FundingTime: ts, Rate: float64(i + 1)})
	}
	r, iv, ok := settledAt(&f, 13*hourMs)
	if !ok || r != 3 || iv != 4 {
		t.Fatalf("got %v %v %v", r, iv, ok)
	}
	if _, _, ok := settledAt(&f, -1); ok {
		t.Fatalf("no settlement before first")
	}
}

func TestPreviewSeriesUsesFormingBar(t *testing.T) {
	in := newInst("X")
	m := int64(1_700_000_000_000) / minuteMs * minuteMs // forming bar open
	for k := int64(1); k <= 300; k++ {
		in.putBar(store.Bar{Ts: m - k*minuteMs, C: 100, Quote: 1})
	}
	in.live, in.liveRecv = store.Bar{Ts: m, C: 110, Quote: 5}, m+30_000
	s := previewSeries{liveSeries{in}, m + minuteMs, m + 30_000}
	if c, ok := s.Close(m + minuteMs); !ok || c != 110 {
		t.Fatalf("close at T = %v %v", c, ok)
	}
	if q, ok := s.Quote(m+minuteMs-4*hourMs, m+minuteMs); !ok || q != 239+5 {
		t.Fatalf("quote incl. forming bar = %v %v", q, ok)
	}
	if c, ok := s.Close(m + minuteMs - hourMs); !ok || c != 100 {
		t.Fatalf("past close = %v %v", c, ok)
	}
}

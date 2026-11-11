package aigate

import "testing"

func short() Plan {
	return Plan{Side: "short", EntryLo: 96, EntryHi: 97, Stop: 98, TP1: 94, TP2: 92, Status: "pending"}
}

func run(p Plan, bars ...Bar) Plan {
	for i, b := range bars {
		b.T = int64(i+1) * 60_000
		p.step(b)
		if !isOpen(p.Status) {
			break
		}
	}
	return p
}

func TestPlanLifecycle(t *testing.T) {
	cases := []struct {
		name string
		bars []Bar
		want string
	}{
		{"waits below the zone", []Bar{{H: 95.5, L: 95}}, "pending"},
		{"fills in the zone", []Bar{{H: 96.4, L: 95.8}}, "active"},
		{"filled then TP1", []Bar{{H: 96.4, L: 95.8}, {H: 95, L: 93.9}}, "tp1"},
		{"filled then TP2", []Bar{{H: 96.4, L: 95.8}, {H: 95, L: 93.9}, {H: 93, L: 91.5}}, "done"},
		{"filled then stopped", []Bar{{H: 96.4, L: 95.8}, {H: 98.2, L: 96}}, "stopped"},
		{"stop and TP in one bar: stop first", []Bar{{H: 96.4, L: 95.8}, {H: 98.5, L: 93}}, "stopped"},
		{"breaks the stop before a fill", []Bar{{H: 95.9, L: 95}, {H: 98.4, L: 97.6}}, "invalid"},
		{"runs to TP1 without a fill", []Bar{{H: 95.9, L: 93.8}}, "missed"},
	}
	for _, c := range cases {
		if got := run(short(), c.bars...).Status; got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	long := Plan{Side: "long", EntryLo: 100, EntryHi: 101, Stop: 98, TP1: 104, Status: "pending"}
	if got := run(long, Bar{H: 100.5, L: 99.8}, Bar{H: 104.2, L: 101}).Status; got != "done" {
		t.Errorf("long without TP2: got %s, want done", got)
	}
}

func TestHoldMs(t *testing.T) {
	const h = 3600_000
	for in, want := range map[string]int64{"1–3 天": 72 * h, "4-8 小时": 8 * h, "日内": 24 * h, "": 72 * h, "30 分钟": 2 * h, "2 周": 14 * 24 * h} {
		if got := holdMs(in); got != want {
			t.Errorf("holdMs(%q) = %dh, want %dh", in, got/h, want/h)
		}
	}
}

func TestPlanBlockParse(t *testing.T) {
	text := "x\n```verdict\n{\"direction\":\"short\",\"confidence\":\"中\"}\n```\n```plan\n{\"instId\":\"SOL-USDT-SWAP\",\"side\":\"short\",\"entry\":[96,96.9],\"stop\":97.8,\"tp\":[93.85,91.9],\"leverage\":8,\"hold\":\"1–3天\"}\n```"
	if m := planBlock.FindAllStringSubmatch(text, -1); len(m) != 1 {
		t.Fatalf("plan blocks: %d", len(m))
	}
	if c := confidenceOf(text); c != "中" {
		t.Errorf("confidence %q", c)
	}
}

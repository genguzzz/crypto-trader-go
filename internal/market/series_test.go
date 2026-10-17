package market

import (
	"reflect"
	"testing"
)

func TestLastPerStep(t *testing.T) {
	const m = int64(60_000)
	pts := []SeriesPoint{{T: 0, V: 1}, {T: m, V: 2}, {T: 14 * m, V: 3}, {T: 15 * m, V: 4}, {T: 29 * m, V: 5}, {T: 45 * m, V: 6}}
	got := LastPerStep(pts, 15*m)
	want := []SeriesPoint{{T: 14 * m, V: 3}, {T: 29 * m, V: 5}, {T: 45 * m, V: 6}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := LastPerStep(pts, 0); len(got) != len(pts) {
		t.Fatalf("step 0 must return the input, got %d points", len(got))
	}
	if got := LastPerStep(nil, 15*m); got != nil {
		t.Fatalf("nil input: got %v", got)
	}
}

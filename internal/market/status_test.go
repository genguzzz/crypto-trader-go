package market

import (
	"testing"
	"time"

	"crypto-trader-go/internal/okx"
)

// Status must not wait for table row counts (COUNT(*) over millions of rows takes seconds).
func TestStatusDoesNotBlockOnCounts(t *testing.T) {
	st := openStore(t)
	e := New(Config{Threshold: 1, TopN: 10}, okx.NewREST(), st)
	dbCounts.Lock()
	dbCounts.at, dbCounts.v, dbCounts.running = time.Time{}, nil, false
	dbCounts.Unlock()
	start := time.Now()
	e.Status()
	if d := time.Since(start); d > 2*time.Second { // inline COUNT(*) on a real DB takes ~8s
		t.Fatalf("Status took %s", d)
	}
	deadline := time.Now().Add(5 * time.Second) // counts arrive in the background
	for e.Status().DB == nil {
		if time.Now().After(deadline) {
			t.Fatal("counts never refreshed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

package market

import (
	"context"
	"path/filepath"
	"testing"

	"crypto-trader-go/internal/store"
)

// openStore opens a temp SQLite DB that is closed before the temp dir is removed.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	return st
}

// runWriter starts the batched DB writer and, at cleanup, waits for its final flush — otherwise the
// flush can race the temp-dir removal ("directory not empty"), a rare load-dependent failure.
func runWriter(t *testing.T, e *Engine) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.writer(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

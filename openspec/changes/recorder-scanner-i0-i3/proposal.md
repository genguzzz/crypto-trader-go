## Why

The user started implementation per the 2026-09-26 revision (I0 recorder first, then I1–I3) and asked for a usable URL. This change delivers a single Go binary with an embedded web UI.

## What Changes

- Stage: **implementation, slices I0–I3** (plus a prototype-quality UI on real recorded public data instead of a separate synthetic prototype, as agreed in the revision).
- `cmd/server` + `internal/{okx,store,market,features,rules,track,api}` + `web/` (Vue 3, TypeScript, Lightweight Charts), SQLite WAL persistence, HTTP snapshot + SSE.
- Recorder: full-pool 1m candles (business WS), OI + funding (public WS), tickers for active ∪ watchlist, 5m fast pool, 1m/1H backfill, gap-driven refill, restart recovery.
- Universe: all categories (user decision), >10M USDT 12h, cap 200, per confirmed minute.
- Fact panel, versioned user rules (3 unvalidated presets), hourly immutable score snapshots, 120h hourly replay, out-of-sample stats (rank IC, t, top-quintile excess/net of fees+spread).
- Optional HTTP Basic auth and outbound proxy/DoH for constrained networks.

## Non-goals

No private API, no positions (page shows not-connected), no orders, no news/unlock/holder providers (I4).

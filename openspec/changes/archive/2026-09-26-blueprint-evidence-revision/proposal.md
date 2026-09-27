## Why

The 2026-09-19 blueprint left performance, data coverage and scoring validity as "pending PoC". Public-API probes on 2026-09-26 (`research/poc/`) show several of its assumptions are wrong or unnecessary: the >10M 12h threshold yields ~24–95 instruments (Top200 cap never binds); 190 USDT swaps are stocks/commodities; full-pool tickers cost ~0.5 MB/s; fine-grained OI history exists only ~5 days; and the preset `entry-core-v0` direction has a negative out-of-sample rank IC at 1h/4h.

## What Changes

- Stage: **research (stage 1) revision**. Documentation plus throw-away public-only probe programs; no production code, no credentials, no orders.
- Add `docs/blueprint/revision-2026-09-26.md` as the superseding decision record; mark older blueprint files with a pointer.
- Universe: `instCategory` exposed as a field/filter (all categories included by user decision); threshold-driven "active pool (cap N)" semantics; 5m fast bootstrap then 1m authoritative window.
- Scoring: replace preset-sign default score with fact panel + user rules + automatically tracked out-of-sample record; drop 5s preview scores from MVP.
- Delivery: add slice I0 (public data recorder) before/parallel to the prototype; prototype uses recorded real public data.
- OSS: native narrow OKX adapter preferred over third-party SDK.

## Non-goals

No prototype UI, no Go production module, no private API, no strategy claims. Archive only after the user confirms the open decisions in §6 of the revision.

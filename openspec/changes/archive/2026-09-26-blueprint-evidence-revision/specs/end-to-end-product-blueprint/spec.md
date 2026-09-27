## ADDED Requirements

### Requirement: Evidence-backed blueprint revisions
Blueprint assumptions about universe size, feed bandwidth, history depth and factor direction SHALL be backed by reproducible public-data probes whose programs and outputs are kept in the repository, and superseded statements SHALL point to the revision instead of being silently rewritten.

#### Scenario: Reviewer questions a revised number
- **WHEN** a reviewer asks why the default pool is threshold-bound rather than Top200-bound
- **THEN** the revision SHALL cite the probe program, run time and output file that produced the measured pool size.

### Requirement: No preset factor direction in default ranking
The default ranking SHALL use a user-selected, versioned rule over a fact panel, and every enabled rule SHALL accumulate immutable score snapshots with automatically computed out-of-sample statistics; a rule SHALL be labelled unvalidated until it meets the documented threshold.

#### Scenario: A rule shows negative out-of-sample IC
- **WHEN** a rule's rolling out-of-sample rank IC is negative or insignificant
- **THEN** the UI SHALL display it as unvalidated with its record, and SHALL NOT describe its score as entry quality.

## MODIFIED Requirements

### Requirement: Coherent market-time and universe semantics
The blueprint SHALL distinguish discovery polling, WebSocket arrivals, confirmed 1m candle windows, confirmed scoring, stale-data states, and membership transitions with explicit timestamps and rate-limit budgets; it SHALL filter instruments by contract type, SHALL expose instrument category (crypto, stock/ETF, commodity) as a visible field and filter, and SHALL describe the active pool as threshold-bound with an upper cap.

#### Scenario: Instrument crosses universe boundary during a disconnect
- **WHEN** input candle data has a gap or a later REST response contains older exchange timestamps
- **THEN** the blueprint SHALL specify safe recovery and SHALL NOT promise a fresh exact ranking while the gap remains.

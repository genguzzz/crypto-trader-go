# end-to-end-product-blueprint Specification

## Purpose
TBD - created by archiving change end-to-end-trading-tool-blueprint. Update Purpose after archive.
## Requirements
### Requirement: Complete requirement-to-implementation traceability
The blueprint SHALL map every requested user-facing capability to its source, units, algorithm or transformation, backend module, API/UI contract, degraded behavior, development slice, and acceptance check.

#### Scenario: Reviewer inspects a single feature
- **WHEN** a reviewer selects the live Top200 list, chart comparison, news/unlock tag, or position exit assessment
- **THEN** the blueprint SHALL identify each upstream dependency and how to validate its correct operation without assuming the feature has been implemented.

### Requirement: Coherent market-time and universe semantics
The blueprint SHALL distinguish discovery polling, WebSocket arrivals, confirmed 1m candle windows, confirmed scoring, stale-data states, and membership transitions with explicit timestamps and rate-limit budgets; it SHALL filter instruments by contract type, SHALL expose instrument category (crypto, stock/ETF, commodity) as a visible field and filter, and SHALL describe the active pool as threshold-bound with an upper cap.

#### Scenario: Instrument crosses universe boundary during a disconnect
- **WHEN** input candle data has a gap or a later REST response contains older exchange timestamps
- **THEN** the blueprint SHALL specify safe recovery and SHALL NOT promise a fresh exact ranking while the gap remains.

### Requirement: Auditable OSS decision
The blueprint SHALL compare a direct OKX adapter, dedicated Go SDKs and larger trading frameworks against verified licensing, OKX USDT SWAP coverage, maintenance, lock-in, performance and PoC criteria, and SHALL separate accepted, evaluated, deferred and rejected components.

#### Scenario: Dependency is selected
- **WHEN** a reviewer asks why a library is preferred over another
- **THEN** a repository/source link, version/pin policy, integration boundary and measurable acceptance/rollback condition SHALL be available.

### Requirement: Explainable and falsifiable scoring and portfolio design
The blueprint SHALL define independent long/short entry and held-position exit directions, factor provenance/units/window, missingness and hard risk constraints, and offline/online parity, backtest and forward validation protocols.

#### Scenario: Volatility/price rise with unconfirmed open interest
- **WHEN** turnover and price rise but coin-denominated open interest or spot participation fails to confirm
- **THEN** the specification SHALL distinguish weaker confirmation or an evidence penalty from any unsupported claim of wash trading or guaranteed reversal.

### Requirement: Delivery separation and security
The blueprint SHALL define prototype and implementation slices with dependency order and acceptance gates; it SHALL keep account access read-only until separately approved and SHALL use synthetic data for prototypes.

#### Scenario: Stage transition
- **WHEN** the blueprint is archived
- **THEN** the project's instructions SHALL describe the enhanced research as complete but SHALL NOT claim prototype, live position access or automatic trading is complete.

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

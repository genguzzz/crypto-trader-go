## ADDED Requirements

### Requirement: Complete requirement-to-implementation traceability
The blueprint SHALL map every requested user-facing capability to its source, units, algorithm or transformation, backend module, API/UI contract, degraded behavior, development slice, and acceptance check.

#### Scenario: Reviewer inspects a single feature
- **WHEN** a reviewer selects the live Top200 list, chart comparison, news/unlock tag, or position exit assessment
- **THEN** the blueprint SHALL identify each upstream dependency and how to validate its correct operation without assuming the feature has been implemented.

### Requirement: Coherent market-time and universe semantics
The blueprint SHALL distinguish discovery polling, WebSocket arrivals, confirmed 1m candle windows, fast preview scoring, confirmed scoring, stale-data states, and membership transitions with explicit timestamps and rate-limit budgets.

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

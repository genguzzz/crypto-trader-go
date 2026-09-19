# go-okx-trading-product-research Specification

## Purpose
TBD - created by archiving change go-okx-personal-trading-research. Update Purpose after archive.
## Requirements
### Requirement: Phased Go-first product research
The research deliverable SHALL document the Go-first target, historical Python assets in the separate old repository, and separately gated research, prototype, and implementation stages; it SHALL NOT describe later stages as completed.

#### Scenario: Contributor opens project
- **WHEN** a contributor reads root-level project instructions and the research report
- **THEN** they SHALL find the target, phase scope, related OpenSpec change, and the prior Python/Top100/24h decision as historical rather than current architecture.

### Requirement: Verifiable market-data semantics
The research SHALL define universe ordering, configurable 12h USDT volume threshold and Top200 cap, approximately five-second reconciliation, units and freshness for OKX public feeds, and the difference between 1m candle aggregation and an exact trade-level sliding window.

#### Scenario: Instrument crosses the threshold
- **WHEN** a previously unqualified instrument's validated rolling 12h turnover crosses the configured threshold
- **THEN** the proposed design SHALL describe its entry, removal and updates without labeling 24h base volume as quote turnover.

### Requirement: Explainable directional and portfolio analysis
The research SHALL specify separate long/short entry and long/short position-exit hypotheses, per-factor reasons, quality/missing flags, and validation criteria; it SHALL distinguish public exchange data from third-party news, supply and holder information.

#### Scenario: Input data is incomplete
- **WHEN** the source lacks reliable spot mapping, holder attribution or OI history
- **THEN** the proposal SHALL display the data as unavailable or low confidence rather than infer a directional signal.

### Requirement: Primary-source OSS comparison and safe delivery
The research SHALL cite project repositories and official exchange documentation, contrast at least three Go alternatives, recommend a minimal architecture, and forbid secret access, account mutations and real orders during research or prototyping.

#### Scenario: Go dependency is chosen
- **WHEN** comparing a dedicated OKX client, a multi-exchange framework and direct API integration
- **THEN** the report SHALL record license, coverage of perpetuals, maintenance, unverified claims and a reasoned adoption boundary.

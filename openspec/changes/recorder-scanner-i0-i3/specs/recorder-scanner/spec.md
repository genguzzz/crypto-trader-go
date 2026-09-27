## ADDED Requirements

### Requirement: Minute-confirmed threshold universe
The system SHALL publish the active pool once per confirmed minute from complete 720-minute windows of `volCcyQuote`, strictly above the configured threshold, capped at N, ordered by turnover then instId, and SHALL mark incomplete instruments stale or warming instead of ranking them.

#### Scenario: Transport gap
- **WHEN** an instrument is missing any minute in its window
- **THEN** it SHALL be excluded from the pool with status stale and its gap SHALL be refilled from REST.

### Requirement: Rule scores with tracked out-of-sample record
Scores SHALL come only from versioned user rules over the fact panel; every rule version SHALL accumulate immutable hourly snapshots whose statistics are displayed, and every rule SHALL be labelled unvalidated in the UI.

#### Scenario: Rule edited
- **WHEN** a rule is saved
- **THEN** a new version SHALL be created, older versions' records SHALL be kept, and the new version's replay SHALL be recomputed.

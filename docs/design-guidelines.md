# UI design guidelines

> Every page change follows these rules; check here for an existing pattern before adding a component. Tokens are defined in `web/src/style.css`.

## 1. Information hierarchy (most important)

Each panel has exactly one visual focus, presented in three levels: primary → secondary → tertiary.

| Level | Used for | Style |
|---|---|---|
| Primary | price, score, key numbers, contract name | `--text-1`, weight 600–700, `--fs-lg` – `--fs-3xl` |
| Secondary | supporting numbers, secondary columns | `--text-2`, weight 400–500 |
| Tertiary | labels, units, times, notes | `--text-3`, `--fs-xs`/`--fs-sm` |

- Metrics use **small grey label on top, value below, supporting note below that** (see `.stat` in the detail header); never cram "label: value" onto one line.
- Units (USDT, bps, coins) are de-emphasized with `<small>`; numbers always use `tabular-nums` so they don't jitter on refresh.
- Color carries meaning only: up `--up`, down `--down`, warning `--warn`, interactive/selected `--accent` (OKX lime; text on an accent fill uses `--on-accent`, black). Neutral data stays uncolored; long/short scores below 50 stay grey.
- The palette follows OKX's web trading terminal: pure black `--bg-0`, neutral (not blue-tinted) grey panels, OKX green/red for up/down. Chart canvas colors come from `src/theme.ts` (same values as the `style.css` tokens); components never hard-code colors.
- Loading uses `ui/Loading.vue`: over charts as an `overlay` on top of the old content (keep the old chart, no white flash); inline when a whole block hasn't arrived yet. Wrap async loaders with `track` from `composables/busy.ts`.

## 2. Explanatory text: collapsed by default, shown on demand

**Don't pile explanatory text onto pages.** Definitions, formulas, data sources and caveats always go into an interactive component:

| Situation | Component | Rule |
|---|---|---|
| Definition, method or data source of a panel/field | `<InfoTip>` (ⓘ) | Next to the panel's `h3`; hover on PC, tap opens a bottom sheet on touch. The content may be detailed (paragraphs, bold keywords, examples). |
| Scoring formula and rationale | `<ScoringExplainer />` | Used inside an InfoTip; never copy the formula text onto pages. |
| Large detail (e.g. every fact-panel field) | `<Modal>` | Opened by a button; closes on ESC or backdrop click; bottom sheet on phones. |

Text allowed directly on a page: **dynamic state** (errors, empty states, loading, save results, missing fields, live counts) and **one-line essential warnings** (e.g. "not connected ≠ no positions").

## 2.1 Clickable metric cards and detail views

- Metric cards use `ui/StatCard.vue` (label + icon / value / supporting line; hover shows an arrow and a light background). Clickable cards open a detail view instead of navigating away.
- Detail views are hosted by `details/StatDetailDialog.vue`: a large centered dialog on PC, a bottom sheet on phones; tabs at the top switch between details.
- Adding a detail = register `{key, label, icon, component}` in `details/registry.ts`; a detail component receives only `ctx: DetailCtx`.
- Charts inside details must be reusable components under `components/charts/*` shared with page panels (e.g. `OIChart` serves both the panel and the OI detail; `OrderBookView` serves the order-book panel and its detail).
- Numeric rows use `ui/KpiRow.vue`; icons come from lucide-vue-next.
- Grid containers always use `grid-template-columns: minmax(0, 1fr)` (or `repeat(n, minmax(0, 1fr))`) so unbreakable content can't widen the phone layout.

## 3. Tokens and spacing

- Backgrounds: `--bg-0` page / `--bg-1` panel / `--bg-2` raised and hover / `--bg-3` input and selected. Borders `--line` (hairline) / `--line-2` (strong). `--oi-bar` is the scanner's open-interest row bar.
- Type scale: 11 / 12 / 13 / 14 / 16 / 20 / 28 (`--fs-xs` … `--fs-3xl`); no ad-hoc px sizes.
- Spacing on a 4 px grid; panel padding 14–16 px, radius `--radius` (10 px), small controls `--radius-sm`.
- Tags `.tag` (good/warn/bad/info semantics), status dots `.dot` (ok/warn/bad).
- Funding rates are colored by level with `fundingClass()` from `fmt.ts` (`.fr1`–`.fr3` × `.pos`/`.neg`), the same everywhere they appear.

## 3.1 Component library (single entry point; don't hand-roll equivalents)

Base interactive components are built on **Reka UI** (unstyled, fully accessible: keyboard, focus, ARIA, portals) and styled with this project's tokens:

| Need | Use | Don't |
|---|---|---|
| Single choice (direction, period, range, presets) | `<Segmented>` (global; ToggleGroup single, can't deselect) | side-by-side buttons with negative margins (clips the highlight border) |
| Dropdown (rule, sort, defaults) | `<USelect>` (global; groups/descriptions/badges/check marks) | native `<select>` |
| Multi-select filter (categories) | `ui/ChipGroup.vue` (ToggleGroup multiple, optional `at-least-one`) | hand-written span.chip |
| On/off filter (watchlist, gate) | `ui/FilterChip.vue` (Toggle; dashed when off, solid accent + count when on) | |
| Enable/disable a list item (e.g. alert rules) | `ui/Switch.vue` (Reka Switch; on = accent track) | checkbox |
| Search | `ui/SearchInput.vue` (icon, clear, `/` to focus, Esc to clear) | bare input |
| Floating menu / list | Reka `Popover` (style class `.lm-pop`) | expanding large blocks inline |
| Metric cards / details | `ui/StatCard.vue` + `details/registry.ts` | |
| Settings | `settings/SettingsSection.vue` (grouped card) + `settings/SettingRow.vue` (label + one-line description left, control right, unsaved dot, inline error) | stacked bare labels |
| Usage / progress bar | `ui/Meter.vue` (turns to warning at ≥ 90%) | hand-written div bars |
| Section jumps on long pages | `status/SectionNav.vue` (sticky left on PC with scroll-spy, horizontal pills on phones) | |

## 3.2 Global navigation and layout

- Top bar: brand mark + icon navigation (selected = light pill + accent icon) + market pulse (BTC/ETH) + status pill + layout button; 52 px tall. On phones navigation moves to a bottom tab bar.
- PC is an app shell: the window never scrolls, only the content area does (no double scrollbars); phones keep document scrolling.
- Layout settings (panel order, list width) never add hints or buttons to the content area; register them in the top "Layout" menu (`useLayoutEntry`).
- Wide tables go inside `.table-scroll`; any grid column uses `minmax(0, 1fr)`; on phones the page width must equal the viewport (the screenshot script checks `scrollWidth`).

## 4. Interaction

- PC-only interactions (dragging panels or dividers, hover tips) are gated by `useDesktop()`; touch devices get an equivalent tap interaction or a fixed layout.
- Data refreshes must not move what the user is looking at: keep the selection, keep sort order stable, update only changed cells.
- Unknown values show "—" with the meaning explained in a tip; never use 0 or blank for unknown.

## 5. References

Exchange trading terminals (Binance / OKX / Bybit) for ticker headers and list density; Radix Colors' graded scales (background and text roles); IBM Carbon's productive type scale and 4 px grid; Linear / Vercel Geist navigation and filter bars (icon + pill, dashed inactive filters, keyboard shortcuts); Reka UI (MIT, the Vue port of Radix Primitives) for interaction primitives.

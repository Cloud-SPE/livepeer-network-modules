# UI design system

**Status:** active  
**Last updated:** 2026-05-08

Implementation rules for DOM structure and CSS location live in
[`frontend-dom-and-css-invariants.md`](./frontend-dom-and-css-invariants.md). This
document defines the visual system; the invariant doc defines the mandatory frontend
authoring model.

## Purpose

The rewrite now has multiple operator and product-facing UIs:

- `secure-orch-console`
- `openai-gateway`
- `video-gateway`
- `vtuber-gateway`
- future operator surfaces around resolver / protocol / coordinator status

If each surface develops its own look, the repo will drift into a pile of
inconsistent dashboards. This doc defines the shared visual and interaction system
for all rewrite UIs.

## Brand anchor

The design system should align to the current Livepeer public brand and network
surfaces, not invent a separate visual language.

Reference surfaces:

- `livepeer.org`
- `explorer.livepeer.org`
- Livepeer brand guidelines: `https://livepeer.org/media-kit`

Observed common traits:

- dark, high-contrast surfaces
- restrained but recognizable green accent
- premium, technical, modern presentation
- strong metric/data density on network surfaces
- minimal ornamental noise

From the current Livepeer brand guidelines:

- Accent Green: `#18794E`
- Primary Black: `#181818`
- Dark Card: `#242424`
- Dark Border: `#2A2A2A`
- Primary White: `#FFFFFF`
- Primary typeface: Favorit Pro
- Secondary/technical typeface: Favorit Mono

## Design goal

Create a unified Livepeer control-plane and product-system aesthetic:

- polished
- professional
- technical
- premium
- clearly part of the same network

The system should make every UI feel like one family, while still allowing each app
to express its role.

## Implementation status

The design system is now partially implemented, not only planned.

The shared frontend package lives at:

- `customer-portal/frontend/shared`

Current adopted apps:

- `customer-portal`
- `video-gateway`
- `vtuber-gateway`

Current implementation note:

- the shared and product UIs now follow the light-DOM + external-CSS invariant
  contract
- the migration that removed shadow-DOM + CSS-in-TS patterns is recorded in
  [`../exec-plans/completed/0023-strict-frontend-dom-and-css-invariants.md`](../exec-plans/completed/0023-strict-frontend-dom-and-css-invariants.md)
- new UI work must continue to follow the invariant doc

Current mode wiring in apps:

- `document.body.dataset.livepeerUiMode = "network-console"`
- `document.body.dataset.livepeerUiMode = "product-app"`

New primitives should land in that shared package before being copied into app-local
CSS or component trees.

## Product modes

There should be one system with two presentation modes.

### 1. Network Console

Used for:

- `secure-orch-console`
- protocol/resolver/admin/operator views
- coordinator or manifest-management surfaces
- dense account/network/state dashboards

Characteristics:

- denser layouts
- stronger data/table presence
- more mono usage
- stronger emphasis on state, validation, diffing, logs, identity

### 2. Product App

Used for:

- `openai-gateway` portal/admin views
- `video-gateway`
- `vtuber-gateway`

Characteristics:

- more user-task-oriented flows
- clearer call-to-action hierarchy
- more breathing room
- still clearly Livepeer, but less "block explorer"

## Design principles

### 1. Dark-first, not dark-only

The default system should be dark-first because it matches current Livepeer brand
and operator usage patterns. But the tokens should be defined so light variants are
possible later for documentation or embedded product surfaces.

### 2. Accent is scarce

Green is a brand accent, not a background fill strategy.

Use green for:

- primary actions
- active states
- success/healthy states
- focus accents
- key charts or selected tabs

Do not wash the whole UI in green.

### 3. Data surfaces must feel trustworthy

Tables, metrics, cards, logs, manifests, addresses, balances, status pills, and
diffs should feel precise and stable.

That means:

- consistent spacing
- strong alignment
- predictable typography
- visible hierarchy
- explicit empty, loading, and error states

### 4. Technical but not crude

Avoid generic crypto-dashboard tropes:

- neon everywhere
- glassmorphism overload
- random gradients
- oversized pills on every surface

The tone should be closer to "infrastructure product" than "token landing page".

### 5. Motion is minimal and meaningful

Use motion for:

- page-load reveal
- success/error confirmation
- drawer/modal transitions
- small chart/metric updates

Avoid gratuitous animation.

## Token system

All UI packages should consume shared tokens rather than hand-coded values.

### Color tokens

Core:

- `--lp-color-bg: #181818`
- `--lp-color-surface-1: #1E1E1E`
- `--lp-color-surface-2: #242424`
- `--lp-color-border: #2A2A2A`
- `--lp-color-text-primary: rgba(255,255,255,0.96)`
- `--lp-color-text-secondary: rgba(255,255,255,0.78)`
- `--lp-color-text-muted: rgba(255,255,255,0.56)`

Brand/action:

- `--lp-color-accent: #18794E`
- `--lp-color-accent-hover: #1E9960`
- `--lp-color-accent-strong: #40BF86`
- `--lp-color-accent-dark: #115C3B`
- `--lp-color-accent-subtle: rgba(24,121,78,0.15)`

State:

- `--lp-color-success`
- `--lp-color-warning`
- `--lp-color-danger`
- `--lp-color-info`

These can be derived, but they must be defined centrally.

Implemented centrally in:

- `customer-portal/frontend/shared/src/css/tokens.css`
- `customer-portal/frontend/shared/src/global-styles.ts`

### Typography tokens

Primary:

- brand/product UI: Favorit Pro

Technical:

- addresses
- hashes
- balances
- logs
- manifest diffs
- table cells that need fixed rhythm

Use Favorit Mono or the closest licensed/fallback mono stack.

Type scale:

- display
- h1
- h2
- h3
- body-lg
- body
- body-sm
- label
- mono-sm

### Spacing tokens

Single spacing scale for all apps:

- `4, 8, 12, 16, 24, 32, 40, 48, 64`

Avoid app-specific spacing scales.

### Radius/shadow tokens

Cards should feel crisp, not bubbly.

- small radius for inputs
- medium radius for cards/drawers
- restrained shadow scale

## Shared component set

### Implemented now

- `portal-layout`
- `portal-card`
- `portal-button`
- `portal-input`
- `portal-toast`
- `portal-modal`
- `portal-balance`
- `portal-metric-tile`
- `portal-status-pill`
- `portal-data-table`
- `portal-action-row`
- `portal-detail-section`

### Still desirable

- page header
- form row / fieldset helper
- select
- textarea helper
- icon button
- tabs
- callout
- empty state
- loading skeleton
- drawer
- diff viewer
- code/mono block helper

## Primitive usage rules

### `portal-layout`

- Use for top-level browser shells.
- Required for pages that behave like an app, not a one-off embedded widget.

### `portal-card`

- Use as the default content container.
- Do not build page-local card shells unless the surface truly needs a different treatment.

### `portal-metric-tile`

- Use for hero summaries and top-level metrics.
- Keep values short and scannable.

### `portal-status-pill`

- Use for machine state, not arbitrary labels.
- Map variants consistently:
  - `success`: healthy, ready, completed, live
  - `warning`: degraded, retrying, operator concern
  - `danger`: failed, dead-lettered, destructive
  - `info`: active in-progress states that are not failures
  - `neutral`: idle, unknown, ended, pending without error

### `portal-data-table`

- Use for list-heavy screens instead of ad hoc table headers inside cards.
- Put search/create/filter controls into the `toolbar` slot.
- Pair with `portal-status-pill` and `portal-action-row` for row state/actions.

### `portal-action-row`

- Use for inline action clusters inside tables, detail cards, and reveal panels.
- Use `ghost` buttons for non-destructive secondary actions.
- Use `danger` buttons for destructive actions like `Remove`, `End`, `Refund`.

### `portal-detail-section`

- Use for stacked detail panels inside a larger card.
- Prefer it over repeated `<section><h3>...` patterns when a page has multiple subsections.

## Surface-specific guidance

### `secure-orch-console`

Tone:

- highest trust
- sparse
- explicit
- operator-safe

Design cues:

- prominent diff cards
- sign/publish actions isolated and high-confidence
- no noisy gradients
- clear immutable audit feel

### `openai-gateway`

Tone:

- developer product
- usage and billing clarity
- route/network visibility

Design cues:

- API-key, usage, rate-card, route health
- product-app mode

### `video-gateway`

Tone:

- media operations
- session/live-state visibility
- asset pipeline confidence

Design cues:

- stream/session cards
- ingest/playback state
- media asset tables

### `vtuber-gateway`

Tone:

- product-app mode with media-session richness
- still unmistakably Livepeer

Design cues:

- session state, control, balance, persona/media config
- richer visual affordances than secure-orch, but same token base

## Interaction rules

### Forms

- labels always visible
- helper text muted but readable
- destructive actions clearly separated
- validation inline, not hidden in toasts only

### Tables

- fixed column rhythm
- mono for addresses/hashes/numeric values where needed
- row hover subtle
- active filters visible

### Buttons

- one clear primary action per area
- secondary actions neutral
- destructive actions red, never green

### Status

- every async/network state gets a visible status model:
  - healthy
  - degraded
  - unavailable
  - pending
  - stale

These statuses should render consistently across apps.

## Implementation recommendation

Use one shared package for all frontends, not copy-pasted CSS.

Recommended shape:

```text
ui-design-system/
  tokens/
  css/
  components/
  patterns/
  docs/
```

If this repo wants to keep frontend-sharing inside the existing shell, the minimum
acceptable alternative is:

- shared tokens and primitives under `customer-portal/frontend/shared/`
- mandatory reuse by `openai-gateway`, `video-gateway`, and `vtuber-gateway`

But the design system should be treated as a first-class cross-cutting module, not
just a handful of helper CSS files.

## Adoption rules

When building or editing a UI in this repo:

1. Set the correct `livepeerUiMode` at app startup.
2. Import from `@livepeer-rewrite/customer-portal-shared` before writing new primitives.
3. If a pattern repeats across more than one page, promote it into the shared package.
4. If a pattern is app-specific, keep it local but compose it from shared primitives.
5. Update this doc when a new primitive or mode rule becomes load-bearing.

## Short checklist

Before considering a new UI screen done, check:

- uses shared mode and global styles
- no duplicate page-local card/button/table styling without justification
- list screens use `portal-data-table`
- machine state uses `portal-status-pill`
- repeated actions use `portal-action-row`
- repeated subsections use `portal-detail-section`
- colors come from shared tokens, not ad hoc literals
- success/error/empty/loading states are explicit

## Rollout order

### Phase 1 — foundations

- define tokens
- define typography rules
- define card/form/table/button primitives

### Phase 2 — operator surfaces

- `secure-orch-console`
- coordinator/protocol/resolver admin surfaces

### Phase 3 — product surfaces

- `openai-gateway`
- `video-gateway`
- `vtuber-gateway`

### Phase 4 — polish

- motion
- iconography
- charts/data-viz conventions
- docs/site examples

## Non-goals

- replicating the marketing site wholesale inside every app
- adding heavy brand flourish to operator workflows
- allowing each app team to invent its own palette or type scale

## Acceptance criteria

The system is working when:

- all UIs share the same token source
- buttons/cards/forms/tables/status states look related everywhere
- `secure-orch-console`, `openai-gateway`, `video-gateway`, and `vtuber-gateway`
  feel like products from the same organization
- operator surfaces feel trustworthy and calm
- customer-facing surfaces feel polished and premium without drifting away from the
  Livepeer network identity

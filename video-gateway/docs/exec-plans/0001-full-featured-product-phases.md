# 0001 — Video Gateway Full-Featured Product Phases

## Goal

Turn `video-gateway/` from a mostly-complete gateway shell with partial
media workflows into a full-featured customer and operator product for:

- live stream creation, tracking, and record-to-VOD
- VOD upload, submit, orchestration, and playback
- customer pricing, usage, and billing visibility
- operator controls for routing, failures, and media lifecycle

This plan is intentionally phased. The current codebase already has
working slices across auth, admin, portal, resolver-based selection,
live session-open, and VOD quoting. The remaining work is mostly about
closing workflow gaps, making state transitions real, and exposing that
state consistently across API, portal, and admin.

## Non-goals

- New customer identity or wallet logic in `video-gateway/`
  - `customer-portal/` remains the system of record for customer auth,
    API keys, ledger movement, and shared admin/customer shell logic.
- Re-introducing `video:transcode.vod`
  - The active product model is `video:transcode.abr` for all VOD work,
    including one-rendition jobs.
- CDN-specific playback policy logic
  - Strict-proxy playback remains the default component stance unless a
    later phase explicitly expands it.

## Current baseline

The component already has:

- portal and admin SPAs served from the gateway image
- resolver-based route selection
- live RTMP session-open through broker capability selection
- persisted live stream rows and customer live stream management routes
- VOD quote and VOD route selection
- pricing surfaced in the customer portal
- operator views for nodes, assets, streams, recordings, webhooks, and
  customer/account operations

The largest missing pieces are:

- real end-to-end VOD execution and asset lifecycle
- real live telemetry and live usage accounting
- complete recording-to-asset handoff
- customer-visible usage and charge history
- project scoping and product-grade tenancy polish
- operator-grade retry/requeue/drain/debug workflows
- broader test and migration hardening

## Phase 1 — Make Current Workflows Honest

### Objective

Finish the persistence and state transitions for the workflows already
present in the UI and API.

### Scope

- VOD asset and upload persistence
  - make `/portal/uploads`, `/portal/assets`, `/admin/assets`, and the
    public VOD asset routes reflect real `media.*` state
  - persist selected broker/capability/offering on submit
- Live stream lifecycle stabilization
  - make create/end/status reflect durable state consistently across
    public API, portal, and admin
  - reconcile in-memory live session routing with persisted rows cleanly
- Auth/session regression hardening
  - ensure portal and admin login survive refresh and isolate their
    browser sessions from each other
  - add targeted frontend tests for login, sign-out, and refresh
- Migration/startup hardening
  - ensure first boot and upgrade boot both succeed without manual DB
    intervention

### Deliverables

- persisted upload and asset state transitions
- real live stream status responses instead of placeholders
- stable admin/portal login behavior on refresh
- smoke-tested migration boot path

### Acceptance criteria

- upload -> submit -> asset status changes are visible in portal and admin
- create stream -> list stream -> end stream works after process restart
- admin and portal remain signed in after refresh in the same tab session
- clean deploy on an empty database succeeds without operator patch-ups

## Phase 2 — Real VOD Execution and Recording Handoff

### Objective

Turn VOD and record-to-VOD into real media workflows instead of route
selection placeholders.

### Scope

- `video:transcode.abr` orchestration
  - drive real broker/worker dispatch for submit
  - persist job rows, rendition rows, and output state transitions
- Asset finalization
  - write manifest/playback/storage state that the portal and admin can
    inspect directly
- Recording handoff
  - convert live recordings into real VOD asset records
  - connect recording state to resulting assets and playback records
- Failure handling
  - persist explicit failure states and messages
  - expose retry/requeue hooks for operator use later

### Deliverables

- real VOD submit lifecycle from queued to completed or failed
- real record-to-VOD asset creation path
- persisted failure reasons for failed media work

### Acceptance criteria

- VOD submit produces real asset/rendition state changes
- completed jobs surface playback-ready outputs
- live recordings appear in portal/admin with linked asset IDs
- failed jobs explain why they failed in operator-visible state

## Phase 3 — Usage, Billing, and Rate Card Completion

### Objective

Make customer charges inspectable and reconcile actual work to wallet
movement.

### Scope

- VOD usage records
  - persist actual media usage and final charges
- Live usage records
  - convert current placeholders into real session debit/usage entries
- Customer billing surfaces
  - add usage history and charge breakdowns
  - show per-asset/per-stream cost summaries
- Operator billing visibility
  - expose charge, debit, reservation, and refund state in admin
- Quote accuracy
  - align public quote responses with eventual committed charges

### Deliverables

- real `usage_records` / `live_session_debits` population
- customer-visible usage history
- admin billing reconciliation surfaces

### Acceptance criteria

- customers can answer “what did I get charged for?”
- operators can answer “why is this balance what it is?”
- VOD quotes are materially consistent with committed VOD charges

## Phase 4 — Projects, Tenancy, and Integration Surfaces

### Objective

Promote the current customer-level app into a real multi-project product
surface.

### Scope

- Project CRUD and listing
- explicit scoping of assets, streams, recordings, and webhooks to
  projects
- project-aware portal and admin views
- stronger tenant boundary enforcement in all product routes
- webhook/event catalog hardening
  - improve event taxonomy, visibility, replay, and filtering

### Deliverables

- real projects API and UI flows
- project-scoped media resources
- cleaner event/integration model

### Acceptance criteria

- customers can manage more than one project cleanly
- every media resource is project-scoped in API, portal, and admin
- webhooks are inspectable and manageable as a product feature

## Phase 5 — Operator Controls and Observability

### Objective

Make the admin console sufficient for day-to-day operations.

### Scope

- Route and node operations
  - inspect candidate coverage for live and VOD
  - drain/disable/override routing workflows where appropriate
- Job operations
  - retry, requeue, and inspect failed VOD/recording jobs
- Live observability
  - viewer counts, ingest health, disconnect reasons, session metrics
- Playback/access policy operations
  - surface playback IDs, access controls, and delivery posture

### Deliverables

- richer node and worker views
- operator controls for failed media work
- meaningful live session telemetry

### Acceptance criteria

- operators can manage and debug failed media work from admin
- live session state is operationally useful, not just present
- node/routing views show enough metadata to explain route choices

## Phase 6 — Production Hardening and Cleanup

### Objective

Lock the component for sustained use and remove drift between docs,
runtime, and operator expectations.

### Scope

- end-to-end tests across:
  - portal auth
  - admin auth
  - VOD submit/finalize
  - live create/end
  - record-to-VOD
  - migrations
- resilience testing
  - worker unavailability
  - broker unavailability
  - restart recovery
  - stale live session cleanup
- doc cleanup
  - remove stale references to older VOD capability split or runner
    assumptions
  - align operator docs, compose examples, and manifests with the
    final active product model

### Deliverables

- E2E coverage for core product flows
- updated runbooks and operator docs
- reduced design/runtime drift

### Acceptance criteria

- deploy/runbook is reproducible
- primary failure modes are tested and documented
- docs describe the system that actually ships

## Recommended sequencing inside the phases

### Immediate next work

1. Phase 1 VOD asset/upload persistence
2. Phase 1 live lifecycle stabilization
3. Phase 1 auth/session regression tests
4. Phase 1 migration/startup hardening

### After Phase 1 lands

1. Phase 2 real `video:transcode.abr` submit execution
2. Phase 2 record-to-VOD finalized asset handoff
3. Phase 2 failure-state persistence and operator inspection

### After Phase 2 lands

1. Phase 3 actual usage/debit population
2. Phase 3 customer billing/usage UI
3. Phase 3 operator billing reconciliation views

## Risks and constraints

- `customer-portal/` remains a shared dependency and must stay the owner
  of customer auth, API keys, ledger movement, and shell UX primitives.
- The current live path still relies on broker-managed RTMP/HLS session
  behavior. Richer live telemetry may require additions outside this
  component.
- The current VOD APIs are ahead of the actual execution lifecycle in
  some places. Phase 2 should prefer making existing routes real over
  inventing parallel routes.
- Browser UX regressions have already occurred in portal/admin session
  handling. Frontend behavior must be validated with browser-facing
  tests, not only `tsc` and bundle builds.

## Definition of done for the component

`video-gateway/` is “full-featured” when:

- customers can upload, submit, monitor, and play VOD assets
- customers can create, monitor, end, and optionally record live streams
- customers can understand pricing, usage, and charges
- operators can inspect routing, failures, streams, recordings, assets,
  and billing state from admin
- migrations, startup, and primary failure modes are tested and
  documented

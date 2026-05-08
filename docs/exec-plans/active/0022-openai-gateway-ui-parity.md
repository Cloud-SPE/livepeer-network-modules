# Plan 0022 — openai-gateway admin + customer UI parity

**Status:** active  
**Opened:** 2026-05-08  
**Owner:** harness  
**Related:** `customer-portal/`, `openai-gateway/`, `video-gateway/`, `vtuber-gateway/`, old sibling source repos `openai-livepeer-bridge/`, `livepeer-cloud-openai-ui/`

## 1. Why this plan exists

The deployed rewrite `openai-gateway` currently serves:

- `/healthz`
- OpenAI-compatible `/v1/*` API routes

It does **not** currently serve:

- a customer portal UI
- an operator admin UI
- static frontend assets mounted into the gateway runtime

The older working bridge did all three in one runtime:

- served the customer portal SPA at `/portal/`
- served the operator admin SPA at `/admin/console/`
- served matching JSON APIs from the same Fastify process

The rewrite already has:

- a shared UI/design-system package in `customer-portal/frontend/shared/`
- starter scaffolds in:
  - `customer-portal/frontend/portal/`
  - `customer-portal/frontend/admin/`
- product-specific real frontends in:
  - `video-gateway/src/frontend/...`
  - `vtuber-gateway/src/frontend/...`

But for `openai-gateway`, the portal/admin path has not yet been wired up. This plan
gets that service to the point where it has a deployable customer UI and admin UI
again.

## 2. What inspection showed

### 2.1 Old bridge

The old bridge had real browser apps and served them from the same container:

- frontend apps:
  - `openai-livepeer-bridge/frontend/portal/`
  - `openai-livepeer-bridge/frontend/admin/`
- built into the runtime image in:
  - `openai-livepeer-bridge/Dockerfile`
- mounted in the server at:
  - `/portal/` via `registerPortalStatic(...)`
  - `/admin/console/` via `registerAdminConsoleStatic(...)`
- registered from:
  - `openai-livepeer-bridge/packages/livepeer-openai-gateway/src/main.ts`

The old portal included real pages/services for:

- dashboard
- billing / top-up
- API keys
- usage
- settings
- login

The old admin included real pages/services for:

- login
- health
- nodes and node detail
- customers and customer detail
- top-ups
- reservations
- audit
- rate-card management across capabilities
- config view

### 2.2 Rewrite now

The rewrite has:

- shared primitives and global styles:
  - `customer-portal/frontend/shared/`
- admin scaffold:
  - `customer-portal/frontend/admin/src/index.ts`
- portal scaffold:
  - `customer-portal/frontend/portal/src/index.ts`

But those scaffolds are only starter routes with placeholder cards, not functional
product UIs. They also do not currently have:

- Vite build wiring
- static mounts in `openai-gateway`
- page-specific services
- route integration to real `openai-gateway` admin/customer APIs

So there is **not** an existing hidden admin/customer UI that can simply be toggled on.

## 3. Goal

Restore old-bridge-class deployment shape for the rewrite `openai-gateway`:

- customer portal SPA mounted in the gateway runtime
- operator admin SPA mounted in the gateway runtime
- static assets built into the gateway image
- UI routes backed by real `openai-gateway` JSON APIs
- a customer playground for trying gateway-backed OpenAI requests from the browser
- design aligned with the current shared Livepeer UI system

## 4. Target runtime shape

One `openai-gateway` service should expose:

- `POST /v1/*` — OpenAI-compatible API
- `GET /healthz`
- `GET /portal/*` — customer SPA static assets
- `GET /admin/console/*` — operator SPA static assets
- matching JSON APIs for those UIs

This should mirror the old bridge deployment model rather than introducing a
separate frontend-only service.

## 5. Scope

### In scope

- Build real `openai-gateway` portal and admin frontends in this monorepo.
- Mount static UI assets into `openai-gateway`.
- Update `openai-gateway` Dockerfile to include built frontend assets.
- Add compose/docs for the UI-enabled deployment.
- Reuse the shared `customer-portal/frontend/shared` primitives and current design system.
- Port or recreate the old openai-specific UI features needed for an actually usable
  admin/customer surface.
- Bring over the **playground concept** from `livepeer-cloud-openai-ui` as product scope,
  without copying its code directly.

### Not in scope

- Rebuilding the old bridge visual language exactly.
- Reintroducing old architecture choices that conflict with the rewrite’s manifest-driven routing.
- Creating a separate standalone frontend deployment unless later forced by CDN or security needs.

## 6. Product surfaces to ship

### 6.1 Customer portal v1

Minimum usable parity:

- signup
- API-key-backed login
- account overview / balance
- API keys
- billing / top-up
- usage summary
- playground for the first OpenAI API set against the user’s own API key:
  - chat completions
  - embeddings
  - image generation
  - audio speech
  - audio transcription

Nice-to-have near-parity:

- settings
- request history / basic consumption drilldown
- later playground extensions for embeddings / images / audio

Current landing shape:

- account, API keys, billing, and playground are live
- usage history is now backed by the reservation ledger:
  - reserved vs committed vs refunded values
  - capability / model when the request path reports them
  - work id, created time, resolved time, and state
  - single-request drilldown from the portal UI

### 6.2 Operator admin v1

Minimum usable parity:

- admin login
- health
- customer search/list
- customer create
- customer detail
- balance adjustment
- manual refund
- suspend / unsuspend
- top-up history
- audit feed

OpenAI-specific v1.5:

- rate-card management
- route / resolver visibility
- resolved offering / candidate inspection

Current landing shape:

- JSON snapshot editor for rate-card management
- resolver candidate table sourced from the live route selector pool
- reservation ledger view for:
  - all recent customer requests
  - selected-customer request drilldown
  - single reservation detail fetch by id

### 6.3 Customer playground v1

The portal should include a first-class playground route. The reference idea exists in
the sibling repo:

- `livepeer-cloud-openai-ui/portal/components/portal-playground.js`

This is idea-level inspiration only; do **not** copy the code directly.

Minimum v1 playground behavior:

- browser UI inside the customer portal
- uses the customer’s issued API key
- targets the same deployed `openai-gateway` host
- supports the first requested API set:
  - chat completions
  - embeddings
  - image generation
  - audio
- shows:
  - request payload editor
  - response panel
  - loading / error states
  - request id if returned
- makes it obvious which model/offering is being requested

Nice-to-have v1.5:

- presets for common prompts
- streaming output mode
- capability tabs for the full OpenAI suite after the initial API set is stable

### 6.4 Explicitly optional for first landing

- deep node detail parity
- full route-debug inspector
- advanced pricing editors for every capability in the first cut
- full email/password customer auth flow

## 6.5 Future incremental product-parity backlog

These items are intentionally deferred so the current gateway/customer/admin
surface can be deployed and exercised first. They should be treated as
incremental follow-on work driven by real operator and customer feedback.

### Customer-side future work

- richer dashboard summaries:
  - spend to date
  - usage by capability/model
  - current-period summaries
- filtered and exportable usage history
- billing reports and downloadable statements
- broader account/settings management

### Operator-side future work

- richer gateway health and operator dashboarding
- request failure inspection
- retry/fallback visibility
- deeper resolver/orchestrator diagnostics
- richer pricing editor beyond raw JSON snapshot replacement

### Playground future work

- broader OpenAI API coverage beyond the first landing set
- streaming UX improvements
- request presets/examples
- richer response inspectors per API family

### Auth/product hardening future work

- full customer login/session model
- password reset / account recovery
- broader customer identity lifecycle management

## 7. Recommended implementation strategy

### Strategy choice

Do **not** copy the old bridge frontend wholesale.

Instead:

1. reuse the rewrite’s shared design-system + primitives
2. reuse old bridge page structure and service logic as reference
3. implement new `openai-gateway`-specific frontends against the rewrite runtime

Reason:

- the shared design system is already more aligned with the rewrite direction
- the old bridge UI was functional, but from a previous package/layout
- the rewrite should not fork back into two frontend systems

## 8. Proposed component layout

Add real product frontends under `openai-gateway/`, mirroring the successful
pattern already used by `video-gateway`:

```text
openai-gateway/
  src/
    frontend/
      admin/
      portal/
```

Each frontend should:

- import `@livepeer-rewrite/customer-portal-shared`
- set `livepeerUiMode`
- own openai-specific routes and service adapters
- build to `dist/`

This is preferred over trying to mutate the generic `customer-portal/frontend/*`
scaffolds directly into a product app, because those scaffolds are intended as
templates, not as product homes.

## 9. Backend/runtime work required

The frontend wiring is only half the story. `openai-gateway` must also gain:

- static registration for:
  - `/portal/`
  - `/admin/console/`
- backend routes consumed by those pages
- any missing account/admin endpoints that the old bridge had and the rewrite still lacks

Likely backend slices:

- account/profile summary
- API keys
- top-up / billing UX endpoints
- usage queries
- admin customer search/detail
- refund / suspend / unsuspend
- rate-card CRUD if included in v1

## 10. Migration phases

### Phase 1 — runtime/static mounting

- Add frontend build outputs to `openai-gateway`.
- Mount:
  - `/portal/`
  - `/admin/console/`
- Update Dockerfile and compose.

Exit criteria:

- gateway image contains built frontend assets
- static routes serve HTML instead of 404

### Phase 2 — customer portal v1

- Create `openai-gateway/src/frontend/portal/`
- Implement:
  - login
  - account
  - API keys
  - billing/top-up
  - usage summary
  - playground for:
    - chat completions
    - embeddings
    - image generation
    - audio
- Wire to real backend routes

Exit criteria:

- customer can log in and manage account/key/billing basics from the browser
- customer can send real requests from the portal playground for the initial API set

### Phase 2.5 — playground expansion

- extend the playground from the initial API set to the rest of the OpenAI surface
- keep one shared playground shell with per-capability panels, not separate mini-apps

Exit criteria:

- the portal exposes the full intended OpenAI API suite from one coherent playground surface

### Phase 3 — admin UI v1

- Create `openai-gateway/src/frontend/admin/`
- Implement:
  - admin login
  - customer list/detail
  - top-ups
  - audit
  - refund
  - suspend / unsuspend
  - health view

Exit criteria:

- operator can manage customers and inspect basic gateway state from the browser

### Phase 4 — openai-specific operator features

- Port or recreate:
  - rate-card views/editing
  - route/resolver inspection
  - offering visibility

Exit criteria:

- openai-gateway has the product-specific operational surface the old bridge had

### Phase 5 — docs and deployment examples

- Document the UI routes in `openai-gateway/README.md`
- Add compose/example env for the UI-enabled deployment
- Add scenario docs if needed under `infra/scenarios/`

Exit criteria:

- operators know exactly what URLs exist and how to deploy them

## 11. Immediate first slice

The first implementation slice should be:

1. create `openai-gateway/src/frontend/portal/`
2. create `openai-gateway/src/frontend/admin/`
3. add minimal Vite builds for both
4. mount them into the runtime
5. ship skeleton-but-real pages backed by actual data for:
   - portal account
   - portal API keys
   - portal playground shell
   - admin customer list
   - admin customer detail

Why this slice first:

- it proves the deployment model
- it turns “no UI exists” into “UI exists and is reachable”
- it unlocks iterative feature parity without further runtime redesign

## 12. Risks

- The old bridge had page/service logic that may depend on routes the rewrite has not yet reintroduced.
- Recreating the full admin rate-card UX may require backend feature fill, not only frontend porting.
- Mounting static assets without solid auth/session behavior could expose unfinished pages publicly; route protection needs to be decided as part of the runtime integration.

## 13. Acceptance criteria

This plan is complete when:

- `openai-gateway` serves:
  - `/portal/`
  - `/admin/console/`
  - `/v1/*`
from one runtime
- customer portal is usable for account/key/billing basics
- admin UI is usable for customer lookup and operator actions
- deployment docs and compose examples show the UI-enabled shape
- the UI uses the rewrite’s shared Livepeer design system rather than an ad hoc copy of the old bridge styling

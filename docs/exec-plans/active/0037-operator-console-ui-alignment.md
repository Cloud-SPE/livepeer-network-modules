---
plan: 0037
title: Align secure-orch-console and orch-coordinator operator UX with a shared shell and design language
status: active
phase: implementation
opened: 2026-05-24
owner: harness
related:
  - "completed plan 0018 — orch-coordinator design"
  - "completed plan 0019 — secure-orch trust spine design"
  - "completed plan 0023 — strict frontend DOM and CSS invariants"
---

# Plan 0037 — align secure-orch-console and orch-coordinator operator UX with a shared shell and design language

## 0. Why this plan exists

The two operator-facing UIs already solve adjacent workflows, but they do not yet read
as one product family. Both are server-rendered and functional, yet they still expose
their internals as plain sections and forms rather than an intentional operator shell.

The target is not a framework rewrite. The target is a shared operator UX contract:

- the same navigation shell
- the same visual tokens and component primitives
- the same feedback states
- task-oriented page hierarchy for review, publish, and audit work

## 1. Constraints

- Keep both UIs in light DOM only.
- Keep styling in checked-in CSS files only.
- Preserve the existing Go `html/template` + embedded asset architecture.
- Use JavaScript only where it materially improves UX without introducing a frontend
  build step.
- Do not collapse the distinct security postures or operator workflows of the two
  components.

## 2. First-pass implementation scope

This slice ships the shared shell and styling baseline, not the full information
architecture rewrite.

### 2.1 Shared shell

- Add the same topbar + sidebar + content shell to both UIs.
- Support a mobile drawer sidebar via a tiny embedded JS helper.
- Expose consistent active navigation treatment across pages.

### 2.2 Shared design primitives

- Introduce aligned CSS tokens for:
  - background and surface colors
  - accent, info, warning, and danger states
  - spacing, radius, borders, and typography
- Restyle:
  - cards
  - buttons
  - badges and status pills
  - forms
  - tables
  - code blocks
  - alerts and flash messages

### 2.3 First-pass page hierarchy cleanup

- Reframe existing page headings and explanatory copy so the workflow reads as:
  - `secure-orch-console`: inspect protocol state, perform explicit protocol actions,
    review manifest diff, confirm signing, audit operator activity
  - `orch-coordinator`: inspect roster health, review candidate drift, upload the
    signed manifest, audit publication history
- Preserve existing routes and handler behavior.

## 3. Follow-on work intentionally left out of this slice

- New overview/dashboard routes
- richer inline filtering and sorting
- copy-to-clipboard helpers
- diff row expansion controls
- audit search
- cross-component shared asset package

Those can land incrementally after the shell and visual system are stable.

## 4. Success criteria

This plan is complete when:

- both UIs share the same operator shell structure
- both UIs use the same visual token vocabulary and component primitives
- navigation, forms, status messaging, and tables feel obviously related
- the changes remain compliant with repo-wide frontend DOM and CSS invariants

## 5. Extension — pool-controller admin console

The `pool-controller` admin UI is brought into the same operator-console family
as a follow-on slice. It previously shipped as a single server-built page
(inline `<style>` + one client script) under `internal/ui/adminpage`.

Scope of this extension:

- Adopt the shared design system: `internal/ui/web/assets/style.css` is the
  same token-based stylesheet used by `orch-coordinator`, with a small
  `pool.css` companion that maps the pool console's existing component classes
  (cards, pills, preview checks, control-plane forms) onto the shared tokens.
- Adopt the shared shell + multi-page navigation: a `layout.html` shell
  (topbar + sidebar nav with active states + content + footer + theme toggle +
  mobile drawer) and one page per task, served via the same
  `html/template` + `go:embed` asset architecture (`internal/ui/web`).
  The page set at the time of writing was Overview, Offers, Join requests,
  Members & backends, Assignments, Broker runtime, Audit; the shell contract is
  what this plan owns, not that list. Broker runtime went with plan 0043 (the
  controller pushes offers instead of rendering a config), and Join requests,
  Members & backends, and Assignments went with plan 0044 §5 phase A, which
  deleted the legacy member model outright. The current set is Overview, Pool,
  Offers, Audit; plan 0044 phase H rebuilds the console around members, hosts
  and GPUs, templates, exceptions, settlement, and payouts.

Auth — same login model as the trust-spine consoles:

- `pool-controller` adopts the same **session login** flow: `GET/POST
  /admin/login` takes an **admin token + actor**, validates the token, enforces
  a single active operator session, and issues a `pool_controller_session`
  cookie; `POST /admin/logout` clears it. The shell shows the actor chip and a
  logout control, matching `secure-orch-console` and `orch-coordinator`.
- The operator UI pages (`/admin`, `/admin/offers`, …) require a live session
  (redirect to `/admin/login` otherwise). When no admin token is configured,
  auth is disabled and the UI stays open, matching prior open-mode behavior.

Deliberate differences, preserved on purpose:

- The existing client-side `/admin/v1/*` fetch logic is retained (made
  page-aware) rather than re-implemented as server-rendered page models. To
  make that work under the session, the `/admin/v1/*` auth wrapper accepts
  **either** a valid session cookie (used by the browser) **or** the admin
  bearer token (used by scripts/automation), so existing token-based callers
  keep working.

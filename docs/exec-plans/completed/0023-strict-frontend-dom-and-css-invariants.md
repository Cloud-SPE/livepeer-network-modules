# Plan 0023 — strict frontend DOM and CSS invariants

**Status:** completed  
**Opened:** 2026-05-09  
**Completed:** 2026-05-09  
**Owner:** harness  
**Related:** `customer-portal/`, `openai-gateway/`, `video-gateway/`, `vtuber-gateway/`, [`../../design-docs/frontend-dom-and-css-invariants.md`](../../design-docs/frontend-dom-and-css-invariants.md), [`../../design-docs/ui-design-system.md`](../../design-docs/ui-design-system.md), [`../../references/modern-css-2026.md`](../../references/modern-css-2026.md)

## Closeout

This plan is complete.

The repo now enforces the frontend DOM/CSS contract in code and docs:

- light DOM only across current frontend packages
- no inline `style=`
- no `static styles = css`
- no runtime or test `.shadowRoot` dependence
- styling sourced from checked-in `.css` files
- semantic first-pass enforcement for nav landmarks, clickable non-interactive
  elements, fake button roles, and strong-label metadata rows

The invariant checker baseline is zero:

- `scripts/frontend-invariants-allowlist.json` = `{}`

and `pnpm check:frontend-invariants` now acts as the repo gate for these rules.

Future semantic lint expansions can land as normal hardening work, but they are no
longer required to close the original migration plan.

## 1. Why this plan exists

The repo now has multiple real browser UIs, but their implementation model violates the
desired frontend rule in several ways:

- shared and product UIs use `LitElement` + shadow DOM
- styles are embedded in TS via `static styles = css\`\``
- some screens still use extensive inline `style=` attributes
- semantic HTML is inconsistent across navigation, headings, and metadata views

This is not an isolated `openai-gateway` issue. It is a repo-wide frontend architecture
problem.

The target rule is strict and applies to all current and future UIs in this repo:

- light DOM only
- semantic HTML only
- no inline CSS
- styling only from checked-in CSS files

## 2. What inspection showed

### 2.1 Shared UI layer is non-compliant by design

The main shared frontend package is built around `LitElement` classes with embedded
style blocks. That means the repo's primary UI foundation currently violates the target
rule even before product-specific pages are considered.

Representative files:

- `customer-portal/frontend/shared/src/components/portal-layout.ts`
- `customer-portal/frontend/shared/src/components/portal-card.ts`
- `customer-portal/frontend/shared/src/components/portal-button.ts`
- `customer-portal/frontend/shared/src/components/portal-data-table.ts`

### 2.2 Product UIs add additional direct violations

OpenAI admin and portal screens currently contain many inline style attributes and
non-semantic wrappers.

Representative files:

- `openai-gateway/src/frontend/admin/main.ts`
- `openai-gateway/src/frontend/portal/main.ts`

Video and vtuber UIs are less style-heavy in their page logic, but they still inherit
shadow-DOM and CSS-in-TS architecture from the current model.

Representative files:

- `video-gateway/src/frontend/web-ui/components/admin-app.ts`
- `video-gateway/src/frontend/portal/src/components/portal-app.ts`
- `vtuber-gateway/src/frontend/admin/src/components/admin-app.ts`
- `vtuber-gateway/src/frontend/portal/src/components/portal-vtuber-app.ts`

### 2.3 Tests encode the old architecture

Multiple frontend tests query `.shadowRoot`, which means test coverage itself currently
assumes a non-compliant rendering model.

Representative files:

- `customer-portal/frontend/shared/test/widgets.test.ts`
- `video-gateway/src/frontend/test/admin-app.test.ts`
- `video-gateway/src/frontend/portal/test/portal-app.test.ts`
- `vtuber-gateway/src/frontend/admin/test/admin-app.test.ts`
- `vtuber-gateway/src/frontend/portal/test/portal-app.test.ts`

## 3. Goal

Bring every browser UI in the repo into compliance with the frontend DOM and CSS
invariants:

- all frontend rendering in the light DOM
- semantic HTML structure across admin and product surfaces
- all presentation styles sourced from `.css` files
- no shadow-DOM-specific frontend runtime or test behavior
- repo-wide CI checks that prevent regression

## 4. Non-goals

- changing the visual identity defined in `ui-design-system.md`
- replacing working business behavior while migrating structure
- forcing one specific frontend micro-framework, as long as the emitted result obeys
  the invariant contract

## 5. Locked decisions

### 5.1 The invariant is repo-wide

No component gets its own exception model. Product-specific styling can vary, but DOM
and CSS implementation rules do not vary by package.

### 5.2 Shared layer migrates first

`customer-portal/frontend/shared` moves before downstream product UIs. The repo should
not duplicate product-local fixes for primitives that belong in the shared layer.

### 5.3 Enforcement lands before full migration

CI/lint checks must be introduced while the migration is in progress, using temporary
allowlists where needed. The repo cannot wait until the end to start preventing new
violations.

### 5.4 No new shadow-DOM frontend code

While this plan is active, new frontend work may not introduce:

- `LitElement`-style shadow-DOM UI components
- `static styles = css`
- new inline style attributes
- new `.shadowRoot` test dependencies

## 6. Execution phases

### Phase 0 — policy and documentation

- add the cross-cutting design doc
- link it from design-doc indices and component guidance
- update component `AGENTS.md` files so review-time guidance matches the repo rule

Exit criteria:

- the rule exists as a checked-in cross-cutting document
- all frontend-bearing components point contributors at it

### Phase 1 — enforcement scaffolding

- add repo checks that flag:
  - `style=`
  - `static styles = css`
  - `.shadowRoot`
  - `createRenderRoot`
- start with migration allowlists so existing debt is visible but bounded
- make checks fail on any new violation outside the allowlist

Exit criteria:

- CI blocks newly introduced violations
- existing debt inventory is explicit and versioned

### Phase 2 — shared frontend foundation migration

- replace shadow-DOM shared widgets with light-DOM equivalents
- move all shared component styling into `.css` files
- convert generic wrapper markup to semantic primitives where appropriate
- preserve token usage and design-system styling direction

Primary target area:

- `customer-portal/frontend/shared/src/components/*`

Exit criteria:

- shared widgets render into light DOM
- no shared component uses `static styles = css`
- shared package consumers can style via imported CSS files

### Phase 3 — OpenAI UI migration

- refactor:
  - `openai-gateway/src/frontend/admin/main.ts`
  - `openai-gateway/src/frontend/portal/main.ts`
- remove inline styles and replace with CSS classes
- replace non-semantic nav/detail markup with semantic structures
- keep operator and customer behavior unchanged while migrating structure

Exit criteria:

- zero inline style attributes in OpenAI frontend source
- semantic nav, form, header, and metadata structures in OpenAI surfaces

### Phase 4 — video gateway migration

- convert video admin and portal UIs onto the compliant shared foundation
- remove any remaining embedded style and shadow-DOM usage

Primary target areas:

- `video-gateway/src/frontend/web-ui/*`
- `video-gateway/src/frontend/portal/*`

Exit criteria:

- video admin and portal comply with the invariant doc

### Phase 5 — vtuber gateway migration

- convert vtuber admin and portal UIs onto the compliant shared foundation
- remove any remaining embedded style and shadow-DOM usage

Primary target areas:

- `vtuber-gateway/src/frontend/admin/*`
- `vtuber-gateway/src/frontend/portal/*`

Exit criteria:

- vtuber admin and portal comply with the invariant doc

### Phase 6 — test migration and strict gate

- rewrite frontend tests to query light DOM directly
- delete migration allowlists
- turn the enforcement checks into strict zero-tolerance gates

Exit criteria:

- zero `.shadowRoot` usage in frontend tests
- allowlists deleted
- CI fails on any regression

## 7. Work breakdown by package

### 7.1 customer-portal

Owns the shared primitives and therefore the migration critical path.

Expected work:

- replace Lit-based components with light-DOM equivalents
- split component styles into CSS modules/files
- expose stable class/data hooks for downstream product customization
- update package README and tests

### 7.2 openai-gateway

Owns the most direct inline-style debt and the first product-specific validation path.

Expected work:

- admin surface semantic/layout cleanup
- portal semantic/layout cleanup
- rate-card editor class-based styling conversion
- login/session/feedback UI structure cleanup

### 7.3 video-gateway

Expected work:

- remove remaining Lit/shadow-DOM admin and portal roots
- migrate to compliant shared primitives
- convert account/detail semantics where needed

### 7.4 vtuber-gateway

Expected work:

- remove remaining Lit/shadow-DOM admin and portal roots
- migrate to compliant shared primitives
- convert account/session/detail semantics where needed

## 8. Acceptance criteria

This plan is complete when all of the following are true:

- no frontend source file in the repo contains `style=`
- no frontend source file in the repo contains `static styles = css`
- no frontend runtime code in the repo depends on shadow DOM
- no frontend tests in the repo query `.shadowRoot`
- styling for frontend packages lives in checked-in `.css` files
- semantic landmarks, headings, forms, tables, and metadata structures are used across
  all current UIs
- CI enforces the invariant with no temporary allowlists

## 9. Risks

### 9.1 Shared primitive churn

The shared layer is consumed by multiple apps. Poorly staged refactors could break
multiple products at once.

Mitigation:

- keep migration phases small
- land primitive-by-primitive compatibility wrappers where necessary
- run all frontend package builds/tests on every shared-layer change

### 9.2 Styling regressions during CSS extraction

Moving from embedded styles to CSS files can change cascade behavior.

Mitigation:

- migrate with explicit class contracts
- preserve token usage
- compare rendered states package-by-package before removing old code

### 9.3 Semantics regressions hidden by visual parity

A UI can look correct while still using poor markup.

Mitigation:

- include semantic review as part of acceptance
- add targeted HTML linting where practical

## 10. First landing tasks

1. Add the invariant doc and link it from design docs.
2. Update `AGENTS.md` in:
   - `customer-portal/`
   - `openai-gateway/`
   - `video-gateway/`
   - `vtuber-gateway/`
3. Add an initial debt inventory + enforcement script with allowlists.
4. Open the first implementation PR against `customer-portal/frontend/shared`.

# Frontend DOM and CSS invariants

**Status:** accepted  
**Last updated:** 2026-05-09  
**Related:** [`./ui-design-system.md`](./ui-design-system.md), [`../references/modern-css-2026.md`](../references/modern-css-2026.md), [`../exec-plans/completed/0023-strict-frontend-dom-and-css-invariants.md`](../exec-plans/completed/0023-strict-frontend-dom-and-css-invariants.md)

## Purpose

This repo now ships multiple browser UIs:

- `customer-portal/frontend/shared`
- `openai-gateway/src/frontend/*`
- `video-gateway/src/frontend/*`
- `vtuber-gateway/src/frontend/*`

Those surfaces must obey the same frontend authoring contract. This document defines
that contract as a cross-cutting invariant for the entire repo.

The visual language still comes from [`ui-design-system.md`](./ui-design-system.md).
This document defines the DOM, HTML, and CSS implementation rules that every UI must
follow.

## Hard rules

### 1. Light DOM only

All browser UI in this repo renders into the light DOM.

Disallowed patterns:

- shadow DOM component trees
- `LitElement` components that render into a shadow root
- `createRenderRoot()` overrides that preserve shadow DOM
- tests that reach into `.shadowRoot`

Allowed patterns:

- semantic HTML emitted directly into the document light DOM
- light-DOM custom elements that explicitly render into `this`
- framework-free DOM composition helpers

### 2. Semantic HTML only

HTML structure must express document meaning, not only layout.

Required expectations:

- navigation uses `<nav>`
- page-level headings use real heading elements
- grouped content uses appropriate sectioning elements
- labeled metadata uses semantic structures such as `<dl>`, `<dt>`, `<dd>`
- interactive controls use native elements before inventing abstractions
- lists of homogeneous items use `<ul>` / `<ol>`
- tabular data uses `<table>` with proper header cells

Disallowed patterns:

- `div`/`span` trees standing in for landmarks or metadata
- fake buttons or links implemented on non-interactive elements
- heading text rendered only as styled `div`s

Enforced first-pass semantic checks:

- no `<div slot="nav">`; navigation slots must render a real `<nav>`
- no clickable `<div>` or `<span>` via `@click=...`
- no `role="button"` on non-button elements in frontend source
- no metadata rows written as `<strong>Label:</strong> value`; use `<dl>`, `<dt>`, `<dd>`

### 3. No inline CSS

No frontend source in this repo may use inline presentation styles.

Disallowed patterns:

- `style="..."`
- runtime `.style.foo = ...` presentation logic except for narrowly-scoped geometry
  writes required by browser APIs
- CSS-in-JS template blocks used as the primary styling system
- per-component `static styles = css\`\``

Allowed patterns:

- class toggles
- `data-*` attributes that drive CSS state selectors
- CSS custom properties whose definitions live in checked-in CSS files

### 4. Styling lives in CSS files

All styling is authored in checked-in `.css` files.

Required expectations:

- shared tokens, resets, utilities, and component styles live in CSS files
- app-local surfaces import app-local CSS files rather than embedding CSS in TS
- CSS classes and `data-*` attributes are the main styling API

Disallowed patterns:

- style template literals embedded in TS component classes
- ad hoc per-screen style blocks inside render functions

## Modern CSS constraint

This repo follows [`../references/modern-css-2026.md`](../references/modern-css-2026.md)
for feature choice.

That reference says how to write modern CSS. This document says where CSS must live and
how DOM must be structured.

## Enforcement targets

The repo-wide steady state is:

- zero `style=` attributes in frontend source
- zero `static styles = css` in frontend source
- zero shadow DOM frontend components
- zero `.shadowRoot` usage in frontend tests
- semantic landmarks and metadata patterns across all UIs

Temporary migration allowlists were permitted only during
[`0023-strict-frontend-dom-and-css-invariants.md`](../exec-plans/completed/0023-strict-frontend-dom-and-css-invariants.md).
That migration is now complete and the steady-state bar applies repo-wide.

## Migration order

Because multiple product UIs consume the shared portal/admin package, migration order is
fixed:

1. `customer-portal/frontend/shared`
2. `openai-gateway/src/frontend/*`
3. `video-gateway/src/frontend/*`
4. `vtuber-gateway/src/frontend/*`
5. test suites and CI gates

## Review rule

Any frontend-facing PR must be reviewed against this document.

If a change introduces:

- shadow DOM
- inline styles
- non-semantic layout markup where semantic HTML exists
- new CSS-in-JS patterns

the change is incorrect unless it is part of the active migration plan and explicitly
removes more debt than it adds.

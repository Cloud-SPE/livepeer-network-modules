# customer-portal-shared

Shared frontend primitives for Livepeer rewrite UIs.

## Purpose

This package is the shared design-system and browser utility layer for:

- `customer-portal`
- `video-gateway`
- `vtuber-gateway`
- future browser UIs in `openai-gateway`, `secure-orch-console`, and operator tools

It provides:

- global Livepeer-branded tokens and page chrome
- shared UI primitives
- browser-side API helpers
- shared auth/signup/login components for portal flows

## UI modes

The global style layer supports two body modes:

- `network-console`
- `product-app`

Set one at app startup:

```ts
document.body.dataset.livepeerUiMode = 'network-console';
```

or:

```ts
document.body.dataset.livepeerUiMode = 'product-app';
```

## Shared primitives

Current UI primitives:

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

## Usage rules

- Use shared tokens and primitives before adding app-local styling.
- Prefer `portal-data-table` for list-heavy screens.
- Prefer `portal-action-row` for inline row actions.
- Prefer `portal-detail-section` for record/detail panels inside cards.
- Use `portal-status-pill` for machine state, not plain colored text.
- Use `portal-metric-tile` for small summary metrics at page or hero level.

If a needed pattern repeats across apps, promote it into this package instead of
copying markup and CSS into each gateway.

## Reference

- [UI design system](/home/mazup/git-repos/livepeer-cloud-spe/livepeer-network-rewrite/docs/design-docs/ui-design-system.md)

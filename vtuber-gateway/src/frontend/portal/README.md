# vtuber-gateway portal SPA

Vtuber-specific portal pages per plan 0013-vtuber OQ3 lock — these
pages live here (NOT in `customer-portal/frontend/portal/`) because
product-specific pages don't belong in the shared shell.

The shared `customer-portal/frontend/shared/` library provides common
widgets (auth forms, API-key UI, balance display, Stripe checkout,
layout, design tokens); this portal composes those primitives + adds
vtuber-specific routes:

- session list (active + historical sessions, per-second metering UI)
- persona authoring (system-prompt + tone preset)
- scene history (recent VRMs, target broadcasts)

## Layout

```
src/
  components/
    portal-vtuber-sessions.ts  ← customer "my sessions" page
    portal-vtuber-persona.ts   ← persona authoring
    portal-vtuber-history.ts   ← scene history
  index.html
  main.ts
```

## Status

Scaffold only — Vite build + page implementation lands in a follow-up
commit on top of plan 0013-vtuber phase 4.

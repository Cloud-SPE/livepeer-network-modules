# `@livepeer-network-modules/daydream-portal`

Operator-run SaaS portal sitting in front of `daydream-gateway/`. The
gateway itself is deliberately not-a-SaaS (no auth, no DB, no billing —
broadcaster mode). This package layers the customer-facing surface on
top: waitlist-gated signup, single API key per user, simple admin
approval queue, per-session usage tracking, and a Lit-based playground
SPA that opens sessions through the gateway.

## Status

Workstream branch: `feat/daydream-portal`. Ported from
`daydream-live-pipelines/apps/streamdiffusion` + `apps/api` with the
following intentional cuts:

| Removed | Replacement |
|---|---|
| Clerk | Waitlist → admin approval → single API key (blueclaw-style) |
| `@daydreamlive/react` + Livepeer WHIP | `daydream-gateway` `/v1/sessions` → `scope_url` |
| Stripe / credits / wallet | Plain `usage_events` table; no quota |
| Content discovery (`/explore`, `/discover`, gallery, workflows) | dropped |
| Plugins, projects, articles, follows, bookmarks, comments | dropped |
| PostHog / Mixpanel / Kafka / Mux / Tigris / FAL / Hyvor / gtag | dropped |
| Notifications, moderation, reports, BullMQ admin | dropped |

## Architecture

```
   browser (portal SPA, Lit)
        │
        │  HTTP: /portal/* (UI token auth)
        ▼
   daydream-portal (this package)        ── Postgres ─────
        │     │                                            │
        │     └── @livepeer-network-modules/customer-portal│
        │           (auth, admin engine, ledger types)     │
        │                                                  │
        │  HTTP: /v1/sessions, /v1/orchs, /api/v1/*        │
        ▼                                                  │
   daydream-gateway (sibling)                              │
        │                                                  │
        │  payment + capability handshake                  │
        ▼                                                  │
   orchestrator + Scope (out-of-band WebRTC to browser)    │
                                                           │
   admin SPA (Lit) ──────────────────────────────────────── ┘
```

The portal backend never touches WebRTC media. The browser receives a
`scope_url` and opens its connection directly to the orchestrator's
TURN.

## Routes

### Public

| Method | Path | Purpose |
|---|---|---|
| POST | `/portal/waitlist` | Submit signup (idempotent on email) |
| GET | `/portal/waitlist/status?email=` | Check own status |
| POST | `/portal/login-by-key` | Exchange API key for short-lived UI token |
| GET | `/healthz` | Liveness |

### Customer (UI token, `Authorization: Bearer <ui_token>`)

| Method | Path | Purpose |
|---|---|---|
| POST | `/portal/sessions` | Open a Scope session via the gateway |
| POST | `/portal/sessions/:id/close` | Close a session (idempotent) |
| GET | `/portal/prompts` | List saved prompts |
| POST | `/portal/prompts` | Save a prompt |
| PATCH | `/portal/prompts/:id` | Update a prompt |
| DELETE | `/portal/prompts/:id` | Delete a prompt |
| GET | `/portal/usage/summary` | 30-day rollup |
| GET | `/portal/usage/recent` | 50 most recent sessions |

`customer-portal` also exposes `/portal/login`, `/portal/keys`, etc. on
the same instance; see its README.

### Admin (`Authorization: Bearer <admin-token>` + `X-Actor: <name>`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/admin/waitlist` | List waitlist entries (filter by status) |
| POST | `/admin/waitlist/:id/approve` | Create customer + issue key |
| POST | `/admin/waitlist/:id/reject` | Reject a waitlist entry |
| GET | `/admin/usage/summary` | Operator-wide rollup |

`customer-portal/admin/*` adds `customers`, `topups`, `reservations`,
etc. — those mount via its own helper (not wired by default here).

## Env

| Name | Required | Purpose |
|---|---|---|
| `DAYDREAM_PORTAL_LISTEN_HOST` | no (default `0.0.0.0`) | Bind host |
| `DAYDREAM_PORTAL_LISTEN_PORT` | no (default `8080`) | Bind port |
| `DAYDREAM_PORTAL_POSTGRES_URL` | yes | Postgres DSN |
| `DAYDREAM_PORTAL_AUTH_PEPPER` | yes | Pepper for API-key hashing |
| `DAYDREAM_PORTAL_ADMIN_TOKENS` | no | Comma-sep admin tokens |
| `DAYDREAM_PORTAL_GATEWAY_BASE_URL` | yes | URL of sibling daydream-gateway |
| `DAYDREAM_PORTAL_UI_TOKEN_TTL_MS` | no (default 1h) | UI token TTL |
| `DAYDREAM_PORTAL_CAPABILITY` | no (default `daydream-scope`) | Gateway capability id |

## Local dev

```bash
pnpm -F @livepeer-network-modules/daydream-portal build
docker compose -f compose.yaml up -d   # starts a local Postgres
pnpm -F @livepeer-network-modules/customer-portal exec drizzle-kit push   # base tables
pnpm -F @livepeer-network-modules/daydream-portal exec drizzle-kit push   # daydream tables
DAYDREAM_PORTAL_POSTGRES_URL=postgres://... \
DAYDREAM_PORTAL_AUTH_PEPPER=dev-pepper \
DAYDREAM_PORTAL_GATEWAY_BASE_URL=http://localhost:9100 \
pnpm -F @livepeer-network-modules/daydream-portal start
```

Portal SPA lives in `src/frontend/portal/`, admin in `src/frontend/admin/`
— see their READMEs.

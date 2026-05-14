# AGENTS.md

This is `daydream-gateway/` — a **thin** broadcaster-side gateway that
exposes the upstream Daydream Scope workload (`daydreamlive/scope:main`)
as a paid capability on the Livepeer network rewrite. It mints
`Livepeer-Payment` envelopes via `payment-daemon` (sender mode),
resolves orchestrators that advertise `daydream-scope` via
`service-registry-daemon`, opens a paid session on the chosen orch's
broker, and forwards every subsequent Scope API call through that
broker's `/_scope/<session_id>/*` reverse proxy.

Component-local agent map. Repo-root [`../AGENTS.md`](../AGENTS.md) is
the cross-cutting map. The plan is [0026-daydream-scope-capability](../docs/exec-plans/active/0026-daydream-scope-capability.md).

## Operating principles

Inherited from the repo root. Plus:

- **Not a SaaS.** This component has **no** customer-portal dependency,
  **no** DB, **no** migrations, **no** Stripe, **no** API-key auth,
  **no** admin SPA, **no** rate-card schema. The user running this
  gateway is the broadcaster (with their own Arbitrum-funded address);
  there are no customers below them.
- **Reference UI is `scope-playground-ui`** (separate repo). This
  gateway is the API; that SPA is one consumer. Third-party SPAs,
  CLIs, and automation can consume the same API directly.
- **API surface is Scope-API-compatible by design.** A consumer that
  works against a direct Scope server works against this gateway as
  long as it talks to `/api/v1/*` paths; the gateway intercepts
  `POST /api/v1/session/start` to mint payment + open the broker
  session, then transparently proxies everything else through the
  broker's `/_scope/<session_id>/*` plane.
- **Random orch selection.** No UI orch picker. The selector resolves
  candidates from the registry, randomises, retries on failure. Pin a
  specific orch via env var for dev/debug.
- **No media handling.** Browser ↔ Cloudflare TURN ↔ Scope. The
  gateway is control-plane only — it never sees a media byte. Scope's
  own TURN config (driven by `HF_TOKEN` on the Scope container at the
  orch) determines the relay; this gateway just hands the SPA the
  broker's `scope_url` from the session-open response.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Operator runbook | [`docs/operator-runbook.md`](./docs/operator-runbook.md) |
| Build / run gestures | [`Makefile`](./Makefile) (TBD) — for v0 use `pnpm` directly |
| Compose stack (gateway side) | [`compose.yaml`](./compose.yaml) |
| The broker it talks to | [`../capability-broker/`](../capability-broker/) |
| The mode spec | [`../livepeer-network-protocol/modes/session-control-external-media.md`](../livepeer-network-protocol/modes/session-control-external-media.md) |
| Reference SPA | `scope-playground-ui` (separate repo) |

## Layout

```
daydream-gateway/
  AGENTS.md           ← this file
  CLAUDE.md           ← pointer
  README.md
  Dockerfile
  compose.yaml
  package.json
  tsconfig.json
  src/
    index.ts          ← Fastify server entrypoint
    config.ts         ← env + flags
    orchSelector.ts   ← service-registry-daemon resolver client + random pick
    paymentClient.ts  ← payment-daemon (sender) client
    sessionRouter.ts  ← per-session orch mapping
    routes/
      orchs.ts        ← GET /v1/orchs
      sessions.ts     ← POST /v1/sessions, /sessions/:id/topup, /sessions/:id/close
      passthrough.ts  ← /api/v1/* and intercept of /api/v1/session/start
  docs/
    operator-runbook.md
  test/
    unit/
```

## Doing work in this component

- Docker-first per core belief #15. `docker compose -f compose.yaml up`.
- TypeScript strict; tsc is the lint gate (same as `openai-gateway`).
- Do not add `@livepeer-rewrite/customer-portal` as a dependency. If
  you find yourself needing per-customer state, the design is wrong —
  this component is broadcaster-scoped, not customer-scoped.
- Workload-specific knobs (e.g. `media.session_start_path`) come from
  the orch's published manifest `extra` block, not gateway code.
- Per-product wire surface lives in `src/routes/`; reusable Livepeer
  wire pieces come from `@tztcloud/livepeer-gateway-middleware`.

## What lives elsewhere

- The broker on the orch host — `../capability-broker/`.
- The mode spec — `../livepeer-network-protocol/modes/session-control-external-media.md`.
- Reference SPA — `scope-playground-ui` (separate repo); changes there
  are a separate PR.
- Customer billing — does not exist here; the broadcaster's own
  Arbitrum funding is the only payment source.

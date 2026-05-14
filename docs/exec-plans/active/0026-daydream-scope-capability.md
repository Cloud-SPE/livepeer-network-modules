---
plan: 0026
title: Daydream Scope capability — external-media interaction mode + thin gateway
status: active
phase: design
opened: 2026-05-14
owner: harness
related:
  - "completed plan 0012 — session-control-plus-media driver (referenced for control-WS pattern)"
  - "completed plan 0013-vtuber — vtuber-suite migration (referenced for streaming-workload patterns)"
  - "active plan 0024 — quote-free ticket-params flow"
  - "active plan 0025 — per-request ticket-params broker routing"
---

# Plan 0026 — Daydream Scope capability — external-media interaction mode + thin gateway

## 1. Problem

Daydream Scope (`daydreamlive/scope:main`) is a real-time generative-video
workload with a Scope-native HTTP + WebRTC API surface. Orchestrators on the
Livepeer network must be able to advertise and serve Scope as a paid
capability **without modifying the upstream Scope image**, and a gateway
must be able to broker paid sessions and route customers to it.

The existing streaming mode (`session-control-plus-media@v0`) does not fit:

1. It assumes the broker spawns a per-session backend subprocess. Scope is
   a long-lived multi-session process — model load is minute-scale and a
   GPU typically hosts one Scope instance.
2. It assumes the broker relays media through pion-webrtc. Scope owns its
   own WebRTC plane and signs ICE candidates against its own host — proxying
   media would require modifying Scope.

A new mode is required that brokers control + payment while leaving Scope's
media plane untouched.

## 2. Required invariants

For every Daydream Scope session:

1. The customer (browser) talks Scope's HTTP API and WebRTC media plane
   directly — through a thin gateway proxy that does not touch media bytes.
2. The capability broker intercepts session-open, validates the
   `Livepeer-Payment` envelope, meters seconds elapsed, debits via
   `payment-daemon` on cadence, and forces session close when balance
   exhausts.
3. The Scope container's HTTP API is reachable only from the broker; its
   WebRTC UDP ports are reachable only by Cloudflare TURN.
4. Scope's source is unmodified. All TURN configuration is supplied via
   the upstream-supported `HF_TOKEN` (or direct Cloudflare creds) env vars.
5. The `service-registry-daemon` and `payment-daemon` components require
   no code changes — capability and offering are opaque strings to them.

## 3. Architecture

```
                ┌──────── customer browser ────────┐
                │                                  │
HTTP/SDP        │                       WebRTC/UDP │
        ▼       ▼                                  ▼
   daydream-gateway (TS, thin)            Cloudflare TURN
        │                                          │
        │   Livepeer wire protocol                 │
        │   (POST /v1/cap, control WS)             │
        ▼                                          ▼
   ┌─ orchestrator host ───────────────────────────────────────┐
   │  capability-broker  ─── payment-daemon ── service-registry │
   │       │                                                    │
   │       │ scope-control net (broker only)                    │
   │       ▼                                                    │
   │  daydreamlive/scope:main (HF_TOKEN env)                    │
   │       │                                                    │
   │       │ egress net (Cloudflare only)                       │
   │       └──────────────► Cloudflare TURN ◄───────────────────┘
   └────────────────────────────────────────────────────────────┘
```

Three Docker networks isolate Scope's surfaces:

- `public` — broker's HTTPS listener faces the public internet.
- `scope-control` — broker ↔ Scope HTTP control plane; no other reach.
- `egress` — Scope ↔ Cloudflare TURN; outbound only.

## 4. Execution

### 4.1 New interaction mode spec

Add `livepeer-network-protocol/modes/session-control-external-media.md`.

Delta from `session-control-plus-media@v0`:

- **Session-open response** carries a Scope-API base URL in the
  capability-defined `media` block instead of a capability-defined publish
  descriptor:
  ```json
  {
    "session_id": "...",
    "control_url": "wss://broker/v1/cap/.../control",
    "media": {
      "schema": "scope-passthrough/v0",
      "scope_url": "https://broker/_scope/<session_id>/"
    },
    "expires_at": "..."
  }
  ```
- **`scope_url`** is a thin authenticated reverse proxy on the broker that
  forwards Scope's `/api/v1/*` endpoints (pipeline load, session start,
  WebRTC offer/answer, metrics, etc.) and **only** those endpoints. No
  Scope endpoints are exposed publicly except via this proxy. The proxy
  short-circuits `POST /api/v1/session/start` and `POST /api/v1/session/stop`
  to keep the broker's session lifecycle authoritative.
- **Control WS frame set is lifecycle-only.** No capability-defined
  command frames — Scope's runtime control (prompt updates, parameter
  changes) flows over the customer's WebRTC data channel directly to
  Scope, never through the broker. The control WS carries:
  - Broker → gateway: `session.started`, `session.balance.low`,
    `session.balance.refilled`, `session.usage.tick`, `session.ended`.
  - Gateway → broker: `session.topup`, `session.end`.
- **Work-unit** is `seconds-elapsed`, granularity 1, started at session
  proxy first contact.
- **Media plane** is explicitly out-of-band: browser ↔ Cloudflare TURN ↔
  Scope. The broker MUST NOT relay media. The spec calls this out so the
  conformance suite does not test media bytes.

### 4.2 Conformance fixtures

Add under `livepeer-network-protocol/conformance/fixtures/session-control-external-media/`:

- `happy-path.yaml` — open, tick for N seconds, graceful close, ledger
  reconciles.
- `balance-exhausted.yaml` — broker forces close when balance hits 0.
- `runner-disconnect.yaml` — Scope process dies mid-session; broker emits
  `session.ended` with `runner_disconnect` reason and refunds remainder.
- `proxy-passthrough.yaml` — broker's `/_scope/` proxy correctly forwards a
  non-lifecycle Scope call (e.g. `GET /api/v1/pipeline/status`).

### 4.3 Broker driver

Add `capability-broker/internal/modes/sessioncontrolexternalmedia/`. Reuses:

- `internal/livepeerheader/` — header validation middleware (unchanged).
- `internal/modes/sessioncontrolplusmedia/controlws*.go` — lift the
  control-WS lifecycle code path into a shared subpackage; the new mode
  uses the same WS machinery with a smaller frame vocabulary. Avoid
  duplicating.
- `internal/media/` — **not** reused. This mode owns no media plumbing.

New code:

- `driver.go` — implements `modes.Driver` for `session-control-external-media@v0`.
- `proxy.go` — the `/_scope/<session_id>/*` HTTP reverse proxy with the
  session-start / session-stop short-circuits and the broker's seconds-elapsed
  clock attached to first-contact.
- `proxy_test.go` — tests the short-circuit + passthrough behaviors.

### 4.4 Host-config example

Add to `capability-broker/examples/host-config.example.yaml` (commented,
matching the file's opt-in convention):

```yaml
# - id: "daydream:scope:v1"
#   offering_id: "default"
#   interaction_mode: "session-control-external-media@v0"
#   work_unit:
#     name: "seconds"
#     extractor: { type: "seconds-elapsed", granularity: 1 }
#   price:
#     amount_wei: "1500000"
#     per_units: 1
#   backend:
#     transport: "http"
#     url: "http://scope:8000"
#   extra:
#     workload: "daydream-scope"
#     turn_provider: "cloudflare"
```

### 4.5 New component: `daydream-gateway/`

Top-level TS component. Paper-thin. **Does not** depend on
`@livepeer-rewrite/customer-portal`. Has no DB, no migrations, no
admin SPA, no API-key auth, no rate-card schema.

```
daydream-gateway/
  AGENTS.md
  CLAUDE.md      ← pointer to AGENTS.md
  README.md
  Dockerfile
  package.json
  tsconfig.json
  compose.yaml
  src/
    index.ts            ← server entrypoint
    config.ts           ← env + flags (HF_TOKEN passthrough, broker selection algo)
    orchSelector.ts     ← service-registry-daemon client + random selection
    paymentClient.ts    ← payment-daemon gRPC client (sender mode)
    sessionRouter.ts    ← per-session orch mapping; routes /_scope/* to chosen orch
    routes/
      orchs.ts          ← GET /v1/orchs?capability=daydream-scope
      sessions.ts       ← POST /v1/sessions, /sessions/:id/topup, /sessions/:id/close
      passthrough.ts    ← /api/v1/* → chosen orch's broker /_scope/:session_id/*
  test/
    unit/
    integration/        ← compose-up Scope + broker + payment-daemon, exercise full flow
```

Responsibilities:

- `GET /v1/orchs` — resolve `daydream-scope` capability via
  service-registry-daemon, return current healthy orchs.
- `POST /v1/sessions` — pick orch (random), mint payment envelope via
  payment-daemon, send session-open to broker, store
  `session_id → orch` mapping, return `{ session_id, scope_url }` plus
  the Scope-API-equivalent URL the SPA points at.
- `/api/v1/*` — Scope-compatible reverse proxy. Routes to the chosen
  orch's broker `/_scope/:session_id/*`. The SPA's existing API calls
  flow through unchanged.
- `POST /v1/sessions/:id/topup`, `/v1/sessions/:id/close` — payment
  refill and graceful close, forwarded to broker's control WS.

### 4.6 Compose stacks

Add `capability-broker/compose/daydream-scope.yaml` — orch-side stack
example, pulling `daydreamlive/scope:main` straight from upstream:

```yaml
# Pseudo-skeleton; actual file is the deliverable.
services:
  scope:
    image: daydreamlive/scope:main
    environment:
      - HF_TOKEN=${HF_TOKEN}     # required for Cloudflare TURN
    networks: [scope-control, egress]
    deploy: { resources: { reservations: { devices: [{ driver: nvidia, count: 1 }] } } }
  capability-broker:
    image: livepeer-rewrite/capability-broker:latest
    networks: [public, scope-control]
    volumes: [./host-config.yaml:/etc/broker/host-config.yaml]
  payment-daemon:
    networks: [public]
  service-registry-daemon:
    networks: [public]
networks:
  public: {}
  scope-control: { internal: true }
  egress: {}     # default-bridge for Cloudflare egress
```

Add `daydream-gateway/compose.yaml` for the gateway side.

### 4.7 Service-registry manifest

Document the manifest entry the orch operator publishes. The schema is
already capability-agnostic; this is example documentation only, not a
code change in `service-registry-daemon/`.

```yaml
capabilities:
  - name: daydream-scope
    offerings:
      - id: default
        url: https://broker.example.com
        constraints: { gpu_class: "L40S", tier: "standard" }
```

### 4.8 SPA pointer

No code change in this repo. `scope-playground-ui` (separate repo)
gets a one-line config change: backend URL points at the gateway. That
work tracks under its own PR in that repo.

### 4.9 Operator runbook

Add `daydream-gateway/docs/operator-runbook.md` covering:

- Required env: `HF_TOKEN` on the Scope container (Cloudflare TURN auth).
- Network isolation diagram (the three Docker networks).
- Cost model note: Cloudflare TURN bandwidth is operator-paid.
- Failure modes: Cloudflare TURN unreachable → sessions fail at offer
  exchange.

### 4.10 Cross-cutting docs

- Add a row in `docs/design-docs/streaming-workload-pattern.md`'s mode-mapping
  table for `session-control-external-media`.
- Index the new mode in `livepeer-network-protocol/modes/README.md`.
- Add a row in root `AGENTS.md` "Where to look" pointing at this plan
  while it is active.

## 5. Files to change

### `livepeer-network-protocol/`

- `modes/session-control-external-media.md` (new)
- `modes/README.md` (index)
- `conformance/fixtures/session-control-external-media/*.yaml` (new)
- `conformance/test-broker-config.yaml` (add capability fixtures)

### `capability-broker/`

- `internal/modes/sessioncontrolexternalmedia/` (new package)
- `internal/modes/sessioncontrolplusmedia/controlws*.go` (refactor — lift
  reusable control-WS code into a shared subpackage; no behavior change)
- `examples/host-config.example.yaml` (commented entry)
- `compose/daydream-scope.yaml` (new orch-side stack)
- `cmd/broker/main.go` (register new mode)

### `daydream-gateway/` (new top-level component)

- entire tree per §4.5

### `docs/`

- `design-docs/streaming-workload-pattern.md` (mode-mapping row)
- `AGENTS.md` (Where-to-look pointer, active-plan visibility)

## 6. Non-goals

- Modifying Scope's source. The plan succeeds with `daydreamlive/scope:main`
  unmodified.
- Customer billing surface. The gateway is paymaster-only; no API keys,
  no Stripe, no ledger, no admin SPA. Customers run their own broadcaster.
- Self-hosted TURN. Cloudflare TURN via `HF_TOKEN` is the only supported
  path in this plan. No coturn, no pion/turn embedded in the broker.
- Per-session TURN credential rotation. Scope reads TURN config at startup
  only; per-session creds are infeasible without source changes.
- Orch-picker UI. Selection is random with retry; never surfaced to the
  customer.
- High availability for Cloudflare TURN. Documented as a hard dependency;
  HA story (multi-provider, fallback) is a future plan.
- Generalized "long-lived multi-session backend" framework. This plan
  scopes to Scope; abstracting the pattern for other workloads is a future
  exec plan when a second workload needs it.
- Broker media-stack refactor (extracting `media-relay/`). Worth doing,
  out of scope here; tracked separately in tech-debt-tracker.

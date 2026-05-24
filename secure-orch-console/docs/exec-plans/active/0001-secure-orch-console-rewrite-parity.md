---
title: Plan 0001 — secure-orch console rewrite parity
status: active
opened: 2026-05-09
owner: harness
related:
  - ../../../README.md
  - ../../../DESIGN.md
  - ../../../../docs/exec-plans/completed/0019-secure-orch-trust-spine-design.md
  - /home/mazup/git-repos/livepeer-cloud-spe/livepeer-secure-orch-console/
---

# Plan 0001 — secure-orch console rewrite parity

## 1. Why this plan exists

The rewrite repo's `secure-orch-console/` currently ships only the cold-key
manifest diff/sign loop:

- upload candidate
- inspect diff
- type the signer suffix
- sign and download the envelope
- mirror `last-signed.json`
- append local audit records

That is materially smaller than the sibling
`/home/mazup/git-repos/livepeer-cloud-spe/livepeer-secure-orch-console`
operator UI, which also provides:

- admin-token auth
- actor identity
- single active session
- protocol-daemon status reads
- cold-key gated protocol actions
- ServiceRegistry and AIServiceRegistry pointer status/actions
- audit browsing/export

This plan brings the rewrite console to similar operator capability, but updated
for the rewrite architecture:

- signing stays local in `secure-orch-console`
- chain actions flow through `protocol-daemon`
- no legacy publisher-daemon coupling for signing

## 2. Scope

### In scope

- Add authenticated operator access to the Go console.
- Record operator actor identity in audit records.
- Add a protocol-daemon client over a local unix socket.
- Expose status and typed-confirm cold-key actions in the UI.
- Expose both `ServiceRegistry` and `AIServiceRegistry` pointer workflows.
- Make audit data queryable/exportable while preserving append-only operator
  provenance.

### Out of scope

- Reintroducing the legacy service-registry publisher daemon as the signing path.
- Auto-signing or bypassing the diff-confirm gesture.
- Public-network access patterns.
- Hardware-backed signers in this plan.

## 3. Target operator surface

The rewrite console should converge on these capabilities:

1. Auth/session
2. Actor identity
3. Manifest preview/diff/sign
4. Protocol status dashboard
5. Force round-init and reward
6. `set-service-uri`
7. `set-ai-service-uri`
8. Queryable audit log
9. Audit export

## 4. Proposed rewrite architecture

### 4.1 HTTP surface

Retain a single embedded Go HTTP server, but split the surface into:

- unauthenticated:
  - `GET /healthz`
  - `GET /login`
  - `POST /login`
- authenticated:
  - `POST /logout`
  - manifest upload/diff/sign routes
  - protocol status/action routes
  - audit read/export routes

### 4.2 Auth/session

- Config via `SECURE_ORCH_ADMIN_TOKENS`, comma-separated.
- Operator submits token + actor once.
- Server validates token, issues one cookie-backed session, and rejects a second
  concurrent session. Sessions expire after 12 hours absolute or 30 minutes
  idle, and expired sessions release the single-session slot automatically.
- Actor becomes part of every audited gesture.

### 4.3 Protocol integration

`secure-orch-console` talks to local `protocol-daemon` over unix socket for:

- daemon health
- round/reward status
- wallet balance
- registration state
- on-chain service URI pointers
- force-init / force-reward
- `set-service-uri` / `set-ai-service-uri`

### 4.4 Audit storage

Keep the append-only JSONL write path for durable local provenance, but add a
queryable read model. SQLite is the likely next step because:

- actor/time filters become cheap
- exports are deterministic
- the sibling UI already proved the shape is useful

The JSONL file remains the operator-visible append-only artifact.

## 5. Phases

### Phase 1 — auth/session baseline ✅ shipped

- ~~Add `SECURE_ORCH_ADMIN_TOKENS`~~ — parsed in `cmd/secure-orch-console/main.go`
- ~~Add login/logout pages~~ — `web/templates/login.html`, handlers in `web/handlers.go`
- ~~Add single-session cookie auth~~ — enforced in `web/server.go`
- ~~Require actor at login~~ — captured at login handler
- ~~Record actor on all audit writes~~ — `audit.Event.Actor` populated on every append

### Phase 2 — protocol read surface ✅ shipped

- ~~Add protocol-daemon client~~ — `internal/protocol/client.go`
- ~~Add authenticated status view~~ — wired behind `requireAuth` in `web/server.go`
- ~~Show round/reward/registration/wallet state~~
- ~~Show `ServiceRegistry` and `AIServiceRegistry` pointers~~

### Phase 3 — protocol write surface ✅ shipped

- ~~Add typed-confirm actions: force-init, force-reward, set-service-uri,
  set-ai-service-uri~~ — handlers in `web/handlers.go`
- ~~Audit all action attempts and outcomes~~

### Phase 4 — audit browse/export ⏳ partial

Shipped:

- ~~Add queryable audit store~~ — paginated cursor-based `audit.ReadPage`
  backs `GET /audit` (`web/handlers.go`); JSONL store with rotation in
  `internal/audit/audit.go`

Remaining:

- Add audit list API (JSON over HTTP, not just the rendered HTML page)
- Add export endpoints (CSV / JSONL download for operator off-box review)
- Decide whether richer query filters (by actor / kind / time range) are part
  of this phase or a follow-up

## 6. Risks / constraints

- The current Go console is server-rendered HTML, not a SPA. Avoid a large
  frontend toolchain migration unless proven necessary.
- Auth must not weaken the local-only trust posture; it complements it.
- The diff-and-sign path remains load-bearing and must stay simple.
- Protocol writes must never bypass typed confirmation.

## 7. Exit criteria

This plan is complete when the rewrite `secure-orch-console` provides operator
capabilities comparable to the sibling secure-orch UI, adapted to the rewrite's
split of responsibilities:

- local signing in `secure-orch-console`
- chain actions in `protocol-daemon`
- authenticated operator identity and auditable action history

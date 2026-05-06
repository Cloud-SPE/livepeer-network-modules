# PLANS

Current state of work in this repo, plus pointers to active plans.

## Current state

**Phases 4-chain + 9 shipping** — chain-integrated payment-daemon
(plan 0016) and secure-orch-console v0.1 (plan 0019) both landed.
Phase 4-chain wired receiver-side ECDSA validation, on-chain
TicketBroker / RoundsManager / BondingManager providers, gas-price
polling, and the redemption queue with Arbitrum-One pre-checks
behind `--chain-rpc`. Phase 9 shipped the cold-key host's
diff-and-sign console — V3 keystore signer, JCS canonicalization,
secp256k1 + EIP-191 personal-sign, structural diff against
last-signed.json keyed on `(capability_id, offering_id)`,
tap-to-sign confirm gesture (last-4-hex-chars input), audit log
with size-based rotation, and a localhost-bound web UI reached over
`ssh -L`.

Live-deployment smoke for both phases is operator-driven and
post-merge: the secure-orch host's deployment posture (sshd, LAN +
key + password is one valid example per plan 0019 §13 Q6), the
funded mainnet wallet for ticket redemption, and the operator's
preferred RPC are all chosen at deploy time and cannot run from
agent worktrees.

**Repo shape: monorepo for now.** All components live as top-level subfolders here;
extraction to standalone repos is a v2 concern. See [`README.md`](./README.md) §"Repo
shape" for the planned component list.

Code shipping today:

- `capability-broker/` — Go reference impl, 6 modes + 6 extractors registered.
  Plan 0015 added the broker-side interim-debit ticker for long-running
  sessions (ws-realtime today; `LiveCounter` interface in place for the
  rtmp / session-control followups).
- `payment-daemon/` — sender + receiver modes; gRPC over unix socket; BoltDB
  session ledger. Plan 0016 lit up real chain integration: keccak256-flatten
  ticket hashing, V3 keystore signing, on-chain TicketBroker /
  RoundsManager / BondingManager providers, eth_gasPrice polling, ECDSA
  recovery + 600-nonce ledger receiver-side, MaxFloat with 3:1 heuristic
  sender-side, redemption queue + loop with gas pre-checks. All under
  `--chain-rpc`; the dev-mode flow (no flag) keeps the daemon testable
  without any RPC.
- `gateway-adapters/` — TypeScript per-mode middleware for the HTTP family.
- `openai-gateway/` — reference OpenAI-compat gateway end-to-end (calls
  `PayerDaemon.CreatePayment` over unix socket).
- `livepeer-network-protocol/` — spec subfolder (manifest schema + 6 modes +
  6 extractors + payment proto + conformance runner with 13 fixtures).
  The `verify/` package recovers signers from manifest envelopes and is
  the cross-cutting verifier consumed by coordinator / resolver / gateway
  (landed under plan 0019).
- `secure-orch-console/` — cold-key host's diff-and-sign UX. V3 keystore
  signer, JCS canonical bytes, secp256k1 + EIP-191 personal-sign,
  structural diff against last-signed.json, tap-to-sign confirm gesture,
  audit log with size-based rotation. Localhost-bound web UI; operator
  reaches it over `ssh -L`. Manifest transport is HTTP-only via the web
  UI (no inbox / outbox spool, no USB, no filesystem watcher). v0.1
  scope locked 2026-05-06 (plan 0019).

Design-doc batch (plans 0013, 0015–0019, plus `migration-from-suite.md`)
landed 2026-05-06: ~4,140 lines of paper documenting the next implementation
layer. Plans 0015 (interim-debit, 13/13 conformance), 0016 (chain
integration), 0017 (warm-key), and 0019 (secure-orch-console v0.1) have
all since shipped; plan 0018 (orch-coordinator) is the remaining unstarted
implementation, and plan 0013 (suite-openai-gateway migration) is gated on
chain v1.0.0.

What does not exist yet:

- `orch-coordinator/` component (design landed in plan 0018; no code).
- Any change to the existing `livepeer-network-suite`.
- Live-mainnet smoke gate for the chain-integrated payment-daemon
  (plan 0016 acceptance #3) — funded mainnet wallet + user's preferred
  RPC; runs as a user-driven post-merge gate.
- Live-deployment smoke for secure-orch-console v0.1 (plan 0019) —
  operator-driven and post-merge; deployment posture (e.g. LAN-only
  sshd with key + password) is the operator's choice per plan 0019
  §13 Q6.

## Active plans

Two numbered design docs at `docs/exec-plans/active/000N-*.md`. Each
is **paper-only** — no committed code. Each surfaces 5–8 open
questions the user must answer before implementation begins.

- **Plan 0013** — `0013-suite-openai-gateway-migration-brief.md`.
  Migration brief for the suite's existing `livepeer-openai-gateway`
  (option B from plan 0009). Recommends collapsing the
  engine-vs-shell split during the migration and renaming away from
  `-core`. 5-phase migration sequence; -1,500 to -1,800 net LOC.
  Estimate 8–14 working days.
- **Plan 0018** — `0018-orch-coordinator-design.md`. Phase 3 from
  the roadmap. New `orch-coordinator/` component scrapes LAN broker
  `/registry/offerings`, builds candidate manifest (JCS-canonical,
  idempotent), hosts signed manifest at
  `/.well-known/livepeer-registry.json`, exposes capability-as-roster
  UX via embedded web UI on the LAN. v0.1 locks (2026-05-06): web UI
  primary (CLI deferred-or-never); HTTP POST upload only (no
  filesystem-drop / inotify); two on-chain registries (`ServiceRegistry`
  + `AIServiceRegistry`) consolidate to one well-known path with
  unified manifest. 7-commit cadence (commit 0 = manifest README +
  architecture-overview doc cleanup for the two-registry consolidation).

Each plan's open-question list is the gate to implementation work.

Followups still open from earlier plans:

- **Plan 0011-followup** — actual RTMP ingest + FFmpeg + HLS pipeline
  (the session-open phase landed in plan 0011; the media pipeline is
  its own workstream).
- **Plan 0012-followup** — control-plane WebSocket lifecycle +
  media-plane provisioning for `session-control-plus-media`.

Completed plans live in [`docs/exec-plans/completed/`](./docs/exec-plans/completed/) —
plans 0001–0012, 0014, 0015, 0016, 0017, and 0019 are all closed;
together they shipped the 6-mode broker, 6 extractors, gateway-adapters
TS middleware, the OpenAI-compat reference gateway, the wire-compat
sender + receiver daemons, the broker-side interim-debit cadence with
SufficientBalance runway termination on long-running sessions (plan
0015), the warm-key lifecycle (V3 keystore loader + production-mode
wiring + rotation runbook + no-secrets-in-logs lint, plan 0017),
chain-integrated payment (real keccak256-flatten ticket hashing +
on-chain TicketBroker / RoundsManager / BondingManager providers +
ECDSA recovery + nonce ledger + redemption queue with gas pre-checks,
plan 0016), and the secure-orch-console v0.1 cold-key trust spine
(V3 keystore signer, JCS canonicalization, secp256k1 + EIP-191
personal-sign, structural diff + tap-to-sign UX, audit log with
size-based rotation, localhost-bound web UI; plan 0019).

## Roadmap (rough; subject to change)

| Phase | Outcome | Component subfolder | Status |
|---|---|---|---|
| 0 | Docs-and-spec scaffold + conversation provenance | (root) | ✅ completed (plan 0001) |
| 1 | Interaction-mode specs published as a subfolder | `livepeer-network-protocol/` | ✅ completed (plan 0002) |
| 2 | Capability-broker reference implementation (Go) | `capability-broker/` | ✅ completed (plan 0003) |
| 2.5 | Conformance runner mode drivers | `livepeer-network-protocol/conformance/runner/` | ✅ completed (plan 0004) |
| 3 | Coordinator UX rework — capability-as-roster-entry | `orch-coordinator/` | 📄 design landed (plan 0018); implementation pending |
| 4 | Real `payment-daemon` integration | `payment-daemon/` | ✅ completed (plan 0005) |
| 4-followup | Wire-compat envelope + sender daemon | `payment-daemon/` | ✅ completed (plan 0014) |
| 4-chain | Chain-integrated payment-daemon (Arbitrum One) | `payment-daemon/` | ✅ completed (plan 0016) — code shipped; live-mainnet smoke is a user-driven post-merge gate |
| 4-warmkey | Warm-key lifecycle + rotation | `payment-daemon/` | ✅ completed (plan 0017) |
| 4-interim | Interim-debit cadence on long-running modes | `capability-broker/` | ✅ completed (plan 0015) |
| 5a | HTTP-family mode drivers (`http-stream`, `http-multipart`) | `capability-broker/`, `runner/` | ✅ completed (plan 0006) |
| 5b | `ws-realtime` mode driver | `capability-broker/`, `runner/` | ✅ completed (plan 0010) |
| 5c | `rtmp-ingress-hls-egress` mode driver — session-open phase | `capability-broker/`, `runner/` | ✅ completed (plan 0011) |
| 5c-followup | `rtmp-ingress-hls-egress` media pipeline (RTMP listener + FFmpeg + HLS sink) | `capability-broker/` | not started |
| 5d | `session-control-plus-media` mode driver — session-open phase | `capability-broker/`, `runner/` | ✅ completed (plan 0012) |
| 5d-followup | `session-control-plus-media` control-WS + media-plane provisioning | `capability-broker/` | not started |
| 6 | Additional extractors | `capability-broker/` | ✅ completed (plan 0007) |
| 7 | Gateway-side per-mode adapters (HTTP family) | `gateway-adapters/` | ✅ completed (plan 0008) |
| 7-followup | gateway-adapters: ws-realtime / rtmp / session-control middleware | `gateway-adapters/` | not started |
| 8 | OpenAI-compat gateway reference (option A) | `openai-gateway/` | ✅ completed (plan 0009) |
| 8-suite-migration | Suite OpenAI-gateway migration brief (option B) | (paper) | 📄 design landed (plan 0013); per-gateway plans gated on chain v1.0.0 |
| 9 | Cold-key signed manifest + secure-orch-console | `secure-orch-console/` | ✅ completed (plan 0019) — code shipped v0.1 (V3 keystore, web UI, audit rotation); live-deployment smoke is a user-driven post-merge gate per §13 Q6 |

Phases 1–5 are independently shippable; phase 6 is gated on at least one
production gateway adopting the new shape. Phase 4-chain (Arbitrum One)
gates the rewrite's v1.0.0 cut, which in turn gates phase 8-suite-migration
per `docs/design-docs/migration-from-suite.md` §3 phase 1. Components can
be extracted from this monorepo to standalone repos at any phase boundary.

## Versioning

Pre-1.0.0 until the first release is cut. **v1.0.0 = the first release of this
monorepo.** Components inside the monorepo do not have independent versions yet; when
a component is extracted to a standalone repo, its versioning becomes its own concern.
Until extraction, the monorepo's tag is the single coordinated release artifact for
everything in it.

This repo's release line is **independent of `livepeer-network-suite`**. The two share
no submodules, no pinned SHAs, and no schedule. See core belief #14.

## Tracking debt

[`docs/exec-plans/tech-debt-tracker.md`](./docs/exec-plans/tech-debt-tracker.md). Empty
at scaffold time; append as debt accumulates.

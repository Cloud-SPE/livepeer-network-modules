# PLANS

Current state of work in this repo, plus pointers to active plans.

## Current state

**Phase 4-chain shipping** — chain-integrated payment-daemon
implementation landed (plan 0016); receiver-side ECDSA validation,
on-chain TicketBroker / RoundsManager / BondingManager providers, gas-
price polling, redemption queue + loop with Arbitrum-One pre-checks
all wired behind `--chain-rpc`. Live-mainnet smoke (acceptance #3) is
a user-driven post-merge gate — requires a funded mainnet wallet and
the user's preferred RPC, and cannot run from agent worktrees.

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
- `orch-coordinator/` — Go reference impl (plan 0018). Scrapes
  capability-broker `/registry/offerings` on the operator's LAN,
  builds JCS-canonical idempotent candidate manifests, packages as
  tar.gz (manifest.json signed bytes + metadata.json operator-only
  sidecar), receives cold-key-signed manifests via HTTP POST, runs
  the five-step verify pipeline (schema / signature / identity /
  spec-version drift / publication-seq rollback), atomic-swap
  publishes at `/.well-known/livepeer-registry.json` on a separate
  locked-down public listener. Web UI on the LAN listener with
  roster (per §8), diff, audit views. BoltDB audit log; Prometheus
  surface.
- `gateway-adapters/` — TypeScript per-mode middleware for the HTTP family.
- `openai-gateway/` — reference OpenAI-compat gateway end-to-end (calls
  `PayerDaemon.CreatePayment` over unix socket).
- `livepeer-network-protocol/` — spec subfolder (manifest schema + 6 modes +
  6 extractors + payment proto + conformance runner with 13 fixtures).

Design-doc batch (plans 0013, 0015–0019, plus `migration-from-suite.md`)
landed 2026-05-06: ~4,140 lines of paper documenting the next implementation
layer. Plans 0015 (interim-debit, 13/13 conformance), 0016 (chain
integration), 0017 (warm-key), and 0018 (orch-coordinator) have since
shipped; the remaining plans (0013, 0019) still surface open questions for
the user before code can start.

What does not exist yet:

- `secure-orch-console/` is partially shipped — canonical/signing
  primitives, console binary scaffold (loopback-bound), audit log,
  keygen helper. **Pending:** web UI handlers, plan close, removal of
  inbox/outbox/yubihsm-doc carryover from the pre-scope-cut branch.
- Any change to the existing `livepeer-network-suite`.
- Live-mainnet smoke gate for the chain-integrated payment-daemon
  (plan 0016 acceptance #3) — funded mainnet wallet + user's preferred
  RPC; runs as a user-driven post-merge gate.

## Active plans

Two numbered design docs at `docs/exec-plans/active/000N-*.md`.

- **Plan 0013** — `0013-suite-openai-gateway-migration-brief.md`.
  Migration brief for the suite's existing `livepeer-openai-gateway`
  (option B from plan 0009). Recommends collapsing the
  engine-vs-shell split during the migration and renaming away from
  `-core`. 5-phase migration sequence; -1,500 to -1,800 net LOC.
  Estimate 8–14 working days.
- **Plan 0019** — `0019-secure-orch-trust-spine-design.md`.
  Implementation **in progress** — 4 of 6 commits cherry-picked onto
  master from a prior in-flight branch (manifest `publication_seq`
  bump, canonical/signing primitives, `livepeer-network-protocol/verify`
  package, console binary scaffold with `127.0.0.1`-only loopback gate).
  v0.1 scope locked 2026-05-06: **V3 keystore only** (no YubiHSM,
  no Ledger, no PKCS#11), **HTTP-only manifest transport** via the
  localhost web UI accessed over `ssh -L` (no USB, no filesystem
  watcher, no inbox/outbox spool). Continuation: web UI handlers +
  audit-log rotation + plan close.

Each plan's open-question list is the gate to implementation work.

Followups still open from earlier plans:

- **Plan 0011-followup** — actual RTMP ingest + FFmpeg + HLS pipeline
  (the session-open phase landed in plan 0011; the media pipeline is
  its own workstream).
- **Plan 0012-followup** — control-plane WebSocket lifecycle +
  media-plane provisioning for `session-control-plus-media`.

Completed plans live in [`docs/exec-plans/completed/`](./docs/exec-plans/completed/) —
plans 0001–0012, 0014, 0015, 0016, 0017, and 0018 are all closed;
together they shipped the 6-mode broker, 6 extractors, gateway-adapters
TS middleware, the OpenAI-compat reference gateway, the wire-compat
sender + receiver daemons, the broker-side interim-debit cadence with
SufficientBalance runway termination on long-running sessions (plan
0015), the warm-key lifecycle (V3 keystore loader + production-mode
wiring + rotation runbook + no-secrets-in-logs lint, plan 0017),
chain-integrated payment (real keccak256-flatten ticket hashing +
on-chain TicketBroker / RoundsManager / BondingManager providers +
ECDSA recovery + nonce ledger + redemption queue with gas pre-checks,
plan 0016), and the orch-coordinator's LAN-side scrape + idempotent
candidate build + signed-manifest receive + locked-down resolver
endpoint + roster/diff/audit web UI (plan 0018).

## Roadmap (rough; subject to change)

| Phase | Outcome | Component subfolder | Status |
|---|---|---|---|
| 0 | Docs-and-spec scaffold + conversation provenance | (root) | ✅ completed (plan 0001) |
| 1 | Interaction-mode specs published as a subfolder | `livepeer-network-protocol/` | ✅ completed (plan 0002) |
| 2 | Capability-broker reference implementation (Go) | `capability-broker/` | ✅ completed (plan 0003) |
| 2.5 | Conformance runner mode drivers | `livepeer-network-protocol/conformance/runner/` | ✅ completed (plan 0004) |
| 3 | Coordinator UX rework — capability-as-roster-entry | `orch-coordinator/` | ✅ completed (plan 0018) |
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
| 9 | Cold-key signed manifest + secure-orch-console | `secure-orch-console/` | 📄 design landed (plan 0019); implementation pending |

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

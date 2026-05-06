# PLANS

Current state of work in this repo, plus pointers to active plans.

## Current state

**Phase 4+** — wire-compat sender + receiver daemons run end-to-end; six
active design-doc plans queued for next-stage implementation work.

**Repo shape: monorepo for now.** All components live as top-level subfolders here;
extraction to standalone repos is a v2 concern. See [`README.md`](./README.md) §"Repo
shape" for the planned component list.

Code shipping today:

- `capability-broker/` — Go reference impl, 6 modes + 6 extractors registered.
- `payment-daemon/` — sender + receiver modes; gRPC over unix socket; BoltDB
  session ledger. Cryptography + chain stubbed under provider interfaces.
- `gateway-adapters/` — TypeScript per-mode middleware for the HTTP family.
- `openai-gateway/` — reference OpenAI-compat gateway end-to-end (calls
  `PayerDaemon.CreatePayment` over unix socket).
- `livepeer-network-protocol/` — spec subfolder (manifest schema + 6 modes +
  6 extractors + payment proto + conformance runner with 11 fixtures).

Design-doc batch (plans 0013, 0015–0019, plus `migration-from-suite.md`)
landed 2026-05-06: ~4,140 lines of paper documenting the next implementation
layer. None of these plans have committed code yet — each surfaces 5–8 open
questions for the user before code can start.

What does not exist yet:

- Real chain integration (Arbitrum signing, ticket validation, redemption).
  Plan 0016 design queued.
- `orch-coordinator/` and `secure-orch-console/` components (designs in
  plans 0018 + 0019; no code).
- Interim-debit cadence on long-running modes (plan 0015 design).
- Any change to the existing `livepeer-network-suite`.

## Active plans

Six numbered design docs at `docs/exec-plans/active/000N-*.md`. Each is
**paper-only** — no committed code. Each surfaces 5–8 open questions the
user must answer before implementation begins.

- **Plan 0013** — `0013-suite-openai-gateway-migration-brief.md`.
  Migration brief for the suite's existing `livepeer-openai-gateway`
  (option B from plan 0009). Recommends collapsing the
  engine-vs-shell split during the migration and renaming away from
  `-core`. 5-phase migration sequence; -1,500 to -1,800 net LOC.
  Estimate 8–14 working days.
- **Plan 0015** — `0015-interim-debit-cadence-design.md`. Broker-side
  per-session ticker + `SufficientBalance` check for long-running
  modes (ws-realtime today; rtmp / session-control gated on
  0011/0012-followup). New `LiveCounter` extractor sibling interface;
  delta-per-tick `DebitBalance(seq)` accounting; new
  `Livepeer-Error: insufficient_balance` code added to the spec.
  4-commit cadence.
- **Plan 0016** — `0016-chain-integrated-payment-design.md`. Replaces
  the v0.2 stub providers with go-ethereum + Arbitrum One: real
  ticket hashing (keccak256-flatten per TicketBroker contract), V3
  keystore, redemption queue + gas pre-checks, signature recovery +
  win-prob + 600-nonce ledger on the receiver side, MaxFloat with
  3:1 deposit-to-pending heuristic on the sender. Wire-compat
  fixturegen as a separate Go module so go-livepeer doesn't pollute
  the daemon dep graph. **Mainnet only — no testnet step.**
  8–10-commit cadence.
- **Plan 0017** — `0017-warm-key-handling-design.md`. Operator-facing
  warm-key lifecycle on top of plan 0016's plumbing: hot-wallet /
  cold-orchestrator split (`--orch-address` semantics already pinned
  in `payment-daemon/docs/operator-runbook.md`), V3 keystore loading
  with eager-decrypt + password XOR + zeroing, 5-step rotation
  runbook, sender-vs-receiver warm-key separation. **No new flags
  beyond what plan 0014's runbook already pins.** 5-commit cadence
  paired with plan 0016's.
- **Plan 0018** — `0018-orch-coordinator-design.md`. Phase 3 from
  the roadmap. New `orch-coordinator/` component scrapes LAN broker
  `/registry/offerings`, builds candidate manifest (JCS-canonical,
  idempotent), hosts signed manifest at
  `/.well-known/livepeer-registry.json`, exposes capability-as-roster
  UX (CLI + JSON API for v0.1; SPA deferred). 4–6-commit cadence.
- **Plan 0019** — `0019-secure-orch-trust-spine-design.md`. The
  cold-key host + diff-and-sign console + air-gap workflow.
  YubiHSM 2 recommended (USB, PKCS#11, secp256k1 native); Ledger
  blocked on app availability; V3 keystore as `--insecure-software-keystore`
  staging fallback. Tauri + CLI dual UI. New `publication_seq`
  field added to manifest schema (pre-1.0.0 minor bump) for
  rollback protection inside the validity window. 7-commit cadence.

Each plan's open-question list is the gate to implementation work.

Followups still open from earlier plans:

- **Plan 0011-followup** — actual RTMP ingest + FFmpeg + HLS pipeline
  (the session-open phase landed in plan 0011; the media pipeline is
  its own workstream).
- **Plan 0012-followup** — control-plane WebSocket lifecycle +
  media-plane provisioning for `session-control-plus-media`.

Completed plans live in [`docs/exec-plans/completed/`](./docs/exec-plans/completed/) —
plans 0001–0012 plus 0014 are all closed; together they shipped the
6-mode broker, 6 extractors, gateway-adapters TS middleware, the
OpenAI-compat reference gateway, and the wire-compat sender + receiver
daemons.

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
| 4-chain | Chain-integrated payment-daemon (Arbitrum One) | `payment-daemon/` | 📄 design landed (plan 0016); implementation pending |
| 4-warmkey | Warm-key lifecycle + rotation | `payment-daemon/` | 📄 design landed (plan 0017); implementation paired with 4-chain |
| 4-interim | Interim-debit cadence on long-running modes | `capability-broker/` | 📄 design landed (plan 0015); implementation pending |
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

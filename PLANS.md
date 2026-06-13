# PLANS

Current state of work in this repo, plus pointers to active plans.

## Current state

**Repo shape: monorepo for now.** All components live as top-level subfolders;
extraction to standalone repos is a v2 concern.

Code shipping today (current retained components):

### Protocol + infrastructure layer

- `livepeer-network-protocol/` — spec subfolder. 6 interaction-modes + 7
  extractors + payment proto + sessionrunner proto + manifest schema with
  `publication_seq` + JCS verifier package + conformance runner with
  fixtures across all modes (happy-path / end-to-end / backpressure /
  reconnect-window / runner-crash / interim-debit / balance-exhausted /
  per-mode gateway-target).
- `capability-broker/` — Go reference impl. 6 modes registered; 7
  extractors. Plan 0011-followup added the production RTMP pipeline
  (yutopp/go-rtmp + 4 encoder profiles passthrough/nvenc/qsv/vaapi/libx264
  + LL-HLS muxer + 4-trigger lifetime watchdog). Plan 0012-followup added
  control-WS + reconnect-30s + pion/webrtc relay + session-runner
  subprocess. Plan 0015 wired the broker-side interim-debit ticker.
- `payment-daemon/` — sender + receiver modes; gRPC over unix socket;
  BoltDB session ledger. Plan 0016 lit up Arbitrum One chain integration
  (keccak256-flatten ticket hashing, V3 keystore signing, on-chain
  TicketBroker + RoundsManager + BondingManager providers, eth_gasPrice
  polling, ECDSA recovery + 600-nonce ledger, MaxFloat with 3:1
  heuristic, redemption queue + loop with gas pre-checks). Plan 0017
  warm-key lifecycle.
- `orch-coordinator/` — orch-side coordinator (plan 0018). LAN scrape +
  JCS-canonical idempotent candidate manifest + tar.gz packaging +
  HTTP-POST signed-manifest receive + 5-step verify + atomic-swap publish
  at `/.well-known/livepeer-registry.json` on a separate locked-down
  public listener. Web UI on the LAN listener.
- `secure-orch-console/` — cold-key host's diff-and-sign UX (plan 0019).
  V3 keystore signer, JCS canonical bytes, secp256k1 + EIP-191
  personal-sign, structural diff vs `last-signed.json`, tap-to-sign
  confirm gesture, audit log with size-based rotation. Localhost-bound
  web UI; operator reaches it over `ssh -L`.

### Removed component families

Removed component families have been deleted from the working tree and
should not be used as implementation references.

What does not exist yet:

- **Live-mainnet smoke gate for chain-integrated payment-daemon** (plan
  0016 acceptance #3) — funded mainnet wallet + user's preferred RPC;
  user-driven post-merge gate.
- **Live-deployment smoke for secure-orch-console v0.1** (plan 0019) —
  operator-driven and post-merge; deployment posture is the operator's
  choice per plan 0019 §13 Q6.
- **Suite + byoc + livepeer-vtuber-project source-repo retirement** —
  user retires manually after audit (per `migration-from-suite.md` §4).
  This monorepo's components are the canonical replacement.
- **Three deferred follow-ups** (not blocking, sequenced as future plan
  dispatches when priority surfaces):
  - **`http-binary-stream@v0` mode definition** — needed to unblock
    `/v1/audio/speech` (currently 503 + `Livepeer-Error:
    mode_unsupported`); separate spec-level plan. Most concrete of the
    three; ships when speech becomes a customer ask.
  - **Hardware-wallet keystore support** (YubiHSM 2 / Ledger / generic
    PKCS#11) — deferred per plan 0019 Q1 lock; revisit when operator
    demand surfaces.
  - **VOD hard-delete janitor** — separate future plan; v0.1 is
    soft-delete only. Operators wanting hard-delete now run a manual SQL
    + S3 cleanup script in their own deployment tooling.

## Active plans

Live in [`docs/exec-plans/active/`](./docs/exec-plans/active/):

- **0031 — Pool follow-up backlog**
- **0034 — Priced funding and final-usage settlement** across gateway,
  broker, and payment-daemon
- **0037 — Operator console UX alignment** (shared shell + design
  language for secure-orch-console and orch-coordinator)
- **0038 — Metrics coverage** for payment-daemon,
  service-registry-daemon, and the broker's dependency surfaces
- **0040 — Pool template onboarding and connected-worker reset**
- **0042 — Automated manifest sign cycle** (secure-orch agent) —
  replaces the hand-carry manifest loop with an outbound-only agent on
  the secure host; amends trust-model sign-cycle invariant #4

The three deferred follow-ups listed above are candidates for the next
plan dispatch when the user picks them up.

Completed plans live in [`docs/exec-plans/completed/`](./docs/exec-plans/completed/).

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
| 5a | HTTP-family mode drivers | `capability-broker/`, `runner/` | ✅ completed (plan 0006) |
| 5b | `ws-realtime` mode driver | `capability-broker/`, `runner/` | ✅ completed (plan 0010) |
| 5c | `rtmp-ingress-hls-egress` session-open phase | `capability-broker/`, `runner/` | ✅ completed (plan 0011) |
| 5c-followup | RTMP + FFmpeg + LL-HLS pipeline | `capability-broker/` | ✅ completed (plan 0011-followup) |
| 5d | `session-control-plus-media` session-open phase | `capability-broker/`, `runner/` | ✅ completed (plan 0012) |
| 5d-followup | Control-WS + reconnect + media-plane provisioning | `capability-broker/` | ✅ completed (plan 0012-followup) |
| 6 | Additional extractors | `capability-broker/` | ✅ completed (plan 0007) |
| 9 | Cold-key signed manifest + secure-orch-console | `secure-orch-console/` | ✅ completed (plan 0019) — code shipped; live-deployment smoke is a user-driven post-merge gate |

Every roadmap row is ✅ shipped. The three deferred follow-ups sit under
"What does not exist yet" — user picks them up as discrete plan
dispatches when priority surfaces.

## Versioning

Pre-1.0.0 on a coordinated monorepo release line. Components inside the
monorepo do not have independent versions yet; when
a component is extracted to a standalone repo, its versioning becomes its own concern.
Until extraction, the monorepo's tag is the single coordinated release artifact for
everything in it.

This repo's release line is independent of upstream source repos used
during the rewrite.

## Tracking debt

[`docs/exec-plans/tech-debt-tracker.md`](./docs/exec-plans/tech-debt-tracker.md). Append as debt accumulates.
</content>

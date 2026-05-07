# PLANS

Current state of work in this repo, plus pointers to active plans.

## Current state

**Infrastructure layer complete; suite + byoc collapse in design-doc batch.** All
protocol-level components are shipping: spec subfolder + capability-broker (6 modes
including the full RTMP→FFmpeg→LL-HLS pipeline + session-control-plus-media with
reconnect-window + pion/webrtc relay + session-runner subprocess) + payment-daemon
(chain-integrated against Arbitrum One + warm-key lifecycle) + gateway-adapters
(TS + Go halves for HTTP family + ws-realtime + rtmp + session-control + WebRTC
SFU pass-through) + reference openai-gateway + orch-coordinator + secure-orch-console.

**Repo shape: monorepo for now.** All components live as top-level subfolders here;
extraction to standalone repos is a v2 concern. See [`README.md`](./README.md) §"Repo
shape" for the planned component list.

Code shipping today:

- `livepeer-network-protocol/` — spec subfolder. Manifest schema + 6 interaction-modes +
  6 extractors + payment proto + sessionrunner proto. Conformance runner with
  fixtures across all modes (happy-path + end-to-end + backpressure +
  reconnect-window + runner-crash + interim-debit + balance-exhausted +
  per-mode gateway-target fixtures). The `verify/` package recovers signers from
  manifest envelopes — cross-cutting verifier consumed by coordinator / resolver /
  gateway (plan 0019).
- `capability-broker/` — Go reference impl. 6 modes registered:
  `http-reqresp@v0` / `http-stream@v0` / `http-multipart@v0` / `ws-realtime@v0` /
  `rtmp-ingress-hls-egress@v0` / `session-control-plus-media@v0`. 7 extractors
  registered (`response-jsonpath` / `request-formula` / `bytes-counted` /
  `seconds-elapsed` / `ffmpeg-progress` / `openai-usage` / `runner-reported`).
  Plan 0011-followup added the production RTMP listener (yutopp/go-rtmp) +
  FFmpeg subprocess wrapper (4 named profiles: passthrough / nvenc / qsv /
  vaapi / libx264) + LL-HLS muxer (default fmp4 + 333ms parts; legacy mpegts
  fallback via `--hls-legacy=true`) + 4-trigger lifetime watchdog
  (expires_at / idle / SufficientBalance / customer CloseSession).
  Plan 0012-followup added control-WS + reconnect-within-30s window + replay
  buffer + pion/webrtc media relay + session-runner subprocess lifecycle (Docker
  launcher + watchdog + graceful Shutdown + drop-all caps default).
  Plan 0015 wired the broker-side interim-debit ticker for long-running sessions.
- `payment-daemon/` — sender + receiver modes; gRPC over unix socket; BoltDB
  session ledger. Plan 0016 lit up real chain integration: keccak256-flatten
  ticket hashing, V3 keystore signing, on-chain TicketBroker / RoundsManager /
  BondingManager providers, eth_gasPrice polling, ECDSA recovery + 600-nonce
  ledger receiver-side, MaxFloat with 3:1 heuristic sender-side, redemption
  queue + loop with gas pre-checks. All under `--chain-rpc`; the dev-mode flow
  (no flag) keeps the daemon testable without any RPC.
- `gateway-adapters/` — split into `ts/` (TypeScript) + `go/` (Go) halves
  per plan 0008-followup. TS half ships per-mode middleware for the HTTP family
  (`http-reqresp`, `http-stream`, `http-multipart`) + the new `ws-realtime` and
  `session-control-plus-media` (control-WS) adapters. Go half ships the
  `rtmp-ingress-hls-egress` listener (yutopp/go-rtmp; aligned with broker-side)
  + `session-control-plus-media` WebRTC SFU pass-through (pion/webrtc).
  Conformance runner now supports `--target=gateway` dispatch (plan 0008-followup
  C4) so per-mode adapter wire-compat is verified end-to-end against a
  mock-broker.
- `openai-gateway/` — reference OpenAI-compat gateway end-to-end (calls
  `PayerDaemon.CreatePayment` over unix socket). Six paid endpoints across
  http-reqresp + http-stream + http-multipart modes.
- `orch-coordinator/` — orch-side coordinator (plan 0018). Scrapes
  capability-broker `/registry/offerings` on the operator's LAN every 30s,
  builds JCS-canonical idempotent candidate manifests, packages as tar.gz
  (`manifest.json` signed bytes + `metadata.json` operator-only sidecar),
  receives cold-key-signed manifests via HTTP POST, runs the five-step verify
  pipeline (schema / signature / identity / spec-version drift /
  publication-seq rollback), atomic-swap publishes at
  `/.well-known/livepeer-registry.json` on a separate locked-down public
  listener (only that path serves; all others return 404). Web UI on the LAN
  listener with roster + diff + audit views. Uniqueness key for tuple
  collision is `(capability_id, offering_id, extra, constraints)` (locked
  per plan 0018 §14 Q2). BoltDB audit log; Prometheus surface.
- `secure-orch-console/` — cold-key host's diff-and-sign UX (plan 0019).
  V3 keystore signer, JCS canonical bytes, secp256k1 + EIP-191 personal-sign,
  structural diff against `last-signed.json`, tap-to-sign confirm gesture
  (last-4-hex-chars input), audit log with size-based rotation. Localhost-bound
  web UI; operator reaches it over `ssh -L`. Manifest transport is HTTP-only
  via the web UI (no inbox / outbox spool, no USB, no filesystem watcher).
  v0.1 scope locked 2026-05-06.

What does not exist yet:

- **Five new components** designed in the migration-brief batch (see Active
  plans below): `customer-portal/`, `openai-runners/`, `rerank-runner/`,
  `vtuber-gateway/`, `vtuber-pipeline/`, `vtuber-runner/`, `video-gateway/`,
  `video-runners/`. All paper-only; implementation gated on per-brief
  walks completing.
- Any change to the existing `livepeer-network-suite/`, `livepeer-byoc/`, or
  `livepeer-vtuber-project/` source trees. They retire manually after the
  monorepo absorbs everything per `docs/design-docs/migration-from-suite.md`.
- Live-mainnet smoke gate for the chain-integrated payment-daemon (plan 0016
  acceptance #3) — funded mainnet wallet + user's preferred RPC; runs as a
  user-driven post-merge gate.
- Live-deployment smoke for secure-orch-console v0.1 (plan 0019) —
  operator-driven and post-merge; deployment posture is the operator's
  choice per plan 0019 §13 Q6.
- Reference `openai-gateway/` adoption of the new `ws-realtime` TS adapter
  (plan 0008-followup C8 deferred — mechanical follow-up; the adapter is
  unit-tested and ready, just needs wiring alongside the existing http-*
  imports in `openai-gateway/src/livepeer/`).
- `images-generations` endpoint port on the OpenAI gateway (deferred to plan
  0013-openai phase 4 with the wire-cut).

## Active plans

Five paper-only migration briefs at `docs/exec-plans/active/0013-*.md`. All
locks landed in November 2026 walks; implementation is per-brief dispatch.

- **Plan 0013-shell** — `0013-shell-customer-portal-extraction.md`.
  Foundation for the four product-family briefs. Extracts the shared SaaS
  shell (customer auth + ledger + Stripe + Lit/RxJS portal/admin SPA shell +
  Fastify middleware + drizzle migrations) into a new `customer-portal/`
  TypeScript library that per-product gateways import as a workspace
  dependency. **Per-product separate businesses** (own Postgres, own pepper,
  own Stripe creds, own customer scope; cross-product SSO out of v0.1).
  18 DECIDED. **Not chain-gated** — the shell is foundation infrastructure;
  it can ship before chain v1.0.0.
- **Plan 0013-openai** — `0013-openai-gateway-collapse.md`. Collapses the
  suite's `livepeer-openai-gateway-core` (engine) + `livepeer-openai-gateway`
  (SaaS shell) into a single `openai-gateway/` component in this monorepo
  (replacing the existing reference impl), using `customer-portal/` for
  the shared shell pieces. Adds image-generation in phase 4 (with the wire
  cut). 14 DECIDED. **Chain-gated v1.0.0** — emits payments.
- **Plan 0013-vtuber** — `0013-vtuber-suite-migration.md`. Three new
  components: `vtuber-gateway/` (the protocol gateway, suite called it
  vtuber-livepeer-bridge), `vtuber-pipeline/` (the SaaS product;
  Python+FastAPI; OAuth Twitch+YouTube; chunked-POST egress workers; chat
  workers), `vtuber-runner/` (operator-pulled image; Python session-runner +
  three.js avatar-renderer + vendored OLV in `third_party/`). Pipeline-app
  acts as a meta-customer of vtuber-gateway (shared-per-deployment API
  key); direct B2B integrators get per-customer keys via the gateway portal.
  15 DECIDED. **Chain-gated v1.0.0** for the gateway; pipeline + runner
  can pre-ship.
- **Plan 0013-video** — `0013-video-gateway-migration.md`. New `video-gateway/`
  component absorbing `livepeer-video-gateway` + `livepeer-video-core` from
  the suite. TypeScript + Fastify; pure-TS RTMP listener; nginx playback-
  origin dropped (broker handles LL-HLS); strict-proxy gateway with no
  cache/CORS rewrite layer (CDN is the operator's add-on); soft-delete VOD;
  customer-tier ABR ladder; opt-in live→VOD handoff. 14 DECIDED.
  **Chain-gated v1.0.0**.
- **Plan 0013-runners** — `0013-runners-byoc-migration.md`. Three new
  workload-runner components: `openai-runners/` (chat + embeddings +
  image-gen + audio + TTS + image-model-downloader + tester),
  `rerank-runner/` (zerank-2 Cohere-compat), `video-runners/` (transcode +
  abr; live-transcode skipped per plan 0011-followup). Shared
  `python-runner-base/` Docker image; capability identity via env +
  YAML manifest; fail-fast GPU probe; amd64-only ML runners + multi-arch
  proxy; opt-in `/metrics` behind flag. 17 DECIDED. **Not chain-gated** —
  workload runners only consume broker dispatch.

The previous `0013-suite-openai-gateway-migration-brief.md` was superseded
when the user locked the single-component-per-product collapse model on
2026-05-06; it lives at `docs/exec-plans/superseded/0013-openai-pre-collapse.md`
for history.

Implementation dispatch order (recommended):
1. **0013-shell first** — foundation; the four product briefs depend on
   `customer-portal/` existing.
2. **0013-runners parallel-safe with shell** — workload runners are
   independent (no chain dep, no shell dep beyond Docker discipline).
3. **0013-openai / 0013-vtuber / 0013-video after shell lands** — each
   depends on `customer-portal/` being importable as a workspace
   dependency.

Completed plans live in [`docs/exec-plans/completed/`](./docs/exec-plans/completed/) —
plans 0001–0012 + 0014 + 0015 + 0016 + 0017 + 0018 + 0019 + 0011-followup +
0012-followup + 0008-followup are all closed. Together they shipped:

- The 6-mode + 7-extractor capability-broker (plans 0003 + 0006 + 0007 + 0010 +
  0011 + 0012 + 0011-followup + 0012-followup).
- The wire-compat sender + receiver payment daemons + chain integration +
  warm-key lifecycle (plans 0005 + 0014 + 0016 + 0017).
- The gateway-adapters TS half (HTTP family) + TS+Go middleware for non-HTTP
  modes (plans 0008 + 0008-followup).
- The reference OpenAI-compat gateway (plan 0009).
- The broker-side interim-debit cadence (plan 0015).
- The orch-coordinator (plan 0018).
- The secure-orch-console v0.1 cold-key trust spine (plan 0019).

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
| 7 | Gateway-side per-mode adapters (HTTP family) | `gateway-adapters/` | ✅ completed (plan 0008) |
| 7-followup | Gateway-adapters: ws-realtime + rtmp + session-control middleware | `gateway-adapters/` | ✅ completed (plan 0008-followup) |
| 8 | OpenAI-compat gateway reference | `openai-gateway/` | ✅ completed (plan 0009) |
| 9 | Cold-key signed manifest + secure-orch-console | `secure-orch-console/` | ✅ completed (plan 0019) — code shipped; live-deployment smoke is a user-driven post-merge gate |
| 10-shell | Shared SaaS shell extraction | `customer-portal/` | 📄 design landed (plan 0013-shell); implementation pending |
| 10-openai | OpenAI gateway suite collapse + UI/billing/admin | `openai-gateway/` | 📄 design landed (plan 0013-openai); chain-gated on v1.0.0 |
| 10-vtuber | Vtuber suite migration (gateway + pipeline + runner) | `vtuber-gateway/`, `vtuber-pipeline/`, `vtuber-runner/` | 📄 design landed (plan 0013-vtuber); gateway chain-gated; pipeline+runner can pre-ship |
| 10-video | Video gateway + video-core collapse | `video-gateway/` | 📄 design landed (plan 0013-video); chain-gated on v1.0.0 |
| 10-runners | Workload runners migration (openai + rerank + video) | `openai-runners/`, `rerank-runner/`, `video-runners/` | 📄 design landed (plan 0013-runners); not chain-gated |

Phases 1–9 are independently shippable. Phase 4-chain (Arbitrum One) gates the
rewrite's v1.0.0 cut, which in turn gates the chain-emitting product gateways
(10-openai, 10-vtuber gateway, 10-video). Phase 10-shell is the foundation for
the per-product collapses; 10-runners is independent. Components can be
extracted from this monorepo to standalone repos at any phase boundary.

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
</content>

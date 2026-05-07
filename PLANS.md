# PLANS

Current state of work in this repo, plus pointers to active plans.

## Current state

**Suite + byoc collapse complete.** All 15 monorepo components shipping.
The protocol layer (broker + payment-daemon + gateway-adapters + spec +
secure-orch-console + orch-coordinator) and the product layer (the new
`customer-portal/` shared SaaS shell + three product-family gateways +
three workload-runner families) are all on master. Suite and byoc trees
are now retire-ready — paper plans 0013-* lock the per-component
absorption; user retires the source repos manually per
`docs/design-docs/migration-from-suite.md`.

**Repo shape: monorepo for now.** All components live as top-level subfolders;
extraction to standalone repos is a v2 concern.

Code shipping today (15 components):

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
- `gateway-adapters/` — split into `ts/` + `go/` halves per plan
  0008-followup. TS half: HTTP family (`http-reqresp` / `http-stream` /
  `http-multipart`) + `ws-realtime` + `session-control-plus-media`
  (control-WS) adapters. Go half: `rtmp-ingress-hls-egress` listener
  (yutopp/go-rtmp) + `session-control-plus-media` WebRTC SFU
  pass-through (pion/webrtc).
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

### SaaS shell + product gateways

- `customer-portal/` — shared TypeScript library (plan 0013-shell).
  Auth (`sk-{env}-{rand}` + HMAC-SHA-256 with pepper) + customer ledger
  (`balance_usd_cents` + `reserved_usd_cents` reservations table keyed on
  `work_id`) + Stripe integration (Checkout sessions + idempotent webhook
  handler) + Lit + RxJS shared widget catalog (signup / login / API-key
  UI / balance display / Stripe checkout / layout / form primitives) +
  Fastify pre-handler middleware composition + drizzle migrations for
  `app.customers / app.api_keys / app.reservations / app.topups /
  app.stripe_webhook_events / app.admin_audit_events`. Each product
  gateway imports it as a workspace dependency. Per-product separate
  businesses (own Postgres, own pepper, own Stripe creds, own customer
  scope; cross-product SSO out of v0.1).
- `openai-gateway/` — collapsed engine + SaaS shell into single
  component (plan 0013-openai). 6 paid endpoints (chat, embeddings,
  images-generations, transcriptions, translations, audio-speech-503)
  across `http-reqresp@v0` + `http-stream@v0` + `http-multipart@v0`
  modes. Per-product `RateCardResolver` reads from `app.rate_card_*`
  tables. `OPENAI_DEFAULT_OFFERING_PER_CAPABILITY` reads YAML at
  `/etc/openai-gateway/offerings.yaml`. Always emits
  `Livepeer-Request-Id`. `mock-runner` Docker image at
  `test/mock-runner/` for offline smoke. `/v1/audio/speech` returns 503
  + `Livepeer-Error: mode_unsupported` until `http-binary-stream@v0`
  lands. True SSE pass-through verified.
- `vtuber-gateway/` — protocol gateway (plan 0013-vtuber). Replaces the
  suite's "vtuber-livepeer-bridge" with the bridge term retired.
  Fastify 5 + drizzle. Pipeline-streams ↔ vtuber-gateway is
  shared-per-deployment API key (pipeline acts as meta-customer); direct
  B2B integrators get per-customer keys via the gateway portal. Bearer
  auth (sessionBearer + workerControlBearer HMAC-SHA-256). WebSocket
  relay with M6 baseline; reconnect-30s replay buffer is a follow-up.
- `vtuber-pipeline/` — Python + FastAPI SaaS product (plan 0013-vtuber).
  Browser REST + WebSocket API, customer billing, OAuth-token vault
  (Twitch + YouTube), egress workers (chunked-POST → ffmpeg → RTMP),
  chat-source workers (Twitch IRC + YouTube Live Chat), Stripe billing.
  Acts as a meta-customer of vtuber-gateway.
- `vtuber-runner/` — Python + Playwright + Chromium + three.js
  avatar-renderer (plan 0013-vtuber). OLV vendored at `third_party/olv/`
  (upstream commit pin in `UPSTREAM.md`). Three-stage Dockerfile:
  Vite renderer build → Python deps via uv → runtime with
  chromium-headless-shell. Reports work-units to broker via
  `SessionRunnerControl.ReportWorkUnits` gRPC stream.
- `video-gateway/` — collapsed `livepeer-video-gateway` +
  `livepeer-video-core` into single component (plan 0013-video).
  TypeScript + Fastify; pure-TS RTMP session-open + LL-HLS strict-proxy
  + tus VOD upload + customer-tier ABR + soft-delete VOD + HMAC webhook
  signing + opt-in live→VOD recording. nginx playback-origin dropped
  (broker handles LL-HLS).

### Workload runners

- `openai-runners/` — chat + embeddings + image-generation + audio
  (Whisper STT) + TTS (Kokoro) + image-model-downloader + openai-tester
  (plan 0013-runners). Shared `python-runner-base/` Docker base for
  Python runners. Capability NAME via env + offering DETAILS via YAML
  at `/etc/runner/offering.yaml`. Fail-fast on `DEVICE=cuda` + no GPU.
  `/metrics` opt-in behind `METRICS_ENABLED=true`.
- `rerank-runner/` — zerank-2 Cohere-compat (plan 0013-runners). Same
  shape as openai Python runners + model-downloader sidecar.
- `video-runners/` — VOD `transcode-runner` + `abr-runner` + shared
  `transcode-core` Go library + `codecs-builder` multi-stage Docker base
  (x264 + SVT-AV1 + libopus + libvpx + libzimg compiled from source).
  amd64-only ML runners; `openai-runner-go` is multi-arch.
  `live-transcode-runner` skipped (capability-broker covers it).

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
- **Plan-flagged follow-ups** (deferred during 0013-* implementation —
  not blocking, sequenced as future plan dispatches):
  - **0013-vtuber-followup** — wire vtuber-gateway Phase 4's 503 stubs
    to real payerDaemon + serviceRegistry + customer-portal Stripe +
    drizzle pool; M6 control-WS reconnect-30s replay buffer; bridge-
    symbol cleanup in inherited `gateway.py` (HTTPBridgeClient et al).
  - **0013-video-followup** — concrete shell-side adapter impls
    (`assetRepo` / `liveStreamRepo` / `webhookSink` /
    `s3StorageProvider`); frontend SPA fill-in; wire-layer payment
    activation post-chain-live.
  - **0008-followup C8** — reference `openai-gateway/` adopting the
    `ws-realtime` TS adapter (mechanical follow-up; plan 0008-followup
    §12 explicitly permitted slipping).
  - **`http-binary-stream@v0` mode definition** — needed to unblock
    `/v1/audio/speech` (currently 503 + `Livepeer-Error:
    mode_unsupported`); separate spec-level plan.
  - **Hardware-wallet keystore support** (YubiHSM 2 / Ledger / generic
    PKCS#11) — deferred per plan 0019 Q1 lock; revisit when operator
    demand surfaces.
  - **VOD hard-delete janitor** — separate future plan; v0.1 is
    soft-delete only per plan 0013-video OQ2 lock.

## Active plans

**None.** All 5 migration briefs (0013-shell + 0013-openai + 0013-vtuber +
0013-video + 0013-runners) shipped. Implementation backlog is empty.

The `docs/exec-plans/active/` directory is empty; future plans land here.
The follow-up items listed above are candidates for the next plan-batch
when the user picks them up.

Completed plans live in [`docs/exec-plans/completed/`](./docs/exec-plans/completed/) —
plans 0001–0012, 0014, 0015, 0016, 0017, 0018, 0019, 0011-followup,
0012-followup, 0008-followup, 0013-shell, 0013-openai, 0013-vtuber,
0013-video, 0013-runners are all closed (26 completed plans).

The pre-collapse plan 0013 lives at
[`docs/exec-plans/superseded/0013-openai-pre-collapse.md`](./docs/exec-plans/superseded/0013-openai-pre-collapse.md)
(superseded when the user locked the single-component-per-product
collapse model on 2026-05-06).

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
| 10-shell | Shared SaaS shell extraction | `customer-portal/` | ✅ completed (plan 0013-shell) |
| 10-openai | OpenAI gateway suite collapse + UI/billing/admin | `openai-gateway/` | ✅ completed (plan 0013-openai) |
| 10-vtuber | Vtuber suite migration (gateway + pipeline + runner) | `vtuber-gateway/`, `vtuber-pipeline/`, `vtuber-runner/` | ✅ completed (plan 0013-vtuber) |
| 10-video | Video gateway + video-core collapse | `video-gateway/` | ✅ completed (plan 0013-video) |
| 10-runners | Workload runners migration | `openai-runners/`, `rerank-runner/`, `video-runners/` | ✅ completed (plan 0013-runners) |

Every roadmap row is ✅ shipped. Follow-up items are tracked under "What
does not exist yet" above; user picks them up as discrete plan dispatches
when ready.

## Versioning

Pre-1.0.0 until the first release is cut. **v1.0.0 = the first release of this
monorepo.** Components inside the monorepo do not have independent versions yet; when
a component is extracted to a standalone repo, its versioning becomes its own concern.
Until extraction, the monorepo's tag is the single coordinated release artifact for
everything in it.

This repo's release line is **independent of `livepeer-network-suite`**, `livepeer-byoc/`,
and `livepeer-vtuber-project/`. The three sources are now retire-ready; user
retires manually post-audit.

## Tracking debt

[`docs/exec-plans/tech-debt-tracker.md`](./docs/exec-plans/tech-debt-tracker.md). Append as debt accumulates.
</content>

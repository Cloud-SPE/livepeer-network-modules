# Plan 0013 — Suite OpenAI-gateway migration brief

**Status:** active (paper exercise; design only)
**Opened:** 2026-05-06
**Revised:** 2026-05-06 (collapse + rename revision)
**Owner:** initial author + assistant
**Type:** option B spinoff from completed plan 0009. Pure design doc; no
code changes in this monorepo, **no changes to the suite**.

Anchors: `docs/exec-plans/completed/0009-openai-gateway-reference.md`,
`livepeer-network-protocol/headers/livepeer-headers.md`,
`livepeer-network-protocol/proto/livepeer/payments/v1/types.proto`,
`payment-daemon/docs/operator-runbook.md`.

## 1. Subject under review

The suite ships an OpenAI-compatible gateway as **two coordinated
TypeScript packages** (the canonical adapter — *not* the deprecated
`byoc-ollama-openai*`):

| Path (suite) | Role |
|---|---|
| `livepeer-network-suite/livepeer-openai-gateway-core/` | Engine. Framework-free dispatchers + provider interfaces + Fastify wrappers. Published as `@cloudspe/livepeer-openai-gateway-core@4.0.1`. |
| `livepeer-network-suite/livepeer-openai-gateway/` | Cloud-SPE shell. Customer/billing/Stripe/admin SPAs over the engine; pulls the engine via npm dep. Internal-only. |
| `livepeer-network-suite/openai-worker-node/` | Go process running on orchestrator hosts; receives HTTP from the gateway, wraps modules with payee-side payment middleware, and drives workload binaries. **Out of scope** for this brief — see §8. |

The **engine** is the relevant artifact. It pins Node ≥20, ESM, TS 5,
Fastify 4, Zod 3, `@grpc/grpc-js`, `eventsource-parser`, drizzle-orm +
pg. Six paid OpenAI endpoints registered as Fastify routes:

- `livepeer-openai-gateway-core/src/runtime/http/chat/completions.ts:44`
  (delegates to `chat/streaming.ts` when `stream: true`,
  `completions.ts:69`)
- `livepeer-openai-gateway-core/src/runtime/http/embeddings/index.ts:36`
- `livepeer-openai-gateway-core/src/runtime/http/images/generations.ts:40`
- `livepeer-openai-gateway-core/src/runtime/http/audio/transcriptions.ts:40`
- `livepeer-openai-gateway-core/src/runtime/http/audio/speech.ts:38`

One framework-free dispatcher per endpoint under
`livepeer-openai-gateway-core/src/dispatch/` (six files, ~1,400 LOC).
Provider interfaces:

- `NodeClient` — `src/providers/nodeClient.ts:250`, fetch impl at
  `src/providers/nodeClient/fetch.ts` (six per-endpoint POSTs).
- `PayerDaemonClient` — `src/providers/payerDaemon.ts:50`, gRPC impl
  at `src/providers/payerDaemon/grpc.ts` (sender-mode unix socket).
- `ServiceRegistryClient` — `src/providers/serviceRegistry.ts:11`
  (`Select` / `ListKnown` against the suite's `service-registry-daemon`).
- `quoteRefresher` (`src/service/routing/quoteRefresher.ts`) polls
  each registered worker's `/quotes` and fills a gateway-local
  `QuoteCache`.

The shell adds customer auth / rate limiting / Postgres ledger / Stripe
/ admin SPAs. Those layers sit above `AuthResolver` / `Wallet` /
`RateLimiter` (`src/interfaces/index.ts`).

### 1.1 Revision: the two-package split is being unwound, not preserved

The split between `livepeer-openai-gateway-core` (OSS engine,
npm-published as `@cloudspe/livepeer-openai-gateway-core@4.0.1`) and
`livepeer-openai-gateway` (Cloud-SPE shell consuming the engine via
npm) was introduced for a future where multiple gateways share the
engine; today there is one consumer. The split pays lockstep-release,
dual-repo CI, and npm-publish taxes for a separation not yet
load-bearing. The rewrite's reference gateway already collapsed it:
one component, ~600 LOC (`openai-gateway/src/livepeer/payment.ts:64`,
`openai-gateway/compose.yaml`, `openai-gateway/Dockerfile`). The
migration is the moment to unwind it — moving to the rewrite's wire
spec and packaging shape together costs little extra. The `-core`
suffix on the engine makes the shell sound canonical when it's the
wrapper; both names retire here. §3.5 has two options.

## 2. Current behavior summary

### 2.1 Per-request lifecycle (chat-completion, non-streaming)

Source: `livepeer-openai-gateway-core/src/dispatch/chatCompletion.ts:62-187`.

1. Pricing pre-flight + `workId = "<callerId>:<uuid>"` (lines 68, 70).
2. `wallet.reserve(callerId, costQuote)` — engine-internal cents
   reservation, **not** the Livepeer payment (line 92).
3. `selectNode` against the registry's `Select` by
   `(capability, model, tier)` (line 94 → `service/routing/router.ts:38`).
4. `quoteCache.get(node.id, capabilityString('chat'))` — hot quote
   refreshed in the background by `quoteRefresher` (line 102).
5. `paymentsService.createPaymentForRequest({ nodeId, quote,
   workUnits, capability, model })` (line 107 →
   `service/payments/createPayment.ts:40`):
   a. Asserts `payerDaemon.isHealthy()` + quote not expired
      (lines 41, 44).
   b. `sessions.getOrStart(nodeId, quote, …)` — per-node
      `StartSession(ticketParams, priceInfo)` cache; first request
      opens, later requests reuse the `workId`
      (`service/payments/sessions.ts`).
   c. `payerDaemon.createPayment({ workId, workUnits, capability,
      model, nodeId })` over gRPC. Wire payload is `{ workId,
      workUnits }` only (`providers/payerDaemon/grpc.ts:119-144`);
      capability/model/nodeId travel for metric labelling
      (`providers/payerDaemon.ts:30-33`).
6. base64-encode `paymentBytes` and POST to
   `<node.url>/v1/chat/completions` with header
   **`livepeer-payment: <b64>`** (`providers/nodeClient/fetch.ts:86`)
   + `content-type: application/json`.
7. Parse `response.usage` → `wallet.commit(handle, usage)` → persist
   `usage_records` row. `wallet.refund(handle)` best-effort on throw.

### 2.2 Streaming chat-completion

`runtime/http/chat/streaming.ts` (wrapper) + `dispatch/streamingChatCompletion.ts`
(engine). Three settlement paths: commit-with-actuals on `usage`
chunk, refund on no-first-token, commit-with-tokenizer-estimate on
first-token-but-no-usage. Upstream via
`nodeClient.streamChatCompletion`
(`providers/nodeClient/fetch.ts:215-241`); same `livepeer-payment`
header; SSE consumed via `eventsource-parser`. Pass-through is **true
streaming** (not buffered) — important for §4.5.

### 2.3 Multipart (transcriptions)

`dispatch/transcriptions.ts:122-142` hand-rolls a multipart boundary
(`buildOutboundMultipart` at line 207) and forwards via
`nodeClient.createTranscription`
(`providers/nodeClient/fetch.ts:175-213`, `duplex: 'half'`). Worker
reports duration via response header
**`x-livepeer-audio-duration-seconds`** (read at `fetch.ts:200`,
constant at `types/transcriptions.ts:11`) — billing depends on it.

### 2.4 Capability discovery, mode dispatch, header set

- Worker emits `/capabilities` + `/quotes`; schemas at
  `src/providers/nodeClient.ts:99-124`. Capability strings already
  follow `<domain>:<uri-path>` — e.g. `openai:/v1/chat/completions`,
  canonical map at `src/types/capability.ts:14-21`.
- **No mode dispatcher.** Routing is path-based: each OpenAI endpoint
  POSTs to the same path on the worker. Worker mux mirrors this:
  `livepeer-network-suite/openai-worker-node/internal/runtime/http/mux.go`
  binds each module by `(HTTPMethod, HTTPPath)`. **No `Livepeer-Mode`
  header anywhere in the suite.**
- Headers emitted today: only `livepeer-payment` (six sites in
  `providers/nodeClient/fetch.ts:86,110,134,158,182,225`) and the
  duration response header above. No `Livepeer-Capability`, no
  `Livepeer-Offering`, no `Livepeer-Spec-Version`, no
  `Livepeer-Request-Id`, no `Livepeer-Work-Units`. Capability/offering
  identity travels out-of-band (capability implicit in path, model in
  JSON body).

### 2.5 Observability

`withMetrics` decorators around `PayerDaemonClient` + `NodeClient`
(`providers/payerDaemon/metered.ts`,
`providers/nodeClient/metered.ts`). `Recorder` emits Prom
counters/histograms labelled by `capability`/`model`/`nodeId`.

## 3. Target shape (this monorepo)

The migration target is the wire spec at
`livepeer-network-protocol/` plus the surfaces the reference gateway
already uses against it. Concretely:

### 3.1 Single dispatch endpoint at the broker

The broker accepts every paid request at `POST /v1/cap` (HTTP family)
or `GET /v1/cap` (ws-realtime upgrade). Source:
`capability-broker/internal/server/routes.go:34-39`. **Dispatch is by
header, not by path.** The broker reads `Livepeer-Mode`, looks up the
mode driver, and the driver calls into the broker's worker resolver to
forward.

### 3.2 Wire headers

`capability-broker/internal/livepeerheader/headers.go:11-17` is the
canonical Go mirror of the spec. The gateway must emit:

| Header | Value example | Spec section |
|---|---|---|
| `Livepeer-Capability` | `openai:/v1/chat/completions` | `livepeer-network-protocol/headers/livepeer-headers.md` §`Livepeer-Capability` |
| `Livepeer-Offering` | `vllm-h100-batch4` (an offering id within the capability — typically the model) | §`Livepeer-Offering` |
| `Livepeer-Payment` | base64(`livepeer.payments.v1.Payment`) bytes | §`Livepeer-Payment` |
| `Livepeer-Spec-Version` | `0.1` | §`Livepeer-Spec-Version` |
| `Livepeer-Mode` | `http-reqresp@v0` / `http-stream@v0` / `http-multipart@v0` | §`Livepeer-Mode` |
| `Livepeer-Request-Id` | optional uuid | §`Livepeer-Request-Id` |

Reference impl emission sites:
`openai-gateway/src/livepeer/headers.ts:5-15`,
`openai-gateway/src/livepeer/http-reqresp.ts:27-32`,
`openai-gateway/src/livepeer/http-stream.ts:30-38`,
`openai-gateway/src/livepeer/http-multipart.ts:29-34`.

### 3.3 Wire-compat `Payment` minted by a co-located sender-mode daemon

The gateway no longer hand-rolls protobuf or stringifies anything from
`TicketParams`. It calls a sender-mode `payment-daemon` over a unix
socket via `PayerDaemon.CreatePayment` and forwards the returned
`payment_bytes` as base64. The proto:
`livepeer-network-protocol/proto/livepeer/payments/v1/payer_daemon.proto:43`.
The reference gateway's call site:
`openai-gateway/src/livepeer/payment.ts:115-132`.

The daemon is the canonical owner of envelope encoding **and** of any
key handling. From `openai-gateway/src/livepeer/payment.ts:5-8`:

> The daemon is the canonical owner of envelope encoding — once
> warm-key handling lands (plan 0017), the gateway being able to sign
> tickets locally would itself be a key-handling surface we don't want.

The wire-compat `Payment` (`types.proto:117-135`) is byte-identical to
go-livepeer's `net.Payment`. The suite's gateway-local
`TicketParams`/`Payment` Zod projections in
`livepeer-openai-gateway-core/src/types/node.ts:26-72` are
**gateway-side state** that becomes redundant once the daemon does the
minting.

### 3.4 Capability-broker termination + quote-free flow

The orch no longer runs `openai-worker-node` on
`/v1/chat/completions`; it runs `capability-broker` on `/v1/cap`. The
broker validates payment via a co-located **receiver-mode**
payment-daemon (`PayeeDaemon.ProcessPayment`) and forwards to a
backend per `host-config.yaml`. The gateway never talks to a worker
port directly.

The wire spec is **quote-free** (`payer_daemon.proto:23-43`):
receiver-side quote endpoint is gone; pricing is operator-configured
at the orch; the sender daemon authors `TicketParams` JIT. This
collapses three suite components — `quoteRefresher`, `quoteCache`,
`serviceRegistry.listKnown` — and reduces `selectNode` to "use the
configured broker URL for this orch identity".

### 3.5 Packaging shape: one OSS package, optional separate SaaS repo

The target is **one OSS package + (optionally) one separate SaaS
layer in a separate repo**, NOT two packages in one repo. Two options:

**Option A — Collapse (preferred).** One package, `openai-gateway/`
(OSS, protocol-only — mirrors the rewrite's reference impl shape).
Cloud-SPE billing / customers / Stripe / admin-SPA layers **move to
a separate internal repo** with a role-descriptive name like
`cloudspe-openai-billing/` or `tzt-openai-shell/`. Different release
cadences, audiences, perimeters. Trade-off: stands up a new repo +
relocates shell history against the extracted `Wallet` /
`AuthResolver` / `RateLimiter` boundary.

**Option B — Engine + shell rename (fallback).** Same monorepo, two
packages, role-descriptive names: `openai-gateway-engine/` (was
`-core`) + `openai-gateway-saas/` (was `livepeer-openai-gateway`).
Smaller revision; doesn't fix the lockstep / dual-CI / npm-publish
taxes inherent to two-package layout.

Both retire the `-core` suffix and the bare `livepeer-openai-gateway`
name. Phase-4 acceptance (§6) requires *at minimum* that no `-core`
survives in package metadata, repo names, or import paths.

## 4. Delta — what would change

### 4.1 Payment minting

| Aspect | Suite today | Target |
|---|---|---|
| Trigger | `paymentsService.createPaymentForRequest` (`src/service/payments/createPayment.ts:40`) | Same name, but body collapses to a single `payerDaemon.createPayment(face_value, recipient, capability, offering)` call. |
| Daemon-call payload | `{ workId, workUnits }` (`src/providers/payerDaemon/grpc.ts:124`) — work-unit-driven, session-cached | `{ face_value, recipient, capability, offering }` per `payer_daemon.proto:54-71` — target-spend-driven, session-free at the gateway boundary |
| Session bookkeeping | `sessions.getOrStart(nodeId, quote, …)` opens `StartSession(ticketParams, priceInfo)` per-node, caches `workId` | **Deleted.** No `StartSession` on the sender; sessions are a receiver-side concept. `src/service/payments/sessions.ts` and the gateway's per-node session cache go away. |
| `TicketParams` handling | Fetched by gateway via `/quote`, fed back to daemon via `StartSession(ticketParams)` | Daemon fetches them directly from the worker's `/v1/payment/ticket-params`. The gateway never sees a `TicketParams`. |
| Wire-format | `paymentBytes` is whatever the suite daemon emits today; suite has its own gen'd protos at `src/providers/payerDaemon/gen/livepeer/payments/v1/` | Wire-compat 5-message format from `livepeer-network-protocol/proto/livepeer/payments/v1/types.proto`. Suite's generated stubs need re-gen against the rewrite's protos. **Validate via the round-trip test referenced in `wire-compat.md`.** |
| Header name | `livepeer-payment` (lowercase, six sites in `nodeClient/fetch.ts`) | `Livepeer-Payment` (canonical case; HTTP is case-insensitive, but every reference impl uses canonical) |

### 4.2 Headers (the set of strings)

Per worker call, the suite's nodeClient currently sets exactly two
identifying headers: `content-type` and `livepeer-payment` (e.g.
`fetch.ts:84-87`).

**Net delta per call:** add five new headers, drop zero. (Existing
`livepeer-payment` is renamed only in case.)

### 4.3 Mode dispatching

| Aspect | Suite today | Target |
|---|---|---|
| Dispatch | Path-based: each OpenAI endpoint POSTs to the same path on the worker (chat/completions, embeddings, …). Six fetch impls in `nodeClient/fetch.ts:79-241`. | Header-based: every paid call goes to `<broker>/v1/cap`, `Livepeer-Mode` chooses the driver. |
| Mode mapping | (implicit) | `chat-completions` (non-streaming) + `embeddings` + `images/generations` + `audio/speech` non-stream → `http-reqresp@v0`. Streaming chat → `http-stream@v0`. Transcriptions → `http-multipart@v0`. (Anchors: reference gateway emits these at `openai-gateway/src/livepeer/http-reqresp.ts:4`, `http-stream.ts:6`, `http-multipart.ts:4`.) |
| Capability strings | Preserved as-is. The suite's canonical map (`src/types/capability.ts:14-21`) is already the right shape (`<domain>:<uri-path>`). | No change. The strings continue to be exactly `openai:/v1/chat/completions`, `openai:/v1/embeddings`, etc. |
| Offering | Implicit (model in body) | Explicit `Livepeer-Offering` header. Convention from the reference gateway (`routes/chat-completions.ts:25`): one offering per `(stream | non-stream)`-by-`model` combination, named by the orch. The gateway picks an offering string per request (today derives from model + stream-flag); the broker rejects unknowns with `Livepeer-Error: offering_not_served`. |

### 4.4 Body shapes / multipart handling

- **Non-streaming JSON** (chat / embeddings / images / speech): no
  change to body shape. Forward verbatim. Suite's
  `nodeClient.createChatCompletion` body building at
  `fetch.ts:88-90` becomes a `http-reqresp` send call mirroring
  `openai-gateway/src/livepeer/http-reqresp.ts:36-41`.
- **Multipart (transcriptions):** the suite's hand-rolled boundary
  builder at `dispatch/transcriptions.ts:207-237` keeps working — the
  multipart body goes into `http-multipart`'s `body` (FormData or raw
  Buffer + content-type). The reference impl shows the FormData path
  is preferred (`http-multipart.ts:36-43`), but Buffer + explicit
  content-type is supported. Suite already produces a Buffer; the
  shorter migration is to keep the Buffer path.
- **SSE streaming:** the suite already consumes upstream SSE via
  `eventsource-parser` (`fetch.ts:243-283`). The `http-stream@v0`
  driver in the reference gateway also expects SSE-shaped responses
  with trailers carrying `Livepeer-Work-Units`
  (`http-stream.ts:60-87`). The suite's streaming dispatcher at
  `dispatch/streamingChatCompletion.ts` doesn't read trailers today —
  the new flow either:
  (a) keeps `Livepeer-Work-Units` in headers (broker emits it that way
  for non-trailer-capable downstreams), or
  (b) wires trailer reading into the dispatcher.
  Reference gateway uses (a) (`http-stream.ts:74-78` falls back to the
  header). Pick (a) for the migration.

### 4.5 Streaming semantics

- The dispatcher's three-settlement-path logic
  (`dispatch/streamingChatCompletion.ts:56-83`) is **independent of
  the wire** — it's local billing logic over an upstream SSE stream.
  Survives the migration unchanged.
- The suite forwards SSE bytes from worker → customer with TLS
  termination at the gateway. Reference impl currently buffers the
  whole SSE body before flushing
  (`openai-gateway/src/routes/chat-completions.ts:38-40` + tech-debt
  note in `0009-openai-gateway-reference.md` line 56-60). The suite
  does **true** pass-through (`runtime/http/chat/streaming.ts`). The
  migration must preserve true pass-through; the reference gateway is
  the wrong template here. The fix is to use the reference's
  `http-stream.send` directly but plumb the response body through
  rather than `await arrayBuffer`. Tracked as tech-debt in the
  reference; suite must not regress.

### 4.6 Configuration surface

| Suite env / config | Disposition |
|---|---|
| `service-registry-daemon` URL + auth (`config/serviceRegistry.ts`) | **Removed.** No registry on the gateway. Daemon resolves recipients itself. |
| `quote_refresh_seconds`, `quote_ttl_seconds` (`config/routing.ts`) | **Removed.** Quote-free flow. |
| `payerDaemon.socketPath`, `payerDaemon.callTimeoutMs`, `payerDaemon.healthIntervalMs` (`config/payerDaemon.ts`) | **Kept.** Same semantics. Reference uses `LIVEPEER_PAYER_DAEMON_SOCKET` (`openai-gateway/compose.yaml:129`). |
| `bridgeEthAddress` (gateway-local sender identity passed to `quoteRefresher` and `selectNode`) | **Kept** but moves into the daemon's config (the daemon owns the keystore; the gateway no longer needs to emit a sender address at the application boundary). |
| **New:** `LIVEPEER_BROKER_URL` | The orch's broker endpoint. Single value per orch identity. Reference uses this name (`openai-gateway/compose.yaml:128`). |
| **New:** spec-version + default offering convention | Per-capability defaults; managed by whatever survives of the suite's shell layer (post-collapse: the SaaS repo or the `-saas` package; see §3.5). |
| `nodeCallTimeoutMs` | **Renamed** to `brokerCallTimeoutMs`. Same numeric value. |
| Manifest entries (orch-side `host-config.yaml`) | **Out of scope** — orch operator concern, not gateway-engine. |

### 4.7 Test-harness changes

- `livepeer-openai-gateway-core/tests/` is built around fakes for
  `NodeClient`, `PayerDaemonClient`, `ServiceRegistryClient`, `Wallet`.
  - `NodeClient` fake (`src/providers/nodeClient/testFakes.ts`) — gets
    replaced by a `BrokerClient` fake that returns SSE/JSON/multipart
    just like a `/v1/cap` response. Surface area shrinks (one method
    per mode instead of one per OpenAI endpoint) — likely net
    reduction.
  - `PayerDaemonClient` fake — surface tightens. `startSession`,
    `closeSession`, `getDepositInfo`, `isHealthy` all stay; the
    `createPayment` fake's input changes shape per §4.1.
  - `ServiceRegistryClient` fake — **deleted.**
- `serviceRegistry.test.ts`, `quoteRefresher` tests, `selectNode.test.ts`,
  `quoteCache.test.ts`, `circuitBreaker*.test.ts` — most can be
  deleted. The gateway-local circuit-breaker
  (`src/service/routing/circuitBreaker.ts`) might still be useful per
  broker-URL or per recipient; keep if used, delete if not.
- A new conformance smoke test against
  `compose.yaml`-style stack (mirror `openai-gateway/compose.yaml`
  +  `openai-gateway/scripts/smoke.sh`) gates the migration phase by
  phase.

## 5. Blockers

### 5.1 Wire-compat protobuf re-gen (hard prerequisite)

Suite ships its own gen'd stubs at
`livepeer-openai-gateway-core/src/providers/payerDaemon/gen/livepeer/payments/v1/`
(from suite's `livepeer-payment-library`). Migration needs (a) re-gen
against `livepeer-network-rewrite/livepeer-network-protocol/proto/livepeer/payments/v1/`,
and (b) byte-for-byte equivalence on a corpus of envelopes. The
rewrite shipped this side in plan 0014; the suite-side daemon needs
the same alignment. **Until both daemons emit byte-identical
envelopes, the migration cannot ship.**

### 5.2 Quote-free flow vs quote-driven dispatcher

Suite billing reads a hot quote at request time
(`dispatch/chatCompletion.ts:102`) for (1) sizing `face_value` via
`StartSession(priceInfo)` and (2) ledger `expectedValueWei`
reconciliation. In the new flow (1) becomes the daemon's job (sender
`MaxEV` / `MaxTotalEV` knobs in the operator runbook); (2) still
works via `CreatePaymentResponse.expected_value`
(`payer_daemon.proto:84`). **Action:** switch the gateway's
`face_value` from quote-driven to an env-var per orch. Reference uses
placeholder `DEFAULT_FACE_VALUE_WEI = 1000n`
(`openai-gateway/src/livepeer/payment.ts:64`).

### 5.3 Streaming pass-through

Reference gateway buffers SSE bodies (tracked as plan-0009 tech debt).
Suite must not regress — its current pass-through is true streaming.
Migration must use Node's `http.request` with an unbuffered pipe to
the customer reply. **Implementation discipline; not a spec blocker.**

### 5.4 `/v1/audio/speech` — streaming binary, not SSE

Suite returns `ReadableStream<Uint8Array>` with audio content-type
(`fetch.ts:151-173`). Wire spec's `http-stream@v0` is SSE-shaped
(`Accept: text/event-stream`, `http-stream.ts:35`). **Real spec gap.**
Resolutions: (a) a new `http-binary-stream@v0` mode, or (b) piggyback
on `http-reqresp@v0` with binary content-type at the cost of
buffering full audio in the gateway. Both want a separate plan; out
of scope here.

### 5.5 Images endpoint — minor

Suite covers `/v1/images/generations` + `/v1/images/edits`; reference
covers neither. JSON (URLs) and base64-inline both fit
`http-reqresp@v0`. First adopter writes the routes — not a blocker.

### 5.6 `x-livepeer-audio-duration-seconds` is unspecified

Read at `fetch.ts:200`, declared at `types/transcriptions.ts:11`. Not
in `headers/livepeer-headers.md`. **Recommendation:** fold into
`Livepeer-Work-Units` (`livepeerheader/headers.go:27`); duration *is*
the work unit for transcription. Update the matching extractor in
`livepeer-network-protocol/extractors/`. Action item, not blocker.

### 5.7 Public-surface deprecation: `@cloudspe/livepeer-openai-gateway-core@4.0.1`

The engine is published to npm. Even if practical consumer count is
one (the suite shell), retiring the name warrants a concrete heads-up:
audit npm download stats and the `cloudspe` org; publish a final
`4.0.x` tagged `deprecated` with a README pointing at the new name +
the upstream rewrite; lock down the old name on npm. Option A also
needs an internal announcement of the SaaS repo's location; option B
needs both new package names announced and old metadata stubbed.
Coordination concern, not a technical blocker — but must land before
phase 4 ships.

## 6. Sequencing recommendation

Five phases. Each phase is independently revertable. Acceptance is
defined per phase and gates the next.

### Phase 1 — Daemon wire-compat alignment (prerequisite, no gateway change)

Suite-side payment-daemon emits byte-compatible `Payment` envelopes
per rewrite protos. **Acceptance:** round-trip test against ≥10
fixtures from the rewrite's wire-compat corpus.
**Risk:** low; offline-verifiable. **Diff:** 0 in the gateway.

### Phase 2 — Header rename + emit (no behavior change)

In `providers/nodeClient/fetch.ts` (six call sites), rename
`'livepeer-payment'` to canonical `'Livepeer-Payment'` and add
`Livepeer-Capability` (using the existing canonical map at
`src/types/capability.ts:14-21`), `Livepeer-Offering`,
`Livepeer-Spec-Version`, `Livepeer-Mode` (one of `http-reqresp@v0`,
`http-stream@v0`, `http-multipart@v0`). Old workers ignore the new
headers; path is still `/v1/chat/completions` etc.
**Acceptance:** existing tests + smoke pass; new tests assert the
four headers per outbound call. **Risk:** very low. **Diff:** ~50
LOC × 6 ≈ ~300 LOC. **Elapsed:** 1 day.

### Phase 3 — Sender-daemon RPC shape

Swap `PayerDaemon.createPayment({ workId, workUnits })` for
`{ face_value, recipient, capability, offering }`. Drop `StartSession`
/ `CloseSession` and `SessionCache` (`src/service/payments/sessions.ts`).
`workUnits` stays in-memory for the local `Recorder`. Ledger
`expectedValueWei` still threads through unchanged.
**Acceptance:** engine tests pass; smoke against `payment-daemon/`
sender. **Risk:** moderate — ledger reconciliation needs a careful
re-read. **Diff:** ~800 LOC across ~10 files. **Elapsed:** 2-3 days.

### Phase 4 — Forward to broker + collapse two-package split + rename

**Wire cut.** Replace `providers/nodeClient/fetch.ts` impls with a
`brokerClient` POSTing to `<brokerUrl>/v1/cap`. Drop
`serviceRegistry`, `quoteCache`, `quoteRefresher`, `selectNode`. Keep
`circuitBreaker.ts` only if it earns its weight per-broker-URL.
Dispatchers lose `selectNode` + `quoteCache.get`; `node.url` becomes
a single config-resolved broker URL.

**Packaging cut.** Per §3.5, pick option A or B before opening this
phase; choice changes file-move shape, not wire work. For A, the
`Wallet` / `AuthResolver` / `RateLimiter` boundary at
`src/interfaces/index.ts` is what the new SaaS repo imports against;
it gets re-homed, not broken.

**Acceptance:** (1) smoke against rewrite's `capability-broker` +
receiver-mode `payment-daemon` + mock backend (mirror
`openai-gateway/compose.yaml`); six endpoints, full lifecycle incl.
refunds. (2) **No `-core` suffix anywhere** — metadata, repo names,
import paths. Option A: single OSS package, role-descriptive name.
Option B: two packages, both renamed. (3) §5.7 deprecation
announcement out before phase-4 PR merges.

**Risk:** highest. Mitigation: file moves in a separate commit from
logic changes. **Diff:** -2,200/-2,300 LOC removed, +600 added — more
negative than originally estimated because cross-package plumbing
(~200-300 LOC of re-export shims, npm-publish workflow files,
`package.json` duplication) also goes. **Elapsed:** 4-6 days (A
upper end, B lower).

### Phase 5 — Pass-through verification + speech plan

Verify streaming dispatcher preserves true pass-through (Node http
client unbuffered; broker `http-stream@v0` driver flushes per chunk —
`capability-broker/internal/modes/httpstream/driver.go`). For
`/v1/audio/speech`, defer to a spec-followup plan; gate behind a
feature flag if the spec gap isn't closed first.
**Acceptance:** streaming-latency post-phase-4 ≤ 1.2× pre-phase-4;
speech either works under `http-reqresp@v0` (with documented memory
caveat) or returns 503 + `Livepeer-Error: mode_unsupported`.
**Risk:** low (SSE) / medium (speech). **Elapsed:** 1-2 days.

## 7. Scope estimate

| Phase | Net LOC diff | Files touched | Elapsed | Deploy risk |
|---|---|---|---|---|
| 1 | 0 (gateway side) | 0 | (depends on suite daemon repo) | none for gateway |
| 2 | +200 to +300 | 6-8 | 1 day | very low — header-only, backward-compatible |
| 3 | +400, -400 (~ ±800) | 10-12 | 2-3 days | moderate — daemon RPC shape; gated by phase 1 |
| 4 | -2,200/-2,300 (A) or -2,000/-2,100 (B); +600 | 30-40 | 4-6 days | highest — structural cut + packaging collapse |
| 5 | +50 to +200 | 2-4 | 1-2 days | low (SSE) / medium (speech) |
| **Total** | ~ -1,500 to -1,800 net (A) / -1,200 to -1,500 (B) | 45-60 | **8-14 working days** | dominated by phase 4 |

The headline is phase 4's net negative — the migration **simplifies**
the gateway because the registry/quote machinery is gone *and* the
cross-package plumbing between engine and shell goes away. Most work
is moving billing logic from "gateway computes from quote" to "daemon
computes; gateway reads back"; the rest is file relocation under the
chosen packaging option. Phase 4 is the bottleneck; option A's
separate-repo logistics push the upper end out by ~1-2 days vs B.

## 8. Out of scope

- **`openai-worker-node`** — the orchestrator-side process the suite
  ships. The migration target is to retire it in favour of
  `capability-broker` + the orch's `host-config.yaml` backend pointers.
  That is a separate workstream owned by the orch operator, gated by
  capability-broker feature parity (production extractors, capacity
  semantics, real metrics). No change to this brief.
- **Suite shell internals** — `livepeer-openai-gateway/` (Stripe +
  customers + ledger + admin SPAs) is **relocated, not rewritten**,
  by phase 4. The `Wallet` / `AuthResolver` / `RateLimiter`
  interfaces hide the wire from the shell whether it ends up in a
  separate repo (option A) or a renamed sibling package (option B).
- **`Livepeer-Audio-Duration-Seconds` standardization** — spec-level
  followup, not this brief.
- **`/v1/audio/speech` mode definition** — needs `http-binary-stream@v0`
  or equivalent; separate spec-level plan.
- **Image endpoint reference impl** — rewrite's `openai-gateway/`
  doesn't have it; first adopter writes the routes using
  `http-reqresp@v0`.
- **Sender daemon hot-key handling, real chain integration** — owned
  by rewrite plans 0016/0017. Stubbed crypto + provider-fakes ship
  as-is.
- **Cutover plan + customer comms** — paper exercise. Production
  cutover (parallel-run, draining, etc.) is its own runbook.
- **Choice between option A and option B (§3.5)** — user's, before
  phase 4 opens.

## 9. Notes

Suite paths cited with the `livepeer-openai-gateway-core/` prefix
reflect **the suite's current layout**. Post-phase-4 those files live
under the chosen §3.5 name (`openai-gateway/src/...` for option A,
`openai-gateway-engine/src/...` for option B); citations are not
retro-rewritten because that would obscure what the migration is
moving *from*. The reference gateway (`openai-gateway/`) is the
target shape — single package, protocol-only, ~600 LOC.

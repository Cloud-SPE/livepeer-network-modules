---
plan: 0027
title: Layered route health and check placement
status: completed
phase: shipped
opened: 2026-05-14
owner: harness
related:
  - "completed plan 0003 — capability broker"
  - "completed plan 0018 — orch-coordinator design"
  - "active plan 0025 — per-request ticket-params broker routing"
  - "active plan 0026 — Daydream Scope capability"
---

# Plan 0027 — Layered route health and check placement

## 1. Problem

The current architecture already distinguishes:

- Layer 1: signed manifest health
- Layer 2: broker live health
- Layer 3: observed failure-rate health

But the execution split is still too implicit. Two practical gaps remain:

1. `orch-coordinator` must know whether a broker is online and whether
   each advertised tuple is still actually serveable.
2. gateways need a consistent way to avoid routing to tuples that remain
   present in the signed manifest but are currently unhealthy.

Without a locked placement for each check, implementations will drift
toward one of two bad shapes:

- **smearing liveness into the sign cycle** — reissuing manifests for
  routine outages
- **smearing trust into hot-path routing** — letting unsigned live
  health create routable capability claims

This plan locks where each check goes, which component acts on it, and
what "healthy" means on the request path.

It also locks the extensibility boundary for specialized health logic:
custom health behavior is allowed, but it must be implemented as a
broker-owned probe recipe and normalized before it leaves the broker.

## 2. Goals

Ship a routing stack where:

1. the broker remains the source of truth for runtime reachability
2. the coordinator surfaces broker and tuple readiness for operators
3. `service-registry-daemon` composes signed manifest validity with
   short-TTL live health
4. gateways make final route choices using resolver output plus local
   observed-failure policy
5. no component learns workload semantics just to answer a health
   question
6. core modules allow specialized health behavior without pushing
   capability semantics into the coordinator, resolver, or gateways

## 3. Non-goals

- inventing a signed "live health" channel
- pushing Layer 3 failure-rate decisions into the chain or manifest
- adding capacity advertisement to the manifest
- teaching the resolver or coordinator capability-specific health logic
- redesigning gateway pricing or ticket-minting flow

## 4. Locked model

### 4.1 Layer ownership

| Layer | Question | Source of truth | Acting component |
|---|---|---|---|
| 1 | "Did the operator declare this tuple?" | cold-signed manifest | `service-registry-daemon` verifies; gateways trust resolver output |
| 2 | "Is this tuple live right now?" | broker `/healthz` + `/registry/health` | broker reports; coordinator and resolver poll |
| 3 | "Has this route been working well for me lately?" | observed request outcomes | gateway-local route selection policy |

### 4.2 Request-path rule

A route is selectable only when:

1. the tuple exists in a valid signed manifest
2. the resolver's live-health cache says the tuple is `ready`
3. the gateway's local failure-rate policy has not currently opened the
   circuit

### 4.3 Hard boundaries

- `orch-coordinator` may observe live health but must not mutate the
  signed manifest automatically because a probe failed.
- `service-registry-daemon` may hide live-unhealthy tuples from route
  selection but must not invent tuples missing from the signed manifest.
- gateways may circuit-break routes locally but must not bypass Layer 1
  manifest validity.
- the broker may implement backend-specific probes internally but must
  normalize outward health states to workload-agnostic values.

### 4.4 Extensibility rule

Specialized health logic is allowed only at one seam:

- `host-config.yaml` selects a **probe recipe** per tuple
- capability-broker implements the probe recipe library
- `/registry/health` publishes only normalized status + freshness + reason

This means:

- adding a new capability with an existing probe recipe is a YAML change
- tuning thresholds for a capability is a YAML change
- adding a brand-new probe recipe type is a broker change
- coordinator, resolver, and gateways do not gain workload-specific
  health branches

## 5. Impacted components

### 5.1 capability-broker

Owns Layer 2 truth.

Required work:

- keep `GET /healthz` as broker-process liveness
- make `GET /registry/health` the canonical per-tuple health surface
- ensure `/registry/health` only reports tuples that also exist in
  `/registry/offerings`
- add per-tuple probe-recipe selection in `host-config.yaml`
- normalize probe results into a small generic state set:
  `ready`, `draining`, `degraded`, `unreachable`, `stale`
- expose probe freshness and reason fields
- ship a fixed broker-side probe recipe library with configuration for:
  - cadence
  - timeout
  - consecutive failure threshold
  - optional success threshold for recovery

Questions the broker answers:

- is the process up?
- is this `(capability_id, offering_id)` backend reachable now?
- is the operator draining this tuple?

Questions the broker does not answer:

- should this tuple be market-visible?
- should a gateway prefer this route over another similarly-healthy one?

Examples of probe recipe types the broker should be able to host:

- `http-status`
- `http-jsonpath`
- `http-openai-model-ready`
- `tcp-connect`
- `command-exit-0`
- `manual-drain`
- mode- or runner-specific recipes where the runtime shape requires it

### 5.1.1 Workload matrix

The broker probe model must be validated against more than OpenAI-style
HTTP services. At minimum, the shared health contract must accommodate
these workload families without changing coordinator, resolver, or
gateway semantics.

| Workload family | Example capability shape | What "ready" may actually mean | Likely probe recipe shape | Outward normalized result |
|---|---|---|---|---|
| OpenAI-compatible HTTP | `openai:chat-completions:<model>` | backend reachable, requested model loaded, auth usable | `http-openai-model-ready` or `http-jsonpath` | `ready` / `degraded` / `unreachable` |
| Video / RTMP | `video:live.rtmp`, `transcode-vod` | ingest or worker path healthy, encoder initialized, required subprocess ready | mode-specific broker recipe or `command-exit-0` + local dependency checks | `ready` / `degraded` / `draining` |
| VTuber | `livepeer:vtuber-session` | control plane healthy, session runner reachable, media/session prerequisites available | session-runtime-specific broker recipe | `ready` / `degraded` / `draining` |
| Daydream Scope | `daydream:scope:v1` | Scope control API responsive, session-open path valid, required external media dependency available | `http-jsonpath` or dedicated external-media recipe | `ready` / `degraded` / `unreachable` |
| Generic SaaS backend | `customer:custom-rest-api` | upstream reachable, credentials valid, quota or account state usable | `http-status`, `http-jsonpath`, or SaaS-auth-aware recipe | `ready` / `degraded` / `unreachable` |
| Future unknown capability | arbitrary opaque string | workload-defined | existing recipe if possible; otherwise new broker recipe type | same normalized states only |

The lock here is deliberate:

- workload families may require different readiness logic
- that logic lives only in broker probe recipes
- the cross-stack health contract does not fork by workload family

### 5.2 orch-coordinator

Owns operator visibility for Layer 2, but not routing.

Required work:

- scrape broker `/registry/offerings` for candidate-manifest input
- scrape broker `/healthz` and `/registry/health` on a short cadence
- store live-health cache per broker and per tuple
- show freshness / readiness badges in the roster UI
- expose metrics for broker reachability, tuple readiness, and stale
  health data

Questions the coordinator answers:

- which brokers are reachable right now?
- which signed or candidate tuples currently report `ready` vs
  `degraded`?
- is a tuple disappearing because of operator intent or a live outage?

Questions the coordinator does not answer:

- which tuple should a gateway route this request to?
- should the signed manifest be rewritten because a broker went down for
  2 minutes?

### 5.3 service-registry-daemon

Owns composition of Layer 1 and Layer 2 for routing.

Required work:

- continue fetching and verifying signed manifests from orch
  `serviceURI`
- add live-health polling against broker `/registry/health`
- maintain a short-TTL route-health cache keyed by
  `(orch, worker_url, capability_id, offering_id)`
- return only tuples that are both:
  - manifest-valid
  - live-healthy
- treat stale live-health cache as unavailable on the hot path
- export metrics showing:
  - manifest-valid tuples
  - live-ready tuples
  - tuples filtered out due to stale or degraded health

Questions the resolver answers:

- who is allowed to serve this capability?
- among those, who is live-ready right now?

Questions the resolver does not answer:

- which route has been best for this gateway over the last 10 minutes?
- should a live outage remove the tuple from the signed manifest?

### 5.4 openai-gateway and other gateways

Own local Layer 3 route choice.

Required work:

- consume resolver output as the Layer 1 + Layer 2 filtered candidate
  set
- maintain per-route observed outcome counters and sliding-window policy
- skip or down-weight routes with recent 5xx / timeout patterns
- retry to the next resolver candidate when policy allows
- expose route-health and circuit-breaker metrics to operator surfaces
- share Layer 3 cooldown policy through the workspace package
  `gateway-route-health/` instead of re-implementing tracker logic in
  each gateway

OpenAI gateway example:

- `POST /v1/chat/completions` asks the resolver for
  `openai:chat-completions:<model>`
- resolver returns only tuples that are signed and live-healthy
- gateway picks among them using local observed-failure policy
- if the chosen route times out repeatedly, the gateway starts skipping
  it even before the broker health probe flips red

Shared Layer 3 package rule:

- the generic cooldown / retry / metrics tracker lives in
  `gateway-route-health/`
- each gateway may keep a thin local wrapper only for route-key shape
  and selection style (`rankCandidates` vs `chooseRandom`)
- policy changes to Layer 3 should land in the shared package unless a
  gateway has a documented reason to diverge

## 6. Execution phases

### 6.0 Status update

The core implementation phases covered by this plan are now present in
code:

- broker-owned Layer 2 tuple health is implemented in
  `capability-broker`
- coordinator live-health polling and operator surfacing is implemented
  in `orch-coordinator`
- Layer 1 + Layer 2 resolver filtering is implemented in
  `service-registry-daemon`
- Layer 3 cooldown and fallback policy is implemented in all active
  gateways
- real daemon-backed gateway composition tests exist for OpenAI, video,
  VTuber, and Daydream
- Layer 3 policy has been extracted into the shared workspace package
  `gateway-route-health/`

Remaining work under this plan is now mostly maintenance and follow-up:

- keep future gateways on the shared Layer 3 package
- decide whether Prometheus text rendering should also move into shared
  gateway code
- extend end-to-end coverage when new workload families or gateways land

### Phase 1 — Broker health contract

Components:

- `capability-broker/`
- `livepeer-network-protocol/` if the endpoint shape must be frozen in
  protocol docs

Deliverables:

- document `/registry/health` response contract
- document broker-side probe recipe contract and config shape
- lock generic outward states and freshness semantics
- ensure tuple identity matches `/registry/offerings`
- add tests covering:
  - healthy tuple
  - draining tuple
  - backend unreachable
  - stale probe data
  - specialized probe mapped to normalized status
  - at least one representative probe from:
    OpenAI, video/RTMP, vtuber/session, Daydream Scope, generic SaaS HTTP

Acceptance:

- a broker can answer "what is live now?" without leaking workload
  semantics outward
- a capability with specialized readiness checks does not require
  coordinator, resolver, or gateway code changes
- the design is demonstrated against OpenAI, video, vtuber, Daydream
  Scope, and a generic future backend family

### Phase 2 — Coordinator visibility

Components:

- `orch-coordinator/`

Deliverables:

- background polling of broker `/healthz` and `/registry/health`
- in-memory or persisted live-health cache
- roster UI badges for broker reachability and tuple readiness
- display probe freshness and normalized reason, but not workload-
  specific probe semantics
- metrics for stale or unreachable brokers

Acceptance:

- operator can distinguish:
  - "not signed"
  - "signed but live-unhealthy"
  - "signed and ready"

### Phase 3 — Resolver composition

Components:

- `service-registry-daemon/`

Deliverables:

- manifest cache remains authoritative for Layer 1
- live-health polling cache added for Layer 2
- hot-path selection filters out tuples that are:
  - degraded
  - unreachable
  - stale
  - draining for new work
- resolver metrics and logs explain why a tuple was filtered
- resolver treats probe recipes as opaque and routes only on normalized
  status and TTL

Acceptance:

- a dead broker no longer appears routable merely because its signed
  manifest still exists

### Phase 4 — Gateway failure-rate policy

Components:

- `openai-gateway/`
- other gateway components as they land

Deliverables:

- per-route observed-outcome tracking
- local circuit-breaker / weighting policy
- retry to next resolver candidate when safe
- operator-facing health view or metrics for recent route quality
- no capability-specific health code branches outside gateway-local
  failure-rate policy

Acceptance:

- a route can be temporarily skipped because of repeated real-request
  failures even when broker probes remain green

### Phase 5 — Cross-stack validation

Components:

- `capability-broker/`
- `orch-coordinator/`
- `service-registry-daemon/`
- `openai-gateway/`
- `video-gateway/`
- `vtuber-gateway/`
- `daydream-gateway/`

Deliverables:

- compose or integration test covering:
  - signed route healthy
  - broker process down
  - backend for one tuple down while broker stays up
  - draining tuple
  - shallow health green but repeated request failures
- gateway-family-specific fault injection covering:
  - OpenAI request-response and stream fallback
  - video live RTMP session-open fallback
  - video VOD / ABR worker-submit fallback
  - vtuber worker start / topup / stop degradation
  - Daydream session-open fallback and cooldown reuse on later sessions
- resolver wire coverage proving actual unix-socket gRPC selection
  composes with live broker `/registry/health` responses, including
  route recovery after a stale cached red-state expires
- docs and operator runbooks updated together

Acceptance:

- the stack demonstrates distinct behavior for Layer 1, Layer 2, and
  Layer 3 failures

### Phase 5.1 — Fault-injection matrix

The validation bar is not met by a single OpenAI path. The following
matrix must be exercised across the active gateway families.

| Gateway family | Injected failure | Expected Layer | Expected behavior |
|---|---|---|---|
| OpenAI | manifest valid, broker `/registry/health` red for one tuple | 2 | resolver excludes tuple before request dispatch |
| OpenAI | broker health green, repeated 5xx on real completions | 3 | first request retries next route; later requests cool down failing route |
| Video live | broker session-open returns 5xx | 3 | gateway records failure, retries next route if available, operator surface shows cooled route |
| Video VOD / ABR | worker submit returns 5xx or malformed job response | 3 | gateway marks route failed, asset/job enters errored path, later route choice avoids cooled route |
| VTuber | worker `startSession` fails | 3 | gateway records node failure locally and in node-health, later selects another worker when available |
| VTuber | worker `topupSession` fails | 3 | session remains visible, topup returns failure, route health and node-health both reflect degradation |
| VTuber | worker `stopSession` fails | 3 | best-effort close path records failure without hiding Layer 1 declaration |
| Daydream Scope | broker session-open returns 5xx | 3 | gateway retries another orch now and cools the failing one for subsequent sessions |
| Any gateway | broker process unreachable | 2 | resolver or direct selection path does not present route as live-ready |
| Any gateway | tuple removed from signed manifest but backend still up | 1 | route never returned even if live health stays green |
| Any gateway | tuple marked `draining` | 2 | new requests avoid the route while existing sessions can continue |
| Any gateway | broker health snapshot stale | 2 | route is treated unavailable until refreshed |

## 7. Concrete checks by component

| Check | capability-broker | orch-coordinator | service-registry-daemon | gateway |
|---|---|---|---|---|
| tuple exists in signed manifest | no | display-only context | yes | consumes resolver result |
| broker process alive | source via `/healthz` | polls | may poll or infer from health endpoint reachability | no |
| tuple backend live | source via `/registry/health` | polls and displays | polls and filters | consumes resolver result |
| tuple probe freshness | source | displays | filters on TTL | no |
| probe recipe semantics | source | opaque | opaque | opaque |
| recent request failure-rate | metrics source | dashboard only | no | yes |
| final route selection | no | no | candidate filtering only | yes |
| sign-cycle mutation | no | operator-driven only | no | no |

## 8. Acceptance criteria

This plan is complete when the stack can demonstrate all of the
following:

1. A tuple present in the signed manifest but currently red in
   `/registry/health` is hidden from resolver-backed route selection
   without requiring a new signed manifest.
2. A tuple absent from the signed manifest is never returned by the
   resolver even if the broker reports it as healthy.
3. The coordinator UI clearly distinguishes operator declaration from
   live readiness.
4. The gateway can skip a recently failing route while keeping other
   signed-and-live routes active.
5. Metrics and logs make it obvious which layer caused a route to be
   excluded.
6. The same routing contract is shown to work for OpenAI, video,
   vtuber, Daydream Scope, and at least one generic future-style
   backend.
7. Operator/debug surfaces expose Layer 3 route state for every active
   gateway family, not only OpenAI.

## 9. Risks

- If the broker health surface is too shallow, Layer 2 will stay green
  while real traffic fails; Layer 3 must still exist.
- If specialized checks leak out of the broker, every new capability will
  force resolver and gateway changes, violating workload-agnosticism.
- If the resolver TTL is too long, gateways will route to stale health.
- If the resolver TTL is too short, transient probe failures will cause
  noisy flapping.
- If the coordinator starts mutating manifests from live health, the
  operator sign cycle becomes meaningless.
- If the gateway bypasses resolver filtering, unsigned or stale routes
  can leak back onto the hot path.

## 10. Recommended implementation order

1. freeze broker `/registry/health` semantics
2. implement coordinator polling and visibility
3. implement resolver live-health filtering
4. implement gateway local circuit-breaker policy
5. add end-to-end fault-injection tests

This order keeps each layer independently testable and avoids building a
gateway policy on top of an unstable live-health contract.

## 11. Immediate next steps

The next concrete work should follow directly from the implementation
order above. The intent is to avoid a half-integrated health story where
one gateway or one workload family gets special treatment and the rest of
the stack lags behind.

Step 1 is now broken out as active plan
[`0028-broker-health-contract.md`](./0028-broker-health-contract.md).

### Step 1 — Freeze the broker health contract

Start here. Nothing downstream should ship first.

Produce:

- `host-config.yaml` schema for per-tuple `health.probe`
- broker-side probe recipe interface
- `/registry/health` response schema
- normalized status definitions and transitions
- freshness, timeout, and consecutive-failure semantics

This is the contract every other component depends on.

### Step 2 — Prove the probe model across workload families

Before resolver or gateway routing changes ship, validate the probe model
against the representative workload set:

- OpenAI-compatible HTTP
- video / RTMP
- VTuber
- Daydream Scope
- generic SaaS / HTTP backend

The goal is not to ship every possible recipe first. The goal is to prove
the abstraction is correct and does not leak workload-specific semantics
out of the broker.

### Step 3 — Surface Layer 2 in orch-coordinator

Once the broker contract is stable:

- poll `/healthz` and `/registry/health`
- cache live-health snapshots
- expose roster status and freshness
- make the operator-facing distinction clear:
  - not signed
  - signed but unhealthy
  - signed and ready

This phase is operationally useful and low-risk because it does not yet
change route selection.

### Step 4 — Make resolver selection health-aware

Only after the live-health contract and coordinator visibility are stable:

- add short-TTL live-health polling to `service-registry-daemon`
- filter manifest-valid tuples on normalized live status
- fail closed on stale health beyond policy TTL
- emit logs and metrics explaining filtered routes

This is the first place where customer-visible routing changes.

### Step 5 — Add gateway-local failure-rate policy

After Layer 2 filtering is in place:

- implement route outcome tracking in gateways
- add retry / skip / down-weight behavior
- make the policy reusable across gateways rather than OpenAI-only

The first implementation may land in `openai-gateway`, but the design
target is all gateways, not one gateway.

### Step 6 — Run cross-stack fault-injection validation

Before calling this work complete, exercise:

- broker process down
- tuple-specific backend failure
- operator drain
- stale health cache
- shallow probe green but real requests failing
- representative cases from each workload family

## 12. Robustness bar

This plan is intentionally broader than "make OpenAI routing better."
The execution bar is:

- every gateway routes on the same layered-health contract
- every worker / runner family reports into that contract through the broker
- every new capability either reuses an existing probe recipe or adds a
  broker-local one without forcing resolver or gateway redesign

### 12.1 What "robust" means here

A robust implementation must satisfy all of the following:

1. **Cross-gateway consistency**
   - OpenAI, video, vtuber, Daydream Scope, and future gateways consume
     the same Layer 1 / Layer 2 contract
   - gateway-local differences are limited to Layer 3 policy and
     workload-specific customer UX

2. **Cross-workload extensibility**
   - one workload family needing a smarter readiness check must not
     require code changes in every other module
   - specialized semantics must stop at the broker boundary

3. **Failure isolation**
   - a broken probe recipe or unhealthy tuple does not poison unrelated
     tuples on the same broker
   - a dead broker does not force manifest churn
   - a stale manifest does not become routable just because live health is green

4. **Operator clarity**
   - operators can tell whether a tuple is missing because it is unsigned,
     unhealthy, draining, stale, or recently failing under real traffic

5. **Deterministic routing behavior**
   - given the same manifest set, live-health cache, and gateway failure
     window, route selection behavior should be explainable and reproducible

### 12.2 What would count as half-baked

The following outcomes are explicitly not acceptable:

- only `openai-gateway` understands the new health model
- resolver filters on probe-specific semantics instead of normalized states
- coordinator silently edits manifests based on transient health
- one workload family gets bespoke routing logic outside the broker
- new capabilities require touching resolver and gateway code for basic
  readiness behavior
- no end-to-end tests exist outside the OpenAI flow

## 13. Completion standard

This plan should not be considered complete until all of these are true:

1. The broker health contract is documented and implemented.
2. The probe model is validated against OpenAI, video, VTuber,
   Daydream Scope, and a generic future-style backend.
3. `orch-coordinator` surfaces Layer 2 clearly for operators.
4. `service-registry-daemon` filters on Layer 1 + Layer 2 generically.
5. At least one gateway implements Layer 3 policy in a way that is
   reusable by other gateways.
6. Cross-stack tests cover the major fault modes and multiple workload
   families.
7. The docs and runbooks explain where each check lives and why.

## 14. Guidance for future gateways and workers

When a new gateway or new runner/worker family lands, the expected path is:

1. determine whether the workload can reuse an existing interaction mode
2. determine whether the workload can reuse an existing broker probe recipe
3. if not, add a new broker-local probe recipe
4. keep `/registry/health` output normalized
5. do not add workload-specific health logic to coordinator, resolver, or
   other gateways unless a broader architectural change is being made

That is how the stack scales to N future possibilities without
accumulating routing logic in the wrong places.

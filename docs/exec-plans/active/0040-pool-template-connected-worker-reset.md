---
plan: 0040
title: Pool template onboarding and connected-worker reset
status: active
phase: planning
opened: 2026-06-03
owner: harness
related:
  - "completed plan 0029 — pool node design"
  - "completed plan 0030 — pool backend scoring and selection"
  - "completed plan 0033 — pool control plane onboarding and assignment"
  - "active plan 0031 — pool follow-up backlog"
---

# Plan 0040 — Pool template onboarding and connected-worker reset

## 1. Purpose

Replace the current Pool member/backend onboarding and broker-render flow with
a simpler product model:

- the Pool remains one orch identity externally
- Pool members do not run `capability-broker` or `payment-daemon`
- a member signs up, proves payout address ownership, downloads a host bundle,
  and runs `docker compose up`
- member hosts connect outbound to the Pool broker; members do not expose DNS,
  TLS, or inbound backend ports
- the Pool operator controls which workload templates each GPU may run
- routing/scoring happens per hardware unit and template assignment
- payouts are automated native ETH transfers on Arbitrum after operator
  approval of a frozen settlement batch

Backwards compatibility with the existing Pool join-request, backend URL,
assignment, and rendered per-member broker config flow is not required. Keep
what is salvageable, but treat this as the replacement direction.

## 2. What stays

Preserve these existing decisions and implementation assets where they fit:

- one Pool orch identity externally
- no member-side broker or member-side payment daemon
- idempotent work receipts
- deterministic payout rows
- real-job performance as a routing input
- operator-visible audit history
- the embedded `pool-controller` admin UI/UX approach:
  - server-rendered shell
  - simple sidebar navigation
  - same-origin authenticated admin API calls
  - dense operational cards/tables/forms
  - no separate frontend build pipeline

The UI screens and navigation should change to match the new model. The visual
and operational style should not be thrown away.

## 3. What gets replaced

The normal production path should no longer depend on:

- member-submitted backend URLs
- member-managed public DNS/TLS/firewall setup
- `members -> backends -> assignments` as the operator's primary object graph
- `pool-controller` rendering one broker backend entry per member assignment
- broker runtime apply/re-sign cycles for ordinary member churn
- operator hand-wiring a backend into an offer before the member can be tested

Offer and price changes still require the normal manifest publication/signing
cycle. Member availability and assignment churn should not.

## 4. Target domain model

### 4.1 Identity hierarchy

```text
Member
  eth_address
  display_name/contact
  payout identity

HostEnrollment
  enrollment_id
  member_eth_address
  host_label
  enrollment_token
  broker_session_credential
  status
  last_seen_at

HardwareUnit
  hardware_unit_id
  enrollment_id
  gpu_uuid
  gpu_model
  vram_bytes
  driver/runtime facts
  state

TemplateAssignment
  assignment_id
  hardware_unit_id
  template_id
  role: primary | secondary
  state
  max_in_flight
  share policy
  certification state
```

One ETH address may own many host enrollments. One host may expose many GPUs.
Each GPU UUID is globally unique in the Pool.

### 4.2 GPU uniqueness

Rules:

- same ETH address + same GPU UUID updates/re-certifies the existing hardware
  unit
- different ETH address + same GPU UUID blocks activation by default
- operator override is allowed only with an audit reason
- an override is treated as a hardware transfer: the previous binding is
  suspended/retired, and the new binding must re-certify
- missing GPU UUID blocks GPU workload activation

For v1, NVIDIA GPU UUID from `nvidia-smi` is acceptable as pragmatic
trust-but-verify identity. Strong hardware attestation is deferred.
This blocks honest or casual duplicate enrollment, not a malicious agent that
spoofs hardware facts. Operator workflows and audit trails must not treat GPU
UUID uniqueness as cryptographic proof of physical hardware ownership.

### 4.3 Template catalog

`pool-controller` owns the template catalog and offer catalog. The broker syncs
active offers and dynamic eligible worker assignments from the controller.

Initial v1 template families:

| Template family | Initial hardware policy |
|---|---|
| `video:transcode.abr` / VOD transcode | GTX 1080 / RTX 2080 / RTX 3090 / RTX 4090 / RTX 5090-class GPUs |
| `openai:embeddings` | RTX 3090 / RTX 4090 / RTX 5090-class GPUs |
| `openai:chat-completions` | RTX 4090 / RTX 5090 by default; RTX 3090 only for operator-approved small or quantized models |
| `openai:audio-transcriptions` | RTX 3090 baseline; RTX 2080 possible only when dedicated; stackable on RTX 4090 / RTX 5090 when policy allows |
| `image-generation` | RTX 4090 / RTX 5090-class GPUs |

`rerank` remains later.

The operator controls template eligibility and assignment. Members cannot
self-switch into higher-demand templates.

### 4.4 Stacking policy

A hardware unit has one primary template. It may have zero or more secondary
templates only when the operator's stacking policy explicitly allows the exact
combination for that GPU class.

Default stance:

- GTX 1080 / RTX 2080-class: one template only
- RTX 3090-class: one template by default; audio can be dedicated; small chat
  requires operator override
- RTX 4090 / RTX 5090-class: one primary template; optional secondary
  low-footprint templates, such as audio transcription, when operator policy
  allows

Stacking is capacity-controlled by the Pool, not chosen by the member.

## 5. Signup and bundle flow

### 5.1 Member signup

Normal member flow:

1. Member opens signup page.
2. Member enters ETH payout address.
3. Pool issues a nonce.
4. Member signs nonce with that ETH address.
5. Pool verifies signature.
6. Pool creates or updates the member identity.
7. Member downloads a host enrollment bundle.
8. Member runs `docker compose up`.

The runner bundle never contains or needs the ETH private key.

V1 member identity uses a SIWE-style EIP-191 `personal_sign` nonce flow:

- nonce is pool-issued, single-use, and expires
- successful signature verification creates a server session cookie for member
  dashboard access
- the same verified ETH address becomes the payout identity
- v1 is EOA-only unless ERC-1271 / Safe support is explicitly planned later

Member endpoints must not trust a plain `member_eth_address` field without a
verified session or enrollment credential.

### 5.2 Host bundle

One bundle is per member host/enrollment, not per GPU. A multi-GPU rig gets one
bundle and reports all visible GPUs. A member with many machines creates one
host enrollment per machine under the same ETH address.

Bundle contents:

```text
docker-compose.yaml
.env
README.md
update.sh
enrollment-token
pool-member-agent config
```

Initial services:

- `pool-member-agent`
- hardware/enrollment probe
- assigned runner containers after assignment
- optional model downloader services where the template requires local weights

V1 update behavior:

- operator changes assignment
- old assignment enters draining
- broker sends no new work to old assignment
- in-flight request/response work finishes or times out
- hardware unit enters `update_required`
- member dashboard shows the new assignment and one update command
- member runs `update.sh`
- `update.sh` calls a controller bundle endpoint authenticated by the
  enrollment token, downloads the current rendered host assignment, updates
  compose/env material, and restarts affected services
- new assignment enters certification before receiving real traffic

V1.5 can automate assignment refresh through the member agent. V1 should ship
the bundle shape that makes that possible later.

Enrollment and session credentials are live secrets on an untrusted member
host. The controller must support:

- operator-initiated and member-initiated credential rotation
- operator-initiated revocation
- retiring a host enrollment, which immediately kills live broker sessions
- leaked-token recovery by revoking the host credential and forcing a fresh
  bundle/update flow

V1 tunnel authentication may use signed bearer/session credentials, but the
credential format must support rotation and revocation from day one.

## 6. Connected-worker transport

### 6.1 Network model

Members do not expose inbound endpoints. The host agent opens one outbound
multiplexed session to the Pool broker.

```text
member host -> outbound session -> capability-broker
capability-broker -> dispatches work over the established session
```

The member does not configure DNS, public TLS certificates, router forwarding,
or public backend URLs.

The tunnel presents member-host services as broker-dialable virtual backends.
Existing mode drivers should keep using the broker's backend dial path wherever
possible; the tunnel layer adapts the member's outbound-only connectivity into
that local dial abstraction.

V1 tunnel design should be generic enough for all broker modes that use
TCP-like or HTTP-like backend dialing, but v1 acceptance tests only need to
exercise the modes required by the v1 template catalog:

- `http-reqresp@v0`
- `http-stream@v0`
- `http-multipart@v0`

WebSocket, RTMP, and other TCP-like modes should work by construction once the
virtual backend dialer is complete, but they do not gate this plan until a v1
template requires them.

`session-control-plus-media` is a carve-out: its WebRTC UDP/SRTP media plane
cannot be treated as a simple TCP stream through this tunnel. If a future Pool
template needs that mode, the repo must decide between direct worker media via
ICE/STUN/TURN or a broker-operated TURN-style relay, with explicit billing and
trust implications. That media-plane decision is deferred and is not implied by
the reverse tunnel.

Preferred transport:

- QUIC as the primary tunnel protocol, using bidirectional streams and
  per-stream flow control to avoid TCP head-of-line blocking between large
  payloads and latency-sensitive streams
- listener on standard egress-friendly ports where practical, especially
  `443/udp`
- HTTPS/WebSocket plus a stream multiplexer such as yamux as a fallback for
  networks that block UDP

The tunnel must support backpressure, cancellation, stream timeouts, and clear
per-dispatch failure reporting.

### 6.2 Component ownership

`capability-broker` owns the paid request path and terminates connected member
sessions.

`pool-controller` owns:

- signup and wallet nonce verification
- host enrollment credentials
- hardware inventory
- template and offer catalogs
- template assignment policy
- scoring state
- settlement accounting

Broker syncs from controller:

- active offer catalog
- valid host/session credentials
- hardware/template assignments
- selection weights and caps
- suspended/throttled state

Broker emits to controller:

- hardware inventory reports from connected host sessions
- idempotent work receipts
- backend outcomes
- connected-session health and capacity signals

### 6.3 Session scope

Use one multiplexed session per host enrollment. The session carries:

- heartbeat
- hardware inventory
- assignment status
- capacity/backoff signals
- work dispatches for all GPUs/templates on that host
- responses, usage, and failures

The local agent routes each job to the right runner container/GPU/template.

Tunnel-drop policy:

- request/response jobs in flight on a dropped tunnel fail fast
- broker may re-dispatch only when the request is known to be safely replayable
  and no final receipt has been emitted
- interrupted work may leave a stub receipt, but must not emit a final receipt
- long-lived sessions are not in the initial template catalog; if they are added
  later, they need explicit drain/deploy semantics

Controller-driven revocation requires a push path to the broker. Add a private
broker admin endpoint for controller-initiated session kill / credential
revocation, and keep per-dispatch credential validation or short-lived cached
validation as a defense-in-depth check.

## 7. Offer advertisement

The manifest advertises operator-enabled Pool offers. Individual member churn
does not add/remove offer tuples from the signed manifest.

Rules:

- offer/price/catalog changes still require the normal publication/signing
  cycle
- worker availability affects broker health/routing, not whether an
  operator-enabled offer exists
- if no eligible worker is available, broker returns `503` plus
  `Livepeer-Backoff`

This removes normal dependency on broker config render/apply/re-sign for
ordinary member activation, failure, or assignment changes.

## 8. Activation and certification

### 8.1 State model

Suggested state path:

```text
registered
  -> bundle_downloaded
  -> online
  -> certification_testing
  -> probationary_real_traffic
  -> active
```

Failure and operator states:

```text
throttled
suspended
update_required
retired
```

### 8.2 Certification layers

Certification probes and smoke requests must route through the broker's
virtual-backend dial path over the connected-worker tunnel. The controller owns
certification state and policy, but should not assume it can directly dial
member runner services.

Hardware/runtime certification:

- GPU UUID present
- duplicate binding check passes
- NVIDIA driver/container runtime works
- GPU model and VRAM satisfy template policy

Template health certification:

- assigned containers running
- health endpoint OK
- model/options/preset endpoint OK

Functional smoke certification:

- known request succeeds
- response shape is valid
- work-unit reporting is valid
- latency is within a loose threshold
- deterministic sanity checks only

No subjective quality scoring in v1.

Examples:

- transcode: output exists, is probeable/playable, and has expected
  renditions/codecs
- audio transcription: response has text and deterministic fixture sanity
  passes where practical
- image generation: returns a valid image with expected dimensions/format
- embeddings: vector dimensions and usage count
- chat: valid OpenAI response/stream usage

### 8.3 Probation

No real traffic is routed before certification passes.

After certification, an assignment receives tightly capped real paid traffic
for one full Livepeer round. If the round completes without serious failures,
the assignment becomes active.

Suggested probation caps:

- max share cap: 1-5%
- max in-flight: 1
- no secondary templates unless explicitly enabled by operator policy

Failures throttle immediately at any stage.

## 9. Capacity and routing

Capacity is internal Pool state and is never advertised publicly.

For each `hardware_unit + template_assignment`, the Pool tracks:

- `max_in_flight`
- queue limit, usually zero or very small
- backoff cooldown
- probation cap
- share policy

Broker behavior:

- enforce local in-flight cap before selecting a worker
- route over connected member session
- honor member-side saturation signals such as `503 + Livepeer-Backoff`
- treat saturation/backoff differently from bad output

Outcome policy:

- `503 + Livepeer-Backoff`: temporary cooldown or lower weight
- timeout or backend 5xx after accepting work: failure, hurts score more
- invalid output: serious failure, may suspend
- successful but slow: lowers performance score gradually

## 10. Scoring and fair distribution

Score is per `hardware_unit + template_assignment`. It affects routing only.
Payouts are based on attributed revenue from accepted final receipts. Accepted
work units are retained for audit, dashboard display, and consistency checks.

Inputs:

- certification status
- recent success rate
- backend failure rate
- latency against template target
- saturation/backoff behavior
- probation/active/throttled state
- operator caps/suspensions

The score should be product policy, not a large set of operator-facing math
knobs.

### 10.1 Adaptive distribution

Fair distribution should grow stricter as Pool supply grows.

Policy:

- probationary assignments are always tightly capped
- when active supply is small, prioritize availability and basic fairness
- as active supply grows, apply stronger per-hardware and per-member share
  pressure per offering
- use weighted randomness, not winner-take-all selection
- reserve a small exploration budget for active, healthy, under-sampled
  assignments

Suggested exploration share: 5-10% of eligible traffic per offering.

Never route exploration traffic to uncertified, suspended, or currently failing
assignments.

## 11. Settlement and payouts

### 11.1 Settlement windows

Use fixed Livepeer-round windows.

Default:

```text
settlement_window_length_rounds = 14
window_id = floor(round_id / 14)
```

Close only after all rounds in the window are complete.

Per-round close artifacts survive. `pool-reconciler` remains the component that
consumes `protocol-daemon` round events, closes individual Livepeer rounds, and
forwards round/window progress to `pool-controller`. The 14-round settlement
window aggregates already-closed rounds; `pool-controller` does not need its
own protocol-daemon feed in v1.

### 11.2 Per-offering settlement

Do not compare mixed work units globally. Settle independently per offering.

Attributed revenue on final receipts is the authoritative settlement basis.
Accepted work units are retained for audit, dashboard display, and consistency
checks. This avoids incorrect payouts when price changes or multiple price tiers
exist inside the same offering window.

Window close must still reconcile receipt-attributed revenue against
`payment-daemon` confirmed revenue. Receipt attribution determines each
member's relative share; confirmed payment revenue bounds the distributable pot.

Confirmed revenue is winning-ticket redemption face value and exists only as a
round-global number (`payment-daemon` `GetRoundRevenue`); it cannot be
attributed per offering. The reconciliation therefore applies at the
window-global level and is pro-rated across offerings:

```text
window_confirmed_revenue = sum(payment_daemon.GetRoundRevenue(r) for r in window)
window_attributed_revenue = sum(final_receipt.attributed_revenue_wei for window)
scale = min(1, window_confirmed_revenue / window_attributed_revenue)
```

For each offering:

```text
member_attributed_revenue = sum(final_receipt.attributed_revenue_wei for member/offering/window)
offering_attributed_revenue = sum(final_receipt.attributed_revenue_wei for offering/window)
settlement_revenue = offering_attributed_revenue * scale
distributable_revenue = settlement_revenue - pool_commission
member_share = member_attributed_revenue / offering_attributed_revenue
member_payout = distributable_revenue * member_share
```

If confirmed revenue is lower than attributed revenue, payouts scale down
proportionally and an anomaly is recorded. If confirmed revenue is higher, the
surplus accrues to the Pool operator rather than inflating member shares.
Redemption is probabilistic per round; aggregating over the 14-round window is
what makes `confirmed ≈ attributed` a meaningful invariant to alert on.

The final payout row per member is the sum of that member's line items across
offerings in the window.

Pool commission is per offering, with an optional global default. This changes
round/window accounting from today's round-global cut into offering-scoped line
items.

Receipts must carry enough data to support this:

```text
round_id
capability_id
offering_id
member_eth_address
host_enrollment_id
hardware_unit_id / gpu_uuid
template_id
accepted_work_units
attributed_revenue_wei
status=final
```

### 11.3 Approval-gated automated ETH payouts

Normal payout flow:

1. 14-round window closes.
2. System computes a frozen payout batch.
3. Batch status becomes `pending_approval`.
4. Operator reviews totals and anomalies.
5. Operator approves the batch.
6. Executor automatically submits native ETH payouts on Arbitrum.
7. System tracks `submitted`, `paid`, and `failed`.

Approved batches are immutable. Late corrections become adjustment rows in a
later settlement window.

Immutability applies to financial facts: amount, recipient, settlement window,
and line-item composition. Technical retries inside the same approved batch are
allowed when the amount and destination do not change, for example after RPC,
gas, nonce, or dropped-transaction failures. A technical retry updates execution
metadata and status, not the approved financial batch.

The operator should not normally issue payments manually. Export remains only
for inspection, audit, and recovery.

Bonded LPT payout rails are explicitly tabled for a separate future decision.

## 12. UI/UX target

Keep the current embedded admin-console approach, but replace the information
architecture.

### 12.1 Operator navigation

Suggested operator pages:

- Overview
- Members
- Host enrollments
- Hardware inventory
- Template catalog
- Assignments
- Certification
- Routing health
- Settlement windows
- Payout approvals
- Audit

Retire or repurpose old pages:

- `Join requests` becomes signup/enrollment review
- `Members & backends` becomes members/hosts/hardware
- `Assignments` becomes hardware/template assignments
- `Broker runtime` is no longer the normal member convergence path

### 12.2 Operator dashboard

Show:

- members, hosts, and hardware units by state
- duplicate GPU UUID alerts
- template assignments and stacking-policy validation
- certification queue/results
- probationary assignments and activation round countdown
- active supply by offering
- throttled/suspended/update-required counts
- under-sampled healthy assignments
- share distribution
- saturation/backoff events
- rule-based recommendations
- current 14-round settlement progress
- estimated revenue/payouts
- frozen batches pending approval
- submitted/paid/failed payout status

Recommendations should be simple and rule-based in v1:

- move eligible RTX 4090/5090-class GPUs to high-demand image/chat templates
- move older GPUs to transcode/ABR
- add audio transcription as a secondary template only when policy allows
- reduce routing to saturated assignments
- investigate failing hardware

### 12.3 Member dashboard

Show:

- ETH payout address
- registered hosts and GPUs
- assigned primary and secondary templates
- current state: online, testing, probationary, active, throttled, suspended,
  update_required
- clear reason when not receiving work
- last certification result
- update-required banner and command
- jobs processed this settlement window
- accepted work units this settlement window
- success/failure counts
- recent latency band
- recent backoff/saturation events
- current settlement window ID
- completed rounds / 14
- window completion percentage
- estimated payout so far, clearly marked provisional
- estimate by offering
- prior payout batches and transaction hashes

Do not expose the exact scoring formula or global ranking.

## 13. Implementation phases

### Phase 1 — Model and docs reset

- Add/update cross-cutting design docs for the new Pool model.
- Mark old member/backend/rendered-config flow as superseded by this plan.
- Define controller data types for members, host enrollments, hardware units,
  template catalog, template assignments, certification runs, and settlement
  windows.
- Preserve receipt and payout concepts where useful.

### Phase 2 — Template catalog and hardware inventory

- Implement template catalog persistence and admin APIs.
- Implement hardware inventory persistence and duplicate GPU detection.
- Add stacking-policy validation.
- Add operator UI pages for template catalog and hardware inventory.

### Phase 3 — Signup and bundle generation

- Implement nonce issuance and SIWE-style EIP-191 ETH signature verification.
- Implement host enrollment creation and token issuance.
- Generate host bundle zip with compose, env, token, README, update script, and
  member-agent config.
- Add authenticated bundle refresh endpoint for `update.sh`.
- Add credential rotation and revocation APIs.
- Add member dashboard shell and enrollment status views.

### Phase 4 — Member agent and connected broker sessions

- Add `pool-member-agent` component or subcomponent.
- Add QUIC-first outbound reverse tunnel protocol with WebSocket/multiplexer
  fallback for UDP-blocked networks.
- Terminate sessions in `capability-broker`.
- Authenticate sessions with controller-issued credentials.
- Add controller-triggered broker admin endpoint for session kill and
  credential revocation.
- Report heartbeat, inventory, assignment status, capacity, and outcomes.

### Phase 5 — Dynamic offer sync and routing

- Move broker member routing from rendered static backends to dynamic connected
  worker assignments.
- Keep static config for identity, payment daemon, controller URL, and broker
  session listener settings.
- Sync offer catalog and routing state from controller.
- Route paid work through broker-dialable virtual backends backed by connected
  worker sessions.

### Phase 6 — Certification and probation

- Implement hardware/runtime certification.
- Implement template health checks.
- Implement functional smoke checks for v1 templates.
- Implement one-round probation with tight caps.
- Add certification/probation UI.

### Phase 7 — Scoring, capacity, and distribution

- Implement per-assignment score using product-level inputs.
- Enforce local in-flight capacity.
- Process backoff/saturation signals.
- Add adaptive distribution and exploration budget.
- Add operator recommendations.

### Phase 8 — Settlement and approval-gated payouts

- Implement 14-round fixed settlement windows.
- Extend `pool-reconciler` to keep per-round closes and aggregate closed rounds
  into settlement windows.
- Settle independently per offering using attributed revenue as authoritative.
- Reconcile window-attributed revenue against `payment-daemon` confirmed
  revenue at window close and scale the distributable pot accordingly.
- Generate immutable frozen payout batches.
- Add operator approval UI/API.
- Keep automated native ETH payout execution on Arbitrum.
- Preserve audit/export surfaces for recovery.

### Phase 9 — UI replacement and old-surface retirement

- Replace old pages/navigation with the new operator/member surfaces.
- Remove normal-path references to backend URLs, manual assignments, and broker
  runtime apply for member churn.
- Keep debug/break-glass tools only if still useful and clearly labeled.

## 14. Open implementation questions

- Whether `pool-member-agent` should be a new top-level component or live under
  `pool-controller` / `capability-broker` initially.
- Exact v1 model IDs and container images per template.
- Whether Intel/AMD video GPUs stay in template policy now or after NVIDIA v1.
- How much existing payout-executor code can be reused after payout batches
  become settlement-window scoped.
- Whether ERC-1271 / Safe wallet support is needed before or after EOA-only v1.
- Which non-template modes, if any, need explicit tunnel acceptance tests before
  a template family depends on them.

## 15. Exit criteria

This plan is complete when:

- a member can sign up, download a host bundle, and connect outbound without
  exposing an inbound service
- the Pool detects and records all visible NVIDIA GPU UUIDs on that host
- duplicate GPU UUID enforcement works with audited operator transfer override
- the operator can assign allowed templates per GPU, including allowed stacks
- certification gates real traffic
- probation lasts one full Livepeer round with tight caps
- broker routes paid work over connected member sessions
- idempotent final receipts include member, host, hardware, template, offering,
  units, and revenue attribution
- a 14-round settlement window creates a frozen payout batch
- operator approval triggers automated native ETH payout execution
- member and operator dashboards expose the new lifecycle, performance, and
  payout-window status
- normal member churn does not require broker backend config rendering or
  manifest re-signing

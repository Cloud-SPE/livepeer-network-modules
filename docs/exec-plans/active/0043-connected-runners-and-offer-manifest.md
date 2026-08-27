---
plan: 0043
title: Connected runners and the offer-only manifest pipeline
status: active
phase: shipped
opened: 2026-08-26
owner: harness
related:
  - "active plan 0040 — pool template onboarding and connected-worker reset (§6–8 superseded in part; see §7)"
  - "active plan 0042 — automated manifest sign cycle (unchanged mechanics; policy amended in §3.7)"
  - "active plan 0037 — operator console UI alignment (coordinator pages land in its shell)"
  - "docs/design-docs/runner-declared-capabilities.md (superseded by this plan once shipped)"
  - "docs/design-docs/trust-model.md"
  - "docs/design-docs/pool-overlay-flows.md"
audience: broker / coordinator / console / registry / pool maintainers, trust-model reviewers
---

# Plan 0043 — Connected runners and the offer-only manifest pipeline

**Status:** shipped 2026-08-27 — every item in §5 landed; beads
`lnm-pkv.1`–`.18`. `lnm-sk7` followed on 2026-08-27: the conformance
suite now attaches its own runner and the broker's legacy
`capabilities[]` grammar is deleted, along with the HTTP health prober
it fed — `/registry/health` keeps its contract but is now computed from
the offer set and the attach tunnels (§3.4). One follow-up remains
split out: `lnm-za5`, the coordinator's now-inert broker
metadata-discovery plumbing. The legacy worker tunnel that served
`worker://` backend URLs is inert but not yet removed, because
`pool-controller` still speaks its admin surface — tracked as
`lnm-wyu`. One item was deliberately not done: overlay `pin[]` keeps
its current shape, because the declared job/session axes it would gain
are dropped by the envelope's node projection and no consumer reads
them (§3.8).

Operators upgrading: see
[`../../design-docs/migrating-to-connected-runners.md`](../../design-docs/migrating-to-connected-runners.md).
There is no backward compatibility.

This is epic 1 of two; epic 2 (pool onboarding simplification) builds
on §7.

## 1. Purpose

Today an orchestrator operator hand-authors, per backend, eight facts that
only the runner knows (capability id, protocol, transports, work unit and
extractor, runner paths, readiness recipe, model identity), keeps prices
byte-identical across duplicate tuples, has `pool-controller` re-render the
whole broker file on every member change, and runs a sign cycle whose held
queue fills with changes no human decided. The same tuple is restated five
times between the runner and the signed manifest (host-config → brokerrender
mirror → broker → coordinator with a hardcoded `spec_version` → console).

This plan collapses that to:

> **The operator authors offers (what is sold, at what price, with what
> capacity, where) and the sign policy. Nothing else.** Runners attach to the
> broker dynamically and declare what they are. A runner can never change the
> manifest; only the operator can.

Backward compatibility with the current `capabilities[].backend` grammar,
the pool render/apply path, and the registry daemon's v3.0.1 manifest is
**not** required (core belief #14; consistent with the interaction-mode
reset).

## 2. Decisions (locked)

| # | Decision | Chosen |
|---|---|---|
| 1 | Target statement | Operator authors offers + sign policy only. |
| 2 | Runners are dynamic | Every runner attaches **outbound** with a credential and self-describes. `host-config` carries no runner URLs or runner facts. One attach path for pool members and the orch's own hardware ("a pool of one"). |
| 3 | Freeze rule | Runner-shaped tuple fields are frozen into an offer from the **first certified runner**. Later runners are matched, never adopted. A runner whose description disagrees is *ineligible*, not a manifest change. **An offer with no certified runner is not advertised.** |
| 4 | Runner contract | One versioned attach document: host level + capability level, explicit required/optional fields, namespaced `x-*` extension space. `x-*` is relay-only unless the offer lists the key in `extra_from_runner`. |
| 5 | Attach security | Bearer credentials in a broker-side sealed **credential store**, per host enrollment, with expiry, rotation, revocation (delete + kill). Credential kind is pluggable; the upgrade path to per-host keypairs is a documented deliverable. |
| 6a | Hot-zone console | `orch-coordinator` hosts the operator pages (Runners, Offers, Enroll host, Certification) over a documented broker admin API. First freeze is automatic — **the signature is the acceptance**; superseding a frozen shape is an explicit coordinator gesture, then sign. |
| 6b | Certification | **Execution in the broker** as a generic step engine (`readiness`, `request`, `usage`, `latency`). **Steps authored by the offer/template author only**; a runner may suggest steps (`x-certification-suggested`), never self-certify. Pool-controller keeps the probation/active/share-cap ladder and feeds selection weights. |
| 7 | Versions | Protocol module exports `VERSION` as a Go constant; coordinator imports it; broker stamps it on `/registry/offerings`; mixed majors refused. Renewal threshold published by the coordinator in candidate metadata; the console's policy field is removed. `spec_version` change reclassified *forbidden → critical* with a typed-version confirm gesture; `eth_address` change stays forbidden. |
| 8 | Registry schema | The protocol `manifest/schema.json` envelope is the **only** manifest. Registry daemon v3.0.1 schema, `Publisher.Build/Sign/BuildAndSign`, and `ProbeWorker` are deleted (hard cut). Overlay `pin[]` moves to the protocol tuple shape. |

## 3. Target model

### 3.1 Operator config — offers only

```yaml
identity: { orch_eth_address: 0x1234…5678, label: broker-a }
listen: { paid: ":8080", metrics: ":9090", worker_quic: ":8443" }
payment_daemon: { socket: /var/run/livepeer/payment-daemon.sock }
credential_store: { path: /var/lib/livepeer/broker/credentials.db, sealing_key_file: … }

offers:
  - offering_id: llama-3-70b-shared
    capability: openai:chat-completions
    match: { identity.openai.model: llama-3-70b }      # selector over attached runners
    price: { amount_wei: "210000000", per_units: 1 }
    capacity: { max_in_flight: 4, queue_limit: 8 }
    extra: { region: us-west-2, gpu_class: h100 }
    extra_from_runner: [x-quantization]                 # optional promotion of x-* keys
    certification:                                      # authored here (standalone) or by the template (pool)
      - { name: ready,   type: readiness, required: true }
      - { name: smoke,   type: request,   required: true,
          config: { body: {model: llama-3-70b, messages: [{role: user, content: "ping"}]},
                    expect_status: 200, assert: ["$.choices[0].message.content"] } }
      - { name: usage,   type: usage,     required: true }
      - { name: latency, type: latency,   config: { samples: 3, p50_max_ms: 4000 } }
```

`capabilities[]`, `backend{}`, `job{}`, `session{}`, `work_unit{}`,
`health.probe{}` and `pool_snapshot` render targets are **removed** from the
grammar. Session commercial axes (`lease_*`, `refill`, `min_runway_units`,
`tolerance_band_pct`, `runway_increment_units`, `max_rotations`) remain
operator-owned and live under `offers[].session_policy`.

The pool emits the same `offers[]` — pushed to the broker by
`pool-controller` over the admin API, not rendered to a file.

### 3.2 Runner attach contract (protocol deliverable)

One document, sent on attach and re-sent on reconnect or on change,
versioned by `contract_version`.

**Host level**

| Field | Req | Validated against |
|---|---|---|
| `contract_version` | ✔ | broker's supported set; unknown major → reject |
| `credential` | ✔ | credential store; binds the document to an enrollment |
| `host_id`, `agent_version` | ✔ | free; audit |
| `hardware[]` `{gpu_uuid, gpu_model, vram_bytes, driver, cuda}` | ✔ for GPU work | GPU-uniqueness rule (0040 §4.2); offer/template `requirements` |
| `hardware[].facts{}` | opt | opaque string map; UI only |

**Capability level** (one entry per capability the host can serve)

| Field | Req | Validated against |
|---|---|---|
| `capability_id`, `protocol` | ✔ | protocol must be known; capability id is opaque (workload-agnostic) |
| `transports[]` (paid-job) / `descriptor_schemas[]` (paid-session) | ✔ | protocol enum; **frozen** |
| `work_unit.name` | ✔ | **frozen** |
| `work_unit.extractor` (paid-job) | ✔ | must name a broker-known extractor type; runner never supplies code; **frozen** |
| `paths{}` | ✔ | relative; `invoke`/`options` (job) or `create`/`status`/`terminate` (session) |
| `readiness{}` | ✔ | broker-known probe types |
| `identity{}` (e.g. `openai.model`, `provider`) | ✔ | matched by `offers[].match`; **frozen** into `extra` |
| `schema_versions{}` | ✔ | protocol module's supported set at attach |
| `metering`, `heartbeat`, `session_params_schema` | opt | as `paid-session` §7.1.1 today |
| `requirements{}` `{gpu_vram_min_bytes, gpu_models[]}` | opt | this host's `hardware[]` |
| `x-certification-suggested[]` | opt | shown in UI; never adopted |
| `x-*` | custom | relayed verbatim; promoted to `extra` only via `extra_from_runner` |

Rules: unknown non-`x-` field → reject document; invalid value → reject
*that capability* stating the field and both sides; a changed document
against an already-frozen offer → runner ineligible for that offer, never a
manifest mutation. Supersedes `paid-session` §7.1.1.

### 3.3 Attach security

- **Credential store** in the broker (sealed on disk like the session store).
  Pool: synced from `pool-controller`. Standalone: `POST /admin/v1/enroll`
  mints a credential and returns the same bundle the pool issues.
- Scope: one credential = one host enrollment; it grants *attach*, not
  eligibility.
- Lifecycle: `expires_at`; agent re-enrolls before expiry; operator
  force-rotate; revoke = delete from store **and** kill the session; a
  revoked credential cannot re-attach. Controller-initiated kill path stays.
- Blast radius (stated): a stolen bearer attaches *as that member*; it cannot
  set prices or touch the manifest; certification gates work; receipts credit
  the legitimate payout address; revoke ends it. Acceptable for v1.
- **Keypair path (documented, not built):** credential kind `ed25519` where
  the agent generates a key at enrollment, the store holds the public key,
  and the attach document is signed (or QUIC client-cert). The store schema
  carries `kind` from day one so this is additive.

### 3.4 Freeze, eligibility, advertisement

```
attached ──describe valid──▶ matched(offer) ──certified──▶ eligible ──▶ selected
    │                             │                              │
    └─ invalid → rejected         └─ shape ≠ frozen → ineligible └─ recertify on change
```

- First certified runner for an offer **freezes** `transports/descriptor_schemas`,
  `work_unit`, `identity` (→ `extra`), and `schema_versions` into the offer.
  The candidate changes; the cold console holds it; the signature is the
  acceptance.
- `/registry/offerings` publishes only offers with a frozen shape **and** at
  least one certified runner at the time of freeze; runner churn afterwards
  is health/routing (`503` + `Livepeer-Backoff` when none), never a manifest
  change (0040 §7 preserved).
- Superseding a frozen shape is explicit (`POST /admin/v1/offers/{id}/accept-shape`
  from the coordinator console); old-shape runners become ineligible.

### 3.5 Certification engine (broker)

Step types, all workload-agnostic: `readiness`, `request` (fixture body —
JSON, multipart, or stream — expected status, jsonpath assertions), `usage`
(the offer's frozen extractor yields > 0), `latency` (p50 of N under a bound).
Runs through the tunnel; results stored per runner × offer; state machine
`attached → certified → eligible | ineligible`, `recertify` on runner change
or operator request. The controller's hardcoded probe families
(`probes.go`) become step *config* shipped with pool templates.

### 3.6 Coordinator as hot-zone console

New pages in the 0037 shell: **Runners** (hosts, hardware, eligibility and
mismatch reasons), **Offers** (per broker, frozen shape, accept-shape),
**Enroll host** (standalone credential + bundle), **Certification**
(results). Backed by the broker admin API contract:
`GET /admin/v1/runners`, `GET /admin/v1/offers`,
`POST /admin/v1/offers/{id}/accept-shape`, `POST /admin/v1/enroll`,
`POST /admin/v1/credentials/{id}/revoke`, `GET /admin/v1/certification`,
`POST /admin/v1/certification/{runner}/{offer}/run`. `coordinator-config
.brokers[]` gains `admin_token_ref`.

### 3.7 Versions and the sign policy

- `livepeer-network-protocol` exports `VERSION` (Go const); coordinator
  imports it; broker stamps `spec_version` on `/registry/offerings`;
  coordinator refuses to merge brokers on different majors.
- Coordinator publishes `manifest_ttl` and `renewal_threshold_seconds` in
  `metadata.json`; console reads them, drops `renewal_threshold_fraction`,
  keeps sanity bounds and the rate limiter.
- Console classifier: `spec_version` change → `critical` (held) with the
  confirm gesture "type the new version"; `orch.eth_address` change stays
  `forbidden`. Rate-limit latch gains an in-console clear (tracked debt).

### 3.8 Registry daemon

Resolver validates the protocol envelope directly; `coordinator_envelope.go`
compat branch becomes the path; v3.0.1 decoder, `manifest-schema.md`,
`signature-scheme.md`, `Publisher.{BuildManifest,SignManifest,BuildAndSign,ProbeWorker}`
deleted; overlay `pin[]` entries use the protocol tuple shape. serviceURI
modes A–D, verification, audit unchanged.

## 4. Component impact

| Component | Removed | Added |
|---|---|---|
| `livepeer-network-protocol` | `paid-session` §7.1.1 as the describe spec | `protocols/runner-attach.md` + schema; `protocols/broker-admin.md`; certification step spec; `VERSION` Go const; conformance fixtures |
| `capability-broker` | `capabilities[].backend/job/session/work_unit/health`; config-string credential check; describe polling + quarantine; `offering_metadata.go` hydration | `offers[]` grammar; credential store; attach protocol + hardware intake; freeze/eligibility; certification engine; admin API |
| `pool-member-agent` | controller `/hardware` report | builds the attach document from the container's contract or an adapter profile (`openai-compatible`, `transcode`); same bundle for pool and standalone |
| `orch-coordinator` | hardcoded `SpecVersion`; dead `WorkerURLOverride` | imported version; threshold in metadata; four console pages; per-broker admin token |
| `secure-orch-console` | `renewal_threshold_fraction` | typed-version gesture; `spec_version` critical; rate-limit clear |
| `service-registry-daemon` | v3.0.1 schema + docs; `Publisher` build/sign; `ProbeWorker` | envelope as native |
| `pool-controller` | `brokerrender`, `runtimeservice`, `brokeradmin` apply, `backendverify`, `probes` families | offer + credential push over admin API; certification *policy*; hardware relay from broker |

What stays pool-only: member identity and money (signup, receipts,
settlement, reconciler, executor), member surfaces, fairness ladder,
cross-owner GPU rules, credential authority for many owners.

## 5. Work breakdown

Beads epic `lnm-pkv` (children `lnm-pkv.1`–`.18`) holds one child per item;
dependencies as listed.

**Phase A — protocol (blocks everything)**
1. Runner attach contract spec + JSON schema (§3.2). Supersedes `paid-session` §7.1.1.
2. Broker admin API contract (§3.6) and `/registry/offerings` `spec_version` stamp.
3. Certification step-type spec (§3.5).
4. `VERSION` Go constant; manifest changelog entry; conformance fixtures for the attach contract.

**Phase B — broker**
5. `offers[]` config grammar; delete the tuple/backend grammar; validation. *(after 1)*
6. Credential store: sealed store, `enroll`/rotate/revoke admin, attach auth against store; keypair-path doc. *(after 2)*
7. Attach protocol: attach document over QUIC/WS register, hardware intake, describe for all protocols, `schema_versions` check, adapter-profile hook. *(after 1, 6)*
8. Offer freeze + eligibility + `accept-shape`; `/registry/offerings` from frozen offers only. *(after 5, 7)*
9. Certification engine + results + recertify state machine. *(after 3, 7)*
10. Selection over eligible runners; selection-weight hook fed by `pool_snapshot`; capacity from offer. *(after 8, 9)*
11. Remove describe polling, quarantine, `offering_metadata.go` hydration (folded into adapter profiles). *(after 8)*

**Phase C — agent**
12. `pool-member-agent` builds the attach document; adapter profiles `openai-compatible`, `transcode`; one bundle shape for pool and standalone. *(after 1)*

**Phase D — coordinator + consoles**
13. Coordinator: import `VERSION`, refuse mixed majors, publish threshold in metadata, delete dead override. *(after 4)*
14. Coordinator console pages: Runners, Offers/accept-shape, Enroll host, Certification; per-broker admin token. *(after 2, 8, 9)*
15. secure-orch-console: read threshold from candidate, drop policy field, `spec_version` critical + typed-version gesture, rate-limit clear. *(after 13)*

**Phase E — registry daemon**
16. Hard cut to the protocol envelope; delete v3 decoder + docs + `Publisher` build/sign + `ProbeWorker`; overlay `pin[]` tuple shape. *(after 4)*

**Phase F — pool-controller (epic-1 portion)**
17. Delete `brokerrender`/`runtimeservice`/apply; push offers and credentials over the admin API; certification policy only; hardware relay from the broker. *(after 6, 8, 9)* — everything member-facing is epic 2.

**Phase G — docs and migration**
18. Supersede `runner-declared-capabilities.md`; update `architecture-overview.md`, `trust-model.md`, `pool-overlay-flows.md`, `backend-health.md`, host-config example, operator runbooks; migration note for existing deployments (no back-compat). *(after 8, 13, 16)*

## 6. Interaction with plan 0042

Mechanics unchanged: agent pull, debounce, classify, auto-sign, held queue,
audit. Amendments: §3.7 (threshold source, `spec_version` class and
gesture). Turn phase-2 `auto_sign.benign: true` on once §3.4 ships — with
runner churn out of the manifest, benign changes are exactly the operator's
own price edits.

## 7. Seam into epic 2 (pool onboarding)

Epic 2 starts from a broker that already does attach, credentials, freeze,
certification execution, and selection weights. What remains pool-specific
for epic 2 to simplify: signup and bundle, template catalog as
*offer defaults* (capacity, `extra_from_runner`, requirements, certification
steps) so the operator's gesture is "enable template + set price",
hardware→template matching from `requirements` (making `allowed_gpu_*`
real), the probation ladder, fairness, and payouts. 0040 §6–8 are superseded
where they describe render/apply, controller-run certification, and the
controller-only credential check.

## 8. Deferred / out of scope

- Keypair credentials (documented path only, §3.3).
- WebRTC media-plane workloads over the tunnel (0040 carve-out stands).
- Multi-broker pools: offer and credential push must be per-broker and
  idempotent; designed in item 17, not exercised in v1.
- Runner-declared *capacity*; capacity stays operator-owned.

## 9. Success criteria

- A standalone orch publishes a signed manifest having authored only
  `offers[]`, `coordinator-config.yaml`, and `sign-policy.json`; no runner
  fact appears in any file the operator edits.
- Starting a second identical runner produces no config edit, no reload, and
  no sign.
- A runner that changes its declared shape gets no work and appears on the
  coordinator's Runners page with the disagreeing field; the manifest is
  byte-identical.
- The pool member path and the standalone path use the same agent bundle and
  the same attach document.
- `spec_version` in a published manifest equals the protocol module's
  `VERSION`; the registry daemon has exactly one manifest validator.

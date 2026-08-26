---
spec_name: broker-admin
version: 1.0.1-draft
status: draft
last_updated: 2026-08-26
---

# Broker admin API contract

The private operator surface of a `capability-broker`: what the
`orch-coordinator` console (plan 0043 §3.6), `pool-controller` (§3.1, item
17), and an operator's own tooling call to see runners, manage offers,
enroll hosts, and read certification. It is a contract because two other
components depend on it and neither is the broker.

Also specified here: the `spec_version` stamp on `GET /registry/offerings`
(plan 0043 §3.7), because it is the one unpaid registry field this epic
adds and the coordinator gates on it.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as in
RFC 2119. Companion specs: [`runner-attach.md`](./runner-attach.md)
(what a runner sends), [`certification-steps.md`](./certification-steps.md) (what a step is), and
[`offering-axes.md`](./offering-axes.md) (what an offer advertises).

## 0. Contents

1. [Scope, auth, conventions](#1-scope-auth-and-conventions)
2. [State model the API exposes](#2-state-model)
3. [Runners](#3-runners)
4. [Offers](#4-offers)
5. [Enrollment and credentials](#5-enrollment-and-credentials)
6. [Certification](#6-certification)
7. [`/registry/offerings` stamp](#7-registryofferings-spec_version-stamp)
8. [Error codes](#8-error-codes)
9. [Versioning](#9-versioning)
10. [Conformance obligations](#10-conformance-obligations)

## 1. Scope, auth, and conventions

- **Path prefix** `/admin/v1/`. Rides the paid listener but MUST be
  disabled unless `admin_auth.method: bearer` is configured, and SHOULD be
  fenced by network policy (broker operator runbook §1.1). The pre-existing
  routes `GET /admin/v1/runtime`, `POST /admin/v1/runtime/reload`,
  `GET /admin/v1/worker-sessions`, `POST /admin/v1/worker-sessions/{id}/kill`
  keep their shape; `worker-sessions` is superseded by §3 and removed with
  plan 0043 item 11.
- **Auth:** `Authorization: Bearer <token>`. One token per broker. The
  coordinator holds it as `coordinator-config.brokers[].admin_token_ref`;
  the pool controller holds its own. A missing or wrong token is `401`
  with no body distinction between the two. Tokens are compared
  constant-time.
- **Write access is the whole point.** Every `POST`/`PUT` here changes
  what a runner may serve or who may attach. The coordinator's token is
  therefore a hot-zone secret with the same handling as its manifest
  candidate path: file-referenced (`env://` or `file://`), never inline.
- **Bodies** are JSON, UTF-8, `Content-Type: application/json`. Responses
  are JSON. Timestamps are RFC 3339 UTC. Field names `snake_case`.
- **Unknown request fields are rejected** (`400 unknown_field`, naming the
  JSON pointer) — the same rule as the attach document, for the same
  reason: silently ignored operator input is how a price ends up unset.
- **Idempotency:** every `PUT` is a full replacement and idempotent.
  `POST` routes that create (`enroll`) accept `Livepeer-Request-Id`; a
  replay with the same id returns the recorded outcome, including the
  plaintext credential, exactly as `paid-session` §3.1 replays an open.
- **Errors:** non-2xx bodies are
  `{ "code": "<§8 code>", "message": "<free text>", "field": "<pointer>"? }`
  and carry `Livepeer-Error: <code>`.
- **Pagination:** list routes accept `?limit=` (default 200, max 1000)
  and `?cursor=`; responses carry `next_cursor` when truncated. A console
  reading hundreds of pool hosts must not depend on one page.
- **Read routes are safe to poll.** Nothing here has side effects on
  `GET`; the coordinator polls `runners` and `certification` every few
  seconds and that MUST be cheap.

## 2. State model

Three objects, each with a state the API reports and never lets a runner
set:

```
runner capability × offer
  attached ──(match: capability_id, protocol, identity ⊆ match)──▶ matched
  matched  ──(certification passes)────────────────────────────▶ certified
  certified ─(shape == frozen shape, or this run froze it)──────▶ eligible
  certified ─(shape ≠ frozen shape)─────────────────────────────▶ ineligible
  any ──(document re-sent with different projection)───────────▶ attached
  any ──(certification fails / recertify requested)─────────────▶ matched
  any ──(connection lost)───────────────────────────────────────▶ (gone)
```

| Object | Key | States |
|---|---|---|
| **Runner** | `host_id` (one enrollment, one or more connections) | `connected` \| `disconnected` (kept for `runner_retention`, default 24h, then dropped) |
| **Runner capability** | `host_id` + `local_id` | `accepted` \| `rejected` at attach (`runner-attach.md` §4); then per offer: `attached` \| `matched` \| `certified` \| `eligible` \| `ineligible` |
| **Offer** | `offering_id` (unique per broker; `capability_id` is a property, not part of the key) | `unfrozen` (no certified runner yet; not advertised) \| `frozen` (advertised while ≥ 1 eligible runner existed at freeze; afterwards health) \| `superseding` (an `accept-shape` is pending sign) \| `disabled` |
| **Credential** | `credential_id` | `active` \| `rotating` (old + new both valid until `grace_until`) \| `expired` \| `revoked` |
| **Certification run** | `host_id` + `offering_id` + `run_id` | `running` \| `passed` \| `failed` \| `aborted` |

The API is a projection of these; nothing in it lets a caller move a
runner to `eligible` directly. The only operator transitions are
`accept-shape` (§4.3), `disable`/`enable` on an offer (§4.4), certification
`run` (§6.3), and credential revoke/rotate (§5).

## 3. Runners

### 3.1 `GET /admin/v1/runners`

Every runner the broker knows, connected or recently disconnected.

```json
{
  "runners": [
    {
      "host_id": "host-3f9a",
      "enrollment": { "credential_id": "cred_01jx…", "label": "rig-3f9a", "member_eth_address": "0x1234…" },
      "state": "connected",
      "connected_since": "2026-08-26T10:02:11Z",
      "last_seen": "2026-08-26T10:41:03Z",
      "connections": 1,
      "agent_version": "pool-member-agent/0.9.2",
      "contract_version": "1.0",
      "hardware": [ { "gpu_uuid": "GPU-8f3c…", "gpu_model": "NVIDIA H100 80GB HBM3", "vram_bytes": 85899345920,
                      "driver": "560.35.03", "cuda": "12.6", "facts": { "pcie_gen": "5" } } ],
      "capabilities": [
        {
          "local_id": "vllm-70b",
          "capability_id": "openai:chat-completions",
          "protocol": "paid-job/v1",
          "attach": { "status": "accepted", "warnings": [] },
          "declared": { "transports": ["unary","stream"], "work_unit": { "name": "tokens", "extractor": { "type": "openai-usage" } },
                        "identity": { "openai.model": "llama-3-70b", "provider": "vllm" },
                        "schema_versions": { "paid-job/v1": "1.0.15" }, "devices": ["GPU-8f3c…"],
                        "requirements": { "gpu_vram_min_bytes": 68719476736 } },
          "readiness": { "status": "ready", "checked_at": "2026-08-26T10:41:00Z" },
          "offers": [
            { "offering_id": "llama-3-70b-shared", "state": "eligible", "since": "2026-08-26T10:05:40Z",
              "certification": { "run_id": "run_01jx…", "state": "passed", "finished_at": "2026-08-26T10:05:40Z" } },
            { "offering_id": "llama-3-70b-premium", "state": "ineligible", "since": "2026-08-26T10:05:41Z",
              "reason": { "code": "shape_mismatch", "field": "/work_unit/extractor/type",
                          "declared": "openai-usage", "frozen": "response-jsonpath" } }
          ],
          "extensions": { "x-quantization": "fp8",
                          "x-certification-suggested": [ { "name": "smoke", "type": "request", "required": true, "config": { } } ] }
        },
        {
          "local_id": "whisper",
          "capability_id": "openai:audio-transcriptions",
          "protocol": "paid-job/v1",
          "attach": { "status": "rejected",
                      "reasons": [ { "code": "extractor_unknown", "field": "/capabilities/1/work_unit/extractor/type",
                                     "declared": "whisper-seconds", "expected": "one of: …" } ] },
          "offers": []
        }
      ],
      "extensions": { "x-agent-build": "2026.08.26+g1a2b3c" }
    }
  ],
  "next_cursor": null
}
```

Rules:

- `declared` is the accepted attach document's capability entry minus
  `paths` and `readiness` config (the runner's private surface) and minus
  `x-*` (which sit under `extensions`). It is exactly what the broker
  evaluated, so a console can show the disagreeing value unaltered.
- `offers[].reason` is present iff `state` is `ineligible`, `attached`
  after a match failure, or `matched` after a certification failure. It
  always names `field`, `declared`, and the other side (`frozen` for shape
  mismatch, `expected` for match and certification).
- A **rejected** capability (`attach.status: rejected`) is listed with its
  reasons and an empty `offers[]`. This is how a broken container becomes
  visible without being a manifest event.
- `readiness.status` is `ready` \| `not_ready` \| `unknown` from the
  runner-declared probe; `unknown` until the first probe completes.
- Filters: `?state=connected|disconnected`, `?offering_id=`,
  `?capability_id=`, `?host_id=`. `?include=paths` adds `paths` and
  `readiness.config` to `declared` for debugging.

### 3.2 `GET /admin/v1/runners/{host_id}`

One runner, same shape as a list entry, `404 runner_not_found` after the
retention window.

### 3.3 `POST /admin/v1/runners/{host_id}/disconnect`

Closes every connection for the host. The runner MAY reconnect (its
credential is untouched). Use `credentials/{id}/revoke` to make that
impossible. Returns `{ "host_id", "connections_closed": n }`.

## 4. Offers

### 4.1 `GET /admin/v1/offers` and `GET /admin/v1/offers/{offering_id}`

```json
{
  "offers": [
    {
      "offering_id": "llama-3-70b-shared",
      "capability_id": "openai:chat-completions",
      "protocol": "paid-job/v1",
      "state": "frozen",
      "advertised": true,
      "source": "admin",
      "operator": {
        "match": { "identity.openai.model": "llama-3-70b" },
        "price": { "amount_wei": "210000000", "per_units": 1 },
        "capacity": { "max_in_flight": 4, "queue_limit": 8 },
        "extra": { "region": "us-west-2", "gpu_class": "h100" },
        "extra_from_runner": ["x-quantization"],
        "session_policy": null,
        "certification": [ { "name": "ready", "type": "readiness", "required": true },
                           { "name": "smoke", "type": "request", "required": true, "config": { } } ]
      },
      "frozen": {
        "shape_hash": "sha256:9b1f…",
        "frozen_at": "2026-08-26T10:05:40Z",
        "frozen_by": { "host_id": "host-3f9a", "local_id": "vllm-70b", "run_id": "run_01jx…" },
        "projection": { "protocol": "paid-job/v1", "transports": ["unary","stream"],
                        "work_unit": { "name": "tokens", "extractor": { "type": "openai-usage" } },
                        "identity": { "openai.model": "llama-3-70b", "provider": "vllm" },
                        "schema_versions": { "paid-job/v1": "1" },
                        "promoted": { "x-quantization": "fp8" } }
      },
      "candidates": [
        { "shape_hash": "sha256:3c07…", "first_seen": "2026-08-26T11:12:00Z",
          "runners": [ { "host_id": "host-77b1", "local_id": "vllm-70b" } ],
          "diff": [ { "field": "/promoted/x-quantization", "frozen": "fp8", "candidate": "int8" } ] }
      ],
      "runners": { "eligible": 3, "ineligible": 1, "matched": 0, "attached": 0 },
      "advertised_tuple": { "capability_id": "openai:chat-completions", "offering_id": "llama-3-70b-shared",
                            "protocol": "paid-job/v1", "job": { "transports": ["unary","stream"] },
                            "work_unit": { "name": "tokens" }, "price_per_unit_wei": "210000000", "per_units": 1,
                            "extra": { "region": "us-west-2", "gpu_class": "h100", "openai": { "model": "llama-3-70b" },
                                       "provider": "vllm", "x-quantization": "fp8" }, "constraints": {} }
    }
  ],
  "next_cursor": null
}
```

- `operator` is the offer exactly as configured (file or admin push);
  `frozen` is absent while `state: unfrozen`; `candidates[]` lists every
  *certified-but-mismatching* shape currently presented by a connected
  runner, with the diff against the frozen projection, so a console can
  offer `accept-shape` on a concrete hash rather than "whatever is newest".
- `advertised_tuple` is what `/registry/offerings` carries for this offer
  (absent when not advertised). `work_unit.extractor` is never in it.
- `source` is `file` (host-config `offers[]`) or `admin` (§4.2). A broker
  serves exactly one source, chosen by config (`offers.source`).

### 4.2 `PUT /admin/v1/offers`

Full replacement of the offer set. This is how `pool-controller` pushes
template-derived offers; a standalone broker on `offers.source: file`
answers `409 offers_source_is_file`.

```json
{ "revision": "ctl-rev-4182",
  "offers": [ { "offering_id": "…", "capability_id": "…", "protocol": "…", "match": { }, "price": { }, "capacity": { },
                "extra": { }, "extra_from_runner": [ ], "session_policy": { }, "certification": [ ] } ] }
```

Rules:

- The body is the same `offers[]` grammar the file accepts (plan 0043
  §3.1); it is validated in full before anything changes, and one invalid
  offer rejects the whole push (`400 offer_invalid`, `field`).
- **Idempotent and revision-tagged.** `revision` is opaque; the broker
  stores it and reports it on `GET /admin/v1/runtime` as
  `offers_revision`. A push with a body identical to the current set is a
  no-op `200` (no reload, no freeze change).
- **Freeze survives a push.** Re-pushing an offer whose `offering_id`
  already exists keeps its frozen shape and its runners' states. Changing
  operator fields (price, capacity, `extra`) is an ordinary manifest
  candidate change and enters the sign cycle as such. Changing `match` or
  `extra_from_runner` does **not** re-freeze: the existing frozen shape
  stays until an explicit `accept-shape`; runners are simply re-matched.
  Dropping an `offering_id` deletes it, its freeze, and its certification
  results — the pool controller MUST NOT drop and re-add to "reset" an
  offer; that is what `accept-shape` and `certification/run` are for.
- Response: `{ "revision", "applied": true, "offers": n,
  "changed": ["<offering_id>", …] }`.

### 4.3 `POST /admin/v1/offers/{offering_id}/accept-shape`

The explicit operator gesture that supersedes a frozen shape (plan 0043
decision 6a). Body: `{ "shape_hash": "sha256:3c07…" }` — MUST name one of
the offer's current `candidates[]`; anything else is `409 shape_not_candidate`.

Effect, atomically:

1. The offer's `frozen` becomes that candidate's projection, `frozen_by`
   the first runner presenting it; `state` → `superseding`.
2. Every runner on the old shape → `ineligible` (`shape_mismatch`); every
   certified runner on the new shape → `eligible`.
3. `/registry/offerings` changes; the coordinator sees a candidate; the
   console holds it as `critical`. `state` returns to `frozen` when the
   coordinator confirms the signed manifest carries the new shape:
   `POST /admin/v1/offers/{offering_id}/confirm-published` with
   `{ "shape_hash" }` — idempotent; a hash that is not the pending one is
   a no-op `200`.

Until step 3 completes the broker serves the **old** shape's runners for
paid work — the signed manifest is what gateways bought against — and
returns `503` + `Livepeer-Backoff` if none remain. Two accepts in a row
before a sign simply replace the pending shape.

Response: `202 { "offering_id", "state": "superseding", "shape_hash",
"eligible_now": n, "ineligible_now": m }`.

### 4.4 `POST /admin/v1/offers/{offering_id}/disable` and `/enable`

`disable`: stop advertising and stop dispatching; freeze and certification
results are kept. `enable`: reverse. Both `200 { "offering_id", "state" }`.
A pool that wants an offer gone removes it from the push instead.

## 5. Enrollment and credentials

The credential store (plan 0043 §3.3) is sealed on disk like the session
store and holds only a **hash** of each bearer secret. Plaintext is
returned exactly once, from `enroll` or `rotate`.

### 5.1 `POST /admin/v1/enroll`

Standalone path (a pool mints on the controller and syncs via §5.4).

```json
{ "host_id": "host-3f9a", "label": "rig in rack 3", "expires_in_seconds": 7776000, "kind": "bearer" }
```

- `host_id` optional; generated (`host-<8 hex>`) when absent. MUST be
  unused (`409 host_id_taken`).
- `kind` defaults to `bearer`; a broker that does not implement the kind
  answers `400 credential_kind_unsupported`.
- `expires_in_seconds` defaults to 90 days; max is config.

Response `201`:

```json
{
  "credential_id": "cred_01jx…",
  "host_id": "host-3f9a",
  "credential": { "kind": "bearer", "token": "lpc_9f2c1ab4c81b32d0…" },
  "expires_at": "2026-11-24T10:00:00Z",
  "bundle": {
    "broker_urls": { "quic": "broker.example.net:8443", "ws": "wss://broker.example.net/internal/v1/worker/session" },
    "broker_eth_address": "0x1234…5678",
    "contract_version": "1.0"
  }
}
```

`bundle` is the same shape a pool's signup issues; the agent consumes one
format for both paths (plan 0043 decision 2, item 12). The token appears in
this response and nowhere else; the broker stores `sha256(token)`.

### 5.2 `GET /admin/v1/credentials` and `GET /admin/v1/credentials/{credential_id}`

```json
{ "credentials": [ { "credential_id": "cred_01jx…", "host_id": "host-3f9a", "label": "rig in rack 3", "kind": "bearer",
                     "state": "active", "issued_at": "…", "expires_at": "…", "last_used_at": "…",
                     "rotation": null, "source": "enroll" } ], "next_cursor": null }
```

`rotation` is `{ "previous_expires_at": "…" }` while `state: rotating`.
`source` is `enroll` or `sync`. No secret material is ever listed.

### 5.3 `POST /admin/v1/credentials/{credential_id}/rotate` and `/revoke`

- **rotate** — body `{ "grace_seconds": 3600 }` (default 1h). Returns the
  same `201` shape as `enroll` with a new token; the old token stays valid
  until `grace_until`, then `expired`. A connected runner keeps its
  connection through the rotation and MUST re-attach with the new token
  before grace ends or its next reconnect fails.
- **revoke** — body `{ "reason": "<free text, audited>" }`. Deletes the
  secret, marks `revoked`, **closes every connection for the host**, and
  moves its capabilities to gone. A revoked credential can never re-attach
  (`credential_rejected`, indistinguishable from unknown). Returns
  `200 { "credential_id", "state": "revoked", "connections_closed": n }`.

Both are audited with the caller's token id and the reason.

### 5.4 `PUT /admin/v1/credentials`

Pool sync: full replacement of the *synced* set (`source: sync`); locally
enrolled credentials are untouched.

```json
{ "revision": "ctl-cred-91",
  "credentials": [ { "credential_id": "cred_…", "host_id": "host-3f9a", "kind": "bearer",
                     "token_sha256": "e3b0…", "expires_at": "…", "label": "…", "member_eth_address": "0x…",
                     "state": "active" } ] }
```

- Only the hash travels; the controller is the minting authority and holds
  plaintext until it hands the bundle to the member.
- An entry present before and absent now is a **revoke** with all of §5.3's
  effects (connections closed). An entry whose `state` is `revoked` likewise.
- Idempotent; identical body → no-op `200`.

## 6. Certification

Steps are authored on the offer (`operator.certification[]`, shape per the
certification step spec); results are per runner × offer. The broker runs
them automatically when a capability reaches `matched`, on runner change,
and on request.

### 6.1 `GET /admin/v1/certification`

```json
{ "results": [
    { "host_id": "host-3f9a", "local_id": "vllm-70b", "offering_id": "llama-3-70b-shared",
      "run_id": "run_01jx…", "trigger": "match", "state": "passed",
      "started_at": "…", "finished_at": "…", "shape_hash": "sha256:9b1f…",
      "steps": [
        { "name": "ready",   "type": "readiness", "required": true, "status": "passed", "duration_ms": 41 },
        { "name": "smoke",   "type": "request",   "required": true, "status": "passed", "duration_ms": 1830,
          "evidence": { "status": 200, "asserted": ["$.choices[0].message.content"] } },
        { "name": "usage",   "type": "usage",     "required": true, "status": "passed", "evidence": { "units": 17 } },
        { "name": "latency", "type": "latency",   "required": false, "status": "failed",
          "evidence": { "p50_ms": 5210, "bound_ms": 4000 }, "message": "p50 above bound" } ] } ],
  "next_cursor": null }
```

- Filters `?host_id=`, `?offering_id=`, `?state=`, `?latest=true` (one
  result per runner × offer).
- Step `status`: `passed` \| `failed` \| `skipped` (a prior required step
  failed) \| `error` (the broker could not run it — tunnel down, fixture
  missing). A run `passed` iff every `required` step passed; a non-required
  failure is recorded and does not block.
- `evidence` is step-type-defined (`certification-steps.md` §3) and bounded
  (≤ 8 KiB per step); bodies are never stored verbatim.
- `trigger`: `match` \| `runner_change` \| `operator` \| `recertify`.

### 6.2 `GET /admin/v1/certification/{host_id}/{offering_id}`

All runs for the pair, newest first, same entry shape.

### 6.3 `POST /admin/v1/certification/{host_id}/{offering_id}/run`

Body `{ "local_id": "vllm-70b"? }` (required when the host serves that
`capability_id` under more than one identity matching the offer).
Aborts an in-flight run for the pair, starts a new one with
`trigger: operator`, returns `202 { "run_id", "state": "running" }`.
`409 runner_not_matched` if the capability is not matched to the offer.

A passing run on an unfrozen offer **freezes** it (`runner-attach.md` §5);
that is the only automatic freeze and it is why `run` is a write.

## 7. `/registry/offerings` `spec_version` stamp

`GET /registry/offerings` gains a required root field:

```json
{ "spec_version": "2.0.0", "orch_eth_address": "0x…", "offers_revision": "ctl-rev-4182", "capabilities": [ … ] }
```

- `spec_version` MUST equal the protocol module's exported `VERSION` the
  broker was built against (plan 0043 item 4). It is the same string the
  coordinator writes into `manifest.spec_version`.
- The coordinator MUST refuse to merge brokers whose `spec_version` majors
  differ from its own imported `VERSION` major, naming both, and MUST NOT
  publish a candidate in that state.
- `capabilities[]` carries **only** offers in state `frozen` or
  `superseding` (old shape) that had ≥ 1 eligible runner at freeze; an
  `unfrozen` or `disabled` offer is absent. The payload is a pure function
  of (offer set, frozen shapes) — runner churn does not change it.
- `offers_revision` is informational, mirrors §4.2.

## 8. Error codes

| Code | HTTP | Meaning |
|---|---|---|
| `unauthorized` | 401 | Missing or wrong admin bearer. |
| `unknown_field` | 400 | Request body field not in this spec; `field` names it. |
| `invalid_request` | 400 | Any other body/parameter validation failure; `field` names it. |
| `offer_invalid` | 400 | An offer in a push failed the `offers[]` grammar; `field` names the offer and key. |
| `credential_kind_unsupported` | 400 | Enroll asked for a kind the broker does not implement. |
| `runner_not_found` | 404 | No such `host_id` within retention. |
| `offer_not_found` | 404 | No such `offering_id`. |
| `credential_not_found` | 404 | No such `credential_id`. |
| `host_id_taken` | 409 | Enroll with a `host_id` already enrolled. |
| `offers_source_is_file` | 409 | `PUT /offers` on a broker whose offers come from host-config. |
| `shape_not_candidate` | 409 | `accept-shape` named a hash that is not a current candidate. |
| `runner_not_matched` | 409 | Certification `run` for a pair that is not matched. |
| `credential_revoked` | 409 | Rotate on a revoked credential. |
| `internal_error` | 500 | Anything else. |

## 9. Versioning

`/admin/v1/` is the major. Additive fields in responses are minor and
consumers MUST ignore response fields they do not know (the reverse of the
request rule). A removed or re-typed field, a changed state name, or a
changed side effect is `/admin/v2/`. The version in this spec's
frontmatter tracks the document.

## 10. Conformance obligations

| Fixture | Asserts |
|---|---|
| `admin-rejects-without-token` | Every route → `401 unauthorized`, identical body for missing and wrong token. |
| `admin-runners-lists-rejected-capability` | Attach with one bad entry → `GET /runners` shows it `attach.status: rejected` with `declared`/`expected`. |
| `admin-runners-names-shape-mismatch` | Freeze, attach a differing runner → `offers[].state: ineligible`, `reason.frozen` and `reason.declared` set. |
| `admin-offers-put-is-idempotent` | Same body twice → second is `200`, `changed: []`, no reload. |
| `admin-offers-put-keeps-freeze` | Push with a price change on a frozen offer → `frozen` unchanged, `advertised_tuple.price_per_unit_wei` changed. |
| `admin-offers-put-rejects-unknown-field` | An offer with `backend:` → `400 unknown_field`, `field` names it. |
| `admin-accept-shape-requires-candidate` | Random hash → `409 shape_not_candidate`. |
| `admin-accept-shape-flips-eligibility` | Two shapes attached and certified → accept the second → old runners `ineligible`, new `eligible`, `/registry/offerings` unchanged until publish. |
| `admin-enroll-returns-token-once` | `enroll` → token in `201`; `GET /credentials/{id}` shows none. |
| `admin-enroll-replays-on-request-id` | Same `Livepeer-Request-Id` → identical `201` including token. |
| `admin-revoke-kills-connection` | Connected runner, revoke → connection closed, re-attach `credential_rejected`. |
| `admin-credentials-sync-drop-is-revoke` | Sync without a previously synced entry → that host's connection closed. |
| `admin-certification-run-freezes-unfrozen-offer` | Unfrozen offer, run passes → `state: frozen`, `frozen_by.run_id` matches. |
| `registry-offerings-stamps-spec-version` | Root `spec_version` equals the module `VERSION`. |
| `registry-offerings-excludes-unfrozen` | An offer with no certified runner is absent from `capabilities[]`. |

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.0.1-draft | 2026-08-26 | Add `POST /admin/v1/offers/{id}/confirm-published` — the coordinator's report that the signed manifest now carries the accepted shape, which resolves `superseding → frozen`. Until it lands the broker keeps dispatching the previously published shape. Additive. |
| 1.0.0-draft | 2026-08-26 | Initial contract (plan 0043 §3.6, §3.7, item 2). Runners (list/get/disconnect), offers (list/get, full-replacement `PUT`, `accept-shape`, disable/enable), enrollment and credentials (`enroll`, list, rotate, revoke, hash-only `PUT` sync), certification (results, per-pair history, `run`), the `spec_version` stamp and frozen-only rule on `/registry/offerings`, error codes, and conformance fixtures. Supersedes `GET/POST /admin/v1/worker-sessions*`. |

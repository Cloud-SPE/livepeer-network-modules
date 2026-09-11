---
spec_name: runner-attach
version: 1.2.0-draft
status: draft
last_updated: 2026-09-02
contract_version: "1.2"
---

# Runner attach contract

The one document a runner sends a broker to say what it is. It is the only
way runner facts enter the system: the operator never types them, and the
runner can never turn them into a manifest change.

This spec supersedes `paid-session/v1` §7.1.1 (the optional describe path)
and generalises it to every protocol. It is the protocol deliverable of
plan 0043 §3.2 and implements decisions 2–5 of that plan.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as in
RFC 2119.

## 0. Contents

1. [Roles and trust context](#1-roles-and-trust-context)
2. [When the document is sent](#2-when-the-document-is-sent)
3. [Document shape](#3-document-shape)
   — [host level](#31-host-level), [capability level](#32-capability-level),
   [`x-*`](#33-the-x--extension-space)
4. [Validation and rejection](#4-validation-and-rejection)
5. [The frozen shape](#5-the-frozen-shape)
6. [The attach result](#6-the-attach-result)
7. [Routing dispatched work back to the runner](#7-routing-dispatched-work-back-to-the-runner)
8. [Versioning](#8-versioning)
9. [Conformance obligations](#9-conformance-obligations)
10. [Runner obligations — the implementer's checklist](#10-runner-obligations--the-implementers-checklist)

The JSON Schema is [`runner-attach/schema.json`](./runner-attach/schema.json);
worked examples, valid and invalid, are under
[`runner-attach/examples/`](./runner-attach/examples/) and are validated by
`make validate-examples`.

## 1. Roles and trust context

Three parties, two trust boundaries:

| Party | Owns | Never does |
|---|---|---|
| **Runner** (the agent speaking for one host and its containers) | What it *is*: capability ids, protocol, transports or descriptor schemas, work unit and extractor, its own paths, readiness, model identity, hardware, versions | Set a price, choose an offering, change what is advertised, certify itself |
| **Broker** | Admitting the document, matching it to offers, freezing the first certified shape, marking later runners eligible or not | Adopt a runner's later change into an offer; execute or interpret `x-*` |
| **Operator** (standalone host-config or pool template) | Offers: price, capacity, `extra`, session policy, certification steps, which `x-*` keys are promoted | Author any field of this document |

The document flows *into* the cold-key-signed manifest exactly once per
offer: when the first certified runner freezes it (§5). After that the
manifest is byte-stable against runner churn. A runner that disagrees with
the frozen shape is ineligible for that offer; it is never a manifest change
(plan 0043 decision 3). This is the property every rule below protects.

The runner is authenticated by a **credential** the broker's credential
store issued to one host enrollment (plan 0043 §3.3). The credential grants
*attach*, not eligibility, and never carries commercial authority: a stolen
credential attaches as that host, is gated by certification, and credits
the enrollment's payout address.

## 2. When the document is sent

The document is the payload of the `register` message on the runner's
outbound connection to the broker — the QUIC session or the WebSocket
fallback of plan 0040 §6. The runner MUST send it:

- **once, first**, on every new connection, before any other message; a
  connection that carries anything else first MUST be closed by the broker;
- **again on any change** to its content, on the same connection. Each
  document is a full replacement of the previous one for that connection,
  never a patch;
- **again after reconnect**, unconditionally — the broker keeps no
  document across connections.

The broker answers every document with exactly one attach result (§6).
Until the result says the document was accepted, the broker MUST NOT
dispatch work over the connection.

A connection serves exactly one host (`host_id`). One enrollment MAY hold
several connections (a restart's overlap, a deliberate dual-path); the
document on each is independent and the broker unions their capabilities.

Transport framing (message envelope, stream multiplexing, keepalive) is the
tunnel's business and is not specified here.

## 3. Document shape

A single JSON object, UTF-8, at most **256 KiB**. Field names are
`snake_case`. Every field not listed here and not prefixed `x-` is unknown,
and an unknown field rejects the whole document (§4.1).

```json
{
  "contract_version": "1.0",
  "credential": { "kind": "bearer", "token": "lpc_…" },
  "host_id": "host-3f9a",
  "agent_version": "pool-member-agent/0.9.2",
  "hardware": [
    { "gpu_uuid": "GPU-8f3c…", "gpu_model": "NVIDIA H100 80GB HBM3",
      "vram_bytes": 85899345920, "driver": "560.35.03", "cuda": "12.6" }
  ],
  "capabilities": [
    {
      "capability_id": "openai:chat-completions",
      "protocol": "paid-job/v1",
      "local_id": "vllm-70b",
      "transports": ["unary", "stream"],
      "work_unit": { "name": "tokens", "extractor": { "type": "openai-usage" } },
      "paths": { "invoke": "/v1/chat/completions" },
      "readiness": { "type": "http-openai-model-ready", "path": "/v1/models",
                     "config": { "model": "llama-3-70b" } },
      "identity": { "openai.model": "llama-3-70b", "provider": "vllm" },
      "schema_versions": { "paid-job/v1": "1.0.15" },
      "requirements": { "gpu_vram_min_bytes": 68719476736 },
      "devices": ["GPU-8f3c…"],
      "x-quantization": "fp8"
    }
  ]
}
```

### 3.1 Host level

| Field | Req | Type | Validated against |
|---|---|---|---|
| `contract_version` | ✔ | `"<major>.<minor>"` string | The broker's supported majors. An unknown major rejects the document; a newer minor on a known major is accepted and any field it introduced is treated as unknown (§8). |
| `credential` | ✔ | object, `{kind, …}` | The credential store. `kind: "bearer"` is the only kind defined by this version: `{ "kind": "bearer", "token": "<opaque>" }`. Other kinds are reserved (§3.1.1). |
| `host_id` | ✔ | string, 1–128 chars, `[A-Za-z0-9._-]` | Free. Stable per host across reconnects; audit key. MUST equal the enrollment's host id where the credential store records one. |
| `agent_version` | ✔ | string, ≤ 128 chars | Free; audit only. Conventionally `<binary>/<semver>`. |
| `hardware[]` | ✔ (MAY be empty) | array of hardware units, ≤ 64 | GPU-uniqueness rules of plan 0040 §4.2; each capability's `requirements`. |
| `public_url` | opt (1.2) | string, ≤ 256 chars, an `https://` origin with no path, query or fragment | The origin at which this host's paid-session runners are reachable from outside — the base a runner builds its descriptor `url` from, and the fact placement gates `paid-session` templates on (a host without it is `host_not_public`). Absent means not public. Never a tunnel address: the broker does not relay session media. |
| `capabilities[]` | ✔ (MAY be empty) | array of capability entries, ≤ 64 | §3.2. An empty array is a valid *hardware-only* attach (a host announcing itself before it has any runner to offer; epic 2 uses this for template matching). |
| `x-*` | opt | any JSON | Relayed to operator surfaces verbatim; never interpreted (§3.3). |

**Hardware unit**

| Field | Req | Type | Notes |
|---|---|---|---|
| `gpu_uuid` | ✔ | string, 1–128 chars | The vendor's stable device id (`nvidia-smi -q` `GPU UUID` on NVIDIA). Unique within the document. Pragmatic trust-but-verify identity per plan 0040 §4.2 — not proof of ownership. |
| `gpu_model` | ✔ | string, ≤ 128 chars | Marketing name as the driver reports it. Matched by `requirements.gpu_models[]` with exact, case-sensitive equality after whitespace trim. |
| `vram_bytes` | ✔ | integer ≥ 0 | Total device memory. |
| `driver` | opt | string, ≤ 64 chars | Driver version string. |
| `cuda` | opt | string, ≤ 64 chars | CUDA (or equivalent runtime) version string. |
| `facts` | opt | object of string → string, ≤ 32 keys | Opaque; UI only. Never matched, never frozen. |
| `kind` | opt (1.2) | `gpu` (default) \| `cpu` | What this unit is. A `cpu` unit is a socket: `gpu_uuid` is its stable id (`cpu-<host_id>-<socket>`), `gpu_model` the CPU's model string, `vram_bytes` 0. The field names are the GPU's because renaming them is a major; a compute unit was a GPU first. |
| `cores` | ✔ for `cpu` | integer ≥ 1 | Physical cores on this socket. The pool's CPU classes are core tiers. |
| `threads` | opt | integer ≥ 1 | Hardware threads on this socket. |
| `isa` | opt | array of string, ≤ 16 | Instruction-set extensions the pool may select on (`avx2`, `avx512`, `amx`). |

A document whose `hardware[]` is empty MAY still carry capabilities: CPU
work exists. What it MUST NOT do is carry a capability whose
`requirements` name GPU facts (§4.2).

#### 3.1.1 Credential kinds

`kind` selects how the broker binds the document to an enrollment. Only
`bearer` is normative in `1.0`. The field exists so the upgrade path is
additive:

- **`bearer`** — `{ "kind": "bearer", "token": "<opaque, ≤ 512 chars>" }`.
  Compared constant-time against the sealed credential store. Expired,
  revoked, and unknown tokens are indistinguishable to the sender (§4.1).
- **`ed25519`** (reserved, not implemented in `1.0`) — the agent generates
  a keypair at enrollment; the store holds the public key; the document
  carries `{ "kind": "ed25519", "key_id": "…", "signature": "<base64 over
  the JCS form of the document with credential.signature removed>" }`.
  Alternatively the same key is presented as a QUIC client certificate and
  `credential` carries only `{ "kind": "ed25519", "key_id" }`. A broker
  that does not implement a `kind` MUST reject the document with
  `credential_kind_unsupported`, which is how an agent learns to fall back.

### 3.2 Capability level

One entry per capability the host can serve. The same `capability_id` MAY
appear more than once with different `identity` (two models behind one
API), never twice with the same `identity` (§4.1).

**Capability id vocabulary.** The broker treats `capability_id` as opaque
and never enforces a shape — that is core belief #1 — but a runner, a
catalog, and a gateway have to agree on the string, and three spellings
of one capability were found in the wild (`openai-chat-completions`,
`openai.chat.completions`, `openai:chat-completions`). The network's ids
follow one rule, and a runner SHOULD emit them in this form:

- **prefix** — the wire family when the capability implements a real,
  externally specified API (`openai:`); otherwise the product domain
  (`video:`, `vision:`, `audio:`, `text:`, `meet:`). `livepeer:` is not a
  prefix: it names the network, not a product.
- **suffix** — for `openai:`, the endpoint name with `/` folded to `-`
  (`chat-completions`, `audio-transcriptions`, `images-generations`);
  otherwise what the capability does (`transcode`, `image-analysis`,
  `rerank`, `sfu-room`), with `.` for variants of one product
  (`video:transcode.vod`, `video:transcode.abr`, `video:transcode.live`,
  `audio:transcribe.live`).
- exactly one `:`, never `/`.

A capability that is not the OpenAI endpoint it resembles does not take
the `openai:` prefix — an image-analysis model that happens to accept a
chat-shaped request is `vision:image-analysis`, and a live transcript
stream is `audio:transcribe.live` even though the same model's batch
endpoint is `openai:audio-transcriptions`. The prefix is a promise about
the wire, and a caller who sends an OpenAI request to an `openai:`
capability must get an OpenAI response.

| Field | Req | Type | Validated against | Frozen |
|---|---|---|---|---|
| `capability_id` | ✔ | string, 1–256 chars | Opaque; no closed enum (core belief #1, #7). | — (it is the match key) |
| `protocol` | ✔ | `<name>/v<N>` | The broker's known protocols (`paid-job/v1`, `paid-session/v1`). Unknown → capability rejected, never guessed. | ✔ |
| `local_id` | opt | string, 1–64 chars, `[A-Za-z0-9._-]` | Unique within the document. Echoed on every dispatched request so the agent can route (§7). Defaults to the entry's index as a decimal string. | — |
| `transports[]` | ✔ for `paid-job` | non-empty unique subset of `unary`, `stream`, `multipart` | `offering-axes.md` §2. | ✔ |
| `descriptor_schemas[]` | ✔ for `paid-session` | non-empty unique list of `<name>/v<N>` tags | Each MUST be well-formed and present in `schema_versions`. The broker keeps no list of schemas: it never interprets a descriptor body (runtime-descriptor §4), so a tag it has not seen is a schema it can carry. | ✔ |
| `work_unit.name` | ✔ | string, 1–64 chars | Opaque metering dimension name. | ✔ |
| `work_unit.extractor` | ✔ for `paid-job`; MUST be absent for `paid-session` | `{ "type": "<extractor>", …params }` | `type` MUST name an extractor the broker implements (`extractors/`); params are that extractor's own, validated by it. The runner never supplies code. | ✔ (whole object) |
| `paths` | ✔ | object of relative paths | Job: `invoke` required, `options` optional. Session: `create`, `status`, `terminate` required; `status` and `terminate` MUST contain the literal `{id}` placeholder. Every path MUST start with `/`, MUST NOT contain `..`, `?`, `#`, or a scheme. | — |
| `readiness` | ✔ | `{ "type", "path"?, "config"? }` | `type` MUST be a broker-known *remote* probe: `http-status`, `http-jsonpath`, `http-openai-model-ready`, `tcp-connect`. `path` is relative like `paths.*`, default `/`. `config` is that probe's own parameters. Broker-local probe types (`command-exit-0`, `manual-drain`) are not valid here. | — |
| `identity` | ✔ (MAY be `{}`) | object of string → string, ≤ 32 keys | Keys `[a-z][a-z0-9_-]*` joined by `.` (dotted paths), values ≤ 256 chars. Matched by `offers[].match`; frozen into the offer's `extra` as nested objects (`"openai.model"` → `extra.openai.model`). A key MUST NOT be both a leaf and a prefix of another key. | ✔ |
| `schema_versions` | ✔ | object of tag → SemVer string | One entry for `protocol` and one for every `descriptor_schemas[]` tag. Each entry's major MUST equal the tag's `vN`; each MUST be a version the broker implements or is minor-compatible with (§8). | ✔ at major |
| `metering` | ✔ for `paid-session`; MUST be absent for `paid-job` | `runner-reported` | `offering-axes.md` §3. The only value: no session traffic transits the broker, so the runner is the only party that can count it. | ✔ |
| `heartbeat` | opt, session only | `{ "interval_seconds": ≥ 1 }` | Advisory: a cadence slower than the offer's `interval × missed_threshold` is surfaced as a warning; the broker enforces the offer's threshold. | — |
| `session_params_schema` | opt, session only | object, ≤ 16 KiB | Opaque description, never a validator (`paid-session` §7.1, carried forward). Relayed to `/registry/offerings`. | — |
| `requirements` | opt | `{ "gpu_vram_min_bytes"?, "gpu_models"?[] }` | This host's own `hardware[]` (§4.2). Epic 2 additionally matches it against template policy. | — |
| `devices[]` | opt | unique list of `gpu_uuid` strings | Each MUST appear in `hardware[]`. Names which of this host's GPUs back this capability. Absent means "unspecified"; a broker or pool that needs the binding (share caps, cross-address GPU rules) treats absent as *all* of `hardware[]`. | — |
| `x-certification-suggested[]` | opt | array of step objects (shape per [`certification-steps.md`](./certification-steps.md) §2) | Shown to the offer author; **never** adopted or executed automatically. | — |
| `x-*` | opt | any JSON, ≤ 32 KiB total per capability | Relayed verbatim to operator surfaces. Promoted into the offer's `extra` only for keys the offer lists in `extra_from_runner` (§3.3). | promoted keys only |

"Frozen" is defined in §5. Everything not frozen is live data: it changes
how the broker reaches, probes, or routes to the runner, never what is
advertised.

#### 3.2.1 Why each side owns what it owns

| Fact | Why the runner | Why not the operator |
|---|---|---|
| `work_unit.extractor` | The runner knows what its response carries (an OpenAI `usage` block, a JSONPath, a trailer). | Transcribing it is how `tokens` vs `participant_minutes` drift happened; a wrong extractor bills wrong silently. |
| `readiness` | The runner knows what "ready" means for it (model loaded, GPU free). | An operator-authored HTTP-status recipe approximates a fact the runner has exactly. |
| `identity` | Only the runner knows which model it actually loaded. | `extra.openai.model` typed by hand drifts from reality; the offer *selects* on identity instead of restating it. |
| `paths` | The runner's own API surface. | A runner refactor silently broke orchestrators. |
| capacity, price, `extra` (region, class), session policy | — | **These stay with the operator.** A runner declaring its own capacity or price would set what the orchestrator sells. Plan 0043 §8: runner-declared capacity is out of scope. |

### 3.3 The `x-*` extension space

Any key beginning `x-` at host or capability level is a runner-authored
extension. Rules:

- The broker MUST accept, store, and relay `x-*` values verbatim to the
  admin API and operator surfaces. It MUST NOT execute, interpret, or gate
  on them, with one exception: it MUST enforce the size bound.
- `x-*` values MUST NOT reach `/registry/offerings` or the manifest unless
  the offer lists the exact key in `extra_from_runner`. A promoted key is
  copied into the offer's `extra` under the same name (`x-` prefix kept)
  and is then part of the frozen shape for that offer.
- `x-certification-suggested` has a defined shape (a list of certification
  steps) so a console can render it, but it is still `x-*`: suggested,
  never run.
- Runners SHOULD namespace further (`x-vllm-…`) to avoid collisions across
  runner images.
- Total `x-*` payload: ≤ 32 KiB per capability entry, ≤ 32 KiB at host
  level.

Everything that is *not* `x-*` and not in this spec is unknown, and unknown
is fatal (§4.1). Extensions have a namespace precisely so the core can stay
strict.

## 4. Validation and rejection

The broker validates in a fixed order so a sender can rely on which class
of failure it will hear first. Each step either accepts the document,
rejects the document, or rejects individual capabilities.

```
parse JSON ─▶ size/encoding ─▶ contract_version ─▶ credential ─▶ shape (schema)
   ─▶ host-level semantic rules ─▶ per-capability semantic rules ─▶ accepted
```

### 4.1 Document-level rejection

The whole document is refused, nothing is stored, and the broker MUST NOT
dispatch work over the connection. The result (§6) carries one of these
reason codes:

| Code | Trigger |
|---|---|
| `malformed` | Not a single JSON object, not UTF-8, or > 256 KiB. |
| `contract_version_unsupported` | Major not in the broker's supported set, or the field is missing or not `<major>.<minor>`. |
| `credential_kind_unsupported` | `credential.kind` the broker does not implement. |
| `credential_rejected` | Bearer token unknown, expired, or revoked. The three MUST be indistinguishable to the sender (constant-time compare; no existence disclosure). The broker SHOULD close the connection after a bounded number of these per source. |
| `unknown_field` | Any field, at any depth of the structure this spec defines, that is neither listed here nor `x-`-prefixed. The result names the JSON pointer. Unknown fields inside opaque blobs (`facts`, `session_params_schema`, extractor and probe `config`, `x-*`) are not unknown fields. |
| `schema_violation` | Any other JSON-Schema failure at host level (missing required, wrong type, bound exceeded). The result names the pointer. |
| `duplicate_gpu_uuid` | Two `hardware[]` entries share a `gpu_uuid`. |
| `duplicate_capability` | Two `capabilities[]` entries share `(capability_id, identity)` after key-sorting `identity`, or share a `local_id`. |
| `host_id_mismatch` | The enrollment the credential resolves to records a different host id. |

Document-level rejection is the answer to malformation and impersonation.
It is deliberately not the answer to "one of your capabilities is wrong":
that would let one bad container take a healthy host off the network.

### 4.2 Capability-level rejection

A capability with an invalid value is rejected **on its own**; the document
and its other capabilities are accepted. The result names the entry, the
field, what was declared, and what the broker expected.

| Code | Trigger |
|---|---|
| `schema_violation` | Shape failure scoped to this entry (missing required for its protocol, bad enum, bound exceeded, `extractor` on a session capability, `metering` on a job capability). |
| `protocol_unknown` | `protocol` the broker does not implement. |
| `extractor_unknown` | `work_unit.extractor.type` the broker does not implement. |
| `extractor_config_invalid` | The named extractor rejected its own parameters. |
| `readiness_type_unknown` | `readiness.type` not a remote probe the broker implements. |
| `readiness_config_invalid` | The named probe rejected its parameters. |
| `path_invalid` | A path fails §3.2's path rules, or a session `status`/`terminate` path lacks `{id}`. |
| `schema_version_missing` | `schema_versions` lacks an entry for `protocol` or a `descriptor_schemas[]` tag. |
| `schema_version_major_mismatch` | An entry's major differs from its tag's `vN`. |
| `schema_version_unsupported` | The broker implements neither that version nor a minor-compatible one (§8). |
| `identity_invalid` | Key grammar, size, or leaf/prefix conflict. |
| `requirements_unmet` | `requirements` cannot be satisfied by *any* unit in this host's `hardware[]` (or `hardware[]` is empty and `requirements` names GPU facts). The runner is asking to serve work its own host cannot run. |
| `device_unknown` | A `devices[]` entry not present in `hardware[]`. |

A rejected capability is not matched to any offer, is not certified, and
receives no work. It is visible on the admin API with its reason so an
operator can see a broken container without it being a manifest event.

### 4.3 What is not validated at attach

- **Reachability.** Attach does not probe. Readiness and certification run
  after acceptance; an unreachable runner is health, not rejection.
- **Match to any offer.** A host may attach capabilities no offer wants.
  They sit `attached`, unmatched — visible, harmless.
- **GPU uniqueness across *other* enrollments**, and every other rule that needs
  an offer or another host: these are match-time or pool-time decisions
  and surface as eligibility, not attach results.
- **`session_params_schema`, `x-*`, `facts`, `config` contents.** Opaque
  by contract; bounded in size only.

### 4.4 The rule that has no exceptions

> **A document never changes what is advertised.** Not on first attach,
> not on re-attach, not on change, not on operator whim through this
> channel. The only path from a runner fact to the manifest is §5's
> freeze — and the signature is the acceptance.

A broker that mutates an offer, `/registry/offerings`, or a manifest
candidate in response to an attach document, other than through §5,
does not conform.

## 5. The frozen shape

An offer selects runners with `match` over `identity` (and `capability_id`,
`protocol`). The first runner to pass the offer's certification **freezes**
the offer's shape; every later runner is compared to it.

**Frozen projection** of an accepted capability entry — computed over the
document as accepted, key-sorted, JCS-canonicalised (RFC 8785):

```json
{
  "protocol": "…",
  "transports": […]            | "descriptor_schemas": […],
  "work_unit": { "name": "…", "extractor": { … } },
  "metering": "…",             // session only
  "identity": { … },
  "schema_versions": { "<tag>": "<major>" },   // majors only
  "promoted": { "x-…": … }     // the offer's extra_from_runner keys, if present
}
```

Rules:

- **Freeze once.** The first certification success for an offer stores
  the projection on the offer and produces a manifest candidate. From
  then on the offer's advertised tuple is derived from the frozen
  projection plus operator fields, never from any live document.
- **Match, never adopt.** A later runner's projection MUST equal the
  frozen one, byte for byte after canonicalisation, to be eligible. A
  differing runner is `ineligible` for that offer with the disagreeing
  field named; the offer is unchanged.
- **Minor versions do not disagree.** `schema_versions` is frozen at major
  so a runner upgrading `sfu-room/v1` `1.0.3 → 1.0.4` stays eligible; a
  runner speaking `sfu-room/v2` is a different `descriptor_schemas` tag
  and therefore a different shape.
- **A changed re-attach is a new runner.** If a connected runner re-sends
  a document whose projection differs from what it was certified with, it
  drops to `attached` for that offer and is re-evaluated as above. It does
  not carry its certification across the change.
- **Supersession is an operator gesture.** Replacing a frozen shape is
  `POST /admin/v1/offers/{id}/accept-shape` ([`broker-admin.md`](./broker-admin.md) §4.3),
  followed by a sign. The old shape's runners become ineligible at that
  moment. Nothing in this document can trigger it.
- **No certified runner, no advertisement.** An offer whose frozen shape
  has no eligible, certified runner at the time of freeze is not
  published; runner churn afterwards is `503` + `Livepeer-Backoff`, never
  a manifest change (plan 0040 §7 preserved).

## 6. The attach result

The broker answers each document with one `register_result` message:

```json
{
  "contract_version": "1.0",
  "document": "accepted",
  "host_id": "host-3f9a",
  "reasons": [],
  "capabilities": [
    { "index": 0, "local_id": "vllm-70b", "capability_id": "openai:chat-completions",
      "status": "accepted", "warnings": [] },
    { "index": 1, "local_id": "whisper", "capability_id": "openai:audio-transcriptions",
      "status": "rejected",
      "reasons": [ { "code": "extractor_unknown", "field": "/capabilities/1/work_unit/extractor/type",
                     "declared": "whisper-seconds",
                     "expected": "one of: openai-usage, response-jsonpath, request-formula, multipart-audio-duration, bytes-counted, seconds-elapsed, response-header, response-trailer, ffmpeg-progress" } ] }
  ]
}
```

| Field | Notes |
|---|---|
| `contract_version` | The version the broker evaluated the document under. |
| `document` | `accepted` \| `rejected`. When `rejected`, `capabilities` is empty and `reasons` is non-empty. |
| `reasons[]` | `{ code, field?, declared?, expected?, message? }`. `field` is a JSON pointer (RFC 6901) into the document. `declared` and `expected` are strings; both sides are always named when a comparison failed. |
| `capabilities[]` | One entry per document entry, in order. `status` is `accepted` \| `rejected`; `reasons` on rejection, `warnings` (same shape, advisory — e.g. `heartbeat_slower_than_offer`) on acceptance. |

The result reports *attach* outcomes only. Matching, certification, and
eligibility are broker state that changes after the result is sent; they
are read from the admin API ([`broker-admin.md`](./broker-admin.md) §3), not
from this message. A runner does not need them — it serves what it is sent.

Reason `code` values are stable identifiers (§4.1, §4.2); `message` is
free text and MUST NOT be parsed.

## 7. Routing dispatched work back to the runner

The broker dispatches requests over the connection to the paths a
capability declared. Because one host may serve several capability entries
— including the same `capability_id` under two identities — every request
the broker dispatches for an entry carries:

- `Livepeer-Runner-Local-Id: <local_id>` — the entry's `local_id`
  (defaulted per §3.2). This is the routing key; the agent maps it to the
  container and port it chose when it built the document.
- `Livepeer-Capability` and `Livepeer-Offering` per
  [`headers/livepeer-headers.md`](../headers/livepeer-headers.md)
  §Forwarding behavior, unchanged.

Readiness probes and certification requests carry the same header, so an
agent has exactly one routing rule. The agent MUST NOT route on the path
alone.

The header is broker → runner only. It MUST NOT be accepted from a gateway
and MUST be stripped if present on an inbound paid request.

### 7.1 Draining

A capability entry may carry `draining: true`. It means this runner is
winding down and MUST be sent no new work.

Draining is a live fact, not a change to what is sold: the runner stays
certified, its offer stays advertised, and its frozen shape is
untouched — only dispatch stops. That separation is the point. A pool
withdrawing a workload from one host has not changed the offering, so
the manifest must not flicker, and a gateway must get a 503 with a
backoff rather than a 404 on a tuple that is still on sale elsewhere.

The broker MUST NOT dispatch to a draining capability, MUST keep
advertising the offer it serves, and MUST NOT treat the flag as a shape
change (§5) — an agent that sets and later clears it does not
re-trigger certification.

Withdrawal is therefore the agent's to sequence: set `draining`,
re-register, let in-flight work finish, then stop the container.
Stopping first would drop requests the broker had already dispatched.

### 7.2 Response framing

A runner's response travels back over the connection as a complete unit,
so the broker knows the body's length before it writes anything to the
gateway. The broker MUST length-delimit the reply it relays, whatever the
runner sent. A runner therefore need not set `Content-Length`, and MUST
NOT be relied upon to: an agent that omits it must not cause the gateway
to receive a chunked reply for a response that was never streamed.

A runner that declares `Transfer-Encoding` keeps it, and the broker leaves
the framing alone.

## 8. Versioning

`contract_version` is `<major>.<minor>` and versions **this document's
shape and rules**, independently of the spec-wide `VERSION` and of any
protocol's version.

- **Major** changes remove or re-type a field, change a rule in §4 or §5, or
  change the projection in §5. A broker lists the majors it accepts;
  unknown major → `contract_version_unsupported`.
- **Minor** changes add optional fields or reason codes. A broker on minor
  *m* receiving minor *n > m* on the same major accepts the document and
  treats any field it does not know as `unknown_field` — which, per §4.1,
  rejects it. An agent MUST therefore send the lowest minor that carries
  the fields it uses, and SHOULD retry at a lower minor on `unknown_field`
  for a field it added under a newer minor. This keeps "unknown is fatal"
  and "additive minors" both true.
- `schema_versions` values are compared **per tag**: the broker accepts a
  version if it implements the same major and its own minor is ≥ the
  runner's, or the runner's minor is ≥ the broker's and the protocol spec's
  changelog marks the intervening minors additive. A broker MAY be
  stricter (exact minor) and MUST say so in `expected`.

The examples under `runner-attach/examples/` are pinned to the
`contract_version` in this spec's frontmatter.

## 9. Conformance obligations

Fixtures, named for the conformance runner (plan 0043 item 4 delivers the
runner side; the declarations live here so the spec owns them):

| Fixture | Asserts |
|---|---|
| `attach-accepts-minimal-job` | A job capability with only required fields is accepted; result lists it `accepted`. |
| `attach-accepts-minimal-session` | Likewise for a session capability with `descriptor_schemas`, `metering`, three paths. |
| `attach-accepts-hardware-only` | `capabilities: []` with one hardware unit is accepted. |
| `attach-rejects-unknown-field` | A top-level `price` key → `document: rejected`, `unknown_field`, pointer `/price`. |
| `attach-rejects-unknown-capability-field` | `capabilities[0].capacity` → whole document rejected (unknown is document-level even inside an entry). |
| `attach-rejects-bad-major` | `contract_version: "2.0"` → `contract_version_unsupported`. |
| `attach-rejects-credential-indistinguishably` | Unknown, expired, and revoked tokens produce byte-identical results. |
| `attach-rejects-one-capability-keeps-others` | Two entries, one with an unknown extractor → document accepted, entry 1 rejected with `extractor_unknown` naming `declared` and `expected`, entry 0 accepted and dispatchable. |
| `attach-rejects-extractor-on-session` | Session entry carrying `work_unit.extractor` → that entry `schema_violation`. |
| `attach-rejects-requirements-unmet` | `requirements.gpu_vram_min_bytes` above every unit → `requirements_unmet`. |
| `attach-rejects-duplicate-identity` | Two entries, same `capability_id` and `identity` → `duplicate_capability`. |
| `attach-replaces-on-resend` | Second document on the same connection with one fewer entry → the removed entry is gone from `GET /admin/v1/runners`. |
| `attach-never-mutates-offer` | Attach, certify, freeze, then re-attach with a changed `identity` → `/registry/offerings` is byte-identical; runner shows `ineligible` with `identity` named. |
| `attach-first-before-anything` | A `request` message before `register` → connection closed. |
| `attach-routes-by-local-id` | Two entries with the same `capability_id`; each dispatched request carries the right `Livepeer-Runner-Local-Id`. |

## 10. Runner obligations — the implementer's checklist

Derivative of the numbered sections; where it conflicts, they win.

| Obligation | Where | If you don't |
|---|---|---|
| Send the document first on every connection, and again on any change | §2 | Connection closed / stale facts served until reconnect. |
| Send a full document each time, never a delta | §2 | Fields you omitted are gone: capabilities vanish, hardware unregisters. |
| Send only fields in this spec or `x-*` | §3, §4.1 | Whole document rejected — one typo takes the host offline. Namespace extensions. |
| Declare `work_unit.extractor` for job work; never for session work | §3.2 | Entry rejected. |
| Declare `schema_versions` for the protocol and every descriptor schema | §3.2 | `schema_version_missing`. |
| Put `{id}` in session `status` and `terminate` paths | §3.2 | `path_invalid`. |
| Declare `requirements` your own hardware satisfies | §4.2 | `requirements_unmet` — you asked to serve what you cannot run. |
| Route dispatched requests by `Livepeer-Runner-Local-Id` | §7 | Two identities behind one `capability_id` cannot be told apart; the wrong model answers. |
| Never expect a change you send to alter an offer | §4.4, §5 | It won't. You become ineligible for the frozen shape instead; the operator decides. |
| Never send price, capacity, offering ids, or certification steps as core fields | §3.2.1 | `unknown_field`. Suggest certification via `x-certification-suggested`; the rest is not yours. |

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.2.0-draft | 2026-09-02 | `public_url` added at host level (plan 0046, decision 13 of the 2026-09-02 walkthrough): every paid-session data plane is external, so whether a host is reachable from outside is a fact the pool must see and gate on. `contract_version` goes to 1.2 — an optional field a runner may send; an agent that sets it sends 1.2, one that does not keeps sending 1.1 (§8). Same minor, same day: a hardware unit gains `kind`, `cores`, `threads`, `isa` so a CPU socket is a placeable compute unit (plan 0047, `lnm-iqn`). |
| 1.1.2-draft | 2026-09-02 | Prose only, no wire change. §3.2 gains the capability id vocabulary rule (plan 0045, decision 1 of the 2026-09-02 walkthrough): prefix is the wire family for a real standard API (`openai:`) else the product domain; suffix is the endpoint name or what it does, `.` for variants, one `:`, never `/`. `livepeer:meet/sfu-room` becomes `meet:sfu-room` in the examples. The broker still treats the id as opaque. Same day, decision 5: `descriptor_schemas[]` no longer has to be "known to the broker" — a well-formed tag with a `schema_versions` entry is accepted, and the `descriptor_schema_unknown` reason code is removed. This is a loosening, so a runner valid before is valid after; `contract_version` stays 1.1. Decision 13: `metering` loses `broker-observed` (offering-axes 1.0.8 — the relayed data plane it described was never built); a runner that declared it was never matchable, so nothing valid becomes invalid. |
| 1.1.1-draft | 2026-09-01 | Prose only, no wire change. §3.3's namespacing advice said "across adapter profiles"; the profiles are deleted by plan 0045 §3 and a runner now serves its own capability entry (`runner-contract.md`), so it says "across runner images". |
| 1.1.0-draft | 2026-08-27 | Added `draining` to a capability entry (§7.1): the runner is winding down and takes no new work, while staying certified and advertised. Live state, not shape — it is excluded from the frozen projection, so setting and clearing it never re-triggers certification. `contract_version` goes to 1.1: this adds a field a runner may send, so an older broker ignoring it is the pre-existing behaviour and a newer one gains the withdrawal it needs to drain a host without flickering the manifest. |
| 1.0.1-draft | 2026-08-27 | Added response framing (renumbered §7.2 when draining took §7.1): the broker MUST length-delimit the reply it relays from a runner, because the response crosses the connection as a complete unit and its length is therefore known. A runner need not set `Content-Length` and MUST NOT be relied upon to — an omitted one previously turned a non-streamed reply into a chunked one for the gateway. No change to the document shape, so `contract_version` stays `1.0`. |
| 1.0.0-draft | 2026-08-26 | Initial contract (plan 0043 §3.2, decisions 2–5). One versioned document for every protocol, sent on the tunnel `register` message; host level (`contract_version`, `credential`, `host_id`, `agent_version`, `hardware[]`) and capability level (`capability_id`, `protocol`, `local_id`, `transports`/`descriptor_schemas`, `work_unit{name, extractor}`, `paths`, `readiness`, `identity`, `schema_versions`, `metering`, `heartbeat`, `session_params_schema`, `requirements`, `devices`, `x-*`). Unknown non-`x-` field rejects the document; invalid value rejects the capability; frozen projection defined; `register_result` shape; `Livepeer-Runner-Local-Id` routing header. Supersedes `paid-session/v1` §7.1.1. Deviations from the plan-0043 sketch: `metering` is required (the manifest requires it and no operator field supplies it); `local_id` and `devices[]` added for routing and GPU binding; `schema_versions` frozen at major only. |

---
spec_name: runtime-descriptor
version: 1.0.0-draft
status: draft
part_of: paid-session@v1
last_updated: 2026-08-18
---

# Runtime descriptor framework

The runtime descriptor is the capability-owned payload that a runner returns
when a paid session is opened, and the only vehicle by which workload-specific
runtime coordinates cross the broker. The broker validates its **structure**,
stores it, and relays its **public part** — it never interprets its fields.

This is the seam that keeps `paid-session@v1` workload-agnostic:

> **Workload identity lives in descriptor schemas. Protocol names never carry
> it.** An SFU room, an RTMP ingest point, a generative-video scope, and a
> trickle-egress target are different descriptor schemas riding one identical
> session protocol.

This document defines the envelope, the partition rules, admission grants, and
the validation contract. Individual schemas (`sfu-room/v1`, `rtmp-hls/v1`, …)
are specified separately under `descriptors/` and are owned by their
capabilities.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as in
RFC 2119.

## 1. Roles and trust context

| Role | Relationship to the descriptor |
|---|---|
| **Runner** | Authors the descriptor when the broker creates a runner session. Sole owner of its meaning. |
| **Broker** | Validates structure, stores it, relays the public part, delivers grants exactly once. Never interprets fields, never branches on their values. |
| **Gateway** | Consumes the public part and the grants. Selects offerings by declared `descriptor_schema`; a gateway MUST NOT open a session whose schema it does not understand. |
| **Customer client** | May receive fields the gateway chooses to forward. The framework makes no guarantee about this hop; schemas SHOULD note which public fields are customer-safe. |

Descriptors exist inside the dual-meter trust model (see
`docs/design-docs/` trust-model doc): grants are how the runner delegates
**admission** to the gateway, so the gateway can meter attach/join at its own
edge without touching media.

## 2. Envelope

The runner returns the descriptor as a top-level `runtime` object in its
session-create response:

```json
{
  "runtime": {
    "schema": "sfu-room/v1",
    "public": {
      "url": "wss://sfu-07.example.net",
      "room": "rm_9f2c1ab4",
      "status_url": "https://sfu-07.example.net/rooms/rm_9f2c1ab4/health"
    },
    "private": {
      "terminate_token": "rt_9d41c6…"
    },
    "grants": [
      {
        "id": "grant_01jx2…",
        "operations": ["participant-token-mint"],
        "secret": "gs_c81b32…",
        "expires_at": "2026-08-18T21:04:00Z",
        "max_uses": 1
      }
    ]
  }
}
```

Exactly four top-level keys are defined: `schema`, `public`, `private`,
`grants`. `schema` and `public` are REQUIRED; `private` and `grants` are
OPTIONAL. Any other top-level key MUST cause the broker to reject the create
response and fail the session open (fail-closed: the runner session is
terminated and payment state is closed). Unknown keys are a partition-bypass
vector, never a compatibility affordance.

### 2.1 `schema`

A tag naming the descriptor contract: `[a-z][a-z0-9-]*` name, `/v` and a
non-negative integer version — e.g. `rtmp-hls/v1`. The tag MUST equal the
`descriptor_schema` the selected offering declares in its manifest; a mismatch
is a create-response rejection. Schema versions are immutable: a breaking
change is a new tag.

### 2.2 `public`

A JSON object. This is the *only* portion of the descriptor the broker ever
returns to anyone, and it is returned on **both** session open and session
status — canonically identical each time. **The public part is immutable for
the lifetime of the session.** There is no update mechanism in v1: a runtime
whose coordinates change (host migration, failover) ends its session and a
new one opens. Mid-session runtime relocation is reserved for a future
version as an explicit control-plane event, not a descriptor mutation.

The public part SHOULD include a coordinate the gateway can cheaply probe as
evidence the runtime is real (a status or playback URL). This is the
verifiability hook the trust model's divergence policy leans on; schemas that
omit one force gateways to rely on usage claims alone.

Gateways MUST treat unknown fields inside `public` as ignorable (tolerant
reader). Field evolution *within* a schema version is the schema owner's
contract, not the framework's.

### 2.3 `private`

A JSON object holding material the broker may need for its own operations
against the runner (e.g. a termination credential) but that MUST NEVER be
relayed to the gateway, appear in status responses, logs, metrics labels, or
error messages. Long-lived runner credentials, upstream API secrets, TURN
shared secrets, and broker callback material belong here or nowhere.

The broker persists `private` in its durable session store only as needed for
its own operations, and implementations SHOULD encrypt it at rest.

### 2.4 `grants`

An array (at most 4 entries) of one-time, least-privilege admission grants.
Each grant delegates a named runner-side operation to the gateway, scoped to
this runner session only:

| Field | Req | Meaning |
|---|---|---|
| `id` | yes | Opaque identifier, unique within the session. |
| `operations` | yes | Non-empty list of operation names defined by the schema (e.g. `participant-token-mint`, `stream-key-issue`). |
| `secret` | yes | The bearer material the gateway presents to the runner. |
| `expires_at` | yes | RFC 3339 instant after which the runner MUST refuse it. |
| `max_uses` | no | Positive integer; absent means unbounded until expiry. |

Grant semantics — the load-bearing rules:

1. **Delivered by session open exactly once, and never by status.** This
   generalizes the established once-only rule for sensitive open-time material
   and is what makes the admission edge auditable: whoever holds the grant is
   whoever the open response was delivered to.
2. **The broker MUST NOT retain `secret` in recoverable form** after the open
   response is delivered. It MAY retain a hash plus the grant metadata for
   audit and for status displays (`grants_issued: [...ids...]`).
3. **Restart recovery never re-mints grants.** A rebound session (see
   `paid-session@v1` recovery) continues with the grants already in the
   gateway's hands. A gateway that loses a grant has lost it; a schema MAY
   define a fresh-grant operation, but it MUST be an authenticated
   gateway→runner operation, not a broker replay.
4. **Verification is the runner's job.** The runner issued the secret and
   enforces `expires_at`, `max_uses`, and operation scope. The broker is not
   in the grant's data path after delivery.
5. **Grants die with the session.** Once the session is terminal the runner
   MUST refuse its grants regardless of `expires_at` — expiry is a backstop,
   not the lifetime. This is what lets long-running sessions use unbounded
   `max_uses` for inherently repeated admission operations (every
   participant join, key rotation, or re-attach is a use) without an
   open-ended credential outliving the thing it admits to.

## 3. Broker validation contract

On every runner create response, before binding the session, the broker MUST
enforce — in schema-agnostic form:

1. **Size**: the serialized `runtime` object is at most 16 KiB by default;
   an offering MAY declare a lower cap, never a higher one than the
   operator-configured ceiling.
2. **Shape**: `runtime` is a JSON object; only the four defined top-level
   keys; `schema` matches the tag grammar and the offering's declared
   `descriptor_schema`; `public` and `private` (if present) are objects;
   `grants` (if present) obeys §2.4 with at most 4 entries.
3. **Partition**: the outbound open/status views are built exclusively from
   `public` by construction (§4). Nothing validates "no secrets in public" —
   the broker cannot know what is secret. The schema's conformance fixtures
   carry that burden (§6), and the structural rule makes accidental leakage
   an impossibility rather than a review item.

A validation failure is a `502`-class open failure: terminate the runner
session if one was created, close payment state, record a stable reason.
Fail-closed, never fail-open with a partial descriptor.

## 4. Structural opacity requirements on implementations

These are requirements on broker implementations, testable in conformance:

- **Named types, deny-by-default serialization.** The public view MUST be
  produced by a sanitizer that copies from `public` only — never by
  serializing the stored descriptor with fields blocklisted. If the type
  system can express "the outbound struct has no private field," it MUST.
- **No interpretation.** The broker MUST NOT branch on the value of any
  descriptor field. It MAY use the `schema` tag as a metrics label and in
  operator display.
- **No leakage surfaces.** `private` and grant secrets MUST NOT appear in
  logs, traces, metrics, error bodies, or admin/debug endpoints. Errors
  reference JSON paths, never values.
- **Open/status identity.** The status handler MUST serve the public view
  from the same stored canonical form the open handler used — one source of
  truth, two readers, byte-identical output.

## 5. Discovery and negotiation

An offering manifest declares `descriptor_schema: <name/vN>`. Gateways filter
routes on it; the clearinghouse and registry treat it as an opaque string.
This replaces mode-name negotiation: the gateway asks for `paid-session` work
whose descriptor schema it understands, and gets it or a typed refusal —
never a session it cannot consume.

A capability MAY offer the same backend under multiple schemas as distinct
offerings. A single session has exactly one schema for its lifetime.

## 6. Conformance obligations

The framework ships executable fixtures every broker implementation MUST pass:

- open for a descriptor-declaring capability succeeds with no legacy media
  fields anywhere in the flow;
- open and status return byte-identical public views;
- a `private` field, a grant secret, and an unknown top-level key each fail
  to appear in any broker response (leak-attempt fixtures);
- an unknown top-level key, an oversize descriptor, a schema-tag mismatch,
  and a malformed grant each reject the open, fail-closed, with payment state
  closed;
- grants appear in the open response exactly once and in no status response;
  a restarted broker (rebind path) still never re-emits them.

Every descriptor **schema** additionally ships its own fixtures declaring
which of its fields are public-by-contract, so a schema change that moves a
sensitive field into `public` fails conformance rather than review.

## 7. What this framework is not

- **Not a media contract.** Media never transits the broker; the descriptor
  is coordinates, not content.
- **Not a general extension mechanism for the session protocol.** Session
  lifecycle, payment, leases, usage claims, and the control plane are fixed
  by `paid-session@v1`; the descriptor extends only *what the runtime is*.
- **Not self-describing.** There is no inline schema definition or
  reflection; the tag points at a spec document under `descriptors/`, and
  both ends are expected to have read it.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.0.0-draft | 2026-08-18 | Initial framework, extracted from the meeting-product handoff (A1–A3) and generalized per the 2026-08-18 redesign decisions. |

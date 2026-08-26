---
title: Runner-declared capabilities — what the runner owns vs what the operator owns
status: active
last-reviewed: 2026-08-19
---

# Runner-declared capabilities

An architectural gap surfaced 2026-08-19: `host-config.yaml` requires an
operator to hand-transcribe facts that only the runner actually knows.
This doc states the split we should be designing to, the drift it causes
today, and what is missing to close it.

> **Superseding work (2026-08-26).** Plan 0043 replaces the optional describe
> path with a mandatory, versioned, outbound attach document for every
> protocol — see
> [`livepeer-network-protocol/protocols/runner-attach.md`](../../livepeer-network-protocol/protocols/runner-attach.md).
> Gap 1 below (version negotiation) closes there via `schema_versions`. This
> doc is retired once plan 0043 item 18 lands; until then it is the record of
> why the split exists.
>
> **Implementation status (2026-08-19).** The design below shipped in the same
> cycle it was written. Runner self-description is now normative in
> [`paid-session/v1` §7.1.1](../../livepeer-network-protocol/protocols/paid-session.md)
> and implemented in `capability-broker/internal/sessionengine/describe.go`
> plus `internal/server/session_engine_init.go`, with the read-diff-never-adopt
> rule intact. Gaps 2 (declared `session_params` shape), 3 (runner-declared
> readiness), and 4 (extractors are `paid-job` only) below are **closed**; gap 1
> (protocol/schema version negotiation) is still open.

It deliberately does **not** propose that this repo test runner
implementations. Runners live outside this repo by design. The protocol
here specifies what a runner must *provide*; verifying a given runner is
its author's job.

## The split

> **The runner declares what it *is*. The operator declares what it
> *costs* and *where it runs*.**

Every field in a capability tuple belongs on exactly one side of that
line, and today several are on the wrong one.

### Runner-owned facts (currently hand-transcribed by operators)

| Field | Why it is a runner fact | Cost of it being config |
|---|---|---|
| `session.descriptor_schema` | The runner emits the descriptor; it knows its own schema | Stated twice, cross-checked by the broker. The check exists *only* because the fact is duplicated. |
| `work_unit.name` | The runner meters in a unit | Stated twice, cross-checked on **every event**. A mismatch rejects usage indefinitely — the meeting team hit exactly this class of drift (`participant_seconds` vs `participant_minutes`). |
| `session.runner.{create,status,terminate}_path` | The runner's own API surface | Pure transcription; a runner refactor silently breaks an orchestrator. |
| `job.transports` | What the runner actually serves | An operator can advertise a transport the backend does not implement; the failure appears at request time. |
| `session.metering` | Whether the runner self-reports | Advertised as a gating axis; nothing verifies it matches reality. |
| `capability_id` | What workload the runner implements | Typos advertise a capability nobody serves. |
| Health probe recipe | The runner knows what "ready" means (model loaded, GPU free, queue depth) | An HTTP-status recipe approximates a fact the runner has exactly. |
| Model/workload identity in `extra` | e.g. `extra.openai.model` | Drifts from what the backend actually loaded. |

### Operator-owned facts (correctly in host-config)

`price.amount_wei`, `offering_id`, `backend.url`, capacity
(`max_in_flight`, `queue_limit`), the commercial policy axes
(`lease_*`, `refill`, `min_runway_units`, `tolerance_band_pct`,
`runway_increment_units`), `constraints`, deployment `extra`
(region, gpu class), and the decision to advertise at all.

The rule of thumb: **a runner must never be able to set its own price**,
and an operator must never have to know the runner's internals.

## The missing piece: runner self-description

The broker should be able to *ask* a runner what it implements, instead
of an operator retyping it:

- a `describe` endpoint (path operator-configured, like the others),
  returning the runner-owned facts above — capability id(s), protocol,
  descriptor schema, work unit, transports, its own paths, its emit
  cadence, its readiness semantics, and which protocol/schema **versions**
  it speaks;
- read at broker startup and on runtime reload;
- composed with operator facts into the published offering.

Then host-config collapses toward its honest content: *point at this
runner, sell it as offering X at price Y with capacity Z*.

This is squarely in scope for this repo: the broker↔runner contract is
specified in `paid-session/v1` §7, the broker is our reference
implementation, and none of it requires a runner to live here.

### The safety constraint that shapes it

Runner-declared facts flow into the **cold-key-signed manifest**. If the
broker adopted them automatically, a runner-side change would silently
change what the orchestrator advertises and sells — exactly what the sign
cycle exists to prevent, and `secure-orch-console` already classifies
axes changes as `critical`.

So the design is **read, diff, and require acknowledgement** — not
auto-adopt. A runner whose description drifts from the published manifest
should mark the tuple as needing operator review, not quietly republish.
The existing drift machinery in `orch-coordinator` is the right place for
that to surface.

## Other gaps this exposes

1. **No version negotiation.** Nothing tells the broker which protocol or
   descriptor-schema versions a runner speaks. A runner that upgrades
   `sfu-room/v1` → `v2` fails at create time with a schema mismatch
   instead of being caught at configuration time.
2. ~~**No declared `session_params` shape.**~~ **Closed.** A runner MAY now
   declare `session_params_schema` in its describe response; the broker
   relays it verbatim on `/registry/offerings` and never enforces it
   (`paid-session` §7.1.1). It is not yet carried by the signed manifest
   schema, so gateways reading a manifest still do not see it.
3. ~~**Readiness is approximated.**~~ **Closed** for readiness: a runner MAY
   declare a readiness endpoint and the broker MAY use it in place of a
   probe it would otherwise default to, with an operator-configured probe
   always winning. Runner-declared *capacity* remains unaddressed.
4. ~~**Dead config: extractors on session capabilities.**~~ **Closed.**
   Config validation now rejects `work_unit.extractor` on a
   `paid-session` capability outright ("usage comes from runner-reported
   claims; the broker never runs an extractor here") and requires it only
   for `paid-job`.

## What this is not

- Not a proposal to host runner implementations here.
- Not a proposal to certify runners here. If the protocol places
  obligations on runners — and it does — the deliverable from this repo
  is a **stated, consolidated contract**, not a test harness for other
  people's code.

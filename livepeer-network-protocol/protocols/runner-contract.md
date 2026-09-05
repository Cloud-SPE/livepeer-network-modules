---
spec_name: runner-contract
version: 1.1.0-draft
status: draft
last_updated: 2026-09-04
---

# Runner contract

A runner says what it is. This document specifies how: the runner serves
its own capability entry at a well-known path, and the agent that attaches
it to a broker reads that entry once and relays it.

It is a contract because it sits between two parties that ship
separately — whoever builds a runner image, and the `pool-member-agent`
that attaches it — and neither may guess at the other. Plan 0043 §237
named this seam ("the container's contract or an adapter profile") and
only the profile half was ever built. Plan 0045 §3 removes the profiles
and makes this the one mechanism.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as in
RFC 2119.

## 1. Who owns what

| Party | Owns | Never does |
|---|---|---|
| **Runner** (the container serving a capability) | Everything runner-attach §3.2 lists as the runner's: `capability_id`, `protocol`, `transports` or `descriptor_schemas`, `work_unit` and its `extractor`, `paths`, `readiness`, `identity`, `schema_versions`, `metering`, `heartbeat`, `session_params_schema`, `requirements`, `x-*` | Say which of the host's GPUs back it, which container it is, or whether it is being withdrawn |
| **Agent** (one per host) | `local_id`, `devices[]`, `draining`; the host's `hardware[]` and credential | Author, default, or repair any runner-owned field |
| **Pool controller / operator** | Which image runs, on which card, at what price (the template) | Author any field of the contract |

The table is the same one as runner-attach §3.2 with one line drawn
through it: the runner-owned fields are served *by the runner*, the
host-owned fields are added *by the agent*, and there is no third source.

## 2. The endpoint

A runner MUST serve

```
GET /.well-known/livepeer-runner
```

on the same base URL it serves its capability on, answering `200` with
`Content-Type: application/json` and a body that is exactly one
**capability entry** as defined in §3. The path is fixed; it is not
configurable on either side, because a configurable path is a fact
someone has to type.

The agent reads it **once per attach** — at start, and again on every
re-attach the desired-state loop triggers. It is not polled. Polling is
what plan 0043 item 11 deleted, and a contract that changes under a
running attach is an image that was replaced, which is a re-attach.

The body MUST NOT exceed 128 KiB. Runner-attach caps `x-*` at 32 KiB per
capability and `session_params_schema` at 16 KiB; a contract larger than
this is not describing a runner.

## 3. The body

The body is a JSON object carrying the runner-owned fields of
runner-attach §3.2, with the same names, types, and requirements. It
MUST NOT carry `local_id`, `devices`, or `draining`. Any other key MUST
begin with `x-`; an unrecognised key that does not is a rejection, not an
extension, because a misspelled field that decoded to nothing would
attach a runner that is not what it says it is.

Minimal job runner:

```json
{
  "capability_id": "openai:chat-completions",
  "protocol": "paid-job/v1",
  "transports": ["unary", "stream"],
  "work_unit": { "name": "tokens",
                 "extractor": { "type": "openai-usage", "field": "total_tokens" } },
  "paths": { "invoke": "/v1/chat/completions" },
  "readiness": { "type": "http-openai-model-ready", "path": "/v1/models",
                 "config": { "model": "Qwen3.6-27B" } },
  "identity": { "openai.model": "Qwen3.6-27B", "provider": "vllm" },
  "schema_versions": { "paid-job/v1": "1.0.15" },
  "x-quantization": "nvfp4"
}
```

Minimal session runner:

```json
{
  "capability_id": "video:transcode.live",
  "protocol": "paid-session/v1",
  "descriptor_schemas": ["rtmp-hls/v1"],
  "metering": "runner-reported",
  "work_unit": { "name": "output_seconds" },
  "paths": { "create": "/v1/video/live/sessions",
             "status": "/v1/video/live/sessions/{id}",
             "terminate": "/v1/video/live/sessions/{id}" },
  "readiness": { "type": "http-status", "path": "/healthz" },
  "identity": { "provider": "livepeer-live-runner" },
  "schema_versions": { "paid-session/v1": "1.0.0", "rtmp-hls/v1": "1.0.0" }
}
```

A container that serves more than one capability — the audio runner's
transcriptions and translations, a vendor-backed runner with several
models — returns a JSON **array** of such objects, one per capability,
each validated on its own; two entries MUST NOT share a `capability_id`.
The agent attaches each as its own capability entry: the first under the
container's `local_id`, the rest under `<local_id>.<n>`, and routes all of
them to the container. Between the array and `CAPABILITY_NAME`-style
selection, prefer the latter on a pool host — the pool places one
template per service, and a container that advertises two capabilities
from every service doubles the broker's view of it.

The agent performs only the check that the body could be relayed at
all: `capability_id`, `protocol`, `paths`, `work_unit.name` and
`readiness.type` present. Everything else is validated by the broker
(runner-attach §4), which names the field it rejects. Two validators of
one shape drift; the broker's is the one that matters.

## 4. What the agent does

For each runner the host declares — by URL, and nothing else — the agent:

1. fetches the contract;
2. adds `local_id` (the compose service name), `devices[]` (the GPU
   UUIDs the pool pinned this service to, intersected with the host's
   own inventory), and `draining` (from desired state);
3. relays the result as one `capabilities[]` entry of the attach
   document.

A runner whose contract cannot be fetched or does not validate is
**omitted and named**. The agent logs the container, the URL, what it
expected there, and this document. It does not fail the attach: one
image that does not adhere MUST NOT keep the host's other runners from
serving. A host with nothing resolved still attaches, hardware-only, and
is visible on the broker as connected and serving nothing — which is a
better signal than absence.

That log line is the inventory of runners that do not adhere. It is
generated by the system rather than maintained by hand; when one appears,
the runner gets rewritten, per the operator's stated policy.

## 5. What this replaces

`pool-member-agent` previously carried *adapter profiles*: an operator
declared "this container, this profile, this model" and the profile
expanded to a capability entry. The eight facts in that entry were the
runner's, transcribed into a different repository's Go — a hand-copied
fact, which is what runner-attach §3.2.1 argues against for every other
field — and changing one meant shipping a new agent to every member. The
profiles are deleted. There is no fallback: a runner serves its contract
or it does not attach.

The mechanism this most resembles, `paid-session` §7.1.1's `describe`,
was a poll the broker ran against the runner. This is a read the agent
makes once, at the one moment the answer is needed. The difference is
the whole of plan 0043 item 11.

## 6. Conformance

| Fixture | Asserts |
|---|---|
| `contract-relayed-verbatim` | Every runner-owned field of the served contract appears unchanged in the attach document; `local_id`, `devices`, `draining` are the agent's. |
| `contract-missing-omits-not-fails` | A host with one runner serving a contract and one not attaches with exactly one capability and reports the other by name and URL. |
| `contract-unknown-field-rejected` | A body with a non-`x-` key the spec does not name is refused. |
| `contract-draining-relayed` | A runner the desired state marks draining attaches with `draining: true`. |

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.1.0-draft | 2026-09-04 | The body MAY be an array of entries for a container that serves several capabilities; each is attached under a derived `local_id` (`<id>.<n>`) and routed to the container. Additive. |
| 1.0.0-draft | 2026-09-01 | Initial contract (plan 0045 §3). Supersedes `pool-member-agent` adapter profiles. |

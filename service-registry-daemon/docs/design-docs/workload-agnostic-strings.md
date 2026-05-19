---
title: Workload-agnostic capability strings
status: accepted
last-reviewed: 2026-05-19
---

# Workload-agnostic capability strings

Capability names are opaque to the registry. The daemon does not parse, route on, or interpret them. This document describes the *naming convention* operators and consumers should follow so the strings are machine-friendly across workloads.

## Why opaque?

The single biggest design constraint of this repo (core-beliefs §3) is that adding a new workload type — a new AI runner, a new transcoding profile, a new something-else-entirely — must require zero code changes here. If the registry knew what "transcoding" was, every new transcoding profile would need a code update. By treating capabilities as opaque strings, we push interpretation to the consumer where it belongs.

## Convention

`{namespace}:{operation}[-{variant}]`

Three parts:

- `namespace` — short identifier for the protocol/family. Examples: `livepeer`, `openai`, `myco`.
- `operation` — operation identifier within the namespace. Lower-case, stable, machine-friendly.
- `variant` (optional) — extra qualifier when the capability itself, not the offering, needs disambiguation.

## Reserved namespaces

These are reserved by convention to keep the network coherent. The registry doesn't enforce reservation; consumers and operators agree.

| Namespace | Owner | Examples |
|---|---|---|
| `livepeer` | Livepeer protocol | `livepeer:transcoder/h264`, `livepeer:transcoder/hevc`, `livepeer:transcoder/av1`, `livepeer:vtuber-session` |
| `openai` | OpenAI-compatible HTTP API surface | `openai:chat-completions`, `openai:embeddings`, `openai:images-generations`, `openai:audio-transcriptions` |
| `huggingface` | Hugging Face inference-API style | `huggingface:text-generation`, `huggingface:image-classification` |

Operator-defined capabilities use a namespace the operator owns (e.g. `myco:custom-pipeline-v3`). The registry is workload-agnostic — any namespace works as long as consumers recognize it.

A capability with no path component (e.g. `livepeer:vtuber-session`) names a streaming-session workload — the consumer establishes a long-lived session via its own protocol after `Select` returns the worker. The first such consumer is the external `livepeer-vtuber-project` (not vendored in this monorepo).

Anyone may publish a manifest with any string. Consumers ignore strings they don't recognize.

## Worker-known formats

The capability strings used in the wild today, by source:

- **Current capability-broker / orch-coordinator examples in this repo** use
  colon-form OpenAI capability IDs such as `openai:chat-completions`,
  `openai:embeddings`, and `openai:audio-transcriptions`. Gateways and
  resolvers should treat the published string as opaque and reuse it exactly.
- **Older bridge / worker examples** in this repo and adjacent repos may still
  show slash-form IDs like `openai:/v1/chat/completions`. Treat those as
  historical examples or compatibility-test inputs, not the preferred current
  shape.
- **go-livepeer transcoding** historically used a bitmask `Capability_*` enum (`Capability_TextToImage = 27`, etc.). The canonical form in this stack is the namespaced string per above (`livepeer:ai/text-to-image` and friends — see [`../references/capability-enum-mapping.md`](../references/capability-enum-mapping.md)). Consumers that need the integer form do their own mapping.

## Models

The `offerings` array on a capability is intended for capability instances that have a model or preset dimension (any AI inference, certain transcoding presets). It's optional. A capability with no `offerings` represents itself.

## Constraints

The `constraints` blob on an offering is fully opaque. Common keys we've seen in practice (advisory, not enforced):

- `loaded`: bool — node currently has the offering loaded
- `min_capacity`: int — concurrent requests supported
- `runner_version`: string
- `gpu`: string — GPU class (e.g., `a100-40gb`)

Consumers SHOULD treat unknown keys as a soft signal (informational) and known keys as a hard signal (filter on).

## Don't:

- Encode pricing in the capability name. Use `offerings[].price_per_work_unit_wei`.
- Encode geo in the capability name. Use node-level `extra` if geo-aware
  routing metadata needs to publish.
- Include whitespace, control characters, or non-printable bytes.
- Encode operator identity. The chain already binds `eth_address` to the operator.

## Migration from go-livepeer's enum

If at some point we need bidirectional mapping with go-livepeer's `Capability_*` enum, the agreed-on table lives in [docs/references/capability-enum-mapping.md](../references/capability-enum-mapping.md). It's a translation reference; neither side is canonical for the other.

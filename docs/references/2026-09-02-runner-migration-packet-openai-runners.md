# Runner migration packet — `livepeer-modules-openai-runners`

Date: 2026-09-02. For the owner of `livepeer-modules-openai-runners`
(`openai-chat-runner`, `openai-audio-runner`, `openai-embeddings-runner`,
`openai-image-generation-runner`, `openai-tts-runner`, `rerank-runner`).
Based on a read of the repository on 2026-09-01/02; what is quoted below is
from your source. Tracked as `lnm-9x7` in `livepeer-network-modules`.

**Dependency order (decision 4):** nothing in this packet waits on anything
else. Each image is independently verifiable the moment it ships: attach it
to a broker, watch it certify, watch its offer advertise.

## The short version

The broker no longer dials runners or reads `GET /<capability>/options`. A
runner **declares itself** with one JSON document at
`GET /.well-known/livepeer-runner`, the pool member agent relays it, and the
broker validates it and freezes the offer from it. Your `/options` payload
already carries most of the same facts under different names; this is a
rename and a reshape, not new information. Two further changes: capability
ids move to the catalog's colon form, and two runners must count their work.

Governing rule from the operator: a runner that does not serve this contract
is not sold. There is no compatibility path for `/options`.

## What you can delete

| What you have | Why it existed | What replaces it |
|---|---|---|
| `GET /<capability>/options` on every image, and `RUNNER-INVARIANTS.md` listing it as required | The broker dialled each runner and merged the payload into host config | `GET /.well-known/livepeer-runner` (below). Delete the options route once the contract is served; nothing reads it. |
| `BROKER-CONTRACT.md` | Described the broker dialling runners and merging `/options` | Rewrite against `livepeer-network-protocol/protocols/runner-contract.md` and `runner-attach.md` §3.2. The broker never dials a runner; the agent does, once, for the contract, and thereafter relays requests over its tunnel. |
| The v0 `streaming_modes` / interaction-mode vocabulary in `/options` | Mode negotiation | `transports: ["unary", "stream"]` in the contract. Modes are gone. |

## What changes

### 1. Serve the contract

Every image serves `GET /.well-known/livepeer-runner` returning the
runner-owned half of a capability entry (`runner-contract.md` §3). For the
chat proxy:

```json
{
  "capability_id": "openai:chat-completions",
  "protocol": "paid-job/v1",
  "transports": ["unary", "stream"],
  "work_unit": { "name": "tokens",
                 "extractor": { "type": "openai-usage", "field": "total_tokens" } },
  "paths": { "invoke": "/v1/chat/completions" },
  "readiness": { "type": "http-openai-model-ready", "path": "/v1/models",
                 "config": { "model": "<served model name>" } },
  "identity": { "openai.model": "<served model name>", "provider": "vllm" },
  "schema_versions": { "paid-job/v1": "1.0.15" },
  "x-quantization": "nvfp4"
}
```

Mapping from your `/options` payload: `capability` → `capability_id`;
`served_model_name` → `identity.openai.model`; `features.streaming` →
`transports`; anything else you advertise → an `x-*` key (relayed
verbatim; the catalog decides whether to publish it). The body MUST NOT
carry `local_id`, `devices`, or `draining` — the agent adds those. An
unknown key that is not `x-`-prefixed rejects the document.

`identity.openai.model` is what the catalog's `match` selects on. It is the
name a caller puts in `"model"` — for the chat runner the value the operator
configured as the served name, for the audio runner `whisper-large-v3`, for
TTS `kokoro`. The HF path is not the identity.

### 2. Capability ids: colon form

The catalog, both gateways, and the protocol examples use the colon form.
Your defaults are the hyphen form and match no template. Change the
defaults; keep `CAPABILITY_NAME` overridable.

| Image | Today | Contract `capability_id` |
|---|---|---|
| `openai-chat-runner` | `openai-chat-completions` | `openai:chat-completions` |
| `openai-audio-runner` | `openai-audio-transcriptions` and `openai-audio-translations` | `openai:audio-transcriptions` **and** `openai:audio-translations` |
| `openai-embeddings-runner` | `openai-text-embeddings` | `openai:embeddings` |
| `openai-image-generation-runner` | `image-generation` | `openai:images-generations` |
| `openai-tts-runner` | `openai-audio-speech` | `openai:audio-speech` |
| `rerank-runner` | `rerank` | `text:rerank` |

The rule (runner-attach §3.2, "Capability id vocabulary"): `openai:` plus
the endpoint name with `/` folded to `-`, when the capability IS the OpenAI
endpoint. Rerank is not an OpenAI endpoint, so it takes the product domain
and declares its identity under the plain key: `"identity": { "model":
"zerank-2" }`, not `openai.model`.

### 3. The audio runner declares two capabilities

`openai-audio-runner` serves `/v1/audio/transcriptions` and
`/v1/audio/translations` from one process. Its contract document is one
JSON object per capability entry today; the agent accepts **an array** of
entries at `/.well-known/livepeer-runner` for a container that serves more
than one (the `capabilities[]` shape of runner-attach §3.2, minus the
agent-owned fields). Return two entries — same identity, same readiness,
different `capability_id` and `paths.invoke`. The catalog has a template
for each (`openai-audio-transcriptions-whisper-large-v3`,
`openai-audio-translations-whisper-large-v3`) and each offer matches one.

### 4. Two runners must count

- **`rerank-runner`** emits no usage at all — no `X-Livepeer-Work-Units`,
  no usage block. The catalog meters it per document
  (`text-rerank-zerank-2`, work unit `documents`). Declare an extractor in
  the contract: `{"type": "request-formula", "expression": "len($.documents)"}`
  if the broker's `request-formula` accepts it, else emit the header with
  the document count as the audio runner does. The certification usage
  step fails loudly until one of the two is true.
- **`openai-embeddings-runner`** already parses `usage.total_tokens`
  (`USAGE_FIELD`); declare it: `{"type": "openai-usage", "field":
  "total_tokens"}`, and keep the header — the extractor reads the body, the
  header is the claim a gateway sees.

### 5. Streams carry the claim in a trailer

Unchanged from the 2026-08-19 gateway packet, restated because it is the
runner's half: on `stream`, `Livepeer-Work-Units` is an HTTP trailer, and
the body must be length-unknown chunked so the trailer can follow it.

## What is *not* fixed by this work

- Passthrough to a hosted OpenAI-compatible endpoint (`UPSTREAM_KIND`
  pointing off-host). The operator's decision: pool members bring GPUs, not
  API keys; passthrough is the orchestrator's own business and needs no
  runner change here.
- Image tags. The catalog omits `runner_compose.image` for every OpenAI
  workload until you tell us the tag that ships the contract (`lnm-v12`).

## Verifying

```sh
cd livepeer-network-protocol/conformance
go run ./cmd/livepeer-conformance --broker-url http://<broker>:8080 --serve-runner <image>
```

`--serve-runner` runs your image in a container, fetches its contract,
attaches it, and walks the job scenarios against it. Then, against a real
pool: attach, confirm the exception queue is empty for your host, confirm
the offer appears on `GET /v1/offerings` with `identity` frozen from your
document.

## What we need from you

1. The tag of each image that serves the contract, per row of the table.
2. **Does each image run on `sm_61` / CUDA 12.x (a GTX 1080)?** The catalog
   admits the 1080 for no AI workload today because that is your fact, not
   ours (decision 9). A yes is a one-line class addition on our side.
3. A pointer to the rewritten `BROKER-CONTRACT.md` so we can check the two
   documents agree.

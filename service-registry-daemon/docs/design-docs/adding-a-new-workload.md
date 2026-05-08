---
title: Adding a new workload to the suite
status: verified
last-reviewed: 2026-04-29
---

# Adding a new workload to the suite

The Livepeer suite is **workload-agnostic**: capability strings are opaque to
this daemon, the manifest schema accommodates arbitrary `extra`/`constraints`
JSON, and the resolver doesn't interpret what's published. Adding a new
workload (e.g. live transcription, image upscaling, RAG inference, anything
that hasn't been built yet) does not require a code change in this repo.

This page is the recipe.

## Quick checklist

1. Pick a capability name following [`workload-agnostic-strings.md`](workload-agnostic-strings.md).
2. Pick a `work_unit` — what you bill by.
3. Decide whether pricing is published in the manifest or set by an
   intermediate bridge.
4. Build a worker (HTTP service + payment validation).
5. Optionally build a bridge (customer-facing API + resolver client).
6. Drop the worker into archetype A: orch-coordinator pre-fills the operator's
   roster from the worker's `/registry/offerings`, secure-orch signs, the
   manifest publishes.

No coordinator change, no daemon change, no proto regen.

## Step-by-step

### 1. Capability name

The capability is an **opaque string** the registry never interprets. Convention
(per [`workload-agnostic-strings.md`](workload-agnostic-strings.md)):

- `<vendor>:<api-path>` for established API shapes — e.g.
  `openai:/v1/chat/completions`, `openai:/v1/audio/transcriptions`.
- `livepeer:<workload>/<variant>` for native Livepeer workloads — e.g.
  `livepeer:transcoder/h264`, `livepeer:vtuber-session`.
- `<your-org>:<workload>` for new workloads. Pick a stable identifier that
  consumers can match exactly.

The string ends up in `nodes[].capabilities[].name`.

### 2. `work_unit`

What gets metered when payment settles. Examples already in flight:

| Workload | `work_unit` | Notes |
|---|---|---|
| LLM chat completions | `token` | Input + output tokens combined |
| Embeddings | `token` | Input tokens |
| Image generation | `image_step_megapixel` | Composite unit |
| TTS / audio synthesis | `character` | Output characters |
| Speech-to-text | `audio_second` | Input audio seconds |
| Video transcoding | `frame` or `pixel-second` | Per repo convention |
| Streaming sessions (vtuber) | `second` | Wall-clock |

Pick something the worker can count cleanly. The unit appears in
`nodes[].capabilities[].work_unit` and as the denominator of
`offerings[].price_per_work_unit_wei`.

### 3. Pricing pattern: manifest-embedded vs bridge-mediated

Two patterns are both valid (see network-suite plan 0003 §Decision 3):

- **Manifest-embedded pricing.** Orch publishes
  `offerings[].price_per_work_unit_wei` in the signed manifest. Gateways read
  it as the wholesale price input to routing (then add margin to compute
  customer-facing prices). Default for direct workloads (vtuber-gateway
  pattern).
- **Bridge-mediated pricing.** Orch leaves `price_per_work_unit_wei` empty;
  customer-facing prices live on a separate bridge that fronts the workers
  (openai-livepeer-bridge pattern). Note: in v3.0.1 the openai-bridge
  ALSO reads manifest pricing — see plan 0003 §G — so empty prices mean
  "this offering is opted out of routing," not "the bridge will set it."
  Default for workloads with a customer-facing rate-card front door.

Pick whichever fits your business model. Workers don't care; only the
operator's roster row decides whether prices populate.

### 4. Build a worker

Templated by [`openai-worker-node`](https://github.com/Cloud-SPE/openai-worker-node),
[`vtuber-worker-node`](https://github.com/Cloud-SPE/vtuber-worker-node), and
[`video-worker-node`](https://github.com/Cloud-SPE/video-worker-node). Roughly:

- HTTP service that handles the workload's traffic.
- Co-located `payment-daemon` (receiver mode) over a unix socket — validates
  the payment ticket attached to each paid request.
- `worker.yaml` config declaring `capabilities[]` with the names, work_units,
  and offerings (model/preset/tier ids + per-unit prices) the worker serves.
- One required worker-advertisement endpoint:
  - `/registry/offerings` — uniform modules-canonical capability
    fragment. See
    [worker-offerings-endpoint.md](worker-offerings-endpoint.md).
- Prometheus `/metrics`.

Crucially, the worker is **registry-invisible** under archetype A. It does
not dial this daemon directly. The orch-coordinator scrapes
`/registry/offerings` and the operator confirms before anything publishes.

### 5. Optionally build a bridge

A bridge is a payer-side gateway with a customer-facing surface (e.g.
OpenAI-compatible API, vtuber session endpoint, RTMP/HLS streaming). It runs
a co-located `service-registry-daemon` (resolver mode) sidecar to discover
orchestrators and a `payment-daemon` (sender mode) to mint payment tickets.

Templated by `livepeer-openai-gateway`, `livepeer-vtuber-gateway`,
`livepeer-video-gateway`. Optional — a workload can run without a bridge if
gateways speak the workload's API directly to workers.

### 6. Drop into archetype A

Once your worker is up:

1. Operator adds the worker to the orch-coordinator roster (URL + optional
   Prom URL + optional offerings auth token).
2. Coordinator scrapes `/registry/offerings`, shows operator a draft,
   operator confirms.
3. When the operator wants to publish, they download
   `raw-registry-manifest.json` from
   the coordinator, hand-carry it to the secure-orch host, run
   `livepeer-registry-refresh` against this daemon's publisher mode, and
   carry the signed manifest back to the coordinator for atomic-swap onto
   the public path.
4. Gateways using a resolver sidecar discover the new offerings on next
   refresh; their workload routing code queries
   `Resolver.Select(capability, offering, ...)` and routes to whichever orch
   advertises a matching offering at an acceptable price.

Done. New workload on the network without a coordinator change, a daemon
change, or a proto regen.

## What you do NOT need to do

- ❌ Add code to this daemon.
- ❌ Regenerate protos.
- ❌ Coordinate a manifest schema change.
- ❌ Update any gateway repo's discovery logic — `Resolver.Select` is
  capability-string-based, opaque.
- ❌ Update `livepeer-orch-coordinator` — its roster is structured against the
  modules-canonical capability shape, which already accommodates any `extra`
  / `constraints` blob your workload needs.

## What you DO need to do (org-internal)

- Open per-repo exec-plans in your worker repo and (if applicable) bridge repo
  declaring the new capability + workload contract.
- Add a tech-debt entry in this daemon's `docs/exec-plans/tech-debt-tracker.md`
  if the new workload exposes a quirk worth tracking (e.g. unusual `work_unit`
  semantics) — purely informational.
- If your workload introduces a new naming convention for capability strings
  that's worth standardizing, add it to
  [`workload-agnostic-strings.md`](workload-agnostic-strings.md) so other
  workload authors converge.

## See also

- [`workload-agnostic-strings.md`](workload-agnostic-strings.md) — capability
  string conventions.
- [`worker-offerings-endpoint.md`](worker-offerings-endpoint.md) — the
  uniform endpoint workers expose for orch-coordinator scrape.
- [`manifest-schema.md`](manifest-schema.md) — the wire shape your offerings
  end up in.
- `livepeer-network-suite` plan 0003 — cross-repo coordination story for the
  v3.0.1 archetype-A reset.

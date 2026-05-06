# DESIGN

Component-local design summary. Cross-cutting design lives at the repo root in
[`../docs/design-docs/`](../docs/design-docs/). The full architectural overview
is in [`../docs/design-docs/architecture-overview.md`](../docs/design-docs/architecture-overview.md).

This file points at the local design docs and gives a one-page mental model.

## What this component is

A single Go binary that **brokers** between:

- **Above**: the Livepeer network's payment + discovery plane (paid HTTP/WS
  traffic from gateways with `Livepeer-Payment` envelopes).
- **Below**: arbitrary backends — local containers (vLLM, Ollama, Whisper,
  Kokoro, FFmpeg subprocess), LAN services, third-party APIs (OpenAI,
  Anthropic, OpenRouter), or anything else operator-declared.

The broker contains **zero capability-specific code**. All workload knowledge
lives in:

1. **Mode adapters** — implementations of the spec's interaction modes
   (`http-reqresp@v0` first; others per plan 0006).
2. **Extractor implementations** — declarative work-unit recipes
   (`response-jsonpath` first; others per plan 0007).
3. **The `host-config.yaml` operator config** — capability ID, offering ID,
   pricing, backend descriptors, declared extractors.

## Wire spec compliance

The broker MUST conform to the wire spec at
[`../livepeer-network-protocol/`](../livepeer-network-protocol/):

- [`manifest/schema.json`](../livepeer-network-protocol/manifest/schema.json)
  defines the `/registry/offerings` shape (sans signature — signing is the
  orch-coordinator's job).
- [`headers/livepeer-headers.md`](../livepeer-network-protocol/headers/livepeer-headers.md)
  defines the `Livepeer-*` HTTP header conventions.
- [`modes/<mode>.md`](../livepeer-network-protocol/modes/) defines per-mode
  wire shape.
- [`extractors/<extractor>.md`](../livepeer-network-protocol/extractors/)
  defines the extractor recipes.

When code disagrees with the spec, the spec wins. Conformance is verified by
[`../livepeer-network-protocol/conformance/`](../livepeer-network-protocol/conformance/).

## Internal architecture

See [`docs/design-docs/architecture.md`](./docs/design-docs/architecture.md)
for the planned package layout, request lifecycle, and dispatch flow.

## What stays out of this binary

- **Signing** the manifest — orch-coordinator's job. Broker just publishes
  the bare offerings list at `/registry/offerings`.
- **Resolver logic** — gateway-side concern, lives in
  `service-registry-daemon`.
- **TLS termination** — operator-side reverse proxy (Cloudflare Tunnel,
  Traefik, Cloudflare LB) per repo-root requirement R2.
- **Customer auth** — gateway-side concern.
- **Capability semantics** — the broker doesn't know what a "chat
  completion" or a "doggo bark" actually is.

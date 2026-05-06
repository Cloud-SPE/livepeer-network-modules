# Plan 0002 — Define the initial interaction-mode specifications

**Status:** queued (becomes active when plan 0001 closes)
**Opened:** 2026-05-06
**Owner:** TBD

## Goal

Define the six initial interaction modes as language-neutral specifications. The spec
**lives at `<repo-root>/livepeer-network-protocol/`** as a top-level subfolder of this
monorepo; it can be extracted to a standalone repo later once it stabilizes.

## Why

Interaction modes are the only place workload-specific knowledge is permitted to leak
in this architecture (per requirement R8 / core belief #1). Getting the typology right
is the single biggest leverage point in the rewrite — every other layer depends on it.

The initial six are:

1. `http-reqresp` — one HTTP req → one HTTP resp, JSON or binary.
2. `http-stream` — request → SSE / chunked-response stream.
3. `http-multipart` — multipart upload → JSON or binary response.
4. `ws-realtime` — bidirectional WebSocket.
5. `rtmp-ingress-hls-egress` — RTMP in → HLS manifest+segments out.
6. `session-control-plus-media` — HTTP session-open → long-lived media plane.

## Open questions to resolve

1. ~~**Repo home.**~~ **Resolved 2026-05-06**: spec lives at
   `<repo-root>/livepeer-network-protocol/` as a top-level subfolder of this monorepo.
   Extractable to standalone later.
2. ~~**Mode-version semantics.**~~ **Resolved 2026-05-06**: hybrid SemVer.
   - **Spec-wide SemVer** for cross-cutting parts — manifest schema, header
     conventions, payment envelope shape, extractor library envelope.
   - **Per-mode SemVer** under `modes/` for each mode independently.
   - Manifest tuples carry both: `spec_version: "1.0"` at the manifest root +
     `interaction_mode: "http-stream@v1"` per capability.
   - Gateway/broker can declare support for multiple versions of the same mode in
     parallel.
   - SemVer rules apply identically to both axes.
3. ~~**Conformance test framework.**~~ **Resolved 2026-05-06**: hybrid (declarative
   YAML fixtures + Go runner) shipped as a **Docker image**, per core belief #15.
   - Image: `tztcloud/livepeer-conformance:<tag>` (working name; revisit at first publish).
   - Tag matches the spec-wide SemVer.
   - Image bundles the runner binary + the `fixtures/` folder + mock-backend +
     mock-payment-daemon helpers.
   - Layout: `conformance/{fixtures,runner,compose.yaml,Makefile,README.md}`.
   - Implementers never install Go locally; they pull the image and run `make` or
     `docker run`. Networking modes for the implementation under test: `host.docker.internal`
     (host process) or shared docker network (container).
4. ~~**Reference implementation language.**~~ **Resolved 2026-05-06**: split per side.
   - **Broker reference: Go.** Image `tztcloud/livepeer-capability-broker:<tag>` +
     Go module under `capability-broker/`. Justified by streaming/RTMP ecosystem
     fit, conformance-runner co-location, and continuity with the existing suite's
     worker pattern.
   - **Gateway middleware reference: TypeScript.** npm package
     `@tztcloud/livepeer-gateway-middleware` under `gateway-adapters/`. Justified by
     the first adopter (OpenAI-compatible gateway) being TS and Fastify-shaped.
   - Asymmetric distribution is correct: broker is a service (ships as image);
     middleware is library code embedded in a gateway service (ships as npm). Core
     belief #15 applies to services, not libraries.
   - Second-language reference impls (Rust broker, Python gateway adapter, etc.)
     welcome later as community contributions; not v1 critical path.
5. ~~**Headers + payment envelope.**~~ **Resolved 2026-05-06** —
   `livepeer-network-protocol/headers/livepeer-headers.md` accepted. Defines:
   - 5 required request headers: `Livepeer-Capability`, `Livepeer-Offering`,
     `Livepeer-Payment`, `Livepeer-Spec-Version`, `Livepeer-Mode`.
   - 1 optional request header: `Livepeer-Request-Id`.
   - 4 response headers: `Livepeer-Backoff`, `Livepeer-Work-Units`,
     `Livepeer-Health-Status`, `Livepeer-Error`.
   - 9 machine-readable error codes.
   - Broker forwarding behavior (strip `Livepeer-*` before backend; inject
     backend-specific auth from `host-config.yaml`).
   - `Livepeer-Payment` envelope adds `(capability_id, offering_id,
     expected_max_units)` to the existing payment-daemon protobuf shape — a
     payment-daemon decoupling change that lands when phase 4 of the roadmap
     activates.

## Outcomes (proposed)

Decisions:
- [x] Spec repo location → `<repo>/livepeer-network-protocol/`.
- [x] SemVer policy → hybrid (spec-wide SemVer + per-mode SemVer).
- [x] Conformance test framework → fixtures + Go runner shipped as Docker image
  (`tztcloud/livepeer-conformance`).
- [x] Reference implementation languages → Go for broker (`capability-broker/`),
  TypeScript for gateway middleware (`gateway-adapters/`).
- [x] Subfolder scaffolded — `livepeer-network-protocol/` with README, VERSION
  (`0.1.0`), PROCESS, plus placeholder READMEs in `manifest/`, `modes/`,
  `extractors/`, `headers/`, `conformance/`.

Artifacts:
- [x] `headers/livepeer-headers.md` spec written and accepted.
- [x] Manifest JSON Schema published (`manifest/schema.json` + example + changelog).
- [x] Six mode specs written and accepted (`http-reqresp`, `http-stream`,
  `http-multipart`, `ws-realtime`, `rtmp-ingress-hls-egress`,
  `session-control-plus-media`).
- [x] `extractors/*.md` for the initial six extractors drafted (`openai-usage`,
  `response-jsonpath`, `request-formula`, `bytes-counted`, `seconds-elapsed`,
  `ffmpeg-progress`).
- [ ] Conformance runner + fixtures + Dockerfile + Makefile + compose.yaml.
- [ ] At least one reference implementation (broker side) demonstrably passing the
  conformance suite for one mode.

All decisions are now resolved; remaining work is the conformance scaffold and the
first reference impl. Mode specs and extractor specs are present and reviewable.

## Out of scope

- Implementing the broker beyond a conformance-test reference.
- Modifying any existing `livepeer-network-suite` submodule.
- Defining new modes beyond the initial six (governance for that lives in the spec
  repo's `PROCESS.md`).

## Done condition

A third-party developer can read the spec repo's README, pick a mode, write a backend
that conforms, and have it work with the reference broker — without consulting any
livepeer Go library.

## Notes

The spec repo's design intent is captured in
[`../../references/2026-05-06-architecture-conversation.md`](../../references/2026-05-06-architecture-conversation.md)
§6.11 ("The spec repo"). Don't re-litigate the structure question; treat that as the
starting layout and improve it.

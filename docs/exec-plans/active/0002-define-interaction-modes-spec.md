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
   - Image: `tztcloud/lnp-conformance:<tag>` (working name; revisit at first publish).
   - Tag matches the spec-wide SemVer.
   - Image bundles the runner binary + the `fixtures/` folder + mock-backend +
     mock-payment-daemon helpers.
   - Layout: `conformance/{fixtures,runner,compose.yaml,Makefile,README.md}`.
   - Implementers never install Go locally; they pull the image and run `make` or
     `docker run`. Networking modes for the implementation under test: `host.docker.internal`
     (host process) or shared docker network (container).
4. **Reference implementation language.** Go for the broker side; Go or TS for the
   gateway side? Both? Pick to match where the first production implementations will
   live.
5. **Headers + payment envelope.** The conversation specified
   `Livepeer-Capability`, `Livepeer-Offering`, `Livepeer-Payment`, plus
   `Livepeer-Backoff` for the 503 path. Need a `headers/livepeer-headers.md` spec
   before any mode spec lands.

## Outcomes (proposed)

Decisions:
- [x] Spec repo location → `<repo>/livepeer-network-protocol/`.
- [x] SemVer policy → hybrid (spec-wide SemVer + per-mode SemVer).
- [x] Conformance test framework → fixtures + Go runner shipped as Docker image
  (`tztcloud/lnp-conformance`).
- [ ] Reference implementation language(s).

Artifacts:
- [ ] `headers/livepeer-headers.md` spec written.
- [ ] Six mode specs written, each with: wire format, payment cadence (if applicable),
  required `extra`/`constraints` fields, conformance test fixtures.
- [ ] `extractors/*.md` for the initial six extractors (`openai-usage`,
  `response-jsonpath`, `request-formula`, `bytes-counted`, `seconds-elapsed`,
  `ffmpeg-progress`).
- [ ] Manifest JSON Schema published.
- [ ] At least one reference implementation (broker side) demonstrably passing the
  conformance suite for one mode.

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

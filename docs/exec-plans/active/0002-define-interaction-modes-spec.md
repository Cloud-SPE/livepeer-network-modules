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
2. **Mode-version semantics.** Each mode gets its own SemVer (`http-stream@v1`)? Or a
   single repo SemVer covers all modes? Per-mode versioning is more honest but more
   work to track.
3. **Conformance test framework.** WireMock-style fixtures? Pact-style contract tests?
   Custom Go runners? Pick one before the first mode spec is written.
4. **Reference implementation language.** Go for the broker side; Go or TS for the
   gateway side? Both? Pick to match where the first production implementations will
   live.
5. **Headers + payment envelope.** The conversation specified
   `Livepeer-Capability`, `Livepeer-Offering`, `Livepeer-Payment`, plus
   `Livepeer-Backoff` for the 503 path. Need a `headers/livepeer-headers.md` spec
   before any mode spec lands.

## Outcomes (proposed)

- [ ] Decision recorded: spec repo location and SemVer policy.
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

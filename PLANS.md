# PLANS

Current state of work in this repo, plus pointers to active plans.

## Current state

**Phase 0** — docs-and-spec scaffold. No production code. The repo is a system of record
for the architectural intent that motivates the rewrite.

**Repo shape: monorepo for now.** All components live as top-level subfolders here;
extraction to standalone repos is a v2 concern. See [`README.md`](./README.md) §"Repo
shape" for the planned component list.

What exists:

- This scaffold (AGENTS.md / DESIGN.md / PRODUCT_SENSE.md / README.md / CLAUDE.md).
- Core beliefs, requirements, and an architecture-overview design-doc.
- Full conversation provenance in `docs/references/2026-05-06-architecture-conversation.md`.
- Reference: `docs/references/openai-harness.pdf` (the operating-model template).

What does not exist yet:

- Any code.
- The `livepeer-network-protocol/` subfolder (spec contents — planned outcome of plan 0002).
- A capability-broker reference implementation.
- Any change to the existing `livepeer-network-suite`.

## Active plans

Numbered `docs/exec-plans/active/000N-*.md`. **None active right now** — plans
0001–0004 + 0006 all closed. The broker now serves three of the six modes
(`http-reqresp`, `http-stream`, `http-multipart`); the conformance runner
exercises all three end-to-end via compose.

The next sequenced workstreams are queued (open one when ready):

- **Plan 0005** — real `payment-daemon` integration (replaces the broker's
  mock; introduces protobuf decoding + `expected_max_units` from the
  envelope).
- ~~**Plan 0006**~~ — completed 2026-05-06. Narrowed scope to
  `http-stream` + `http-multipart`. Three remaining modes split into
  0010 / 0011 / 0012.
- **Plan 0007** — additional extractors (`openai-usage`, `request-formula`,
  `bytes-counted`, `seconds-elapsed`, `ffmpeg-progress`).
- **Plan 0008** — `gateway-adapters/` TS reference middleware.
- **Plan 0009** — OpenAI-compat gateway migration brief execution.
- **Plan 0010** — `ws-realtime@v0` driver pair (broker + runner). Adds
  WebSocket lifecycle and the interim-debit payment cadence pattern.
- **Plan 0011** — `rtmp-ingress-hls-egress@v0` driver pair. Requires RTMP
  listener + FFmpeg subprocess + HLS sink.
- **Plan 0012** — `session-control-plus-media@v0` driver pair (the vtuber
  shape: session-open + control-plane WS + capability-defined media plane).

Completed plans live in [`docs/exec-plans/completed/`](./docs/exec-plans/completed/).

## Roadmap (rough; subject to change)

| Phase | Outcome | Component subfolder | Status |
|---|---|---|---|
| 0 | Docs-and-spec scaffold + conversation provenance | (root) | ✅ completed (plan 0001) |
| 1 | Interaction-mode specs published as a subfolder | `livepeer-network-protocol/` | ✅ completed (plan 0002) |
| 2 | Capability-broker reference implementation (Go) | `capability-broker/` | ✅ completed (plan 0003) |
| 2.5 | Conformance runner mode drivers | `livepeer-network-protocol/conformance/runner/` | ✅ completed (plan 0004) |
| 3 | Coordinator UX rework — capability-as-roster-entry | `orch-coordinator/` | not started |
| 4 | Real `payment-daemon` integration | `payment-daemon/` | not started (plan 0005) |
| 5a | HTTP-family mode drivers (`http-stream`, `http-multipart`) | `capability-broker/`, `runner/` | ✅ completed (plan 0006) |
| 5b | `ws-realtime` mode driver | `capability-broker/`, `runner/` | not started (plan 0010) |
| 5c | `rtmp-ingress-hls-egress` mode driver | `capability-broker/`, `runner/` | not started (plan 0011) |
| 5d | `session-control-plus-media` mode driver | `capability-broker/`, `runner/` | not started (plan 0012) |
| 6 | Additional extractors | `capability-broker/` | not started (plan 0007) |
| 7 | Gateway-side per-mode adapters | `gateway-adapters/` | not started (plan 0008) |
| 8 | OpenAI-compat gateway migration | (root `docs/`) | not started (plan 0009) |

Phases 1–5 are independently shippable; phase 6 is gated on at least one
production gateway adopting the new shape. Components can be extracted from
this monorepo to standalone repos at any phase boundary.

## Versioning

Pre-1.0.0 until the first release is cut. **v1.0.0 = the first release of this
monorepo.** Components inside the monorepo do not have independent versions yet; when
a component is extracted to a standalone repo, its versioning becomes its own concern.
Until extraction, the monorepo's tag is the single coordinated release artifact for
everything in it.

This repo's release line is **independent of `livepeer-network-suite`**. The two share
no submodules, no pinned SHAs, and no schedule. See core belief #14.

## Tracking debt

[`docs/exec-plans/tech-debt-tracker.md`](./docs/exec-plans/tech-debt-tracker.md). Empty
at scaffold time; append as debt accumulates.

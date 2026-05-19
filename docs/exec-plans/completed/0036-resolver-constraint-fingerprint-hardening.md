---
plan: 0036
title: Resolver constraint-fingerprint hardening + pool publishing defaults + broker-quote-rejected surface
status: completed
phase: shipped
opened: 2026-05-19
owner: harness
related:
  - "completed plan 0018 — orch-coordinator design"
  - "completed plan 0027 — layered route health and check placement"
  - "completed plan 0029 — pool-node design"
  - "completed plan 0030 — pool backend scoring and selection"
  - "completed plan 0033 — pool control-plane onboarding and assignment"
  - "active plan 0034 — priced funding and final usage settlement"
---

# Plan 0036 — Resolver constraint-fingerprint hardening + pool publishing defaults + broker-quote-rejected surface

## Completion summary

This plan shipped on 2026-05-19, same day as opening.

Landed changes:

- `service-registry-daemon/internal/runtime/grpc/convert.go` —
  `fingerprintCanonicalJSON` now canonicalizes absent/empty input to
  `{}` before hashing, so `SelectedRoute.ConstraintFingerprint` is
  always a non-nil deterministic digest. A populated constraint block
  still re-marshals through JSON canonicalization and cannot collide
  with the empty-object digest. Locked in by
  `TestSelectedRouteFromResolvedNode_EmptyConstraints` in
  `convert_test.go`. Spec updated at
  `service-registry-daemon/docs/product-specs/grpc-surface.md`.
- `pool-controller/internal/service/brokerrender/render.go` — rendered
  `BrokerCapability.Constraints` is now `map[string]any{}` instead of
  nil when the operator declared no constraints; the YAML key is always
  emitted. Test:
  `TestRenderEmitsEmptyConstraintsBlock` in
  `brokerrender/render_test.go`.
- `capability-broker/internal/server/registry/offerings.go` —
  `/registry/offerings` always serializes `"constraints": {}` for
  capabilities without configured constraints; downstream
  orch-coordinator scrapes pick this up verbatim. Test:
  `TestBuildOfferings_EmitsEmptyConstraintsBlock` in
  `registry/offerings_test.go`.
- `openai-gateway/src/service/routeDispatch.ts` — dispatch attempts
  whose underlying error matches a quote-rejection signal (any of
  `quote_ref*`, `constraint_fingerprint`, `route_fingerprint`, or
  `missing quote fingerprints`) now surface as a
  `LivepeerBrokerError` with `code: "broker_quote_rejected"`. The
  generic "no route candidates" path is unchanged.
- `openai-gateway/src/livepeer/errors.ts` — new helper
  `gatewayHttpStatusFor()` preserves 503 for `broker_quote_rejected`
  while continuing to squash other upstream 5xx into 502. Wired into
  `chat-completions`, `embeddings`, `images-generations`,
  `audio-speech`, and `audio-transcriptions` route handlers.
- Tests: `openai-gateway/test/route-dispatch.test.ts` covers the
  matcher, the HTTP-status mapping, and the carve-out for
  `broker_quote_rejected` vs upstream 5xx.

Verification:

- `go test ./...` green in `service-registry-daemon`,
  `capability-broker`, `pool-controller`, and `payment-daemon`.
- `pnpm test` green in `openai-gateway` (60/61; the single failing
  case `payment.buildPayment …` was already failing on `main` before
  this branch and is unrelated to this plan).

Operational note: the open-pool `zerank-2-default` rerank capability
that surfaced this incident is now unblocked without requiring the
operator to re-sign their manifest. The pool publishing default also
prevents the class of bug from recurring against any future producer
that omits a constraints block.

## 1. Problem

A live production incident on the `open-pool.com` coordinator surfaced an
asymmetry in the resolver's quote/route fingerprinting. Two coordinators
publish the same shape of manifest, but only one of them publishes
offerings with a `constraints` block:

| Capability | Coordinator | `constraints` present? | Outcome |
|---|---|---|---|
| `rerank` / `zerank-2-default` | `coordinator.open-pool.com` | no | resolver `SelectMany` returns `FailedPrecondition: resolver selected route missing quote fingerprints` → gateway surfaces 503 `no route candidates` |
| `openai:chat-completions` / `Qwen3.6-27B` | `coordinator.xodeapp.xyz` | `{gpu:"5090", tier:"standard"}` | works |
| `daydream:scope:v1` / `default` | `coordinator.xodeapp.xyz` | `{gpu_vendor:"NVIDIA", tier:"standard"}` | works |

Root cause inside this repo:

- `service-registry-daemon/internal/runtime/grpc/convert.go` builds the
  selected route's `ConstraintFingerprint` via
  `fingerprintCanonicalJSON(out.Constraints)`. That helper returns `nil`
  when the input is empty (see `convert.go:283-289`):

  ```go
  func fingerprintCanonicalJSON(raw []byte) []byte {
      if len(bytes.TrimSpace(raw)) == 0 {
          return nil
      }
      ...
  }
  ```

- Downstream contracts treat the fingerprint as mandatory.
  `payment-daemon/internal/service/sender/sender.go:274` hard-rejects an
  `AcceptedPrice.QuoteRef` with an empty constraint fingerprint:

  ```go
  if len(in.GetQuoteRef().GetConstraintFingerprint()) == 0 {
      return nil, errors.New("quote_ref.constraint_fingerprint is empty")
  }
  ```

- The `gRPC surface` spec at
  `service-registry-daemon/docs/product-specs/grpc-surface.md:77-78`
  describes `constraint_fingerprint` as the SHA-256 of the canonicalized
  constraints, but does not explicitly cover the empty/absent case.

The result is that any orchestrator that legitimately publishes an
offering without a `constraints` block — which the manifest schema allows
— is silently unrouteable. The error surface on the gateway is also a
generic "no route candidates", so a customer trying to use a registered
model cannot tell the difference between "the broker can't quote this"
and "no orchestrator advertises this".

## 2. Goals

1. Make `constraint_fingerprint` a deterministic non-empty digest for
   every selected route, including when the offering declares no
   `constraints` block.
2. Make sure every pool-published offer carries a `constraints` field
   even when the operator left it empty (omit-then-rehydrate-on-publish,
   never an invented value).
3. Surface a distinct gateway error class when a route is rejected for
   quote-fingerprint reasons rather than masking it as "no route".
4. Document the empty-constraints case in the resolver gRPC spec so
   future implementations match.

## 3. Non-goals

- Inventing nominal constraint values (e.g., hard-coding `tier:"standard"`).
  The fix must not change the semantic content of any operator's manifest.
- Re-signing or back-filling already-published manifests. Once the
  resolver tolerates empty constraints, those manifests work as-is.
- Changing the wire shape of `QuoteRef` or `AcceptedPrice`. This plan
  only changes the resolver's *value* for `constraint_fingerprint` in
  the empty case.
- Extending the openai-gateway's existing snapshot-based route selection
  to call `SelectMany`. The gateway in this repo uses
  `ListKnown`/`ResolveByAddress` and rebuilds its own candidate list;
  that is out of scope.

## 4. Locked model

### 4.1 Empty-constraints fingerprint

When an offering's `constraints` block is absent or empty, the resolver
emits `constraint_fingerprint = sha256("{}")`. That is, the canonical
JSON form of an empty object. This choice:

- guarantees a fixed, deterministic non-nil digest
- preserves the property that two semantically different constraint
  blocks produce different fingerprints (the empty-object digest never
  collides with any populated block)
- requires no manifest change on the orch side

The same canonicalization rule already applies to populated constraints:
the bytes are JSON-unmarshaled then re-marshaled via `encoding/json`
before hashing. This plan extends that rule to the empty case so
"absent" and `{}` become identical wire-level identities.

### 4.2 Pool publisher defaults

Pool-controller's broker-render and the capability-broker's
offerings-payload both surface `Constraints` as `omitempty`. The fix is
to publish `{}` (an empty object) when no constraints are configured,
not to drop the field. Downstream resolvers can then hash the same `{}`
the publisher renders — making the resolver behavior in §4.1 redundant
but layered.

This is a belt-and-suspenders move: the resolver fix protects every
producer, including legacy adapters and CSV fallback. The publisher
defaults protect every consumer, including older resolvers in production
that haven't picked up §4.1 yet.

### 4.3 Gateway error class

When dispatch fails because the payer-daemon (or, in the
`SelectMany`-using path, the resolver) explicitly rejected a route for
quote-fingerprint or quote-validation reasons, the gateway emits a
`LivepeerBrokerError` with `code = "broker_quote_rejected"` instead of
the generic "no route candidates" `Error`. The HTTP envelope stays
fastify-friendly: `code(503)` with `{error: "broker_quote_rejected",
message: <reason>}`. Existing 500/502 mappings are unaffected.

## 5. Impacted components

### 5.1 `service-registry-daemon/`

- `internal/runtime/grpc/convert.go` — change
  `fingerprintCanonicalJSON` so the empty/whitespace input case
  returns `sha256("{}")` rather than `nil`. Apply to both the
  `ConstraintFingerprint` field on `SelectedRoute` and any other
  consumer of the helper.
- `internal/runtime/grpc/convert_test.go` — update
  `TestSelectedRouteFromResolvedNode` to keep the "fingerprints
  populated" assertion. Add a new case
  `TestSelectedRouteFromResolvedNode_EmptyConstraints` that asserts the
  empty case still emits a non-nil deterministic
  `ConstraintFingerprint`.
- `docs/product-specs/grpc-surface.md` — extend the
  `constraint_fingerprint` paragraph (line ~77) with one sentence:
  "Absent or empty constraints are canonicalized as `{}` before
  hashing; `constraint_fingerprint` is therefore never empty for a
  selectable route."

### 5.2 `pool-controller/`

- `internal/service/brokerrender/render.go` — when an offer's
  `Constraints` map is nil, render `Constraints: map[string]any{}`
  rather than omitting. Keep `omitempty` removed from the YAML tag (or
  set explicit `{}` before marshal). The rendered broker config now
  always carries a `constraints:` key per capability.

### 5.3 `capability-broker/`

- `internal/server/registry/offerings.go` — in `BuildOfferings`, when a
  capability's `Constraints` is nil, set it to an empty map. Drop
  `omitempty` on the JSON tag so the field renders as `{}` instead of
  being elided. Round-tripping through the orch-coordinator then
  preserves the empty object.
- `internal/server/registry/offerings_test.go` (if present, otherwise
  add) — assert that a config with no constraints renders `{}`.

### 5.4 `openai-gateway/`

- `src/service/routeDispatch.ts` — in `attemptCandidates`, classify
  candidate-loop failures. If every attempt failed with a message that
  looks like a quote-fingerprint rejection (string match on
  `"constraint_fingerprint"` / `"route_fingerprint"` /
  `"quote_ref"` / `"missing quote fingerprints"`), throw a
  `LivepeerBrokerError` with `code: "broker_quote_rejected"` and
  `status: 503`. Otherwise rethrow the last error as today.
- `src/service/routeDispatch.ts` — same treatment in
  `selectRealtimeCandidate` and in the empty-candidate branches.
- `src/livepeer/errors.ts` — no API change needed; reuse
  `LivepeerBrokerError` with the new code. Document the code in a
  short comment.
- Tests (if present) — extend
  `test/daemon-route-health.integration.test.ts` or an equivalent
  routeDispatch test with one case that produces the new error code.

## 6. Verification

- `make test` in `service-registry-daemon/` passes the new fingerprint
  case.
- `make test` in `pool-controller/` and `capability-broker/` confirm the
  rendered output carries `{}` for offers without configured
  constraints.
- `pnpm test` in `openai-gateway/` confirms the new
  `broker_quote_rejected` mapping.
- Live smoke (post-merge, user-driven): re-run the
  `zerank-2-default` rerank call against the existing open-pool
  coordinator manifest. With §5.1 in place, the resolver should issue a
  non-nil `constraint_fingerprint` for the now-`{}` constraints and the
  gateway should dispatch normally.

## 7. Risks and rollback

- **Risk: SHA-256 of `{}` clashing with a legitimate constraint hash.**
  Vanishingly small; the canonicalizer always re-marshals input, so a
  populated constraint never canonicalizes to `{}`.
- **Risk: pool YAML schema breakage.** The broker-render output now
  always carries a `constraints:` key. capability-broker config loader
  already treats this as `map[string]any` with no required fields, so
  the new shape is forward and backward compatible with existing
  configs.
- **Risk: openai-gateway tests fail on stricter error class.** The new
  error path is a strict superset; if the matcher doesn't catch a
  case, behavior falls back to the prior generic-Error path. Net
  regression risk is zero.
- **Rollback:** revert `service-registry-daemon/internal/runtime/grpc/convert.go`
  and the matching test. Pool-publishing and gateway changes are
  independent and can stay in place even if §5.1 is rolled back.

## 8. Files to change

- `service-registry-daemon/internal/runtime/grpc/convert.go`
- `service-registry-daemon/internal/runtime/grpc/convert_test.go`
- `service-registry-daemon/docs/product-specs/grpc-surface.md`
- `pool-controller/internal/service/brokerrender/render.go`
- `pool-controller/internal/service/brokerrender/render_test.go` (if it
  exists; otherwise verify via the consumer)
- `capability-broker/internal/server/registry/offerings.go`
- `capability-broker/internal/server/registry/offerings_test.go` (if it
  exists; otherwise add coverage)
- `openai-gateway/src/service/routeDispatch.ts`
- `openai-gateway/src/livepeer/errors.ts` (comment-only)
- `openai-gateway/test/routeDispatch.test.ts` (or nearest equivalent)

## 9. Completion criteria

- Resolver emits a deterministic non-nil
  `constraint_fingerprint` for every selectable route, including the
  empty-constraints case, and the gRPC spec documents this.
- Pool-controller broker render and capability-broker offerings payload
  both emit `constraints: {}` when no constraints are configured.
- openai-gateway returns a distinct `broker_quote_rejected` error code
  (HTTP 503) when dispatch fails for fingerprint/quote-validation
  reasons.
- All component test suites pass.
- This plan moves to `docs/exec-plans/completed/`.

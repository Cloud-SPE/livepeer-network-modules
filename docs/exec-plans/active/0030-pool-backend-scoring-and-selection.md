---
plan: 0030
title: Pool backend scoring and broker-integrated selection for OpenAI workloads
status: active
phase: design
opened: 2026-05-17
owner: harness
related:
  - "active plan 0029 — pool node design"
  - "completed plan 0027 — layered route health and check placement"
  - "completed plan 0028 — broker health contract"
---

# Plan 0030 — Pool backend scoring and broker-integrated selection for OpenAI workloads

## 1. Problem

Pool multi-backend routing exists in `capability-broker/`, but the shipped
selection path is still broker-health-weighted only. The Pool design in
[`0029-pool-node-design.md`](./0029-pool-node-design.md) promised unpaid
synthetic validation probes, real-traffic feedback, Pool-owned eligibility and
cooldown state, warm-up/recovery behavior, and broker request-time selection
using Pool-computed trust/performance state.

That full behavior is not yet implemented. This plan closes that gap for the
first Pool scoring slice, limited to OpenAI workloads.

## 2. Scope

### In scope

- `pool-controller` synthetic probes for:
  - `openai:chat-completions`
  - `openai:embeddings`
  - `openai:audio-*`
- backend+offering Pool runtime state model
- `pool-controller` HTTP JSON snapshot endpoint
- broker poller and cached snapshot integration
- broker selection using:
  - broker-local health gating
  - Pool eligibility state
  - Pool effective selection score
  - weighted random selection
  - max-share cap
- warm-up state for new/recovered backends
- Pool cooldown state
- operator overrides:
  - quarantine
  - drain
  - warm-up override
  - max-share override
- observability in `pool-controller` and broker `/registry/health`
- coordinator compatibility for additive broker health fields

### Out of scope

- `video:transcode.abr` synthetic probes
- `video:live.rtmp` synthetic probes
- `vtuber`, `daydream`, or session-control synthetic probes
- online sampling / shadow-backend diffing
- manifest / resolver / gateway protocol changes
- request-time broker RPCs to `pool-controller`
- force-include override
- emergency degraded fallback routing mode

## 3. Locked product decisions

### 3.1 Scoring authority and signals

- `pool-controller` is the scoring authority.
- `pool-controller` runs unpaid synthetic probes against member backends.
- Real workload outcomes also feed backend trust/performance state.
- Synthetic probes are primarily a gating and baseline-confidence signal.
- New backends can become eligible from synthetic success alone, but only with
  warm-up-limited weight.

### 3.2 Synthetic probe failure semantics

- Synthetic hard exclusion does not happen on first failure.
- Exclusion occurs after `3` consecutive synthetic probe failures.
- Failures before threshold progressively reduce selection weight.
- Exclusion is per `(backend_id, capability_id, offering_id)`.
- One later synthetic success clears exclusion, but the backend re-enters via
  warm-up/reduced-confidence mode rather than instantly restoring full share.

### 3.3 Snapshot delivery model

- `capability-broker` polls `pool-controller` over HTTP JSON.
- Poll cadence: `5s`.
- Stale warning threshold: `15s`.
- Hard expiry / fail-closed threshold: `60s`.
- Broker uses last known good snapshot until hard expiry.
- After hard expiry, Pool-managed backends are ineligible until fresh snapshot
  state returns.

### 3.4 Health vs trust split

Broker-local health owns:

- transport reachability
- workload-specific readiness probes
- operator drain state
- broker probe freshness/staleness
- immediate backend availability from the broker's perspective

Pool trust/performance owns:

- synthetic probe streaks and synthetic confidence
- recent real-traffic success-rate scoring
- recent real-traffic latency scoring
- warm-up state for new or recovered backends
- suspension/quarantine decisions based on repeated poor behavior
- longer-lived backend quality judgments

Combination rule:

- broker-local unhealthy => ineligible
- Pool ineligible => ineligible
- final weight exists only if both layers permit routing

### 3.5 Trust/performance model

Internal signals stay decomposed:

- `synthetic_confidence`
- `real_success_score`
- `real_latency_score`
- `warmup_modifier`

Broker selection consumes a derived `effective_selection_score`.
Eligibility remains separate state, not just "score > 0".

### 3.6 Real workload outcome classes

Outcomes are classified into:

- `success`
- `backend_failure`
- `caller_failure`
- `policy_termination`
- `payment_termination`

`backend_failure` hurts backend quality.
`success` improves backend quality.
The other classes are tracked but neutral to backend quality by default.

### 3.7 Latency policy

Latency scoring is mode-specific, not global.

For this first phase, only OpenAI families are in scope:

- `http-reqresp` requests use end-to-end response latency
- `http-stream` requests use time-to-first-token / first streamed chunk

### 3.8 Windows and decay

- recent real success window: `5m`
- recent real latency window: `5m`
- longer-lived trust memory uses EMA with `24h` half-life
- inactive backends drift toward neutral `0.5`
- new / sample-poor backends stay in warm-up mode

### 3.9 Routing states and thresholds

States:

- `eligible`
- `degraded`
- `excluded`
- `quarantined`

Thresholds:

- synthetic hard exclusion at `3` consecutive failures
- minimum effective trust floor for eligibility: `0.10`
- degraded band: `0.10 <= score < 0.30`
- normal eligible band: `>= 0.30`

Rules:

- score floor gates eligibility
- degraded remains selectable but strongly downweighted
- exclusion can come from hard synthetic failure threshold or score floor breach
- quarantine is a hard block and is not auto-cleared

### 3.10 Pool cooldown

- Pool has its own cooldown state in addition to broker health and
  gateway-local route cooldown.
- Pool cooldown applies per backend+offering.
- Pool cooldown opens after repeated `backend_failure` events within the recent
  window.
- While cooling down, the backend is `excluded`.
- After cooldown expiry, the backend re-enters through recovery / warm-up.

### 3.11 Warm-up and recovery

- New backends must pass synthetic probes before becoming eligible.
- After synthetic pass, they enter warm-up mode.
- Warm-up backends are selectable with capped weight/share.
- They do not get full share until minimum real sample thresholds are met.
- Recovered backends after exclusion/cooldown also re-enter through warm-up.

### 3.12 Fairness

- Weighted random remains the core selector.
- Add a max-share cap so one backend cannot permanently absorb nearly all
  traffic.
- No strict minimum-share floor for weak backends.
- Warm-up backends use their own capped participation behavior.

### 3.13 Operator overrides

V1 overrides:

- force `quarantine` on backend+offering
- clear `quarantine`
- force `drain` on backend+offering
- clear `drain`
- warm-up override
- max-share cap override

No force-include override in v1.

### 3.14 Observability requirements

Required per backend+offering:

- current state
- exclusion reason
- current `effective_selection_score`
- current `synthetic_confidence`
- current `real_success_score`
- current `real_latency_score`
- warm-up status
- cooldown status and expiry
- consecutive synthetic failure count
- recent synthetic result and timestamp
- `pool-controller` snapshot freshness
- final effective broker selection weight
- broker-local health status alongside Pool state

Required aggregates:

- per member summary
- per offering summary
- top excluded backends
- top degraded backends
- score distribution / traffic share summary

### 3.15 No-eligible-backend behavior

- If no backend is eligible, broker returns hard `503`.
- Broker includes `Livepeer-Backoff`.
- No silent fallback to excluded/quarantined backends.
- No emergency low-trust routing mode in v1.

## 4. Architecture

### 4.1 High-level flow

1. `pool-controller` computes runtime state per
   `(backend_id, capability_id, offering_id)`.
2. `pool-controller` runs synthetic probes and ingests real backend outcome
   reports.
3. `pool-controller` exposes a read-only HTTP JSON snapshot.
4. `capability-broker` polls the snapshot every `5s`.
5. Broker joins:
   - broker-local health
   - Pool snapshot state
6. Broker excludes anything ineligible from either layer.
7. Broker computes final effective weight and performs weighted random
   selection.
8. Broker exposes Pool-derived reasons in `/registry/health`.

### 4.2 Invariants

- The manifest, resolver, and gateway contracts remain unchanged.
- Gateways still resolve one broker route and send work to the broker.
- Pool member backends remain invisible to the public protocol layer.
- `pool-controller` stays out of the request hot path.
- Broker remains the final request-time selector.

## 5. Runtime state model

Each backend+offering record stores:

- `state`
- `exclusion_reason`
- `synthetic_confidence`
- `real_success_score`
- `real_latency_score`
- `warmup_modifier`
- `effective_selection_score`
- `consecutive_synthetic_failures`
- `cooldown_until`
- `max_share_cap`
- `last_synthetic_result`
- `last_synthetic_at`
- `last_real_outcome_at`
- `updated_at`

Persist this state in `pool-controller` so restarts do not reset Pool routing
memory.

## 6. Scoring formulas

### 6.1 Synthetic confidence

- Range: `0.0..1.0`
- Updated by synthetic probe results
- Hard exclusion at `3` consecutive failures
- One success clears exclusion, but backend re-enters warm-up

### 6.2 Real success score

Source: 5-minute rolling window of real outcomes.

Suggested v1 formula:

```text
real_success_score = successes / max(successes + backend_failures, 1)
```

Where:

- `success` increments numerator and denominator
- `backend_failure` increments denominator only
- other outcome classes do not affect the score by default

### 6.3 Real latency score

Source: 5-minute rolling latency window using mode-specific metric.

Suggested v1 formula:

```text
real_latency_score = clamp(target_latency_ms / max(observed_p95_ms, 1), 0, 1)
```

Use separate target values per supported workload family / mode.

### 6.4 Warm-up modifier

- New and recovered backends start with reduced participation.
- Suggested v1 default initial value: `0.25`
- Backends graduate after minimum real sample threshold is met.

### 6.5 Effective selection score

Use separate internal signals and derive one score:

```text
effective_selection_score =
  synthetic_confidence
  * real_success_score_or_neutral
  * real_latency_score_or_neutral
  * warmup_modifier
```

Neutral defaults for sample-poor backends:

- `real_success_score_or_neutral = 0.5`
- `real_latency_score_or_neutral = 0.5`

### 6.6 EMA memory

- Apply `24h` half-life EMA to longer-lived confidence smoothing.
- Inactive backends drift toward neutral `0.5`.

## 7. Recommended default knobs

These are implementation defaults and may become config:

- synthetic failure threshold: `3`
- minimum real sample threshold to exit warm-up: `20`
- backend-failure cooldown trigger: `5` failures in 5 minutes
- cooldown duration: `5m`
- default max-share cap: `0.50`

## 8. Synthetic probes

### 8.1 `openai:chat-completions`

- tiny fixed prompt
- low max tokens
- non-streaming
- success requires valid OpenAI-compatible response and usable output

### 8.2 `openai:embeddings`

- fixed short string
- success requires valid embedding array response

### 8.3 `openai:audio-*`

Use per-family probe recipes:

- speech / TTS: tiny known input, valid audio response
- transcription / translation: tiny fixture audio, valid text payload

Probe requirements:

- deterministic
- cheap
- rate-limited
- timeout-bounded
- per backend+offering

## 9. Real outcome reporting contract

Broker reports a compact backend-outcome event after request/session completion.

Illustrative payload:

```json
{
  "backend_id": "member-west",
  "capability_id": "openai:chat-completions",
  "offering_id": "shared-qwen",
  "outcome": "success",
  "latency_metric_ms": 842,
  "occurred_at": "2026-05-17T16:20:00Z"
}
```

This reporting path should be asynchronous and best-effort. Failures to report
must not fail paid traffic.

## 10. Snapshot API

Add a read-only `pool-controller` endpoint:

`GET /admin/v1/backend-selection-snapshot`

Illustrative response:

```json
{
  "generated_at": "2026-05-17T16:20:00Z",
  "version": 1,
  "entries": [
    {
      "backend_id": "member-west",
      "capability_id": "openai:chat-completions",
      "offering_id": "shared-qwen",
      "state": "eligible",
      "exclusion_reason": "",
      "synthetic_confidence": 0.92,
      "real_success_score": 0.88,
      "real_latency_score": 0.73,
      "warmup_modifier": 1.0,
      "effective_selection_score": 0.59,
      "consecutive_synthetic_failures": 0,
      "cooldown_until": null,
      "max_share_cap": 0.50,
      "last_synthetic_result": "success",
      "last_synthetic_at": "2026-05-17T16:19:55Z",
      "updated_at": "2026-05-17T16:19:55Z"
    }
  ]
}
```

## 11. Broker selection integration

Per candidate backend:

1. Read broker-local health snapshot.
2. Read Pool snapshot entry for `(backend_id, capability_id, offering_id)`.
3. Reject if:
   - broker-local unhealthy / stale / draining
   - Pool state is `excluded` or `quarantined`
   - Pool snapshot expired beyond `60s`
4. Compute final effective weight:

```text
final_weight =
  broker_health_weight
  * pool_effective_selection_score
  * share_cap_adjustment
```

5. Run weighted random over remaining candidates.

If the Pool snapshot is stale but not expired:

- continue using last known good snapshot
- surface warning in `/registry/health`

## 12. `/registry/health` additions

Per backend in `backends[]`, add Pool-derived fields:

- `pool_state`
- `pool_exclusion_reason`
- `pool_effective_selection_score`
- `pool_snapshot_generated_at`
- `pool_snapshot_age_seconds`
- `pool_snapshot_stale`
- `pool_cooldown_until`
- `pool_warmup`
- `pool_consecutive_synthetic_failures`
- `effective_selection_weight`

Top-level tuple health remains normalized at the published tuple, but it is now
Pool-aware:

- if at least one backend remains selection-eligible, tuple `status` / `reason`
  continue to reflect the best live broker-local health among those selectable
  backends
- if every backend is selection-blocked by Pool snapshot freshness problems, the
  tuple falls to `stale`
- if every backend is selection-blocked by Pool routing state (for example
  cooldown, exclusion, or score-floor failure), the tuple falls to `degraded`

This keeps resolver-facing tuple health aligned with actual broker routability
instead of advertising a tuple as `ready` when the broker would deny all
backend choices at dispatch time.

## 13. Operator override API

Add v1 endpoints:

- `POST /admin/v1/backend-overrides/quarantine`
- `POST /admin/v1/backend-overrides/clear-quarantine`
- `POST /admin/v1/backend-overrides/drain`
- `POST /admin/v1/backend-overrides/clear-drain`
- `POST /admin/v1/backend-overrides/warmup`
- `POST /admin/v1/backend-overrides/clear-warmup`
- `POST /admin/v1/backend-overrides/max-share-cap`
- `POST /admin/v1/backend-overrides/clear-max-share-cap`

Illustrative payload:

```json
{
  "backend_id": "member-west",
  "capability_id": "openai:chat-completions",
  "offering_id": "shared-qwen",
  "reason": "operator action"
}
```

## 14. Persistence changes

Add `pool-controller` persistence buckets / namespaces for:

- `backend_selection_state`
- `backend_probe_history`
- `backend_real_outcome_rollups`
- `backend_overrides`

Persist compact rollups and current state rather than unbounded raw event logs.

## 15. Work breakdown

### 15.1 `pool-controller`

1. Add config knobs for:
   - probe cadence
   - probe timeouts
   - synthetic failure threshold
   - cooldown duration
   - warm-up exit threshold
   - max-share cap default
   - snapshot freshness thresholds
2. Add runtime state structs and snapshot response structs.
3. Persist backend selection state and overrides.
4. Implement synthetic probe workers for chat, embeddings, and audio.
5. Implement scoring engine:
   - rolling 5-minute success / latency views
   - 24-hour EMA memory
   - warm-up, cooldown, thresholds, overrides
6. Add outcome-ingest endpoint and recompute path.
7. Add snapshot endpoint and operator override endpoints.
8. Add operator read surfaces for the locked observability set.

### 15.2 `capability-broker`

1. Add Pool snapshot poller and in-memory cache.
2. Enforce stale/expiry handling.
3. Integrate Pool state into multi-backend selection.
4. Emit real backend outcomes asynchronously to `pool-controller`.
5. Extend `/registry/health` with Pool-derived backend fields.
6. Preserve current behavior for non-Pool deployments.

### 15.3 `orch-coordinator`

1. Preserve compatibility with additive broker health fields.
2. Add / keep tests that ensure broker `/registry/health` can evolve
   additively without coordinator breakage.

## 16. Rollout phases

### Phase 1 — Scaffolding

- `pool-controller` runtime state model
- persistence
- snapshot API
- broker poller / cache
- no selection behavior change yet

### Phase 2 — Synthetic probes

- chat, embeddings, audio probes
- failure streaks
- exclusion / recovery transitions
- observability

### Phase 3 — Broker integration

- Pool eligibility join
- effective score / warm-up / cooldown
- share cap
- fail-closed on expired snapshot

### Phase 4 — Real traffic feedback

- broker outcome reporting
- rolling windows
- EMA memory
- latency/success scoring

### Phase 5 — Operator controls and summaries

- overrides
- member / offering summaries
- exclusion / degradation / traffic-share reporting

## 17. Testing

### Unit tests

- probe success/failure transitions
- synthetic exclusion threshold at 3
- cooldown enter/exit
- warm-up enter/exit
- score-band transitions
- stale/expired snapshot handling
- max-share cap math

### Integration tests

- broker polls snapshot and changes routing
- synthetic exclusion removes backend from routing
- recovered backend re-enters warm-up
- no eligible backend returns `503` + `Livepeer-Backoff`
- additive `/registry/health` fields remain coordinator-safe

### Soak tests

- multi-backend traffic-share stability
- fluctuating latency/failure conditions
- snapshot outages
- override behavior under load

## 18. Risks

- Score volatility if short windows are too sensitive
- Audio synthetic probe false positives / negatives
- Control-plane churn if recomputation is too aggressive
- Share-cap tuning pain in small backend pools

## 19. Acceptance criteria

- Pool can score OpenAI-family backends without changing
  manifest/resolver/gateway protocol surfaces.
- Broker routes using Pool snapshot state plus broker-local health.
- Operators can see why a backend is selected, downweighted, or excluded.
- Synthetic failures exclude after 3 consecutive failures.
- Recovered backends re-enter via warm-up.
- Expired Pool snapshot fail-closes Pool-managed backends.
- Coordinator does not break on additive broker health fields.

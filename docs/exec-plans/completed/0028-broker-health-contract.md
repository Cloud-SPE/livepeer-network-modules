---
plan: 0028
title: Broker health contract
status: completed
phase: shipped
opened: 2026-05-14
owner: harness
related:
  - "completed plan 0003 — capability broker"
  - "completed plan 0018 — orch-coordinator design"
  - "completed plan 0027 — layered route health and check placement"
---

# Plan 0028 — Broker health contract

## 1. Problem

Plan 0027 locked the layered-health architecture, and this plan froze
the broker-side contract that the rest of the stack now builds against:

- how operator config selects health behavior per tuple
- how the broker executes specialized health checks
- how those checks are normalized into a workload-agnostic route-health surface

The original gap was that `capability-broker` reported static
availability at `/registry/health`. The implemented contract now covers:

- OpenAI model-loaded vs port-open distinctions
- video / RTMP pipelines that need subprocess or encoder readiness
- VTuber session runtimes with control-plane dependencies
- Daydream Scope with external-media prerequisites
- unknown future capabilities that will need more than shallow TCP/HTTP probes

This plan is now implemented in `capability-broker` and is the broker
health contract the rest of the stack builds against.

## 2. Goals

1. Define the `host-config.yaml` schema for per-tuple health behavior.
2. Define the broker probe-recipe interface and execution model.
3. Define the normalized state machine exposed by `/registry/health`.
4. Keep coordinator, resolver, and gateways workload-agnostic.
5. Support current workload families and future unknown capabilities
   without redesigning upstream modules.

## 2.1 Status

This plan is shipped.

Implemented surfaces include:

- per-tuple `health` config in `host-config.yaml`
- validation/defaulting for `initial_status`, drain, cadence, timeout,
  thresholds, and recipe-specific config
- broker-owned probe recipes:
  - `http-status`
  - `http-jsonpath`
  - `http-openai-model-ready`
  - `tcp-connect`
  - `command-exit-0`
  - `manual-drain`
- normalized outward status set:
  `ready`, `draining`, `degraded`, `unreachable`, `stale`
- cached `/registry/health` snapshots with freshness fields
- broker tests covering representative health transitions

## 3. Non-goals

- adding signed live health to manifests
- moving health logic into `service-registry-daemon` or any gateway
- solving Layer 3 observed-failure policy
- defining every future probe recipe upfront
- hot-reload of broker config

## 4. Design locks

### 4.1 Extensibility seam

Specialized health behavior is allowed only inside the broker.

- operator picks a probe recipe in `host-config.yaml`
- capability-broker executes the recipe
- broker publishes only normalized status, freshness, and reason

Everything outside the broker treats probe semantics as opaque.

### 4.2 Tuple scope

Health is tracked per advertised tuple:

`(capability_id, offering_id)`

The broker may share lower-level probe machinery across tuples pointing at
the same backend, but the outward contract remains per tuple because:

- one backend may serve multiple capabilities with different readiness semantics
- one tuple may be draining while another on the same host is not
- gateways route on tuples, not on hosts

### 4.3 Outward status set

`/registry/health` is limited to these outward states:

- `ready`
- `draining`
- `degraded`
- `unreachable`
- `stale`

Definitions:

- `ready` — probe is succeeding and the tuple is eligible for new work
- `draining` — operator or runtime policy allows existing work to continue
  but rejects new opens
- `degraded` — backend is partially alive but not healthy enough for normal
  routing
- `unreachable` — required dependency cannot currently be reached or used
- `stale` — broker has no sufficiently fresh probe result

No workload family is allowed to invent its own outward state strings.

### 4.4 Reason model

`/registry/health` may expose machine-readable reason codes, but they are
advisory and must not be required for route selection.

Allowed uses:

- operator UI
- debugging
- metrics labels where cardinality is bounded

Not allowed:

- resolver branching on capability-specific reason codes
- gateways implementing workload-specific route policy from reason strings

## 5. Operator config contract

Each capability tuple may define a `health` block in `host-config.yaml`.

Illustrative shape:

```yaml
capabilities:
  - id: "openai:chat-completions:llama-3-70b"
    offering_id: "default"
    interaction_mode: "http-stream@v0"
    health:
      initial_status: "stale"
      drain:
        enabled: false
      probe:
        type: "http-openai-model-ready"
        interval_ms: 5000
        timeout_ms: 1500
        unhealthy_after: 2
        healthy_after: 1
        config:
          url: "http://10.0.0.5:8000/healthz"
          expect_model: "llama-3-70b"
```

### 5.1 Required semantics

- `health` omitted:
  broker uses a safe default recipe derived from backend transport where
  possible, otherwise marks tuple `stale` until explicitly configured
- `initial_status`:
  default `stale`
- `drain.enabled`:
  when `true`, outward status is `draining` regardless of probe success
- `probe.type`:
  selects a broker-local probe recipe
- `interval_ms`:
  cadence for background refresh
- `timeout_ms`:
  per-probe execution timeout
- `unhealthy_after`:
  consecutive failed probes before moving from `ready` to unhealthy
- `healthy_after`:
  consecutive successes before moving from unhealthy back to `ready`
- `config`:
  recipe-specific parameters

### 5.2 Config validation rules

Broker config loading must reject:

- unknown top-level health fields
- `interval_ms <= 0`
- `timeout_ms <= 0`
- `timeout_ms > interval_ms` only if explicitly disallowed by the final implementation
- `unhealthy_after < 1`
- `healthy_after < 1`
- unknown `probe.type`
- missing required recipe-specific config fields

## 6. Probe recipe interface

The broker needs one internal interface for all recipe types.

Illustrative behavior contract:

- input:
  - tuple identity
  - backend descriptor
  - recipe config
  - context with timeout
- output:
  - success / failure / partial state signal
  - bounded reason code
  - measured probe timestamp
  - optional low-cardinality details for logs

The interface is intentionally not exported as a public extension API in
v1. New recipes land in broker code, not as third-party plugins.

### 6.1 Initial recipe set

The first implementation should cover the representative workload set:

- `http-status`
  - shallow HTTP reachability
- `http-jsonpath`
  - response field must satisfy an expected predicate
- `http-openai-model-ready`
  - backend reachable and the named model is present / loaded
- `tcp-connect`
  - port accepts connections
- `command-exit-0`
  - local command or sidecar check succeeds
- `manual-drain`
  - operator intent forces `draining`

Mode- or runtime-specific recipes may be added where the above do not fit:

- video / RTMP pipeline recipe
- VTuber session-runtime recipe
- Daydream Scope external-media recipe

### 6.2 Workload mapping

| Workload family | Likely first recipe |
|---|---|
| OpenAI | `http-openai-model-ready` |
| video / RTMP | runtime-specific recipe or `command-exit-0` wrapper |
| VTuber | runtime-specific session recipe |
| Daydream Scope | `http-jsonpath` or dedicated external-media recipe |
| generic SaaS HTTP | `http-status` or `http-jsonpath` |

## 7. Probe execution model

### 7.1 Background cadence

Probes run in the background on per-tuple cadence. `/registry/health`
serves cached results; it does not synchronously block on a fresh probe
for every request.

Why:

- keeps resolver and coordinator polling cheap
- avoids cascading latency from health reads into route selection
- prevents thundering herds on fragile upstreams

### 7.2 State transitions

Illustrative model:

1. broker startup → tuple begins at `stale`
2. first `healthy_after` successes → `ready`
3. `unhealthy_after` consecutive failures:
   - `ready` → `degraded` or `unreachable` depending on recipe result
4. probe result ages beyond freshness window → `stale`
5. operator drain forces `draining`
6. recovery requires `healthy_after` consecutive successes

### 7.3 Freshness

Each tuple needs:

- `probed_at`
- `observed_at`
- freshness TTL derived from cadence and policy

If the last successful or failed observation is older than freshness TTL,
the outward status becomes `stale` even if the last known result was green.

### 7.4 Shared-backend optimization

When many tuples share one backend, the broker may optimize probe execution
internally, but:

- tuple status must still be computed independently
- recipe-specific tuple config must still be respected
- one tuple's drain or specialized failure must not implicitly poison
  unrelated tuples unless the shared dependency truly makes them all unusable

## 8. `/registry/health` response contract

Illustrative shape:

```json
{
  "broker_status": "ready",
  "generated_at": "2026-05-14T15:04:05Z",
  "capabilities": [
    {
      "id": "openai:chat-completions:llama-3-70b",
      "offering_id": "default",
      "status": "ready",
      "reason": "probe_ok",
      "probe_type": "http-openai-model-ready",
      "probed_at": "2026-05-14T15:04:00Z",
      "stale_after": "2026-05-14T15:04:15Z",
      "consecutive_successes": 12,
      "consecutive_failures": 0
    },
    {
      "id": "video:live.rtmp",
      "offering_id": "default",
      "status": "draining",
      "reason": "operator_marked_drain",
      "probe_type": "manual-drain",
      "probed_at": "2026-05-14T15:04:01Z",
      "stale_after": "2026-05-14T15:04:16Z",
      "consecutive_successes": 0,
      "consecutive_failures": 0
    }
  ]
}
```

### 8.1 Field rules

- `broker_status`
  - broker-process-level summary; not a replacement for per-tuple status
- `generated_at`
  - when the snapshot was rendered
- `id`, `offering_id`
  - must correspond to `/registry/offerings`
- `status`
  - one of the locked outward states
- `reason`
  - bounded machine-readable reason code
- `probe_type`
  - optional operator/debug field; consumers must not branch on it
- `probed_at`
  - time of last completed probe
- `stale_after`
  - timestamp after which consumers should treat this tuple as stale
- `consecutive_successes`, `consecutive_failures`
  - optional operator/debug counters; not required for routing

### 8.2 Consumer rules

Coordinator:

- may display any field
- must not mutate manifest content from this response

Resolver:

- may route only on:
  - tuple presence
  - `status`
  - freshness from `stale_after`
- must not implement capability-specific logic from `reason` or `probe_type`

Gateways:

- should normally consume resolver output rather than raw broker health
- must not fork route logic by probe type

## 9. Observability contract

Broker metrics should include:

- probe executions total by tuple and outcome
- probe duration
- current tuple status
- stale tuple count
- drain count

Reason labels must stay bounded. Free-form text reasons are for logs, not metrics.

## 10. Failure model

The contract must clearly distinguish:

- process alive but tuple unhealthy
- tuple draining by operator intent
- no fresh probe data
- shared dependency outage affecting many tuples
- recipe misconfiguration

Recommended normalized mapping:

- misconfiguration at startup → broker refuses to start or rejects config
- probe timeout → usually `degraded` or `unreachable`
- missing fresh data → `stale`
- operator drain → `draining`

## 11. Acceptance criteria

This plan is complete when:

1. `host-config.yaml` health schema is documented and validated.
2. `/registry/health` response schema is documented and implemented.
3. Broker supports a representative initial probe set.
4. Broker maps specialized checks into the locked outward states.
5. The contract is shown to fit:
   - OpenAI
   - video / RTMP
   - VTuber
   - Daydream Scope
   - generic HTTP/SaaS
6. Coordinator and resolver can consume the contract without learning
   workload semantics.

## 12. Recommended implementation sequence

1. document config and response schema
2. implement internal probe interface
3. implement default recipe set
4. replace static `/registry/health` with cached tuple snapshots
5. add broker tests for state transitions and stale behavior
6. hand off to coordinator and resolver work in plan 0027

# gateway-route-health

Shared Layer 3 route-health tracker used by the Livepeer rewrite
gateways.

## Purpose

This package centralizes the generic gateway-local policy for:

- tracking per-route request outcomes
- opening cooldown windows after retryable failures
- clearing cooldown state after later success
- exposing cumulative Layer 3 metrics
- producing workload-agnostic route-health snapshots

It exists so gateways do not fork the same cooldown logic four
different ways.

## Contract

The package exports:

- `GenericRouteHealthTracker<TCandidate>`
- `RouteHealthPolicy`
- `RouteOutcome`
- `RouteHealthSnapshot`
- `RouteHealthMetrics`

The caller provides:

- `failureThreshold`
- `cooldownMs`
- `keyOf(candidate)` for stable per-route identity

The generic tracker provides:

- `rankCandidates(...)`
- `chooseRandom(...)`
- `record(...)`
- `inspect(...)`
- `inspectMetrics(...)`
- `summarizeRouteHealth(...)`
- `renderRouteHealthMetrics(...)`

## Usage pattern

Gateways should keep only a thin wrapper around the generic tracker:

- define the candidate type used by that gateway
- define `keyOf(candidate)` for that route shape
- re-export shared types if the gateway wants local naming stability

Gateway wrappers may differ only where route shape differs, for example:

- OpenAI includes `interactionMode` in the key
- Video keys by broker, operator, capability, and offering
- Daydream uses the same tracker but selects randomly from non-cooled
  candidates
- VTuber keys by node URL, operator, capabilities, and offering

## Workspace

This package is a root workspace package. Dependents should reference it
with:

```json
"@livepeer-network-modules/gateway-route-health": "workspace:*"
```

Then run:

```bash
pnpm install
pnpm -F @livepeer-network-modules/gateway-route-health build
```

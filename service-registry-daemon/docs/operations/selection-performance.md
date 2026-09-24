# Selection performance verification

The warm-selection target is p99 below 100 ms under a documented representative
load, with zero outbound calls during Select/SelectMany. This is a target, not a
latency guarantee for every deployment or transport.

## Reproduce

From `service-registry-daemon/` with the repository Go toolchain:

```sh
go test ./internal/service/resolver ./internal/runtime/grpc -run '^$' -bench 'Benchmark(SnapshotSelection|SelectMany)$' -benchtime=2s -cpu=1,8 -benchmem
```

Both benchmarks use 1,000 warmed routes spread across 100 capability/offering
pairs, with ten matches per request. `RunParallel` uses one or eight concurrent
callers. `SnapshotSelection` measures the indexed reader and defensive copies;
`SelectMany` also constructs routes, quotes and fingerprints. Neither includes
gRPC transport. Refresh workers are not running during these microbenchmarks;
provider isolation and concurrent refresh are exercised separately by race tests.
The bounded latency samples retain up to 1,024 observations per caller for the
reader and 4,096 per caller for SelectMany. `ns/op` is aggregate throughput time
per operation; sampled p99 measures elapsed time of individual calls.

## Local evidence, 2026-09-24

Linux amd64, Intel Core i5-10400 at 2.90 GHz, Go 1.26.6:

| Benchmark | Callers | ns/op | Sampled p99 (ms) |
|---|---:|---:|---:|
| SnapshotSelection | 1 | 12,769 | 0.04391 |
| SnapshotSelection | 8 | 10,361 | 3.700 |
| SelectMany, including quote construction | 1 | 30,687 | 0.1346 |
| SelectMany, including quote construction | 8 | 8,188 | 0.4844 |

The host was also running build/release checks during this measurement.
These local results are below the proposed target; they are not production
end-to-end measurements. Re-run with deployed route counts, filters, CPU limits,
transport and competing workloads when evaluating an operator deployment.

`TestSnapshotSelectionNeverCallsProviders` runs 32 concurrent readers while an
unrelated address is blocked on the RPC provider and counts all provider calls.
Other regression tests cover cold/background startup, hard expiry, unavailable
versus known-negative state, invalid publications, source changes, configuration
generations, restart, worker bounds, and shutdown before store close. Run
`make ship-check` for lints, race tests, document checks and package coverage.

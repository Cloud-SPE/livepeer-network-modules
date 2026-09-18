# Regional local acceptance — 2026-09-16

This records local evidence from the uncommitted `tasks/lpm-v2` implementation.
It is not a deployment, image publication, independent release review or live
payment record. `lnm-l17` remains open while required implementation and
validation remain; `lnm-rqz` still governs the coordinated major-4 release.

## Cross-component process tests

Run the real processes inside a Docker-first Go environment:

```sh
docker run --rm -v "$PWD:/src" \
  -v regional-go-mod:/go/pkg/mod \
  -v regional-go-cache:/root/.cache/go-build \
  -w /src/e2e golang:1.26 go test -race -count=1 ./...
```

The fresh full suite passed after runtime and startup-order fixes in 77.802
seconds: `/tmp/regional-e2e-final-race.log`. The original portal/accounting
run remains in `/tmp/regional-e2e-portal-and-accounting.log`. The Docker transfer
case is enabled separately below; a default suite run explicitly skips it.

| Test | Actual production boundaries | Observed result |
|---|---|---|
| `TestRegionalPortalRealControllersRestartAndOutage` | Shared portal process, TLS termination, two controller processes and their durable stores | One wallet signs in once; regional joins remain explicit; wrong-region signed token and cross-wallet access rejected; EU outage leaves US fresh; cached EU is stale, then unavailable after portal restart; session, pool identity and membership survive restart; logout invalidates access |
| `TestRegionalRealRevenueCollectionAndIndependentWindows` | Four receiver gRPC services and durable receiver stores; four broker processes and durable work outboxes; scoped HTTPS reporting/receipt ingestion; two controller processes; actual reconciler commands and durable retry stores | EU receives 601 tickets and 601 receipts; US collects 606 receipts across three sources; missing US receiver holds US while EU completes; restarting that receiver restores collection; exact window results below; immutable approval replay; EU failure/requeue leaves US intents byte-for-byte unchanged; controller restart and reconciler replay preserve obligations |

The receiver fixture supplies canonical chain inclusions to the **real**
`revenuereport.Reporter` and receiver service. The work fixture provisions
completed metering operations in the **real** broker work ledger. Initial
operator terms, source registrations and accounting-test memberships are
provisioned offline before serving. The portal test instead creates its
memberships through actual signed-in HTTP requests. The clock is a synthetic
gRPC round source. No fixture substitutes broker reporting HTTP, controller
receipt/window HTTP, or reconciler collection logic.

Payout failure/requeue in this process test exercises controller intent APIs
with synthetic pre-submission failure. Actual signing and transaction restart
are covered by the payout-executor's simulated-chain tests, not claimed as live
Arbitrum transactions or as an executor process in this harness. GPU inference,
real paid traffic remain separate validation boundaries. The local transfer
acceptance is described below.

## Actual local container transfer

Run `infra/scenarios/regional-pools/check-transfer-acceptance.sh` on a host with
local Docker and Compose. It mounts that local Docker socket into the Go test
environment and removes only its unique test containers and network afterward.
The final fault-injection run passed in 50.781 seconds:
`/tmp/regional-transfer-release-replay.log`.

The test runs the actual ownership HTTPS binary and source controller binary
with persistent databases. A broker test process exposes production drain,
credential revocation, ownership verification and dispatch handlers backed by
real durable stores. Agent test processes execute production desired-state
fetch, Docker Compose apply and status reporting. Synthetic boundaries are
GPU identity, an outstanding authorization's receiver state, a held HTTP work
request, and two sleeping Alpine containers. This is not GPU inference, funded
payment execution, or a full broker/agent tunnel test; those boundaries have
separate component and process coverage.

Observed assertions:

- Outstanding work holds the device in draining state while both containers
  remain running; new source dispatch is fenced and sibling dispatch continues.
- Controller restart preserves the pending drain. After work completes, every
  source revocation must finish before an explicit stop is rendered.
- The agent actually removes the selected container. A deliberately lost status
  report still blocks target ownership; restart and report replay unblock it.
- The first successful ownership release response is replaced with HTTP 502
  after the destination claims generation 2. Source GET still redacts target
  wallet/enrollment. Replaying the recorded source release completes the
  transfer without changing target ownership.
- The old generation cannot reclaim or dispatch, including after broker restart.
  The sibling container keeps the same container ID throughout.

This exposed and fixed controller reconciliation starting before configuration
was installed, and source release recovery incorrectly relying on another
pool's redacted wallet field. The ownership release index is rebuilt from
historical audit events when opening an older database.

## Exact accounting observations

All values are integer wei. Windows contain the accepted default 14 rounds.

| Pool/window | Realized revenue | Billed monetary weight | Commission | Member allocations | Rounding residual | Zero-work operator allocation |
|---|---:|---:|---:|---|---:|---:|
| EU, 100–113 | 6010 | 601 | 601 | First wallet: 5409 | 0 | 0 |
| US, 100–113 | 14320 | 822 | 1432 | First wallet: 9752; second wallet: 3135 | 1 | 0 |
| EU, 114–127 | 7 | 0 | 0 | None | 0 | 7 |

US contributions combine transcode weight 601, audio weight 21 and LLM weight
200. These are monetary weights, not raw heterogeneous units. Source revenue
includes every confirmed ticket and is not limited to the reported member work.
The first wallet earns in both regional books without merging their batches.

## Other local evidence

The eleven component race suites passed before the final runtime audit; logs
are under `/tmp/regional-final-component-checks/`. Changed components were then
rerun with fresh executions: agent `/tmp/regional-agent-final-race.log`, broker
`/tmp/regional-broker-final-race.log`, portal
`/tmp/regional-portal-bootstrap-runtime.log`, and controller
`/tmp/regional-controller-release-replay-final.log`. The controller's final
result also covers release-history migration and conflicting replay rejection.

Protocol schema/signature/version checks and all 58 conformance scenarios
passed. Registry `make ship-check` passed, including lints, race tests and its
per-package coverage gate. Generated deployment parsers, secret scopes,
read-only mounts, volume identity, archive verification and manual-restore
obligation tests are recorded in the scenario's
[local validation record](local-validation-2026-09-16.json) and
[backup procedure](backup-and-restore.md).

Controller-to-agent regional runner JSON/Compose goldens passed, and actual
`docker compose config --quiet` accepted the generated AI runner group. This
proves configuration structure and lifecycle behavior, not model execution.
See [runner readiness](runner-readiness.md) for image provenance and hardware
limits. The development GPU is a GTX 1650 with 4 GB, outside the AI catalog's
4090/5090 target classes.

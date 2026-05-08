# AGENTS — protocol-daemon

The map every agent should keep at hand when modifying this module.

## What this module does

Round initialization (`RoundsManager.initializeRound`), reward calling
(`BondingManager.rewardWithHint`), and on-chain `ServiceRegistry` /
`AIServiceRegistry` pointer management on behalf of a Livepeer
orchestrator. Built fresh on `chain-commons`. Three modes:
`round-init`, `reward`, `both`.

## Layer rule

```
cmd/ → runtime/ → service/ → repo/ → providers/ → types/
                             │
                             └─▶ chain-commons.providers.* / chain-commons.services.*
```

- `internal/types/`: pure data, no I/O imports.
- `internal/config/`: validated config, embeds `chain-commons.config.Config`.
- `internal/providers/{bondingmanager,roundsmanager,minter}/`: ABI bindings — only place `github.com/ethereum/*` is allowed.
- `internal/repo/poolhints/`: BoltDB cache — uses `chain-commons.providers.store`, never raw `bbolt`.
- `internal/service/{roundinit,reward,preflight}/`: business logic — never imports go-ethereum directly.
- `internal/runtime/{grpc,metrics,lifecycle}/`: gRPC server, Prometheus listener, signal handling.
- `cmd/livepeer-protocol-daemon/`: thin entry point — flags, dispatch, provider wiring.

Enforced by `lint/layer-check/`. Don't reach across layers; route through providers.

## Invariants

- Every on-chain write goes through `chain-commons.services.txintent.Manager.Submit`. Never call `keystore.SignTx` + `rpc.SendTransaction` directly from service code.
- `chain-commons.services.txintent` provides idempotency by `(Kind, KeyParams)`. For round-init: `Kind="InitializeRound", KeyParams=round.Number.Bytes()`. For reward: `Kind="RewardWithHint", KeyParams=round.Number.Bytes() ++ orchAddr.Bytes()`.
- `Manager.Resume(ctx)` is called once at startup before any other service runs.
- Mode-specific RPCs return `Unimplemented` when the daemon is not running in that mode.
- Preflight failures exit non-zero with a structured `error_code` log line; the gRPC socket is not opened until preflight passes.
- `--metrics-listen` is empty by default. Operators opt in. No metrics listener bound when empty.
- No `prometheus/client_golang` imports outside `internal/runtime/metrics/`.
- No `bbolt` imports outside `internal/repo/poolhints/` (and even there, only via `chain-commons.providers.store`).

## Common workflows

| Task | Where to start |
|---|---|
| Add a new RPC method | `proto/livepeer/protocol/v1/protocol.proto` → `internal/runtime/grpc/server.go` → tests |
| Tune the round-init loop | `internal/service/roundinit/service.go` |
| Tune positional-hint walking | `internal/service/reward/hints.go` + `internal/repo/poolhints/cache.go` |
| Add a new metric | `internal/runtime/metrics/names.go` (constant) → emitter site → `docs/design-docs/observability.md` |
| Bump preflight | `internal/service/preflight/preflight.go` |
| Add a config flag | `cmd/livepeer-protocol-daemon/run.go` (flag def) → `internal/config/config.go` (struct field + validation) |

## Tests

`make test` — race-clean preferred. `make coverage-check` — 75% per-package gate. Coverage exemptions live in [`lint/coverage-gate/exemptions.txt`](lint/coverage-gate/exemptions.txt) with written reasons.

## Pull-request checklist

1. `make build` is green.
2. `make test` is green.
3. `make lint` is green.
4. `make coverage-check` is green.
5. Documentation updated (design-doc / runbook) if the change affects an externally-visible behaviour.

## Attribution

This component was ported into this monorepo from the sibling repo:

- `/home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/protocol-daemon`

That copy was explicitly user-authorized in this thread. The supporting
`chain-commons/` and `proto-contracts/` directories were copied for the
same reason so the daemon can build and run inside this monorepo.

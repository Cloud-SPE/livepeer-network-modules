# chain-commons Adoption — Exec Plan 0048

> Status: Code complete; real-process run pending · Owner: Mike Zupper · Last updated: 2026-09-04
> Branch: `tasks/lpm-v2`
> Components: `chain-commons`, `payment-daemon`, `pool-payout-executor`, `protocol-daemon`, `service-registry-daemon`

## 0. TL;DR

Five modules talk to the chain. Two of them (`protocol-daemon`,
`service-registry-daemon`) do it through `chain-commons`; the other two
(`payment-daemon`, `pool-payout-executor`) each carry their own copy of
the same glue: RPC dial, Controller address resolution, gas pricing, a
hand-rolled send-then-wait-for-confirmations loop, and a round poller.
This plan moves both onto `chain-commons` in five stages, adds the test
infrastructure that makes the transaction-lifecycle stage safe, and
puts a per-package coverage floor on every touched package.

It supersedes plan 0016 §11.Q1 ("skip chain-commons; revisit if a
second chain-talking component ever appears") — three have.

## 1. What is duplicated today

| Concern | protocol-daemon | service-registry-daemon | payment-daemon | pool-payout-executor |
|---|---|---|---|---|
| RPC transport + failover | chain-commons `rpc/multi` | chain-commons `rpc/multi` | own: first-healthy at startup | own: first-healthy at startup |
| Controller address resolve | chain-commons `controller/eth` | chain-commons `controller/eth` | own `providers/chain.Resolver` | n/a |
| Gas pricing | chain-commons `gasoracle/ttl` | n/a | own `providers/gasprice/onchain` | own (`SuggestGasTipCap` inline) |
| Tx submit + nonce + confirm | chain-commons `services/txintent` | n/a | own loop in `broker/ticketbroker` | own loop in `internal/ethclient` |
| Round / block clock | chain-commons `roundclock` + `timesource` | chain-commons `roundclock` | own `providers/clock/onchain` | n/a |
| `--chain-rpc-urls` CSV parsing | own `splitCSV` | own `csvList` | own `splitCSV` | YAML list |

`chain-commons/README.md` already claims payment-daemon consumes it.
That claim becomes true at the end of this plan.

## 2. Decisions

1. **One transport.** Every chain-backed provider takes
   `chain-commons/providers/rpc.RPC`, opened once per process by
   `rpc/multi.Open` from `--chain-rpc-urls`. No provider holds a
   concrete `*ethclient.Client`. Runtime failover is therefore uniform.
2. **One parser.** `chain-commons/config.ParseRPCURLs(string) ([]string,
   error)` trims, rejects blank entries, and is the only CSV parser for
   the flag. The payout executor also accepts `CHAIN_RPC_URLS` from the
   environment, overriding `executor.rpc_urls`, so a compose host has
   one list.
3. **One transaction lifecycle.** Both hand-rolled loops are replaced by
   `services/txintent` (durable intents, idempotency keys, replacement,
   reorg-aware confirmation, restart resume). The hot wallet's nonce is
   owned by the intent processor and nothing else.
4. **Metric names do not change.** `payment-daemon` keeps its
   `livepeer_payment_*` series (eight are referenced by
   `docs/operations/prometheus/alerts.yaml` and the Grafana board) by
   adapting `chain-commons`'s `metrics.Recorder` to its existing
   recorder. The alert rules are a gate: every metric they name must
   still be emitted.
5. **Startup reconciliation for in-flight work.**
   - Redemptions: before submitting an intent for a winning ticket the
     settlement path reads `TicketBroker.isUsedTicket`; a ticket already
     redeemed by the pre-upgrade loop is marked settled without a tx.
     A ticket with an intent already in the store is never resubmitted
     (idempotency key = ticket hash).
   - Payouts: a controller intent in `submitted` with a recorded
     `tx_hash` is adopted into the intent store in the submitted state
     (`txintent.Manager.Adopt`, new API) and tracked to confirmation
     rather than re-sent. Idempotency key = controller intent id.
   - Upgrade runbook says: stop the daemon, upgrade, start. No drain
     required because of the two rules above; a drain is still the
     conservative choice and is documented as such.
6. **Coverage floor.** Every package touched by this plan carries a
   75% per-package floor enforced by `make coverage-check`, using the
   same `lint/coverage-gate` tool the registry daemon and protocol-daemon
   already run.
7. **Hard cut.** No compatibility shims for the deleted in-tree
   providers. Plan 0016's provider *interfaces* stay; only their
   implementations change.

## 3. Test infrastructure (stage 0)

Three layers, in order of fidelity:

1. **Fakes.** `chain-commons/testing/fakerpc` and friends for unit tests
   of wiring, failover selection, and error classification.
2. **Simulated chain.** `chain-commons/testing/simchain`: an adapter
   exposing go-ethereum's `ethclient/simulated.Backend` as `rpc.RPC`,
   with a helper to deploy a contract and mine on demand. Used to run
   real signed transactions, nonces, receipts, replacement and
   restart-resume against the intent processor, the ticket broker and
   the payout executor without a network. Also a two-endpoint variant
   (one fake that fails, one sim that works) to prove failover on a
   real call path.
3. **Real process.** `infra/scenarios/integration-stack` run at the end
   of stage 4, and the payment-daemon rotation + go-livepeer wire-compat
   tests on every stage.

## 4. Stages

Each stage is one bead and one or more commits; each ends green on
build, vet, test, lint and coverage-check in every touched module.

### Stage 1 — Transport
- `payment-daemon`: `providers/chain`, `clock/onchain`,
  `gasprice/onchain`, `broker/ticketbroker` take `rpc.RPC`; `main.go`
  opens `multi.Open` once; `DialFirstHealthy` deleted; chain-id check
  via `rpc.ChainID`. Adapters: `slog` → `logger.Logger`, payment
  recorder → `metrics.Recorder`.
- `pool-payout-executor`: `internal/ethclient` takes `rpc.RPC`;
  `dialFirstHealthy` deleted.
- Tests: fakes for wiring; simchain for one end-to-end call per
  provider; failover test on a real call.

### Stage 2 — One parser
- `config.ParseRPCURLs` in chain-commons; all four daemons use it.
- Executor: `CHAIN_RPC_URLS` env overrides `executor.rpc_urls`;
  pool-orchestrator / pool-node compose pass it through.

### Stage 3 — Controller resolver and gas oracle in payment-daemon
- `providers/chain.Resolver` → `controller/eth` (keeps the per-contract
  override flags). `gasprice/onchain` → `gasoracle/ttl` behind the
  existing `providers.GasPrice` interface.

### Stage 4 — Transaction lifecycle
- 4a `pool-payout-executor`: `SendNativeTransfer` / `ConfirmTransaction`
  become `txintent` submit + wait; `Adopt` for submitted intents at
  startup; local state repo records intent ids.
- 4b `payment-daemon` ticket broker: `RedeemWinningTicket` becomes an
  intent keyed by ticket hash; `isUsedTicket` pre-check; settlement's
  retry classification maps from `chain-commons/errors.Classify`.
- Tests: simchain for happy path, revert, replacement after stall,
  restart-resume with a pending intent, adopt-then-confirm; rotation and
  wire-compat suites; integration-stack real-process run.

### Stage 5 — Round clock in payment-daemon
- `clock/onchain` → `roundclock` + `timesource/poller` +
  `bondingmanager` reads behind `providers.Clock`.

## 5. Rollout

Images pushed 2026-09-04 (`f9b68fb`) carry the pre-rename flags. The
next image push happens after this plan lands, so the secure-orch host
moves once: rename its three env values, pull, up. The broker host
rollout (broker + coordinator + payment-daemon) follows on the images
built from this plan's final commit.

## 6. Status log

- 2026-09-04 — plan opened; commits `eda0a56` and `f4c65f4` (one flag,
  one list, env layout) are the precondition.
- 2026-09-04 — stages 0–3 landed: `ecb84a1` (parser), `fc04d5a`
  (simchain, Adopt, NotFound class), `2cc5270` (executor transport +
  env list), `334522c` (payment-daemon transport, Controller resolver,
  gas oracle). Coverage gates installed in both modules; pre-existing
  debt exempted against `lnm-tou.7`. Stage 4 next.
- 2026-09-04 — stages 4 and 5 landed: `2a8feab` (executor payouts on
  txintent), `09cc28b` (ticket redemption on txintent), `494533f`
  (txintent first-submit race found by the redemption tests),
  `5d8ea89` (payment-daemon image copies chain-commons), and the round
  clock commit. Both images build. All five modules green on build,
  vet, test, lint and the coverage gate. Remaining before the plan
  moves to completed/: the integration-stack real-process run (§3
  layer 3), which uses real keys on mainnet and is the operator's
  call, and the image rebuild + push that the broker rollout needs.

---
title: Core beliefs (protocol-daemon)
status: accepted
last-reviewed: 2026-04-26
---

# Core beliefs

Module-specific invariants on top of the monorepo-wide [`core-beliefs.md`](../../../docs/design-docs/core-beliefs.md). Every line of code in this module respects both.

## Module-specific clauses

1. **Every on-chain write goes through `chain-commons.services.txintent`.** No service code calls `keystore.SignTx` + `rpc.SendTransaction` directly. The TxIntent state machine provides idempotency, replacement, reorg recovery, and restart resume; bypassing it gives up all four.

2. **Idempotency by `(Kind, KeyParams)`, never by client-generated UUID.**
   - `InitializeRound`: `KeyParams = round.Number.Bytes()`
   - `RewardWithHint`: `KeyParams = round.Number.Bytes() ++ orchAddr.Bytes()`
   - The same logical operation always hashes to the same IntentID.

3. **Mode-specific RPCs return `Unimplemented`, never a confusing error.** If the daemon is `--mode=reward`, calling `ForceInitializeRound` returns `ErrUnimplemented`, not "round-init service is nil".

4. **Preflight refuses to start the daemon on misconfiguration.** Specifically: chain-id mismatch, missing Controller addresses, no contract code at the resolved address, undecryptable keystore, wallet balance below `--min-balance-wei`. Each surfaces a structured error code.

5. **Pool-hint cache is a fast path, never a correctness mechanism.** The cache hit short-circuits the RPC walk; the cache miss falls through to the walk. The TxIntent's `KeyParams = round||orch` is the only correctness mechanism for "one reward per round per orch."

6. **Pool walks are bounded.** The cache eviction window is 5 rounds (`PurgeWindow`). Older entries are evicted on each new-round handler call. Operators with very long running daemons don't accumulate unbounded BoltDB pages.

7. **Reward earnings are observed by parsing `BondingManager.Reward` event logs from the receipt, not by calling `Minter`.** Keeps `chain-commons.txintent` workload-agnostic; the reward service decodes the event from `Receipt.Logs` and emits `livepeer_protocol_reward_earned_wei_total`.

8. **Init-jitter is opt-in, not load-bearing.** `--init-jitter=30s` (default 0) introduces a uniform random delay before the round-init `Submit`. With TxIntent idempotency, a colliding `InitializeRound` from another daemon submitting at the same instant is a no-op (returns the existing IntentID); jitter is an optimization to save gas on collision, not a correctness mechanism.

9. **No `prometheus/client_golang` outside `internal/runtime/metrics/` or `cmd/`.** Other packages emit through the `chain-commons.providers.metrics.Recorder` interface. The Prometheus decorator lives in cmd; the listener and metric-name constants live in `internal/runtime/metrics/`. Enforced by `lint/layer-check/`.

10. **No raw `bbolt` outside `internal/repo/poolhints/`** (and even there, only via `chain-commons.providers.store.Bolt`). Enforced by `lint/layer-check/`.

11. **The orchestrator address is operator-supplied, not derived from the keystore.** A common production setup runs a hot-wallet keystore for signing and a cold-orch identity registered on-chain as the transcoder. The daemon supports this split via `--orch-address`. Preflight emits a hot/cold-split log line when the wallet and orch addresses differ.

## What we explicitly do NOT do

- We don't replicate `go-livepeer`'s in-memory tx queue. TxIntent's BoltDB-backed state machine is the canonical replacement.
- We don't multi-orchestrator. One keystore = one orchestrator address. Operators with multiple identities run multiple daemons.
- We don't do gas funding. Operator's job. Preflight refuses to start when wallet is below the threshold.
- We don't manage bonding / unbonding / delegation. Operator-driven via existing tooling.
- We don't compute reward inflation predictions. The Minter binding is read-only and currently unused; reserved for future inflation-prediction work.

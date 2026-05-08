# PRODUCT SENSE — protocol-daemon

Who this is for and what "good" looks like.

## Audience

Livepeer orchestrator operators. Specifically, operators who want to run round-init and reward as discrete responsibilities outside of `go-livepeer`'s monolithic process — for crash isolation, language flexibility, or to host an orchestrator on a non-go-livepeer media stack.

A typical deployment:

- An external workload runtime (transcode, inference, etc.) handling capacity, in its own repo.
- One `payment-daemon` redeeming probabilistic tickets.
- One `service-registry-daemon` advertising capabilities.
- One `protocol-daemon` (this one) keeping the on-chain protocol responsibilities current.
- A V3 keystore wallet funded with enough ETH to cover ~50 reward calls.

## What "good" looks like

- Operator runs `--mode=both --eth-urls=URL1,URL2 --controller-address=0x... --keystore-path=...`. Everything else picks safe defaults.
- Daemon refuses to start on misconfiguration with a structured log line that names the failing gate.
- Round transitions trigger exactly one on-chain `initializeRound` call — even if the operator runs the daemon on multiple machines.
- Reward calls land at the right transcoder-pool position the first time. Pool walks are cached, not re-walked every round.
- A reorg-out of a mined `rewardWithHint` is recovered automatically (TxIntent resubmits at the same nonce).
- Operator can call `ForceInitializeRound` or `ForceRewardCall` over the gRPC unix socket to nudge a stuck round.
- Operator can `StreamRoundEvents` to subscribe to round transitions from another local consumer (e.g. an external workload binary resetting capacity per round).

## What "bad" looks like (and we should never ship)

- Daemon submits two on-chain transactions for the same round. (TxIntent prevents this.)
- Daemon silently no-ops a round because of an RPC error. (We log + emit a metric; operator decides.)
- Daemon corrupts BoltDB on crash. (Single-writer; we don't multi-process the same store.)
- A misconfigured keystore lets the daemon boot anyway. (Preflight refuses.)
- A wallet running out of ETH lets the daemon waste rounds. (Min-balance preflight + `livepeer_protocol_active_status` gauge.)

## Non-goals

- Bonding / unbonding / delegation. Operator-driven; existing tooling handles it.
- Fee accounting / earnings dashboard. We emit `livepeer_protocol_reward_earned_wei_total`; Grafana aggregates.
- Automatic gas funding. Operator's job.
- Multi-orchestrator-per-daemon. One keystore, one orchestrator address. Operators with multiple identities run multiple daemons.

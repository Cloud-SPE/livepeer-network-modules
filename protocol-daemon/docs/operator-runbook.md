---
title: protocol-daemon — operator runbook
status: accepted
last-reviewed: 2026-05-08
audience: orchestrator operators, on-call
---

# Operator runbook

`protocol-daemon` is the chain-side daemon for an orchestrator. In the rewrite stack it
does three jobs:

- initialize new rounds
- call rewards for the configured orchestrator
- write and read the on-chain `ServiceRegistry` / `AIServiceRegistry` URI pointers

It is not part of the inference data path and it does not build or sign the manifest.
Those stay with:

- `orch-coordinator` — builds and publishes the signed manifest
- `secure-orch-console` — cold-signs the candidate manifest

## 1. Modes

One binary, three modes:

| Mode | What runs |
|---|---|
| `--mode=round-init` | round initialization only |
| `--mode=reward` | reward calling only |
| `--mode=both` | both services in one process |

The common production shape is `--mode=both`.

## 2. Boot

```sh
livepeer-protocol-daemon \
  --mode=both \
  --socket=/var/run/livepeer/protocol.sock \
  --store-path=/var/lib/livepeer/protocol.db \
  --eth-urls=https://arb1.arbitrum.io/rpc,https://arbitrum.publicnode.com \
  --chain-id=42161 \
  --controller-address=0xD8E8328501E9645d16Cf49539efC04f734606ee4 \
  --keystore-path=/etc/livepeer/keystore.json \
  --keystore-password-file=/etc/livepeer/keystore-password \
  --orch-address=0xYOUR_COLD_ORCH_ADDRESS \
  --metrics-listen=:9094
```

Required inputs:

- a V3 JSON keystore
- the keystore password, via `--keystore-password-file` or `LIVEPEER_KEYSTORE_PASSWORD`
- `--eth-urls`
- `--orch-address` for `reward` and `both`
- writable state at `--store-path`
- writable unix-socket directory for `--socket`

## 3. What it talks to

- **Chain RPC** over the URLs in `--eth-urls`
- **Local operators / local tools** over the unix socket at `--socket`
- **Prometheus** optionally over `--metrics-listen`

The daemon does not expose an unauthenticated TCP admin API. Its operator RPC surface is
over a local unix socket.

## 4. Rewrite flow

The manifest-publication flow is:

1. `orch-coordinator` publishes the signed manifest at:
   - `https://<coordinator-host>/.well-known/livepeer-registry.json`
2. `protocol-daemon` writes that URL on chain with `SetServiceURI`
3. external resolvers and gateways use the on-chain pointer, then fetch the manifest from
   the coordinator

So when you use the service-registry RPCs in this daemon, the URI should point at the
public coordinator URL, not at `secure-orch-console` and not at any old publisher file
path.

## 5. Compose

Run-only compose:

- `protocol-daemon/compose/docker-compose.yml`

Local component-level compose:

- `protocol-daemon/compose.yaml`

Example env file:

- `protocol-daemon/compose/.env.example`
- `protocol-daemon/compose/.env.useast-coordinator.example`

The published image build is wired into:

- `./infra/scripts/build-images.sh protocol-daemon`

### USEast example

If your public `orch-coordinator` runs in USEast, the chain-side service URI should look
like:

```text
https://useast-coordinator.example.com/.well-known/livepeer-registry.json
```

The provided example env file records that explicitly:

- `protocol-daemon/compose/.env.useast-coordinator.example`

## 6. Metrics and health

`--metrics-listen` is optional. When set, it exposes Prometheus metrics for:

- round-init activity
- reward activity
- tx-intent processing
- process health

If you do not need Prometheus on the host, leave `--metrics-listen` empty.

## 7. Common failure modes

### Preflight failure

The daemon exits before opening the socket when:

- RPC connectivity is broken
- the keystore cannot be decrypted
- controller-resolved contracts are missing
- the wallet balance is below `--min-balance-wei`
- `--orch-address` is missing in reward-capable modes

Action: fix config or chain connectivity first. This is an intentional fail-fast gate.

### Reward not firing

Common reasons:

- the daemon is running `round-init` mode instead of `reward` / `both`
- the configured orch is not reward-eligible on chain
- the daemon cannot resolve `BondingManager`
- tx-intent submission is failing due to gas / RPC / keystore problems

Action: inspect daemon logs and status RPCs; check the configured `--orch-address`.

### Round initialization not firing

Common reasons:

- another orchestrator already initialized the round first
- the daemon is running `reward` mode instead of `round-init` / `both`
- `RoundsManager` resolution or tx submission is failing

Action: inspect logs first; “someone else initialized it” is not itself a fault.

### Service URI wrong on chain

Most likely causes:

- old publisher-style URI used instead of the public coordinator URL
- typo in the hostname
- coordinator public listener not reachable from the outside

Action: verify the coordinator URL directly before calling `SetServiceURI`:

```sh
curl -s https://<coordinator-host>/.well-known/livepeer-registry.json
```

Then set that exact URL on chain.

## 8. Operational rule

Do not point the chain-side service URI at:

- `secure-orch-console`
- Docker-internal hostnames like `capability-broker` or `xode_capability_broker`
- a LAN-only coordinator URL if external consumers need to resolve it

Point it at the public `orch-coordinator` manifest URL instead.

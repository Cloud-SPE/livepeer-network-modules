# AGENTS.md

`pool-payout-executor` is the Pool payout-submission worker boundary.

## Scope

- Reads and leases payout intents from `pool-controller`
- Prepares executor batches
- Signs and submits native-`ETH` payouts on Arbitrum through chain-commons
  transaction intents, and confirms them
- Writes payout status updates back to `pool-controller`

## Constraints

- Native `ETH` on Arbitrum is the chosen v1 payout rail. Do not add another
  rail, asset or signing path unless the user explicitly chooses it.
- Preserve `pool-controller` as the accounting source of truth.
- Keep nonce ownership, replacement and confirmation in chain-commons
  transaction intents; do not send transactions around the intent processor.
- Regional executors are bound to one pool, wallet and chain
  ([`docs/regional-wallets.md`](docs/regional-wallets.md)); never weaken that
  identity check or delete a store to pass it.

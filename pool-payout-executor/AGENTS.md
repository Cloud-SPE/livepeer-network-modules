# AGENTS.md

`pool-payout-executor` is the Pool payout-submission worker boundary.

## Scope

- Reads payout intents from `pool-controller`
- Prepares executor batches
- Writes payout status updates back to `pool-controller`

## Constraints

- Do not add signing or chain transport code unless the user explicitly chooses
  the payout rail.
- Preserve `pool-controller` as the accounting source of truth.
- Keep executor logic transport-agnostic until the payout mechanism is settled.

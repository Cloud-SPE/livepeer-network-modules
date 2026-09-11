# chain-probe — authorization-only paid path on a real chain

The chain probe exercises wholesale-account funding, single-purpose spend
authorization, broker invocation, settlement, and restart recovery against
real payer and receiver daemons. It is deliberately outside CI because it can
create redeemable probabilistic tickets with real value.

## Cost

Minting a ticket creates a lottery claim. A winning ticket can draw its face
value from the payer deposit and costs the receiver gas to redeem. Review
`--max-payment-wei`, `--max-ticket-face-value-wei`, target float, wallet,
chain, and recipient before running.

## Supported modes

| `--protocol` | Purpose |
|---|---|
| `wholesale` | Account shortfall funding, unrelated job/session authorizations, delegated caller proof, concurrency, replay, cumulative usage, and residual release. |
| `wholesale-recovery-prepare` | Create durable mint/authorization/account state and a checkpoint before daemon restart. |
| `wholesale-recovery-verify` | Verify the checkpoint after restart, replay idempotently, and settle the held authorization. |
| `wholesale-evidence` | Reconcile signed broker and daemon evidence for saved checkpoints. |

Legacy `job`, `session`, `both`, `rotation`, `retry`, and `evidence`
modes were payment-only workload probes and have been removed. Tickets are
tested only as account-funding instruments.

## Wholesale run

Bring up a payer, receiver, authorization-only broker, and the conformance
session runner against one chain:

```bash
go run ./cmd/livepeer-chain-probe \
  --protocol=wholesale \
  --chain-id=42161 \
  --recipient=0x... \
  --broker-url=https://broker.example \
  --broker-uri=https://broker.example \
  --capability=conformance:job \
  --offering=all \
  --work-unit=tokens \
  --price-wei=100 \
  --per-units=1000 \
  --max-authorization-units=131072 \
  --session-capability=conformance:session \
  --session-offering=default \
  --session-work-unit=participant_minutes \
  --session-price-wei=100 \
  --session-per-units=1 \
  --session-max-authorization-units=600 \
  --session-runner-control-url=http://runner:8092
```

The first operation restores the stable payer-payee account to the configured
`--account-float-wei`. After actual work settles and unused reservation is
released, the next mint covers only actual aggregate shortfall—not another
maximum-sized workload. Replays must not mint, credit, reserve, execute, or
debit twice.

Choose target float from aggregate burn, concurrency, refill latency, and
route-exit tolerance. It must cover intended concurrent reservations, but it
must not mirror the sum of customer maxima.

## Recovery

Use a persistent `--checkpoint-file`:

```bash
go run ./cmd/livepeer-chain-probe --protocol=wholesale-recovery-prepare \
  --checkpoint-file=/var/lib/livepeer/probe/recovery.json ...

# Restart payer, receiver, and broker with their original persistent stores.

go run ./cmd/livepeer-chain-probe --protocol=wholesale-recovery-verify \
  --checkpoint-file=/var/lib/livepeer/probe/recovery.json ...
```

Verification requires identical mint replay, unchanged account totals,
surviving authorization reservation, broker settlement recovery, and
exactly-once release. Losing authorization state while a broker session
survives must fail closed; it is not repaired by creating a replacement
ticket-session workload payment.

## Assertions

The probe checks money and durable evidence, not log strings:

- funding EV moves into one stable account;
- account conservation holds across credited, available, reserved, and
  debited value;
- authorization scope, request digest, caller proof, and accepted quote bind
  the invocation;
- settlement bills measured units on the pinned cumulative curve;
- unused reservation returns to aggregate availability;
- request, mint, and authorization replay are exactly-once; and
- signed evidence is directly reconcilable without SDK callback delivery.

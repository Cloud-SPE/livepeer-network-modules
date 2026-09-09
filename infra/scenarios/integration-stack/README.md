# Arbitrum One wholesale-account pilot

This scenario validates reusable wholesale payer-payee credit against the real
Livepeer TicketBroker while keeping ticket issuance behind an explicit approval
gate. It uses the generic conformance workload names; no retail provider is
part of the protocol identity.

The stack is Docker-first and runs pinned images for:

- receiver `payment-daemon` and `capability-broker` (the payee side);
- an attached conformance runner (the workload side);
- sender `payment-daemon` (the payer/provider side);
- `livepeer-chain-probe`, invoked only by `pilot.sh`.

All ledgers and broker idempotency state live in named volumes. `down.sh` keeps
them. Do not use `docker compose down -v` during a pilot or recovery drill.

## Safety and cost

A payment is a probabilistic ticket batch. Issuing a ticket commits expected
value and exposes its full winning face value; it does not immediately transfer
the expected value. If a ticket wins and is redeemed, the face value is drawn
from the payer's on-chain deposit and the payee pays redemption gas.
“Dust” describes the expected value of this controlled run, not its tail loss.
The example deliberately uses an artificial test price: for its
1-trillion-wei refill, the receiver retains a 0.001 ETH redeemable winning face
and selects roughly 1/1000 probability. The face-value limit must still be
reviewed as carefully as the expected-value limit; the test price is not a
production price.

Three independent sender limits are required:

- `MAX_PAYMENT_WEI`: expected value in one refill;
- `MAX_TICKET_FACE_VALUE_WEI`: worst-case payout if one ticket wins;
- `MAX_AUTHORIZATION_WEI`: maximum wholesale debit for one workload.

`ACCOUNT_FLOAT_WEI` is reusable service credit held on this payer-payee route.
Size it for the largest concurrently admitted reservation plus a small buffer,
not the sum of customer request ceilings. Route exit can still strand up to the
remaining bounded float because v1 does not promise withdrawal or transfer.

## Prepare without spending

1. Build and publish the current images. Record immutable `@sha256:` references.
2. Copy `stack.env.example` to ignored `stack.env` and fill exact addresses,
   key paths, external broker origin, RPCs, and limits.
   The payer/payee keystore and password files must be readable by container
   uid 65532; do not make them world-readable.
3. Verify the payer deposit/reserve, payee address, and external origin with a
   second operator.
4. Run the fail-closed preflight. It rejects legacy/incomplete configuration,
   mutable image tags, non-Arbitrum RPCs, a payee-address/key mismatch,
   inconsistent economic ceilings, persisted approval, and a non-TLS public
   broker origin:

   ```bash
   ./preflight.sh startup
   ```

5. Start the stack:

   ```bash
   ./up.sh
   ./status.sh
   ```

`up.sh` enforces migration order: receiver first, broker advertisement and
runner attachment second, payer last. It does not run the probe or mint a
ticket. The offer advertises `extra.features.wholesale_accounts: true` only
after the upgraded receiver is listening.

## Run the approved dust pilot

Approval is intentionally invocation-scoped and must not be saved in
`stack.env`:

```bash
PILOT_APPROVAL=ARBITRUM_ONE_DUST_APPROVED ./pilot.sh
```

The run performs:

1. a generic in-path provider job with a 131,072-unit authorization;
2. actual work of only the runner-reported units, releasing unused reserve;
3. a second 131,072-unit authorization delegated to an ephemeral caller key;
4. a refill equal only to the account shortfall, not the workload ceiling;
5. an identical transport retry that must not mint, execute, reserve, or debit
   again;
6. simultaneous authorizations, admitting exactly the count the shared balance
   can afford and admitting any loser only after release, without another mint;
7. provider and delegated-caller sessions with bounded initial runway,
   cumulative usage, idempotent top-up, actual settlement, and residual release;
8. conservation reconciliation across credited, reserved, debited, and
   available wholesale value.

The probe signs the configured `EXTERNAL_BASE_URL` into each authorization even
though its container reaches the broker at `http://broker:8080`. This matches a
clearinghouse or provider whose internal control plane and public locked route
use different network names.

To exercise a real stop/restart boundary, use the separately approval-gated
recovery probe:

```bash
PILOT_APPROVAL=ARBITRUM_ONE_DUST_APPROVED ./pilot-recovery.sh
```

It writes a non-secret checkpoint to the persistent `probe-state` volume,
leaves one authorization admitted, stops payer, runner, broker, and payee,
restarts them in receiver-first order, and then verifies the identical mint
replay, account and authorization continuity, durable settlement lookup, and
idempotent reservation release. Each invocation uses a new checkpoint name;
retained checkpoints contain identifiers and account totals, not payment
envelopes, credentials, or private keys.

## Evidence and drain

After the approved standard and recovery probes complete, capture the
redact-safe bundle:

```bash
./evidence.sh post-pilot
```

It writes an owner-only directory below ignored `run/` containing:

- all three resolved image digests;
- chain ID, payer/payee addresses, limits, price, target float, and timestamps;
- probe output and broker/daemon version lines;
- account observations before and after, signed settlement identifiers, and
  wholesale metrics;
- retry/restart results and the final drain observation.

The bundle intentionally excludes keystore paths and contents, passwords,
admin tokens, RPC URLs, and payment/authorization envelopes. Its `SHA256SUMS`
detects later evidence mutation.

Drain only after admitted work settles:

```bash
./drain.sh
```

The command captures pre-drain evidence, refuses while any wholesale value is
reserved, checks account conservation and the bounded residual, writes the
`run/DRAINING` admission fence, stops the runner, waits until neither pilot
offering is selectable, and captures post-drain evidence. It preserves payer,
payee, broker, checkpoint, and ledger volumes. Removing the route does not
erase or refund account credit.

`pilot.sh`, `pilot-recovery.sh`, and `up.sh` all refuse while the marker exists.
To resume deliberately, preserve the marker as audit evidence before startup:

```bash
mv run/DRAINING "run/DRAINED-$(date -u +%Y%m%dT%H%M%SZ)"
./up.sh
```

To stop the remaining control plane after evidence capture:

```bash
./down.sh
```

Shipping this harness does not close the rollout bead: both approved probes
must run against the pinned production images, and their restart plus
rollback/drain evidence must be captured before the mainnet rollout is
considered complete.

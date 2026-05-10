---
title: payment-daemon — operator runbook
status: accepted
last-reviewed: 2026-05-06
audience: orchestrator operators, gateway operators, on-call
---

# Operator runbook

This runbook is for two audiences:

- **Orchestrator operators** running the daemon in `--mode receiver` to
  validate payments, track per-sender balances, and (post chain
  integration) redeem winning tickets on-chain.
- **Gateway operators** running the daemon in `--mode sender` next to a
  paying app — an OpenAI-compat gateway, a transcoding gateway, anything
  that needs to mint Livepeer payment envelopes per request.

If you are a developer hacking on the daemon code, start with
`AGENTS.md` and `DESIGN.md` instead. This document is operational, not
architectural.

> **v0.2 scope warning.** This daemon is production-shaped but
> chain-stubbed. Cryptographic signatures use a deterministic dev key,
> escrow and reserve queries are stubbed, and winning tickets are queued
> but never submitted. Plan 0016 wires the real chain providers behind
> the same operator surface this runbook describes; this document is
> what plan 0016 lands against. Until then, **do not deposit real funds
> against this daemon**, and **do not point a v0.2 daemon at production
> traffic that expects redeemable tickets**.

---

## 1. Two modes, one binary

The same `livepeer-payment-daemon` binary runs as either a sender or a
receiver. The mode is selected at boot via `--mode=sender|receiver`, and
the choice is permanent for the lifetime of the process — you do not
switch modes at runtime, you restart the binary.

| Aspect | `--mode=sender` | `--mode=receiver` |
|---|---|---|
| Who runs it | Gateway operators (anyone paying an orchestrator) | Orchestrator operators (anyone getting paid) |
| gRPC service | `PayerDaemon` | `PayeeDaemon` |
| Key role | Signs tickets | Validates tickets, redeems winners |
| Persistence | None (sessions in memory) | BoltDB (sessions, redemption queue, nonce ledger) |
| Chain interaction (post-0016) | Reads sender escrow + reserve at ticket-creation time | Submits redemption txs for winners |
| Failure surface | Refuses to mint tickets when sender escrow runs dry | Refuses to credit balances on invalid signatures |

Calls to the wrong service surface return gRPC `Unimplemented`. A
sender-mode daemon does not expose `PayeeDaemon.ProcessPayment`; a
receiver-mode daemon does not expose `PayerDaemon.CreatePayment`.

---

## 2. Probabilistic-micropayment economics

The protocol does not transfer money on every request. It mints
**tickets** — small signed claims that pay out a large `face_value` if
they win, with `win_prob` chosen so the **expected value** matches the
work performed:

```
EV  =  face_value × win_prob / 2^256
```

A request worth `EV` wei produces (typically) one ticket with `face_value
≫ EV` and a tiny `win_prob`. Most tickets do not win. Winners get
redeemed on-chain. Over many requests the law of large numbers makes the
economics work out to the agreed unit price.

### Why the asymmetry between face_value and win_prob

This is the central insight that separates this protocol from naive
"send a wei every API call":

> **Redeeming a winning ticket on-chain costs gas. If `face_value` is
> too small, the redemption tx itself loses money. So `face_value` must
> be much larger than the redemption-tx cost, and `win_prob` must be
> small enough that the EV stays in line with the actual work charged.**

Concretely: on Arbitrum One in 2026, redemption gas is ~500,000 units
at ~0.1 gwei base fee = ~50,000 gwei = 0.00005 ETH ≈ tens of cents at
ETH=$3,000. To redeem profitably, `face_value` should be at least an
order of magnitude above tx cost — call it $5–$10 worth of LPT — and
`win_prob` is computed from there.

If you are pricing a request at $0.001, the math is:
- face_value = ~$5 worth of LPT
- win_prob = $0.001 / $5 = 1 / 5000
- → 1 in 5000 tickets wins; each winner pays for $5 of work + gas

Sender and receiver both compute these numbers; the receiver authors
the params, the sender validates them are economically reasonable
before signing.

### Operator-facing knobs

| Knob | Default | Side | What it controls |
|---|---|---|---|
| `--receiver-ev` | 1e12 wei | receiver | Target per-ticket expected value. Receiver sizes face_value × win_prob to hit this. Smaller = more tickets per request, higher overhead, finer billing granularity. Larger = fewer tickets, coarser. |
| `--receiver-tx-cost-multiplier` | 100 | receiver | Receiver targets `face_value = redemption-tx-cost × multiplier`. 100× = winners pay 100× the gas cost, leaving 99% for the operator. |
| `--redeem-gas` | 500000 | receiver | Estimated gas for a `redeemWinningTicket` tx. Used to compute the face_value floor. Match to the chain you're on; Arbitrum L2 ≈ 500k. |
| `--gas-price-multiplier-pct` | 200 | receiver | Headroom on the chain's `eth_gasPrice`. 200% = 2.0×. Protects against base-fee spikes between price-read and tx-submit on EIP-1559 chains. **Lower to 100 only on stable-price chains; raise to 300 if your provider's gas estimates are unusually conservative.** |
| `MaxEV` (sender config) | unset | sender | Caps per-ticket EV. Refuses to sign tickets where `face_value × win_prob > MaxEV`. Protects payer from a malicious or buggy receiver claiming a $1000 ticket on a $0.001 request. |
| `MaxTotalEV` (sender config) | unset | sender | Caps cumulative EV across a single batch (size × per-ticket EV). Same protection at batch granularity. |
| `DepositMultiplier` (sender config) | 0 (disabled) | sender | Caps `face_value ≤ Deposit / DepositMultiplier`. Bounds in-flight exposure as a fraction of escrow. |

The sender knobs are the gateway operator's defense. **Run with them
set.** A misconfigured or compromised receiver should never be able to
mint $X worth of expected loss against a sender that was sized for $Y ≪
X.

---

## 3. Sender escrow and reserve

The TicketBroker contract holds two sender-controlled pools:

- **Deposit** — first pool that pays out winning tickets. Replenished by
  the sender via direct contract calls.
- **Reserve** — fallback pool when deposit runs dry. Claimed by recipients
  per-round, contract-limited to prevent sybil drain.
- **WithdrawRound** — non-zero iff the sender has initiated an unlock.
  No new tickets can be sent until this round passes.

### maxFloat — the per-sender ceiling

A single ticket's `face_value` cannot exceed the sender's `maxFloat`,
which the receiver computes per-recipient at ticket-params time:

```
if pendingAmount == 0:
  maxFloat = reserveAlloc + deposit
else if deposit / pendingAmount ≥ 3.0:
  maxFloat = reserveAlloc + deposit          # ignore pending if deposit is 3x larger
else:
  maxFloat = reserveAlloc + deposit - pendingAmount
```

`pendingAmount` is the total face_value of tickets currently in the
redemption pipeline (queued or submitted, not yet confirmed).

The 3:1 heuristic prevents legitimate redemption activity from
starving new ticket creation. **If pending grows beyond `deposit/3`,
maxFloat shrinks** — operators should watch the ratio.

### Sender validation rejection cases

`PayerDaemon.CreatePayment` will refuse to sign a ticket when:

- **No deposit** — sender has zero deposit; nothing to pay out from.
- **No reserve** — sender has zero reserve; no fallback pool.
- **Pending unlock imminent** — `WithdrawRound != 0` and the lock
  period is about to expire. Once unlock completes, the sender is free
  to withdraw their funds; signing tickets after that point creates
  unrecoverable obligations.

The corresponding error returned to the gateway is
`SenderValidationError`; the message identifies which reason fired.

### Unlock / withdrawal flow

If a sender wants to drain their escrow:

1. Call `TicketBroker.unlock()` directly. Sets `WithdrawRound = current
   + lockPeriod`.
2. Wait for the lock period (one Livepeer round in the production
   protocol).
3. Call `TicketBroker.withdraw()`.

The daemon detects step 1 via the `Broker` provider's
`SenderInfo.WithdrawRound` field and rejects new tickets accordingly.
There is no daemon-initiated unlock; this is a deliberate operator
decision and stays out-of-band.

---

## 4. Gas economics and redemption (receiver side)

Winning tickets accumulate in a durable redemption queue (BoltDB-backed)
and a background loop submits them on-chain.

### Redemption loop

Configurable via `--redemption-interval` (default 30s). On each tick:

1. Pop the oldest queued winner that hasn't been submitted.
2. Run pre-checks (see "Gas validation pre-checks" below). Drop the
   ticket if a check fails.
3. Submit the redemption transaction via the configured RPC endpoint.
4. Wait for the receipt.
5. Wait for **`--redemption-confirmations`** additional blocks (default
   4) before marking the ticket `MarkRedeemed`.
6. Repeat.

### Why redemption-confirmations matters

The default of 4 protects against L1 reorgs that would cause the same
ticket to be submitted twice to the contract's `usedTickets` set. A
shorter window is faster revenue recognition but exposes you to reorgs;
a longer window is safer but defers revenue. Tune for the chain:

- **Arbitrum One**: 4 (default) is appropriate. Reorgs within Arbitrum
  Nitro are extremely rare past a few blocks.
- **L1 mainnet**: use 12+ for safety against typical reorgs.
- **Test L2s**: 0 (return on first receipt) is fine for development.

### Gas validation pre-checks

Before each redemption submission, the daemon checks:

| Check | Failure mode | Operator-actionable? |
|---|---|---|
| `ErrTicketExpired` — ticket's `CreationRound` is older than `Clock.LastInitializedRound() - ValidityWindow` (default 2 rounds) | Ticket is dropped; receiver loses the EV. | Increase `--validity-window` if your block time is unusually slow; otherwise, this is a payer problem (their batch sat too long before redemption). |
| `ErrFaceValueTooLow` — ticket face_value < estimated redemption-tx cost | Redeeming would lose money to gas. Ticket is dropped; receiver loses the EV. | Tune `--receiver-tx-cost-multiplier` higher when issuing future tickets to avoid this. Past tickets can't be retroactively fixed. |
| `ErrInsufficientFunds` — sender's available funds (deposit + reserve - pending) < redemption-tx cost | Submission would revert at the contract. Ticket is left queued and retried. | Watch for this in logs as a leading indicator of a sender draining their escrow. |

The pre-checks save gas; the alternative is submitting a tx that
reverts on-chain and paying the gas anyway.

### Gas-price multiplier

`--gas-price-multiplier-pct` (default 200, validated `>= 100`):

```
submitted_gas_price = eth_gasPrice × multiplier_pct / 100
```

200% (2.0×) is the Arbitrum One default. The reasoning: between the
moment we read `eth_gasPrice` (or `eth_feeHistory` on EIP-1559) and the
moment our tx lands in a block, the base fee can tick up. A 2× headroom
means we tolerate a single base-fee tick without "max fee per gas less
than block base fee" rejections. On stable-price chains, lower it to
~110–120; on volatile chains or when your provider is conservative,
raise to 300.

---

## 5. Identity: hot wallet vs cold orchestrator

The wallet that signs tickets and the on-chain orchestrator identity
**should not be the same**. The signing wallet is hot — it sits on the
machine running the daemon, accessible to anything that has filesystem
access to the keystore. The orchestrator identity is the entry in the
BondingManager that earns inflation; it should be cold.

The `--orch-address` flag separates the two:

- Default: empty → daemon uses the loaded keystore's address as the
  recipient embedded in tickets.
- Set: → daemon uses the supplied address as the recipient. The hot
  wallet only signs; it never appears in the on-chain ticket.

Setup checklist:

1. Generate a hot signing key (V3 keystore JSON).
2. Authorize that key under the orchestrator identity via the
   contracts (`BondingManager.setSigningAddress` or the equivalent).
3. Set `--orch-address` to the cold orchestrator address.
4. The hot key's exposure ceiling is now the deposit/reserve
   replenishment cycle, not the entire orchestrator balance.

### Single-wallet vs hot/cold split — what the daemon logs

When the V3 keystore is loaded (production mode, `--chain-rpc` set)
the daemon logs one of two startup lines:

- `WARN single-wallet config — hot signer is also the on-chain
  orchestrator identity. OK for dev, dangerous for prod.` — fires when
  `--orch-address` is empty (recipient defaults to the keystore
  address) or explicitly equals it. **The daemon does not block
  startup on this** (locked in plan 0017 §11.5): solo-operator
  single-wallet setups are legitimate; the WARN exists to make a
  misconfigured hot/cold split visible.
- `INFO hot/cold split active signer=0x… orch_address=0x…` — fires
  when the addresses differ. This is the production-shaped state.

A malformed `--orch-address` (not 40 hex chars) is treated as the
single-wallet case so the WARN still fires; the daemon does not
hard-block. Operators should fix the flag so the INFO line appears
instead.

### 5.1 Hot-key rotation runbook (5 steps, on-call card)

Source: plan 0017 §6.5. The daemon does not orchestrate rotation —
rotation is off-host, operator-driven; the daemon makes it safe by
being restart-tolerant. Every record in the BoltDB session ledger and
(post-0016) the redemption queue is keystore-agnostic, so a rotation
does not lose state.

1. **Generate a new V3 keystore on the host.** Put the password in
   the secret store under a new key. Example:
   `geth account new --keystore /etc/livepeer/keystore.next/`.
2. **From the cold wallet, call
   `BondingManager.setSigningAddress(NEW_HOT_ADDRESS)`.** The
   protocol will now accept redemption txs from the new hot key.
   `setSigningAddress` replaces the previously-authorized signer; if
   you want a brief dual-running window, see plan 0017 §6.1.
3. **Update the password source and `--keystore-path`.** Either set
   `LIVEPEER_KEYSTORE_PASSWORD` to the new password, or update
   `--keystore-password-file` to a file containing it. Both set is an
   error.
4. **Restart the daemon.** Brief unavailability (seconds) on socket
   restart; senders see transient `connection refused`; receivers
   lose nothing — the redemption queue is durable in BoltDB. Verify
   the startup log shows the new `address_hex` on the
   `keystore unlocked` and `receiver identity` / `sender identity`
   lines.
5. **Wait for `setSigningAddress` confirmations; delete the old
   keystore file from the host.** Best practice: physically remove
   the old V3 JSON once revocation lands on chain so a filesystem
   compromise after rotation can't recover it.

In-flight tickets keep working across the rotation:

- **Sender side:** signed tickets are already in the recipient's
  hands; they remain valid because the cold-orch identity (the
  ticket's `Recipient`) hasn't changed.
- **Receiver side:** queue items were signed by *senders*, not by us.
  Our hot key signs the redemption tx itself; the new hot key signs
  new redemption txs after restart.

---

## 6. Ticket-params lifecycle

The receiver issues `TicketParams` (recipient, face_value, win_prob,
recipient_rand_hash, seed, expiration_block, expiration_params). The
sender uses them verbatim when constructing each ticket. Two key
operator concerns:

### `recipient_rand_hash` rotation

`recipient_rand_hash = keccak256(seed)`. The receiver derives `seed`
deterministically via HMAC over (sender, capability, monotonic counter)
so the same seed cannot collide across senders. Rotating
`recipient_rand_hash` invalidates all prior tickets bound to the old
hash and forces a re-quote on the sender side. **Do not rotate
arbitrarily** — rotating is a session reset and costs the sender a
quote round-trip.

### Nonce-replay window

Per `recipient_rand_hash`, the receiver tracks **up to 600 nonces**.
Senders allocate nonces monotonically per session. If a sender crashes
and restarts with the receiver still holding its session, the sender
**MUST resume from the last-used nonce** — replaying nonces from 0
will be rejected as replays.

This is what `StartSessionWithNonce` is for in the sender code: when
the receiver hands a recovering sender back the last nonce it observed,
the sender resumes the session at that nonce + 1.

### Expiration

`expiration_block` is an L1 block number after which the params are
stale. Default lifetime is one round's worth of blocks (~6 hours on
Arbitrum). The daemon refreshes params well before expiry; payers
should not rely on a specific TTL.

`expiration_params.creation_round` and `creation_round_block_hash` are
baked into each ticket — the validity window check at the receiver
compares these to the current round at redemption time. **Tickets
older than `--validity-window` rounds are dropped at redemption.**
Default is 2 rounds.

---

## 6.5. Long-running session billing (broker interim-debit cadence)

The receiver daemon owns per-(sender, work_id) balances. The
**capability-broker** is the component that performs work and tells the
daemon "I just did N units; debit accordingly". For request/response
modes (`http-reqresp@v0`, `http-multipart@v0`) the broker calls
`PayeeDaemon.DebitBalance` exactly once at handler completion. For
**long-running modes** (`ws-realtime@v0`, `rtmp-ingress-hls-egress@v0`,
`session-control-plus-media@v0`, streaming `http-stream@v0`) the broker
runs a per-session ticker and issues a sequence of `DebitBalance` calls
plus periodic `SufficientBalance` runway checks. Plan 0015.

This section is what the gateway operator and the orchestrator operator
need to know to reason about long-running session economics together.

### 6.5.1. Broker flags

| Flag | Default | Meaning |
|---|---|---|
| `--interim-debit-interval` | `30s` | Tick cadence. Each tick computes the bytes/seconds/etc. consumed since the last tick and issues a `DebitBalance(seq=N+1, work_units=delta)`. Setting to `0` disables the ticker entirely; the broker reverts to single-debit-at-handler-close (the v0.2 behavior). Lower = tighter billing, higher RPC load on the receiver daemon; higher = more credit float on each session. |
| `--interim-debit-min-runway-units` | `60` | Minimum required runway passed to `PayeeDaemon.SufficientBalance` per tick. With the default `30s` tick on a `seconds-elapsed` workload (1 unit per second), the broker requires the session to have ≥60 seconds of credit at every tick. When `SufficientBalance` returns false, the broker terminates the session (see §6.5.3). Set to `0` to disable the runway check (debits still happen; broker keeps relaying until the handler ends). |
| `--interim-debit-grace-on-insufficient` | `0` | Grace period between observing `sufficient=false` and terminating. Reserved for the future mid-session top-up flow; the gateway-side middleware would have this much wall-clock to mint a fresh `Payment` and re-credit the daemon. v0.1 default zero (hard terminate immediately). |

### 6.5.2. Cadence + revenue-recognition latency

The interim-debit cadence sits at the front of the same end-to-end
latency the receiver operator already cares about for redemption. The
relevant chain (broker → receiver daemon → on-chain) is:

```
work performed → DebitBalance (per tick)
              → ProcessPayment credits EV (already done at session open)
              → ticket queued at receiver
              → win-prob roll on a winner
              → redemption queue → on-chain submit
              → receipt + --redemption-confirmations blocks
```

Worst-case latency from "work performed" to "revenue recognised
on-chain" is bounded by:

```
worst_case ≈ interim-debit-interval
           + redemption-interval
           + redemption-confirmations × block-time
```

On Arbitrum One with the recommended defaults (interval=30s,
redemption-interval=30s, confirmations=4 × ~250ms block time), that's
about 61s. Lowering `--interim-debit-interval` does not lower the
critical path — the redemption-interval still dominates — but it does
tighten the broker's exposure window when a payer's session balance
runs out.

### 6.5.3. Termination semantics

When `SufficientBalance` returns `sufficient=false`:

1. The broker logs at WARN: `terminating session work_id=… reason=insufficient_balance`.
2. The broker cancels the request context. The mode driver (`ws-realtime`,
   etc.) sees the cancellation, closes both halves of its relay, and
   returns.
3. The middleware performs a final `DebitBalance` for any units
   accumulated between the last tick and the cancellation point (so the
   daemon's ledger matches the bytes/seconds actually shipped).
4. `CloseSession` runs.
5. The connection closes gateway-side. For ws-realtime, the runner sees
   a server-side close. For other long-running modes, the gateway sees
   the body terminate; where the protocol allows it, the broker emits
   `Livepeer-Error: insufficient_balance` as a trailer.

The receiver operator does not see anything they wouldn't see from a
normal session close: a `CloseSession` on the daemon plus the final
`DebitBalance` call. The signal that this was a *forced* close lives in
the broker's logs and metrics, not the daemon's.

### 6.5.4. Idempotency contract

Every `DebitBalance` call is idempotent by `(sender, work_id, debit_seq)`
per `payee_daemon.proto`. The broker's ticker maintains a monotonic
counter starting at 1: tick #1 → seq=1, tick #2 → seq=2, …, final
flush → seq=N+1. **Retries reuse the same seq** until the daemon
returns success; the daemon's idempotency guard then ensures the same
delta is never applied twice. This is plan 0015 §5.3 — the broker
trades retry simplicity for keeping the daemon's `DebitBalance`
semantics unchanged from v0.2.

If you see `DebitBalance call rate exceeds expected cadence` in
operator dashboards, that's a sustained-retry signal. See §7.

**`rtmp-ingress-hls-egress@v0` work-units.** RTMP sessions emit
work-units via the FFmpeg progress extractor (see
`capability-broker/docs/operator-runbook.md` §"GPU encoder hardware"
+ §"RTMP pipeline observability"). The broker's interim-debit ticker
reads `LiveCounter.CurrentUnits()` from the encoder's progress atomic
and debits at the same cadence as ws-realtime. Termination triggers
(no_push_timeout / idle_timeout / insufficient_balance / customer
CloseSession) are all surfaced through the broker's existing
`Livepeer-Error` channel.



| Symptom | Likely cause | What to check |
|---|---|---|
| Sender returns `SenderValidationError: no sender deposit` | Payer's TicketBroker deposit hit 0. | Have the gateway operator top up via direct contract call. |
| Sender returns `SenderValidationError: pending unlock imminent` | Payer initiated `unlock()`. | Either the operator wants to drain (in which case stop sending), or it was an accident (in which case, do nothing — `unlock()` doesn't reset; either let it complete and re-lock or call `cancelUnlock()`). |
| Receiver returns ProcessPayment `signature recovery failed` | Sender's signature does not parse to a valid ETH address. | The hot signing key may be wrong, or the wire bytes were re-encoded mid-flight. Check the wire-compat round-trip test. |
| Receiver `ErrFaceValueTooLow` shows up consistently for one sender | Sender's last-known price is stale; receiver bumped face_value floor on a gas spike. | Sender should re-quote. Check sender logs for the next outgoing ticket — if the price is back in line, the issue self-corrected. |
| Receiver redemption queue depth grows unbounded | Redemption loop is wedged or chain RPC is slow. | Check `--chain-rpc-dial-timeout`, `--redemption-interval`. Look at the `pending_redemptions_total` metric over time. |
| Receiver "params expired" rejections from senders | Daemon's L1 clock is trailing the on-chain round. | `--clock-refresh-interval` (default 30s) may be set too high; also check `eth_blockNumber` latency on the RPC endpoint. |
| Daemon prints `DEV MODE — --chain-rpc is empty` in production logs | Operator forgot to supply `--chain-rpc`. | Set it. Production must not run in dev mode. |
| Sender returns `face_value capped by maxFloat` | Pending redemptions are eating into deposit faster than 3× heuristic allows. | Speed up redemption (lower `--redemption-interval`), or have payer top up deposit. |
| Broker terminated long-running session with `Livepeer-Error: insufficient_balance` | Payer's session balance hit zero before the session ended (plan 0015). Either the gateway sized the initial payment too small for the session length, or no mid-session top-up flow exists yet. | Have the gateway raise the initial `face_value` it asks the sender daemon for; or confirm the planned top-up flow is wired (currently a deferred follow-up plan). On Arbitrum One, look for the broker log line `terminating session work_id=… reason=insufficient_balance`. |
| `livepeer_payment_interim_debit_total{outcome="retried"}` rate > 0 sustained | Broker's interim-debit tick is failing on the daemon. Could be a daemon RPC error, a network partition, or BoltDB contention. | Check broker logs for the per-tick `interim DebitBalance work_id=… failed: …` warning. The broker reuses the same `debit_seq` across retries (plan 0015 §5.3) so the daemon's idempotency key prevents double-debit; sustained retries still indicate a real problem on the daemon side. |
| DebitBalance call rate exceeds expected cadence | Broker is retrying tick deltas and the daemon is observing duplicate debit_seq values without successful prior commits. | Check broker logs for `interim DebitBalance work_id=… failed` patterns; if the same work_id repeats with the same `debit_seq`, the daemon is rejecting the ticket (signature, sender mismatch, or session-already-closed). Race with `CloseSession` is the most common — increase the broker's tick-stop wait timeout. |

---

## 8. Observability

The daemon exposes a Prometheus `/metrics` HTTP endpoint when started
with `--metrics-listen=:9092` (or any other host:port). Empty means
metrics are off and the in-process recorder is a no-op (zero overhead).

Per-mode metrics surface (when implemented; v0.2 scaffolds the
recorder; plan 0016 wires real counters):

**Sender:**
- `livepeer_payment_session_started_total` (counter) — sessions opened.
- `livepeer_payment_ticket_created_total{outcome}` (counter) — tickets
  signed; outcome ∈ {success, validation_failed, escrow_empty}.
- `livepeer_payment_ticket_ev_wei` (histogram) — per-ticket EV.
- `livepeer_payment_sender_deposit_wei` (gauge) — last-observed deposit.
- `livepeer_payment_sender_reserve_wei` (gauge) — last-observed reserve.

**Receiver:**
- `livepeer_payment_session_opened_total` (counter)
- `livepeer_payment_processed_total{outcome}` (counter) — outcome ∈
  {accepted, rejected_signature, rejected_nonce, rejected_expired,
  rejected_price}.
- `livepeer_payment_balance_wei{sender}` (gauge) — per-sender balance.
- `livepeer_payment_pending_redemptions_total` (gauge) — current queue
  depth.
- `livepeer_payment_redemption_attempt_total{outcome}` (counter) —
  outcome ∈ {confirmed, failed_gas, failed_revert, expired}.
- `livepeer_payment_pending_face_value_wei` (gauge) — total face_value
  pending redemption.

**Broker (interim-debit cadence — plan 0015):**
- `livepeer_payment_interim_debit_total{outcome}` (counter) — interim
  DebitBalance call results from the broker's per-session ticker;
  outcome ∈ {success, retried, terminal_failure}. `retried` means the
  same `debit_seq` was reused after a non-success daemon reply (plan
  0015 §5.3 retry semantics). High `retried` rate is a leading
  indicator of daemon RPC distress.
- `livepeer_payment_session_terminated_total{reason}` (counter) —
  long-running sessions terminated by the broker; reason ∈
  {balance_insufficient, handler_complete, ctx_cancelled}.
  `balance_insufficient` rates trending up indicate gateway
  operators are sizing initial payments below their session length.

Cardinality is bounded by `--metrics-max-series-per-metric` (default
10000). New label combinations beyond the cap are dropped silently
(existing combinations continue to update); the daemon logs `metric
cardinality cap exceeded` once per metric on first hit.

### Logging

`--log-level` (`error|warn|info|debug`) and `--log-format` (`text|json`).
Production runs JSON to ship to Loki / Elastic; development defaults to
text. Every session start/close, every batch created, every redemption
attempt + result emits a structured event.

The daemon **never logs raw private keys**. The deterministic dev key
prints its derived ETH address at INFO level on dev-mode startup; the
key bytes themselves stay in memory.

---

## 9. Dev mode (no chain)

Run with `--mode=...` and a socket path; omit `--chain-rpc`:

```sh
./bin/livepeer-payment-daemon --mode receiver --socket /tmp/rx.sock
./bin/livepeer-payment-daemon --mode sender   --socket /tmp/tx.sock
```

In sender mode, every `CreatePayment(...)` request must carry
`ticket_params_base_url`, and sender mode queries
`POST /v1/payment/ticket-params` on that exact broker origin.
`CreatePayment` fetches canonical payee-issued `TicketParams` there
before signing each quote-free payment.

Dev mode prints a loud warning to stderr at startup:

```
livepeer-payment-daemon: DEV MODE — --chain-rpc is empty; using fake chain providers (redemptions will not hit any chain)
```

If you see that line in a production log, the operator forgot
`--chain-rpc`. Page someone.

For a deterministic sender identity (so the receiver can pre-seed fake
broker state), set `--dev-signing-key-hex` or
`LIVEPEER_DEV_SIGNING_KEY_HEX`. The raw key is never logged; the derived
address is logged once at startup.

`--dev-signing-key-hex` is rejected when `--chain-rpc` is set. You
cannot mix dev signing with real chain.

---

## 10. Production startup checklist (post-0016)

This section is forward-looking — v0.2 ships the docs, v0.16 lights up
the code.

1. **Network choice locked.** Mainnet (Arbitrum One, chain ID 42161) is
   the only deployment we deploy against. We do not stand up testnets;
   we mitigate risk with dust amounts on mainnet from the start.
2. **Hot wallet funded.** Hot signing wallet has enough ETH for
   redemption gas (~10 redemptions per day × 0.00005 ETH ≈ 0.0005 ETH
   minimum; size to your traffic).
3. **Cold orchestrator authorized.** `BondingManager.setSigningAddress`
   is called from the cold wallet to authorize the hot wallet.
   `--orch-address` is set to the cold address.
4. **Keystore mounted.** V3 JSON keystore at `--keystore-path`; password
   either via `--keystore-password-file` or `LIVEPEER_KEYSTORE_PASSWORD`
   env var. Both set is an error.
5. **Chain RPC reachable.** Test `eth_blockNumber`, `eth_gasPrice`,
   `eth_call` against the RPC endpoint before pointing the daemon at
   it. Latency under 1s; 99.9% uptime SLO.
6. **BoltDB on persistent storage.** `--store-path` mounted on a real
   disk, not tmpfs. Backups are operator-responsibility.
7. **Metrics scraping configured.** Prometheus pointed at the daemon's
   `--metrics-listen` port. Alerts wired to `pending_redemptions_total`
   above some queue-depth threshold.
8. **Sender knobs set.** Gateway-side `MaxEV`, `MaxTotalEV`, and
   `DepositMultiplier` set per the operator's risk tolerance.

A misconfigured production daemon that starts up clean and silent is
worse than one that fails fast. The startup sequence is deliberately
load-bearing — read the logs.

---

## 11. Session-control-plus-media operations

Cross-cutting deltas for the broker's `session-control-plus-media@v0`
mode. The mode driver itself ships with the broker; this section
captures the operator-facing knobs and failure modes.

### 11.1. Container-runtime prereq

Docker daemon must be running on the broker host with image registry
credentials configured for the operator's runner image. The broker's
`--container-runtime` flag defaults to `docker`; the alternative
`process` runtime is debug-only and bypasses the runtime entirely.

### 11.2. Image management

Operator pulls the runner image; the broker does not vendor it. Pin to
a digest in production. Rotation = push new image + update
`host-config.yaml`'s `capabilities[].backend.session_runner.image` +
SIGHUP the broker. The `--session-control-max-concurrent-sessions` cap
governs how many runners can run simultaneously per broker host.

### 11.3. WebRTC firewall

The pion media-plane binds UDP ports `--webrtc-udp-port-min` through
`--webrtc-udp-port-max` (default `40000-49999`). The full range must be
reachable from customer clients. STUN config for NAT traversal is
operator-provisioned via the customer-side player; TURN is not bundled
by the broker.

If `--webrtc-public-ip` is unset, pion auto-detects the host's
outbound IP. In multi-homed deployments (private + public NICs), pin
the flag explicitly to avoid advertising the wrong interface in ICE
candidates.

### 11.4. Resource sizing

Per-session sizing depends on the runner image. The reference vtuber
session-runner needs ~ 2 GiB RAM + 2 CPU per session; capacity formula
is `--session-control-max-concurrent-sessions x per-session sizing`.
Set `capabilities[].backend.session_runner.resources` to enforce per
session.

### 11.5. Common failure modes

- **Runner crashed.** Check the broker logs for `runner_crashed` /
  `runner_oom`. OOM means raising `resources.memory`; missing env or
  pull failure means registry credentials.
- **Control-WS keeps disconnecting.** Most often NAT or firewall on the
  customer side. Pong RTT vs `--session-control-heartbeat-interval`
  matters: a 10s ping with three missed pongs gives a 30s detection
  window. Reconnect-window default is 30s; raise both for high-latency
  customer paths.
- **SDP failure.** Verify `--webrtc-public-ip`; UDP range open;
  customer's STUN reachable. The broker emits `media.failed` on the
  control-WS when negotiation fails.
- **Session starvation.** `--session-control-max-concurrent-sessions`
  cap hit. The broker rejects new sessions at the dispatcher with
  `capacity_exhausted`; pre-existing sessions continue.
- **Backpressure drop.** The broker dropped a control-WS because the
  customer stopped reading; the WS close-frame reason is
  `backpressure_drop`. Customer-side bug; broker is healthy.

### 11.6. Observability

New broker-side metrics for the mode:

- `livepeer_mode_session_runner_subprocess_total{outcome}` — counter;
  outcome ∈ {started, exited_clean, crashed, oom_killed,
  watchdog_killed}.
- `livepeer_mode_session_control_ws_active{capability}` — gauge; per-
  capability count of currently-attached control-WS connections.
- `livepeer_mode_session_media_pc_state{state}` — counter; state
  transitions of the per-session pion PeerConnection.

Reconnect-window expiry shows up on the broker as a
`control_disconnect_window_expired` close cause and propagates to
`livepeer_payment_session_terminated_total` via the existing
interim-debit ticker.

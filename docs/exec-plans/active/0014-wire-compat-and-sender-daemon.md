# Plan 0014 — wire-compat envelope + sender-side payment-daemon

**Status:** active
**Opened:** 2026-05-06
**Owner:** harness
**Related:** plan 0005 (closed), `livepeer-cloud-spe/livepeer-modules-project/` (prior implementation reference)

## Problem

Two limitations of the v0.1 payment surface make plans 0015–0017 (interim
debit, chain integration, warm-key handling) impossible to land cleanly
without disrupting downstream consumers:

1. **The wire envelope is a placeholder.** v0.1 ships a four-field
   `Payment { capability_id, offering_id, expected_max_units, ticket }`
   message that has no relationship to the probabilistic-micropayment
   protocol the orchestrator network already uses. When chain integration
   lands (plan 0016), the wire format MUST change — and at that point the
   broker, conformance runner, and OpenAI-compat gateway will all need to
   rewrite their decoders / encoders. Doing it twice is wasted motion.

2. **Gateways still encode envelopes locally.** The OpenAI-compat gateway
   hand-rolls a TS protobuf encoder; the conformance runner mints
   envelopes via the generated Go bindings. Both bypass the daemon. Once
   the daemon owns a warm signing key (plan 0017), a gateway that knows
   how to sign tickets is itself a key-handling surface the operator
   doesn't want.

This plan addresses both. We adopt the go-livepeer-compatible wire format
for `Payment` and stand up a sender-side gRPC service in the existing
daemon binary so consumer apps stop hand-rolling envelopes. Cryptography
stays stubbed — face-value zero, deterministic dev signing, no chain RPC,
no redemption — because chain integration is plan 0016. But the wire
format, the daemon-app gRPC contract, and the architectural patterns are
all in their final shape.

The prior implementation at `livepeer-cloud-spe/livepeer-modules-project/`
is the design reference. It extracts go-livepeer's `pm/` package
unmodified, has a verified byte-for-byte wire-compat round-trip test, and
ships a working two-mode binary. We borrow the wire shape, the RPC
surface, and the architectural split; we do not borrow the chain
integration code (that's plan 0016).

## Scope

### In scope (v0.2)

**Spec — `livepeer-network-protocol/`:**

- Replace `proto/livepeer/payments/v1/payment.proto` with a wire-compat
  `types.proto` declaring `Payment`, `TicketParams`, `TicketSenderParams`,
  `TicketExpirationParams`, and `PriceInfo` with field numbers identical
  to go-livepeer's `net/lp_rpc.proto`.
- Add `proto/livepeer/payments/v1/payer_daemon.proto` with `PayerDaemon`
  service: `CreatePayment(face_value, recipient, capability, offering)`
  and `GetDepositInfo`.
- Migrate `proto/livepeer/payments/v1/payee_daemon.proto` to the richer
  surface: `GetQuote`, `GetTicketParams`, `OpenSession(work_id, capability,
  offering, price_per_work_unit_wei, work_unit)`, `ProcessPayment(payment_bytes,
  work_id)`, `DebitBalance(sender, work_id, work_units, debit_seq)`,
  `SufficientBalance`, `GetBalance`, `CloseSession(sender, work_id)`,
  `ListPendingRedemptions`, `GetRedemptionStatus`, `ListCapabilities`.
- New design doc `livepeer-network-protocol/docs/wire-compat.md` —
  pins the byte-for-byte invariant and describes the round-trip test
  that enforces it (the test itself ships under `payment-daemon/`).

**Component — `payment-daemon/`:**

- New `internal/types/` package: `Ticket`, `TicketBatch`, big-int helpers,
  the EIP-191 hash + signature helpers. Pure data, no providers.
- New `internal/service/sender/` package: `StartSession`,
  `CreateTicketBatch`, `ValidateTicketParams`, `CleanupSession`, `EV`,
  `Nonce`. Stubbed cryptography via a deterministic dev signing key (no
  chain). Mirrors the prior impl's structure so chain integration in 0016
  is a provider swap, not a rewrite.
- New `internal/service/escrow/` package — stubbed `MaxFloat`
  computation that always returns "infinity" until a real `Broker`
  provider lands. Architecture-equivalent surface.
- Receiver service migrated to the new RPC surface: per-session pricing,
  idempotent debits via `debit_seq`, `payment_bytes` parsing, capability
  catalog from `worker.yaml`.
- New `internal/providers/` package — interfaces only (`Broker`,
  `KeyStore`, `Clock`, `GasPrice`). v0.2 ships fakes that match the
  prior impl's dev-mode behavior; chain implementations land in plan
  0016 as a provider swap.
- `cmd/livepeer-payment-daemon`: `--mode=sender|receiver` flag. The two
  modes share boot, providers, signal handling, and BoltDB; they expose
  different gRPC services.
- Operator runbook `docs/operator-runbook.md` — gas economics, escrow /
  reserve, redemption queue, hot-wallet / cold-orchestrator split,
  ticket-params lifecycle, common failure modes, observability surface.
  Operator audience; ports the corresponding doc from the prior impl.

**Broker — `capability-broker/`:**

- Drop the v0.1 envelope decode + capability/offering string cross-check
  from payment middleware. The wire `Payment` no longer carries those
  strings; binding is at the daemon RPC boundary.
- Migrate to the new flow: `OpenSession(work_id, capability, offering,
  price_per_work_unit_wei, work_unit)` → handler runs → `ProcessPayment`
  validates the wire bytes → `DebitBalance(sender, work_id, work_units,
  debit_seq)` posts the actual unit count → `CloseSession`.
- Host-config gains `price_per_work_unit_wei` per capability.

**Conformance — `livepeer-network-protocol/conformance/`:**

- Add a `payer-daemon` (sender mode) sidecar to the compose stack.
- Drop the runner's local `envelope.SubstituteHeaders` Go encoder; call
  `PayerDaemon.CreatePayment` over the unix socket instead.
- Fixtures unchanged at the YAML level (they still use the
  `<runner-generated-payment-blob>` placeholder).

**Gateway — `openai-gateway/`:**

- Drop the hand-rolled TS protobuf encoder (`src/livepeer/payment.ts`).
- Add `@grpc/grpc-js` runtime dep; call `PayerDaemon.CreatePayment` over
  unix socket.
- Compose stack adds the `payer-daemon` sidecar.

### Out of scope (deferred)

- **Chain integration.** No Arbitrum, no go-ethereum, no real signing
  keys, no redemption submissions. The provider interfaces are in place
  (`Broker`, `Clock`, `GasPrice`); v0.2 ships fakes. Plan 0016 swaps in
  the real implementations behind those interfaces.
- **Real ticket validation.** v0.2 receivers accept any well-formed
  `Payment` bytes and credit zero-EV. The signature check, win-prob
  evaluation, nonce ledger, and redemption queue all stub. Plan 0016
  fills them in.
- **Interim-debit cadence on long-running modes.** The new RPC surface
  supports it (`DebitBalance` is idempotent by `debit_seq`, callable
  many times per session); plan 0015 wires the broker tickers.
- **Service-registry resolver.** Sender mode in the prior impl uses a
  resolver socket to turn a recipient ETH address into a worker URL.
  v0.2 takes the worker URL via `--ticket-params-base-url` only;
  resolver integration is its own plan downstream.
- **Real keystore.** v0.2 uses a deterministic dev-mode private key
  via `--dev-signing-key-hex`. V3 JSON keystore loading lands with
  chain integration.

## Operator-grade detail brought in (drawn from `livepeer-modules-project/`)

The prior implementation captures hard-won operator knowledge that is
NOT obvious from the wire format alone. We port the documentation now
so operators preparing for chain integration have time to absorb it.

| Topic | Source in prior impl | Lands in this plan |
|---|---|---|
| Probabilistic-micropayment economics (face_value, win_prob, EV, MaxEV, MaxTotalEV, DepositMultiplier) | `internal/service/sender/sender.go`, `docs/operations/running-the-daemon.md` | `payment-daemon/docs/operator-runbook.md` §Economics |
| Sender escrow / reserve / `maxFloat` semantics + 3:1 deposit-to-pending heuristic | `internal/service/escrow/escrow.go:30-125` | `operator-runbook.md` §Escrow |
| Redemption queue + gas pre-checks (face_value-covers-tx-cost, validity-window, sender-funds-cover-tx-cost) | `internal/service/settlement/settlement.go:33-165` | `operator-runbook.md` §Redemption |
| Gas-price multiplier (200% headroom on Arbitrum's per-block base-fee tick) | `--gas-price-multiplier-pct` flag in `run.go` | `operator-runbook.md` §Gas |
| Redemption-confirmations depth (4 blocks; reorg protection vs revenue-recognition latency) | `--redemption-confirmations` flag | `operator-runbook.md` §Redemption |
| Hot-wallet / cold-orchestrator split via `--orch-address` | `docs/operations/running-the-daemon.md:74-75` | `operator-runbook.md` §Identity |
| Ticket-params HMAC-derived rotation; nonce-replay window (600 per recipientRandHash) | `internal/service/receiver/receiver.go` | `operator-runbook.md` §Receiver state |
| Ticket-params expiration baked into the wire (CreationRound + CreationRoundBlockHash) | `types.proto:80-92`, `wire-compat.md` | `wire-compat.md` here |
| Wire-compat byte-for-byte round-trip test against go-livepeer's `pm/` | `internal/compat/testdata/payment-canonical.bin` + matching test | `wire-compat.md` here; test ships under `payment-daemon/internal/compat/` (currently zero-byte fixture; plan 0016 generates the real one) |
| Dev mode banner ("DEV MODE — chain-rpc is empty; redemptions will not hit any chain") | `docs/operations/running-the-daemon.md:32-38` | runbook + the daemon prints it on `--mode` startup when `--chain-rpc` is empty |
| Per-mode boot loud-warning (deterministic dev signing key logged at INFO; raw key never logged) | `docs/operations/running-the-daemon.md:40-41` | runbook + daemon behavior |

These all migrate into the **docs and config grammar** in this plan,
even though their behavior is stubbed. Doing the documentation now
means plan 0016 lands purely as code; plan 0017 lands purely as key
handling — neither plan re-litigates the operator surface.

## Commit cadence

1. **`feat(spec+docs): wire-compat plan + design docs (plan 0014 C1)`**
   - This file (`docs/exec-plans/active/0014-...`).
   - `livepeer-network-protocol/docs/wire-compat.md`.
   - `payment-daemon/docs/operator-runbook.md`.
   - No code yet.

2. **`feat(spec): migrate Payment proto to wire-compat shape (C2)`**
   - `proto/livepeer/payments/v1/types.proto` (new).
   - `proto/livepeer/payments/v1/payment.proto` (delete or repoint).
   - `proto/livepeer/payments/v1/payer_daemon.proto` (new).
   - `proto/livepeer/payments/v1/payee_daemon.proto` (refactor).
   - Generated bindings in `proto-go/`.
   - **Nothing else builds against the new bindings yet**; this commit
     is reviewable on its own.

3. **`feat(payment-daemon): types + sender service + --mode flag (C3)`**
   - `internal/types/` package.
   - `internal/service/sender/` package, stubbed signing.
   - `internal/service/escrow/` package, stub MaxFloat.
   - `internal/providers/` interfaces + dev-mode fakes.
   - `cmd/livepeer-payment-daemon`: `--mode=sender|receiver` flag.
   - Receiver still on the v0.1 surface for now; broker still uses v0.1
     envelope. Sender-only self-tests pass.

4. **`feat(payment-daemon): receiver migrated to new RPC surface (C4)`**
   - Receiver service rewritten against the new `PayeeDaemon` proto.
   - `worker.yaml` capability catalog loader.
   - `internal/repo/` BoltDB schema for the new session model.
   - In-process self-tests pass for both modes.
   - **Broker still on the v0.1 surface; conformance and gateway smoke
     are red between C4 and C5.** Acceptable because each is a single
     reviewable commit and they ship together.

5. **`feat(broker+conformance): migrate to new RPC surface (C5)`**
   - Broker payment middleware + host-config schema migrated.
   - Conformance compose adds the payer-daemon sidecar; runner calls
     `CreatePayment` over the unix socket.
   - Broker standalone smoke: 11/11 again.
   - Conformance compose: 11/11 again.

6. **`feat(openai-gateway): gRPC sender call; close plan 0014 (C6)`**
   - OpenAI gateway calls `PayerDaemon.CreatePayment` over unix socket.
   - Hand-rolled TS encoder removed; `@grpc/grpc-js` added.
   - Compose adds the payer-daemon sidecar.
   - Smoke: 10/10.
   - Plan 0014 file moves to `completed/`.
   - PLANS.md refreshed.

## Acceptance

- `make smoke` in `capability-broker/` passes 11/11.
- `make test-compose` in `livepeer-network-protocol/conformance/` passes
  11/11 against real receiver + sender daemons.
- `make smoke` in `openai-gateway/` passes 10/10 against real receiver +
  sender daemons.
- `make test` in `payment-daemon/` passes for both `sender` and `receiver`
  service packages (in-process gRPC self-tests).
- The wire `Payment` produced by the sender daemon and consumed by the
  receiver daemon is structurally byte-compatible with go-livepeer's
  `net.Payment` — verified by a round-trip test in
  `payment-daemon/internal/compat/`. v0.2 ships the test scaffold; the
  canonical fixture against go-livepeer is plan 0016 (it requires a
  go-livepeer build, which is out of scope here).

## Tech debt opened by this plan

- Stubbed cryptography in sender. `internal/service/sender/sender.go`
  signs with a deterministic dev key; nonce monotonicity is enforced but
  signatures are valid only against the same dev key. Plan 0016 swaps
  the `KeyStore` provider.
- Stubbed escrow. `MaxFloat` returns infinity. Plan 0016 implements real
  on-chain queries.
- Stubbed redemption. Receiver queues winners but never submits. Plan
  0016 wires the redemption loop.
- Wire-compat fixture is a zero-byte placeholder. Plan 0016 generates
  the canonical fixture against go-livepeer.
- Resolver integration deferred. Sender mode requires
  `--ticket-params-base-url`; the `--resolver-socket` path is unwired.
- Real keystore (V3 JSON) loading deferred. Sender uses
  `--dev-signing-key-hex`.
- The conformance runner's TS-side TS gRPC client and the OpenAI
  gateway's TS-side gRPC client both pull in `@grpc/grpc-js` as a
  runtime dep — the openai-gateway is no longer zero-runtime-deps. The
  zero-deps property has served us well, but the daemon is the right
  owner for envelope encoding once a warm key is involved, so the trade
  is correct.

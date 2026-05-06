---
plan: 0016
title: Chain-integrated payment-daemon — design choices
status: design-doc
phase: plan-only
opened: 2026-05-06
owner: harness
related:
  - "completed plan 0014 — wire-compat envelope + sender daemon"
  - "payment-daemon/docs/operator-runbook.md (operator-grade flow plan 0016 implements)"
  - "livepeer-cloud-spe/livepeer-modules-project/payment-daemon (prior reference impl)"
---

# Plan 0016 — chain-integrated payment-daemon — design choices

> **Scope of this document.** Design choices for landing chain
> integration behind the `Broker` / `KeyStore` / `Clock` / `GasPrice`
> provider interfaces declared in plan 0014. **No code is committed
> by this document. No `go.mod` edit. No provider implementation.**
> The output is a list of pinned decisions plus a list of open
> questions the user must answer before implementation begins.
>
> The operator surface is already pinned in
> `payment-daemon/docs/operator-runbook.md` (plan 0014, completed).
> Plan 0016 lands the **code** that runbook describes; it does not
> redesign anything that runbook has settled.

---

## 1. What's already settled (do not relitigate)

Plan 0014 pinned the architectural contract that plan 0016 lands
behind. The following are **not open questions**:

| Settled by | What's pinned | Reference |
|---|---|---|
| Plan 0014 | Provider interface signatures (`Broker`, `KeyStore`, `Clock`, `GasPrice`) | `payment-daemon/internal/providers/providers.go:1-87` |
| Plan 0014 | `--mode=sender|receiver` flag, two-mode binary, gRPC services | `payment-daemon/cmd/livepeer-payment-daemon/...` (in tree) |
| Plan 0014 | Wire `Payment` shape (5 wire-locked messages, byte-compat with go-livepeer's `net.Payment`) | `livepeer-network-protocol/proto/livepeer/payments/v1/types.proto:1-135` |
| Plan 0014 | Operator surface: `--chain-rpc`, `--keystore-path`, `--orch-address`, `--gas-price-multiplier-pct`, `--redemption-confirmations`, `--redeem-gas`, `--receiver-ev`, `--validity-window`, `--clock-refresh-interval`, `MaxEV` / `MaxTotalEV` / `DepositMultiplier` | `payment-daemon/docs/operator-runbook.md:96-114, 224-237, 240-263` |
| Plan 0014 | Dev-mode banner contract: `DEV MODE — --chain-rpc is empty; using fake chain providers` and `--dev-signing-key-hex` rejected when `--chain-rpc` set | `payment-daemon/docs/operator-runbook.md:374-398` |
| Plan 0014 | `Broker.RedeemWinningTicket(ctx, ticketHash, sig, recipientRand)` signature; `KeyStore.Sign` returns 65-byte `[R||S||V]`, V ∈ {27,28} over `accounts.TextHash` | `payment-daemon/internal/providers/providers.go:38-58` |
| Plan 0014 | BoltDB persistence (`go.etcd.io/bbolt v1.4.3`), session ledger schema | `payment-daemon/internal/store/store.go:1-86`; `payment-daemon/go.mod:12` |
| Plan 0014 | Wire-compat round-trip test scaffold (zero-byte fixture pending plan 0016) | `payment-daemon/internal/compat/` (placeholder); prior fixture: `livepeer-modules-project/payment-daemon/internal/compat/wire_test.go:28-128` |
| User memory | **Mainnet only — no Livepeer testnets.** Arbitrum One (chain ID 42161) from day 1; mitigate risk with dust amounts, not testnets. | (memory `feedback_no_livepeer_testnets`) |
| User memory | Don't use the term "BYOC" or "bridge" anywhere | (memory) |

---

## 2. Library choices

### 2.1 go-ethereum — RPC, ECDSA, contract bindings

**Recommendation:** depend on `github.com/ethereum/go-ethereum` at the latest
stable v1.x (currently `v1.14.x` line; pin to whatever stable tag is
released closest to the start of plan 0016 work).

Used for:

- `accounts/keystore` — V3 JSON keystore decryption
  (prior impl: `livepeer-modules-project/payment-daemon/internal/providers/keystore/jsonfile/jsonfile.go:14-42`).
- `accounts/abi` — TicketBroker ABI parsing + pack/unpack
  (prior impl: `livepeer-modules-project/payment-daemon/internal/providers/broker/ticketbroker/abi.go:1-88`).
- `crypto` — `Keccak256`, `Sign`, `Ecrecover`, `PubkeyToAddress`.
- `accounts.TextHash` — EIP-191 `personal_sign` digest wrapper
  (prior impl: `.../keystore/inmemory/inmemory.go:50-74`).
- `common` — `Address`, `Hash`, `LeftPadBytes`.
- `core/types` — `Transaction` (used as the synthetic CheckTx handle in
  the prior impl; see §6.3 below for whether we keep that pattern).
- `ethclient` + `rpc` — JSON-RPC dial, `eth_call`, `eth_sendRawTransaction`,
  `eth_getTransactionReceipt`, `eth_blockNumber`, `eth_gasPrice`,
  `eth_chainId`, `eth_getBalance`.

**Alternatives rejected:** hand-rolled ABI codec (footgun-prone for
nested tuples; we'd pay the go-ethereum cost for keystore anyway);
third-party RPC libs (none stable enough to bet payment correctness
on). go-ethereum is heavy (~50 transitive deps) but the daemon ships
in Docker per the repo constraint, so dep weight is irrelevant at use
time, and the prior impl pays this cost in production already.

### 2.2 Wire-compat: generated bindings stay; we don't import go-livepeer's `net`

**Recommendation:** keep using
`livepeer-network-protocol/proto-go/livepeer/payments/v1` — our
generated bindings — for the daemon's wire path. **Do not** depend on
`github.com/livepeer/go-livepeer/net` from the daemon binary.

Rationale: the wire-compat invariant is enforced by the round-trip
fixture test (§9 below) — if our generated bindings produce identical
bytes to go-livepeer's, that's the contract. Importing go-livepeer
into the daemon would pull a much larger transitive graph (libp2p,
ipfs-core, ffmpeg cgo bindings) and would conflate "the protocol we
talk on the wire" with "the project that defined the protocol".

Where go-livepeer's `net` IS imported: only the **fixturegen tool**
in §9, which is a separate Go module so it doesn't pollute the daemon
binary's deps.

### 2.3 BoltDB — confirmed

`go.etcd.io/bbolt v1.4.3` is already in `payment-daemon/go.mod:11`.
Plan 0016 keeps it; adds new buckets for the redemption queue (§5.1).
Alternatives (SQLite-no-cgo; separate file per repo) rejected:
bbolt's bucket model with cross-bucket atomicity is exactly what the
redemption queue needs (prior impl's four-bucket atomic enqueue at
`livepeer-modules-project/payment-daemon/internal/repo/redemptions/redemptions.go:91-132`).

### 2.4 Other direct deps

Only `github.com/ethereum/go-ethereum` (latest stable v1.14.x) and
`golang.org/x/sync/errgroup` (refresh-loop goroutine lifecycles) need
to land net-new. The prior impl extracted a separate `chain-commons`
Go module; we **deliberately do not adopt that** — the v0.2 provider
interfaces are right-sized, and pulling in a sibling extracted module
would add a second maintenance surface for marginal code reuse. (Q1
below if the user wants this revisited.)

---

## 3. Arbitrum One topology

### 3.1 Contract addresses

The Livepeer protocol on Arbitrum One uses a **Controller-resolver**
pattern: a single Controller contract stores the address of every
other named contract. We resolve TicketBroker / RoundsManager /
BondingManager from the Controller at boot, fail loudly on
chain-id mismatch, and cache addresses for the lifetime of the
process.

| Contract | Address | Source |
|---|---|---|
| **Controller** | `0xD8E8328501E9645d16Cf49539efC04f734606ee4` | `payment-daemon/docs/operator-runbook.md:401-414`; prior impl flag default `livepeer-modules-project/payment-daemon/cmd/livepeer-payment-daemon/run.go:70` |
| TicketBroker | resolved via Controller | – |
| RoundsManager | resolved via Controller | – |
| BondingManager | resolved via Controller | – |

**Override flags** (mirrors prior impl `run.go:71-73`):

- `--chain-controller-address` (default = Arbitrum One)
- `--ticketbroker-address` (empty = resolve via controller)
- `--rounds-manager-address` (empty = resolve via controller)
- `--bonding-manager-address` (empty = resolve via controller)

### 3.2 Chain-ID guard

**Recommendation:** boot-time `eth_chainId` call; fail with non-zero
exit code if the result doesn't match `--expected-chain-id`. Default
`42161` (Arbitrum One). Setting `--expected-chain-id 0` disables the
check (escape hatch for forks / local Anvil — but per the user's
"mainnet only" rule, **production deployments MUST keep the default**).

Prior impl: `run.go:76`, `expectedChainID` flag.

### 3.3 Recommended defaults (already pinned in runbook)

These are pinned by `payment-daemon/docs/operator-runbook.md` and
plan 0016 implements them as flag defaults:

- `--redemption-confirmations 4` — Arbitrum Nitro reorgs past ~3
  blocks are rare; 4 is the runbook-pinned default
  (`operator-runbook.md:204`).
- `--gas-price-multiplier-pct 200` — 2× headroom over `eth_gasPrice`
  to absorb a base-fee tick between read and submit
  (`operator-runbook.md:223-237`).
- `--redeem-gas 500000` — empirical Arbitrum L2 cost for
  `redeemWinningTicket`, matches go-livepeer's prod default
  (`operator-runbook.md:103`; prior impl `run.go:81`).
- `--validity-window 2` — drop tickets whose `CreationRound` is more
  than 2 rounds behind `LastInitializedRound`
  (`operator-runbook.md:303-306`; prior impl
  `internal/service/settlement/settlement.go:54-57`).
- `--clock-refresh-interval 30s` — round + L1-block poll
  (`operator-runbook.md:319`; prior impl `run.go:79`).
- `--gasprice-refresh-interval 5s` — gas-price poll cadence (prior
  impl `run.go:80`).

### 3.4 RPC provider expectations

**Required RPC methods:**

| Method | Used by | Frequency |
|---|---|---|
| `eth_chainId` | boot guard | once |
| `eth_blockNumber` | preflight latency check; clock | one per refresh interval |
| `eth_call` | TicketBroker reads (`getSenderInfo`, `claimedReserve`, `usedTickets`); Controller resolution; RoundsManager (`blockHashForRound`, `blockNum`); BondingManager (`getTranscoderPoolSize`) | several per session-open + ticket creation |
| `eth_gasPrice` | gas provider | one per `--gasprice-refresh-interval` |
| `eth_getBalance` | preflight (signing-wallet ETH balance gate) + uptime gauge | once at boot, optional periodic |
| `eth_sendRawTransaction` | redemption submit (receiver only) | one per winning ticket |
| `eth_getTransactionReceipt` | redemption confirm wait (receiver only) | polled per submitted tx until receipt + N confirmations |
| `eth_estimateGas` | optional; we ship `--redeem-gas` as a static cap | rare |

**Latency / availability SLO** (per runbook §10):

- < 1s p99 latency on `eth_call`
- 99.9% uptime
- WSS optional; we do not require subscriptions (see Q5 in §12)

Sender mode issues read-only RPC (`eth_call` only); receiver mode
also issues `eth_sendRawTransaction` + `eth_getTransactionReceipt`.
Per `operator-runbook.md:42`, the sender deployment can therefore use
a read-only endpoint. See §9 for full topology.

---

## 4. Ticket hashing + signing

### 4.1 What changes vs v0.2 stub

`payment-daemon/internal/service/sender/sender.go:271-292` ships a
stub `ticketHash` that's `sha256(...)` of concatenated fields. This
is **not** what go-livepeer expects. Plan 0016 swaps it for
keccak256 over the contract-defined flatten layout.

### 4.2 Ticket hash — exact layout

```
flatten = recipient(20)
       || sender(20)
       || LeftPadBytes(faceValue, 32)       // big-endian uint256
       || LeftPadBytes(winProb, 32)         // big-endian uint256
       || LeftPadBytes(senderNonce, 32)     // uint32 → uint256-padded
       || recipientRandHash(32)
       || auxData                           // 0 or 64 bytes
auxData = (CreationRound > 0 || any byte set in CreationRoundBlockHash) ?
            LeftPadBytes(CreationRound, 32) || CreationRoundBlockHash(32)
          : []byte{}
hash    = keccak256(flatten)
```

This layout is **contract-defined** (TicketBroker
`MixinTicketProcessor.sol`). Reference: prior impl
`livepeer-modules-project/payment-daemon/internal/types/ticket.go:113-162`
— wholesale-portable, only the package path needs renaming.

### 4.3 Signing — EIP-191 personal_sign

Per `payment-daemon/internal/providers/providers.go:50-59` the
contract is fixed: `KeyStore.Sign(hash) returns 65-byte [R||S||V]`
where the input is wrapped in `accounts.TextHash` (EIP-191) before
ECDSA, and `V ∈ {27, 28}`.

go-ethereum gives us all of this:

- `accounts.TextHash(ticketHash)` — prepends
  `\x19Ethereum Signed Message:\n32` and re-keccaks.
- `crypto.Sign(digest, key)` — produces `[R||S||V]` with `V ∈ {0,1}`.
- `sig[64] += 27` — bumps to canonical Ethereum form.

Reference impl: `livepeer-modules-project/payment-daemon/internal/providers/keystore/inmemory/inmemory.go:50-74`
— wholesale-portable; the `Address ethcommon.Address` return shape
needs adapting because plan 0014's `KeyStore.Address() []byte`
already canonicalized to `[]byte` (see
`payment-daemon/internal/providers/providers.go:53`).

### 4.4 V3 keystore loading

`--keystore-path` points at a V3 JSON file (geth `account new` output).
Password supply has **two mutually-exclusive modes** (per
`operator-runbook.md:415-417`):

- `--keystore-password-file <path>` — reads a password from a file
  (recommended for ops; can be a Docker secret mount).
- `LIVEPEER_KEYSTORE_PASSWORD` env var.

**Both set is a hard error.** Prior impl:
`livepeer-modules-project/payment-daemon/cmd/livepeer-payment-daemon/password.go`
+ `password_test.go` — wholesale-portable.

The decoded key is held in memory by the `inmemory.KeyStore`. The
`String()` method redacts the key bytes; raw key never logged. The
derived address is logged once at INFO at startup
(`operator-runbook.md:367-369`).

### 4.5 Dev-signing-key rejection

`--dev-signing-key-hex` is rejected when `--chain-rpc` is set
(plan 0014, `operator-runbook.md:396-397`). Plan 0016 enforces this
in the same flag-validation pass as the keystore-password
double-supply check. Prior impl already does this in
`run.go::productionConfig.validate`.

---

## 5. Redemption loop wiring

### 5.1 Persistence schema (BoltDB redemption queue)

**Recommendation:** port the prior impl's four-bucket layout
verbatim, renaming the prefix from
`payment_daemon_redemptions_*` (which mirrored a different module
namespace) to a stable name.

Buckets (keys on disk; same atomicity story as the prior impl):

| Bucket | Key | Value | Purpose |
|---|---|---|---|
| `redemptions_pending` | `<seq u64 BE>` | JSON `{ticket, sig, recipient_rand}` | FIFO queue of winners |
| `redemptions_by_hash` | `<ticketHash 32B>` | `<seq u64 BE>` | dedup + pending lookup |
| `redemptions_redeemed` | `<ticketHash 32B>` | `<txHash 32B>` (zero = locally drained) | history; prevents re-enqueue |
| `redemptions_meta` | `next_seq` | `<u64 BE>` | monotonic ID counter |

Reference: prior impl
`livepeer-modules-project/payment-daemon/internal/repo/redemptions/redemptions.go:33-237`
+ `livepeer-modules-project/payment-daemon/docs/design-docs/persistence-schema.md:107-154`.
**Wholesale-portable**; only the bucket-name constants change.

### 5.2 Gas pre-checks

Before each `eth_sendRawTransaction`, run the same three checks the
prior impl does
(`livepeer-modules-project/payment-daemon/internal/service/settlement/settlement.go:217-274`):

1. **`ErrTicketExpired`** — `CreationRound < LastInitializedRound -
   ValidityWindow`. Drop the ticket via `MarkRedeemed(zeroTxHash)`;
   it will revert on-chain anyway with `creationRound does not have a
   block hash`.
2. **`ErrFaceValueTooLow`** — `faceValue ≤ redeemGas × gasPrice`.
   Drop. Submitting would lose money to gas.
3. **`ErrInsufficientFunds`** — `availableFunds(sender) ≤ redeemGas
   × gasPrice`. Leave queued; retry next tick (the sender may top
   up).

A fourth check is "is the ticket already on-chain?" — call
`TicketBroker.usedTickets(ticketHash)` (prior impl
`broker/ticketbroker/ticketbroker.go:257-285`). If true, drain
locally via `MarkRedeemed`.

### 5.3 Tx submission

**Recommendation: keep the redemption-tx submission simple.** Use
go-ethereum's `bind.TransactOpts` (or hand-roll a signer + raw-tx
build) targeting `TicketBroker.redeemWinningTicket(ticket, sig,
recipientRand)`.

The prior impl wraps tx submission in a separate `chain-commons.txintent`
manager that owns nonce assignment, gas estimation, and confirmation
tracking. **Recommendation: do NOT port that.** Reasoning:

- It's 5+ packages of abstraction (`txintent`, `gasoracle`, `nonces`,
  `receipts`, store buckets) and we don't need the full
  multi-tx-pipelined model the prior impl architected for.
- Plan 0016's redemption loop is single-threaded (one tx per
  `--redemption-interval` tick) and only one wallet — go-ethereum's
  native `bind.TransactOpts` + an in-process nonce counter is
  sufficient.
- We can revisit if redemption volume grows (tracked as future
  tech-debt; not blocking).

Submission flow per loop tick: pop oldest pending winner →
pre-checks (§5.2; terminal-failure ⇒ `MarkRedeemed` and continue) →
ABI-pack `redeemWinningTicket(ticket, sig, recipientRand)` → sign tx
(go-ethereum `types.SignTx` with chainID) → `eth_sendRawTransaction`
→ poll `eth_getTransactionReceipt` until non-nil → wait
`receipt.block + --redemption-confirmations ≤ head` →
`MarkRedeemed(ticketHash, txHash)` → update gauges.

Note: tx signing is a different signing path from §4.3 (EIP-191
ticket signing). The chain-backed key needs **both**. See Q4.

### 5.4 Confirmation depth and reorg handling

Wait `--redemption-confirmations` (default 4) blocks past the receipt
block. If the receipt disappears mid-wait (extremely rare on Arbitrum
Nitro past 1-2 blocks; theoretically possible during a sequencer
soft-fork), treat as a transient failure: leave the ticket in the
pending queue and retry next tick.

The prior impl
(`livepeer-modules-project/payment-daemon/internal/providers/broker/ticketbroker/ticketbroker.go:170-172,
394-418`) defines `ErrReorged` for this case; we keep the sentinel
even if our simpler submission flow doesn't observe it as often.

### 5.5 Failure / retry semantics

| Failure | Action | Sentinel |
|---|---|---|
| Pre-check `ErrTicketExpired` | drain locally; never retry | `ErrTicketExpired` |
| Pre-check `ErrFaceValueTooLow` | drain locally; never retry | `ErrFaceValueTooLow` |
| Pre-check `ErrInsufficientFunds` | leave queued; retry next tick | `ErrInsufficientFunds` |
| `usedTickets() == true` | drain locally; never retry | `ErrTicketUsed` |
| Tx revert (any reason) | log + drain locally; if revert msg contains "creationRound does not have a block hash", classify as expired | (wraps `ErrTxFailed`) |
| RPC error / timeout | leave queued; retry next tick | wrapped error |
| Reorg out of receipt | leave queued; retry next tick | `ErrReorged` |

`IsNonRetryable(err)` (prior impl `settlement.go:376-395`) is the
canonical classifier — port verbatim.

---

## 6. Receiver-side validation (replacing v0.2 stub)

`payment-daemon/internal/service/receiver/receiver.go:99-147`
currently accepts any well-formed `Payment` and credits zero EV. Plan
0016 fills this in with the full pipeline.

### 6.1 Signature recovery

For each `TicketSenderParams` in the Payment:

1. Reconstruct the in-process `Ticket` from `(TicketParams, Sender,
   ExpirationParams, ticketSenderParam.SenderNonce)`.
2. Compute `ticket.Hash()` (the keccak256-flatten from §4.2).
3. Compute the EIP-191 digest = `accounts.TextHash(hash)`.
4. `crypto.Ecrecover(digest, sig)` → public key → ETH address.
5. Compare recovered address to `Payment.Sender` — mismatch is
   `ErrInvalidSignature` (rejected per ticket; non-fatal for the
   session).

Reference: prior impl
`livepeer-modules-project/payment-daemon/internal/service/receiver/validator/validator.go:69-82`
+ `internal/providers/sigverifier/ecdsa/ecdsa.go`
— wholesale-portable; package path renames only.

### 6.2 Win-probability check

```
winningHash = keccak256(sig || LeftPadBytes(recipientRand, 32))   as uint256
winning     = winningHash < ticket.WinProb
```

Reference: prior impl `internal/types/crypto.go:24-34` + the
validator's `IsWinningTicket`
(`internal/service/receiver/validator/validator.go:87-89`) —
wholesale-portable.

### 6.3 Nonce ledger

Per `recipientRand` (NOT `recipientRandHash`; the rand is the
preimage the receiver knows because it generated the seed), track up
to **600 nonces** (`MaxSenderNonces` in prior impl
`internal/service/receiver/receiver.go:43-44`). Replays return
`ErrNonceAlreadySeen`. Beyond 600, return `ErrTooManyNonces` (sender
should re-quote with a fresh `recipientRandHash`).

Storage: a new `payment_daemon_nonces` BoltDB bucket. Schema (per
prior impl `docs/design-docs/persistence-schema.md:39-55`):

```
key   = hex(recipientRand) || 0x00 || senderNonce(u32 BE)
value = 0x01    (presence marker)
```

Reference: prior impl
`livepeer-modules-project/payment-daemon/internal/repo/nonces/nonces.go`
— wholesale-portable, only constants rename.

### 6.4 EV credit

For each accepted ticket, credit:

```
EV(faceValue, winProb) = faceValue × winProb / 2^256
```

This is rational (`*big.Rat`); the integer floor goes into the
session balance. Helpers already exist in
`payment-daemon/internal/types/types.go:88-96`. The store-credit call
is unchanged
(`payment-daemon/internal/service/receiver/receiver.go:128-131`); we
just stop passing `big.NewInt(0)` and pass the real EV.

### 6.5 Redemption queueing for winners

When `IsWinningTicket(ticket, sig, recipientRand)` returns true,
enqueue a `SignedTicket{ticket, sig, recipientRand}` into the
redemptions repo (§5.1). Reference: prior impl
`internal/service/receiver/receiver.go:295-298, 332-339`
— wholesale-portable.

### 6.6 ProcessPayment response

`pb.ProcessPaymentResponse.WinnersQueued` (currently hard-coded `0` in
`receiver.go:145`) becomes the count of winners actually enqueued in
this call.

---

## 7. Sender-side validation (escrow + reserve)

### 7.1 MaxFloat

Replace `payment-daemon/internal/service/escrow/escrow.go:1-39`'s
"infinity" stub with the formula:

```
reserveAlloc = (reserve.totalFunds / poolSize) - claimedByMe
if pendingAmount == 0:
  maxFloat = reserveAlloc + deposit
else if (deposit / pendingAmount) ≥ 3.0:        # 3:1 heuristic
  maxFloat = reserveAlloc + deposit              # ignore pending
else:
  maxFloat = reserveAlloc + deposit - pendingAmount
```

Reference: `payment-daemon/docs/operator-runbook.md:128-146` +
prior impl `internal/service/escrow/escrow.go:30-125` —
**wholesale-portable**; the only change is `Broker.GetSenderInfo`
already returns the right shape per the v0.2 interface
(`payment-daemon/internal/providers/providers.go:18-30`). The 3:1
constant stays in `MinDepositPendingRatio`.

### 7.2 Sender validation rejection cases

`payment-daemon/internal/service/sender/sender.go:225-239` already
implements the three rejection cases (no deposit / no reserve /
pending unlock imminent) against the v0.2 fake; against the real
broker, the same code path fires. Plan 0016 doesn't change the
rejection logic — it just connects it to a real `Broker`
implementation.

### 7.3 `pendingAmount` tracking

Per the prior impl
(`internal/service/escrow/escrow.go:75-77, 143-161`), pending lives
in **escrow**, in memory, keyed by sender address. Settlement calls
`SubFloat` (reserve pending for an in-flight redemption) and
`AddFloat` (release on terminal outcome).

On receiver restart, pending is **rebuilt** from the redemptions
queue: scan `redemptions_pending`, sum face values per sender, seed
`escrow.pending`. Reference: prior impl
`livepeer-modules-project/payment-daemon/internal/service/escrow/rebuild.go`
+ `cmd/.../run.go:319-329`
— wholesale-portable.

### 7.4 MaxEV / MaxTotalEV / DepositMultiplier

Sender-side defense, configured per gateway operator
(`operator-runbook.md:104-112`):

- `MaxEV` (wei) — refuse to sign a ticket where `EV > MaxEV`.
- `MaxTotalEV` (wei) — refuse to sign a batch whose total EV
  exceeds this.
- `DepositMultiplier` — refuse where `faceValue × multiplier >
  deposit`.

These plumb through `cmd/livepeer-payment-daemon` flags (or a small
sender YAML). Reference: prior impl handles via sender Config in
`livepeer-modules-project/payment-daemon/internal/service/sender/sender.go`
(needs adaptation — the prior impl pre-dates the wire-compat
refactor). **Adapt, don't port wholesale.**

---

## 8. Wire-compat fixture generation

### 8.1 Fixturegen tool

A small Go program that imports
`github.com/livepeer/go-livepeer/{net,pm}`, builds a fully-populated
canonical `Payment`, and writes `payment-canonical.bin`. Lives at
`payment-daemon/internal/compat/fixturegen/` as a **separate Go
module** (its own `go.mod`) so the daemon binary doesn't pick up
go-livepeer in its dep graph.

Reference: prior impl
`livepeer-modules-project/payment-daemon/internal/compat/fixturegen/main.go:1-115`
+ `go.mod:1-23` — **wholesale-portable**, only the import paths to
our v1 generated types change.

The prior impl uses a `replace` directive against a local
go-livepeer checkout (`fixturegen/go.mod:9 — replace
github.com/livepeer/go-livepeer => ../../../../../go-livepeer-mikez`).
**Recommendation: pin to a tagged go-livepeer release** instead, so
the fixture is reproducible. The user picks the tag (Q3 below).

### 8.2 Fixture file path + format

```
payment-daemon/internal/compat/testdata/payment-canonical.bin
```

Binary protobuf-marshaled `net.Payment`. Re-runnable via:

```
cd payment-daemon/internal/compat/fixturegen
go run .
```

Round-trip test (already scaffolded by plan 0014 with a zero-byte
fixture):

1. Read `payment-canonical.bin`.
2. `proto.Unmarshal` into our `paymentsv1.Payment`.
3. `proto.Marshal` it back.
4. `bytes.Equal(got, want)` — assert byte-identity.
5. Spot-check field values (sender prefix `0xa0`, etc.) — see
   prior impl
   `livepeer-modules-project/payment-daemon/internal/compat/wire_test.go:52-128`.

### 8.3 CI hook for drift detection

**Recommendation:** a nightly GitHub Actions job that:

1. Checks out `livepeer/go-livepeer@main` (or the pinned tag).
2. Runs the fixturegen.
3. Diffs against the committed `payment-canonical.bin`.
4. Opens an issue (or fails the matrix) on diff.

This is the canary that tells us go-livepeer changed `net.Payment`'s
wire shape upstream. The daemon CI **always runs the round-trip
test**; the nightly is the "is upstream still byte-equal to our
fixture" canary.

(Per `livepeer-network-protocol/docs/wire-compat.md:99-120`, this is
the §"How compat is enforced" approach, lined up with our v0.2
scaffold.)

---

## 9. Deployment topology

| Aspect | Sender mode | Receiver mode |
|---|---|---|
| RPC type | Read-only sufficient | Write-capable |
| Methods used | `eth_chainId`, `eth_call` (TicketBroker.getSenderInfo), optional `eth_blockNumber` | All of §3.4 |
| Tx submission | None | Yes (`redeemWinningTicket`) |
| Wallet | Hot signing key only — no ETH balance needed | Hot signing key + ETH for gas (`operator-runbook.md:402-409`) |

**Offline sender (`--no-chain-sender`).** Recommendation: do **not**
ship. The sender's `validateSenderInfo` is the very protection
documented in `operator-runbook.md:148-160`; bypassing it lets a
gateway mint tickets against unverified escrow. (Q7 if the user
disagrees.)

**Hot/cold split (settled by `operator-runbook.md:240-263`).** Hot
wallet = V3 keystore on the daemon machine; cold orchestrator =
registered on `BondingManager`, address passed via `--orch-address`.
Authorization is one-time out-of-band: cold wallet calls
`BondingManager.setSigningAddress(hotWalletAddress)`. Plan 0016 does
NOT manage this — it just reads `--orch-address` and stamps it as
the ticket recipient.

**Sender-funder.** Topping up `TicketBroker.fundDeposit` /
`fundReserve` stays out-of-band for plan 0016 (one-time setup; use
Foundry / cast). Q6 if the user wants a CLI tool.

---

## 10. Migration sequence — recommended commit cadence

Plan 0016 is significantly larger than plan 0014; recommend 8–10
commits that each ship green-tested, even if not yet wired into the
final binary. Order chosen so each commit closes a tech-debt item
from plan 0014 and reviewers don't have to context-switch.

| # | Commit | Lands |
|---|---|---|
| 1 | `feat(payment-daemon/docs): plan 0016 design doc (this file)` | Just this doc |
| 2 | `feat(payment-daemon): real ticket hash + EIP-191 signing (replaces v0.2 stub)` | `internal/types/ticket.go` keccak256-flatten; `internal/types/crypto.go` (HashRecipientRand, WinningHash); `KeyStore.Sign` real impl in a new `inmemory` provider; **bumped go.mod** with go-ethereum |
| 3 | `feat(payment-daemon/providers/keystore): V3 JSON keystore loader + password supply` | `keystore/jsonfile/` package; password-file + env-var helpers; mutual-exclusion check; dev-key rejection vs `--chain-rpc` |
| 4 | `feat(payment-daemon/providers/broker): TicketBroker chain provider` | `broker/ticketbroker/` (ABI subset + `getSenderInfo`/`claimedReserve`/`usedTickets`/`redeemWinningTicket`); Controller resolution; chain-id guard; `eth_call` wiring |
| 5 | `feat(payment-daemon/providers/clock): RoundsManager + BondingManager-backed clock` | `clock/onchain/` package; round-poll goroutine; `blockHashForRound` cache |
| 6 | `feat(payment-daemon/providers/gasprice): eth_gasPrice + multiplier` | `gasprice/onchain/` package; refresh goroutine; multiplier validator |
| 7 | `feat(payment-daemon/receiver): real validation pipeline (sig recovery, win-prob, nonce ledger)` | `internal/service/receiver/validator/`; nonces BoltDB bucket; EV credit replaces stub |
| 8 | `feat(payment-daemon/escrow): real MaxFloat + 3:1 heuristic + pending tracking` | `internal/service/escrow/escrow.go` rewrite |
| 9 | `feat(payment-daemon/settlement): redemption loop + queue + gas pre-checks` | `internal/service/settlement/`; redemptions BoltDB buckets; tx submit + confirm wait |
| 10 | `feat(payment-daemon/cmd): wire real providers behind --chain-rpc; preflight; close plan 0016` | `cmd/livepeer-payment-daemon/` flag wiring; preflight balance check; startup banner; smoke tests against Arbitrum One mainnet (dust amounts) |

Smoke test for #10: end-to-end against Arbitrum One mainnet using
dust funds — single ticket, single sender, observe redemption land
on-chain. **No testnet step.** (User memory `feedback_no_livepeer_testnets`.)

In addition, a pre-#10 commit should land the wire-compat fixturegen
(§8) — ideally between #2 and #4 — so the round-trip test is
green throughout the migration.

---

## 11. Resolved decisions

All eight open questions were resolved on 2026-05-06. The implementing
agent works against these locks; rationale captured for future readers.

### Q1. `chain-commons` extraction — adopt or skip?

**DECIDED: skip — build minimal in-tree provider impls.** No
sibling `livepeer-modules/chain-commons` Go module dependency. The
v0.2 provider interfaces are right-sized; ~700 LOC of in-tree
plumbing (RPC dial, gas oracle, redemption tx submit, clock poller)
is shaped exactly to those interfaces. Revisit if a second chain-
talking component ever appears in the monorepo.

### Q2. Canonical Arbitrum RPC provider

**DECIDED: provider-agnostic code; documented recommendations in
the runbook.** `--chain-rpc` accepts any HTTP(S)/WS(S) endpoint.

For the **livepeer community specifically**, the recommended defaults are:

- **Non-archive (primary):** `https://liveinfraspe.com` — community-
  funded, free, no signup. Sufficient for the daemon's normal flow
  (receiver-side `eth_call` reads + redemption tx submission;
  sender-side `eth_call` only — no archive depth required).
- **Archive (forensics / chain-state spelunking / queue backfill):**
  Chainstack. Used only when operators need historical state beyond
  the live RPC's window.
- **Alternatives:** Alchemy, Infura, QuickNode, Ankr — any work; pick
  per operator preference + budget. Document the trade-offs in the
  operator runbook.

The daemon does **not** ship a default `--chain-rpc` value;
operators must specify. The runbook lists the above as the
recommended defaults.

### Q3. go-livepeer pin for fixturegen

**DECIDED: latest tagged release; nightly drift check tracks
`main`.** The fixturegen tool's go.mod pins
`github.com/livepeer/go-livepeer` at the latest stable v0.8.x tag at
plan-0016-implementation time. CI is reproducible; the committed
fixture bytes are stable. A nightly drift check re-runs fixturegen
against `main` (separate go.sum) and alarms if upstream wire format
diverges from the pinned tag — manual escalation, not auto-update.

### Q4. KeyStore — extend or sibling for tx signing?

**DECIDED: option (b) — new sibling `TxSigner` interface.** New
`TxSigner` interface in `payment-daemon/internal/providers/providers.go`,
alongside the existing `KeyStore`. Both consume the same loaded
`*ecdsa.PrivateKey`, but the interfaces stay focused:

- `KeyStore.Sign(hash)` — EIP-191 personal_sign for ticket hashes.
- `TxSigner.SignTx(tx, chainID)` — Ethereum transaction signing for
  redemption submissions.

Sender mode wires `KeyStore` only (no on-chain tx). Receiver mode
wires both. Splitting the interfaces lets sender-mode skip wiring
`TxSigner`; cleaner separation; matches the prior impl pattern.

### Q5. WebSocket subscriptions for round/block tracking

**DECIDED: polling.** Clock poller at `--clock-refresh-interval`
(default 30s); gas-price poller at `--gasprice-refresh-interval`
(default 5s). No `eth_subscribe`, no WSS reconnect logic. Bounded
staleness; simpler; robust to flaky RPC. WSS subscriptions are a
future optimization if profiling shows the staleness window matters.

### Q6. Sender-funder CLI — in scope?

**DECIDED: out-of-band — no `livepeer-payment-funder` CLI in plan
0016.** Operators top up `TicketBroker.fundDeposit` /
`fundReserve` via Foundry / cast / Etherscan / MetaMask. Yet-another-
binary tax for a once-per-deposit op is unjustified. Defer to a
future plan if operator demand surfaces.

### Q7. Offline sender mode (`--no-chain-sender`)

**DECIDED: do NOT ship.** Bypassing the escrow check documented in
`operator-runbook.md:148-160` is a footgun — the sender's
`validateSenderInfo` is the protection against compromised receivers.
Bypass = unbounded loss for the gateway operator. Hard no in v0.1.
Air-gapped operators (rare) can ride on plan 0016's chain-stub fakes
in dev mode if it becomes urgent; production-shaped offline mode is
its own future plan with explicit threat-model justification.

### Q8. Hardware wallet (Ledger / AWS KMS) for hot key

**DECIDED: out of scope for 0016.** The `KeyStore` + `TxSigner`
interfaces are abstract enough; swap-in is mechanical when an
operator concretely needs it. Premature without a real customer
asking. Cold-side hardware wallet is plan 0019's domain
(`secure-orch-console`).

---

## 12. Out of scope (deferred to future plans)

- **Warm-key rotation lifecycle.** Plan 0017 owns this (per
  `0014-...md:121-122` deferral list). Plan 0016 ships V3-keystore
  static unlock at boot; key rotation = restart.
- **Service-registry resolver integration.** Sender mode in plan
  0016 still uses `--ticket-params-base-url`; the
  `--resolver-socket` path stays unwired. Tracked as plan-0014 tech
  debt. Resolver is its own future plan.
- **Hardware-wallet KeyStore.** See Q8.
- **Sender-funder CLI.** See Q6.
- **Subscription-based round/block tracking.** See Q5.
- **Multi-tx redemption pipelining (chain-commons.txintent
  port).** See Q1 + §5.3.
- **Bolt fsck / startup integrity check.** Tracked in prior impl's
  tech-debt; not a plan 0016 blocker.
- **Arbitrum L1-message handling for cross-chain reserve claims.**
  Out of scope; not used by Livepeer's payment flow.
- **Full Prometheus metrics surface.** v0.2 scaffolds the recorder
  (`operator-runbook.md:325-358`); plan 0016 wires the counters
  listed there. Out-of-scope: histograms / distribution metrics
  beyond what the runbook lists.
- **GraphQL / human-friendly redemption-status API.** Plan 0016 ships
  the existing `ListPendingRedemptions` / `GetRedemptionStatus` gRPC
  calls (currently stubbed in
  `payment-daemon/internal/service/receiver/receiver.go:248-260`); a
  richer operator surface is a future plan.

---

## 13. Acceptance for plan 0016

Plan 0016 is "done" when:

1. The wire-compat round-trip test passes against a real
   go-livepeer-generated fixture (the v0.2 zero-byte placeholder is
   replaced).
2. Receiver-mode daemon validates a real ticket signed by the
   sender-mode daemon, recovers the sender address, evaluates win
   probability, and queues winners.
3. Receiver-mode daemon redeems a winning ticket on Arbitrum One
   mainnet — verified by tx hash + on-chain receipt — using dust
   funds.
4. Sender-mode daemon refuses to sign when the broker reports zero
   deposit (confirmed against a real but-empty mainnet sender
   address).
5. The dev-mode banner (`DEV MODE — --chain-rpc is empty`) still
   prints when `--chain-rpc` is omitted; production deployments
   without it page on first log scrape.
6. `make smoke` for `capability-broker/`, `make test-compose` for
   `livepeer-network-protocol/conformance/`, and `make smoke` for
   `openai-gateway/` all stay green (regression check; chain
   integration must not break the dev-mode flows).
7. PLANS.md is refreshed; plan 0016 file moves to
   `docs/exec-plans/completed/`.

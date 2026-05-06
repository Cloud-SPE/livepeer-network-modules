---
title: Warm-key lifecycle and cold-orchestrator escalation
plan: 0017
status: completed
last-reviewed: 2026-05-06
audience: orchestrator operators, gateway operators, payment-daemon implementers
---

# Plan 0017 — warm-key handling (completed)

Implementation landed standalone (without plan 0016): the V3 keystore
loader replaces `devkeystore` in production mode; `devbroker`,
`devclock`, and `devgasprice` stay stubbed pending plan 0016. End
state: payment-daemon has real signing keys but no chain interaction
yet — a clean intermediate.

Implementation map (5-commit series on worktree branch
`worktree-agent-a8fc350329308a523`):

- **C1** — `feat(payment-daemon): V3 keystore loader + password resolution`
  - `payment-daemon/internal/providers/keystore/jsonfile/`
  - `payment-daemon/internal/providers/keystore/inmemory/`
  - `payment-daemon/cmd/livepeer-payment-daemon/password.go`
  - `go.mod` += `github.com/ethereum/go-ethereum` v1.17.2 (imports
    restricted to `accounts`, `accounts/keystore`, `crypto`)
- **C2** — `feat(payment-daemon): wire V3 keystore in production mode + single-wallet WARN`
  - `cmd/livepeer-payment-daemon/main.go` selects devkeystore vs
    `inmemory` based on `--chain-rpc` presence; eager decrypt before
    binding the gRPC socket; specific config errors per §5.2 with
    exit code 2; identity-split logging per §5.3 (locked decision
    §11.5 — WARN, do not block); standalone INFO line clarifying
    broker / clock / gas-price stay dev-mode
- **C3** — `feat(payment-daemon): rotation safety invariants + runbook §6.5`
  - `cmd/livepeer-payment-daemon/rotation_test.go` — BoltDB survives
    keystore swap; gRPC socket is not bound on decrypt failure
  - `payment-daemon/docs/operator-runbook.md` §5 extended with
    "Single-wallet vs hot/cold split" + "5.1 Hot-key rotation
    runbook (5 steps, on-call card)"
- **C4** — `feat(lint): no-secrets-in-logs check`
  - `payment-daemon/lint/no-secrets-in-logs/` — analyzer + tests +
    fixtures; `TestRunPaymentDaemonTreeIsClean` runs as part of
    `make test` and fails on any deny-list finding in the live
    daemon tree
- **C5** — `docs(payment-daemon): close plan 0017` — this file moves
  to `completed/`; PLANS.md updated to flag 0017 as complete and
  point at plan 0016 as the next-stage chain integration

The original design body (open questions, threat model, decisions in
§11) is preserved below verbatim.

---

## Original design (preserved)

Design-only when written; superseded by the implementation map above.

## 1. Status and scope

Plan 0014 shipped the wire-compat envelope and a stubbed sender daemon
(deterministic dev signing, no chain). Plan 0016 (parallel to this
one) lands the real chain providers — `Broker`, `Clock`, `GasPrice`,
and the V3 JSON `KeyStore` impl behind the existing
`payment-daemon/internal/providers` interfaces.

**Plan 0017 is the operator-facing key-handling layer that sits on
top of 0016's plumbing.** In scope:

- Warm-key lifecycle on both modes:
  - sender (gateway) — signs ticket batches at request rate.
  - receiver (orchestrator) — signs `redeemWinningTicket` redemption
    transactions at winning-ticket rate.
- Relationship between the warm key and the **cold orchestrator
  identity** registered on `BondingManager`.
- Failure recovery: rotation, revocation, coercion, operator turnover.
- Threat model that justifies the hot/cold split.
- The cold-key signed manifest publication boundary
  (mirror, not implementation).

Not redesigned (already pinned, see §2):

- `--orch-address`, `--keystore-path`, `--keystore-password-file`,
  `LIVEPEER_KEYSTORE_PASSWORD`, `--dev-signing-key-hex`.
- `providers.KeyStore` interface
  (`payment-daemon/internal/providers/providers.go:50` —
  `Address()` + `Sign(hash)`).
- Probabilistic-micropayment economics, escrow, redemption gas
  pre-checks (plan 0014).

## 2. What is already settled

| Concern | Source |
|---|---|
| Hot wallet vs cold orchestrator split rationale | `payment-daemon/docs/operator-runbook.md` §5 |
| `--orch-address` semantics (default empty → keystore address; explicit → ticket `Recipient`) | runbook §5 |
| V3 JSON keystore via `--keystore-path` | runbook §10 step 4 |
| Password sources: `--keystore-password-file` XOR `LIVEPEER_KEYSTORE_PASSWORD`; both set is an error | runbook §10 step 4 |
| Dev signing via `--dev-signing-key-hex` / `LIVEPEER_DEV_SIGNING_KEY_HEX`; rejected when `--chain-rpc` is set | runbook §9 |
| Production startup checklist | runbook §10 |
| Raw key never logged; derived address logged once at INFO | runbook §8 |

Prior reference implementation that informed these decisions:

- `livepeer-modules-project/payment-daemon/internal/providers/keystore/jsonfile/jsonfile.go` — V3 JSON loader (`ethkeystore.DecryptKey`).
- `livepeer-modules-project/payment-daemon/internal/providers/keystore/inmemory/inmemory.go` — in-memory `providers.KeyStore` impl with redacted `String()`.
- `livepeer-modules-project/payment-daemon/cmd/livepeer-payment-daemon/password.go` — XOR password resolution.
- `livepeer-modules-project/payment-daemon/cmd/livepeer-payment-daemon/providers.go:230-251` — wires V3 keystore into providers at boot.

## 3. Threat model

### 3.1 Filesystem access to the daemon host

Attacker reads `--keystore-path` and the password source. Recovers the
warm key, signs arbitrary tickets / redemption txs.

**Bound:** hot wallet only signs; it is **not** the orchestrator
identity. Loss is bounded by:
- Sender: gateway's deposit + reserve replenishment cycle, gated by
  `MaxEV`, `MaxTotalEV`, `DepositMultiplier` (runbook §2).
- Receiver: warm wallet's ETH-for-gas balance only; ticket face value
  flows to `--orch-address`, not the warm key.

Mitigations are operator-side (filesystem perms, secrets manager,
tmpfs); the daemon trusts the host.

### 3.2 Chain RPC access only

Attacker reads chain state, submits txs from their own funds. Cannot
sign as us. **Bound:** ECDSA signature recovery. Grief possible
(racing redemptions), no value extraction.

### 3.3 Cold orchestrator key compromise

Attacker holds the cold wallet that registered the orch on
`BondingManager`.

**Consequence:** game over for this orch identity — attacker can
withdraw bonded LPT, redirect ticket face values via
`setSigningAddress`, change `ServiceURI`.

**Bound:** other orchestrators are untouched; Livepeer has no
cross-orch failure mode. Recovery is operator-driven re-onboarding
under a new cold key. The hot/cold split exists to make this rare.

### 3.4 Compromised hot key (active exploitation window)

Attacker has the warm key.

- **Sender side:** drains the gateway's deposit + replenishments. Cap:
  per-ticket EV ≤ `MaxEV`, batch ≤ `MaxTotalEV`, ticket face_value ≤
  `Deposit / DepositMultiplier`.
- **Receiver side:** signs `redeemWinningTicket`. Since face value
  flows to `--orch-address`, the worst case is the attacker spends
  the warm wallet's gas budget submitting valid redemptions.

**Bound:** sender = operator's chosen replenishment cadence; receiver
= warm wallet ETH balance.

### 3.5 Compromised RPC provider

Attacker controls `--chain-rpc`. Can return wrong gas prices, withhold
confirmations, replay-feed stale broker state.

- **Receiver:** redemption txs censored. `--validity-window` (~2
  rounds) caps the censor's leverage before tickets expire.
- **Sender:** stale broker state could mint tickets against an
  exhausted deposit; receiver-side `MaxFloat` rejects them at
  redemption — grief, not theft.

**Bound:** metrics
(`livepeer_payment_pending_redemptions_total`,
`redemption_attempt_total{outcome="failed_revert"}`) reveal it; switch
RPC. Multi-URL failover (chain-commons `ccrpcmulti.Open`) is the
defense; out of scope for 0017.

### 3.6 Coercion / regulatory / operator turnover

Operator forced to hand over the key, leaves, or loses access.

- **Hot key turnover:** rotate on schedule (§6); rotation is a
  documented, tested procedure, not a fire drill.
- **Cold key loss:** full re-onboarding. The protocol does not
  recover lost cold keys by design. Operators MUST keep the cold key
  in a recoverable cold store (multi-sig, hardware wallet with
  documented seed-recovery, custodial cold storage).

## 4. Hot wallet / cold orchestrator split — operator playbook

This summarizes runbook §5 + §10; plan 0017 adds **no new flags** but
its implementation MUST honor the split end-to-end.

1. **Generate the hot signing key** — V3 JSON keystore from
   `geth account new` or any V3-emitting wallet. Fresh,
   single-purpose ETH address. Holds **only redemption gas**; never
   bonded LPT, never receives ticket face value.
2. **Authorize the hot key under the cold orch identity.** Call
   `BondingManager.setSigningAddress(hotAddress)` from the cold
   wallet. The protocol now accepts redemption txs referring to
   tickets where `Recipient = coldOrchAddress` when signed by `hot`.
3. **Set `--orch-address` to the cold address.** Hot keystore loads
   by `--keystore-path`; `--orch-address` overrides the recipient
   embedded in tickets so the cold address shows up on-chain.
4. **Confirm via startup log.** The daemon logs `signer-address`
   (hot) and `orch-address` (cold) as separate fields; they should
   differ (prior impl: `providers.go:457 logStartup`).

Hot wallet exposure ceiling is bounded by the deposit + reserve
replenishment cycle (sender) or the wallet's gas balance (receiver).

## 5. V3 keystore loading — concrete behaviors

### 5.1 Password sources (already pinned)

- `--keystore-password-file PATH` — daemon reads the file at startup,
  trims trailing `\r\n` (matches `password.go:35`).
- `LIVEPEER_KEYSTORE_PASSWORD` env var.
- **Both set is an error.** Daemon refuses to start; error names both
  knobs so the operator sees what to remove.
- Neither set + `--chain-rpc` set is an error (production needs the
  keystore). Dev mode without `--chain-rpc` ignores keystore flags.

### 5.2 Empty / corrupt / unreadable keystore

Fail fast at startup with operator-actionable errors:
- Path missing → `read keystore: <path>: no such file`.
- Bad JSON → `decrypt keystore: invalid format`.
- Wrong password → `decrypt keystore: could not decrypt key with given password`.
- Zero-byte file → `keystore file is empty`.

Daemon must not start, must not bind the gRPC socket, must not expose
`/metrics`. Exit code 2 (existing config-error convention).

### 5.3 Address mismatch between keystore and `--orch-address`

A common typo: `--orch-address` set to the **hot** address by mistake,
defeating the split.

**Recommendation:** the daemon does **not** error. It cannot
distinguish a single-wallet dev setup from a misconfigured hot/cold
split. Instead:
- `--orch-address` equals keystore → log `WARN single-wallet config
  — hot signer is also the on-chain orchestrator identity. OK for
  dev, dangerous for prod.` once at startup.
- Differs → log INFO `hot/cold split active`.

A preflight that calls `BondingManager.getTranscoder(orchAddr)` to
verify a registered transcoder is **deferred** to a future plan
(§13).

### 5.4 Eager vs lazy decrypt

**Eager.** Decrypt the V3 keystore at startup, before binding the
gRPC socket. Rationale: fail-fast on bad password before any caller
assumes the daemon is alive; password lifetime in memory is shortest;
scrypt cost is one-shot, not per-request. Matches prior impl
`providers.go:234-251`.

### 5.5 Password lifecycle in memory

- Password is read, passed to keystore decrypt, then dropped (zero
  the bytes).
- Decrypted `*ecdsa.PrivateKey` lives for the daemon's lifetime —
  no operational benefit to scrubbing because every signature needs
  it.
- Never log the password (port `lint/no-secrets-in-logs/check.go`
  from prior impl).
- Never include the password in startup-config dumps, error wrappers,
  or panic stacks.
- `KeyStore.String()` redacts the key (prior impl
  `keystore/inmemory/inmemory.go:78`).

## 6. Key rotation

The daemon does not orchestrate rotation; rotation is **off-host,
operator-driven**. The daemon makes it safe by being restart-tolerant.

### 6.1 Pre-rotation (no live impact)

1. Generate a new V3 keystore on the host
   (`/etc/livepeer/hot-wallet.next.json`).
2. **Authorize on chain:** `BondingManager.setSigningAddress(newHot)`
   from the cold wallet. The protocol now accepts redemption txs
   from either old or new hot key (current `setSigningAddress`
   semantics permit one authorized signer at a time; if the previous
   signer was set, it is replaced — confirm against the deployed
   contract before commit).
3. **Optional dual-running for senders:** spin up a second sender
   daemon on a sibling unix socket pointing at the new keystore;
   gateway dual-writes during migration. Most operators won't need
   this. Receiver-side dual-running is unnecessary — on-chain
   authorization is the actual switchover.

### 6.2 Cutover

4. Update password source (env or file) for the new keystore.
5. Update `--keystore-path` to the new file.
6. Restart the daemon. Brief unavailability (seconds) on socket
   restart; senders see transient `connection refused`; receivers
   lose nothing — the redemption queue is durable in BoltDB.

### 6.3 Post-rotation

7. Confirm `setSigningAddress` has the requisite confirmations.
8. Delete the old keystore file from the host.

### 6.4 In-flight tickets signed under the old key

- **Sender side:** signed tickets are already in the recipient's
  hands; recipients redeem against the orch's currently-authorized
  signer. Tickets carry `Recipient = coldOrchAddress`, not the hot
  signer; they remain valid because the cold-orch identity hasn't
  changed.
- **Receiver side:** queue items were signed by **senders**, not us.
  Our hot key signs the redemption tx itself. After rotation the new
  hot key signs new redemption txs; queue is unaffected.

The forward-looking risk window is the dual-running window where the
old hot key still sits on the host. Best practice: physically remove
the old keystore once revocation lands on chain.

### 6.5 Rotation runbook (5 steps, on-call card)

1. Generate a new V3 keystore on the host; put password in the
   secret store under a new key.
2. From the cold wallet, call
   `BondingManager.setSigningAddress(NEW_HOT_ADDRESS)`.
3. Update env / password file; update `--keystore-path` to the new
   file.
4. Restart the daemon; verify startup log shows the new
   `signer-address`.
5. Wait for `setSigningAddress` confirmations; delete the old
   keystore file.

## 7. Cold-key signed manifest

The cold key has two signing roles:

1. **`BondingManager.setSigningAddress`** — establishes warm-key
   delegation (§4.2).
2. **Manifest signing** — signs the orch's capability manifest
   published at `/.well-known/livepeer-registry.json`, per
   `livepeer-network-protocol/manifest/schema.json` and the
   verification flow in `livepeer-network-protocol/manifest/README.md`.

Plan 0017's role for the manifest:

- **Cold key signs**, not the warm key. The warm key is for redemption
  tx signing only.
- **JCS canonicalization** (RFC 8785) is the canonicalization scheme
  before hashing the payload — pinned in `schema.json:152`
  (`"canonicalization": "JCS"`) and `manifest/README.md:21,35`.
  Confirmed; not redesigned here.
- **Manifest rotation cadence:** driven by `expires_at` in the
  payload. Recommended **monthly to quarterly** in steady state, plus
  on every capability-list change (new capability, price change,
  `worker_url` change). Annual is too long: an exited operator's
  manifest stays valid for too long. Per-major-release intersects
  weakly with capability changes; cadence belongs to the operator.
- **Where the manifest lives:** HTTPS at
  `<orch>/.well-known/livepeer-registry.json`. **Not on chain, not on
  IPFS** in v1 — the on-chain `ServiceRegistry` entry tells resolvers
  where to fetch from, then resolvers fetch and verify the signature.
  IPFS pinning is a future redundancy layer; not 0017.
- **Where cold-key manifest signing happens:** out-of-band on
  `secure-orch-console` (per
  `docs/design-docs/architecture-overview.md` §"signed manifest").
  The payment daemon is **not** involved in manifest signing. Plan
  0017 ships the warm-key half only; the cold-key half is
  `secure-orch-console`'s responsibility.

Manifest signing is OUT OF SCOPE for 0017's code commits but is
documented here because the cold-key role is the mirror of the
warm-key role and operators need both sides explained in one place.
See §11 — open question on whether manifest plumbing pulls into 0017.

## 8. Hardware-wallet support (Ledger et al.)

### 8.1 Sender side: out of scope

Senders sign tickets at request rate — many per second on a busy
gateway. Hardware wallets are too slow (Ledger Nano ~1 ECDSA/sec
under best conditions). Forcing one into the per-ticket path turns
the daemon into a request-rate bottleneck.

**Recommendation:** warm-key signing on a hardware wallet is
**explicitly not supported.** The warm key is software-resident by
design. Operators worried about warm-key compromise rely on rotation
discipline (§6) and EV bounds (`MaxEV`, `MaxTotalEV`,
`DepositMultiplier`), not on a hardware-wallet firewall.

### 8.2 Cold key: applicable, but not via the daemon

The cold key is a perfect fit for a hardware wallet:
- `BondingManager.setSigningAddress` (rare, manual).
- Manifest signing (rare, manual, off-host on secure-orch-console).
- Orch onboarding txs (`bond`, `transcoder`, `setServiceURI`).

These are human-in-the-loop ops where a hardware-wallet prompt is
fine.

**Operator-runbook impact:** none. The hardware wallet never touches
the daemon process — it touches `secure-orch-console` and direct
contract-call workflows (`livepeer_cli`, web UI, MetaMask + Ledger).
No new flag, no daemon code path.

## 9. Sender-side warm key vs receiver-side warm key

Different surfaces, different rates, different risk shapes.

### 9.1 Sender (gateway) warm key

- **Rate:** every paid request mints a ticket. Tens to hundreds /sec.
- **Operation:** EIP-191 `personal_sign` over the ticket hash
  (prior impl `keystore/inmemory/inmemory.go:50-74`). Off-chain only.
- **What gets signed:** ticket bytes. Signer's address ends up in
  the ticket's `Sender` field; the recipient verifies the signature
  on receipt.
- **Authorization needed:** none on chain. Sender identity is
  whatever ETH address the keystore decrypts to.
- **Compromise recovery:** rotate keystore; top up the new address's
  TicketBroker deposit. No on-chain authorization step.

### 9.2 Receiver (orchestrator) warm key

- **Rate:** one redemption per winning ticket. With `1/5000` win-prob
  and ~1 req/sec, ~1 redemption per 80 minutes.
- **Operation:** Ethereum transaction signing
  (`redeemWinningTicket`). On-chain.
- **What gets signed:** the redemption tx itself, sent from the warm
  key as the EOA `from`. Ticket's `Recipient` (cold orch) determines
  who the contract pays.
- **Authorization needed:** ETH for gas, **plus
  `BondingManager.setSigningAddress`** so the cold orch identity
  vouches for it.
- **Compromise recovery:** rotate per §6.

### 9.3 Should the same key serve both modes?

**Recommendation: keep them separate.** A sender-mode daemon MUST NOT
share a keystore file with a receiver-mode daemon on the same orch
identity; the sender warm key and the receiver warm key SHOULD be
different ETH addresses with different keystore files.

Rationale:
- **Different threat surfaces** — sender compromise drains gateway
  deposit; receiver compromise burns redemption gas. Different
  rotation cadences, different on-call procedures.
- **Different on-chain authorization** — receiver warm key is
  `setSigningAddress`-authorized; sender warm key isn't. Sharing
  hands an attacker both surfaces simultaneously.
- **Runbook implicitly assumes this.** `--orch-address` semantics
  only apply to receivers (it's the embedded ticket recipient); the
  sender's keystore address IS the sender identity.

The daemon does **not** enforce "different keystore per mode" —
that's operator discipline. But the docs should be explicit. Plan
0017's runbook delta should add: *"Run sender and receiver daemons
with separate keystores."*

## 10. Operator surface additions

Recommendation: **none beyond runbook §10 today.** The runbook is the
operator contract; stability across 0014 → 0016 → 0017 is itself a
feature.

Plan 0017 implementation should:
- Honor existing flags (`--orch-address`, `--keystore-path`,
  `--keystore-password-file`, `LIVEPEER_KEYSTORE_PASSWORD`,
  `--dev-signing-key-hex`).
- Tighten startup error messages per §5.2 (text changes, not new
  flags).
- Extend the structured startup log with the WARN / INFO lines from
  §5.3 (single-wallet vs hot/cold split).

If operator pressure later demands a `BondingManager.getTranscoder`
preflight, that is a separate `--orch-preflight` flag in a future
plan, not 0017. Keep 0017 minimal.

## 11. Resolved decisions

All six open questions were resolved on 2026-05-06. The implementing
agent works against these locks; rationale captured for future readers.

1. **Companion CLI for rotation?** **DECIDED: manual only.** No
   `livepeer-key-rotate` companion CLI in v0.1. The five-step rotation
   runbook (§6.5) is short; an automated rotator would itself need
   cold-key access, defeating the cold-key isolation that motivates
   the hot/cold split.

2. **Cold-key signed manifest in 0017?** **DECIDED: strictly
   warm-key.** No manifest signature verification in the daemon; no
   publication helper. Manifest signing is `secure-orch-console`'s
   domain (plan 0019); manifest verification is the resolver /
   gateway's domain. Neither belongs to the payment daemon.

3. **Hardware-wallet integration timing.** **DECIDED: never in
   0017.** Hot-side rejected (Ledger ~1 ECDSA/sec — too slow for
   ticket-rate signing). Cold-side belongs to plan 0019
   (secure-orch-console), not the payment daemon.

4. **Sender-side daemon WITHOUT chain RPC?** **DECIDED: defer.** No
   production-shaped offline-sender mode in 0017. Air-gapped
   operators are rare; the use case can ride on plan 0016's
   chain-stub fakes if it becomes urgent.

5. **Single-wallet WARN — block startup or continue?** **DECIDED:
   WARN at INFO, do not block.** Solo small operators legitimately
   run single-wallet (low TVL, simpler ops); hard-block would
   regress go-livepeer behaviour. Daemon logs `WARN single-wallet
   config — hot signer is also the on-chain orchestrator identity.
   OK for dev, dangerous for prod.` once at startup per §5.3.

6. **Password scrubbing — opt-in flag or default?** **DECIDED:
   simple `[]byte` zeroing post-decrypt.** No third-party
   secure-memory dep (`memguard` etc.). The threat we'd defend
   against (process-memory read) would also have the decrypted
   private key, so secure-mem for just the password is mostly
   cosmetic. Keep dependency-free.

## 12. Migration / commit cadence inside plan 0017

Plan 0017 commits land paired with plan 0016's. Suggested cadence:

1. **`feat(payment-daemon): V3 keystore loader (paired with 0016 C1)`**
   - `internal/providers/keystore/jsonfile/` — V3 loader; ports prior
     impl `jsonfile.go`.
   - `internal/providers/keystore/inmemory/` — `providers.KeyStore`
     impl backed by a decrypted `*ecdsa.PrivateKey`.
   - `cmd/livepeer-payment-daemon/password.go` — XOR resolution of
     `--keystore-password-file` vs `LIVEPEER_KEYSTORE_PASSWORD`.
   - Code only; no runbook changes.

2. **`feat(payment-daemon): wire warm key into provider builder (with 0016 C2)`**
   - `cmd/livepeer-payment-daemon/providers.go` — replace
     devkeystore with V3-backed keystore in production mode.
   - Eager decrypt; specific error messages (§5.2);
     redacted-in-logs invariant.
   - Single-wallet WARN / hot-cold-split INFO startup lines (§5.3).

3. **`feat(payment-daemon): rotation safety invariants`**
   - Regression test: in-flight redemption queue survives keystore
     swap (already a property of BoltDB persistence; assert it).
   - Startup verifies decrypt before binding gRPC.
   - Docs: extend `operator-runbook.md` §5 with §6.5 rotation
     runbook.

4. **`feat(lint): no-secrets-in-logs check`**
   - Static check: no log call may reference an `*ecdsa.PrivateKey`
     value or the loaded password. Mirror prior impl
     `lint/no-secrets-in-logs/check.go`.

5. **`docs(payment-daemon): close plan 0017 (paired with 0016 close)`**
   - This file moves to `docs/exec-plans/completed/`.
   - Cross-references in `operator-runbook.md` updated.

If user picks up §11.1 (rotation CLI) or §11.2 (manifest verification
in daemon), commits 6–7 land for those.

## 13. Out of scope for plan 0017

- **Manifest signing** — cold-key responsibility, in
  `secure-orch-console` (§7).
- **Manifest verification on receiver startup** — resolver / gateway
  responsibility (§11.2).
- **`BondingManager.getTranscoder(orchAddr).activationRound`
  preflight** — useful but a separate flag and a separate plan.
  Prior impl tracks this as `orch-address-registration-check`
  tech-debt
  (`livepeer-modules-project/payment-daemon/docs/operations/running-the-daemon.md:250`).
- **Multi-URL RPC failover** — defense against §3.5; chain-commons
  already has it (`ccrpcmulti.Open`). Not extended in 0017.
- **Hardware-wallet integration for warm-key signing** — explicitly
  rejected (§8.1). Cold-key hardware-wallet integration belongs to
  `secure-orch-console`.
- **Companion `livepeer-key-rotate` CLI** — open question §11.1; not
  committed to.
- **Air-gapped sender mode** — open question §11.4; not committed
  to.
- **Secure-memory password scrubbing via third-party libs** — open
  question §11.6; default plan is simple `[]byte` zeroing.
- **In-flight key revocation forensics** — anomaly detection on
  suspicious ticket patterns is metrics-pipeline territory, not
  daemon territory. The operator's response to suspected compromise
  is the §6.5 rotation runbook.

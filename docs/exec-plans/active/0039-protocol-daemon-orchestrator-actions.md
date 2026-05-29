# Protocol-Daemon Orchestrator Actions — Exec Plan 0039

> Status: Active · Owner: Mike Zupper · Last updated: 2026-05-29
> Branch: `feat/protocol-daemon-updates`
> Components: `protocol-daemon`, `secure-orch-console`, `proto-contracts`

## 0. TL;DR

Add five operator-facing orchestrator actions to `protocol-daemon`, each
also surfaced in `secure-orch-console`:

1. Disable/enable the automatic round-init call (console-managed; default **off**).
2. Set reward cut & fee cut (`BondingManager.transcoder`).
3. Transfer bonded LPT (`BondingManager.transferBond`).
4. Withdraw ETH fees (`BondingManager.withdrawFees`).
5. Vote on treasury proposals (`LivepeerGovernor.castVote` / `castVoteWithReason`).

The cross-cutting change is that the **enable/disable + operational config**
for every automated behavior (round-init, reward, transfer-bond,
withdraw-fees) moves out of CLI flags into **runtime config owned by the
daemon and edited from the console**. Items 3 and 4 are automated,
round-lock-gated loops modeled on the reference app
`livepeer-funds-transfer`; item 5 is a manual governance form.

## 1. Context & motivation

`protocol-daemon` today runs `round-init` and `reward` against
`RoundsManager`/`BondingManager` through `chain-commons.services.txintent`
(see [completed/0020](../completed/0020-protocol-daemon-migration.md) and
[0024](../completed/0024-protocol-daemon-grpc-adapter.md)). Operators have
asked for the remaining orchestrator self-service actions that
`go-livepeer`'s `livepeer_cli` exposes, plus the bond/fee automation that
the standalone Rust service
`~/git-repos/livepeer-cloud-spe/livepeer-funds-transfer` performs.

Two reference sources ground every decision here:

- **`~/git-repos/go-livepeer-mikez`** — authoritative ABIs
  (`eth/contracts/bondingManager.go`, `eth/contracts/LivepeerGovernor.go`),
  the percent↔ppm conversion (`eth/helpers.go`), and the `livepeer_cli`
  UX (`cmd/livepeer_cli/wizard_transcoder.go`).
- **`~/git-repos/livepeer-cloud-spe/livepeer-funds-transfer`** — the
  round-lock gating policy and bond/fee automation behavior we are
  porting into the daemon (improving on its in-memory dedup and
  blind-send failure modes).

### Hard invariant

The daemon's hot keystore wallet (`--keystore-path`) **is** the
orchestrator's registered/bonded address. The contracts require
`msg.sender == orchestrator` for every write here (`transcoder` sets
`msg.sender`'s cut, `transferBond` moves `msg.sender`'s bond,
`withdrawFees` withdraws `msg.sender`'s fees, governor votes use
`msg.sender`'s snapshot voting power). The **daemon** signs every tx; the
**console** is only remote-control + audit. The console's cold key
(manifest signing) is a different key and is never involved in these txs.

## 2. Goals / non-goals

### Goals
- New `BondingManager` write calldata builders + a `treasury`/governor
  provider, all behind the existing layer rule.
- A `bondingadmin` service (set-shares, transfer-bond, withdraw-fees) and a
  `governor` service (vote), available whenever the relevant contract
  address resolves — independent of `--mode`.
- Daemon-owned, BoltDB-persisted **operational config** with
  `GetConfig`/`SetConfig` RPCs; console renders the editor.
- Round-lock-gated automation for transfer-bond and withdraw-fees, driven
  by the existing event streams (no busy polling).
- A shared safety stack for every write: durable idempotency via
  `txintent`, fresh authoritative gate read, and a pre-submit `eth_call`
  dry-run.
- Console surfaces for all five actions (forms + status), session-gated
  with typed-confirmation and append-only audit.
- Docs/runbook updated; tests + coverage gate green.

### Non-goals
- No cold-key involvement in any protocol tx or config edit.
- No support for an orchestrator identity that differs from the daemon's
  hot wallet (explicitly out of scope per the hard invariant).
- No proposal *creation*/queue/execute — voting only.
- No automation for set-shares or voting (both are judgment calls).
- No new auth model in the console (reuse session + typed-confirm + audit).

## 3. Design

### 3.1 Layer rule (unchanged)

```
cmd/ → runtime/ → service/ → repo/ → providers/ → types/
                             └─▶ chain-commons.providers.* / chain-commons.services.*
```

- ABI / go-ethereum imports stay in `internal/providers/{bondingmanager,treasury,...}`.
- Business logic in `internal/service/{bondingadmin,governor,roundinit,reward}` — no go-ethereum.
- Every on-chain write goes through `chain-commons.services.txintent.Manager.Submit`.

### 3.2 Operational config (cross-cutting)

New persisted config, owned by the daemon, stored via
`chain-commons.providers.store` (BoltDB) in a dedicated bucket. Shape:

```
OperationalConfig {
  RoundInitEnabled     bool      // default false
  RewardEnabled        bool      // default true
  TransferBond {
    Enabled            bool      // default false; requires Receiver set
    Receiver           Address
    MinRetainWei       *big.Int  // entered as decimal LPT
  }
  WithdrawFees {
    Enabled            bool      // default false; requires Receiver set
    Receiver           Address
    ThresholdWei       *big.Int  // entered as decimal ETH
  }
  RewardBeforeTransfer bool      // default true
}
```

- **Split:** infra knobs stay start-time flags (`--eth-urls`,
  `--keystore-path`, `--socket`, `--chain-id`, `--controller-address`,
  metrics, log). Policy is the struct above.
- **First-boot defaults** apply when no record exists: reward on,
  round-init off, transfer/withdraw off.
- **Validation in `SetConfig`:** transfer/withdraw cannot be enabled
  without a non-zero receiver; amounts non-negative; receiver ≠ zero.
- **Mid-flight changes apply next round** — the durable per-round
  idempotency key (§3.4) prevents re-firing or clawing back the current
  round.
- New RPCs `GetConfig(Empty)→OperationalConfig` and
  `SetConfig(OperationalConfig)→OperationalConfig` (returns the stored,
  normalized result). Inputs are decimal LPT/ETH strings on the wire,
  converted to wei in the daemon.

### 3.3 Round-state gating (items 3 & 4)

Contract-level the writes only require `currentRoundInitialized`. Policy
(from the reference app) is to gate **fund movement on
`currentRoundLocked`** so reward has been claimed earlier in the round and
the active-set / stake snapshot is finalized before any stake leaves.

- `lockBlock = currentRoundStartBlock + roundLength − roundLockAmount`
  (all `RoundsManager` reads; cache `roundLength`/`roundLockAmount`).
- Drive off the existing `chain-commons.services.roundclock` round stream +
  `SubscribeL1Blocks`; when `block ≥ lockBlock`, fire locked-actions once.
  No repeated `currentRoundLocked()` polling.
- Gate at submit is `locked && initialized` (locked is purely block-based
  and does not imply initialized).
- **Reward-before-transfer guard** (default on when reward enabled): skip
  transfer-bond for a round unless `lastRewardRound == currentRound`.
- Amounts computed from fresh reads at submit:
  transfer = `pendingStake(orch, round) − MinRetainWei` (skip if ≤ retain);
  withdraw = `pendingFees(orch, round)` (skip if < threshold).

### 3.4 Shared safety stack (every write)

1. **Durable idempotency via `txintent`** —
   round-init `Kind=InitializeRound, KeyParams=round`;
   reward `Kind=RewardWithHint, KeyParams=round++orch`;
   transfer `Kind=TransferBond, KeyParams=round`;
   withdraw `Kind=WithdrawFees, KeyParams=round`;
   vote `Kind=CastVote, KeyParams=proposalId++wallet`.
   At most one per (round|proposal), surviving restarts — closes the
   reference app's double-send window.
2. **Fresh authoritative gate read** immediately before submit; a failed
   gate returns a typed `Skip`, not an error.
3. **Pre-submit `eth_call` dry-run** of the exact calldata from the orch
   address — catches reverts (round race, paused, insufficient bond/fees,
   already-voted, closed window, no voting power) **without spending gas**.
4. **No-panic, ctx-aware loops**; failures log and retry next tick within
   the window, idempotency-key-protected.

### 3.5 Contract surfaces (verified against go-livepeer bindings)

- **Item 2** `transcoder(uint256 _rewardCut, uint256 _feeShare)`. RPC takes
  `reward_cut` and `fee_cut` as percentages meaning *what the orch keeps*;
  store `rewardCut` directly, `feeShare = 100 − fee_cut`; convert
  `ppm = percent × 10000` (percDivisor 1,000,000), **truncating** extra
  precision to match `livepeer_cli`. Plain `transcoder()` needs no pool
  hints. Idempotency on the resulting `(rewardCut, feeShare)`.
- **Item 3** `transferBond(address _delegator, uint256 _amount, address
  _oldDelegateNewPosPrev, _oldDelegateNewPosNext, _newDelegateNewPosPrev,
  _newDelegateNewPosNext)` (selector `0x062e98b8`). `_delegator` =
  recipient; **zero hints by default** (contract walks the list), optional
  4-address override. Transfer basis = `pendingStake − retain`.
- **Item 4** `withdrawFees(address payable _recipient, uint256 _amount)`
  (two-arg form confirmed current). Reads: `pendingFees(addr, endRound)`,
  `getDelegator(addr)`.
- **Item 5** `LivepeerGovernor` resolved via controller registry key
  `keccak256("LivepeerGovernor")`. Writes `castVote(proposalId, support)` /
  `castVoteWithReason(proposalId, support, reason)`; **support 0=Against
  1=For 2=Abstain**. proposalId = decimal string → uint256. Safety reads
  (all present on the binding): `State`, `HasVoted`, `ProposalDeadline`,
  `ProposalSnapshot`, `GetVotes`, `Quorum`, `ProposalVotes`, `Clock`.

### 3.6 gRPC surface (`proto-contracts/livepeer/protocol/v1/protocol.proto`)

New messages + RPCs (Go-native handlers mirror them in
`internal/runtime/grpc/server.go`, then the protoc adapter):

- `GetConfig`, `SetConfig`.
- `SetTranscoder(reward_cut, fee_cut)` → `TxIntentRef`.
- `ForceTransferBond`, `ForceWithdrawFees` → `ForceOutcome` (same once-per-
  round locked handler as the auto loop; honor the gate, return typed
  `Skip`).
- `CastVote(proposal_id, support, reason)` → `TxIntentRef`.
- `GetTreasuryProposal(proposal_id)` → state/deadline/has_voted/voting_power
  (safety reads for the console).
- Extend `RoundStatus` with `current_round_locked`.
- New `SkipReason.Code` values: `CODE_ROUND_NOT_LOCKED`,
  `CODE_NOTHING_TO_TRANSFER`, `CODE_BELOW_FEE_THRESHOLD`,
  `CODE_REWARD_NOT_CALLED`, `CODE_ALREADY_VOTED`, `CODE_VOTING_CLOSED`,
  `CODE_NO_VOTING_POWER`.

### 3.7 Console (`secure-orch-console`)

Go HTTP server with embedded templates; bridges to the daemon via
`internal/protocol/client.go`. Pattern to copy: the existing
`SetServiceURI` / `ForceReward` gestures (client method → session-gated
POST handler with typed-confirm + `audit.Record` → route in `server.go`
→ form in `web/templates/*.html`).

- **Config page** — form bound to `GetConfig`/`SetConfig`: four toggles,
  LPT receiver + min-retain, ETH-fee receiver + threshold, reward-before-
  transfer. Validation mirrors the daemon's.
- **Protocol actions page** — add forms: set reward/fee cut; force
  transfer-bond; force withdraw-fees; cast vote (proposalId + For/Against/
  Abstain + optional reason) with the pre-vote safety reads
  (`GetTreasuryProposal`) shown before submit.
- **Round status** — surface `current_round_locked` and whether this
  round's automated actions have fired.

## 4. Work breakdown

### Phase 1 — proto + config foundation
- [ ] Extend `protocol.proto`: config messages, new RPCs, `RoundStatus.current_round_locked`, new `SkipReason.Code`s; regenerate.
- [ ] `internal/types`: `OperationalConfig` + decimal↔wei + percent↔ppm (truncating) helpers with unit tests.
- [ ] `internal/repo/opconfig`: BoltDB-backed config store (load/save/defaults) via `chain-commons.providers.store`.
- [ ] Wire `GetConfig`/`SetConfig` Go-native handlers + validation.

### Phase 2 — providers (ABI builders + reads)
- [ ] `internal/providers/bondingmanager`: add `PackTranscoder`, `PackTransferBond`, `PackWithdrawFees`; add reads `PendingStake`, `PendingFees`, `GetDelegator`.
- [ ] `internal/providers/roundsmanager`: add `CurrentRoundLocked`, `CurrentRoundStartBlock`, `RoundLength`, `RoundLockAmount`.
- [ ] `internal/providers/treasury`: governor address resolution (verify chain-commons controller exposes `LivepeerGovernor`; else config field + override flag), `PackCastVote`/`PackCastVoteWithReason`, reads (`State`, `HasVoted`, `ProposalDeadline`, `ProposalSnapshot`, `GetVotes`).

### Phase 3 — services
- [ ] `internal/service/bondingadmin`: `SetTranscoder`; round-locked once-per-round `TransferBond` + `WithdrawFees` handlers (auto loop + force); reward-before-transfer guard; shared `submitGuarded` (gate read + dry-run + txintent).
- [ ] `internal/service/governor`: `CastVote` + proposal safety reads.
- [ ] `internal/service/roundinit`: split Run-loop (checks persisted toggle, observe-only when off) from Force path.
- [ ] `internal/service/reward`: gate auto-reward on the persisted toggle.
- [ ] `internal/runtime/lifecycle`: schedule locked-action firing off the round + L1-block streams.

### Phase 4 — runtime wiring + RPCs
- [ ] `cmd/.../run.go`: build new providers/services, drop `--auto-*` flags, load config store at boot.
- [ ] `internal/runtime/grpc/server.go`: implement `SetTranscoder`, `ForceTransferBond`, `ForceWithdrawFees`, `CastVote`, `GetTreasuryProposal`, config RPCs; extend `RoundStatus`.

### Phase 5 — console
- [ ] `internal/protocol/client.go`: client methods for the new RPCs.
- [ ] `web/handlers.go` + `server.go` routes: config page; set-shares / transfer / withdraw / vote POST handlers (session + typed-confirm + audit).
- [ ] Templates + `views.go` + `console.js`: config form, action forms, pre-vote safety reads, `current_round_locked` display.

### Phase 6 — docs + hardening
- [ ] Update `protocol-daemon/docs/design-docs/` (config model, round-lock gating, safety stack) and the operator runbook.
- [ ] Update `secure-orch-console` runbook + `AGENTS.md` where behavior changed.
- [ ] `make build/test/lint/coverage-check` green in both components; record any coverage exemptions with reasons.
- [ ] Move this plan to `completed/` and update the index.

## 5. Test plan

- **Unit:** percent↔ppm truncation (incl. fee flip), decimal↔wei, config
  validation (enable-without-receiver rejected), calldata builders vs
  known-good selectors/encodings from go-livepeer, `lockBlock` math.
- **Service:** dev-mode fakes — gate skips (`CODE_ROUND_NOT_LOCKED`,
  `CODE_BELOW_FEE_THRESHOLD`, `CODE_NOTHING_TO_TRANSFER`,
  `CODE_REWARD_NOT_CALLED`, `CODE_ALREADY_VOTED`); idempotency (one tx per
  round/proposal across restart); dry-run failure aborts before submit.
- **Console:** session-gating + typed-confirm rejection paths; audit
  records emitted; `SetConfig` round-trips; vote form renders safety reads.
- **Coverage:** keep the 75% per-package gate.

## 6. Risks & open questions

- **Governor address resolution** — must confirm chain-commons' controller
  exposes `LivepeerGovernor`; fallback is a config field + override flag
  (mirrors `AIServiceRegistryAddress`). *(verify in Phase 2)*
- **`getTranscoder` tuple shape** — chain-commons decodes a subset; ensure
  `lastRewardRound` indexing stays correct for the reward-before-transfer
  guard.
- **Behavior change** — round-init no longer auto-runs by default and there
  are no `--auto-*` flags; existing deployments configure via the console.
  Call out in the runbook + changelog.
- **Zero-hint `transferBond` gas** on large pools — acceptable for an
  occasional action; optional 4-address override exists.

## 7. Rollout

- Ships on `feat/protocol-daemon-updates`; one PR per phase where practical,
  proto/config first.
- Backward-compat note: first boot stamps defaults (reward on, everything
  else off); operators opt into automation from the console.
- No data migration — new BoltDB bucket; absence ⇒ defaults.

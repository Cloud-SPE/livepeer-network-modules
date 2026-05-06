---
title: Plan 0019 — secure-orch + trust-spine design
status: design-doc
opened: 2026-05-06
owner: harness
related:
  - plan 0017 (warm-key handling — payment-daemon side, in flight)
  - plan 0018 (orch-coordinator — candidate-manifest builder, in flight)
  - design-doc stub `trust-model.md` (this plan supersedes it)
  - prior reference impl `livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/`
---

# Plan 0019 — secure-orch + trust-spine design

> **This is a paper plan.** No code, no `go.mod` edits, no scaffolding land
> here. The output is a single design doc that closes the `trust-model.md`
> stub in [`docs/design-docs/index.md`](../../design-docs/index.md) and
> sets the bar for whatever component plan(s) follow.

## 1. Status + scope

### What this plan owns

- The **cold-key host** (`secure-orch`) — physical machine, OS posture,
  egress-only network policy.
- The **manifest packaging format** — canonical bytes, signature
  envelope, sidecar metadata.
- The **`secure-orch-console`** — diff-and-sign UX. Native + CLI.
- The **air-gap workflow** — operator-facing transports for candidate-in
  and signed-out.
- The **verification path** on every receiver of the signed manifest:
  coordinator on upload, resolver on fetch, gateway on resolve.
- The **cold-key escalation flow** — fresh key provisioning, on-chain
  authorization, migration policy.

### What this plan does NOT own

- **Building the candidate manifest.** Coordinator scrapes brokers and
  assembles the candidate. That's plan 0018.
- **Warm-key handling on the gateway-side payment daemon.** Hot ticket
  signing, deposit/reserve management, redemption. That's plan 0017
  (see [`payment-daemon/docs/operator-runbook.md` §5](../../../payment-daemon/docs/operator-runbook.md)
  lines 240–263).
- **Manifest schema evolution.** That's owned by
  [`livepeer-network-protocol/manifest/`](../../../livepeer-network-protocol/manifest/)
  under its own SemVer process.
- **Coordinator atomic-publish semantics.** Plan 0018.

### Deliverable shape

Component layout proposal in §11. Migration sequence in §12. No code.

## 2. What's already settled — DO NOT relitigate

The following are pinned in pre-existing design artifacts. This plan
implements them; it does not negotiate them.

| Pin | Source | Lines |
|---|---|---|
| 8-layer architecture, layer 5 = trust spine | [`docs/design-docs/architecture-overview.md`](../../design-docs/architecture-overview.md) | 136–155 |
| Hard rule: secure-orch never accepts inbound connections | [`docs/design-docs/architecture-overview.md`](../../design-docs/architecture-overview.md) | 138 |
| Hard rule restated as core belief #4 | [`docs/design-docs/core-beliefs.md`](../../design-docs/core-beliefs.md) | 28–35 |
| Operator-driven 6-step sign cycle | [`docs/design-docs/architecture-overview.md`](../../design-docs/architecture-overview.md) | 140–153 |
| R6 — cold-key-signed advertisements + double-verify | [`docs/design-docs/requirements.md`](../../design-docs/requirements.md) | 58–65 |
| R11 — egress-only secure-orch, friction lives in console UX | [`docs/design-docs/requirements.md`](../../design-docs/requirements.md) | 101–108 |
| Manifest envelope shape (`{manifest, signature}`) | [`livepeer-network-protocol/manifest/schema.json`](../../../livepeer-network-protocol/manifest/schema.json) | 7–13 |
| Signature algorithm = secp256k1 | [`livepeer-network-protocol/manifest/schema.json`](../../../livepeer-network-protocol/manifest/schema.json) | 140–148 |
| Canonicalization = JCS (RFC 8785) | [`livepeer-network-protocol/manifest/schema.json`](../../../livepeer-network-protocol/manifest/schema.json) | 150–155 |

**Hard rule, verbatim** (architecture-overview.md line 138):

> **Hard rule:** secure-orch never accepts inbound connections.

Restated as core-belief #4 (core-beliefs.md lines 28–35):

> The cold key lives on a firewalled `secure-orch` host and never
> crosses a host boundary. It signs every manifest publication.
> **Secure-orch never accepts inbound connections.** Operator drives
> the sign cycle (download candidate → sign → upload signed). No
> automated push for v1. Hand-carry friction is solved in console UX,
> not in the transport.

Anything below that contradicts the hard rule is a bug in this plan and
must be fixed in this plan, not silently relaxed in implementation.

## 3. Threat model

Six attacker shapes worth designing against. Defenses fold into §4–§9.

- **Chain-RPC-only attacker.** Reads on-chain state; cannot alter
  manifest content. Mitigation: signature recovery must match the
  on-chain orch identity ([`livepeer-network-protocol/manifest/README.md`](../../../livepeer-network-protocol/manifest/README.md)
  lines 33–41). Already the design.
- **Broker-host FS access (warm-key compromise).** Attacker owns
  `host-config.yaml`, warm keystore, local TLS cert. Can serve forged
  `/registry/offerings`. **Cannot** sign a new manifest — cold key is
  on a different host. Coordinator will scrape the forged broker and
  build a poisoned candidate; console diff is the catch (§6). Symmetric
  warm-key story on the payment side: plan 0017 + [`payment-daemon/docs/operator-runbook.md`](../../../payment-daemon/docs/operator-runbook.md)
  §5 lines 240–263.
- **secure-orch network reachability.** Should be impossible by the
  hard rule. Defense in depth: default-deny inbound at the host firewall
  (nftables/pf), no listening sockets except `127.0.0.1`, no SSH daemon
  by default, no remote-management agent. Console binds loopback only;
  HSM bus is USB/PCIe, never IP.
- **Coordinator-host compromise (candidate poisoning).** Attacker
  rewrites the candidate (extra capabilities, silent `price` /
  `worker_url` changes). Console diff is the catch. **This is the
  principal reason the sign cycle is operator-driven.** Any
  automation-of-signing proposal must answer: *does this still catch a
  poisoned candidate?* If no, it does not ship.
- **Cold-key compromise.** Game over for this orch's identity until
  rotation (§10). Blast radius is bounded to one orch — no shared
  cold-key infrastructure (no federation in v1, §14). Mitigation: §5
  (HSM-backed, non-extractable) + §10 (rotation).
- **Operator coercion / regulatory action.** Out of architectural scope
  for v1. Operator + secure-orch + cold key are co-located by design;
  physical control of the operator effectively *is* the orch. Documented
  honestly rather than mitigated.

## 4. Manifest packaging format

### 4.1 Canonicalization — JCS (RFC 8785)

Pinned by [`livepeer-network-protocol/manifest/schema.json`](../../../livepeer-network-protocol/manifest/schema.json)
lines 150–155. **Confirmed.** The prior reference impl at
[`livepeer-modules-project/service-registry-daemon/internal/types/canonical.go`](file:///home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/internal/types/canonical.go)
lines 1–127 implements an equivalent procedure — JSON `Marshal` →
decode-as-`any` → sorted-key re-emit, no whitespace. **We port that
algorithm verbatim** to the secure-orch tooling. Bytes-identical
implementations on both sides of sign/verify is a hard requirement.

Open question (§13): keep the bespoke canonicalizer (~140 LoC, zero
deps, fixture-tested) or swap to a maintained third-party JCS library.
Library swap is appealing for surface area but introduces a versioning
surface that bytes-identical guarantees can't tolerate casually.

### 4.2 Signature envelope — embedded (not detached)

The schema puts `signature` inside the top-level envelope alongside
`manifest` ([`schema.json`](../../../livepeer-network-protocol/manifest/schema.json)
lines 7–13). **Embedded, kept that way.** Single-file artifact;
single fetch at `/.well-known/livepeer-registry.json`; single cache
key. The signature is over the canonical bytes of the *inner*
`manifest` only — the outer envelope's `signature` field is zeroed
(not removed) during canonicalization (prior impl line 25 of
`canonical.go`). The inner object is human-readable; that's already
"readable manifest" without going detached.

Detached (`signature.bin` alongside `manifest.json`) was considered
and rejected — the hand-carry workflow doesn't benefit, the verifier
is simpler with one file.

### 4.3 Signature algorithm — secp256k1 ECDSA + EIP-191 personal-sign

Pinned: [`schema.json`](../../../livepeer-network-protocol/manifest/schema.json)
line 142 (`enum: ["secp256k1"]`).

Concrete shape (matches go-livepeer's payment-ticket convention; see
[`livepeer-network-protocol/docs/wire-compat.md`](../../../livepeer-network-protocol/docs/wire-compat.md)
lines 73–91):

1. Compute JCS canonical bytes of the inner `manifest` payload.
2. Apply EIP-191 prefix: `"\x19Ethereum Signed Message:\n" + len(canonical) + canonical`.
3. `keccak256` the prefixed bytes → 32-byte digest.
4. ECDSA-sign the digest with the cold key → 65 bytes `r || s || v`.
5. Normalize `v` to `{27, 28}` on emit (prior impl line 112 of
   `signer.go`).
6. Hex-encode (`0x`-prefixed) into `signature.value`.

The choice of `personal_sign` envelope over plain `keccak256(canonical)`
is **deliberate** and matches both prior reference impl and go-livepeer's
ticket-signing convention. EIP-712 typed-data signing is a candidate v2
upgrade; not for v1 (see prior impl
[`signature-scheme.md` lines 62–64](file:///home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/docs/design-docs/signature-scheme.md)).

### 4.4 Anti-rollback — sidecar with timestamp + monotonic seq

The schema today carries `issued_at` and `expires_at`
([`schema.json`](../../../livepeer-network-protocol/manifest/schema.json)
lines 27–37) inside the signed payload. That's good — `expires_at`
prevents indefinite replay. It is **not** sufficient against rollback
within the validity window (attacker re-serves an old-but-not-yet-expired
manifest after operator pushed a newer one).

**Add a monotonic sequence number** to the inner `manifest` payload (so
it's signed): `manifest.publication_seq` — a non-negative integer that
strictly increases each sign. Resolver caches the last-seen seq per
`eth_address` and rejects manifests with `seq <= last_seen`.

This is a `manifest/schema.json` change owned by
`livepeer-network-protocol`. Plan 0019 *requests* the addition; the
spec repo enacts it via its own SemVer process. Pre-1.0.0, this is a
minor bump.

Out of scope: anchoring the manifest hash on-chain (§14). That's a
real but expensive defense; v2.

## 5. HSM / cold-key storage

Three viable options. Recommendation at the end. None violate the hard
rule provided the HSM is bus-attached (USB / PCIe), not network-attached.

### 5.1 YubiHSM 2 (USB-A, PKCS#11)

- **Pro:** purpose-built secure element. Key generation on-device; key
  never extractable. Tamper-evident hardware. Vendor-supported PKCS#11.
- **Pro:** supports secp256k1 natively (Yubico added the curve in
  firmware 2.2; current stable is 2.5+). Sign-with-secp256k1 is a
  single PKCS#11 call.
- **Pro:** USB-only is bus-local. Does **not** require a network
  presence; plugs into the secure-orch host directly.
- **Con:** PKCS#11 surface is finicky. Operator must install
  `yubihsm-shell` + the connector daemon (locally, on secure-orch). The
  connector is `localhost`-bound by default — verify before deploy.
- **Con:** ~$650 hardware cost. Higher friction for hobbyist orchs.
- **Con:** Yubico's vendor cloud (YubiCloud) is irrelevant here; we use
  the device standalone. Operators must understand that distinction.

**The "network-attached HSM" worry is a non-issue for YubiHSM 2 specifically**
— it is a USB device. The "HSM on a separate machine" framing in the
brief refers to network-HSM offerings (Thales Luna, AWS CloudHSM,
Entrust nShield Connect). Those *do* require network reachability and
*are* incompatible with our hard rule. We do not use them.

### 5.2 Ledger Nano S Plus / X + ledger-app-livepeer

- **Pro:** cheapest option (~$80). USB-C / Bluetooth (we'd disable
  Bluetooth). Hardware confirm button is a delightful UX bonus —
  operator sees the "sign manifest? Y/N" on the device screen.
- **Pro:** secp256k1 native, used universally for Ethereum signing.
- **Con:** **no `ledger-app-livepeer` exists today.** Without it, we'd
  rely on the generic "Sign Personal Message" Ethereum-app flow, which
  shows the operator a SHA-256 of canonical bytes on the tiny screen —
  not human-readable. The diff lives in the console; the device confirm
  is a "I trust the console" tap, not a "I read the manifest" review.
  Acceptable but slightly weaker.
- **Con:** building a custom Ledger app is a 1–2 month job in Rust/C
  with Ledger's review process. Not v1 work.
- **Con:** signing latency is ~1s per sign (USB transport + on-device
  ECDSA). Fine for a once-per-edit operator gesture; not fine for
  batched signing of 100 manifests in a row (we don't do that anyway).

### 5.3 Air-gapped laptop with V3 keystore

- **Pro:** zero special hardware. The prior reference impl's `signer/signer.go`
  ([`signer.go` lines 36–60](file:///home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/internal/providers/signer/signer.go))
  is a working V3-keystore signer we can port verbatim.
- **Pro:** software keystore unlocks via password. Operator workflow is
  familiar (it's just MetaMask without the browser).
- **Con:** software key in process memory. Even on an air-gapped box,
  any local-privilege-escalation bug is a key-extraction bug. The HSM
  options keep the key off-RAM.
- **Con:** hardware fault → key recovery requires the V3 keystore file's
  backup, which is the exact thing operators are bad at. Hardware-backed
  options force a cold-recovery seed-phrase workflow that operators
  understand from MetaMask.

### 5.4 Recommendation

**YubiHSM 2** as the documented default. **Reason:** strongest
key-isolation, no external network, vendor-supported PKCS#11, secp256k1
first-class. Cost is real but bounded ($650 once); orchs that won't pay
that are not the orchs we're optimizing for.

Document the V3-keystore path as a **fallback for non-production /
staging / smoke-test** use, gated behind `--insecure-software-keystore`.
The flag prints a loud warning at boot, mirroring the payment-daemon's
DEV-MODE pattern (see [`payment-daemon/docs/operator-runbook.md`](../../../payment-daemon/docs/operator-runbook.md)
lines 380–390 and 388).

Defer Ledger to v1.1 contingent on `ledger-app-livepeer` actually existing.

Operator-friction tradeoff: YubiHSM has a 30-min one-time setup (install
shell, generate key, set PIN, set audit log) and ~zero per-sign friction.
Ledger has near-zero setup but a per-sign tap. V3 keystore has a
password-prompt per sign and zero hardware. We optimize for the
amortized case (operator signs many manifests over many months); YubiHSM
wins.

## 6. Console UI — the diff-and-sign tool

### 6.1 Tech choice — recommend native + CLI

Three candidates considered:

| Choice | Pros | Cons |
|---|---|---|
| Native (Tauri) | Single binary, small (~10MB), native fonts, no Electron weight, Rust core. | New build dependency in the monorepo. |
| Native (Electron) | Familiar tech, big ecosystem. | ~150MB binary. Chromium attack surface. |
| Web app on `127.0.0.1` only | Easiest dev. | Operator runs a browser on secure-orch; surface area we don't want. |
| CLI only | Smallest surface. | Diff rendering in a terminal is fine for engineers; bad for the operator who's *not* an engineer. |

**Recommend: Tauri-based GUI** as the primary surface, **plus a CLI
mode** for headless / scripted operation (e.g. an operator who SSHes
locally into secure-orch's console — yes, locally — over a serial line
and won't get a window manager).

The CLI is not a "lesser" surface; both call the same internal
`secure-orch-console-core` library. The GUI is the friction-reduction
layer. The CLI is the don't-block-the-operator layer.

Open question (§13): Tauri vs Electron vs Wails. Tauri is the lightest;
Electron the most familiar; Wails (Go) lets us share code with the rest
of the monorepo. Wails is a strong contender if everything else in the
monorepo is Go.

### 6.2 Diff view

What the diff renders, top to bottom:

1. **Header summary.** "X added, Y removed, Z changed." Big number.
2. **Per-tuple diff** — keyed on `(capability_id, offering_id)`:
   - `+ added` — green; show full tuple.
   - `- removed` — red; show full tuple as it was.
   - `~ changed` — yellow; show side-by-side or unified diff of the
     tuple's fields. Heavy emphasis on `price_per_unit_wei`,
     `worker_url`, and `eth_address` changes — these are the spoof
     vectors.
3. **Unchanged tuples** — collapsed by default, expandable. Don't bury
   in noise; don't waste screen real estate on the "everything's fine"
   case.
4. **Out-of-band metadata** — `issued_at`, `expires_at`,
   `publication_seq` (per §4.4), `orch.eth_address`. The seq must
   increase; the eth_address must NOT change (rotation is §10's
   business, not a normal sign cycle's).

Diff renderer is a structural diff against the **last successfully
signed** manifest, which secure-orch keeps a copy of locally
(`/var/lib/secure-orch/last-signed.json`, `0600`). Coordinator's view
is a peer source of truth; secure-orch's view is the authoritative
local one.

### 6.3 Tap-to-sign UX

Hard rules:

- **No auto-sign.** Ever. Even if "looks identical to last manifest" —
  resolver-side replay protection (§4.4) makes a fresh sign cheap, but
  the operator-confirm gesture is not skippable.
- **Hardware confirm where available.** YubiHSM 2 has no button;
  we expose a software "type the orch eth address last 4 hex chars
  to confirm" gesture. Ledger has a button; we use it.
- **Visible signing identity.** Console shows the signer's eth address
  (lower-cased) at the top of the screen during the entire session. If
  the operator ever sees a different address, they panic-quit.
- **Visible HSM connection state.** "HSM connected: yes / no, label X,
  serial Y." If state changes mid-session, the session aborts.

### 6.4 Air-gap workflow inside the console

**The console never opens a network port.** It reads from a local
inbox dir (`/var/spool/secure-orch/inbox/`) populated by whatever the
operator chose (USB, `scp`, local download — see §9). It writes
`signed.json` to a local outbox (`/var/spool/secure-orch/outbox/`),
updates `last-signed.json` atomically (`rename(2)`), and appends to a
rolling audit log (`/var/log/secure-orch/audit.log.jsonl`, mirroring
the prior impl's `audit/` package shape at
[`livepeer-modules-project/service-registry-daemon/internal/repo/audit/`](file:///home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/internal/repo/audit/)).

## 7. Operator workflow — concrete steps

Time budget per step is bounded by *operator-pace*, not machine-pace.

| # | Step | Where | ~Time |
|---|---|---|---|
| 1 | Edit `host-config.yaml` on broker host(s); restart broker. | Broker host | seconds–minutes (operator's editor) |
| 2 | Broker re-publishes its `/registry/offerings` locally. | Broker host | seconds (automatic on restart) |
| 3 | Coordinator scrapes brokers, builds candidate manifest, exposes for download via its operator UI (LAN only). | Coordinator host | seconds–minute |
| 4 | Operator downloads `candidate.json` to a USB stick / scp-target / portable laptop. | Operator's laptop | seconds |
| 5 | Operator brings candidate to secure-orch (physical walk, USB plug, or `scp` if secure-orch's ssh is enabled). | Both | minutes (operator-pace) |
| 6 | Operator opens console; loads candidate; reads diff. | secure-orch | 1–10 min depending on diff size |
| 7 | Operator confirms sign (HSM tap or software-confirm gesture). Cold key signs. | secure-orch | seconds |
| 8 | Console writes `signed.json` to outbox; updates `last-signed.json`; appends audit entry. | secure-orch | seconds |
| 9 | Operator ferries `signed.json` back to coordinator (USB out, scp from a host that can reach coordinator, etc.). | Both | minutes |
| 10 | Operator uploads `signed.json` via coordinator's operator UI. | Coordinator | seconds |
| 11 | Coordinator double-verifies signature, atomic-publishes at `/.well-known/livepeer-registry.json`. | Coordinator | seconds |
| 12 | Resolvers refetch on next interval (or on push); verify; route. | Resolvers | seconds–minutes |

Total operator wall time: **~5–20 min** for a routine repricing edit.
Larger diffs take longer; the bottleneck is operator review, not transport.

### 7.1 Recovery / undo

If the operator signed a wrong candidate but caught the mistake before
upload to coordinator:

- **Discard `signed.json`.** It's just a file; delete it. Nothing has
  shipped yet. Then redo from step 1 with corrected `host-config.yaml`.

If the operator uploaded a wrong signed manifest and it's already live:

- **Sign a new candidate.** Edit broker config back to correct state,
  redo steps 1–11. New manifest's `publication_seq` is higher; resolvers
  pick it up and the wrong one is gone.
- **Do NOT** offer the coordinator a "rollback to previous signed
  manifest" command. That would shortcut the cold-key cycle (see §13's
  open question on this exact point). Even though the previous manifest
  was once cold-signed, re-publishing an *old* signed manifest violates
  monotonicity (§4.4) and would be rejected by resolvers anyway.

If the cold key is suspected compromised mid-cycle:

- Immediate: power off secure-orch. Stop signing.
- Then: §10 escalation flow.

## 8. Verification path

The same canonical-bytes + recover-signer procedure runs at three points.

### 8.1 Coordinator on receipt

Defense-in-depth before atomic-publish. Coordinator runs the same
`verify(canonical, signature) == orch.eth_address` check the resolver
will run. If the operator hand-carried a corrupted file, fail loud
*before* it's served at `/.well-known/livepeer-registry.json`.

### 8.2 Resolver on fetch

Per [`livepeer-network-protocol/manifest/README.md`](../../../livepeer-network-protocol/manifest/README.md)
lines 33–41:

1. Fetch `/.well-known/livepeer-registry.json`.
2. JCS-canonicalize the inner `manifest`.
3. Recover signer from `signature.value` (secp256k1 + EIP-191).
4. Confirm signer == `manifest.orch.eth_address`.
5. Confirm `eth_address` matches the orch's on-chain `ServiceRegistry`
   entry.
6. Confirm `now < expires_at`.
7. Confirm `publication_seq > last_seen[eth_address]` (§4.4 addition).
8. Index capability tuples.

**No caching of unverified bytes.** A failed verify means the manifest
is dropped, the previously-known-good manifest is retained until its
own expiry, and a metric increments
(`livepeer_manifest_verify_total{outcome="signature_mismatch"}` etc).

### 8.3 Gateway on resolver response

The gateway trusts the resolver but is itself a peer of the same chain.
It re-runs the same recover step on resolver-returned bytes. Canonical
bytes are stable; the signature is over them; the check is cheap
(~microseconds).

This is double-verify (§2 R6 pin) — once at the resolver, once at the
gateway. Both must pass.

### 8.4 Public-key publication — recommend on-chain, manifest-embedded

Where does the verifier *get* the orch's public key (or the eth address
that recovers from it)? Three options:

| Option | Pro | Con |
|---|---|---|
| Manifest sidecar (`orch.eth_address` field, already in the schema) | Already there. No extra fetch. | Self-attestation. Verifier still needs to confirm against on-chain authority. |
| On-chain `ServiceRegistry` entry | Authoritative. Already used as the trust anchor. | Requires chain RPC call per orch. Cacheable. |
| `/.well-known/orch-key.json` separate from manifest | Decouples key from manifest. | Adds another fetch, another file, another caching surface, no obvious benefit. |

**Recommend**: keep `orch.eth_address` in the manifest **and** make the
chain `ServiceRegistry` entry the authority. The manifest's claimed
`eth_address` must equal the recovered signer **and** match the chain.
This is exactly the prior reference impl's approach
([`signature-scheme.md` lines 47–60](file:///home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/docs/design-docs/signature-scheme.md)).
Reject `/.well-known/orch-key.json` — it adds a fetch with no security
benefit.

## 9. Air-gap practicalities

### 9.1 Acceptable transports for candidate → secure-orch

In rough order of operator-friction:

1. **USB drive.** Most paranoid operators' default. Plug in, copy file,
   plug out. Console auto-detects mount.
2. **Local LAN download.** Operator's laptop fetches from coordinator's
   LAN web UI; carries the file to secure-orch via USB or `scp`.
3. **`scp` from operator's laptop into secure-orch.** Requires the
   operator to *opt in* to running an SSH daemon on secure-orch — which
   IS an inbound-listener and IS a hard-rule violation unless restricted
   to LAN-only and key-only. See §9.3.
4. **QR code over a camera.** Theoretical for tiny manifests. Rejected
   for v1 — manifests above a handful of capabilities exceed practical
   QR data density.

The console doesn't care which the operator chose — it reads from the
local inbox directory.

### 9.2 Acceptable transports for signed → coordinator

Symmetric. USB out / `scp` from a different host / operator hand-carries
on a laptop. Same mechanics, opposite direction. Console writes to
local outbox; operator does the rest.

### 9.3 The SSH-on-secure-orch question

Pure hard-rule reading: SSH is inbound; no SSH. Pragmatic reading: SSH
on a LAN-only interface, key-only, single authorized key,
`PermitRootLogin no`, `PasswordAuthentication no`, is a small surface
vs "operator walks USBs around forever."

**Default: SSH OFF, USB-only.** Operator may opt in to SSH on LAN-only
with the strict posture above; deployment doc ships the exact
`sshd_config`. The console process *itself* still opens no listening
sockets in either configuration — the SSH question is purely about an
OS-level daemon outside the console. Open question (§13) is whether
even the LAN-SSH exception is too much; if yes, USB-only is the only
blessed path.

### 9.4 Remote operator (no physical access) — not allowed

USB-over-IP RDP forwarding is an inbound RPC and violates the hard
rule. A traveling HSM worsens §3.6 (operator coercion). **Physical
presence at secure-orch is required for a sign cycle in v1.** If
that's not tenable, hire local hands and authorize them on chain (§10)
so they run their own cold key.

## 10. Cold-key escalation flow

**Generation.** New key generated on the secure-orch host (preferably
on-HSM). Key never leaves; public key (eth address) is exported via
the console (clipboard / file / QR — operator's choice).

**On-chain authorization.** The orch's on-chain identity is the
BondingManager-bonded transcoder address. To rotate the signing
identity, the operator calls the chain authorization function from
the OLD cold key (per
[`payment-daemon/docs/operator-runbook.md`](../../../payment-daemon/docs/operator-runbook.md)
lines 254–263, the warm-key analogue):
`BondingManager.setSigningAddress` or its protocol-equivalent. This
plan does not redefine that function — it inherits whatever plan 0017
+ the chain layer settle on. The OLD key authorizing the NEW key is
"cold key signing its own succession" — irreversible, deliberate.

If the OLD cold key is **lost** (HSM brick, forgotten PIN, hardware
fault): the orch's on-chain identity is orphaned. Recovery requires
protocol-governance coordination to migrate to a new orchestrator
entry. **No automated recovery.**

**Migration window — hard cutover.** New key signs; old key stops;
resolvers see the on-chain `setSigningAddress` and update their
expected signer; one manifest re-issuance under the new key. Both-keys
simultaneously was considered (zero downtime, doubled trust surface
during the window) and rejected — operator-paced re-publish makes a
window unnecessary. If the operator wants to stage during low-traffic
hours, that's their call; architecture doesn't help.

## 11. Component layout

Proposed `secure-orch-console/` directory under monorepo root:

```
secure-orch-console/
  AGENTS.md, CLAUDE.md, README.md, DESIGN.md
  Makefile, Dockerfile, compose.yaml          # standard component shape
  cmd/
    secure-orch-console/                      # main binary (GUI + CLI modes)
    secure-orch-keygen/                       # cold-key generation helper
  internal/
    canonical/    # JCS canonicalization, ported from prior impl
    signing/      # Signer iface + YubiHSM impl + V3-keystore fallback
    diff/         # candidate-vs-last-signed structural diff
    audit/        # rolling JSONL audit log
    inbox/        # spool-dir watcher
    outbox/       # signed-manifest writer
    config/       # operator config (drop dirs, HSM connection, etc.)
  ui/             # GUI assets (Tauri/Wails frontend, embedded)
  docs/
    operator-runbook.md, threat-model.md, hsm-setup-yubihsm.md
    design-docs/, exec-plans/
  testdata/       # canonical-bytes fixtures for round-trip tests
```

The runbook ports from
[`livepeer-modules-project/service-registry-daemon/docs/operations/running-the-daemon.md`](file:///home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/docs/operations/running-the-daemon.md).

## 12. Migration sequence — recommended commit cadence

5–7 commits, ordered to land incremental verifiable surface area:

1. **Commit 1 — manifest packaging spec freeze.** In
   `livepeer-network-protocol/manifest/`: bump schema to add
   `publication_seq`. Update `README.md`'s verification flow to include
   the seq check. Bump `VERSION`. Update example. Pure-spec commit.
2. **Commit 2 — canonicalization + signing primitives (library).**
   New `secure-orch-console/internal/canonical/` and
   `internal/signing/`. Port from prior impl
   `livepeer-modules-project/service-registry-daemon/internal/types/canonical.go`
   and `internal/providers/signer/signer.go` verbatim (with attribution
   in the commit message per
   [`AGENTS.md`](../../../AGENTS.md) lines 62–66). V3-keystore signer
   only. No HSM yet. No console yet. Round-trip tests vs canonical
   fixtures.
3. **Commit 3 — verification primitives.** New
   `secure-orch-console/internal/verify/` (or, more likely, a verifier
   package under `livepeer-network-protocol/` since it's reusable by
   coordinator + resolver + gateway). Port from
   `livepeer-modules-project/service-registry-daemon/internal/providers/verifier/verifier.go`.
   Tests against the same canonical fixtures.
4. **Commit 4 — console scaffold + CLI mode.** `cmd/secure-orch-console`
   binary; CLI subcommands `load`, `diff`, `sign`, `audit-tail`. No
   GUI yet. Inbox/outbox/audit packages stand up.
5. **Commit 5 — GUI shell.** Tauri (or chosen tech) with the diff view
   and tap-to-sign UX. Same `internal/` packages backing it.
6. **Commit 6 — HSM integration (YubiHSM 2 PKCS#11).** Adds an
   `internal/signing/yubihsm/` impl behind the same `Signer` interface.
   `--insecure-software-keystore` flag added to keep the V3-keystore
   path available with a warning.
7. **Commit 7 — air-gap workflow polish.** USB auto-detect, file picker
   GUI, audit-log rotation, deployment doc.

Each commit lands the smallest verifiable thing. Each can be reverted
without stranding the others.

## 13. Open questions for the user

Real, load-bearing decisions. Marked **DECIDE BEFORE CODE**.

1. **HSM hardware default — YubiHSM 2 vs Ledger vs V3 keystore?** The
   plan recommends YubiHSM 2 (§5.4). Ledger is friendlier but blocked
   on `ledger-app-livepeer`. V3 keystore is the fallback. Confirm or
   override.
2. **Console GUI tech — Tauri vs Wails vs Electron vs CLI-only?** Plan
   recommends Tauri primary + CLI mode (§6.1). Wails is appealing if
   we want to keep the entire monorepo in Go. CLI-only is the
   minimum-surface-area option. Decide.
3. **Public-key publication — manifest-embedded + on-chain only, or add
   `/.well-known/orch-key.json`?** Plan rejects the well-known file
   (§8.4). Confirm.
4. **Canonicalization — port the prior bespoke canonicalizer or adopt a
   maintained JCS library?** Plan recommends port (§4.1) for
   bytes-identical guarantees and zero-dep simplicity. Library swap is
   a future option.
5. **Rollback policy — confirm no coordinator-side "rollback" command?**
   Plan rejects it (§7.1). Operators rolling back must sign a new
   candidate that reverts the change. Confirm.
6. **SSH on secure-orch's LAN interface — allowed or never?** Plan
   recommends opt-in LAN-only with strict posture (§9.3). Strict
   reading of the hard rule says no. Decide.
7. **`publication_seq` field addition to manifest schema.** Requires a
   `livepeer-network-protocol` minor bump pre-1.0.0. Confirm we're OK
   with that, or accept rollback exposure within the validity window
   (§4.4) for v1.
8. **Component name — `secure-orch-console` (planned in
   [`AGENTS.md`](../../../AGENTS.md) line 46) vs something shorter?**
   Plan keeps the planned name. Confirm.

## 14. Out of scope

- **Multi-operator federation.** Sharing a cold key, threshold signing
  across operators, multisig manifest publication. v2 or never.
- **On-chain manifest hashes.** Anchoring `keccak256(canonical)` in a
  contract on every sign would give us replay/rollback protection from
  the chain itself. Real defense; expensive (gas per publish); v2.
- **Manifest-signing automation.** Forbidden by the hard rule. Any
  proposal to "let secure-orch sign automatically when a candidate
  arrives" violates §2 and is dead.
- **EIP-712 typed-data signatures.** v2 candidate, see §4.3.
- **Ledger app development.** Out for v1; revisit when an operator
  funds the ledger-app-livepeer work.
- **Protocol-governance recovery for lost cold keys.** §10 names this;
  it's a governance question, not an architecture one.
- **Per-component metrics aggregation.** Components expose Prometheus;
  third parties aggregate (core-belief #9). Same model here.
- **Hot/cold delegation in the manifest** (the prior impl's
  `extra.delegation` pattern, [`signature-scheme.md` lines 72–80](file:///home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/docs/design-docs/signature-scheme.md)).
  v1 uses cold-key-only signing of manifests. Plan 0017 owns the
  warm-key story for *tickets*; this plan does not introduce a warm-key
  story for *manifests*.

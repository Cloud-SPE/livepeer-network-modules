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
- The **`secure-orch-console`** — diff-and-sign UX. Localhost-bound
  web UI on secure-orch; operator accesses it from a LAN laptop via
  `ssh -L`-tunneled port-forward (§6.1).
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
  hard rule. The application contract holds it: console binds
  `127.0.0.1` only; HSM bus is USB/PCIe, never IP. Host-level posture
  (firewall, sshd, remote-management agents) is operator deployment
  choice — see §13 Q6 and §9.3 for non-prescriptive guidance.
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

Locked (§13 Q4): port the bespoke canonicalizer verbatim (~140 LoC,
zero deps, fixture-tested). Library swap was rejected — bytes-
identical guarantees can't tolerate a casual versioning surface.

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

Three options, in order of v1 status: V3 JSON keystore (primary),
YubiHSM 2 (optional hardening), Ledger (deferred). None violate the
hard rule provided any hardware is bus-attached (USB / PCIe), never
network-attached.

### 5.1 V3 JSON keystore (primary)

- **Pro:** zero special hardware. The prior reference impl's
  `signer/signer.go`
  ([`signer.go` lines 36–60](file:///home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/internal/providers/signer/signer.go))
  is a working V3-keystore signer we port verbatim.
- **Pro:** matches go-livepeer + livepeer-network-suite operator muscle
  memory. The V3 JSON keystore is the same artifact operators already
  manage for their bonded transcoder address; this is not a new
  storage concept being introduced for cold-key signing.
- **Pro:** software keystore unlocks via password. Operator workflow is
  familiar (it's MetaMask without the browser).
- **Pro:** recovery is mechanical — the V3 keystore file plus its
  password is the whole story. Backup is a copy.
- **Con:** software key in process memory during sign. Local-privilege-
  escalation on secure-orch is a key-extraction risk. The hardening
  upgrade in §5.2 is the answer for operators who want key-off-RAM.

Selected via `--keystore=v3:<path>` (default).

### 5.2 YubiHSM 2 (optional hardening upgrade, USB-A, PKCS#11)

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
- **Con:** ~$650 hardware cost + ~30-min one-time setup (install
  shell, generate key, set PIN, set audit log). Higher friction for
  hobbyist orchs.
- **Con:** Yubico's vendor cloud (YubiCloud) is irrelevant here; we use
  the device standalone. Operators must understand that distinction.

**The "network-attached HSM" worry is a non-issue for YubiHSM 2 specifically**
— it is a USB device. The "HSM on a separate machine" framing in the
brief refers to network-HSM offerings (Thales Luna, AWS CloudHSM,
Entrust nShield Connect). Those *do* require network reachability and
*are* incompatible with our hard rule. We do not use them.

Selected via `--keystore=yubihsm:<connector-url>` alongside (replacing)
the default `--keystore=v3:<path>`. Same `Signer` interface either way.

### 5.3 Ledger Nano S Plus / X + ledger-app-livepeer (deferred to v1.1+)

- **Pro:** cheapest hardware option (~$80). USB-C / Bluetooth (we'd
  disable Bluetooth). Hardware confirm button is a delightful UX bonus
  — operator sees the "sign manifest? Y/N" on the device screen.
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

Deferred to v1.1+ contingent on `ledger-app-livepeer` actually existing.

### 5.4 Recommendation

**V3 keystore is the documented default; YubiHSM 2 is an optional
hardening upgrade.** Reason for V3-as-primary: alignment with
go-livepeer + livepeer-network-suite — operators already understand
this storage shape from their bonded transcoder address, and the prior
reference impl is a verbatim port. YubiHSM 2 is documented for
operators who want stronger key isolation; it sits behind the same
`Signer` interface and is selected via `--keystore=yubihsm:<connector-url>`
in place of the default `--keystore=v3:<path>`.

Operator-friction tradeoff: V3 keystore has a password-prompt per sign
and zero hardware (the baseline most orchs already understand from
MetaMask). YubiHSM 2 has a $650 + ~30-min one-time setup (install
shell, generate key, set PIN, set audit log) and ~zero per-sign
friction, with stronger key isolation. Ledger has near-zero setup but
a per-sign tap, deferred until `ledger-app-livepeer` exists.

V3 has no warning flag and no "non-production" framing — it is the
baseline (Q1 / §13).

## 6. Console UI — the diff-and-sign tool

### 6.1 Surface — localhost-bound web UI

**Primary surface: localhost-bound web UI**, accessed by the operator
from a LAN laptop via `ssh -L`-tunneled port-forward into secure-orch's
loopback. Secure-orch hosts the web server on `127.0.0.1:<port>` only;
the operator runs `ssh -L 8080:127.0.0.1:8080 secure-orch` from their
LAN laptop and points a browser at `http://localhost:8080`. The
browser, the diff renderer, and any operator-facing input all live on
the laptop; the cold key, signer, inbox, outbox, and audit log all
live on secure-orch.

**No CLI as a parallel surface in v1.** CLI mode is deferred or never
implemented — the diff is the load-bearing review surface and a
terminal renderer makes that worse for operators who aren't engineers.
**Native shells (deferred to a future plan).** The web stack itself is
unspecified for v1: the operator runs whatever the
`secure-orch-console` binary embeds (likely a small Go HTTP server with
embedded static assets — see §11). No frontend build dependency in the
monorepo for v1.

#### 6.1.1 Why ssh-L tunneling preserves the hard rule

The hard rule is "secure-orch never accepts inbound connections" at
the **application layer** of this plan: the console + web UI bind
`127.0.0.1` only and have no listener on a routable interface. That
contract holds with `ssh -L`:

- The web server binds `127.0.0.1:<port>` on secure-orch — never a
  routable interface. Anything trying to reach it from off-host hits
  no listener.
- The SSH tunnel terminates **inside** secure-orch's loopback. The
  laptop's `ssh -L 8080:127.0.0.1:8080 secure-orch` causes sshd on
  secure-orch to open a connection from sshd's own process to
  `127.0.0.1:8080` — a loopback connection — and forward bytes over
  the existing SSH session.
- The inbound TCP that arrives at secure-orch is to **sshd** (the
  operator's deployment-choice OS daemon, not part of console). After
  sshd hands off to its forwarding child, the connection from that
  child to the web UI is loopback-only.
- The console application contract — "no listener on routable
  interfaces" — holds unconditionally, regardless of whether the
  operator runs sshd, what posture they run it under, or whether they
  use USB instead.

Whether sshd runs at all, on which interface, with what auth posture,
is a deployment-layer choice (cross-reference §9.3 / Q6). The console
binary doesn't care.

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
  to confirm" gesture (web-form input in the browser-rendered confirm
  dialog). Ledger has a button; we use it when Ledger ships (§5.3).
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
  manifest" command. That would shortcut the cold-key cycle (locked,
  §13 Q5). Even though the previous manifest was once cold-signed,
  re-publishing an *old* signed manifest violates monotonicity (§4.4)
  and would be rejected by resolvers anyway.

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
   operator to opt in to running an SSH daemon on secure-orch at the
   OS layer; that's a deployment-posture choice (§9.3 / Q6). The
   application-layer hard rule is unaffected — console + web UI still
   bind loopback only.
4. **QR code over a camera.** Theoretical for tiny manifests. Rejected
   for v1 — manifests above a handful of capabilities exceed practical
   QR data density.

The console doesn't care which the operator chose — it reads from the
local inbox directory.

### 9.2 Acceptable transports for signed → coordinator

Symmetric. USB out / `scp` from a different host / operator hand-carries
on a laptop. Same mechanics, opposite direction. Console writes to
local outbox; operator does the rest.

### 9.3 Operator deployment posture

What sshd / firewall / VPN posture the operator runs on secure-orch is
a **deployment-level choice** — out of scope for this plan. The hard
rule is enforced at the **application layer**: the console + web UI
bind `127.0.0.1` only, with no listener on a routable interface
(§6.1.1). That contract holds regardless of OS-daemon posture.

Runbook guidance (non-prescriptive): don't expose secure-orch to
anything but your LAN; prefer key-only SSH if you enable it. One
valid posture is LAN-only + SSH key + password. Another valid
posture is USB-only with sshd off entirely. The console binary
doesn't care. Decided on 2026-05-06 (Q6 / §13).

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
    secure-orch-console/                      # main binary (web-server entrypoint)
    secure-orch-keygen/                       # cold-key generation helper
  internal/
    canonical/    # JCS canonicalization, ported from prior impl
    signing/      # Signer iface + V3-keystore (primary) + YubiHSM impl
    diff/         # candidate-vs-last-signed structural diff
    audit/        # rolling JSONL audit log
    inbox/        # spool-dir watcher
    outbox/       # signed-manifest writer
    config/       # operator config (drop dirs, HSM connection, etc.)
  web/            # Go HTTP server source + embedded static assets
                  # (HTML/CSS/JS for diff + sign forms; localhost-only;
                  # binary embeds it)
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
4. **Commit 4 — console binary scaffold (web server entrypoint).**
   `cmd/secure-orch-console` binary stands up the Go HTTP server bound
   to `127.0.0.1` only; routes are stubbed. Inbox/outbox/audit
   packages stand up. V3-keystore signer wired through. No web
   templates yet.
5. **Commit 5 — web UI (Go HTTP server + embedded HTML/CSS/JS for
   diff + sign).** `web/` package: handler set, templates for diff
   view (§6.2) and tap-to-sign confirm (§6.3). Static assets embedded
   via `embed.FS`. Same `internal/` packages backing it.
6. **Commit 6 — HSM integration (YubiHSM 2 PKCS#11).** Adds a YubiHSM
   2 PKCS#11 signer behind the same `Signer` interface as the V3
   keystore; both selectable via `--keystore=v3:<path>` (default) or
   `--keystore=yubihsm:<connector-url>`.
7. **Commit 7 — air-gap workflow polish.** USB auto-detect, file picker
   in the web UI, audit-log rotation, deployment doc.

Each commit lands the smallest verifiable thing. Each can be reverted
without stranding the others.

## 13. Resolved decisions

All eight open questions were resolved on 2026-05-06. The implementing
agent works against these locks; rationale captured for future readers.

### Q1. HSM hardware default

**DECIDED: V3 JSON keystore is the primary signing path; YubiHSM 2 is
an optional hardening upgrade; Ledger deferred to v1.1+.** Reason for
V3-as-primary: alignment with go-livepeer + livepeer-network-suite
operator muscle memory — operators already understand V3 keystores
from their bonded transcoder address, and the prior reference impl is
a verbatim port (§5.1). YubiHSM 2 sits behind the same `Signer`
interface for operators who want stronger key isolation; selected via
`--keystore=yubihsm:<connector-url>` alongside the default
`--keystore=v3:<path>` (§5.2). Ledger waits on `ledger-app-livepeer`
existing (§5.3). V3 is the baseline, not "insecure" — no warning
flag, no "non-production" framing.

### Q2. Console UI tech

**DECIDED: localhost-bound web UI is the primary surface; CLI deferred
or never; native shells deferred to a future plan.** Secure-orch hosts
a web app bound to `127.0.0.1` only; operators access it from a LAN
laptop via `ssh -L 8080:127.0.0.1:8080 secure-orch`. The hard rule
(no inbound to secure-orch) is enforced at the application layer —
the web server never binds a routable interface; the SSH tunnel
terminates inside secure-orch's loopback (§6.1.1). Web stack itself
is unspecified for v1 — likely a small Go HTTP server with embedded
static assets.

### Q3. Public-key publication

**DECIDED: manifest-embedded `orch.eth_address` + on-chain
`ServiceRegistry` authority; no `/.well-known/orch-key.json`.** Adding
a separate well-known file is a fetch surface with no security
benefit; the manifest's claimed `eth_address` must equal the recovered
signer **and** match the chain (§8.4). This is exactly the prior
reference impl's approach.

### Q4. Canonicalization

**DECIDED: port the prior bespoke canonicalizer verbatim.** ~140 LoC,
zero dependencies, fixture-tested, sourced from
[`livepeer-modules-project/service-registry-daemon/internal/types/canonical.go`](file:///home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/internal/types/canonical.go)
lines 1–127. Reject the third-party JCS library swap — bytes-identical
guarantees on both sides of sign/verify can't tolerate a casual
versioning surface (§4.1).

### Q5. Rollback policy

**DECIDED: no coordinator-side rollback command.** Operators rolling
back must sign a new candidate that reverts the change. Re-publishing
an old signed manifest violates monotonicity (§4.4 `publication_seq`)
and would be rejected by resolvers anyway (§7.1).

### Q6. SSH on secure-orch

**DECIDED: deployment-level — out of scope for this plan.** The hard
rule is enforced at the **application layer**: console + web UI bind
`127.0.0.1` only, no listener on routable interfaces. What sshd /
firewall / VPN posture the operator runs at the OS layer is their
deployment choice. Runbook documents the constraint ("don't expose
secure-orch to anything but your LAN; prefer key-only SSH if enabled")
and gives example postures (LAN + key + password is one valid posture)
without prescribing one (§9.3).

### Q7. `publication_seq` in manifest schema

**DECIDED: confirmed addition.** Pre-1.0.0 minor bump in
`livepeer-network-protocol` per its own SemVer process. Plan 0019
*requests*; the spec repo enacts (§4.4).

### Q8. Component name

**DECIDED: keep `secure-orch-console`.** Matches the name planned in
[`AGENTS.md`](../../../AGENTS.md) line 46. No shorter alternative on
offer worth the rename churn.

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

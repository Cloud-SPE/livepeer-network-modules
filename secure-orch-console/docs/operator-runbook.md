# Operator runbook — secure-orch-console

The cold-key operator's reference for booting the console, reviewing a
candidate manifest, signing it, and recovering from common failure
modes.

Adapted from the prior reference impl
[`service-registry-daemon/docs/operations/running-the-daemon.md`](file:///home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/docs/operations/running-the-daemon.md);
the suite daemon ran in publisher mode on the secure-orch host. The
console replaces that publisher surface with a diff-and-sign UX, but
the keystore concept is unchanged.

## Hard rule

**secure-orch never accepts inbound connections.** The console binds
`127.0.0.1` only. Operators access it via
`ssh -L 8080:127.0.0.1:8080 secure-orch` from a LAN laptop. Whether
sshd runs at all, on which interface, with what auth posture, is a
deployment-layer choice (plan 0019 §9.3 / §13 Q6).

## Status

This runbook lands incrementally as the console comes together:

- Commit 2 (this commit) — keystore, canonicalization, signing
  primitives. No binary yet. This document captures the operator
  surface that arrives in commits 4–7.
- Commit 4 — the binary stands up; flag surface settles below.
- Commit 5 — the web UI's diff and tap-to-sign gestures arrive.
- Commit 6 — the YubiHSM 2 keystore option lights up.
- Commit 7 — USB auto-detect + audit-log rotation polish.

## Boot

```sh
secure-orch-console \
  --keystore=v3:/var/lib/secure-orch/keystore.json \
  --keystore-password-file=/etc/secure-orch/password \
  --inbox=/var/spool/secure-orch/inbox \
  --outbox=/var/spool/secure-orch/outbox \
  --last-signed=/var/lib/secure-orch/last-signed.json \
  --audit-log=/var/log/secure-orch/audit.log.jsonl \
  --listen=127.0.0.1:8080
```

The default keystore selector is `v3:<path>`; YubiHSM 2 lights up in
commit 6 via `--keystore=yubihsm:<connector-url>`. Both selectors sit
behind the same `Signer` interface.

## Sign cycle

1. Operator drops a `candidate.json` into the inbox directory.
2. Operator opens `http://localhost:8080` from a LAN laptop via
   `ssh -L 8080:127.0.0.1:8080 secure-orch`.
3. The console renders the structural diff against `last-signed.json`.
4. Operator confirms via the tap-to-sign gesture (challenge: type the
   orch eth address's last four hex chars).
5. Console writes the signed envelope to the outbox and updates
   `last-signed.json` atomically.
6. Operator copies the signed envelope back out (USB, scp, …).

## Failure modes

(Lands in commit 4; the bullet list above is the boot-loop surface.
The "what happens when X" matrix lives in commit 4's runbook update,
once the failure surface is observable.)

## Cold-key rotation

(Lands as part of commit 4 + commit 6 documentation. The chain side is
plan 0017's territory — `BondingManager.setSigningAddress` or its
protocol equivalent. The console emits the new public key via the
clipboard / file / QR; the cold key rotation is a hard-cutover per
plan 0019 §10.)

## Cold-key escalation

If the cold key is suspected compromised mid-session: power off
secure-orch immediately. Then:

1. Authorize a new key on chain (plan 0019 §10 — irreversible, signed
   by the OLD cold key).
2. Generate a fresh cold key with `secure-orch-keygen` on the new
   secure-orch host.
3. Re-issue the manifest under the new key.

If the OLD key is **lost**, the orch's on-chain identity is orphaned;
recovery is a protocol-governance question (plan 0019 §10 calls this
out as out-of-scope for the architecture).

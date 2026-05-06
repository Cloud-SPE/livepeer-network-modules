# DESIGN — secure-orch-console

## Why a separate component

`secure-orch` is the cold-key host. The console is the only software
the operator runs there directly. Co-locating the diff renderer, the
signer, the audit log, and the spool-dir watchers in one binary keeps
the trust boundary minimal: one process, one binary, one running user.

The verifier is split out into
[`../livepeer-network-protocol/verify/`](../livepeer-network-protocol/verify/)
because the recovery path is shared by every consumer of a signed
manifest — resolver, coordinator, gateway. The signer is not shared;
only secure-orch produces signatures.

## Boundaries

- **Inbound:** none on a routable interface, ever. The HTTP server
  binds `127.0.0.1` only and is reached over `ssh -L` (plan 0019
  §6.1.1).
- **Outbound:** none. The console reads the inbox spool directory and
  writes the outbox spool directory; operators move bytes in and out
  by their own transport (USB, scp, …).
- **Storage:** a single `last-signed.json` (`0600`) plus a rolling
  JSONL audit log under `/var/log/secure-orch/audit.log.jsonl`.
- **Cold key:** held by the configured `Signer`. V3 keystore decrypts
  into process memory at boot; YubiHSM 2 (commit 6) keeps the key on
  the secure element, addressed via PKCS#11.

## Sign cycle

The operator-driven 6-step cycle (plan 0019 §7) is the only path that
emits a signature:

1. Operator copies a candidate manifest into the inbox.
2. Console renders the structural diff against `last-signed.json`.
3. Operator confirms via the tap-to-sign gesture (`Sign` button + a
   challenge: type the orch eth address's last four hex chars).
4. The configured `Signer` produces a 65-byte secp256k1 signature.
5. Console writes the signed envelope to the outbox and atomically
   updates `last-signed.json`.
6. Operator ferries the outbox file out to the coordinator.

No automation. No daemon. The console has no scheduler.

## Canonicalization

JCS-equivalent (RFC 8785). Implementation is a verbatim port from the
prior reference impl, fixture-tested at commit 2; the spec lock at
plan 0019 §4.1 + §13 Q4 forbids swapping in a third-party library
because bytes-identical guarantees on both sides of sign/verify can't
tolerate a casual versioning surface.

## Signature shape

secp256k1 ECDSA over the EIP-191 personal-sign envelope of the
canonical manifest bytes; `v` normalized to `{27, 28}` (plan 0019
§4.3). Symmetrically recovered by
[`../livepeer-network-protocol/verify/`](../livepeer-network-protocol/verify/).

## Audit log

`/var/log/secure-orch/audit.log.jsonl`, append-only, rolled by size.
Schema: one JSON object per line with `at` (RFC3339Nano UTC), `kind`
(load_candidate / view_diff / sign / write_outbox / abort), and
event-specific fields. Storage shape mirrors the prior impl's
`audit/` package (operator muscle memory, simple format).

# DESIGN — secure-orch-console

## Why a separate component

`secure-orch` is the cold-key host. The console is the only software
the operator runs there directly. Co-locating the diff renderer, the
signer, the audit log, and the web upload handler in one binary keeps
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
- **Outbound:** none. The console only reads / writes its own local
  filesystem.
- **Manifest transport:** HTTP-only via the web UI. Operator uploads
  candidate manifests through a multipart form; signed envelopes are
  returned as download attachments and mirrored to
  `/var/lib/secure-orch/last-signed.json` (`0600`) atomically via
  `rename(2)`.
- **Storage:** a single `last-signed.json` plus a rolling JSONL audit
  log under `/var/log/secure-orch/audit.log.jsonl` with size-based
  rotation.
- **Cold key:** held by the configured `Signer`. v0.1 ships V3
  keystore only (decrypted into process memory at boot, zeroed on
  shutdown).

## Sign cycle

The operator-driven cycle (plan 0019 §7) is the only path that emits
a signature:

1. Operator uploads a candidate manifest via the web form.
2. Console renders the structural diff against `last-signed.json`.
3. Operator confirms by typing the last 4 hex chars of the signer
   eth address.
4. The configured `Signer` produces a 65-byte secp256k1 signature.
5. Console writes the envelope to `last-signed.json` atomically and
   returns it to the operator as a download.
6. Operator uploads the signed envelope to the coordinator.

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

`/var/log/secure-orch/audit.log.jsonl`, append-only, rolled by size
(default 100 MiB; configurable via `--audit-rotate-size`). Schema:
one JSON object per line with `at` (RFC3339Nano UTC), `kind`
(`load_candidate` / `view_diff` / `sign` / `write_signed` / `abort` /
`boot` / `shutdown` / `rotate`), and event-specific fields. Rotation
renames the active file with a timestamp suffix and writes a
`rotate` marker into the new file.

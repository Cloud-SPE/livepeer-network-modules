# Operator runbook — secure-orch-console

The cold-key operator's reference for booting the console, reviewing a
candidate manifest, signing it, and recovering from common failure
modes.

Adapted from the prior reference impl
[`service-registry-daemon/docs/operations/running-the-daemon.md`](file:///home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/service-registry-daemon/docs/operations/running-the-daemon.md);
the suite daemon ran in publisher mode on the secure-orch host. The
console replaces that publisher surface with a diff-and-sign UX, but
the keystore concept is unchanged.

## Bind posture

Loopback-only remains the recommended deployment posture. The console
now accepts any explicit `host:port` in `--listen`, so operators can
still use `ssh -L 8080:127.0.0.1:8080 secure-orch` or deliberately
bind a broader interface if their environment requires it. Whether
sshd runs at all, on which interface, with what auth posture, is a
deployment-layer choice (plan 0019 §9.3 / §13 Q6).

If `SECURE_ORCH_ADMIN_TOKENS` is configured, the operator must log in
with an admin token and actor string before using the UI. The actor is
recorded into audit events. The UI permits one active operator session
at a time, with a 12-hour absolute timeout and a 30-minute idle
timeout. Expired sessions are released automatically on the next
request or login attempt.

## Scope (v0.1)

- **Storage:** V3 JSON keystore only. Hardware-backed signers
  (YubiHSM 2, Ledger, PKCS#11) are out of scope per plan 0019 §13 Q1
  + §14.
- **Manifest transport:** HTTP only via the web UI. No inbox / outbox
  spool, no filesystem watcher, no USB. The web UI handles candidate
  upload (multipart form) and signed-envelope download (HTTP
  response) inline.
- **Network posture:** mainnet only — no Livepeer testnets.

## Boot

```sh
secure-orch-console \
  --keystore=v3:/var/lib/secure-orch/keystore.json \
  --keystore-password-file=/etc/secure-orch/password \
  --last-signed=/var/lib/secure-orch/last-signed.json \
  --audit-log=/var/log/secure-orch/audit.log.jsonl \
  --audit-rotate-size=104857600 \
  --listen=127.0.0.1:8080
```

The keystore selector is `v3:<path>`. The password is read from a file
to avoid TTY-echo footguns; alternatively set
`LIVEPEER_KEYSTORE_PASSWORD` in the environment. The console refuses
to start if `--listen` is not an explicit `host:port`.

## Sign cycle

1. Coordinator builds a candidate manifest and exposes it for download
   on the LAN. Operator downloads it on their laptop.
2. Operator opens `http://localhost:8080` from the laptop via
   `ssh -L 8080:127.0.0.1:8080 secure-orch`.
3. Operator uploads the candidate (the inner manifest JSON, or a
   tarball containing `manifest.json` + `metadata.json`) via the
   `Upload candidate manifest` form.
4. Console renders the structural diff against `last-signed.json`.
   Header summary surfaces `publication_seq` monotonicity and
   `orch.eth_address` stability; per-tuple diff highlights
   `price_per_unit_wei` / `worker_url` changes.
5. Operator types the last 4 hex chars of the signer eth address into
   the confirm input and submits the sign form.
6. Console signs the canonical bytes, atomically updates
   `last-signed.json`, and streams `signed.json` back as a download
   attachment.
7. Operator uploads `signed.json` to the coordinator's web UI;
   coordinator double-verifies, then publishes at
   `/.well-known/livepeer-registry.json`.

## Recovery

If the operator signed a wrong candidate but caught the mistake before
upload to the coordinator:

- Discard `signed.json`. Nothing has shipped yet. Edit broker config,
  redo the cycle. The discarded `signed.json` carries the new
  `publication_seq`; resolvers won't pick it up because it never
  reached the coordinator's well-known path.

If a wrong signed manifest is already live:

- Sign a new candidate that reverts the change. Re-publishing the old
  signed manifest is rejected by resolvers (monotonicity, plan 0019
  §4.4).

If the cold key is suspected compromised mid-session:

- Power off secure-orch immediately.
- Authorize a new key on chain (plan 0019 §10 — the OLD cold key
  signs `BondingManager.setSigningAddress` or its protocol
  equivalent).
- Generate a fresh cold key with `secure-orch-keygen` on the new
  secure-orch host. Re-issue the manifest under the new key.

If the OLD cold key is **lost** (host disk failure, forgotten
password, V3 keystore corruption): the orch's on-chain identity is
orphaned. Recovery requires protocol-governance coordination —
plan 0019 §10 marks this out of architectural scope.

## Audit log

`/var/log/secure-orch/audit.log.jsonl` is append-only. Every gesture
(`boot`, `load_candidate`, `view_diff`, `sign`, `write_signed`,
`abort`, `rotate`, `shutdown`) emits one JSON object on its own line.
Rotation is size-based (default 100 MiB). When the log crosses the
threshold the active file is renamed to
`audit.log.jsonl.<UTC-timestamp>` and a fresh file is opened with a
`rotate` marker as its first record. Rotated files are retained on
disk; the operator handles offsite backup.

## Cold-key rotation

The chain side is plan 0017's territory —
`BondingManager.setSigningAddress` or its protocol equivalent. The
console emits the new public key by printing the eth address from
`secure-orch-keygen`; rotation is a hard cutover (plan 0019 §10).

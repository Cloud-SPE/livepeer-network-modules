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
at a time, with a 4-hour absolute timeout and a 30-minute idle
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
  --coordinator-url=http://10.0.0.5:8080 \
  --listen=127.0.0.1:8080
```

The keystore selector is `v3:<path>`. The password is read from a file
to avoid TTY-echo footguns; alternatively set
`LIVEPEER_KEYSTORE_PASSWORD` in the environment. The console refuses
to start if `--listen` is not an explicit `host:port`. `--coordinator-url`
is optional; when set, the review checklist can jump straight back to the
matching coordinator timeline section during the hand-carry cycle.

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

## Agent mode (plan 0042) — automated sign cycle

`--agent` replaces the hand-carry loop above with an outbound-only
agent: the console pulls candidates from the coordinator, classifies
them against your sign policy, auto-signs inside the policy envelope,
holds everything else for your review, and pushes signed manifests
back. The hand-carry flow stays available as the fallback.

### Boot

```
secure-orch-console \
  --keystore=v3:/etc/secure-orch/cold.json \
  --keystore-password-file=/etc/secure-orch/cold.pass \
  --agent \
  --coordinator-url=https://coordinator.lan:8080 \
  --coordinator-public-url=https://coordinator.example.net:8081 \
  --coordinator-token-file=/etc/secure-orch/agent-token \
  --agent-policy=/etc/secure-orch/sign-policy.json \
  --alert-webhook-url=https://hooks.example.net/secure-orch
```

- `--coordinator-url` is the coordinator's admin listener (candidate
  pull + signed-manifest push). The token in
  `--coordinator-token-file` must match the coordinator's
  `--agent-token-file`.
- `--coordinator-public-url` is the resolver-facing listener; the
  agent confirms every publish by re-fetching
  `/.well-known/livepeer-registry.json` and matching seq + canonical
  hash.
- The console refuses to boot if the policy file does not validate —
  a bad policy at boot is a config error, not a runtime pause.
- All transport is initiated from this host. The agent adds no
  listener; the secure-orch inbound posture is unchanged.

Hardening: pin the coordinator's TLS certificate at the OS trust
layer, and add an egress firewall rule allowlisting only the two
coordinator host:ports. The bearer token only keeps anonymous traffic
off the coordinator's endpoints — the manifest signature remains the
real content authentication in both directions.

### Sign policy

The policy file (`--agent-policy`, strict JSON, schema at
[`docs/sign-policy.schema.json`](./sign-policy.schema.json), example
at [`examples/sign-policy.json`](../examples/sign-policy.json))
is the entire trust envelope. It is reloaded at every cycle; a parse
or validation failure **pauses all auto-signing** (fail closed — there
is no fallback to a previous or default policy) and fires the
`policy_invalid` alert. Every load is audited with the file's SHA-256.

Candidate classes and dispositions:

| Class | Meaning | Phase 1 | Phase 2 |
|---|---|---|---|
| `renewal` | content identical, remaining validity below threshold | auto-sign | auto-sign |
| `benign` | every change within `benign_bounds` | hold + `would_auto_sign` shadow audit | auto-sign |
| `critical` | any change beyond the bounds | hold + alert | hold + alert |
| `forbidden` | `eth_address` or `spec_version` changed | refuse + alert | refuse + alert |

### Burn-in procedure (phase 1 → phase 2)

Run phase 1 (`auto_sign.benign: false`) for at least two weeks. Review
the audit log: every manual approval that carries a `would_auto_sign`
shadow event is calibration evidence that the policy would have signed
it for you. If a `would_auto_sign` appears on a change you hesitated
over, tighten `benign_bounds` before flipping. The phase-2 flip is one
line — `"benign": true` — and takes effect on the next cycle, no
restart.

### Held queue

Held candidates appear on the **Manifests** page as a "Pending
changes" card with the classification findings. "Load for review"
drops the candidate into the normal diff + tap-to-sign flow; signing
it clears the slot, audits `operator_approve`, and the agent pushes
the result on its next cycle — no download / re-upload. A newer
candidate superseding a held one replaces it (audited as
`held_superseded`), so you always review the latest diff.

### Kill switches

Any one suffices; all are audited:

1. `touch /var/lib/secure-orch/agent.pause` — pauses pull **and**
   sign; works over a plain SSH session with the console down.
   Remove the file to resume.
2. Policy `auto_sign.{renewal,benign}: false` — stops signing, keeps
   pulling/classifying/alerting (shadow mode).
3. Rate-limit breach auto-pause — more than
   `max_auto_signs_per_hour` auto-sign attempts in a sliding hour
   latches a pause on all auto-signing. A sign burst is the loudest
   available signal that the coordinator side is misbehaving:
   investigate before clearing. The latch clears on console restart
   (an in-console clear gesture is tracked as tech debt).

### Metrics and the expiry alert

`GET /metrics` on the console listener (same SSH tunnel as the UI)
exposes poll outcomes, decision counts, held-queue depth, the
last-publish-confirm timestamp, and
`secure_orch_agent_published_manifest_expiry_seconds`.

**Wiring an alert on manifest expiry is a phase-1 requirement, not
optional.** A rate-limit pause stops renewals too, and with a 24 h
manifest TTL an unnoticed pause can let the published manifest
expire. Two layers ship:

1. Built-in: the agent fires the `manifest_expiry_warning` webhook
   once per crossing when the published manifest has burned through
   half the renewal buffer without a re-publish.
2. If you run Prometheus, alert on the gauge as well, e.g.:

   ```yaml
   - alert: SecureOrchManifestExpiringSoon
     expr: secure_orch_agent_published_manifest_expiry_seconds < 14400
     for: 10m
   ```

The webhook (`--alert-webhook-url`) is best-effort by contract; the
audit log is the system of record.

## Protocol actions

When the console is started with a `protocol-daemon` unix socket
(`PROTOCOL_DAEMON_SOCKET`), the **Protocol actions** page lets the operator
drive the daemon's on-chain orchestrator operations and edit its automation
config. Available actions: force initialize-round / reward, set reward & fee
cut, force transfer bonded LPT, force withdraw ETH fees, cast a treasury
proposal vote (with a pre-vote lookup panel), set Service/AI-Service-Registry
URI, and an **Operational config** form (the auto-* toggles + fund-movement
receivers/thresholds; changes apply next round).

Trust boundary — important: **these are not cold-key operations.** The
console does not sign them and never sends the cold key. It makes a
session-authenticated gRPC call over the unix socket, gated by the same
typed-confirmation gesture (type the full signer address) and recorded in
the audit log. The **protocol-daemon signs the transaction with its own
keystore** — the orchestrator's hot key, which must equal the registered
orchestrator address. The cold key on this host is used *only* for manifest
signing.

Treasury voting prerequisite: the cast-vote and pre-vote lookup work only if
the protocol-daemon was started with `--treasury-address` (the
LivepeerGovernor contract). Without it the daemon returns `Unimplemented`
and the treasury panel shows that error — fix it on the daemon side, not in
the console. See
[`../../protocol-daemon/docs/operator-runbook.md`](../../protocol-daemon/docs/operator-runbook.md).

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

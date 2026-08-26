---
title: Credential store — bearer v1 and the keypair path
status: active
last-reviewed: 2026-08-26
---

# Credential store

The broker-side sealed store of runner attach credentials (plan 0043
§3.3, decision 5). Implemented in `internal/credentialstore`; admin
surface per [`broker-admin.md`](../../../livepeer-network-protocol/protocols/broker-admin.md)
§5; presented by runners per
[`runner-attach.md`](../../../livepeer-network-protocol/protocols/runner-attach.md)
§3.1.1.

## What a credential is

One credential = one host enrollment. It grants **attach** — the right to
open the outbound QUIC/WS connection and send an attach document — and
nothing else. It is not eligibility (certification gates that), not a
price or an offer (operator-owned), not a manifest change (impossible from
this channel). A stolen bearer therefore attaches *as that host*, is gated
by certification like any runner, credits the enrollment's payout address,
and ends when the operator revokes it. That is the v1 blast radius, and it
is accepted.

## v1: bearer

| | |
|---|---|
| Secret | `lpc_` + 256 random bits, base64url. Minted by `POST /admin/v1/enroll` (standalone) or by `pool-controller` (pool). |
| Stored | `sha256(token)` only. Records sealed at rest with AES-256-GCM under `credential_store.sealing_key_file` (same key format as the session store; may be the same file). |
| Shown | Exactly once: the `enroll` / `rotate` response. `Livepeer-Request-Id` replays return the recorded response so a lost reply does not strand a host. |
| Compared | Constant-time on the hash. Unknown, expired, and revoked tokens are indistinguishable to the presenter. |
| Lifecycle | `active` → (`rotate`) `rotating` with a grace window where old and new both attach → `active`; `expires_at` (default 90 d, max 365 d); `revoke` = delete the hash **and** close every connection for the host. |
| Pool sync | `PUT /admin/v1/credentials` carries only hashes; a synced entry that disappears from a push is a revoke. Locally enrolled credentials are never touched by sync. |

Attach auth order on both transports (`/internal/v1/worker/session`
WebSocket and the QUIC listener): the store first; then, until plan 0043
item 8 deletes `capabilities[]`, the legacy per-backend
`worker_session_credential` config string. A connection that
authenticated through the store is tracked by `host_id` so revoke can
kill it; a legacy connection is not (it is killed by backend id via the
worker-sessions route, which item 11 removes).

## The keypair path (documented, not built)

The store schema carries `kind` from day one so this is additive:

1. **Enrollment** — `POST /admin/v1/enroll` with `kind: ed25519` returns
   no secret; instead the agent generates a keypair locally and submits
   the public key in a second call (`POST /admin/v1/credentials/{id}/key`,
   to be added), or the pool controller syncs `{kind: ed25519, public_key}`
   entries. The record stores the public key where a bearer stores a
   hash.
2. **Attach** — the document carries
   `credential: { kind: ed25519, key_id, signature }` where `signature` is
   over the JCS form of the document with `credential.signature` removed;
   or the key is presented as the QUIC client certificate and the
   document carries only `{ kind, key_id }`. `Authenticate` dispatches on
   `kind`: bearer → hash compare; ed25519 → signature verify over the
   supplied bytes.
3. **Rotation** — the agent generates a new key, submits it under the
   same enrollment, and the old public key stays valid for the grace
   window exactly as a rotating bearer does.
4. **Blast radius** — a stolen *device* still attaches as that host, but
   a leaked *config* or *log* no longer does: the private key never
   leaves the host. Pool sync no longer carries anything secret-derived.
5. **Interim** — until this ships the broker answers
   `credential_kind_unsupported` to `kind: ed25519` on both enroll and
   attach, which is how an agent learns to fall back to bearer.

Nothing in the bearer wire shape, the admin routes, or the sync payload
changes when this lands; only a new `kind` value and one new route.

## Configuration

```yaml
credential_store:
  path: /var/lib/livepeer/broker/credentials.db     # persistent volume
  sealing_key_file: /etc/livepeer/broker-seal.key   # 32 bytes raw or 64 hex
  default_expiry_seconds: 7776000                   # 90 d
  max_expiry_seconds: 31536000                      # 365 d
```

Losing `credentials.db` orphans every enrollment: hosts must re-enroll.
Back it up with the session store. Losing the sealing key makes the file
unreadable, which is the intended failure.

# Federated member access

Set `member_issuer_trust_file` to a protected operator-managed JSON trust file.
The member listener accepts the fixed Ed25519 `lpm1` token format implemented by
`pool-commons/memberauth`. It contains issuer/key IDs, one canonical wallet,
immutable destination pool ID, portal session identifier, fixed `regional-member`
scope, issuance and expiry. Maximum token lifetime is two minutes with five seconds
of issuance-clock tolerance. There is no selectable algorithm or admin scope.

Controllers reload trust for each request and verify locally: pinned issuer/key,
signature, pool, scope, key validity, token expiry, key revocation and optional
revoked portal sessions. They never call the portal to authorize a request.
Existing enrollment bearer tokens continue to authorize agents independently.
Member identity can read/manage only its own enrollments; it cannot submit agent
status or stopped-execution evidence. The admin listener does not accept member
tokens. Regional member status remains authoritative for participation.

A wallet proof or `GET /member/v1/membership` does not create regional membership.
`POST /member/v1/join` with `pool_id` and the latest `terms_version` atomically
creates that region's member record and immutable acceptance. It requires member
authentication and exact same-origin protection. Invalid terms leave no membership,
and a new join cannot lift a regional suspension. Each other pool requires its
own explicit action. Regional nonce consumption is atomic; wallet reauthentication
cannot overwrite an operator's member status.

Trust file shape:

```json
{
  "issuer": "https://members.example.org",
  "keys": [{
    "id": "portal-key-1",
    "public_key": "BASE64_ED25519_PUBLIC_KEY",
    "not_before": "2026-09-01T00:00:00Z",
    "not_after": "2027-09-01T00:00:00Z",
    "revoked": false
  }],
  "revoked_sessions": []
}
```

`issuer` must match the token's issuer exactly; the member portal signs with its
exact HTTPS `public_origin` and writes this file via `--generate-key ... --trust-output`.

For key rotation, install the new public key on every regional controller before
switching the portal's signing key. Keep the old public key until issued tokens
expire, or mark it revoked for immediate local rejection. Distribute a session's
identifier into `revoked_sessions` when immediate regional revocation is needed.
Logout stops new token issuance; already-issued tokens otherwise expire within
the short lifetime. Protect the portal's private key as an authentication authority,
separate from every payout wallet. Do not log bearer tokens or private keys.

Agent credential recovery stays outside member authorization. The agent-only
`GET /member/v1/enrollments/{id}/agent-credentials` returns the current attach
credential to the current enrollment token. `POST .../agent-rotation` consumes
a durable, secret 256-bit request proof and rotates both secrets atomically with
the exact recovery result. A retry requires the previous token plus that same
proof, and only succeeds while the resulting generation remains current and
active. Acknowledging with the new token at `POST .../agent-rotation-ack` deletes
the recovery result. Later manual rotation, retirement or revocation invalidates
older recovery authority. Wallet tokens and portal proxy routes cannot use these
endpoints. Pending rotation results contain private credentials and are covered
by protected controller-store backup handling.

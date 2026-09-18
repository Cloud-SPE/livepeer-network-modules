# Pool implementation utilities

This optional Go module shares implementation helpers between regional pool
services. It is not a required protocol library and owns no regional books.
Build and test with `make build` and `make test` (Docker).

`serviceauth` validates machine credentials against an operator-owned JSON file:

```json
{"credentials":[{"id":"us-reconciler-audio","token_sha256":"<SHA-256 of 64-character bearer secret>","pool_id":"<persisted US pool ID>","roles":["revenue-reader"],"resources":["audio-broker"],"expires_at":"2027-01-01T00:00:00Z","revoked":false}]}
```

Use independent cryptographically random 32-byte secrets, hex encoded. The
server stores the SHA-256 digest; the client stores the secret in a mode-0600
file. Never put secrets in image layers, URLs, logs or browser configuration.
Requests carry `Authorization: Bearer <secret>` and `X-Livepeer-Pool-ID`.
The server selects its expected pool, exact resource and allowed roles from
local configuration and the endpoint; caller headers cannot grant authority.
Roles and resources match exactly, without wildcards or implicit admin power.
A credential can also carry an optional `source_id`; the controller requires it
on broker receipt-ingestion credentials and rejects receipts from other sources.

Replace the server credential file atomically to rotate/revoke credentials.
During rotation add the replacement with a distinct ID, switch the caller's
secret file atomically, verify access, then revoke/remove the old credential.
Every request reloads the file; malformed/missing files and duplicate IDs or
digests deny access. Reloaded client token files take effect on the next call.
Use bounded expirations and backup credential configurations with the owning
service, encrypting secret backups separately.

`HTTPSClient` permits requests only to its configured HTTPS origin, verifies
server identity with system or supplied CA roots, and refuses redirects.
Operators provision DNS/certificates; no ACME or VPN automation is included.
TLS ingress may terminate on the service's host with a loopback-only upstream;
never expose a plaintext service listener across hosts. Network controls do
not replace the endpoint's pool/role/resource checks.

Concrete endpoint permission matrices live with consuming components. A
`revenue-reader` credential does not authorize broker administration or receiver
mutation/signing. Local financial Unix sockets remain separate surfaces.

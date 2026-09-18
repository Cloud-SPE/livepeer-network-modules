# Regional service access

This implements the machine authorization boundary in
[regional pools, Decision 10](regional-pools.md). Member wallet federation is a
separate authorization surface. A service credential never becomes a member
session and member sessions do not authorize these machine endpoints.

## Credential and transport contract

Every regional request uses verified HTTPS, an independent bearer secret, and
`X-Livepeer-Pool-ID: <immutable pool ID>`. The receiving service checks all of:
secret digest, expiry/revocation, exact pool ID, resource, and endpoint role.
A resource is the controller (`controller`), a particular broker's configured
`service_resource`, or the shared authority (`ownership`). The authority serves
multiple pools but still verifies each caller against its own pool; callers
cannot mutate another pool's claims. There are no wildcard roles or resources.

Controller configuration enables `service_auth_file`; its pool identity comes
from its persistent database. Broker configuration supplies `pool_id`,
`service_resource`, and `service_auth_file`. Enabling this file replaces legacy
broad bearer authorization on that service's admin endpoints. Missing or malformed
files deny access; they do not fall back to legacy credentials or admin cookies.
The shared authority requires an auth file and TLS certificate/key at startup.

Controller and broker HTTP listeners sit behind a TLS ingress on the same host,
with plaintext upstreams bound to loopback or an unexposed container network.
Only HTTPS ingress is published across hosts. Receiver financial mutation and
protocol control remain local Unix sockets. VPN access is optional and does not
replace credential checks. Operators provision certificates and DNS themselves.

## Endpoint permission matrix

All rows require the owning pool and exact receiving resource. `pool-admin` is
an explicit operator role for controller/broker admin endpoints; never issue it
to reporting, reconciliation, coordinator, or member clients. New admin routes
default to that role until explicitly assigned.

| Resource | Role | Methods and paths |
|---|---|---|
| controller | broker | GET `/admin/v1/backend-selection-snapshot`; POST `/admin/v1/work-receipts`, `/admin/v1/backend-outcomes` |
| controller | reconciler | GET `/admin/v1/work-receipts`, `/admin/v1/round-receipts`, `/admin/v1/settlement-windows`, `/admin/v1/regional-terms`, `/admin/v1/revenue-sources`; POST `/admin/v1/round-close`, `/admin/v1/settlement-windows/close` |
| controller | payout-executor | GET `/admin/v1/payout-intents`, `/admin/v1/payout-alerts`; POST `/admin/v1/payout-intents/claim`, `/renew`, `/release`, `/requeue`, `/status` (all under `/admin/v1/payout-intents`) |
| configured broker | controller | GET `/admin/v1/runners`, `/offers`, `/certification`; PUT `/admin/v1/offers`, `/admin/v1/credentials` |
| configured broker | coordinator | GET `/admin/v1/runners`, `/offers`, `/certification`; POST `/admin/v1/offers/{id}/accept-shape` |
| configured broker | revenue-reader | GET `/reporting/v1/revenue/{round}` |
| ownership | ownership-reader | GET `/ownership/v1/devices/{device}` |
| ownership | ownership-controller | Device GET plus POST `/ownership/v1/claim`, `/drain`, `/release` |
| ownership | ownership-admin | Device GET plus claim/drain/release and POST `/ownership/v1/fenced-recovery` |

Abbreviated broker GET paths above share `/admin/v1`. The revenue endpoint
proxies only local receiver `GetRoundRevenue`, returning the immutable ledger
identity, inclusion digest and chain completeness evidence. Its dedicated
`revenue-reader` role grants no broker admin access, receiver balance changes,
signing, credential administration, or payout approval. A complete zero is
accepted; missing, stale, source-mismatched or incomplete reports hold collection.
The matrix reserves regional terms/source/window routes as those implementations
land; authorization entries alone do not create routes.

## Client configuration

Controller broker authentication and broker controller authentication use:

```yaml
auth:
  method: scoped
  pool_id: <persisted-pool-id>
  token_file: /run/secrets/service-token
  ca_file: /run/secrets/service-ca.pem # optional; system roots otherwise
```

The receiving URL must use HTTPS. Controller broker settings use `auth` under the
broker target; broker `pool_snapshot` and `receipt_sink` settings use the same
shape. Reconciler and payout executor place `pool_id`, `token_file`, and optional
`ca_file` directly under `pool_controller`, alongside its HTTPS `url`.
Do not combine these settings with legacy bearer fields.

Coordinator broker targets use `pool_id` and
`admin_token_ref: file:///run/secrets/service-token` with an HTTPS `base_url`.
Coordinator uses the system trust store, including operator-installed private CA
roots. Its scoped credential may inspect offers and accept shapes; enrollment and
revocation remain controller responsibilities. Unsupported operator actions return
authorization errors rather than acquiring broader credentials.

Controller and broker ownership clients use:

```yaml
ownership:
  url: https://ownership.example.org
  token_file: /run/secrets/ownership-token
  ca_file: /run/secrets/service-ca.pem
```

The controller uses its persisted pool identity; the broker uses its configured
pool identity. Ownership tokens use the `ownership` resource and the appropriate
ownership role. Use separate credentials per service instance.

## Issuance, rotation and revocation

The JSON format and cryptographic secret requirements are documented in
[pool-commons](../../pool-commons/README.md). Generate independent random 32-byte
secrets encoded as 64 lowercase hex characters; hash the encoded secret with
SHA-256 for the verifier file. Store client secrets in mode-0600 files and mount
only the needed secret into each service. Never return these files to members.

For rotation, atomically add the new credential ID/digest to the server's file,
replace the caller token file, verify a scoped request, and then atomically revoke
or remove the old credential. Every request reloads both files. Expiration is
mandatory; revoked credentials fail immediately on that server. Back up server
credential metadata and encrypt client secret backups separately. Do not reuse a
secret across pools or receiving services. No credential management UI is required.

Local tests exercise HTTPS certificate rejection, token rotation, expiry,
revocation, wrong pool/resource/role, and redirect refusal. This is local validation;
production ingress reachability and certificate installation require rollout checks.

The broker also exposes `GET /reporting/v1/work/{round}` to `revenue-reader` on the same scoped HTTPS origin. Broker receipt-ingestion credentials carry an additional immutable `source_id` equal to the receiver settlement-domain ID; the controller rejects receipts from other sources. See [durable work accounting](../../capability-broker/docs/regional-work-accounting.md).

Source retirement adds exact broker mutations `POST /admin/v1/source/drain` and
`POST /admin/v1/source/freeze` for `controller` or `pool-admin`. The separate
`GET /reporting/v1/source` proof requires `revenue-reader`. The controller's
operator-only `POST /admin/v1/revenue-sources/{source}/retire` fetches that proof
using its own local source reader credentials; the request carries only the
retirement round and audit reason. Keep receiver gRPC sockets private: source
freeze is a local privileged receiver operation, never a public receiver port.

The controller regional accounting summary and durable window holds are exposed
at `GET /admin/v1/regional-accounting` to scoped `reconciler` and `pool-admin`
credentials. These read permissions do not grant payout approval.

Regional terms publication uses controller `POST /admin/v1/regional-terms` and
`GET /admin/v1/terms-publications` (pool administrator), then broker
`GET/POST /admin/v1/terms-policy` (scoped controller or pool administrator).
Policy replies bind pool, broker and receiver-source identity. Revenue readers
cannot pause or activate admission policy.

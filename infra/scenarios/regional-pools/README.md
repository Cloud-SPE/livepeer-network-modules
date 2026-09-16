# Independent regional pools

This scenario implements the locked [regional design](../../../docs/design-docs/regional-pools.md):
EU transcode; US transcode, audio transcription/TTS, and LLM chat; one shared
portal and durable GPU ownership service. It generates configuration offline.
Generation and parser validation do not deploy services or establish live
runner readiness, chain completeness, funding, or successful payouts.

`generate.py` writes one Compose project per physical host, so management and
shared services may be colocated without competing for port 443. The four
broker roles remain separate hosts. To separate management, add `eu-management`
and `us-management` host entries and change the regions' `host` fields. Pool IDs,
source IDs, service names and durable volume names remain unchanged. Move the
existing stores using the [manual migration procedure](backup-and-restore.md) before starting the new
writer; changing `host` alone does not transfer data.

## Input and identity preparation

Start from [spec.example.json](spec.example.json). Its placeholders are rejected
by the generator; sample addresses and `.invalid` domains are not production
values. Use a private working directory outside the repository for completed
specifications, material and generated output. Do not check in keys, tokens,
keystores, passwords, issuer secrets, or generated secret directories.

Choose a stable `deployment_id`. Volume names are
`<deployment_id>-<service>-data`; socket volumes end in `-socket`. Changing this
name changes storage mappings and is not a rename operation. Provision and
initialize the two controller volumes **before** completing `pool_id` fields,
using the [offline identity commands](../../../pool-controller/docs/regional-bootstrap.md).
For example, with `deployment_id=regional-pools`, the EU store is
`regional-pools-eu-controller-data`. Existing pools always use `--expect-pool-id`.
Never initialize a replacement database to repair a missing mount.

Each broker's `source_id` is its independent receiver settlement domain, not
the shared payee address or a derived region label. For existing receivers,
preserve the domain in their ledger and all session/redemption history. A new
receiver may import a new random nonzero 32-byte `0x` domain through the generated
`--settlement-domain-id` flag; subsequent startup requires it to match the store.
Record the domain before registering the source. Retain draining/retired sources
and their old reporting obligations during later topology changes; this initial
four-source compiler is not a historical-source deletion tool.

Supply these operator-provisioned materials:

| Material | Mounted services |
|---|---|
| Separate EU and US payout V3 keystores/passwords | The corresponding regional executor only |
| Four distinct receiver signing V3 keystores/passwords | Corresponding receiver only |
| Broker delegated settlement signing key and sealing key | Corresponding broker only |
| Portal Ed25519 issuer private file | Portal only |
| Portal public issuer trust file | Both controllers |
| CA certificate bundle | HTTPS clients and proxies |
| Per-host TLS certificate chain/private key | Host ingress; ownership service also uses its host certificate |

The signing protocol daemon and orchestrator cold key remain on `secure-orch`.
The generator rejects identical EU/US payout addresses, collision with payee or
receiver wallet roles, reused signing-keystore paths, and keystore address
mismatches. Executors additionally verify the decrypted signing wallet against
their configured identity before resuming intents. The generator never unlocks
a financial keystore or signs a transaction.

Generate the portal key using the pinned portal image's `--generate-key`,
`--issuer`, `--key-id`, and `--trust-output` command; see
[portal authentication](../../../member-portal/docs/authentication.md). Broker
settlement-key provisioning uses the broker's `settlement-key` command. Provision
receiver delegated signing authority on the secure side using the existing
signing workflow; do not substitute the orchestrator private key on a broker.

Certificates must cover every ingress hostname assigned to their host. Use
publicly trusted certificates for member browsers and enrolled agents. Internal
service clients additionally support the supplied CA file; the coordinator uses
its system trust store, which must trust these certificates. There is no DNS or
ACME automation here. RPC URLs must use HTTPS; empty URLs are rejected because
they would select receiver development mode.

All ten image entries are immutable digests: eight services plus Nginx and a
BusyBox-compatible volume helper. `sha256:<local image ID>` is accepted only with
`provenance.local_only=true` and requires those images already present on the
host. Portable rollout configurations use `repository@sha256:<digest>` and
archive the tested commit, tree hash and validation evidence. The agent image
is pinned into enrollment bundles. No image is published by these scripts.

## Generate and validate

Run from the repository root, mounting private material at the paths named in
the input. The Python tooling uses the standard library; a container keeps it
independent of the operator's Python installation:

```sh
docker run --rm --network=none \
  -v "$PWD:/repo:ro" -v "$REGIONAL_WORK:/work" -v "$REGIONAL_MATERIAL:/material:ro" \
  -w /repo python:3.12 \
  python infra/scenarios/regional-pools/generate.py /work/spec.json /work/generated
```

Output must not already exist. Generation creates separate random credentials
for every caller, role, target and pool. It does not reuse a controller's
administration token for revenue reads. The portal receives no machine admin
credential. Receiver mutation/signing sockets mount only into the corresponding
broker and receiver; observer sockets mount only into the observer and regional
reconciler. Payout executors have no shared financial socket.

Generated secret directories and config files are protected. Before deploying
a host, use the pinned helper image as container UID 0 to give its `config/` and
`secrets/` trees container UID/GID `65532:65532`. Keep secret files mode `0600` and
directories `0700`; do not make issuer keys group/world-readable. Mounting whole
service-specific secret directories allows atomic file replacement to be seen
by credential reloads. Bind-mounting individual secret files would pin old inodes.
With rootless Docker, container IDs map to subordinate host IDs: perform this
ownership step inside the same Docker engine used to run services.

Prepare every declared external volume on its owning host. Verify an existing
volume's identity and intended writer before reuse. For a genuinely new empty
volume, create it explicitly, then mount only that volume into the helper and
`chown 65532:65532 /data`; do not recursively change an active database. External
volumes deliberately prevent Compose from silently creating replacement stores.
Never use `docker compose down -v` for these projects.

After provisioning file access, run the read-only parser preflight on the
generation host with all images available:

```sh
python3 infra/scenarios/regional-pools/preflight.py "$REGIONAL_WORK/generated"
```

This invokes Docker Compose configuration validation and pinned controller,
broker, reconciler, executor, portal and Nginx parsers at the actual non-root UID,
with networking disabled. It does not start observers, receivers or payout
loops. For a new rollout, verify existing controller identities against the
manifest, certificate trust/SANs/expiry, decrypted wallet identity, and preserved
receiver domains before starting services. Archive evidence with the manifest.

## Activation order

Starting services is a separate, explicitly authorized operator action. First
provision stores and signing material, then start the shared ownership service,
regional controllers/observers and each broker/receiver. Use the generated
operator credentials and `X-Livepeer-Pool-ID` header on scoped HTTPS APIs.

The `operator/<region>-source-registration.json` files contain one request body
per source for `POST /admin/v1/revenue-sources`. They intentionally leave
`start_round` null: choose a common future source/terms boundary from the regional
observer and fill it before submission. Register every source **before**
publishing the first terms version. Publish the same effective round with the
14-round default, uniform commission, participation rules, and required zero-work
and rounding disclosures using the [terms publication barrier](../../../pool-controller/docs/regional-terms.md).
The controller must receive acknowledgments from every regional broker; an
unavailable source holds publication instead of silently shrinking the pool.

Enable each broker's selected templates and prices through the controller's
template administration surface. Catalog selection alone does not enable an
offer. Start reconciler/executor loops after source and terms setup. Batch
approval remains manual unless an explicitly reviewed bounded payout policy
enables automation. Fund each dedicated payout wallet and its gas separately;
there is no automatic treasury transfer. Member deductions do not pay gas.

Merge `operator/coordinator-brokers.json` into the existing secure coordinator's
configuration and install its four role-scoped token files at the named secret
paths. This is a fragment, not a replacement for secure-orch configuration or
keys. The coordinator reads regional broker state and publishes certified
offers; each regional controller remains the sole writer of pool offer policy.

Finally start the portal and enroll the initial transcode/audio/LLM runner hosts
through their regional memberships and terms. Generated enrollment bundles use
the [regional broker fleet](../../../pool-controller/docs/regional-broker-fleet.md).
Validate actual model readiness and certification before advertising the offers.
The catalog and parser tests do not prove that a GPU has downloaded a model or
can complete a paid workload.

## Network and restart behavior

| Interface | Host port | Access |
|---|---|---|
| Portal origin | TCP 443 | Wallet sign-in and member UI |
| Regional member origins | TCP 443 | Regional member and agent APIs; separate controller listener 8084 |
| Regional management origins | TCP 443 | Scoped controller `/admin/v1/*` APIs |
| Broker public origins | TCP 443 | Registry, funded workloads and outbound agent WebSocket attach; admin/reporting paths denied |
| Broker administration origins | TCP 443 | Scoped admin/reporting; read-only registry for coordinator |
| Ownership origin | TCP 443 | Scoped `/ownership/v1/*`; verified TLS again to native ownership listener |
| Metrics | Container 9090 | No public host mapping; operator monitoring network/access required |
| Receiver/observer control | Local Unix sockets | Only explicitly mounted service pairs |

VPN is optional. One proxy per host terminates operator-supplied certificates;
unknown hostnames fail the TLS handshake. Services have no direct public port
bindings. Broker streaming disables proxy buffering and forwards HTTP trailers
using [Nginx's documented trailer support](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_pass_trailers).
The proxy resolves Docker service names periodically so container recreation
does not leave a stale IP pinned until a proxy restart. This is local service
discovery, not regional failover.

`restart: unless-stopped` reopens the same persistent volumes. Lost-response
replays, ownership generations, source proofs and payout intent recovery are
handled by their durable component stores. A normal restart does not authorize
a fresh pool identity, reset a receiver ledger, or transfer a GPU. An outage of
the portal does not stop already-enrolled agents or financial loops. An ownership
outage blocks new claims/transfers; existing assignments continue. Archive all
stores and secrets using the [manual backup/restore procedure](backup-and-restore.md) before migrations.

## Local acceptance

`test_generate.py` checks topology, signing-role collisions, source completeness,
scope, socket/storage isolation and management relocation. `validate-local.py`
uses freshly built `regional-local/*:validation` images, real offline controller
identities, a synthetic portal signer and a test CA. Receiver fixture keystores
are deliberately unusable. It tests actual non-root parser access and returns
fixture file ownership for inspection; it creates no named data volumes or
listening services. Its result and image IDs are written to a new evidence
directory. Live rollout and the broader regional acceptance suite are separate
gates.


For service credential rotation, follow the [scoped access runbook](../../../docs/design-docs/regional-service-access.md): install new verifier entries before switching the corresponding caller file, verify the new scope, then revoke the old entry. Preserve unrelated roles and pools. Replace files atomically inside their mounted service secret directory.

Re-encrypting a payout keystore or changing its password retains the same wallet address and intent store. Stop that executor, replace the paired protected files, verify the decrypted address, and resume its existing journals. Changing a payout **address** requires a controlled wallet migration: settle or explicitly reconcile every old-wallet intent, archive its journals, fence the old executor, provision a separate new wallet and intent store, and change the expected address in that region's configuration. Never edit an existing intent-store binding or run two executors sharing one wallet. Funding and residual balance movement are separate operator transactions; the configuration tools perform neither.

[Recorded local validation](local-validation-2026-09-16.json) lists the tested image IDs and evidence paths. The source tree was uncommitted; its hash describes the validation snapshot, not a published release. Rebuild and rerun acceptance after runtime changes before using an image for rollout.

The [2026-09-16 local acceptance record](local-acceptance-2026-09-16.md)
describes the real two-region process tests, exact financial results, synthetic
input boundaries and remaining hardware/release evidence.

Local transfer acceptance uses
`./infra/scenarios/regional-pools/check-transfer-acceptance.sh`; it requires a
local Docker socket and creates only isolated test containers. Ordinary
`e2e` runs skip that explicitly enabled case. See the
[acceptance evidence and synthetic boundaries](local-acceptance-2026-09-16.md).

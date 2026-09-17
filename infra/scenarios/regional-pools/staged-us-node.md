# Staged US transcode node

`prepare-us-node.py` prepares an initial US transcode source with a colocated US
controller, keyless observer, reconciler, payout executor, portal and shared GPU
ownership authority. This is staged activation of the regional design, not a
replacement for its eventual EU transcode and US transcode/audio/LLM topology.
The full-topology generator retains its existing four-source validation.

This helper is for a fresh deployment whose controller identity has **already**
been initialized and whose encrypted wallets, broker keys, portal keys and
twelve service credentials exist. It never creates a replacement identity.
Use a stable deployment ID: the controller volume must already be named
`<deployment_id>-us-controller-data`. Do not change the deployment ID to move a
service; that changes the volume mapping. Use the manual backup/restore process.

Input JSON has exactly these fields (all URLs are distinct HTTPS origins):

```json
{
  "deployment_id": "pool-deployment",
  "pool_id": "pool_REPLACE_WITH_EXISTING_ID",
  "payee": "REPLACE_WITH_ORCHESTRATOR_ADDRESS",
  "receiver_wallet": "REPLACE_WITH_RECEIVER_ADDRESS",
  "payout_wallet": "REPLACE_WITH_US_PAYOUT_ADDRESS",
  "broker_url": "https://us-transcode.example.org",
  "broker_admin_url": "https://us-transcode-admin.example.org",
  "member_url": "https://us-members.example.org",
  "admin_url": "https://us-management.example.org",
  "portal_url": "https://pool.example.org"
}
```

The base directory must contain `config/receiver-domain-id`, and these files
under `secrets/`: `receiver-keystore.json`, `receiver-password`,
`payout-keystore.json`, `payout-password`, `broker-settlement.key`,
`broker-sealing.key`, `portal-issuer.json`, `member-trust.json`. Credentials are
in `secrets/service-credentials/<caller>/<caller>-to-<target>-<role>.token`;
target folders contain `service-auth.json`. `metadata.json` binds those
credentials to the pool and source. The helper's `LINKS` is the exact role map;
its validation checks hashes, scope, source binding and expiry before staging.

Keep one HTTPS RPC endpoint per line in a protected file. Do not paste provider
API keys into chat or commit them. Run:

```sh
sudo python3 prepare-us-node.py --base /opt/livepeer/pool \
  --spec node.json --rpc-file /opt/livepeer/pool/secrets/rpc-urls
```

The output directory `deployment/` must not exist. Existing material is read,
never modified; output is a protected, service-isolated copy. Partial output
after failure is retained for inspection. Do not regenerate a live deployment
to rotate keys. Rotate the **mounted copies** and their verifiers using the
regional service access procedure, then reconcile protected provisioning copies.

The helper defaults to the operator-requested `tztcloud/*:v2.0.0` images and
does not pull, build or publish images. `--registry` and `--tag` override those
defaults. Tags do not establish source provenance. Pull intended images before
offline validation, record their immutable digests, and retain the validated
images; do not silently upgrade an active deployment by repulling a moved tag.
Agent enrollment requires an immutable image: pull the requested member-agent
tag before preparation. The helper reads its local Docker RepoDigest and records
that reference in controller bootstrap configuration; it does not pull it or
run it. Alternatively supply `--agent-image repository@sha256:<digest>`.

## Internal TLS and operator-managed ingress

Ownership runs native HTTPS at `https://gpu-ownership:8443` on the Compose
network. A private self-signed RSA certificate with that DNS SAN is generated
offline; only controller and broker receive its public trust certificate. Its
key mounts only into ownership. It expires after 365 days: distribute new trust
before replacing its key/certificate and removing old trust. No public DNS or
ACME automation is provided.

Other cross-service clients use the configured public HTTPS origins with system
CA verification. Those names must resolve and route from inside the containers,
not just a user's browser. Cloudflare Origin CA certificates alone are not
system-trusted on direct connections. Configure valid certificate trust rather
than disabling verification. Operator-managed ingress can attach to the generated
`<deployment_id>-us` network; no host ports, proxy container, or Traefik labels are
generated.

| Origin | Backend | Routing boundary |
|---|---|---|
| Broker public | `us-transcode-broker:8080` HTTP | Deny `/admin` and `/reporting`; retain registry, workload, streaming and agent WebSocket paths |
| Broker admin | `us-transcode-broker:8080` HTTP | `/admin/v1/*`, `/reporting/v1/*`, read-only `/registry/*` |
| US management | `us-controller:8080` HTTP | `/admin/v1/*` only |
| US members | `us-controller:8084` HTTP | `/member/v1/*` |
| Portal | `member-portal:8080` HTTP | Preserve external Host and HTTPS origin |

Shared ownership does not need public ingress in this first colocated stage.
Before adding remote regions, provide reachable verified HTTPS ownership access
and scoped credentials while retaining this same store. Do not start a second
independent ownership authority. Metrics have no public mappings.

## Offline checks and phased activation

Generated configs/secrets are initially owned by the generating user (normally
root), mode 0600 in private directories. Before non-root parsers or services,
use the Docker engine's helper container as UID 0 to set **only generated**
`deployment/config` and `deployment/secrets` to container UID/GID 65532:65532.
This also works with rootless Docker's ID mapping. Keep the original input
secrets and operator credentials private; never mount the whole input secrets
directory into every service.

```sh
docker run --rm --network none --user 0:0 \
  --mount type=bind,src=/opt/livepeer/pool/deployment,dst=/work \
  busybox:1.37 chown -R 65532:65532 /work/config /work/secrets
sudo python3 check-us-node.py /opt/livepeer/pool/deployment
```

The checker runs Compose validation plus controller, broker, reconciler, executor
and portal parsers as UID65532 with networking disabled. It mounts no databases
and runs no receiver, transaction or payout loops. The printed image IDs/digests
are evidence for those checks, not evidence of paid workload or live rollout.

Every data/socket volume is external. Explicitly create new volumes and initialize
their container ownership only after checking their intended writer. Preserve
the existing controller volume and require its saved pool ID using offline
`init-identity --expect-pool-id`. Never use `down -v`, initialize an empty
replacement controller, or recursively chown an active database to repair a mount.

Default Compose services are ownership, controller and keyless observer. Other
services require explicit profiles:

| Profile | Services | Prerequisites |
|---|---|---|
| `traffic` | Broker and receiver | Receiver authority/gas, preserved source identity, ingress and durable stores |
| `accounting` | Reconciler | Source registration and acknowledged regional terms boundary |
| `members` | Portal | Member ingress, published terms, enabled templates and readiness checks |
| `payouts` | Executor | Dedicated wallet identity/funding, approved obligations and recovery checks |

Profiles prevent a bare `up -d` from accidentally starting financial services;
explicitly naming a service can still activate it. Deployment is a separate
operator step after review. Source registration request intentionally has null
`start_round`; choose the effective boundary from the observer, register the
source and publish matching terms. Other US broker sources must be registered
before participating; never silently omit already registered/historical sources.
Batch approval remains manual. No automatic treasury funding is introduced.

Merge `operator/coordinator-brokers.json` into the existing coordinator config,
install its role-scoped token on the secure host and mount it at the referenced
path. Replace the old entry for this same broker rather than adding a duplicate;
preserve unrelated brokers and coordinator identity/publish settings. Normal
manifest review and signing still apply.

Back up the generated configuration, mounted secrets, TLS trust/key, all durable
volumes, original provisioning material and recorded image digests. Follow
`backup-and-restore.md` for stopped-writer/coherent backup and manual restore.
No live deployment, target GPU certification or payout is established by generation.

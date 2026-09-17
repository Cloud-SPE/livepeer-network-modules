# Regional pool setup container

A one-shot `plan → generate → validate` workflow creates a regional deployment
package from environment configuration. It handles encrypted V3 wallets, broker
keys, immutable controller identity, source domain, portal keys, scoped service
credentials, internal ownership TLS and service-specific Compose/configuration.
It never deploys services, publishes manifests/images or submits transactions.

The container needs **no Docker socket**. It contains actual component parsers,
key-generation binaries and the curated template catalog, assembled from the
selected runtime images. Configuration checks are offline. Registry digest
resolution is the only generation-time network operation; provide an image lock
file to generate with `--network none` as well.

## Build and run

From the monorepo root, build the setup tool locally (does not rebuild the runtime
modules or publish anything):

```sh
docker build -f pool-setup/Dockerfile -t livepeer-pool-setup:local .
```

Create an operator directory with `setup.compose.yaml` and `.env`, copied from
this component's files. Set `SETUP_IMAGE=livepeer-pool-setup:local`. The generic
`.env.example` creates missing material. `examples/open-pool-us.env` instead
reuses the existing Open Pool identity, wallets, keys and twelve credentials.
Its public addresses are configuration, not secret material.

Create `OUTPUT_DIR` and `EXISTING_MATERIAL_DIR` as private host directories first.
Put RPC URLs, one HTTPS endpoint per line, into the protected `RPC_URLS_FILE`
relative to the input directory. Keep real RPC URLs, passwords and private keys
out of `.env`, logs and source control. For a fresh pool, explicitly create the
named `CONTROLLER_VOLUME` with `docker volume create`; it is external so a missing
mount never silently turns into a new pool. For reuse, select the already
initialized volume and set `POOL_ID` to its recorded immutable value.

```sh
docker compose -f setup.compose.yaml run --rm setup plan
docker compose -f setup.compose.yaml run --rm setup generate
docker compose -f setup.compose.yaml run --rm setup validate
```

Run identity initialization/verification only with the controller stopped. The
controller's own `init-identity` command checks the volume, and `reuse` refuses a
missing database. Even `create-if-missing` refuses an empty volume when an
expected pool ID is supplied. The controller volume is never replaced by a
configuration label. The setup process runs as container root to provision
UID65532-owned files; this also respects rootless Docker's mapped IDs.

## Modes and optional services

All material modes accept `reuse` or `create-if-missing`; neither overwrites
existing identity/key material. Wallet, broker-key, portal-key, receiver-domain,
service-credential, ownership-TLS and pool-identity modes are independent.

`REGION=eu|us` and `BROKER_ROLE=transcode|audio|llm` select names and templates.
EU supports transcode; audio and LLM belong to US. This is a staged node package,
not an assertion that the complete regional epic or full topology is deployed.

- `INCLUDE_REGIONAL_MANAGEMENT=true` adds controller, observer, reconciler and
  executor. Otherwise configure the existing pool ID and regional HTTPS URLs;
  no local payout wallet or controller database is created. Use
  `setup-broker.compose.yaml` for this case; it omits the controller volume
  mount entirely. Commands remain `run --rm setup plan|generate|validate`.
- `INCLUDE_MEMBER_PORTAL=true` adds the portal and its private signer. With it
  false, a local controller still needs the existing shared portal's public
  `member-trust.json`; no private portal key is imported or generated.
- `INCLUDE_GPU_OWNERSHIP=true` creates the shared ownership service with private
  HTTPS trust on the Docker network. Otherwise set reachable `OWNERSHIP_URL`;
  optionally supply `OWNERSHIP_CA_CRT_FILE` for private CA trust. Never create a
  second authority for the same managed GPU fleet.

For additional brokers using existing regional management, set management false
and export/merge the emitted caller credentials and verifier entries into those
existing services. Do not replace a live regional controller's fleet/source list
with this single-source bootstrap configuration. This tool does not yet edit a
running fleet or perform coordinated credential rotation. Existing verifiers
are retained; review merge artifacts instead of overwriting unrelated roles.

## Output, reruns and interruption

```text
OUTPUT_DIR/
  .setup/                    # Private material and identity journal; back up
  current-revision           # Relative path to last validated package
  revisions/<fingerprint>/
    compose.yaml
    config/
    secrets/<service>/
    operator/
    deployment-manifest.json
    validation-report.json
```

Input mounts are read-only. Generated material is journaled before rendering.
A repeat run with identical inputs preserves keys/tokens and returns the same
revision. A config or image change yields another revision; generation does not
activate it. Imported keys that differ from the journal are rejected and require
an explicit rotation process. Do not delete the journal to bypass a conflict.
Interrupted token creation resumes using the existing token; interrupted wallet
creation preserves its password. Incomplete or corrupt key pairs fail closed
for inspection, rather than regenerating their surviving halves.

Only validated packages become the current revision. Partial `.pending-*`
packages are retained for diagnosis and never selected. All mounted service
secrets are mode0600 and owned by container UID65532; operator credentials and
journal stay private to setup. Every service gets only its own key material.
The portal gets no machine-administration bearer token.

The manifest records immutable image references, pool/source/wallet identities,
volume names, credential/portal/TLS expiry dates and package-file hashes. Tags resolve through `skopeo` using normal
TLS verification. `IMAGE_LOCK_FILE` supplies an offline JSON map with exactly
these keys: `pool-controller`, `capability-broker`, `payment-daemon`,
`protocol-daemon`, `pool-reconciler`, `pool-payout-executor`, `member-portal`,
`pool-member-agent`; every value must be `repository@sha256:<digest>`.
The setup image's embedded parsers must correspond to the selected module release;
validation does not establish that arbitrary digest overrides match those tools.

## Deployment boundary

Generated Compose has no public ports or ingress configuration. Attach your
operator-managed ingress to the generated network and configure the boundaries
in [staged node routing](../infra/scenarios/regional-pools/staged-us-node.md).
HTTPS origins must resolve from containers; do not disable TLS verification.
The ownership endpoint is native HTTPS at `gpu-ownership:8443`, with trust copied
only to callers. Its certificate and the default credentials expire in one year;
rotate them before expiry. Portal key expiry is recorded in its public trust file.

Compose uses **external persistent volumes**. Verify/provision the remaining
named data/socket volumes and UID65532 access before starting any services.
Preserve the controller volume setup initialized. Do not run `down -v`.

Only ownership, controller and keyless observer are enabled by default. `traffic`,
`members`, `accounting` and `payouts` profiles control other services; explicitly
naming a service can also activate it. Source registration requires an explicit
round boundary, followed by acknowledged terms publication and template/price
configuration. The generated request intentionally leaves `start_round` null.
Coordinator token installation, receiver authority, wallet funding, member
onboarding, GPU validation and approved payouts are separate operator steps.

Offline validation checks package integrity and five actual module parsers. It
never unlocks financial wallets or proves chain/ingress/hardware readiness, nor
runs Compose on the host. Host `docker compose config --quiet` remains the final
Compose implementation check before deployment. Follow the existing
[backup/manual restore procedure](../infra/scenarios/regional-pools/backup-and-restore.md)
for stopped writers, financial journals and preserved identities. Back up both
`.setup/` and the selected package, keys and all durable volumes.

## Validation

```sh
python3 -m unittest discover -s pool-setup/tests -v
python3 pool-setup/tests/container_acceptance.py --image livepeer-pool-setup:local
```

Acceptance uses only synthetic wallets/RPC endpoints and isolated temporary
bind mounts. Containers have networking disabled, no Docker socket and no runtime
service loops. Image locks in fixtures are synthetic references for parser tests,
not rollout evidence. Nothing is published by these commands.

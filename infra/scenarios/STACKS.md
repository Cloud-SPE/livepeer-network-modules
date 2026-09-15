# v2.0.0 stack audit and upgrade notes

Source audit: 2026-09-15, tracked by `lnm-gi6`. These deployment files target the
current monorepo code and default Modules images to `tztcloud/...:v2.0.0`.
This is not an announcement that the changes below have been published.

## Covered entry points

| Stack | Purpose and current contract |
|---|---|
| `orchestrator-onboarding/secure-orch-control-plane` | Protocol daemon, resolver, cold console; private host bindings; optional sign-cycle agent overlay. |
| `orchestrator-onboarding/orch-coordinator` | Separate public manifest/admin/metrics listeners; agent and ingress overlays. |
| `orchestrator-onboarding/capability-broker` | Broker plus receiver with explicit orchestrator payee; persistent financial/workload state; ingress overlays. |
| `orchestrator-onboarding/pool-member-agent` | Attach-only runner host; external runner network and enrollment credential. |
| `orchestrator-onboarding/ingress-{traefik,nginx,cloudflared}` | External ingress images retain their own release versions, not Modules v2.0.0. |
| `pool-orchestrator` | Full public pool stack, separate controller member/admin listeners and authenticated broker offer push. |
| `pool-node` | Pool accounting/controller stack with external broker, receiver and protocol-daemon dependencies. |
| `integration-stack` | Mainnet pilot requiring explicit image digests; never replace these inputs with an implicit mutable tag. |
| Component `compose/` directories | Broker, payment, protocol, registry, coordinator, console and pool components; all base/agent/dev/scenario variants. |
| Component root Compose files | Coordinator synthetic-fixture mode, console local deployment, protocol daemon and registry identity-only publisher example. |
| Protocol conformance `examples/docker-network` | Broker plus conformance workload, v2.0.0 defaults and explicit image overrides. |
| `infra/compose` and `customer-portal/compose.yaml` | Shared external services; no Modules images to retag. |
| Generated pool member bundle | First-boot agent project plus independently reconciled runner project. |

Historical archives, references and test-fixture runner image versions are not
release-stack defaults. Workload runners have independent release lines; a
Modules v2.0.0 audit does not relabel those images.

## Corrections made

Pool coordinator admin defaults to host loopback port 8080; pool-controller admin
now uses loopback 8083, eliminating the collision. Its public member listener is
container port 8084 and defaults to host loopback 8084 for a TLS proxy. Existing
operator configs must add `listen.member: ":8084"`. Controller-to-broker offer
push uses `bootstrap.broker_admin_auth` referencing the existing admin token.
The payout executor's example paths now match its `/etc/livepeer` key mounts.

The console listens on `0.0.0.0` **inside** its bridge-network container, while
Docker publishes it only on host loopback by default. Binding the process to
container loopback prevents Docker's published-port forwarding from reaching it.
Coordinator/admin/metrics host bindings are private by default where configured;
operators can explicitly choose their private LAN interfaces.

Sender payment stacks now mount `payer-daemon-db` and explicitly use
`/var/lib/livepeer/payer/payer.db`. Receiver stacks keep their separate database
and explicitly configure the orchestrator payee when its redemption wallet can
differ. Do not point independent receivers at one writable financial database.

Generated member bundles use `tztcloud/livepeer-pool-member-agent:v2.0.0` with
`REGISTRY`/`TAG` overrides, a writable directory-mounted enrollment token, and no
unsupported optional Compose include. The image includes Docker CLI 29.8.0 and
Compose 5.5.1 from the pinned Docker CLI image. The agent and runner projects have
separate names and a shared network, so runner `--remove-orphans` never deletes
the agent. Empty desired state stops only the runner project and preserves
volumes. See [member instructions](../../pool-member-agent/README.md).

Registry publisher mode remains a supported **identity-diagnostics** mode. It
does not sign manifests. The root registry example intentionally retains it;
use the resolver Compose for route selection, with `manifest_url` overlays when
bypassing chain serviceURI lookup. Signing belongs to secure-orch-console.

## Upgrading an existing installation

Record the deployed image digests and source revision before changing anything.
A v2.0.0 tag can move and is distinct from protocol major 4. Apply the
[settlement-domain migration procedure](../../docs/design-docs/settlement-domain-release.md)
when changing the paid-path contract; old clients are not made compatible by a
shared image tag.

Before recreating a sender that previously had no volume, stop its writes and
back up its actual `--db` file and related state from the old container. Migrate
that state to the new payer volume and verify ownership before starting the new
sender. Attaching an empty volume without migrating the ledger loses persisted
mint/authorization idempotency. Do not remove the old container until migration
is verified; never use `down -v` for a financial upgrade.

Existing pool members must drain before switching bundle project names. Preserve
tokens, model storage, host-admission state and the old Compose configuration;
stop the old runner project without deleting volumes before starting the new
bundle. This avoids running both old and new runner projects on the same GPUs.
The token now lives at `/workspace/enrollment-token` and rotates by atomic rename.

Set real operator addresses and URLs in copied configs. Do not deploy the example
orchestrator addresses, smoke keystores or placeholder domains. In the pool stack,
proxy member HTTPS to port 8084, public manifests to 8081 and broker HTTPS to 8082.
Never proxy the public member domain to the controller admin port 8083.

`protocol.sock` and `payment-daemon.sock` are local Unix sockets. A named volume
or bind mount does not carry a socket across hosts. The pool reconciler requires
operator-provided connectivity to its configured endpoints; these Compose files
do not create a cross-host tunnel. Keep protocol control RPCs private and the
cold keystore on the secure host. This audit did not provision that transport.

## Reproducing validation

From the repo root with Docker Compose and Python 3:

```sh
python3 infra/scripts/check-stacks.py
```

The script discovers and validates 41 active base/overlay models with synthetic
inputs. It ignores deployment `.env` files and starts no containers. It checks
Modules image defaults, sender ledger persistence and host-port conflicts.
Optional `STACK_CLI_DIR` points to source-built component binaries; the checker
parses each applicable service command with `--help` before runtime startup to
catch removed flags. Nine component CLIs were built for this audit.

The first-boot bundle and generated runner files are also passed to Compose:

```sh
CHECK_COMPOSE=1 go -C pool-controller test ./internal/service/memberenrollment
CHECK_COMPOSE=1 go -C pool-member-agent test ./internal/desiredstate
```

The member image was built and its `docker --version` and `docker compose
version` executed. The pool's Docker-first bootstrap test exercises broker
config validation, key readability and rejection of missing key material:

```sh
./infra/scenarios/pool-orchestrator/test.sh
```

These checks establish source/configuration compatibility. They do not certify
production DNS, TLS, hot-wallet permissions, chain balances, GPU runner images,
or a live paid-work flow. No chain transactions or production deployment were
performed by this audit.

## Observed published tags

Docker Hub was queried read-only on 2026-09-15. All 11 Modules v2.0.0 tags below
resolved. This snapshot records availability, not proof that these artifacts
contain the uncommitted stack fixes or form an approved coordinated release.
Digests can differ from earlier session observations because tags are mutable.

| Image (`tztcloud/`) | Observed digest |
|---|---|
| `livepeer-capability-broker:v2.0.0` | `sha256:5911be4f108b99ef23106a522825875e74fbda9ab39ab06f30e30dad6525a0b4` |
| `livepeer-conformance:v2.0.0` | `sha256:a864351560eb27ddc939802536962bd9712c6af209fce9a554a595a4c584cbae` |
| `livepeer-orch-coordinator:v2.0.0` | `sha256:148decfec90574f01c63858720d4191679634ab0249c11101625db998b78b91c` |
| `livepeer-payment-daemon:v2.0.0` | `sha256:f76ec064cd709f65a9e420ce268260f1c447abfd15597507cf89c0c24b0580db` |
| `livepeer-pool-controller:v2.0.0` | `sha256:2e2a7b4970746cd2d89af469430487ecc830b95534a243348a93bb3dec4b8046` |
| `livepeer-pool-member-agent:v2.0.0` | `sha256:8abe95731133ab69115676adf833f610414f99dbf2ee96f7df9b3226f516a230` |
| `livepeer-pool-payout-executor:v2.0.0` | `sha256:9d3967efab98469c60a2b07f293b88212cff1c16888a27efb8e820a1dbd8a0ff` |
| `livepeer-pool-reconciler:v2.0.0` | `sha256:5047824ae5b909f99517fa40eb88e8e06c460cfd2d6ad7cab17cb6c256eae2b7` |
| `livepeer-protocol-daemon:v2.0.0` | `sha256:f4cf038f275481308fd264a1556c04d8333f1f526e444af4052aab909ed9d5c6` |
| `livepeer-secure-orch-console:v2.0.0` | `sha256:92f754cfca0a877f99be521c9da62e687c2c63343c2dbab4a2e563613363e7b0` |
| `livepeer-service-registry-daemon:v2.0.0` | `sha256:a3e67795ff478faf34d4fe09321178cd39617d2459f07839bd0869bb5c23be3c` |

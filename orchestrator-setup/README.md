# Standalone orchestrator broker setup

Generate the files for a new standalone broker with `plan → generate → validate`.
The output contains the broker and payment receiver Compose stack, encrypted hot
wallet, settlement/sealing keys, admin token, offers configuration, optional
certification fixtures, and coordinator/ingress handoff files. This first profile
uses an existing orchestrator identity and coordinator. It does not generate a
cold-key host, pool services, or workload containers.

Generation and validation run with networking disabled and no Docker socket.
Nothing starts services, funds wallets, changes DNS, submits transactions, or
publishes manifests. The embedded Docker CLI is only used for `compose config`.

## Prepare inputs

Create separate private input and output directories, outside the repository.
Copy `setup.compose.yaml` and `.env.example` to an operator directory, renaming
the latter `.env`. Set the directory paths, real `ORCHESTRATOR_ADDRESS`, deployment
name, public `BROKER_URL`, and private `BROKER_ADMIN_URL`. The private URL must be
reachable by the coordinator and allow its authenticated admin calls. Both URLs
are canonical HTTPS origins without a path, explicit port, or trailing slash.

Place these files in `INPUT_DIR`:

| File | Contents |
| --- | --- |
| `rpc-urls` | One HTTPS Arbitrum One RPC endpoint per line; keep provider credentials in this protected file. |
| `offers.json` | A nonempty array of broker offers. Copy `examples/offers.json`, then set the model selector, prices, capacity and certification recipe. |
| `images.json` | The two immutable runtime image references, as shown below. |
| `fixtures/` | Optional certification fixtures, preserving each recipe's relative directory/name. |

Offer fields follow the [broker contract](../capability-broker/examples/host-config.offers.example.yaml).
Capabilities remain open-ended; the example chat offer is not a closed catalog.
Do not add backend URLs, extractors or runner paths to the offers file: runners
declare those when enrolled later. The JSON files emitted with `.yaml` extensions
are valid YAML and are validated by the real component parsers.

Resolve images before running offline setup. For example, from the repository
root with Docker installed:

```sh
docker pull tztcloud/livepeer-capability-broker:v2.0.0
docker pull tztcloud/livepeer-payment-daemon:v2.0.0
BROKER_IMAGE=$(docker image inspect tztcloud/livepeer-capability-broker:v2.0.0 --format '{{index .RepoDigests 0}}')
PAYMENT_IMAGE=$(docker image inspect tztcloud/livepeer-payment-daemon:v2.0.0 --format '{{index .RepoDigests 0}}')
```

Write those two references into the protected input directory's `images.json`:

```json
{
  "capability-broker": "tztcloud/livepeer-capability-broker@sha256:<resolved-broker-digest>",
  "payment-daemon": "tztcloud/livepeer-payment-daemon@sha256:<resolved-payment-digest>"
}
```

The placeholders must be replaced. Build the setup image with that same broker
reference so its embedded parser matches your selected runtime:

```sh
docker build -f orchestrator-setup/Dockerfile \
  --build-arg BROKER_IMAGE="$BROKER_IMAGE" \
  -t livepeer-orchestrator-setup:local .
```

`KEYGEN_IMAGE` can also be supplied as an immutable secure-orch-console image
reference. Only its existing wallet-generation executable is copied; no signing
service or cold wallet runs in this tool. The manifest records the broker parser's
binary hash. Arbitrary image lock overrides are not proof of binary compatibility;
rebuild setup when changing the broker runtime release.

## Generate files

From the operator directory containing `setup.compose.yaml` and `.env`:

```sh
docker compose -f setup.compose.yaml run --rm setup plan
docker compose -f setup.compose.yaml run --rm setup generate
docker compose -f setup.compose.yaml run --rm setup validate
```

`plan` reads inputs, reports material creation/reuse, and checks output storage and
embedded tools without writing. `generate` journals material, renders a package,
and runs the real broker and Compose parsers before selecting the revision.
`validate` rechecks the selected package's hashes, permissions and parsers.

```text
OUTPUT_DIR/
  .setup/                       # identity binding and durable private material
  current-revision              # relative path to the validated package
  revisions/<fingerprint>/
    compose.yaml
    config/host-config.yaml
    secrets/broker/
    secrets/receiver/
    fixtures/
    operator/
      broker-admin.token
      coordinator-broker.json
      nginx.conf
      README.txt
    deployment-manifest.json
    validation-report.json
```

Everything in this directory is private: Compose includes RPC provider URLs, and
`admin.env` carries the broker's environment-backed bearer. Files are mode 0600;
runtime-mounted config, fixtures and secrets belong to container UID/GID 65532.
Setup runs as container root, including under rootless Docker's ID mapping.
Keep the journal and operator files private to the operator. Each service mounts
only its own secrets.

## Existing material and reruns

With `MATERIAL_MODE=create-if-missing`, setup imports matching files from the input
directory or generates missing material. `reuse` requires all five files:

- `receiver-keystore.json` and `receiver-password` (encrypted V3 hot wallet).
- `broker-settlement.key` (broker-generated secp256k1 key).
- `broker-sealing.key` (64 lowercase hex characters).
- `broker-admin.token` (64 lowercase hex characters).

No cold orchestrator key is accepted as the receiver wallet. Wallet JSON/address
validation does not decrypt an imported keystore or verify its password.

Identical inputs reuse the same revision and key material. Changed offers, URLs,
images, fixtures or generator/parser code produce a new revision with the same
keys. A changed orchestrator identity/deployment name, conflicting imported keys,
or missing/modified journaled keys fails instead of replacing them. A wallet
generation interrupted before key publication preserves its password on retry.
Failed package generation leaves `.pending-*` evidence and does not change
`current-revision`. Back up `.setup/` and the selected revision together; never
delete the journal to bypass an identity conflict. Rotation is not implemented.

## What validation establishes

Offline validation checks input shape, required fixture presence, writable output
space, embedded tools, broker configuration, Compose syntax, file integrity and
private permissions. It does not install Docker or inspect the host daemon, GPU,
ports, DNS/TLS, RPC connectivity, wallet funding, redemption authority, runner
certification, or manifest publication. The report lists these outstanding checks.
Payment-daemon has no equivalent offline configuration parser here; its flags are
rendered from the existing deployment contract and it is not started for validation.

The generated services use three **external persistent volumes**. Provision those
volumes and their UID/GID 65532 ownership before a later deployment; their names
are in the deployment manifest. Preserve the broker state and payment databases
with coordinated backups. No volume is created by setup.

The broker publishes only host-loopback ports 8080 and 9090. `operator/nginx.conf`
is a routing snippet for an existing **host** nginx HTTPS server, not a complete
TLS installation or a container ingress. It blocks public `/admin/`, supports
WebSocket runner attachment, and requires you to supply DNS/certificates. The
private coordinator route must separately permit admin access. QUIC is not enabled
in this initial profile. Merge the coordinator handoff entry into its existing
roster and install the token at the referenced private path; nothing modifies an
existing fleet automatically. These are handoff artifacts, not live validation.

## Development checks

```sh
make -C orchestrator-setup test acceptance
```

Both checks use containers, with synthetic addresses and fake RPC URLs. Acceptance
exercises actual key generation/parsers, imports, repeat generation, configuration
changes, interrupted wallet generation, failed validation, corruption rejection,
ownership and Compose interpolation. Its test harness alone mounts the local Docker
socket to launch isolated setup containers; setup containers never receive it.
The harness supports a local Unix socket, including rootless Docker; override
`DOCKER_SOCKET` if needed. No runtime broker or payment service starts, and test
fixtures are removed afterward. `make publish` requires an explicit `PUBLISH_IMAGE`.

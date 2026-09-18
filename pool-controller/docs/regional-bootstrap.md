# Offline regional bootstrap

Initialize each new controller's persistent store before rendering regional
service credentials/configuration. This command creates the immutable pool ID
without loading runtime configuration, opening a listener or contacting a chain:

```sh
docker run --rm --user 65532:65532 \
  -v eu-controller-data:/var/lib/controller \
  "$CONTROLLER_IMAGE" init-identity --data-dir /var/lib/controller
```

Prepare the volume ownership for the selected container UID first. Repeat for
the US store with a separate volume. Record each returned `pool_id`; repeating
against the same store returns the same identity. Do not substitute a label,
domain name or payout wallet for the generated identity.

For an existing or restored pool, require the recorded identity:

```sh
docker run --rm --user 65532:65532 \
  -v eu-controller-data:/var/lib/controller \
  "$CONTROLLER_IMAGE" init-identity --data-dir /var/lib/controller \
  --expect-pool-id "$EU_POOL_ID"
```

The expected-identity form refuses a missing `pool-controller.db` and a different
stored identity. It never creates a replacement store to satisfy an expectation.
A mismatch requires investigating the mounted store or restoring a consistent
backup, never copying identity metadata into an unrelated database.

`livepeer-pool-controller validate-config --config /path/controller.yaml`
strictly checks known fields and component configuration rules without serving.
The reconciler and payout executor provide the same `validate-config` command;
the portal provides `--validate-config --config /path/portal.yaml`. These checks
are offline: they do not attest remote reachability, runtime key/wallet matching,
funding, receiver history, runner readiness or successful payout. Runtime
identity checks and authorized rollout verification remain required.

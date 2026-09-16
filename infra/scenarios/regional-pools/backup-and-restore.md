# Regional backup, migration and manual restore

This runbook covers deliberate operator maintenance. It does not implement
automatic failover, replacement servers, recovery UI or DNS/certificate renewal.
A normal restart reopens the same stores; a restore from an earlier snapshot is
an incident requiring fresh fencing and financial reconciliation.

## Durable inventory

Archive each entire data volume from the generated manifest, not a hand-picked
subset of database files. The current names below explain why each is required;
new files in the same volume must travel with it.

| Owner | Durable data and recovery purpose |
|---|---|
| Each controller | `pool-controller.db`: immutable pool ID, membership/terms acceptances, enrollment secrets and pending rotation recovery, device assignments/transfers and evidence, source registry, work/round receipts, complete-source proofs, terms publication barriers, immutable approved windows/batches, payout intents and correction holds |
| Each broker | Work accounting DB and outbox, encrypted credential store and generation tombstones, encrypted session/job store including revision intents, offer/frozen-shape state and terms/source admission fences |
| Each receiver | Receiver ledger, its immutable settlement domain, ticket/session/redemption records and chain completeness evidence; **also** the separate redemption transaction-intent DB containing signed nonce/attempt history |
| Each reconciler | Prepared round submissions, durable replay/cursor state and complete-source collection state |
| Each payout executor | Executor state **and** `payout-intents.db`, including pool/wallet/chain binding, signed transaction nonce/hash/attempts and replacement history |
| Shared ownership service | `ownership.db`: per-GPU regional owner, enrollment and generation, transfer state and append-only evidence |
| Shared portal | Portal DB: issuer-origin binding, session/nonces, one-use bootstrap records; the issuer signing key is backed up separately with secrets |

Also archive each host's generated Compose/configuration and service-specific
secret directories, the deployment manifest and tested image provenance,
receiver/executor keystore passwords, broker sealing and settlement signing
keys, issuer private/public trust files, CA/TLS material, and operator/coordinator
credential files. Losing a sealing key can make a preserved broker database
unreadable. Do not copy the secure orchestrator cold key into these archives;
its established secure-host backup procedure remains separate.

Agent-side enrollment directories are separate member-host backups: preserve
`agent-credentials.json` (including an unfinished rotation proof), bundle/env,
runner compose state, the complete `runner-secrets/` directory (records and
initialization markers), and selected-GPU enrollment identity. Preserve named
runner operation-journal volumes together with their keys; the live runner's
journal is encrypted with its assignment master key. Model-download caches may
be rebuilt, but operation journals cannot. Protect these as
credentials. A host must use current controller desired state before starting
managed runners after restore. Never run two restored copies of an enrollment.

Local socket volumes are disposable endpoints, not ledger backups. Recreate
them with the expected ownership; never archive Unix socket files. Preserve
data volumes even if their current service is stopped or a source is retired.

## Establish a consistent stopped-writer boundary

1. Schedule maintenance and retain the current manifest, pool IDs, four source
   IDs, signing-wallet addresses, approved payout/transaction status, last
   complete source/round evidence and ownership transfer audit. Prevent new
   enrollments/transfers and new workload admissions while already accepted
   work is allowed to drain where possible. Do not invoke permanent source
   retirement merely to take a routine backup.
2. Stop the regional controller, reconciler and payout-executor writers; stop
   each broker/receiver writer and observer, then the shared portal/ownership
   writers. Stop or otherwise fence any old agent execution environment that
   may be replaced during this maintenance. Record interrupted/unknown work as
   unresolved; do not label it zero work. In-flight signed chain transactions
   may still confirm after services stop; their persisted intent stores are
   essential for later reconciliation.
3. Disable external supervisors or scheduled jobs that could restart old
   writers. Inspect **all** containers mounting each data volume, not just the
   current Compose project. `docker ps --filter volume=<volume>` must show no
   running writer. Paused containers are still writers for this purpose. Check
   for host-native processes and remote old writers as well. Do not copy live
   Bolt files or assume a heartbeat timeout proves fencing.
4. Record the responsible operator, timestamp, incident/maintenance reference,
   every fenced host, and concrete fencing evidence. Keep the same boundary
   while copying all hosts. The archive checker validates this record's scope;
   it cannot prove that a remote machine is physically fenced.

Build a protected staging tree on the backup host:

```text
snapshot/
  deployment-manifest.json
  fence.json
  operator/                         # generated operator/coordinator material
  hosts/<host>/compose.json
  hosts/<host>/config/...
  hosts/<host>/secrets/...
  volumes/<durable-volume-name>/... # whole volume contents, no socket volumes
```

The fencing record has exactly these fields:

```json
{
  "incident_id": "maintenance-reference",
  "operator": "responsible-operator",
  "fenced_at": "2026-09-16T20:00:00Z",
  "fenced_hosts": ["eu-transcode-broker", "us-transcode-broker", "audio-broker", "llm-broker"],
  "evidence": "Reference to verified stopped writers, supervisor fencing and any replaced agent fencing"
}
```

Use the actual host list and current evidence; the example is not an attestation.
Copy volume contents through the pinned volume-helper image, mounting the
source **read-only**. Container UID 0 is needed to read protected files; it is a
short-lived copy helper, not a financial service. A per-volume intermediate tar
can be created without interpreting filenames as shell commands:

```sh
docker run --rm --network=none --user=0 \
  --mount "type=volume,src=$VOLUME,dst=/source,readonly" \
  --mount "type=bind,src=$BACKUP_STAGE,dst=/backup" \
  "$VOLUME_HELPER_IMAGE" tar -cf /backup/volume.tar -C /source .
```

Unpack that controlled intermediate archive into the corresponding staging
volume directory while preserving permissions, then remove the intermediate
tar from staging. Repeat for every manifest data volume and copy host/operator
material. Inspect any unexpectedly absent database before continuing. An empty
directory alone is not evidence that a formerly active receiver or executor
had no obligations. Keep staging and final archives mode `0700`/`0600` and
account for rootless Docker's subordinate UID mapping when copying files.

Seal the staged inventory, archive it, and store its checksum in a separate
trusted incident/backup record:

```sh
python3 infra/scenarios/regional-pools/backup.py seal "$BACKUP_STAGE" \
  --deployment-manifest "$REGIONAL_MANIFEST"
tar -C "$BACKUP_STAGE" -cf "$BACKUP_ARCHIVE" .
sha256sum "$BACKUP_ARCHIVE"
```

The seal includes each file's hash, all directories, immutable identity
bindings and the fencing record. It refuses missing volume/host directories,
symlinks and sockets. It does not replace the operator's check that the right
volumes were mounted. Encrypt the archive at rest using the established backup
system and retain a protected off-host copy, its independently stored checksum,
decryption access and image provenance. Verify a test restoration periodically.

## Manual restore and migration

1. Fence the old writers **again** before activating a replacement. An archive's
   historical fencing record is not proof about today's old machine. Fence old
   execution containers and reconnect authority before recovering GPU ownership.
   Preserve the old disks and any newer journals for reconciliation.
2. Verify the archive against its independently recorded checksum and the
   intended deployment manifest. The manifest must name the same immutable
   pool/source/signing-wallet/storage identities:

   ```sh
   python3 infra/scenarios/regional-pools/backup.py verify "$BACKUP_ARCHIVE" \
     --deployment-manifest "$REGIONAL_MANIFEST" --sha256 "$RECORDED_SHA256"
   ```

   Verification rejects mismatched identities, missing/changed files, extra
   archive entries, duplicate entries, links, special files and path traversal.
   It never extracts or starts anything. A checksum taken from the untrusted
   archive itself is not an independent integrity check.
3. Extract only a verified archive into a **new empty** protected staging
   directory. Create new empty destination data volumes on the replacement
   host; never overlay a live or nonempty store. Restore complete corresponding
   volume contents and configuration/secrets. Recreate local socket volumes,
   set container UID/GID `65532:65532`, and keep secret modes restrictive. For a
   management-host move, restore the same controller/reconciler/executor/observer
   volumes on the new host; broker receiver volumes remain with their brokers.
   Preserve `deployment_id`, `pool_id`, source IDs and payout wallet identities.
4. Run offline configuration preflight and controller `init-identity
   --expect-pool-id` against **each restored store**. A missing/mismatched store
   is a hard stop; do not copy identity metadata into an unrelated new database.
   Confirm all keystores and sealing keys correspond to their archived roles.
   Expired/revoked service credentials and issuer trust must be deliberately
   rotated; restoring old trust files must not silently undo newer revocations.
5. Keep workload admission, transfers and payout execution held while checking
   the restored state against newer evidence. Re-query chain receipts/nonces for
   every signed receiver and payout intent; reconcile any confirmations after
   the snapshot. Match payout transactions by chain, source wallet, recipient,
   amount, hash and nonce against the approved obligation. Never re-send merely
   because a restored controller still says pending, and never adopt an unknown
   transaction hash as a matching intent. Recover newer intent journals first;
   if they are unavailable, perform documented operator reconciliation and
   retain a hold until the transaction's meaning is established.
6. Recollect complete source history for affected rounds, retaining draining and
   historical sources. Apply the [accounting correction/hold procedure](../../../pool-controller/docs/regional-accounting.md)
   for changed inclusion evidence; do not rewrite approved windows or move debts
   between regions. Verify pending source/terms publication replay against every
   broker. Unavailable sources and unknown work remain explicit holds.
7. Compare the restored ownership authority and all regional assignment,
   credential-generation and transfer records against the newest available
   audit. A stale ownership snapshot cannot establish that newer assignments
   never existed. Keep affected devices unavailable until old execution and
   credentials are externally fenced and the latest owner/generation can be
   established. Use only the existing operator-only
   [fenced recovery procedure](../../../pool-controller/docs/regional-device-transfers.md)
   with concrete evidence; do not use a timeout or hand-edit Bolt generations.
8. Start healthy regional components deliberately, confirm source coverage,
   current terms, model readiness and payout status, then reopen admissions and
   approved execution. Check that the other region's books and agents retain
   their own identities and obligations. Record exactly which checks were local
   and which were performed against live services.

For a brand-new regional deployment, there are no old regional obligations to
migrate. Existing suite/legacy receiver history must not be silently assigned
to a new regional source. Audit its disposition separately; this clean-slate
repository does not copy suite code or configs. An existing controller store's
identity migration and any pre-regional historical records require review before
registering a first regional accounting boundary. Never claim a new empty ledger
is restoration of an existing pool.

## Local evidence and limits

The controller's `TestStoppedRegionalBackupPreservesIdentitiesObligationsAndFences`
copies real stopped EU/US databases with approved member obligations and a
completed cross-region GPU transfer. It verifies unchanged pool IDs, source
history, exact payout intents and the higher ownership generation after reopen;
the obsolete source claim remains rejected. Archive tests verify complete
inventory, checksum/content integrity and wrong pool/source/wallet/storage
refusal. These local tests do not prove that a live host is fenced, that an older
backup includes later broadcasts, or that a member's physical GPU stopped.

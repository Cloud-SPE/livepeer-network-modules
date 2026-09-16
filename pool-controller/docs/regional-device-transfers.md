# Regional GPU transfers

A signed-in source-pool member requests transfer using
`POST /member/v1/hardware/{hardware_unit_id}/transfer`, with same-origin protection
and body `{"pool_id":"SOURCE","destination_pool_id":"DESTINATION","reason":"move GPU"}`.
The destination is immutable for that transfer. The response is HTTP 202 while
work, an unavailable dependency or an agent stop is pending. Read progress at
`GET /member/v1/device-transfers`; administrators can inspect all transfers at
`GET /admin/v1/device-transfers` and transition evidence at
`GET /admin/v1/device-transfers/{id}/history`.

The controller commits the intent, suspends only the selected GPU and marks its
existing placements draining. Ordinary inventory and placement updates cannot
revive that device or create replacement work on it. Its source pool retains all
work, earnings, terms and approved payout obligations.

The source controller records draining ownership at the shared authority, then
pins and contacts every non-retired regional source broker. Each broker durably
fences that device generation against new work and renewal, and proves no active
authorizations, unfinished billing, undelivered final receipts or unqualified
work remain. A missing broker or uncertain proof holds the transfer. Other GPUs
continue serving. Keep the pinned broker identities and service credentials
available until the transfer completes.

After every drain proof, the controller removes only that GPU's enrollment grant.
Every broker must acknowledge its durable per-generation revocation tombstone;
a delayed credential sync cannot restore it. The controller then renders explicit
`stop` services in the agent's desired state. The agent removes those containers
and reports `stopped` only if compose succeeds. The controller requires the exact
current desired-state revision and every affected service's stopped report.
A draining report, old revision, duplicate status or another enrollment's token
cannot stand in for this evidence.

Only then does the source request ownership release to the destination pool,
recording commitments to all broker evidence and the stop report. The destination
must independently accept membership/terms and claim a new exclusive generation
before it activates any placement. No timeout releases a device. Source transfer
progress resumes at startup and every ten seconds. Lost acknowledgments replay
safely; a newer destination assignment can prove that a lost release reply had
already succeeded. Source placements retire after release is established.

A managed agent reboot initially advertises hardware only. It fetches fresh
controller desired state before advertising runner capabilities. Neither an
outage nor an agent restart tears existing containers down; obsolete local runner
files cannot authorize activation. Broker and controller process restarts must
also revalidate ownership, and broker drain/revocation fences survive restart.

## An unavailable source

Uncertain ownership is an explicit hold. The ordinary transfer flow never guesses
that a missing controller, broker or agent has stopped. The shared authority's
operator-only fenced-recovery API exists for documented external fencing: stop
or isolate the old execution environment and remove its ability to reconnect to
all source brokers before supplying revocation and stop evidence. Record the
physical/network fencing action, responsible operator and incident reference.
Do not use a fabricated evidence string, a stale heartbeat or a timeout as proof.
Preserve source databases for remaining accounting; do not move source earnings
into destination books. Restoring an old backup requires renewed external
fencing before either region may activate it. There is no automated failover or
recovery UI.

## Enrolling the selected GPU at the destination

The member enrollment request accepts optional `gpu_uuids`. The controller
canonicalizes and persists that selection; heartbeat, credential rotation and
ordinary enrollment writes cannot broaden it. A selected bundle sets
`POOL_GPU_UUIDS`, and its agent advertises only those devices. The destination
controller independently enforces the selection before contacting the shared
ownership authority. Omitting it retains whole-host discovery.

Extract each enrollment bundle into a different directory. Agent and runner
compose projects and networks derive from the enrollment ID; their state and
compose files stay in that bundle's directory. Selected-GPU bundles use dynamic
edge ports by default to avoid colliding with an agent retaining source GPUs.
Before enabling public HTTPS or RTMPS for a selected enrollment, configure its
own fixed ports and public origin. Certificate and DNS management remain manual.
GPU containers start only after exclusive ownership and current desired state.

## Lost release replies

The authority stores each completed source release and its request evidence in
`ownership.db`, atomically with the ownership transition and audit event. A
retry with the same source pool, enrollment, generation and exact evidence
returns that historical release result even after the destination has claimed
the GPU. It cannot change the destination's current ownership. Conflicting
retry evidence is rejected. On opening an older database, the authority rebuilds
the replay index from its durable release audit events.

This lets a restarted source controller complete its transfer after a lost
HTTPS response without reading the destination's private member or enrollment
fields. Cross-pool GET responses continue to redact those fields. Regional
transfers retain the member wallet; they do not reassign a device to a different
member. Preserve the whole ownership database in backups, including history.

All controller reconciliation loops start after runtime configuration is
installed. In particular, startup must not allow the transfer loop to interpret
an uninitialized configuration as disabled and exit permanently.

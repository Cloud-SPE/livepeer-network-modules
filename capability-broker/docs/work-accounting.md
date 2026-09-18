
## Per-device regional drain

`POST /admin/v1/devices/drain` accepts `pool_id`, `enrollment_id`, `device_id`,
`generation` and an audit `reason`, using the scoped controller or pool-admin
credential. It persists a permanent fence for that assignment generation before
returning source-qualified quiescence evidence. Repeating the request refreshes
evidence, including after the device grant has been revoked. A later exclusive
assignment generation is a different authority; fences are never deleted.

Paid work records the selected runner's device generations in its immutable work
binding. A stale selection cannot create a new binding after draining, and a
session successor cannot bypass the drain by renewing an existing authorization.
Already-admitted work may finish. The response counts active authorizations,
unfinalized operations, undelivered final receipts and unqualified work. All must
be zero before the controller proceeds to revocation and a revision-bound agent
stop. An error or unknown proof is a hold. A sibling device remains eligible.

Unbound admission intents conservatively hold device quiescence until receiver
recovery closes or binds them. Historical work bindings on the same enrollment
without device attribution require manual investigation; absence of attribution
cannot prove that the transferring GPU was idle. Draining is a broker accounting
and admission fence, not proof that the container has stopped. Source ownership
release additionally requires broker revocation acknowledgments and an actual
agent stop report for the controller's stop revision.

With `action: "revoke"`, the same endpoint first verifies quiescence, then
atomically removes that device grant and records a durable generation tombstone
in the credential store. `revoked: true` is the acknowledgment. Every subsequent
credential write filters generations at or below that tombstone, so a delayed
controller snapshot cannot restore revoked authority. Other devices and newer
exclusive assignment generations are unaffected. The accounting fence persists
independently, including across a crash between revocation and acknowledgment.

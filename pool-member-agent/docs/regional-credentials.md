# Regional agent credential recovery

A regional bundle carries `agent-credentials.json`, containing the pool and
enrollment IDs, current enrollment token, attach credential and generation.
`POOL_AGENT_CREDENTIALS_FILE` selects it; for older bundles the agent defaults to
a file beside `POOL_ENROLLMENT_TOKEN_FILE` and initializes it through an
agent-authenticated current-credential read. The file takes precedence over
legacy boot-time environment secrets. Keep it private and persistent.

Before automatic rotation the agent creates and fsyncs a secret 256-bit request
proof in this file. The controller atomically stores the new credential generation
and exact result. If the reply is lost or the process restarts, the old token plus
that proof retrieves the same result, not another rotation. The agent atomically
writes and fsyncs the complete new pair, acknowledges with the new token, then
clears its pending proof. A lost acknowledgement is safe to retry. Failed writes
leave the prior durable recovery request available.

Only the current active generation can be recovered; arbitrary revoked tokens,
a different proof, a retired enrollment or a later manual rotation cannot recover
an old result. These routes use agent enrollment authority, never member wallet
authorization. Controller requests refuse redirects.

The desired-state loop owns rotation and local file writes. Every broker attach
loop reads the shared current credential and receives a broadcast on credential
or runner-state changes. A controller outage holds a pending rotation and leaves
existing runners in place. The next reconcile resumes the pending operation.
Manual portal rotation produces a replacement bundle. The installer downloads
it first, stops the existing enrollment's agent, then replaces its files and
restarts that agent, avoiding a concurrent old credential writer. Running workload
containers remain managed separately; this is not a GPU ownership transfer.

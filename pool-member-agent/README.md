# pool-member-agent

Host-side agent for connected runners. It has two jobs:

1. **Attach.** Connect **outbound** to a broker, declare what this host runs,
   and serve the work the broker dispatches back down the same connection.
2. **Reconcile.** Ask `pool-controller` what this host *should* be running,
   make it so with `docker compose`, and report what happened.

The two run alongside each other, not one inside the other, because they answer
to different things. The tunnel keeps the broker's view of this host current; the
reconcile loop keeps this host's containers matching what the pool decided. A
broker restart must not stop the host reconciling, and a controller outage must
not drop the tunnel.

Job 2 is optional. With no controller configured the agent does job 1 only,
against a runner set declared locally — which is the orchestrator's own hardware
("a pool of one"). One bundle shape serves both deployments (plan 0043
decision 2): the same binary, the same variables. The only difference is who
minted the attach credential — the pool controller, or the broker's own
`POST /admin/v1/enroll`.

The agent never opens a listener, never holds a price, and never decides
what is sold. It holds no policy at all: every decision — which template,
which GPU, when to withdraw one — was made upstream, so an agent that
disagrees with the controller is a bug in the agent. Only outbound
connectivity is required: no DNS entry, no TLS certificate, no router
forwarding.

## Configuration

Attach (always):

| Variable | Meaning |
|---|---|
| `LIVEPEER_BROKER_URL` | Broker base URL; the WebSocket transport. |
| `LIVEPEER_BROKER_QUIC_ADDR` | Broker QUIC address. Preferred when set; the WebSocket is the egress-friendly fallback. |
| `LIVEPEER_ATTACH_CREDENTIAL_FILE` | File holding the attach credential from the bundle. (`LIVEPEER_ATTACH_CREDENTIAL` inline exists for throwaway runs.) |
| `LIVEPEER_HOST_ID` | Stable host id; defaults to the hostname. Must match the enrollment when the store records one. |
| `LIVEPEER_RUNNERS_FILE` | JSON array of runner declarations (below). Locally declared runners; a pool-managed host has these replaced on the first reconcile. |
| `LIVEPEER_RUNNER_URL` | Single-runner shorthand: the runner's base URL. Its contract says the rest. `LIVEPEER_RUNNER_LOCAL_ID` names it (default `runner-0`). |
| `LIVEPEER_REFRESH_EVERY` | How often to rebuild the document and re-send it if it changed. Default `1m`. |

Pool-managed (set all three and the reconcile loop starts):

| Variable | Meaning |
|---|---|
| `POOL_CONTROLLER_URL` | Controller base URL — its member listener. |
| `POOL_ENROLLMENT_ID` | This host's enrollment. |
| `POOL_ENROLLMENT_TOKEN_FILE` | File holding the enrollment token. (`POOL_ENROLLMENT_TOKEN` inline also works.) The agent **rewrites this file** when it rotates. |
| `POOL_COMPOSE_FILE` | Where the generated compose file goes. Default `runners.compose.yaml`. |
| `POOL_COMPOSE_BINARY` | For a host whose docker is called something else; default is `docker compose`. Extra args are appended. |
| `POOL_POLL_EVERY` | Desired-state poll interval. Default `30s`. |
| `POOL_POLL_TIMEOUT` | Per-request timeout. Default `30s`. |
| `POOL_ROTATE_EVERY` | Credential rotation cadence. Default `24h`. |

## Declaring runners

This is the local path — the orchestrator's own hardware, or a dev host. A
pool-managed host does not use it: the controller supplies the runner set.

An operator says **where** a container is. That is all. What it serves —
endpoint path, transports, work unit, extractor, readiness recipe, the
model it loaded — is the runner's to say, and it says so by serving its
own capability entry at `GET /.well-known/livepeer-runner`
([`runner-contract.md`](../livepeer-network-protocol/protocols/runner-contract.md)).
The agent reads that once at attach and relays it, adding only what the
host knows: which container (`local_id`), which GPUs back it (`devices`),
and whether the pool is withdrawing it (`draining`).

```json
[
  { "local_id": "chat",    "url": "http://vllm:8000",
    "devices": ["GPU-8f3c…"] },
  { "local_id": "whisper", "url": "http://whisper:9000" },
  { "local_id": "vod",     "url": "http://transcode:8080" }
]
```

There is no other mechanism. The adapter profiles this agent used to
carry — `openai-compatible`, `transcode` — put runner facts in the agent,
where changing one meant shipping a new agent to every member. They are
gone, and a runner that does not serve its contract is **omitted and
named**:

```
RUNNER HAS NO CONTRACT: runner "vod" at http://transcode:8080 has no usable
contract: GET http://transcode:8080/.well-known/livepeer-runner returned
404; a runner must serve its contract there — it cannot attach until it
serves GET /.well-known/livepeer-runner (runner-contract.md)
```

That line is the inventory of runners to fix. It does not fail the
attach: the host's other runners still serve, and a host with nothing
resolved attaches hardware-only, visible on the broker as connected and
serving nothing.

## The desired-state loop

On a pool-managed host the runner set is live state, not configuration.

```
GET /member/v1/enrollments/{id}/desired-state    (enrollment token, ETag)
  → {revision, services[]{name, compose_fragment, device_ids, models[],
                          capability, identity, draining}}
  → write runners.compose.yaml
  → docker compose pull
  → docker compose up -d --remove-orphans
POST /member/v1/enrollments/{id}/status
  → {revision, services[]{name, status, detail}}
```

The revision doubles as the ETag, so a host that polls often and changes rarely
mostly gets a `304` and no body.

The compose file is written whole and renamed into place. A compose file caught
half-written by a concurrent `docker compose up` is a host that stops serving
for reasons nobody can reconstruct afterwards.

Each service's GPUs are pinned by UUID, because the controller pinned them: a
host with two cards running two workloads must not have both services claim
both devices, and a UUID is the only identifier stable across reboots.

**Reconciling never kills the host.** A controller that is down leaves this
host running exactly what it was running. That is the right answer — the last
desired state is still the pool's most recent instruction, and tearing
containers down because the control plane is unreachable would turn a
control-plane outage into a data-plane one. The one failure the agent cannot
recover from is a rejected token (`401`): it says so plainly rather than
retrying in silence until someone notices the host stopped earning.

**Withdrawal is sequenced, and the order is the whole point.** A service
leaving desired state is marked `draining` in the attach document **first**, so
the broker stops dispatching to it while it can still serve; only then does the
container stop. The agent also wakes a live attach session on change rather
than waiting for `LIVEPEER_REFRESH_EVERY` — the width of that tick is exactly
the window in which the broker would keep sending work to a runner the pool has
already withdrawn, which is the window `runner-attach` §7.1 exists to close. A
draining service is still rendered into the compose file: the pool wants it
gone, but dropping it here would kill it mid-request.

**The agent rotates its own credential** every `POOL_ROTATE_EVERY`, well inside
any plausible token lifetime. A host that waits for expiry has already stopped
earning by the time anyone can act on it. The new token is written to a temp
file and renamed, and the old one stays in place until the new one is safely
down — the replacement is returned exactly once, and losing it between the
response and the disk would leave this host unable to authenticate at all.

## What the pool asks of the host

Running `docker compose` on the member's behalf means the agent mounts the
Docker socket. That is a real grant of privilege on that machine and the member
bundle's README says so plainly rather than burying it. The agent starts only
images named by the pool's template catalog, pinned to the GPUs assigned to
that member, and `runners.compose.yaml` can be read at any time to see exactly
what is running and which template and assignment it came from.

> **Nothing starts from the shipped catalog yet.** The five templates in the
> repo's `templates/` directory carry no `runner_compose` block — the v1 images
> and model ids are still open (`lnm-v12`) — so the fragment they render has no
> `image`. The loop itself is built and tested; a pool that wants containers to
> actually come up must add `runner_compose.image` to the templates it enables.

## What the agent puts on the wire

One attach document per connection
([`runner-attach.md`](../livepeer-network-protocol/protocols/runner-attach.md)),
sent as the first frame and re-sent whenever it changes — a GPU
appearing or failing is the common case, and on a pool-managed host so is a
placement change. The broker answers with a `register_result`; the agent logs
every rejection with the field and both sides, because that line is the
operator's feedback loop:

```
CAPABILITY REJECTED whisper (openai:audio-transcriptions): extractor_unknown
  /capabilities/1/work_unit/extractor/type declared="whisper-seconds" expected="one of: …"
```

A rejected capability gets no work; the rest of the host keeps serving.

Dispatched requests carry `Livepeer-Runner-Local-Id`, and the agent
routes on it — never on the path, since one host can serve the same
capability id under two models. The header is stripped before the
request reaches the container.

The agent does not report hardware to `pool-controller`. GPU inventory rides
the attach document, so the broker — and through it the controller — sees
exactly what the runner declared, in one place, already validated against the
attach contract.

## Build / test

```sh
make build
make check      # go test ./... plus the contract check below
```

`make check-attach-docs` validates `testdata/attach/*.json` — the real
documents this agent builds — against the protocol module's JSON Schema.
The broker's own test suite additionally feeds those same goldens
through its validator, so the two independent implementations are
checked against each other.

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

The agent never holds a price or decides what is sold. Placement policy belongs
to the pool. Attach and broker-dispatched job work require only outbound
connectivity. External session data planes additionally need the optional
public edge described below.

## Public endpoints for external sessions

The caller opens and pays for a session through the broker, then connects to
the runner's advertised endpoint for the session data. The broker does not
relay that media. The agent can terminate TLS and forward HTTP/WebSocket
traffic at `/r/<local_id>/` to the corresponding runner on the Docker network.
RTMPS ingest uses a separate TLS listener on port 1936 and forwards to the
runner declaring `rtmp_port`.

**The operator supplies DNS, certificates, and inbound connectivity.** The
controller does not run DNS and the agent does not issue or renew certificates.
Automatic provisioning is outside the supported scope. A CGNAT host without
an externally reachable endpoint should leave `LIVEPEER_PUBLIC_URL` empty;
it can still serve broker-dispatched jobs. The pool excludes hosts without
that URL from session placement (`host_not_public`). A declared URL alone is
not proof of reachability: session certification policies must include the
appropriate `reach` step to dial the advertised endpoint from the broker.

For the generated member bundle:

1. Point a hostname at the member's reachable public IP. Forward/open the
   HTTPS port, and port 1936 if serving RTMPS ingest.
2. Put the certificate chain in `./edge/tls.crt` and its private key in
   `./edge/tls.key`. The bundle mounts this directory read-only into the agent.
3. Set `LIVEPEER_PUBLIC_URL=https://member.example.com:8443` in `.env` for
   the default published port. Alternatively set `LIVEPEER_EDGE_PORT=443`
   and use `https://member.example.com`. This URL is an origin, with no path.
4. Recreate the agent with `docker compose up -d --force-recreate pool_member_agent`.
   Confirm session certification succeeds before sending production traffic.

Desired state supplies each session runner with
`LIVEPEER_PUBLIC_URL=<origin>/r/<local_id>` and, for an ingest runner,
`LIVEPEER_PUBLIC_RTMP_URL=rtmps://<host>:1936`. The runner uses these to build
its runtime descriptor. Keep the external RTMPS port at 1936: changing the
bundle port mapping alone does not change the advertised URL. These listeners
cover HTTP/WebSocket and RTMPS; other media transports such as WebRTC UDP
require their own runner-specific network setup.

Renew certificates using your existing certificate tooling and restart the
agent after replacing the files: certificates are loaded at startup, with no
hot reload. Schedule that restart around active sessions. If the URL is set
but the certificate cannot be loaded, the agent logs `PUBLIC EDGE CANNOT START`
and continues its outbound attach; a valid public edge is still required for
session traffic.

| Variable | Meaning |
|---|---|
| `LIVEPEER_PUBLIC_URL` | Public HTTPS origin; empty disables the edge. |
| `LIVEPEER_EDGE_LISTEN` | Agent HTTPS listener, default `:8443`. |
| `LIVEPEER_EDGE_TLS_CERT` | Certificate chain path, default `/etc/livepeer/edge/tls.crt`. |
| `LIVEPEER_EDGE_TLS_KEY` | Private key path, default `/etc/livepeer/edge/tls.key`. |
| `LIVEPEER_EDGE_RTMPS_LISTEN` | Agent RTMPS listener, default `:1936`. |
| `LIVEPEER_EDGE_PORT` | Bundle Docker host port mapped to agent port 8443; default 8443. A selected-GPU bundle sets `0` (dynamic) for both edge ports so another enrollment's agent can share the host; set unique fixed ports before enabling a public URL. |
| `LIVEPEER_EDGE_RTMPS_PORT` | Bundle Docker host port mapped to agent port 1936; keep 1936 for generated descriptors. |

## Configuration

Attach (always):

| Variable | Meaning |
|---|---|
| `LIVEPEER_BROKER_URL` | Broker base URL; the WebSocket transport. |
| `LIVEPEER_BROKER_URLS` | Comma-separated distinct HTTPS broker origins. When set, the agent runs one attach loop per broker, all sharing one runner set and credential. Cannot be combined with `LIVEPEER_BROKER_QUIC_ADDR`. |
| `LIVEPEER_BROKER_QUIC_ADDR` | Broker QUIC address. Preferred when set; the WebSocket is the egress-friendly fallback. |
| `LIVEPEER_ATTACH_CREDENTIAL_FILE` | File holding the attach credential from the bundle. (`LIVEPEER_ATTACH_CREDENTIAL` inline exists for throwaway runs.) |
| `LIVEPEER_HOST_ID` | Stable host id; defaults to the hostname. Must match the enrollment when the store records one. |
| `LIVEPEER_RUNNERS_FILE` | JSON array of runner declarations (below). Locally declared runners; a pool-managed host never activates them — it attaches hardware-only until its first desired-state fetch. |
| `LIVEPEER_RUNNER_URL` | Single-runner shorthand: the runner's base URL. Its contract says the rest. `LIVEPEER_RUNNER_LOCAL_ID` names it (default `runner-0`). |
| `LIVEPEER_REFRESH_EVERY` | How often to rebuild the document and re-send it if it changed. Default `1m`. (`--refresh-every` overrides it; `--version` prints the build version.) |

Pool-managed (set the controller URL, enrollment id and a token, and the reconcile loop starts):

| Variable | Meaning |
|---|---|
| `POOL_CONTROLLER_URL` | Controller base URL — its member listener. |
| `POOL_ENROLLMENT_ID` | This host's enrollment. |
| `POOL_ENROLLMENT_TOKEN_FILE` | File holding the enrollment token. (`POOL_ENROLLMENT_TOKEN` inline also works.) Boot-time only once `agent-credentials.json` exists: rotation does not rewrite this file. |
| `POOL_AGENT_CREDENTIALS_FILE` | Durable credential file (enrollment token, attach credential, generation); defaults to `agent-credentials.json` beside the token file. When present it takes precedence over the token and attach-credential variables, and the agent **rewrites this file** when it rotates. See [regional credentials](docs/regional-credentials.md). |
| `POOL_GPU_UUIDS` | Comma-separated GPU UUIDs this enrollment covers. When set, only hardware units with those GPU UUIDs are reported in the attach document; empty reports all hardware. |
| `POOL_COMPOSE_FILE` | Where the generated compose file goes. Default `runners.compose.yaml`. |
| `POOL_COMPOSE_BINARY` | For a host whose docker is called something else (run as `<binary> -f <file> …`); default is `docker compose`. |
| `POOL_POLL_EVERY` | Desired-state poll interval. Default `30s`. |
| `POOL_POLL_TIMEOUT` | Per-request timeout. Default `30s`. |
| `POOL_ROTATE_EVERY` | Credential rotation cadence. Default `24h`. |

The signup bundle bind-mounts `/var/lib/livepeer-resource-admission` at the
same path inside the agent. For an opted-in template the agent creates stable
lock-file inodes there before `docker compose up`; generated runner services
mount the directory read-only and only the declared lock files read-write.
Do not delete or recreate this directory while runner containers are active.

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
  → {enrollment_id, revision, services[]{name, compose_fragment, device_ids,
                          models[], capability, identity, draining, stop}}
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
earning by the time anyone can act on it. The new enrollment-token/attach
credential pair is written to `agent-credentials.json` via a temp file, fsync
and rename. A secret request proof is persisted before the rotation request, so
a lost reply or a restart replays the same request and retrieves the same
result instead of losing the credential; see
[regional credentials](docs/regional-credentials.md).

## What the pool asks of the host

Running `docker compose` on the member's behalf means the agent mounts the
Docker socket. That is a real grant of privilege on that machine and the member
bundle's README says so plainly rather than burying it. The agent starts only
images named by the pool's template catalog, pinned to the GPUs assigned to
that member, and `runners.compose.yaml` can be read at any time to see exactly
what is running and which template and assignment it came from.

The agent image includes the Docker CLI and Compose plugin. The bootstrap starts
only the agent; it does not include a not-yet-generated runner file. Agent and
runners use separate Compose projects (`livepeer-agent-<id>` and
`livepeer-runners-<id>`) on one `livepeer-member-<id>` network, where `<id>` is
the first 16 hex digits of SHA-256 over the enrollment ID. The agent creates the
network; runners reference it as external. Runner reconciliation cannot remove
the agent as an orphan. An empty desired state stops the runner project without
removing its volumes or the shared network.

The enrollment token is `/workspace/enrollment-token` and the durable
credential file `/workspace/agent-credentials.json`, both on the writable bundle
directory mount. A read-only single-file mount prevents atomic credential
rotation. The bootstrap image defaults to
`tztcloud/livepeer-pool-member-agent:v2.0.0`; `REGISTRY` and `TAG` can be set in
its `.env` for an explicitly selected build.

For an existing bundle, drain work before replacing it. Preserve credentials,
sealing/admission state, model storage, and the old Compose project name. Stop the
old runner project without `-v` before enabling the new project names; otherwise
old runners may stay up alongside the new ones. Retain the original files until
the upgraded agent has reconciled and reattached successfully. When leaving,
retire/drain first, then stop `runners.compose.yaml` and the agent Compose project.

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

Desired-state revisions are acknowledged only after successful compose application
and status delivery. Failed applications, failed reports, and draining services
remain eligible for retry. A draining report means the container is still running;
a `stop` instruction removes only that service from compose and reports `stopped`
after compose succeeds. Regional controllers require transfer evidence before
issuing that instruction.

### Bootstrap runtime selection

Start a downloaded regional bundle with `sh start.sh`. The portal installer
runs this automatically. It writes a private Compose override after checking
host PCI inventory: NVIDIA GPU hosts request the NVIDIA container runtime;
Intel-only and CPU hosts do not. An installed NVIDIA GPU with an unavailable
driver fails before the agent starts. Install its driver and Container Toolkit,
then retry. Normal Docker restarts retain the selected runtime; rerun `start.sh`
when changing host hardware or updating the bundle.

The agent's read-only `/host-dev` mount supplies device metadata, including the
numeric group of an Intel render node. Managed Intel runners receive only the
assigned node, mapped to `/dev/dri/renderD128`, and that supplemental group.
This prevents a runner assigned one card from opening another card's node.
NVIDIA runner assignments remain UUID-pinned. The inventory agent is trusted
with the Docker socket and host-wide discovery; it is not an inference runner.

### Incremental WebSocket responses

When requested by an updated broker, the agent sends generic response headers,
32-KiB-or-smaller body chunks, and terminal trailers using `chunks-v1`. Eight
unacknowledged chunks per request bound buffering; broker acknowledgements resume
reading from the local runner. Cancellation closes that local HTTP request.
No model-specific or SSE parsing is performed. Older brokers retain the legacy
buffered response; upgrade both ends before expecting incremental delivery.
Request uploads and QUIC framing are unchanged. See
[response framing](../livepeer-network-protocol/protocols/runner-attach.md#72-response-framing).

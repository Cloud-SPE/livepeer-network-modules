# Operator runbook — Daydream Scope on Livepeer

Step-by-step for standing up the Daydream Scope capability end-to-end.
Two stacks: the orchestrator-side stack (runs the GPU workload + payment
receiver + registry publisher + broker) and the broadcaster-side stack
(this gateway + a payment sender). The two halves communicate only over
the Livepeer wire protocol.

## Prereqs

| Side | Requirement |
|---|---|
| Orch | NVIDIA GPU with ≥24 GB VRAM (Scope's minimum for the default video-diffusion pipelines) |
| Orch | Docker with the `nvidia` runtime configured |
| Orch | HuggingFace account + an `HF_TOKEN` with default access (used to fetch Cloudflare TURN credentials) |
| Orch | Arbitrum One orch eth address registered on-chain |
| Broadcaster | Arbitrum One eth address funded with ETH (for tx fees) + LPT (for tickets), keystore JSON + passphrase file |
| Broadcaster | Arbitrum One RPC endpoint URL |

## Step 1 — Orch host

```bash
cd capability-broker/compose

# 1. Copy the host-config template and uncomment the daydream:scope:v1 block.
cp ../examples/host-config.example.yaml ./host-config.yaml
$EDITOR ./host-config.yaml
#    Required edits:
#      identity.orch_eth_address: 0x...your orch address
#      capabilities: uncomment the daydream:scope:v1 entry

# 2. Set the env the compose stack needs.
export HF_TOKEN="hf_..."
export ORCH_ETH_ADDRESS="0x..."

# 3. Bring up the stack.
docker compose -f daydream-scope.yaml up -d
```

Verify:

- `docker compose ps` shows scope, capability-broker, payment-daemon,
  service-registry-daemon all healthy.
- The broker's `/registry/offerings` endpoint reports the
  daydream:scope:v1 capability:
  ```bash
  curl http://localhost:8080/registry/offerings | jq .
  ```
- The broker's `/registry/health` endpoint reports the tuple as `ready`:
  ```bash
  curl http://localhost:8080/registry/health | jq .
  ```
- Scope has loaded a pipeline (this is automatic on first session-open,
  but you can pre-warm by exec'ing into Scope and calling
  `/api/v1/pipeline/load`).

## Step 2 — Publish the signed manifest through orch-coordinator

The orch must publish a **signed** manifest that includes the
`daydream:scope:v1` tuple and points at this broker's externally-routable URL.
In the rewrite, this is not a direct handwritten service-registry payload.
The flow is:

1. `orch-coordinator` scrapes `/registry/offerings`
2. operator reviews the candidate manifest
3. candidate is carried to `secure-orch`
4. cold key signs it
5. signed manifest is uploaded back to `orch-coordinator`
6. `service-registry-daemon` resolves the published well-known URL

Relevant references:

- [`../../docs/design-docs/architecture-overview.md`](../../docs/design-docs/architecture-overview.md)
- [`../../docs/design-docs/trust-model.md`](../../docs/design-docs/trust-model.md)
- [`../../docs/design-docs/backend-health.md`](../../docs/design-docs/backend-health.md)
- [`../../orch-coordinator/docs/operator-runbook.md`](../../orch-coordinator/docs/operator-runbook.md)

Broadcaster-side resolution will only return routes that pass:

- Layer 1: signed-manifest validity
- Layer 2: broker live health from `/registry/health`

## Step 3 — Broadcaster host

```bash
cd daydream-gateway

export BROADCASTER_ETH_KEY=/path/to/keystore.json
export BROADCASTER_ETH_PASSPHRASE_FILE=/path/to/passphrase.txt
export ETH_RPC_URL=https://arb1.arbitrum.io/rpc

docker compose -f compose.yaml up -d
```

The gateway is now listening on `:9100` and serves the embedded Scope UI
from `/`. Open `http://localhost:9100/` (or your gateway's
externally-routable URL) directly.

Verify:

```bash
curl http://localhost:9100/healthz
# {"status":"ok"}

curl http://localhost:9100/v1/orchs | jq .
# Should list one or more orchs advertising Daydream Scope.

curl http://localhost:9100/v1/orchs/metrics
# Prometheus text for gateway-local Layer 3 route health.
```

Interpret the surfaces like this:

- `/v1/orchs` shows the current resolver-backed candidate set plus
  gateway-local route-health summary
- `service-registry-daemon` has already removed any route that fails signed
  manifest or broker live-health checks
- `/v1/orchs/metrics` shows gateway-local cooldown and route-outcome counters

## Step 4 — Open a session

The embedded UI does this automatically. If you want to drive it by hand:

```bash
SESSION=$(curl -s -X POST http://localhost:9100/v1/sessions | jq -r .session_id)
echo "session: $SESSION"

# Pass that session_id on every subsequent /api/v1/* call:
curl -H "X-Daydream-Session: $SESSION" \
     http://localhost:9100/api/v1/pipeline/status | jq .
```

The embedded UI does not need to know about `session_id` explicitly —
it opens `/v1/sessions` and sends `X-Daydream-Session` on subsequent
`/api/v1/*` requests automatically.

## Network surfaces summary

| Surface | Who talks to it | Reach |
|---|---|---|
| Broker `:8080` (paid HTTPS) | Broadcasters | Public |
| Broker `:9090` (metrics) | Monitoring | Operator-internal |
| Scope HTTP API | Broker | `scope-control` net only |
| Scope WebRTC UDP | Cloudflare TURN | `egress` net only |
| Gateway `:9100` | Customer SPAs / CLIs | Broadcaster-side (whatever you expose) |
| Cloudflare TURN public UDP | Browsers + Scope | Cloudflare's responsibility |

The orch's GPU workload (Scope) is **never** directly reachable by a
customer browser. All control traffic is brokered; all media traffic
is relayed by Cloudflare. The orch's exposure surface is the broker's
HTTPS listener — a small, hardened Go HTTP server.

## Cost model

Operator-paid expenses:

- GPU host (orchestrator).
- Cloudflare TURN bandwidth (orch egress). Roughly ~6 Mbps per
  concurrent session at 720p/24fps. Cloudflare has a free tier; above
  that, billed per GB per their published rates.
- HuggingFace account (free tier sufficient for HF_TOKEN issuance).

Broadcaster-paid expenses:

- Arbitrum gas + LPT for tickets (standard Livepeer payment surface).

## Failure modes

| Symptom | Likely cause | Fix |
|---|---|---|
| `GET /v1/orchs` returns empty | no routes survive manifest or live-health filtering | Check the daemon's logs; verify the signed manifest is published; check broker `/registry/health`; verify the tuple is `ready` |
| `POST /v1/sessions` returns 502 | Payment minting succeeded but broker rejected payment | Check broker logs for `livepeerheader` error code; verify broadcaster has LPT |
| `POST /v1/sessions` returns 503 | No orchs available for the `(capability, offering)` tuple after Layer 2 or Layer 3 filtering | Check `GET /v1/orchs`; inspect `/v1/orchs/metrics`; check whether the broker tuple is `degraded`, `draining`, `stale`, or locally cooled |
| WebRTC offer succeeds but no video frames | Cloudflare TURN credentials expired or `HF_TOKEN` not set on Scope | Check Scope container env; rotate `HF_TOKEN` |
| Scope HTTP 404 on `/api/v1/*` | session_id from SPA doesn't match any router entry | Check `X-Daydream-Session` header; check session hasn't expired |
| Sessions auto-close around 1h mark | broker `DefaultExpiresIn` reached | Reopen session; this is by design |

## Known limitations (v0)

- **No control-WS reconnect.** If the gateway's control-WS to the
  broker drops, the session tears down. Reconnect with last-seq replay
  is a follow-up.
- **No HA for Cloudflare TURN.** If Cloudflare's TURN is unreachable,
  all sessions fail at WebRTC offer/answer. HA story is a follow-up.
- **No per-session TURN credential rotation.** Scope reads TURN config
  at startup only; all sessions on a Scope instance share the same
  credentials. Upstream Scope code-change would be needed to fix.

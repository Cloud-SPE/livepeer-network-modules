# Orch Coordinator

The single public-facing node that publishes your orchestrator's signed
registry manifest. Gateways resolve the AI Service Registry on-chain, find
your URL (e.g. `https://coordinator.example.com/.well-known/livepeer-registry.json`),
and fetch this manifest to learn about your capability brokers.

This is also where you define your fleet: every capability broker (worker
node) you run is listed in `coordinator-config.yaml` and surfaced through
this manifest.

## Role in the topology

- The **Secure Orch** host (separate box, no inbound internet) signs
  manifest candidates with the cold key.
- This **Orch Coordinator** host serves the signed manifest publicly at
  `/.well-known/livepeer-registry.json`.
- The URL of that endpoint is what you publish on-chain via the AI Service
  Registry contract.

## What runs here

| Service           | Purpose                                              |
| ----------------- | ---------------------------------------------------- |
| `orch-coordinator`| Holds the signed manifest, serves it on the public endpoint, exposes an admin API for fleet management, scrapes each broker's `/registry/offerings` + `/registry/health` for the roster view |

## Listeners

| Port | Visibility | Purpose                                            |
| ---- | ---------- | -------------------------------------------------- |
| 8081 | **Public** | `/.well-known/livepeer-registry.json` for gateways |
| 8080 | Private    | Operator admin API                                 |
| 9091 | Private    | Prometheus metrics                                 |

The defaults bind 8080 / 9091 to loopback. **8081 must terminate TLS in
production** — put a reverse proxy in front of the container. A Traefik
reference is documented separately.

## On-disk layout

```
/opt/livepeer/
└── coordinator-config.yaml
```

Bootstrap by copying the example:

```sh
sudo cp infra/scenarios/orchestrator-onboarding/orch-coordinator/coordinator-config.example.yaml \
        /opt/livepeer/coordinator-config.yaml
sudo $EDITOR /opt/livepeer/coordinator-config.yaml
```

Fill in:

- `identity.orch_eth_address` — your on-chain orchestrator address (same
  one whose private key lives on the Secure Orch host).
- `brokers[]` — one entry per capability broker (worker node) in your fleet.

## Bring-up

```sh
cp infra/scenarios/orchestrator-onboarding/orch-coordinator/.env.example \
   infra/scenarios/orchestrator-onboarding/orch-coordinator/.env
$EDITOR infra/scenarios/orchestrator-onboarding/orch-coordinator/.env

# Generate an admin token for the operator API
openssl rand -hex 32   # paste into ORCH_COORDINATOR_ADMIN_TOKENS

docker compose \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/orch-coordinator/.env \
  up -d
```

## Required values

You must set these in `.env` before bring-up:

- `ORCH_COORDINATOR_ADMIN_TOKENS` — generated secret used to authenticate
  operator pushes (e.g. signing a fresh manifest from the Secure Orch host).
- `COORDINATOR_CONFIG` — only if your config lives somewhere other than
  `/opt/livepeer/coordinator-config.yaml`.

## Verify

```sh
# Public registry endpoint (returns "no manifest published" until you sign
# and upload the first manifest from your Secure Orch host)
curl -s http://127.0.0.1:8081/.well-known/livepeer-registry.json

# Admin API health
curl -s http://127.0.0.1:8080/healthz

# Metrics
curl -s http://127.0.0.1:9091/metrics | head
```

Open the admin UI at `http://127.0.0.1:8080/` (use one of the
`ORCH_COORDINATOR_ADMIN_TOKENS` you generated) and check the roster
view — every broker listed in `coordinator-config.yaml` should show its
live status from the broker's `/registry/health` next to its name. A
broker reporting `stale` or carrying a health error is one the
coordinator can't reach right now; gateways will treat it the same way.

Once you publish the URL of the public endpoint to the AI Service Registry
contract, gateways will discover your orchestrator on the next round.

## Fronted by Traefik

For production, run this on the same box as the
[ingress-traefik](../ingress-traefik/) stack and layer the Traefik
overlay on top. The overlay drops the public 8081 port mapping (Traefik
handles it through the `ingress` network) and adds the router labels for
your public hostname.

```sh
$EDITOR infra/scenarios/orchestrator-onboarding/orch-coordinator/.env   # set COORDINATOR_HOST
docker compose \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.traefik.yml \
  --env-file infra/scenarios/orchestrator-onboarding/orch-coordinator/.env \
  up -d
```

See `docker-compose.traefik.yml` and `infra/scenarios/orchestrator-onboarding/ingress-traefik/`
for the full topology.

## Fronted by Cloudflare Tunnel

Alternative to Traefik for hosts behind NAT or without inbound ports
(e.g. LAN nodes). Run [ingress-cloudflared](../ingress-cloudflared/) on
the same box and layer the cloudflared overlay on top:

```sh
docker compose \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.cloudflared.yml \
  --env-file infra/scenarios/orchestrator-onboarding/orch-coordinator/.env \
  up -d
```

Then in the Cloudflare Zero Trust dashboard, add a Public Hostname:

| Field        | Value                                                  |
| ------------ | ------------------------------------------------------ |
| Subdomain    | `coordinator` (or your choice)                         |
| Service URL  | `orch-coordinator:8081`                                |
| Path         | `/.well-known/livepeer-registry.json` _(optional)_     |

The cloudflared overlay does not require `COORDINATOR_HOST` or
`TRAEFIK_CERTRESOLVER` — hostname mapping lives in Cloudflare's UI.

## Fronted by Nginx (nginx-proxy + acme-companion)

Auto Let's Encrypt with either HTTP-01 (default) or Cloudflare DNS-01.
Run [ingress-nginx](../ingress-nginx/) on the same box and layer ONE of
the Nginx overlays on top — not both.

**HTTP-01** (simpler; requires inbound :80):

```sh
docker compose \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.nginx.yml \
  --env-file infra/scenarios/orchestrator-onboarding/orch-coordinator/.env \
  up -d
```

**Cloudflare DNS-01** (no inbound :80 needed; requires API token + zone IDs):

```sh
# In .env: set CF_DNS_API_TOKEN, CF_ACCOUNT_ID, CF_ZONE_ID
docker compose \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.nginx-dns01.yml \
  --env-file infra/scenarios/orchestrator-onboarding/orch-coordinator/.env \
  up -d
```

Both overlays route **only** `/.well-known/livepeer-registry.json` to the
container's :8081 via `VIRTUAL_PATH`.

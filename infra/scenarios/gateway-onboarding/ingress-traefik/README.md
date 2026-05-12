# Traefik ingress

TLS-terminating reverse proxy for the public-facing gateway. Run **one
Traefik instance on the gateway host**. The Livepeer gateway service
(openai, video, or vtuber) attaches to the external `ingress` network
and doesn't publish any public ports of its own.

## Per-host topology

Gateways are single-host (no load balancing). One host per gateway:

| Host                       | Runs                                          | Public hostname                     | Notes                                                                 |
| -------------------------- | --------------------------------------------- | ----------------------------------- | --------------------------------------------------------------------- |
| `openai-gateway-host`      | `ingress-traefik` + `openai-gateway`          | `openai.example.com`                | OpenAI-compatible API.                                                |
| `video-gateway-host`       | `ingress-traefik` + `video-gateway`           | `video.example.com`                 | HTTP API through Traefik; RTMP `:1935` stays direct-exposed.          |
| `vtuber-gateway-host`      | `ingress-traefik` + `vtuber-gateway` _(preview)_ | `vtuber.example.com`                | Long-lived HTTP+WS sessions.                                          |

You typically run one gateway type per host. If you co-locate multiple
gateways on one host, Traefik routes them by hostname — the labels each
gateway overlay adds use the gateway's own `GATEWAY_HOST`.

On the host that runs Traefik:

- Each host's DNS A/AAAA record must point at that host's public IP.
- The Cloudflare DNS API token (or HTTP-01 alternative) must be able to
  issue certs for the gateway hostname configured on that box.

## When to use Traefik

- Domains hosted on Cloudflare DNS (or another DNS provider lego
  supports) and you want automatic cert issuance via the DNS-01 challenge.
- Docker-native label-based routing — the gateway overlay declares its
  hostname and route via labels, no separate config file.
- Cloud host with inbound :80/:443 reachable.

For gateways behind NAT or that can't open inbound :80/:443, the
recommended path is Nginx with DNS-01 instead — there's no Cloudflare
Tunnel option for gateways in this onboarding flow.

## Cert resolvers

Two ACME resolvers are configured in `traefik.example.yml`:

| Resolver       | Challenge | When to use                                                                              |
| -------------- | --------- | ---------------------------------------------------------------------------------------- |
| `cloudflare`   | DNS-01    | **Default.** Domains on Cloudflare. Works behind NAT / without inbound :80.              |
| `letsencrypt`  | HTTP-01   | Domains not on Cloudflare. Requires inbound :80 reachable from Let's Encrypt servers.    |

The example file ships with `cloudflare` active and `letsencrypt`
commented. To switch, uncomment `letsencrypt:` in `traefik.yml` and
change your gateway's `TRAEFIK_CERTRESOLVER` env var from `cloudflare`
to `letsencrypt`.

## On-disk layout

```
/opt/livepeer/traefik/
└── traefik.yml          # static config (this stack's traefik.example.yml)
```

The volume is bind-mounted to `/etc/traefik` in the container. Drop
additional dynamic config files into the same directory and Traefik will
pick them up (`providers.file.watch: true`).

## Bring-up

```sh
# 1. One-time: create the shared external network
docker network create ingress

# 2. Install the static config
sudo mkdir -p /opt/livepeer/traefik
sudo cp infra/scenarios/gateway-onboarding/ingress-traefik/traefik.example.yml \
        /opt/livepeer/traefik/traefik.yml
sudo $EDITOR /opt/livepeer/traefik/traefik.yml   # set ACME email

# 3. Configure compose env
cp infra/scenarios/gateway-onboarding/ingress-traefik/.env.example \
   infra/scenarios/gateway-onboarding/ingress-traefik/.env
$EDITOR infra/scenarios/gateway-onboarding/ingress-traefik/.env     # set CF_DNS_API_TOKEN

# 4. Bring it up
docker compose \
  -f infra/scenarios/gateway-onboarding/ingress-traefik/docker-compose.yml \
  --env-file infra/scenarios/gateway-onboarding/ingress-traefik/.env \
  up -d
```

## Required values

In `.env`:

- **`CF_DNS_API_TOKEN`** — scoped Cloudflare API token with `Zone:DNS:Edit`
  on the zone(s) hosting your gateway domains. Create one at
  `https://dash.cloudflare.com/profile/api-tokens`. Prefer this over the
  legacy `CF_API_KEY` (global key, easy to over-scope).

In `/opt/livepeer/traefik/traefik.yml`:

- **`certificatesResolvers.cloudflare.acme.email`** — the email Let's
  Encrypt sends expiry notices to. Required.

## Per-gateway setup

Each gateway scenario ships a Traefik **overlay**
(`docker-compose.traefik.yml`) alongside its base compose. The overlay
drops the publicly-published port, attaches the gateway to the external
`ingress` network, and declares the Traefik router labels keyed off
`GATEWAY_HOST`.

Result: the gateway binds no public host port. Inbound HTTPS traffic
flows Traefik → `ingress` network → gateway. (The Video gateway is the
exception — its RTMP `:1935` port stays directly exposed; only the HTTP
port routes through Traefik.)

The bring-up pattern is the same for every gateway:

1. Install Traefik (this scenario) — sets up TLS + ingress network.
2. Bring up the gateway with both its base compose and the Traefik
   overlay layered on top.

### OpenAI Gateway

```sh
# Set GATEWAY_HOST + TRAEFIK_CERTRESOLVER in .env
docker compose \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.traefik.yml \
  --env-file infra/scenarios/gateway-onboarding/openai-gateway/.env \
  up -d
```

Routes `https://${GATEWAY_HOST}/` → gateway container `:3000`.

### Video Gateway

```sh
docker compose \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.traefik.yml \
  --env-file infra/scenarios/gateway-onboarding/video-gateway/.env \
  up -d
```

Routes `https://${GATEWAY_HOST}/` → gateway container `:3000`. RTMP
`:1935` is **not** routed through Traefik — it stays as a direct TCP
port mapping. Customers using RTMP ingest connect to
`rtmp://${GATEWAY_HOST}:1935/...` (or your `VIDEO_RTMP_PORT` override).
Stream keys are the auth model for RTMP.

### Vtuber Gateway (preview)

```sh
docker compose \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.traefik.yml \
  --env-file infra/scenarios/gateway-onboarding/vtuber-gateway/.env \
  up -d
```

Routes `https://${GATEWAY_HOST}/` → gateway container `:3001`. Long-lived
HTTP + WebSocket sessions; Traefik forwards WS upgrade headers without
extra config.

> ⚠ **Vtuber gateway is preview / under development.** Do not advertise
> sessions to customers from this stack until the gateway is announced
> as production-ready. See [`../vtuber-gateway/README.md`](../vtuber-gateway/README.md).

### Cert resolver selection

Every gateway overlay defaults to `TRAEFIK_CERTRESOLVER=cloudflare`. To
use Let's Encrypt HTTP-01 instead, uncomment the `letsencrypt:` block
in `traefik.yml` **and** set `TRAEFIK_CERTRESOLVER=letsencrypt` in the
gateway's `.env`.

## Dashboard, metrics (advanced)

The dashboard is enabled but not exposed (`api.insecure: false`). Most
gateway operators don't need it. If you want to reach it:

- Don't publish :8080 directly to the internet.
- Add Traefik labels on the `traefik` service itself that route a host
  like `traefik-admin.example.com` to `api@internal` and
  `prometheus@internal`, protected by `basicauth` or `forwardauth`
  middleware.

Centralized Prometheus scraping (one Prom polling every gateway host's
`/traefik/metrics`) follows the same pattern — expose via labels with
auth, not direct port publishing. Skip this if you're not running
multi-host Prometheus.

## Verify

```sh
# Cert issued?
docker exec traefik cat /ssl-certs/acme-cloudflare.json | jq '.cloudflare.Certificates | length'

# Gateway reachable over TLS?
curl -sI https://${GATEWAY_HOST}/
```

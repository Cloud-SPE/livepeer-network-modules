# Nginx ingress

TLS-terminating reverse proxy using
[`nginxproxy/nginx-proxy`](https://github.com/nginx-proxy/nginx-proxy) +
[`nginxproxy/acme-companion`](https://github.com/nginx-proxy/acme-companion).
Automatic Let's Encrypt cert issuance via either the **HTTP-01** challenge
(default) or the **Cloudflare DNS-01** challenge.

Requires inbound **:443** reachable on the host. HTTP-01 additionally
requires inbound **:80**. Use this on the cloud host where your gateway
runs. There is no Cloudflare Tunnel option for gateways in this
onboarding flow — gateways are public-facing servers, so direct inbound
on :443 is expected.

## Which cert challenge?

You'll pick one per gateway via which overlay you layer on top.

| Pick this  | When                                                                  | Gateway overlay file                        |
| ---------- | --------------------------------------------------------------------- | ------------------------------------------- |
| **HTTP-01**| You can open inbound :80. Simplest setup, no API tokens.              | `docker-compose.nginx.yml`                  |
| **DNS-01** | Your domain is on Cloudflare and you'd rather not open :80, want wildcard certs, or hit issuance friction with HTTP-01. | `docker-compose.nginx-dns01.yml` |

The Nginx stack itself is the **same in both cases** — you don't change
`docker-compose.yml`. Only the gateway overlay file differs.

## Per-host topology

Gateways are single-host. One Nginx instance fronts the gateway on its
own box:

| Host                       | Runs                                          | Public hostname                     | Notes                                                                 |
| -------------------------- | --------------------------------------------- | ----------------------------------- | --------------------------------------------------------------------- |
| `openai-gateway-host`      | `ingress-nginx` + `openai-gateway`            | `openai.example.com`                | HTTP API.                                                             |
| `video-gateway-host`       | `ingress-nginx` + `video-gateway`             | `video.example.com`                 | HTTP API through Nginx; RTMP `:1935` stays direct-exposed.            |
| `vtuber-gateway-host`      | `ingress-nginx` + `vtuber-gateway` _(preview)_| `vtuber.example.com`                | Long-lived HTTP+WS sessions.                                          |

Each host runs an independent nginx-proxy + acme-companion pair. Each
gets its own certs and its own acme-companion store.

## Configuration model: env labels via Docker socket

`nginx-proxy` and `acme-companion` watch the Docker socket. The gateway
container registers itself by setting env vars in its compose:

| Env var             | Purpose                                                  |
| ------------------- | -------------------------------------------------------- |
| `VIRTUAL_HOST`      | Public hostname (the cert FQDN)                          |
| `VIRTUAL_PORT`      | Internal container port to route to                      |
| `LETSENCRYPT_HOST`  | Hostname for cert issuance (same as VIRTUAL_HOST)        |
| `ACMESH_DNS_API_CONFIG` | Only on the DNS-01 overlay — Cloudflare creds        |

No config files to hand-edit on the box. The gateway overlays ship
these vars pre-wired.

## Bring-up

```sh
# 1. One-time: create the shared external network
docker network create ingress

# 2. Configure compose env
cp infra/scenarios/gateway-onboarding/ingress-nginx/.env.example \
   infra/scenarios/gateway-onboarding/ingress-nginx/.env
$EDITOR infra/scenarios/gateway-onboarding/ingress-nginx/.env       # set ACME_EMAIL

# 3. Bring it up
docker compose \
  -f infra/scenarios/gateway-onboarding/ingress-nginx/docker-compose.yml \
  --env-file infra/scenarios/gateway-onboarding/ingress-nginx/.env \
  up -d
```

Within seconds nginx-proxy + acme-companion are running. They'll issue
certs once the gateway registers a hostname.

## Per-gateway setup

Each gateway scenario ships **two** Nginx overlays alongside its base
compose — one per cert challenge. Pick ONE.

The overlay drops the publicly-published port, attaches the gateway to
the external `ingress` network, and sets the nginx-proxy env vars. The
gateway binds no public host port — inbound HTTPS traffic flows
nginx-proxy → `ingress` network → gateway. (The Video gateway is the
exception — its RTMP `:1935` port stays directly exposed; only the HTTP
port routes through Nginx.)

The gateway overlays reuse `GATEWAY_HOST` (and on DNS-01,
`CF_DNS_API_TOKEN`/`CF_ACCOUNT_ID`/`CF_ZONE_ID`) from the same `.env`
file used by the Traefik overlay — swapping ingress flavors doesn't
require .env changes.

### OpenAI Gateway

**HTTP-01** (simpler; requires inbound :80):

```sh
# Set GATEWAY_HOST in .env
docker compose \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.nginx.yml \
  --env-file infra/scenarios/gateway-onboarding/openai-gateway/.env \
  up -d
```

**Cloudflare DNS-01** (no inbound :80 needed):

```sh
# In .env: set GATEWAY_HOST, CF_DNS_API_TOKEN, CF_ACCOUNT_ID, CF_ZONE_ID
docker compose \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.nginx-dns01.yml \
  --env-file infra/scenarios/gateway-onboarding/openai-gateway/.env \
  up -d
```

Routes `https://${GATEWAY_HOST}/` → gateway container `:3000`.

### Video Gateway

**HTTP-01**:

```sh
docker compose \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.nginx.yml \
  --env-file infra/scenarios/gateway-onboarding/video-gateway/.env \
  up -d
```

**Cloudflare DNS-01**:

```sh
docker compose \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.nginx-dns01.yml \
  --env-file infra/scenarios/gateway-onboarding/video-gateway/.env \
  up -d
```

Routes `https://${GATEWAY_HOST}/` → gateway container `:3000`. RTMP
`:1935` is **not** proxied through Nginx — it stays a direct TCP port
mapping. Customers using RTMP ingest connect to
`rtmp://${GATEWAY_HOST}:1935/...` (or your `VIDEO_RTMP_PORT` override).
Stream keys are the auth model for RTMP.

### Vtuber Gateway (preview)

**HTTP-01**:

```sh
docker compose \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.nginx.yml \
  --env-file infra/scenarios/gateway-onboarding/vtuber-gateway/.env \
  up -d
```

**Cloudflare DNS-01**:

```sh
docker compose \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.nginx-dns01.yml \
  --env-file infra/scenarios/gateway-onboarding/vtuber-gateway/.env \
  up -d
```

Routes `https://${GATEWAY_HOST}/` → gateway container `:3001`. nginx-proxy
handles HTTP and WebSocket upgrades automatically — no extra config for
the long-lived session pattern.

> ⚠ **Vtuber gateway is preview / under development.** Do not advertise
> sessions to customers from this stack until the gateway is announced
> as production-ready. See [`../vtuber-gateway/README.md`](../vtuber-gateway/README.md).

### DNS

Point an A/AAAA record for `${GATEWAY_HOST}` at this host's public IP
before bring-up. True for both challenge types:

- **HTTP-01**: acme-companion needs the hostname to resolve and :80 to
  be reachable from the public internet.
- **DNS-01**: the A/AAAA record is still required for inbound HTTPS
  traffic. acme-companion uses your Cloudflare API token to create a
  short-lived TXT record in your zone to prove control; no inbound :80
  needed.

## Verify

```sh
# nginx-proxy generating config for your gateway?
docker exec nginx-proxy cat /etc/nginx/conf.d/default.conf | grep server_name

# acme-companion issued the cert?
docker exec nginx-proxy ls /etc/nginx/certs/

# Gateway reachable over TLS?
curl -sI https://${GATEWAY_HOST}/
```

If cert issuance hangs on HTTP-01: confirm the hostname's DNS A/AAAA
record points at this host and inbound :80 is reachable from the public
internet (Let's Encrypt connects from outside).

## Trade-offs vs Traefik

- **Simpler.** Fewer config files. Auto Let's Encrypt out of the box.
- **Both HTTP-01 and DNS-01 supported** via two parallel overlay variants
  per gateway.
- **No dashboard.** Use `docker logs nginx-proxy` and direct file
  inspection for observability.
- **No fancy routing.** Per-host is what you get. For more (header
  rewrites, middlewares, OIDC), reach for Traefik.

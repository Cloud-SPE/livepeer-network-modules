# Video Gateway

Sender-side gateway for video workloads:

- **VOD uploads** via tus to `/v1/uploads` (configurable)
- **Live streaming ingest** via RTMP on `:1935`
- **ABR delivery** of transcoded segments from S3-compatible storage

Single-host, no load balancing. Routes work to video capability brokers
discovered from the on-chain AI Service Registry.

> ## Important: AI Service Registry vs legacy Service Registry
>
> The gateways in this repo (Video, OpenAI, Vtuber) all coordinate
> with orchestrators through the **AI Service Registry contract**
> (`0x04C0b249740175999E5BF5c9ac1dA92431EF34C5` on Arbitrum).
>
> This is **not** the same contract as the legacy "Service Registry"
> that the original go-livepeer video-transcoding network uses. The two
> registries are independent — gateways here do not discover legacy
> orchestrators, and legacy go-livepeer gateways do not discover the
> orchestrators set up via the orchestrator-onboarding flow in this
> repo.
>
> The AI Service Registry is this repo's testbed for replacing the
> legacy registry. If you're coming from a go-livepeer background, just
> know that the discovery contract is different by design.

## Role in the topology

```
your customer ──HTTPS──►  reverse proxy ──┐
                                          │
                          RTMP ───────────┼──►  video-gateway ─┐
                                          │                    │
                                          │                    │ (discovers via on-chain
                                          │                    │  AI Service Registry)
                                          ▼                    ▼
                                  payment-daemon       service-registry-daemon
                                  sender                       │
                                          │                    │
                                          ▼                    ▼
                                  capability-broker (video-capable orchestrator)
                                          │
                                          ▼
                                  rustfs (S3) ──► ABR delivery back to customers
```

## What runs here

| Service                   | Purpose                                                       |
| ------------------------- | ------------------------------------------------------------- |
| `video-gateway`           | HTTP API (VOD/tus, ABR) + RTMP listener; the public surface   |
| `service-registry-daemon` | Resolves the AI Service Registry from chain                   |
| `payment-daemon` sender   | Signs and sends ticket payments to orchestrators              |
| `postgres`                | Customer + session state                                      |
| `redis`                   | Live stream state + stale-stream sweeper queue                |
| `rustfs`                  | S3-compatible object storage for transcoded segments          |
| `rustfs-init`             | One-shot: creates the default S3 bucket on first start        |

All seven share a private compose-managed network. Only `video-gateway`
attaches to the external `ingress` network (via the overlays) so the
HTTP reverse proxy can reach it. Everything else stays internal.

## Public surfaces

Two ports, two protocols, two different ingress paths:

| Surface     | Container port | Default host port | Proxy-able?                                                        |
| ----------- | -------------- | ----------------- | ------------------------------------------------------------------ |
| HTTP API    | `3000`         | `5000`            | Yes — Traefik / Nginx HTTP overlays drop the host port             |
| RTMP ingest | `1935`         | `1935`            | **No** — TCP-only. ALWAYS exposed directly on the host, even with an HTTP ingress overlay layered on. |

If your host has another RTMP listener already on `:1935` (e.g. an old
go-livepeer node), override `VIDEO_RTMP_PORT` in `.env`.

## Wallet model — throwaway hot wallet

Same model as the OpenAI Gateway. The gateway has **one keystore** that
signs everything (ticket payments + broker authentication). There is no
cold/hot split available.

- **Keep funding small.** Just enough deposit + reserve to keep work
  flowing through the orchestrators you talk to. Loss-on-compromise
  should be a number you can absorb.
- **Rotate immediately on compromise.** Generate a new key, fund it,
  install at `/opt/livepeer/keystore.json`, restart the stack.
- **Maintain reserve.** If the wallet runs out of deposit mid-stream,
  the broker stops accepting work. Top up before that wall.
- **Never reuse this key for anything else.**

## On-disk layout

```
/opt/livepeer/
├── keystore.json
└── keystore-password
```

If you co-locate multiple gateways on a single host (not the default
single-host pattern), use subdirectories like `/opt/livepeer/video/` and
`/opt/livepeer/openai/` and override the `PAYMENT_KEYSTORE` /
`PAYMENT_KEYSTORE_PASSWORD_FILE` env vars per stack.

## Bring-up

```sh
# 1. Install your gateway hot-wallet keystore
sudo cp <your-funded-keystore>.json /opt/livepeer/keystore.json
sudo cp <your-keystore-password> /opt/livepeer/keystore-password

# 2. Configure compose env
cp infra/scenarios/gateway-onboarding/video-gateway/.env.example \
   infra/scenarios/gateway-onboarding/video-gateway/.env
$EDITOR infra/scenarios/gateway-onboarding/video-gateway/.env

# Generate the required secrets:
openssl rand -hex 24   # → POSTGRES_PASSWORD
openssl rand -hex 16   # → RUSTFS_ROOT_PASSWORD
openssl rand -hex 32   # → VIDEO_WEBHOOK_HMAC_PEPPER
openssl rand -hex 32   # → CUSTOMER_PORTAL_PEPPER
openssl rand -hex 32   # → VIDEO_GATEWAY_ADMIN_TOKENS

# 3. Bring it up
docker compose \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.yml \
  --env-file infra/scenarios/gateway-onboarding/video-gateway/.env \
  up -d
```

This bring-up exposes the HTTP API directly on `:5000` and RTMP on
`:1935`. For production HTTPS on the API, layer one of the ingress
overlays below.

## Required values

In `.env` before bring-up:

- **`POSTGRES_PASSWORD`** — generated, used by both postgres init and the
  gateway's `DATABASE_URL`.
- **`RUSTFS_ROOT_PASSWORD`** — generated. Also doubles as `S3_SECRET_ACCESS_KEY`
  by default when using the bundled rustfs.
- **`VIDEO_WEBHOOK_HMAC_PEPPER`** — generated. Rotating invalidates
  in-flight signed webhooks.
- **`CUSTOMER_PORTAL_PEPPER`** — generated. Rotating invalidates all
  issued portal tokens.
- **`VIDEO_GATEWAY_ADMIN_TOKENS`** — generated. Used to authenticate
  operator calls against the gateway's admin API.
- **`CHAIN_RPC`** — your Arbitrum RPC endpoint.

## External S3

To swap the bundled `rustfs` for AWS S3 or another S3-compatible
provider, override these in `.env`:

```
S3_REGION=us-west-2
S3_BUCKET=your-transcoded-bucket
S3_ENDPOINT=                            # leave blank to use AWS default
S3_ACCESS_KEY_ID=AKIA...
S3_SECRET_ACCESS_KEY=...
S3_FORCE_PATH_STYLE=false               # AWS S3 prefers virtual-hosted-style
```

For external S3 you can drop the `rustfs` + `rustfs-init` services from
your compose invocation, but keeping them running idle does no harm.

## Verify

```sh
# Gateway responds on its HTTP API
curl -s http://127.0.0.1:5000/healthz

# service-registry-daemon found brokers from chain
docker compose logs service-registry-daemon | grep -i broker

# payment-daemon loaded the keystore
docker compose logs payment-daemon-sender | grep -i wallet

# RTMP listener is bound
nc -vz 127.0.0.1 1935

# rustfs bucket created
docker compose logs rustfs-init
```

## Fronted by Traefik

For production HTTPS on the API, run [ingress-traefik](../ingress-traefik/)
on the same box and layer the Traefik overlay. The overlay drops the
public `:5000` HTTP port; the RTMP `:1935` port stays directly exposed.

```sh
$EDITOR infra/scenarios/gateway-onboarding/video-gateway/.env  # set GATEWAY_HOST
docker compose \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.traefik.yml \
  --env-file infra/scenarios/gateway-onboarding/video-gateway/.env \
  up -d
```

## Fronted by Nginx (nginx-proxy + acme-companion)

Auto Let's Encrypt for the HTTP API with either HTTP-01 (default) or
Cloudflare DNS-01. Run [ingress-nginx](../ingress-nginx/) on the same
box and layer ONE of the Nginx overlays. RTMP stays direct in both
cases.

**HTTP-01** (simpler; requires inbound :80):

```sh
docker compose \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.nginx.yml \
  --env-file infra/scenarios/gateway-onboarding/video-gateway/.env \
  up -d
```

**Cloudflare DNS-01** (no inbound :80 needed; requires API token + zone IDs):

```sh
# In .env: set CF_DNS_API_TOKEN, CF_ACCOUNT_ID, CF_ZONE_ID
docker compose \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/video-gateway/docker-compose.nginx-dns01.yml \
  --env-file infra/scenarios/gateway-onboarding/video-gateway/.env \
  up -d
```

### Securing RTMP

The HTTP API gets TLS via the ingress overlay. **RTMP does not** — the
overlays leave it as plain TCP on `:1935`. If you need an authenticated
or encrypted ingest path:

- Issue stream keys (HMAC-signed, per-customer) and validate them at
  the gateway. This is the default pattern.
- Use RTMPS at a separate TCP-aware proxy (HAProxy in TCP mode, or
  Traefik TCP routers — both can terminate TLS for RTMP if your client
  supports RTMPS). Beyond scope of this scenario.

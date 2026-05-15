# Vtuber Gateway

> ## ⚠ Preview — DO NOT USE ON LIVE NETWORK
>
> The vtuber gateway is **under active development** and has not been
> tested end-to-end against the current orchestrator + capability-broker
> shape. Do not advertise vtuber sessions to customers from this stack
> until the gateway is announced as production-ready.
>
> Everything in this scenario describes the **target** shape — modeled
> on the OpenAI + Video gateway patterns so the migration lands cleanly
> when the code catches up. Some env vars in the compose may not be
> wired in the published image at `tztcloud/livepeer-vtuber-gateway:v1.1.0`
> yet. Treat this as a use-case reference, not a deployable runbook.
>
> When in doubt: run the OpenAI or Video gateway instead.

Sender-side gateway for vtuber pipeline workloads. Long-lived,
session-based traffic with control-plane reconnect semantics. Routes
sessions to vtuber-capable capability brokers discovered from the
on-chain AI Service Registry.

Single-host, no load balancing. Public surface is the gateway HTTP +
WebSocket API on `:3001` — production must terminate TLS at a reverse
proxy in front (Traefik or Nginx; see overlays below).

> ## Important: AI Service Registry vs legacy Service Registry
>
> The gateways in this repo (OpenAI, Video, Vtuber) all coordinate
> with orchestrators through the **AI Service Registry contract**
> (`0x04C0b249740175999E5BF5c9ac1dA92431EF34C5` on Arbitrum).
>
> This is **not** the same contract as the legacy "Service Registry"
> that the original go-livepeer video-transcoding network uses. The two
> registries are independent — gateways here do not discover legacy
> orchestrators, and legacy go-livepeer gateways do not discover the
> orchestrators set up via the orchestrator-onboarding flow in this
> repo.

## What's different about vtuber

Sessions are **long-lived and stateful**. A customer opens a session,
receives a session bearer, and holds an HTTP / WebSocket connection
open while the vtuber pipeline streams pose data and control messages
in both directions. The gateway:

- pays per-second (not per-request) — see `VTUBER_RATE_CARD_USD_PER_SECOND`
- maintains a reconnect window so a dropped control channel can resume
- buffers a small replay window of recent control messages
- forwards relay channels (sub-streams) up to a per-session cap

This is the design — implementation completion is pending.

## Role in the topology

```
your customer ──HTTPS / WSS──►  reverse proxy
                                    │
                                    ▼
                             vtuber-gateway ─┐
                                    │        │ (discovers via on-chain
                                    │        │  AI Service Registry)
                                    ▼        ▼
                             payment-daemon  service-registry-daemon
                             sender                  │
                                    │                │
                                    ▼                ▼
                             capability-broker (vtuber-capable orchestrator)
                                    │
                                    ▼
                             vtuber-runner / pipeline
```

## What runs here

| Service                   | Purpose                                                       |
| ------------------------- | ------------------------------------------------------------- |
| `vtuber-gateway`          | Vtuber session HTTP + WebSocket API; the public surface       |
| `service-registry-daemon` | Resolves the AI Service Registry from chain                   |
| `payment-daemon` sender   | Signs and sends ticket payments to orchestrators              |
| `postgres`                | Session + bearer state for the gateway                        |

All four share a private compose-managed network. Only `vtuber-gateway`
attaches to the external `ingress` network (via the overlays) so the
reverse proxy can reach it.

## Wallet model — throwaway hot wallet

Same model as the OpenAI and Video gateways. One keystore signs ticket
payments and broker authentication. No cold/hot split available.

- **Keep funding small.** Loss-on-compromise should be a number you can
  absorb. Vtuber sessions pay per-second, so estimate your max concurrent
  session-seconds × rate to size the reserve.
- **Rotate immediately on compromise.** Generate a new key, fund it,
  install at `/opt/livepeer/keystore.json`, restart the stack.
- **Maintain reserve.** A drained wallet means in-flight sessions stall.
- **Never reuse this key for anything else.**

## On-disk layout

```
/opt/livepeer/
├── keystore.json
└── keystore-password
```

If you co-locate multiple gateways on a single host, use subdirectories
like `/opt/livepeer/vtuber/` and override the `PAYMENT_KEYSTORE` /
`PAYMENT_KEYSTORE_PASSWORD_FILE` env vars per stack.

## Bring-up

Again: **preview / not for production**. Use against a dev environment.

```sh
# 1. Install your gateway hot-wallet keystore
sudo cp <your-dev-keystore>.json /opt/livepeer/keystore.json
sudo cp <your-dev-keystore-password> /opt/livepeer/keystore-password

# 2. Configure compose env
cp infra/scenarios/gateway-onboarding/vtuber-gateway/.env.example \
   infra/scenarios/gateway-onboarding/vtuber-gateway/.env
$EDITOR infra/scenarios/gateway-onboarding/vtuber-gateway/.env

# Generate the required secrets:
openssl rand -hex 24  # → POSTGRES_PASSWORD
openssl rand -hex 32  # → VTUBER_SESSION_BEARER_PEPPER
openssl rand -hex 32  # → VTUBER_WORKER_CONTROL_BEARER_PEPPER

# 3. Bring it up
docker compose \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.yml \
  --env-file infra/scenarios/gateway-onboarding/vtuber-gateway/.env \
  up -d
```

## Required values

In `.env` before bring-up:

- **`POSTGRES_PASSWORD`** — generated, used by both postgres init and
  the gateway's `DATABASE_URL`.
- **`VTUBER_SESSION_BEARER_PEPPER`** — generated. Rotating invalidates
  active customer session bearers.
- **`VTUBER_WORKER_CONTROL_BEARER_PEPPER`** — generated. Rotating
  invalidates active broker control-channel bearers.
- **`CHAIN_RPC`** — your Arbitrum RPC endpoint.

## Verify

```sh
# Gateway responds on health endpoint
curl -s http://127.0.0.1:3001/healthz

# service-registry-daemon found brokers from chain (look for
# vtuber-capable entries)
docker compose logs service-registry-daemon | grep -i vtuber

# payment-daemon loaded the keystore
docker compose logs payment-daemon-sender | grep -i wallet
```

Session-open and live relay traffic depend on a vtuber-capable broker
being advertised on chain — see the orchestrator-onboarding flow for
how that gets stood up (and note that the vtuber host-config variant
is not yet shipped on the orch side either).

## Fronted by Traefik

For production HTTPS + WSS, run [ingress-traefik](../ingress-traefik/)
on the same box and layer the Traefik overlay. The overlay drops the
public `:3001` port; Traefik handles HTTP and WebSocket upgrades
through the same router (no extra config).

```sh
$EDITOR infra/scenarios/gateway-onboarding/vtuber-gateway/.env  # set GATEWAY_HOST
docker compose \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.traefik.yml \
  --env-file infra/scenarios/gateway-onboarding/vtuber-gateway/.env \
  up -d
```

## Fronted by Nginx (nginx-proxy + acme-companion)

Auto Let's Encrypt with either HTTP-01 (default) or Cloudflare DNS-01.
Run [ingress-nginx](../ingress-nginx/) on the same box and layer ONE
of the Nginx overlays. nginx-proxy handles HTTP + WebSocket upgrades.

**HTTP-01** (simpler; requires inbound :80):

```sh
docker compose \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.nginx.yml \
  --env-file infra/scenarios/gateway-onboarding/vtuber-gateway/.env \
  up -d
```

**Cloudflare DNS-01** (no inbound :80 needed; requires API token + zone IDs):

```sh
# In .env: set CF_DNS_API_TOKEN, CF_ACCOUNT_ID, CF_ZONE_ID
docker compose \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/vtuber-gateway/docker-compose.nginx-dns01.yml \
  --env-file infra/scenarios/gateway-onboarding/vtuber-gateway/.env \
  up -d
```

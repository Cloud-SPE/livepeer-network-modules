# Gateway Onboarding Guide

A step-by-step path for operators bringing a Livepeer gateway online.
The gateway is the customer-facing surface — it accepts inbound work
(HTTP API, RTMP ingest, or WebSocket sessions depending on type),
discovers orchestrators from chain, pays them with tickets, and routes
traffic to their capability brokers.

> **Note.** This guide is the source of truth for the gateway
> deployment topology. Every stack referenced below lives in a sibling
> folder under `gateway-onboarding/`. The orchestrator onboarding flow
> is documented separately at
> [`../orchestrator-onboarding/`](../orchestrator-onboarding/).

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
>
> The AI Service Registry is this repo's testbed for replacing the
> legacy registry. If you're coming from a go-livepeer background, just
> know that the discovery contract is different by design.

## Pick a gateway type

Single-host, no load balancing. Pick one gateway type per host. You can
run multiple gateway hosts of different types in the same fleet.

| Gateway          | Status      | What it fronts                                       | Public surface                          | Folder                                                |
| ---------------- | ----------- | ---------------------------------------------------- | --------------------------------------- | ----------------------------------------------------- |
| **OpenAI**       | Stable      | OpenAI-compatible chat / audio / etc. via brokers    | HTTP `:3000`                            | [`openai-gateway/`](./openai-gateway/)                |
| **Video**        | Stable      | VOD uploads, ABR delivery, live streaming           | HTTP `:3000` + RTMP `:1935`             | [`video-gateway/`](./video-gateway/)                  |
| **Vtuber**       | **Preview** | Long-lived vtuber pipeline sessions                  | HTTP+WS `:3001`                         | [`vtuber-gateway/`](./vtuber-gateway/) ⚠               |

⚠ Vtuber is **under active development** — do not advertise sessions to
customers from that stack until the gateway is announced as
production-ready. See its README for the full preview disclaimer.

All three follow the **same shape**:

- One private compose network. Postgres + daemons stay internal.
- Service-registry-daemon resolves orchestrators via the AI Service
  Registry contract.
- payment-daemon-sender signs ticket payments with the gateway hot
  wallet.
- The gateway itself attaches to the external `ingress` network when
  fronted by Traefik or Nginx.

## Topology

```
   your customers
        │
        │ HTTPS (and RTMP for video)
        ▼
   ┌───────────────────────────────────────────────────────┐
   │  gateway host (single box, no load balancing)         │
   │                                                       │
   │   reverse proxy (Traefik or Nginx)                    │
   │           │                                           │
   │           ▼                                           │
   │   gateway service ──► service-registry-daemon         │
   │           │            (AI Service Registry on chain) │
   │           │                                           │
   │           ▼                                           │
   │   payment-daemon-sender ──► hot wallet (keystore)     │
   │                                                       │
   │   postgres (+ redis + rustfs for video gateway)       │
   └───────────────────────────────────────────────────────┘
        │
        │ work + payment tickets
        ▼
   capability brokers (on orchestrators' fleets)
```

## Prerequisites

- A Linux host with Docker Engine and `docker compose` v2.
- A funded Ethereum keystore (the gateway hot wallet). Keep its balance
  small — see [Wallet model](#wallet-model) below.
- An Arbitrum RPC endpoint (`CHAIN_RPC`). Public endpoints work for low
  volume; switch to a paid provider as traffic grows.
- A domain you control with DNS managed somewhere (Cloudflare, Route 53,
  etc.). You'll create one A/AAAA record per gateway host.
- For Cloudflare DNS-01 cert flow: an API token with `Zone:DNS:Edit` on
  the relevant zone, the zone ID, and the account ID.

## Wallet model

Every gateway uses a **single keystore** that signs everything: ticket
payments to orchestrators *and* the gateway's identity to brokers. There
is no cold/hot split available on the gateway side.

Treat the gateway keystore as a **throwaway hot wallet**:

- **Keep funding small.** Hold just enough deposit + reserve to keep
  work flowing through the orchestrators you talk to. Loss-on-compromise
  should be a number you can absorb.
- **Rotate immediately on compromise.** Generate a new key, fund it,
  install at `/opt/livepeer/keystore.json`, restart the gateway stack.
  No need to update anything on the orch side.
- **Maintain reserve.** A drained wallet means orchestrators stop
  accepting work. Top up before you hit that wall.
- **Never reuse this key for anything else** — especially not customer
  billing or treasury keys.

On-disk convention:

```
/opt/livepeer/
├── keystore.json
└── keystore-password
```

The keystore is mounted read-only into `payment-daemon-sender`. If you
co-locate multiple gateways on a single host, use subdirectories like
`/opt/livepeer/video/keystore.json` and override `PAYMENT_KEYSTORE` /
`PAYMENT_KEYSTORE_PASSWORD_FILE` per stack.

## Step 1 — Bring up the gateway

Pick your gateway folder ([`openai-gateway/`](./openai-gateway/),
[`video-gateway/`](./video-gateway/), or
[`vtuber-gateway/`](./vtuber-gateway/)) and follow its README. The
shape is the same across all three:

```sh
# Install the gateway hot wallet
sudo cp <your-funded-keystore>.json /opt/livepeer/keystore.json
sudo cp <your-keystore-password> /opt/livepeer/keystore-password

# Configure compose env
cp infra/scenarios/gateway-onboarding/<gateway>/.env.example \
   infra/scenarios/gateway-onboarding/<gateway>/.env
$EDITOR infra/scenarios/gateway-onboarding/<gateway>/.env

# Generate the secrets the gateway needs (count varies — 3 for openai,
# 5 for video, 3 for vtuber). The per-gateway README lists them.
openssl rand -hex 32

# Bring it up
docker compose \
  -f infra/scenarios/gateway-onboarding/<gateway>/docker-compose.yml \
  --env-file infra/scenarios/gateway-onboarding/<gateway>/.env \
  up -d
```

This standalone bring-up exposes the gateway directly on the host port
(`:3000` or `:3001` depending on type, and `:1935` for video RTMP). For
production HTTPS, layer one of the ingress overlays in Step 2.

## Step 2 — Pick an ingress (TLS reverse proxy)

The gateway HTTP API must terminate TLS in production. Two options
under this onboarding flow:

| Option                                              | Use when                                                                                    | Cert flow                              |
| --------------------------------------------------- | ------------------------------------------------------------------------------------------- | -------------------------------------- |
| **[`ingress-traefik/`](./ingress-traefik/)**        | Cloud host with inbound :80/:443. Want label-driven routing.                                | Cloudflare DNS-01 (default) or LE HTTP-01 |
| **[`ingress-nginx/`](./ingress-nginx/)**            | Cloud host with inbound :80/:443. Prefer auto-Let's-Encrypt with minimal config.            | LE HTTP-01 (default) or Cloudflare DNS-01 |

There is **no Cloudflare Tunnel option for gateways** — gateways are
public-facing servers, so direct inbound on :443 is expected. (Orch
operators with NAT'd / LAN hosts can use Cloudflare Tunnel; see the
orchestrator-onboarding flow.)

### How the overlays work

Each gateway scenario ships **ingress overlay files** alongside its
base compose:

| Gateway          | Traefik                        | Nginx HTTP-01                    | Nginx DNS-01                          |
| ---------------- | ------------------------------ | -------------------------------- | ------------------------------------- |
| openai-gateway   | `docker-compose.traefik.yml`   | `docker-compose.nginx.yml`       | `docker-compose.nginx-dns01.yml`      |
| video-gateway    | `docker-compose.traefik.yml`   | `docker-compose.nginx.yml`       | `docker-compose.nginx-dns01.yml`      |
| vtuber-gateway   | `docker-compose.traefik.yml`   | `docker-compose.nginx.yml`       | `docker-compose.nginx-dns01.yml`      |

Pick one overlay per gateway, layer it on top of the base compose, and
the overlay:

- drops the publicly-published gateway HTTP port (ingress handles
  inbound),
- attaches the gateway to the external `ingress` network,
- adds the ingress-specific labels / env vars / hostname mapping keyed
  off `GATEWAY_HOST` from `.env`.

Example: bring up the OpenAI gateway with Traefik in front:

```sh
# 1. Traefik
docker network create ingress
sudo cp infra/scenarios/gateway-onboarding/ingress-traefik/traefik.example.yml \
        /opt/livepeer/traefik/traefik.yml
sudo $EDITOR /opt/livepeer/traefik/traefik.yml   # set ACME email
cp infra/scenarios/gateway-onboarding/ingress-traefik/.env.example \
   infra/scenarios/gateway-onboarding/ingress-traefik/.env
$EDITOR infra/scenarios/gateway-onboarding/ingress-traefik/.env  # set CF_DNS_API_TOKEN
docker compose \
  -f infra/scenarios/gateway-onboarding/ingress-traefik/docker-compose.yml \
  --env-file infra/scenarios/gateway-onboarding/ingress-traefik/.env \
  up -d

# 2. OpenAI Gateway with Traefik overlay
$EDITOR infra/scenarios/gateway-onboarding/openai-gateway/.env  # set GATEWAY_HOST
docker compose \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.traefik.yml \
  --env-file infra/scenarios/gateway-onboarding/openai-gateway/.env \
  up -d
```

Read [`ingress-traefik/README.md`](./ingress-traefik/README.md) or
[`ingress-nginx/README.md`](./ingress-nginx/README.md) for the full
ingress-side bring-up.

### Video gateway: RTMP stays direct

The Video gateway has **two** public surfaces:

- HTTP API (`:3000` inside the container) — routes through Traefik / Nginx.
- RTMP (`:1935` inside the container) — TCP, can't be proxied through
  an HTTP router. The ingress overlays for video-gateway **keep** the
  `:1935` port mapped directly to the host. Customers ingesting via
  RTMP connect to `rtmp://${GATEWAY_HOST}:1935/...`.

Authenticate RTMP via stream keys issued by the gateway. For encrypted
ingest (RTMPS), put a separate TCP-aware proxy (HAProxy in TCP mode, or
Traefik TCP routers) in front of `:1935`. Out of scope for this guide.

## Step 3 — DNS and cert issuance

Before bring-up of the ingress overlay, point an A/AAAA record for
`${GATEWAY_HOST}` at the gateway host's public IP.

- **HTTP-01** needs inbound `:80` reachable from Let's Encrypt servers
  for the challenge.
- **DNS-01** (Cloudflare) needs the API token + zone IDs in the gateway
  `.env`. No inbound `:80` required for cert issuance, but `:443` still
  has to be reachable for inbound HTTPS traffic.

Cert issuance is automatic and fires once the gateway registers with
nginx-proxy or once Traefik picks up its labels. Check with:

```sh
# Traefik
docker exec traefik cat /ssl-certs/acme-cloudflare.json | jq '.cloudflare.Certificates | length'

# Nginx
docker exec nginx-proxy ls /etc/nginx/certs/
```

## Step 4 — Fund the wallet and verify

1. Send a small amount of ETH to the gateway's address. Enough for
   roughly your first day of expected traffic plus headroom — you can
   always top up.
2. Open a deposit / reserve with the orchestrators you intend to use,
   via the gateway's normal session-open flow. (Specifics depend on
   gateway type; see the per-gateway README.)
3. End-to-end verify:

```sh
# Gateway reachable over TLS
curl -sI https://${GATEWAY_HOST}/

# service-registry-daemon found orchestrators on chain
docker compose logs service-registry-daemon | grep -i broker

# payment-daemon loaded the keystore
docker compose logs payment-daemon-sender | grep -i wallet

# Make a small test request against your gateway type's API
# (per-gateway examples are in each scenario README)
```

## Operational notes

### Monitoring wallet balance

The gateway will halt outgoing tickets when the on-chain deposit drops
below the protocol minimum. Watch the address on a block explorer or
script a periodic `eth_getBalance` check. Top up before you hit the
floor.

### Rotating the wallet

1. Generate a new keystore (any standard Ethereum tooling).
2. Fund it from your treasury / off-cold-storage.
3. Move the new files in:
   ```sh
   sudo cp <new-keystore>.json /opt/livepeer/keystore.json
   sudo cp <new-keystore-password> /opt/livepeer/keystore-password
   ```
4. Restart the stack:
   ```sh
   docker compose -f .../docker-compose.yml -f .../docker-compose.<ingress>.yml restart
   ```
5. Drain the old wallet on its own schedule.

Customer-facing service shouldn't see more than a few seconds of
disruption.

### Route health (three-layer model)

The gateway picks a broker route by composing three layers:

1. **Layer 1 (manifest)** — `service-registry-daemon` only returns
   candidates the orch has cold-signed for the requested capability.
2. **Layer 2 (live)** — the resolver polls each broker's
   `/registry/health` and drops anything not `ready`.
3. **Layer 3 (failure-rate)** — the gateway tracks per-route outcomes
   and opens a short cooldown after repeated retryable failures.

You only configure Layer 3 here. Two knobs in each gateway's `.env`,
shared across openai / video / vtuber:

| Knob                               | Default | Effect                                                       |
| ---------------------------------- | ------- | ------------------------------------------------------------ |
| `LIVEPEER_ROUTE_FAILURE_THRESHOLD` | `2`     | Failures before a route enters cooldown                      |
| `LIVEPEER_ROUTE_COOLDOWN_MS`       | `30000` | How long the route stays excluded before being retried (ms)  |

If a route disappears from selection unexpectedly, the fastest debug
question is **which layer dropped it?** Layer 1 is an orch-side fix
(manifest), Layer 2 is a broker-side fix (probe / backend), Layer 3 is
gateway-local (wait it out, restart, or retune the knobs). See
[`docs/design-docs/backend-health.md`](../../../docs/design-docs/backend-health.md)
for the full model.

### Centralized observability

Each gateway exposes Prometheus metrics on the `service-registry-daemon`
side. For multi-host fleets, the same advanced pattern from the orch
onboarding applies — route metrics endpoints through your ingress with
auth middleware. Skip this if you're running just one or two gateways.

### Multi-gateway operators

If you run more than one gateway type (e.g. openai + video), you can:

- Run each on its own host (recommended — simpler ops, separate
  blast radius).
- Co-locate on one host. Use subdirectories under `/opt/livepeer/` for
  each gateway's keystore, set different `GATEWAY_HOST` values, and run
  both compose stacks side by side on the same `ingress` network. One
  Traefik or Nginx fronts both.

## Where to go next

- **Per-gateway deep-dives.** Each gateway folder has its own README —
  use those as the operator runbook once you've picked a type.
- **Orchestrator side.** Gateways need orchestrators to talk to. The
  [`../orchestrator-onboarding/`](../orchestrator-onboarding/) flow
  walks bringing up the matching capability-broker fleet that this
  side discovers via the AI Service Registry.
- **Vtuber preview.** If you're tracking the vtuber pipeline, watch
  [`vtuber-gateway/README.md`](./vtuber-gateway/README.md) for the
  preview → stable transition.

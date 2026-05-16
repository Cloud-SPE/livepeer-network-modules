# OpenAI Gateway

Sender-side gateway that exposes an OpenAI-compatible API to your
customers and routes their requests to capability brokers it discovers
from the on-chain AI Service Registry.

Single-host, no load balancing. Public surface is the gateway HTTP API
on `:3000` — production must terminate TLS at a reverse proxy in front
(Traefik or Nginx; see overlays below).

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

## Role in the topology

```
your customer ──HTTPS──►  reverse proxy
                              │
                              ▼
                       openai-gateway ─┐
                              │        │ (discovers via on-chain
                              │        │  AI Service Registry)
                              ▼        ▼
                       payment-daemon  service-registry-daemon
                       sender           │
                              │         │
                              ▼         ▼
                       capability-broker (on some orchestrator's fleet)
```

## What runs here

| Service                   | Purpose                                                       |
| ------------------------- | ------------------------------------------------------------- |
| `openai-gateway`          | OpenAI-compatible HTTP API; the public surface                |
| `service-registry-daemon` | Resolves the orchestrator manifest registry from chain        |
| `payment-daemon` sender   | Signs and sends ticket payments to orchestrators              |
| `postgres`                | Customer + session state for the gateway                      |

All four share a private compose-managed network. Only `openai-gateway`
attaches to the external `ingress` network (via the overlays) so the
reverse proxy can reach it. Postgres and the daemons never touch the
internet.

## Wallet model — throwaway hot wallet

The gateway has **one keystore** that signs everything:

- ticket-payment transactions to orchestrators
- gateway identity when talking to capability brokers

There is no cold/hot split on the gateway side. Treat this key as a
**throwaway hot wallet**:

- **Keep funding small.** Hold just enough deposit + reserve to keep work
  flowing through the orchestrators you talk to. The exact figure
  depends on your customer volume and the brokers' prices, but you want
  the worst-case loss-on-compromise to be a number you can absorb.
- **Rotate immediately on compromise.** Generate a new key, fund it,
  install the new keystore at `/opt/livepeer/keystore.json`, restart the
  stack. No need to update anything else — the gateway re-derives its
  on-chain identity from the keystore.
- **Maintain reserve.** If the wallet runs out of deposit mid-session,
  orchestrators stop accepting work. Top up before you hit that wall.
- **Never reuse this key for anything else.** Especially not your
  customer billing or treasury keys.

## On-disk layout

```
/opt/livepeer/
├── keystore.json
└── keystore-password
```

Both files mounted read-only into `payment-daemon-sender`.

## Bring-up

```sh
# 1. Install your gateway hot-wallet keystore
sudo cp <your-funded-keystore>.json /opt/livepeer/keystore.json
sudo cp <your-keystore-password> /opt/livepeer/keystore-password

# 2. Configure compose env
cp infra/scenarios/gateway-onboarding/openai-gateway/.env.example \
   infra/scenarios/gateway-onboarding/openai-gateway/.env
$EDITOR infra/scenarios/gateway-onboarding/openai-gateway/.env

# Generate the three required secrets:
openssl rand -hex 24  # → POSTGRES_PASSWORD
openssl rand -hex 32  # → CUSTOMER_PORTAL_AUTH_PEPPER
openssl rand -hex 32  # → OPENAI_GATEWAY_ADMIN_TOKENS

# 3. Bring it up
docker compose \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.yml \
  --env-file infra/scenarios/gateway-onboarding/openai-gateway/.env \
  up -d
```

This bring-up exposes `:3000` directly on the host. For production, layer
one of the ingress overlays below — the overlay drops the port and
routes through TLS.

## Required values

In `.env` before bring-up:

- **`POSTGRES_PASSWORD`** — generated, used by both postgres init and the
  gateway's `DATABASE_URL`.
- **`CUSTOMER_PORTAL_AUTH_PEPPER`** — generated. Rotating this
  invalidates all issued portal tokens.
- **`OPENAI_GATEWAY_ADMIN_TOKENS`** — generated. Used to authenticate
  operator calls against the gateway's admin API.
- **`CHAIN_RPC`** — your Arbitrum RPC endpoint. Public endpoints work
  for low volume; switch to a paid provider as customer traffic grows.

Already have sensible defaults you usually don't change:

- `AI_SERVICE_REGISTRY_ADDRESS`, `CONTROLLER_ADDRESS`, `CHAIN_ID` — pinned
  to the production Arbitrum contracts.
- `PAYMENT_KEYSTORE` / `PAYMENT_KEYSTORE_PASSWORD_FILE` — only override
  if your keystore lives somewhere other than `/opt/livepeer/`.

## Route health

The gateway composes three layers when picking a broker route:

1. **Layer 1 (manifest)** — `service-registry-daemon` only returns
   candidates whose orchestrator has cold-signed for the requested
   capability.
2. **Layer 2 (live)** — the resolver polls each broker's
   `/registry/health` and drops offerings not currently `ready`.
3. **Layer 3 (failure-rate)** — this gateway tracks per-route outcomes
   and opens a short **cooldown** for a route after repeated retryable
   failures, even if Layer 1+2 still say it's available.

You only manage Layer 3 here. Two `.env` knobs:

| Knob                                 | Default | Effect                                                                      |
| ------------------------------------ | ------- | --------------------------------------------------------------------------- |
| `LIVEPEER_ROUTE_FAILURE_THRESHOLD`   | `2`     | Consecutive retryable failures before a route enters cooldown.              |
| `LIVEPEER_ROUTE_COOLDOWN_MS`         | `30000` | How long the route stays excluded from selection (ms) before being retried. |

If a route disappears from the gateway and you don't know why, ask
**which layer dropped it?**

- Layer 1: missing or mismatched signed manifest entry (operator hasn't
  signed for it). Fix on the orch side.
- Layer 2: broker reports the offering as `degraded` / `unreachable` /
  `stale` on `/registry/health`. Fix on the broker side.
- Layer 3: this gateway opened a cooldown after recent failures. Either
  wait it out (`LIVEPEER_ROUTE_COOLDOWN_MS`), restart the gateway, or
  tune the threshold.

See [`docs/design-docs/backend-health.md`](../../../../docs/design-docs/backend-health.md)
for the three-layer model end to end.

## Verify

```sh
# Gateway responds on its OpenAI-compatible endpoint
curl -s http://127.0.0.1:3000/v1/models | jq .

# service-registry-daemon found brokers from chain
docker compose logs service-registry-daemon | grep -i broker

# payment-daemon loaded the keystore
docker compose logs payment-daemon-sender | grep -i wallet

# Gateway admin API responds with your token
curl -s -H "Authorization: Bearer $(grep ^OPENAI_GATEWAY_ADMIN_TOKENS .env | cut -d= -f2)" \
  http://127.0.0.1:3000/admin/health
```

## Fronted by Traefik

For production, run [ingress-traefik](../ingress-traefik/) on the same
box and layer the Traefik overlay on top. The overlay drops the public
`:3000` port mapping and adds the router labels for `GATEWAY_HOST`.

```sh
$EDITOR infra/scenarios/gateway-onboarding/openai-gateway/.env  # set GATEWAY_HOST
docker compose \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.traefik.yml \
  --env-file infra/scenarios/gateway-onboarding/openai-gateway/.env \
  up -d
```

## Fronted by Nginx (nginx-proxy + acme-companion)

Auto Let's Encrypt with either HTTP-01 (default) or Cloudflare DNS-01.
Run [ingress-nginx](../ingress-nginx/) on the same box and layer ONE of
the Nginx overlays on top — not both.

**HTTP-01** (simpler; requires inbound :80):

```sh
docker compose \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.nginx.yml \
  --env-file infra/scenarios/gateway-onboarding/openai-gateway/.env \
  up -d
```

**Cloudflare DNS-01** (no inbound :80 needed; requires API token + zone IDs):

```sh
# In .env: set CF_DNS_API_TOKEN, CF_ACCOUNT_ID, CF_ZONE_ID
docker compose \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.yml \
  -f infra/scenarios/gateway-onboarding/openai-gateway/docker-compose.nginx-dns01.yml \
  --env-file infra/scenarios/gateway-onboarding/openai-gateway/.env \
  up -d
```

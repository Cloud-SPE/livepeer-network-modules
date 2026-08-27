# Capability Broker

The broker is the per-data-center entry point that gateways call to reach
your capabilities. Run one broker (or a load-balanced pair) per data
center / home setup. The broker can:

- live on the same box as the capabilities it advertises (single-box rig), or
- live on a dedicated box that routes to capabilities running on other
  servers in the same data center.

Either way: gateways resolve your manifest from the **Orch Coordinator**,
find the broker's URL there (the `base_url` you listed under `brokers[]`),
and call this stack directly.

## Role in the topology

```
gateway ─┐
         │  (manifest tells gateways which broker to call)
         ▼
   orch-coordinator (public)
         │  capability-broker URL
         ▼
   capability-broker  ◄─  workloads attach outbound
   payment-daemon         (vLLM / ABR / etc., each behind an agent)
```

## What runs here

| Service                   | Purpose                                                    |
| ------------------------- | ---------------------------------------------------------- |
| `capability-broker`       | Sells offers, routes inbound work to attached runners      |
| `payment-daemon` receiver | Redeems Livepeer payment tickets on-chain for this broker  |

What runs **alongside** this stack (and is *not* in `host-config.yaml`):

- The actual capability workloads — vLLM, ABR, vtuber pipeline, etc. They
  can be containers on this same box or on other machines. Each host runs
  an agent that attaches outbound to this broker and declares what it runs;
  the broker never dials them, so they need no inbound port and no DNS
  entry.

## Listeners

| Port | Visibility | Purpose                                                                      |
| ---- | ---------- | ---------------------------------------------------------------------------- |
| 8080 | **Public** | Broker API (`/registry/offerings`, `/registry/health`, `/healthz`, paid traffic) and the WebSocket runner-attach fallback |
| 8443/udp | **Public** | Runner attach over QUIC (preferred; the WebSocket on 8080 is the egress-friendly fallback) |
| 9090 | Private    | Prometheus metrics                                                           |

Production must terminate TLS in front of 8080. A reverse-proxy (Traefik)
reference is documented separately.

### Health endpoints on :8080

| Path                | Returns                                                            |
| ------------------- | ------------------------------------------------------------------ |
| `GET /healthz`      | Broker process liveness (200 once the process is up)               |
| `GET /registry/health` | Per-`(capability, offering)` live-health snapshot — `ready` / `draining` / `degraded` / `unreachable` / `stale` |

`/registry/health` is what resolvers and gateways consult before routing
paid traffic. Nothing is polled to produce it: an offer is `ready` when a
runner that certified for it still has its attach tunnel up, and
`unreachable` when none does (see below). See
[`docs/design-docs/backend-health.md`](../../../../docs/design-docs/backend-health.md)
for the full three-layer model (manifest / live / failure-rate).

## On-disk layout

```
/opt/livepeer/
├── payment-keystore.json          # hot wallet for ticket redemption
├── payment-keystore-password
├── broker-seal.key                # seals the attach-credential store
└── host-config.yaml               # the offers this broker sells
```

`broker-seal.key` is 32 bytes of randomness
(`openssl rand -hex 32 > /opt/livepeer/broker-seal.key`). Back it up: losing
it means every workload host has to re-enrol.

### Keys on this box

The keystore here is the **hot wallet** that signs payment-ticket
redemption transactions. It is a **different** key from the cold orch
keystore on your Secure Orch host. The hot wallet must:

- hold enough ETH to pay redemption gas
- have redeem authority on behalf of `ORCH_ADDRESS` (the cold orch identity
  set on your Secure Orch host)

Do not copy your cold orch keystore to this box.

### host-config.yaml

Defines the **offers** this broker sells: capability, price, capacity,
metadata, which attached runners each offer is for (`match`), and the
`certification` a runner must pass to serve it. It does not say where any
workload lives — the runner declares its own endpoint, transports, work
unit, extractor and readiness when it attaches. Each broker box in your
fleet has its own host-config reflecting what that location sells.

Example host-configs live in [`host-configs/`](./host-configs/). Copy one
to `/opt/livepeer/host-config.yaml` (or wherever `BROKER_CONFIG` points)
and edit:

| Variant                                            | Status | Capability                                          | Pair with |
| -------------------------------------------------- | ------ | --------------------------------------------------- | --------- |
| [`openai-chat.example.yaml`](./host-configs/openai-chat.example.yaml) | Stable | `openai:chat-completions` (vLLM, one `paid-job/v1` offer) | your vLLM deployment |

The backend service does not need to be reachable *from* the broker. Run an
agent next to it (`pool-member-agent`), give it an enrolled credential, and
it attaches outbound; the broker sends work back down that connection.

### Notes on the example host-configs

- **The runner names the work-unit extractor, not you.** The chat variant's
  vLLM runner declares `openai-usage`; other runners declare
  `request-formula`, `bytes-counted`, and so on. You set the price, the
  runner supplies the unit it is counted in, and an extractor the broker
  does not implement is rejected when the runner attaches — with the field
  and both sides named — rather than at broker startup.
- **Certification replaces the health probe.** Each offer's
  `certification:` list is what a matched runner must pass before it is
  advertised or given paid work. If it fails, the offering is reported
  `unreachable` and gateways skip routing here — the signed manifest is not
  touched. See "Readiness and certification" below.
- **One offer serves every transport its runner declares.** This
  host-config used to keep two offerings for the same Qwen model, one
  `[unary]` and one `[unary, stream]`. Transports are the runner's to
  declare now, so that split is not expressible — and it was already
  unnecessary: a single offering declaring both served streaming and
  non-streaming callers, who select per request with ordinary HTTP
  negotiation. Split into two offers only to price or constrain them
  differently, and give each a distinct `offering_id`.
- **`constraints` is operator-supplied metadata.** Gateways may use it
  to route requests to brokers with the hardware they expect (e.g.
  `gpu: "4090"`, `gpu_model: "1080"`, `gpu_vendor: "NVIDIA"`).

### Readiness and certification

The broker does not poll your backends. It never learned their URLs, and a
probe result is stale the moment it lands — whereas for an attached runner
both questions a probe existed to answer are already settled: certification
says whether it can serve the offer, and the attach tunnel says whether it
is reachable right now.

Readiness still has a recipe, but the **runner** picks it, because the
runner is the only party that knows what ready means for it:

| Readiness `type`            | Declared by a runner that…                                                                |
| --------------------------- | ----------------------------------------------------------------------------------------- |
| `http-status`               | exposes a plain `/healthz` (or similar) returning 2xx when ready.                          |
| `http-jsonpath`             | returns JSON from its health endpoint and needs a specific field asserted.                 |
| `http-openai-model-ready`   | is OpenAI-compatible (vLLM, OpenAI SaaS) — checks `/v1/models` lists the model.            |
| `tcp-connect`               | is not HTTP — just opens a TCP socket.                                                     |

What you write is how much readiness is *enough*, plus the exchanges that
prove the runner really serves and meters work:

```yaml
certification:
  - { name: ready, type: readiness, config: { attempts: 10, interval_ms: 3000 } }
  - name: smoke
    type: request
    config:
      transport: unary
      body: { model: "{{identity.openai.model}}", messages: [ { role: user, content: "ping" } ], max_tokens: 8 }
      assert: [ "$.choices[0].message.content" ]
  - { name: usage, type: usage, config: { min_units: 1 } }
```

Set `recertify_every_seconds` to re-prove periodically, so a model that was
unloaded or a GPU that dropped out stops receiving paid work on its own.

Operator surfaces for the three health layers:

| Layer            | Operator action                                  | Where             |
| ---------------- | ------------------------------------------------ | ----------------- |
| 1 (manifest)     | Edit `offers[]` in `host-config.yaml`, run sign cycle | secure-orch-console |
| 2 (live)         | Re-attach or fix the runner, disable the offer   | broker `/registry/health` + `/admin/v1/runners` |
| 3 (failure-rate) | Inspect dashboards, declare incident             | metrics / alerting stack |

See [`docs/design-docs/backend-health.md`](../../../../docs/design-docs/backend-health.md)
for the full model.

## Bring-up

```sh
cp infra/scenarios/orchestrator-onboarding/capability-broker/.env.example \
   infra/scenarios/orchestrator-onboarding/capability-broker/.env
$EDITOR infra/scenarios/orchestrator-onboarding/capability-broker/.env

# Drop your hot-wallet keystore + password and your host-config.yaml at
# /opt/livepeer/ (or override the paths in .env).

docker compose \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/capability-broker/.env \
  up -d
```

## Required values

You must set these in `.env` before bring-up:

- `ORCH_ADDRESS` — your cold orch on-chain address
- `CHAIN_RPC` — Arbitrum RPC endpoint for ticket redemption
- `BROKER_ADMIN_TOKEN` — bearer for the private admin surface. Required:
  enrolling a runner (`POST /admin/v1/enroll`) is how anything gets served,
  and the broker refuses to start with `admin_auth: bearer` and no token.
- `BROKER_SEAL_KEY` — only if `broker-seal.key` lives somewhere other than
  `/opt/livepeer/`
- `PAYMENT_KEYSTORE` / `PAYMENT_KEYSTORE_PASSWORD_FILE` — only if your hot
  wallet keystore lives somewhere other than `/opt/livepeer/`
- `BROKER_CONFIG` — only if `host-config.yaml` lives somewhere other than
  `/opt/livepeer/`

## Verify

```sh
# Broker process is up
curl -sf http://127.0.0.1:8080/healthz

# Broker capability advertisement. Empty until a runner has attached AND
# passed the offer's certification — an offer nobody can serve is never
# published, deliberately.
curl -s http://127.0.0.1:8080/registry/offerings | jq .

# Who is attached, and why an offer is or is not being served by them
curl -s -H "Authorization: Bearer $BROKER_ADMIN_TOKEN" \
  http://127.0.0.1:8080/admin/v1/runners | jq .
curl -s -H "Authorization: Bearer $BROKER_ADMIN_TOKEN" \
  http://127.0.0.1:8080/admin/v1/certification | jq .

# Per-(capability, offering) live health — every entry should reach `ready`
# once a runner has attached and certified. `unreachable` here means no
# certified runner currently has a live tunnel for that offer; check
# `GET /admin/v1/runners` for why.
curl -s http://127.0.0.1:8080/registry/health | jq .

# Prometheus metrics
curl -s http://127.0.0.1:9090/metrics | head
```

Once the broker is up, all offerings report `ready` on `/registry/health`,
and its URL is listed under `brokers[]` in your Orch Coordinator's
`coordinator-config.yaml`, the next manifest publish will surface it to
gateways.

## Fleet pattern

You typically run one of these per data center / home setup. Each broker
in your fleet has its own `host-config.yaml` reflecting that location's
hardware. Your `coordinator-config.yaml` on the Orch Coordinator lists all
brokers in your fleet (see `infra/scenarios/orchestrator-onboarding/orch-coordinator/`).

## Fronted by Traefik

For production, run this on the same box as the
[ingress-traefik](../ingress-traefik/) stack and layer the Traefik
overlay on top. The overlay drops the public 8080 port mapping (Traefik
handles it through the `ingress` network) and adds the router labels for
this broker's public hostname.

```sh
$EDITOR infra/scenarios/orchestrator-onboarding/capability-broker/.env   # set BROKER_HOST
docker compose \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.traefik.yml \
  --env-file infra/scenarios/orchestrator-onboarding/capability-broker/.env \
  up -d
```

`BROKER_HOST` must match the `base_url` you listed for this broker in
your Orch Coordinator's `coordinator-config.yaml`. Repeat the bring-up
on every broker box in your fleet — each gets its own hostname.

See `docker-compose.traefik.yml` and `infra/scenarios/orchestrator-onboarding/ingress-traefik/`
for the full topology.

## Fronted by Cloudflare Tunnel

Alternative to Traefik for hosts behind NAT or without inbound ports
(e.g. home / LAN rigs). Run
[ingress-cloudflared](../ingress-cloudflared/) on the same box and
layer the cloudflared overlay on top:

```sh
docker compose \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.cloudflared.yml \
  --env-file infra/scenarios/orchestrator-onboarding/capability-broker/.env \
  up -d
```

Then in the Cloudflare Zero Trust dashboard, add a Public Hostname:

| Field        | Value                                                                                  |
| ------------ | -------------------------------------------------------------------------------------- |
| Subdomain    | _per-broker, e.g. `broker-a`, `local-rig-1`_                                           |
| Service URL  | `capability-broker:8080`                                                               |

The resulting URL **must match** this broker's `base_url` entry in your
Orch Coordinator's `coordinator-config.yaml`.

The cloudflared overlay does not require `BROKER_HOST` or
`TRAEFIK_CERTRESOLVER` — hostname mapping lives in Cloudflare's UI.

## Fronted by Nginx (nginx-proxy + acme-companion)

Auto Let's Encrypt with either HTTP-01 (default) or Cloudflare DNS-01.
Run [ingress-nginx](../ingress-nginx/) on the same box and layer ONE of
the Nginx overlays on top — not both.

**HTTP-01** (simpler; requires inbound :80):

```sh
docker compose \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.nginx.yml \
  --env-file infra/scenarios/orchestrator-onboarding/capability-broker/.env \
  up -d
```

**Cloudflare DNS-01** (no inbound :80 needed; requires API token + zone IDs):

```sh
# In .env: set CF_DNS_API_TOKEN, CF_ACCOUNT_ID, CF_ZONE_ID
docker compose \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.nginx-dns01.yml \
  --env-file infra/scenarios/orchestrator-onboarding/capability-broker/.env \
  up -d
```

`BROKER_HOST` is reused from the Traefik overlay — swapping ingress
flavors on a host doesn't require .env changes (other than supplying
the DNS-01 credentials when you pick that overlay).

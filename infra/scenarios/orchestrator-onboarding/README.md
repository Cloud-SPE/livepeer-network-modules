# Orchestrator Onboarding Guide

A step-by-step path for orchestrators bringing a fleet online. The fleet
is decomposed across three host roles, each with a self-contained stack
under this folder. You'll stand them up in order, then pick an ingress
flavor that matches your hosts.

> **Note.** This guide is the source of truth for the orchestrator
> deployment topology. Every stack referenced below lives in a sibling
> folder here — never the repo's archive.

## What you're building

```
┌──────────────────────┐         ┌──────────────────────┐
│  Secure Orch         │         │  Orch Coordinator    │
│  (cold-key host)     │   ───►  │  (public registry)   │
│  no inbound internet │         │  serves manifest URL │
└──────────────────────┘         └──────────────────────┘
                                            │
                                            │  manifest tells
                                            │  gateways which
                                            │  broker to call
                                            ▼
   ┌────────────────────────────────────────────────────┐
   │  Capability Broker(s) — one per data center / rig  │
   │  + payment-daemon + workload runners (vLLM, etc.)  │
   └────────────────────────────────────────────────────┘
```

Three host roles, plus one ingress proxy on every public-facing host:

| Host role            | What runs there                                                | Inbound internet | Holds key      |
| -------------------- | -------------------------------------------------------------- | ---------------- | -------------- |
| **Secure Orch**      | `protocol-daemon`, `service-registry-daemon`, `secure-orch-console` | No           | Cold orch key  |
| **Orch Coordinator** | `orch-coordinator` (+ ingress)                                 | Yes (HTTPS only) | None           |
| **Capability Broker**| `capability-broker`, `payment-daemon-receiver` (+ ingress + workload runners) | Yes (HTTPS only) | Hot payment wallet (one per broker box) |

Add one Capability Broker host per data center / home rig. The other two
roles are single-host.

## Prerequisites

- Linux hosts with Docker Engine and `docker compose` v2.
- An on-chain orchestrator address (`ORCH_ADDRESS`) and its keystore.
- One Arbitrum RPC endpoint (`ETH_URLS`) for chain reads/writes.
- Funded hot wallet keys for ticket redemption on every broker box.
- One domain you control, with DNS managed somewhere (Cloudflare,
  Route 53, etc.). You'll create one record per public-facing host.
- (For Cloudflare Tunnel option) a Cloudflare Zero Trust account.

## Step 1 — Secure Orch (cold-key host)

The cold-key host signs manifest candidates with your orchestrator's
private key. It must be firewalled: **no inbound internet**. Outbound to
your Arbitrum RPC endpoints is the only external traffic.

**On-disk convention:**

```
/opt/livepeer/
├── keystore.json
└── keystore-password
```

**Bring it up:** see
[`secure-orch-control-plane/README.md`](./secure-orch-control-plane/README.md)
for the full walkthrough. Short form:

```sh
cp infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/.env.example \
   infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/.env
# Set ORCH_ADDRESS, ETH_URLS, SECURE_ORCH_ADMIN_TOKENS, keystore paths
docker compose \
  -f infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/.env \
  up -d
```

You do **not** put any reverse proxy in front of this host. It is
internet-isolated by design.

## Step 2 — Orch Coordinator (public registry)

The Orch Coordinator publishes your signed manifest at
`https://coordinator.<your-domain>/.well-known/livepeer-registry.json`.
That URL is what you'll later publish on the AI Service Registry
contract — it's how gateways discover you.

**On-disk convention:**

```
/opt/livepeer/
└── coordinator-config.yaml      # defines your fleet of brokers
```

The `coordinator-config.yaml` lists every Capability Broker in your
fleet by name and `base_url`. Names are arbitrary — pick something that
helps you keep track (e.g. `local-rig-1`, `eu-central`, `us-west`). See
[`orch-coordinator/coordinator-config.example.yaml`](./orch-coordinator/coordinator-config.example.yaml)
for the shape.

**Bring it up:** see
[`orch-coordinator/README.md`](./orch-coordinator/README.md). Short form:

```sh
sudo cp infra/scenarios/orchestrator-onboarding/orch-coordinator/coordinator-config.example.yaml \
        /opt/livepeer/coordinator-config.yaml
sudo $EDITOR /opt/livepeer/coordinator-config.yaml      # set orch_eth_address + brokers[]

cp infra/scenarios/orchestrator-onboarding/orch-coordinator/.env.example \
   infra/scenarios/orchestrator-onboarding/orch-coordinator/.env
# Set ORCH_COORDINATOR_ADMIN_TOKENS, COORDINATOR_HOST
docker compose \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/orch-coordinator/.env \
  up -d
```

You still need to put TLS in front of port 8081 on this host — that's
the **Ingress** step below.

## Step 3 — Capability Broker(s)

Run **one broker per data center / home setup**. Each broker box has its
own:

- `host-config.yaml` describing which capabilities it advertises and
  where the backing workloads live
- hot-wallet keystore at `/opt/livepeer/payment-keystore.json` (**not**
  the cold orch key — separate, funded with ETH for ticket-redemption gas)
- public hostname matching the `base_url` you listed for it in your
  Orch Coordinator's `coordinator-config.yaml`

**On-disk convention:**

```
/opt/livepeer/
├── payment-keystore.json
├── payment-keystore-password
└── host-config.yaml             # capability mix for THIS broker box
```

**Capability mix.** What each broker advertises is up to you — it varies
by hardware. Reference host-configs live under
[`capability-broker/host-configs/`](./capability-broker/host-configs/):

| Variant                                              | Status      | Capability                                                          |
| ---------------------------------------------------- | ----------- | ------------------------------------------------------------------- |
| [`openai-audio.example.yaml`](./capability-broker/host-configs/openai-audio.example.yaml) | Stable      | `openai:audio-transcriptions` (Whisper) + `openai:audio-speech` (Kokoro) |
| [`openai-chat.example.yaml`](./capability-broker/host-configs/openai-chat.example.yaml)   | Stable      | `openai:chat-completions` (vLLM, stream + reqresp paired offerings) |
| [`preview/video-transcode.example.yaml`](./capability-broker/host-configs/preview/video-transcode.example.yaml) | **Preview** | `video:transcode.vod` (NVIDIA) — gateway integration not yet shipped; do not advertise on live network |

The workload runners (Whisper, Kokoro, vLLM, etc.) live alongside the
broker on the same Docker network, OR on separate boxes the broker
proxies to. Either way, the broker reaches them via the `backend.url`
field in `host-config.yaml`. Reference runner composes for the OpenAI
stacks ship under `openai-runners/compose/`.

**Work-unit extractors.** The example host-configs demonstrate four
extraction patterns, one per `extractor.type`:

- `response-header` — runner reports work units in a response header
  (Whisper: seconds of audio)
- `request-formula` with a JSONPath expression — count something on the
  inbound request (Kokoro: input characters)
- `request-formula` with literal `1` — per-job billing (preview transcode)
- `openai-usage` — read `total_tokens` straight from the OpenAI usage
  block (vLLM chat)

Pick whichever your runner can support; mix freely.

**Health probes.** Each capability also declares a `health.probe` block.
The broker probes the backend on cadence and exposes the result on
`GET /registry/health` — gateways consult that surface before routing
paid traffic and skip offerings that are `unreachable`, `degraded`, or
`draining`. When a backend dies, the route disappears from gateway
selection without forcing a fresh sign cycle on your manifest. The
example host-configs ship probes that fit each backend
(`http-openai-model-ready` for vLLM, `http-status` against `/healthz`
for the audio runners). See the capability-broker scenario README for
the full probe-type table and
[`docs/design-docs/backend-health.md`](../../../docs/design-docs/backend-health.md)
for the three-layer model (manifest / live / failure-rate).

**Bring it up:** see
[`capability-broker/README.md`](./capability-broker/README.md). Short form,
repeated on every broker host:

```sh
sudo cp infra/scenarios/orchestrator-onboarding/capability-broker/host-configs/openai-chat.example.yaml \
        /opt/livepeer/host-config.yaml
sudo $EDITOR /opt/livepeer/host-config.yaml             # set orch_eth_address + backend urls

cp infra/scenarios/orchestrator-onboarding/capability-broker/.env.example \
   infra/scenarios/orchestrator-onboarding/capability-broker/.env
# Set ORCH_ADDRESS, CHAIN_RPC, BROKER_HOST (per-box hostname)
docker compose \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/capability-broker/.env \
  up -d
```

## Step 4 — Pick an ingress flavor (per host)

Both the Orch Coordinator host and every Capability Broker host need TLS
in front of their public listeners. You have **three** options, all of
which:

- run alongside the Livepeer service on the same box,
- attach to a shared external Docker network named `ingress`,
- consume the same `COORDINATOR_HOST` / `BROKER_HOST` env vars on each
  downstream service.

Mix freely — pick the right one per host based on its network shape.

| Option                                                                   | Use when                                                                                    | Cert mechanism                       |
| ------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------- | ------------------------------------ |
| **[`ingress-traefik/`](./ingress-traefik/)**                             | Cloud / VPS hosts with inbound :80/:443. Want label-driven routing.                          | Cloudflare DNS-01 (default) or LE HTTP-01 |
| **[`ingress-cloudflared/`](./ingress-cloudflared/)**                     | LAN / home / NAT'd hosts that can't open inbound ports. Want Cloudflare Zero Trust in front. | Cloudflare manages certs at the edge |
| **[`ingress-nginx/`](./ingress-nginx/)**                                 | Cloud hosts where you prefer nginx + automatic Let's Encrypt with minimal config.            | LE HTTP-01 (default) or Cloudflare DNS-01 |

### How the overlays work

Each downstream service (orch-coordinator, capability-broker) ships an
**overlay compose file** per ingress option:

| Downstream service | Traefik                              | Cloudflare Tunnel                       | Nginx HTTP-01                       | Nginx DNS-01                              |
| ------------------ | ------------------------------------ | --------------------------------------- | ----------------------------------- | ----------------------------------------- |
| orch-coordinator   | `docker-compose.traefik.yml`         | `docker-compose.cloudflared.yml`        | `docker-compose.nginx.yml`          | `docker-compose.nginx-dns01.yml`          |
| capability-broker  | `docker-compose.traefik.yml`         | `docker-compose.cloudflared.yml`        | `docker-compose.nginx.yml`          | `docker-compose.nginx-dns01.yml`          |

You **pick one overlay per host**, layer it on top of the base compose,
and the overlay:

- drops the publicly-published port (ingress handles inbound),
- attaches the service to the external `ingress` network,
- adds the ingress-specific labels / env vars / hostname mapping.

Example: bring up the Orch Coordinator host with Traefik in front:

```sh
docker compose \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.traefik.yml \
  --env-file infra/scenarios/orchestrator-onboarding/orch-coordinator/.env \
  up -d
```

Read the matching ingress scenario README for full bring-up steps —
they walk you through the Traefik / cloudflared / Nginx stack itself
and the per-node label/env setup.

### Mixing across the fleet

A real orchestrator deployment is often heterogeneous:

| Host                  | Likely ingress                                              |
| --------------------- | ----------------------------------------------------------- |
| Orch Coordinator      | Traefik (cloud) or Nginx (cloud)                            |
| Cloud Capability Broker | Traefik or Nginx                                          |
| LAN / home Capability Broker | Cloudflare Tunnel                                    |

All three speak the same `ingress` network name and the same downstream
env vars, so the overlay you layer is the only thing that changes.

## Step 5 — Sign and publish the first manifest

After the cold-key host is up (Step 1), the coordinator is up and
fronted by an ingress (Steps 2 + 4), and at least one broker is up and
fronted by an ingress (Steps 3 + 4):

1. From an operator workstation, authenticate to the **Secure Orch
   console** using one of the `SECURE_ORCH_ADMIN_TOKENS` you generated
   in Step 1. The console runs on the cold-key host's loopback or
   private LAN — reach it over SSH.
2. Build a manifest candidate from your current `coordinator-config.yaml`
   on the **Orch Coordinator** host. Submit it to the console for
   signing. The console signs with the cold key and returns the signed
   blob.
3. Push the signed blob to the coordinator's admin API (`:8080`) using
   one of the `ORCH_COORDINATOR_ADMIN_TOKENS` you generated in Step 2.
4. Verify the public endpoint serves it:

   ```sh
   curl -s https://coordinator.<your-domain>/.well-known/livepeer-registry.json | jq .
   ```

5. Publish the public URL on the AI Service Registry contract using
   your usual on-chain tooling. The next manifest scrape cycle from
   gateways will pick up your orchestrator.

## Verifying end-to-end

```sh
# Manifest URL serves your signed manifest
curl -s https://coordinator.<your-domain>/.well-known/livepeer-registry.json | jq '.brokers'

# Each broker process is up
curl -sf https://broker-a.<your-domain>/healthz
curl -sf https://broker-b.<your-domain>/healthz

# Each broker advertises the expected capabilities
curl -s https://broker-a.<your-domain>/registry/offerings | jq .

# Each (capability, offering) on each broker is `ready` — anything reporting
# `unreachable` or `stale` will be skipped by gateways
curl -s https://broker-a.<your-domain>/registry/health | jq .
```

Once gateways start sending paid work, the `payment-daemon-receiver` on
each broker box will redeem tickets — keep an eye on its hot-wallet ETH
balance.

## Where to go next

- **Component deep-dives.** Each step above links to its component's
  README in this folder. Those are the source-of-truth operator docs.
- **Centralized observability.** All ingress READMEs include a brief
  section on exposing metrics for a single fleet-wide Prometheus. Skip
  it until you have more than a couple of hosts.
- **Gateway side.** When the gateway-onboarding guide lands in
  `../gateway-onboarding/`, it will walk the matching setup from the
  consumer side.

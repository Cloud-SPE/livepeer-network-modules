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
   capability-broker  ─►  workloads
   payment-daemon         (vLLM / ABR / etc., declared in host-config.yaml)
```

## What runs here

| Service                   | Purpose                                                    |
| ------------------------- | ---------------------------------------------------------- |
| `capability-broker`       | Advertises capabilities, routes inbound work to workloads  |
| `payment-daemon` receiver | Redeems Livepeer payment tickets on-chain for this broker  |

What runs **alongside** this stack (declared in `host-config.yaml`):

- The actual capability workloads — vLLM, ABR, vtuber pipeline, etc. They
  can be containers on this same box (declare them in your own compose)
  or remote services this broker proxies to.

## Listeners

| Port | Visibility | Purpose                                                                      |
| ---- | ---------- | ---------------------------------------------------------------------------- |
| 8080 | **Public** | Broker API (`/registry/offerings`, `/registry/health`, `/healthz`, paid traffic) |
| 9090 | Private    | Prometheus metrics                                                           |

Production must terminate TLS in front of 8080. A reverse-proxy (Traefik)
reference is documented separately.

### Health endpoints on :8080

| Path                | Returns                                                            |
| ------------------- | ------------------------------------------------------------------ |
| `GET /healthz`      | Broker process liveness (200 once the process is up)               |
| `GET /registry/health` | Per-`(capability, offering)` live-health snapshot — `ready` / `draining` / `degraded` / `unreachable` / `stale` |

`/registry/health` is what resolvers and gateways consult before routing
paid traffic. The values it reports come from the `health.probe` block on
each capability in `host-config.yaml` (see below). See
[`docs/design-docs/backend-health.md`](../../../docs/design-docs/backend-health.md)
for the full three-layer model (manifest / live / failure-rate).

## On-disk layout

```
/opt/livepeer/
├── payment-keystore.json          # hot wallet for ticket redemption
├── payment-keystore-password
└── host-config.yaml               # capability mix for this broker
```

### Keys on this box

The keystore here is the **hot wallet** that signs payment-ticket
redemption transactions. It is a **different** key from the cold orch
keystore on your Secure Orch host. The hot wallet must:

- hold enough ETH to pay redemption gas
- have redeem authority on behalf of `ORCH_ADDRESS` (the cold orch identity
  set on your Secure Orch host)

Do not copy your cold orch keystore to this box.

### host-config.yaml

Defines which capabilities this broker advertises and where each workload
lives. The shape varies by what your hardware can host — OpenAI/vLLM,
video ABR, vtuber, etc. Each broker box in your fleet has its own
host-config reflecting the hardware in that location.

Example host-configs live in [`host-configs/`](./host-configs/). Copy one
to `/opt/livepeer/host-config.yaml` (or wherever `BROKER_CONFIG` points)
and edit:

| Variant                                              | Status  | Capability                                                  | Pair with runner compose                                  |
| ---------------------------------------------------- | ------- | ----------------------------------------------------------- | --------------------------------------------------------- |
| [`openai-audio.example.yaml`](./host-configs/openai-audio.example.yaml) | Stable  | `openai:audio-transcriptions` (Whisper) + `openai:audio-speech` (Kokoro) | `openai-runners/compose/docker-compose.audio.yml`         |
| [`openai-chat.example.yaml`](./host-configs/openai-chat.example.yaml)   | Stable  | `openai:chat-completions` (vLLM, paired stream + reqresp)                 | `openai-runners/compose/docker-compose.vllm.chat.yml`     |
| [`preview/video-transcode.example.yaml`](./host-configs/preview/video-transcode.example.yaml) | **Preview** | `video:transcode.vod` (NVIDIA transcode runner, per-job billing)         | _gateway not yet published_                               |

The runner containers must be reachable from the broker via the
`backend.url` host names in the host-config. Either run them in the same
compose project (so they share the default network) or attach both stacks
to the same Docker network.

### Preview variants

Files under [`host-configs/preview/`](./host-configs/preview/) are
**documented for the onboarding guide but not yet for production use**.
The gateway integration and protocol version they depend on have not
shipped. Do not advertise these capabilities on the live network until the
matching gateway release is published.

### Notes on the example host-configs

- **Four work-unit extractors are demonstrated across the variants.**
  Whisper reports duration via a response header (`response-header`);
  Kokoro counts input characters from a private request field
  (`request-formula` with a JSONPath expression); vLLM chat reads
  `total_tokens` from the OpenAI `usage` block (`openai-usage`); the
  preview transcode variant uses `request-formula` with a literal `1` for
  per-job billing. Use whichever pattern your runner can support.
- **Each capability declares a `health.probe`.** The probe runs on
  cadence and feeds the broker's `/registry/health` surface. If a probe
  fails enough times in a row the offering is reported `unreachable` and
  gateways skip routing here until it recovers — the signed manifest is
  not touched. See "Health probes" below.
- **Same model, two interaction modes.** The chat host-config advertises
  the same Qwen model under both `http-stream@v0` and `http-reqresp@v0`
  with different `offering_id`s. Gateways pick whichever fits their
  integration.
- **`constraints` is operator-supplied metadata.** Gateways may use it
  to route requests to brokers with the hardware they expect (e.g.
  `gpu: "4090"`, `gpu_model: "1080"`, `gpu_vendor: "NVIDIA"`).

### Health probes

Every capability gets a `health.probe` block. The broker runs it on
cadence and exposes the result on `GET /registry/health` so resolvers
and gateways can skip an offering whose backend has gone dark — without
forcing a fresh sign cycle on the manifest.

| Probe `type`                | Use when                                                                                  |
| --------------------------- | ----------------------------------------------------------------------------------------- |
| `http-status`               | The backend exposes a plain `/healthz` (or similar) that returns 2xx when ready.          |
| `http-jsonpath`             | The backend's health endpoint returns JSON and you need to assert a specific field value. |
| `http-openai-model-ready`   | OpenAI-compatible backend (vLLM, OpenAI SaaS) — probe `/v1/models` and assert the model is listed. |
| `tcp-connect`               | Non-HTTP backends — broker just opens a TCP socket.                                       |
| `command-exit-0`            | Side-channel checks that need a shell command (e.g. inspect a local file).                |
| `manual-drain`              | Operator-driven; `status: draining` until the operator clears it.                         |

Shared knobs (defaults shown):

```yaml
health:
  probe:
    type: http-status          # required
    interval_ms: 5000          # cadence between probes
    timeout_ms: 1500           # single probe timeout
    unhealthy_after: 2         # consecutive failures → unreachable
    healthy_after: 1           # consecutive successes → ready
    config:
      url: http://runner:8080/healthz   # type-specific config
```

If you omit `health.probe` entirely on an `http`-transport capability,
the broker falls back to `http-status` against `backend.url`. That URL
is almost always a `POST`-only endpoint, so the GET probe gets 405
(Method Not Allowed) → reported as `degraded`, and the resolver only
admits offerings whose status is `ready`. Always point the probe at a
real health surface.

Operator surfaces for the three health layers:

| Layer            | Operator action                                  | Where             |
| ---------------- | ------------------------------------------------ | ----------------- |
| 1 (manifest)     | Edit `host-config.yaml`, run sign cycle          | secure-orch-console |
| 2 (live)         | Restart broker, mark drain, fix backend          | broker `/registry/health` + container orchestration |
| 3 (failure-rate) | Inspect dashboards, declare incident             | metrics / alerting stack |

See [`docs/design-docs/backend-health.md`](../../../docs/design-docs/backend-health.md)
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
- `PAYMENT_KEYSTORE` / `PAYMENT_KEYSTORE_PASSWORD_FILE` — only if your hot
  wallet keystore lives somewhere other than `/opt/livepeer/`
- `BROKER_CONFIG` — only if `host-config.yaml` lives somewhere other than
  `/opt/livepeer/`

## Verify

```sh
# Broker process is up
curl -sf http://127.0.0.1:8080/healthz

# Broker capability advertisement (shape depends on host-config.yaml)
curl -s http://127.0.0.1:8080/registry/offerings | jq .

# Per-(capability, offering) live health — every entry should reach `ready`
# once probes settle. `unreachable` here means the backend is not answering
# the probe URL you configured in host-config.yaml.
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

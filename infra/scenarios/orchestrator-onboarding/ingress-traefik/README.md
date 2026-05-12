# Traefik ingress

TLS-terminating reverse proxy for the public-facing orchestrator
components. Run **one Traefik instance per public-facing box** in your
fleet. Each Traefik instance fronts the Livepeer services that live on
the same box; downstream services don't publish any public ports.

## Per-host topology

For a typical orchestrator deployment:

| Host                       | Runs                                            | Public hostname            | Notes                                              |
| -------------------------- | ----------------------------------------------- | -------------------------- | -------------------------------------------------- |
| `secure-orch-host`         | Secure Orch                                     | _none_                     | No Traefik. No inbound internet. Outbound RPC only.|
| `coordinator-host`         | `ingress-traefik` + `orch-coordinator`          | `coordinator.example.com`  | Serves `.well-known/livepeer-registry.json` to gateways.|
| `broker-host-1`            | `ingress-traefik` + `capability-broker` (+ workload runners) | `broker-a.example.com` | One broker per data center / rig.                  |
| `broker-host-2`            | `ingress-traefik` + `capability-broker` (+ workload runners) | `broker-b.example.com` | Add as many as you have data centers.              |
| `broker-host-N`            | …                                               | …                          | Same pattern repeats per location.                 |

On every box that runs Traefik:

- The Traefik stack is identical (this scenario). Each Traefik gets its
  own certs and its own `acme.json`.
- Each host's DNS A/AAAA record must point at that host's public IP.
- The Cloudflare DNS API token (or HTTP-01 alternative) must be able to
  issue certs for whichever hostname is configured on that box.

## When to use Traefik

- Domains hosted on Cloudflare DNS (or another DNS provider lego supports)
  and you want automatic cert issuance via the DNS-01 challenge.
- Docker-native label-based routing — downstream services declare their
  hostnames and routes via labels, no separate config file.
- Each cloud host runs its own Traefik. (For LAN nodes behind NAT,
  Cloudflare Tunnel is documented separately.)

## Cert resolvers

Two ACME resolvers are configured in `traefik.example.yml`:

| Resolver       | Challenge | When to use                                                                              |
| -------------- | --------- | ---------------------------------------------------------------------------------------- |
| `cloudflare`   | DNS-01    | **Default.** Domains on Cloudflare. Works behind NAT / without inbound :80.              |
| `letsencrypt`  | HTTP-01   | Domains not on Cloudflare. Requires inbound :80 reachable from Let's Encrypt servers.    |

The example file ships with `cloudflare` active and `letsencrypt`
commented. To switch, uncomment `letsencrypt:` in `traefik.yml` and change
your downstream routers' `tls.certresolver=cloudflare` labels to
`tls.certresolver=letsencrypt`.

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
sudo cp infra/scenarios/orchestrator-onboarding/ingress-traefik/traefik.example.yml \
        /opt/livepeer/traefik/traefik.yml
sudo $EDITOR /opt/livepeer/traefik/traefik.yml   # set ACME email

# 3. Configure compose env
cp infra/scenarios/orchestrator-onboarding/ingress-traefik/.env.example \
   infra/scenarios/orchestrator-onboarding/ingress-traefik/.env
$EDITOR infra/scenarios/orchestrator-onboarding/ingress-traefik/.env     # set CF_DNS_API_TOKEN

# 4. Bring it up
docker compose \
  -f infra/scenarios/orchestrator-onboarding/ingress-traefik/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/ingress-traefik/.env \
  up -d
```

## Required values

In `.env`:

- **`CF_DNS_API_TOKEN`** — scoped Cloudflare API token with `Zone:DNS:Edit`
  on the zone(s) hosting your orchestrator domains. Create one at
  `https://dash.cloudflare.com/profile/api-tokens`. Prefer this over the
  legacy `CF_API_KEY` (global key, easy to over-scope).

In `/opt/livepeer/traefik/traefik.yml`:

- **`certificatesResolvers.cloudflare.acme.email`** — the email Let's
  Encrypt sends expiry notices to. Required.

## Per-node setup

The orch-coordinator and capability-broker scenarios each ship a Traefik
**overlay** (`docker-compose.traefik.yml`) alongside their base compose
file. The overlay drops the publicly-published port on the downstream
service, attaches it to the external `ingress` network, and declares the
Traefik router labels.

Result: the Livepeer service binds no public host port. Inbound HTTPS
traffic flows Traefik → `ingress` network → service.

The bring-up pattern is the same on every box:

1. Install Traefik (this scenario) — sets up TLS + ingress network.
2. Bring up the downstream service on the same box with both its base
   compose and the Traefik overlay layered on top.

### On the Secure Orch host

Skip Traefik. The Secure Orch host has no inbound internet and serves no
public endpoints. Just bring up the Secure Orch scenario directly:

```sh
docker compose \
  -f infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/.env \
  up -d
```

### On the Orch Coordinator host

```sh
# 1. Traefik (this scenario)
docker network create ingress
sudo cp infra/scenarios/orchestrator-onboarding/ingress-traefik/traefik.example.yml \
        /opt/livepeer/traefik/traefik.yml
sudo $EDITOR /opt/livepeer/traefik/traefik.yml   # set ACME email
cp infra/scenarios/orchestrator-onboarding/ingress-traefik/.env.example \
   infra/scenarios/orchestrator-onboarding/ingress-traefik/.env
$EDITOR infra/scenarios/orchestrator-onboarding/ingress-traefik/.env     # set CF_DNS_API_TOKEN
docker compose \
  -f infra/scenarios/orchestrator-onboarding/ingress-traefik/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/ingress-traefik/.env \
  up -d

# 2. Orch Coordinator with Traefik overlay
$EDITOR infra/scenarios/orchestrator-onboarding/orch-coordinator/.env    # set COORDINATOR_HOST
docker compose \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.traefik.yml \
  --env-file infra/scenarios/orchestrator-onboarding/orch-coordinator/.env \
  up -d
```

The overlay routes `https://${COORDINATOR_HOST}/.well-known/livepeer-registry.json`
to the container's `:8081`. Admin (`:8080`) and metrics (`:9091`) stay on
the box's loopback for local debugging.

### On each Capability Broker host

Repeat this on every broker box in your fleet. Each broker gets its own
public hostname matching the `base_url` you listed for it in your Orch
Coordinator's `coordinator-config.yaml`.

```sh
# 1. Traefik (this scenario) — same as above on each broker host
docker network create ingress
sudo cp infra/scenarios/orchestrator-onboarding/ingress-traefik/traefik.example.yml \
        /opt/livepeer/traefik/traefik.yml
sudo $EDITOR /opt/livepeer/traefik/traefik.yml
cp infra/scenarios/orchestrator-onboarding/ingress-traefik/.env.example \
   infra/scenarios/orchestrator-onboarding/ingress-traefik/.env
$EDITOR infra/scenarios/orchestrator-onboarding/ingress-traefik/.env
docker compose \
  -f infra/scenarios/orchestrator-onboarding/ingress-traefik/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/ingress-traefik/.env \
  up -d

# 2. Capability Broker with Traefik overlay
$EDITOR infra/scenarios/orchestrator-onboarding/capability-broker/.env   # set BROKER_HOST per box
docker compose \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.traefik.yml \
  --env-file infra/scenarios/orchestrator-onboarding/capability-broker/.env \
  up -d
```

The overlay routes `https://${BROKER_HOST}/` to the broker's `:8080`.
Metrics (`:9090`) stays on the box's loopback.

### Cert resolver selection

Both overlays default to `TRAEFIK_CERTRESOLVER=cloudflare`. To use
Let's Encrypt HTTP-01 instead, uncomment the `letsencrypt:` block in
`traefik.yml` **and** set `TRAEFIK_CERTRESOLVER=letsencrypt` in each
downstream scenario's `.env`. The choice is per-host — different boxes
in your fleet can use different resolvers.

## Dashboard, metrics (advanced)

The dashboard is enabled but not exposed (`api.insecure: false`). Most
orchestrators don't need it. If you want to reach it:

- Don't publish :8080 directly to the internet.
- Add Traefik labels on the `traefik` service itself that route a host
  like `traefik.example.com` to `api@internal` and `prometheus@internal`,
  protected by `basicauth` or `forwardauth` middleware.

Centralized Prometheus scraping (one Prom polling every host's
`/traefik/metrics`) is the same pattern — expose via labels with auth, not
direct port publishing. Skip this if you're not running multi-host
Prometheus.

## Verify

```sh
# Cert issued?
docker exec traefik cat /ssl-certs/acme-cloudflare.json | jq '.cloudflare.Certificates | length'

# Public registry endpoint reachable over TLS?
curl -sI https://coordinator.example.com/.well-known/livepeer-registry.json

# Broker reachable over TLS?
curl -sI https://broker-a.example.com/
```

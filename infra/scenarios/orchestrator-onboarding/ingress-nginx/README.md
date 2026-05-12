# Nginx ingress

TLS-terminating reverse proxy using
[`nginxproxy/nginx-proxy`](https://github.com/nginx-proxy/nginx-proxy) +
[`nginxproxy/acme-companion`](https://github.com/nginx-proxy/acme-companion).
Automatic Let's Encrypt cert issuance via either the **HTTP-01** challenge
(default) or the **Cloudflare DNS-01** challenge.

Requires inbound **:443** reachable on the host. HTTP-01 additionally
requires inbound **:80**. Use this on cloud hosts that can accept inbound
traffic on low ports. For NAT'd / LAN hosts, use
[`ingress-cloudflared`](../ingress-cloudflared/) instead.

Mix is fine — some hosts in your fleet can use Nginx, others Traefik,
others Cloudflare Tunnel. All three ingress scenarios share the same
external `ingress` network name, so the downstream overlays don't care
which ingress is in front.

## Which cert challenge?

You'll pick one per downstream service via which overlay you layer on top.
Pick the same one across services on the same host for sanity.

| Pick this  | When                                                                  | Downstream overlay                          |
| ---------- | --------------------------------------------------------------------- | ------------------------------------------- |
| **HTTP-01**| You can open inbound :80. Simplest setup, no API tokens.              | `docker-compose.nginx.yml`                  |
| **DNS-01** | Your domain is on Cloudflare and you'd rather not open :80, want wildcard certs, or hit issuance friction with HTTP-01. | `docker-compose.nginx-dns01.yml` |

The Nginx stack itself is the **same in both cases** — you don't change
`infra/scenarios/orchestrator-onboarding/ingress-nginx/docker-compose.yml`. Only the downstream
overlay file differs.

## Per-host topology

| Host                  | Runs                                              | Public hostname            | Ingress option |
| --------------------- | ------------------------------------------------- | -------------------------- | -------------- |
| `secure-orch-host`    | Secure Orch                                       | _none_                     | None (no inbound internet) |
| `coordinator-host`    | `ingress-nginx` + `orch-coordinator`              | `coordinator.example.com`  | Inbound :80/:443 required  |
| `broker-host-1`       | `ingress-nginx` + `capability-broker` (+ workload runners) | `broker-a.example.com`     | Inbound :80/:443 required  |
| `broker-host-N`       | …                                                 | …                          | …              |

Each host runs an independent nginx-proxy + acme-companion pair. Each
gets its own certs and its own acme-companion store.

## Configuration model: env labels via Docker socket

`nginx-proxy` and `acme-companion` watch the Docker socket. Downstream
containers register themselves by setting env vars in their compose:

| Env var             | Purpose                                                  |
| ------------------- | -------------------------------------------------------- |
| `VIRTUAL_HOST`      | Public hostname (the cert FQDN)                          |
| `VIRTUAL_PORT`      | Internal container port to route to                      |
| `VIRTUAL_PATH`      | Optional. Restricts routing to one path on `VIRTUAL_HOST`|
| `LETSENCRYPT_HOST`  | Hostname for cert issuance (almost always same as VIRTUAL_HOST) |

No config files to hand-edit on the box. The downstream overlays ship
these vars pre-wired.

## Bring-up

```sh
# 1. One-time: create the shared external network
docker network create ingress

# 2. Configure compose env
cp infra/scenarios/orchestrator-onboarding/ingress-nginx/.env.example \
   infra/scenarios/orchestrator-onboarding/ingress-nginx/.env
$EDITOR infra/scenarios/orchestrator-onboarding/ingress-nginx/.env       # set ACME_EMAIL

# 3. Bring it up
docker compose \
  -f infra/scenarios/orchestrator-onboarding/ingress-nginx/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/ingress-nginx/.env \
  up -d
```

Within seconds nginx-proxy + acme-companion are running. They'll issue
certs once downstream services register a hostname.

## Per-node setup

The orch-coordinator and capability-broker scenarios each ship an Nginx
**overlay** (`docker-compose.nginx.yml`) alongside their base compose
file. The overlay drops the publicly-published port, attaches to the
external `ingress` network, and sets the nginx-proxy env vars.

Result: the Livepeer service binds no public host port. Inbound HTTPS
traffic flows nginx-proxy → `ingress` network → service.

The downstream overlays reuse `COORDINATOR_HOST` and `BROKER_HOST` from
the same `.env` files used by the Traefik overlay — swapping ingress
flavors on a host doesn't require .env changes.

### On the Secure Orch host

Skip Nginx. The Secure Orch host has no inbound internet and serves no
public endpoints.

### On the Orch Coordinator host

**1. Bring up Nginx (this scenario).**

**2. Bring up the Orch Coordinator with one of the two Nginx overlays:**

**HTTP-01 (default):**

```sh
$EDITOR infra/scenarios/orchestrator-onboarding/orch-coordinator/.env   # set COORDINATOR_HOST
docker compose \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.nginx.yml \
  --env-file infra/scenarios/orchestrator-onboarding/orch-coordinator/.env \
  up -d
```

**Cloudflare DNS-01:**

```sh
$EDITOR infra/scenarios/orchestrator-onboarding/orch-coordinator/.env   # set COORDINATOR_HOST,
                                                # CF_DNS_API_TOKEN,
                                                # CF_ACCOUNT_ID, CF_ZONE_ID
docker compose \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.nginx-dns01.yml \
  --env-file infra/scenarios/orchestrator-onboarding/orch-coordinator/.env \
  up -d
```

Either overlay sets:

```
VIRTUAL_HOST=${COORDINATOR_HOST}
VIRTUAL_PORT=8081
VIRTUAL_PATH=/.well-known/livepeer-registry.json
LETSENCRYPT_HOST=${COORDINATOR_HOST}
```

so nginx-proxy routes **only** `https://${COORDINATOR_HOST}/.well-known/livepeer-registry.json`
to the container's :8081. Any other path on the hostname returns 404.
Admin (:8080) and metrics (:9091) stay loopback on the host.

DNS:

- **HTTP-01**: point an A/AAAA record for `${COORDINATOR_HOST}` at this
  host's public IP before bring-up — acme-companion needs the hostname
  to resolve and :80 to be reachable.
- **DNS-01**: the A/AAAA record is still required for inbound HTTPS
  traffic. acme-companion uses your Cloudflare API token to create a
  short-lived TXT record in your zone to prove control; no inbound :80
  needed.

### On each Capability Broker host

Repeat on every broker box. Each broker gets its own public hostname
matching the `base_url` you listed for it in your Orch Coordinator's
`coordinator-config.yaml`.

**1. Bring up Nginx (this scenario)** on this broker host.

**2. Bring up the Capability Broker with one of the two Nginx overlays:**

**HTTP-01 (default):**

```sh
$EDITOR infra/scenarios/orchestrator-onboarding/capability-broker/.env   # set BROKER_HOST per box
docker compose \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.nginx.yml \
  --env-file infra/scenarios/orchestrator-onboarding/capability-broker/.env \
  up -d
```

**Cloudflare DNS-01:**

```sh
$EDITOR infra/scenarios/orchestrator-onboarding/capability-broker/.env   # set BROKER_HOST,
                                                 # CF_DNS_API_TOKEN,
                                                 # CF_ACCOUNT_ID, CF_ZONE_ID
docker compose \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.nginx-dns01.yml \
  --env-file infra/scenarios/orchestrator-onboarding/capability-broker/.env \
  up -d
```

Either overlay sets:

```
VIRTUAL_HOST=${BROKER_HOST}
VIRTUAL_PORT=8080
LETSENCRYPT_HOST=${BROKER_HOST}
```

so nginx-proxy routes `https://${BROKER_HOST}/` (all paths) to the
broker's :8080. Metrics (:9090) stays loopback on the host.

DNS: point an A/AAAA record for `${BROKER_HOST}` at this host's public
IP before bring-up (true for both challenge types).

## Verify

```sh
# nginx-proxy generating configs for your downstream services?
docker exec nginx-proxy cat /etc/nginx/conf.d/default.conf | grep server_name

# acme-companion issued certs?
docker exec nginx-proxy ls /etc/nginx/certs/

# Public endpoint reachable over TLS?
curl -sI https://coordinator.example.com/.well-known/livepeer-registry.json
curl -sI https://broker-a.example.com/
```

If cert issuance hangs: confirm the hostname's DNS A/AAAA record points
at this host and inbound :80 is reachable from the public internet (Let's
Encrypt connects from outside, not Cloudflare).

## Trade-offs vs Traefik

- **Simpler.** Fewer config files. Auto Let's Encrypt out of the box.
- **Both HTTP-01 and DNS-01 supported** via two separate downstream
  overlay variants. Pick per service.
- **No dashboard.** Use `docker logs nginx-proxy` and direct file
  inspection for observability.
- **No fancy routing.** Per-host or per-host+path is what you get. For
  more (header rewrites, middlewares, OIDC), reach for Traefik.

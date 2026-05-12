# Cloudflare Tunnel ingress

Tunnel-mode reverse proxy that connects outbound from your host to
Cloudflare's edge. **No inbound :80 / :443** needed on the host — ideal for:

- LAN nodes behind NAT (home rigs, office workstations)
- Residential ISPs that block low ports
- Cloud hosts where you'd rather not expose ports directly
- Any setup where you want Cloudflare Zero Trust auth / WAF / DDoS in
  front of your endpoints

Run **one cloudflared instance per public-facing box** in your fleet,
the same way you'd run one Traefik instance per box. Mix is fine — some
hosts can use Traefik, others can use Cloudflare Tunnel.

## Per-host topology

| Host                  | Runs                                              | Public hostname            | Tunnel needed? |
| --------------------- | ------------------------------------------------- | -------------------------- | -------------- |
| `secure-orch-host`    | Secure Orch                                       | _none_                     | No             |
| `coordinator-host`    | `ingress-cloudflared` + `orch-coordinator`        | `coordinator.example.com`  | Yes            |
| `broker-host-1`       | `ingress-cloudflared` + `capability-broker` (+ workload runners) | `broker-a.example.com`     | Yes            |
| `broker-host-N`       | …                                                 | …                          | Yes            |

Each box runs an independent cloudflared with its own tunnel token. Hostname
mappings live in the Cloudflare Zero Trust dashboard, not in any file on
the box.

## Configuration model: Zero Trust UI (token mode)

This scenario uses **token mode** — cloudflared authenticates with a
single connector token and pulls its public-hostname mappings from the
Zero Trust dashboard at runtime. You don't manage a `config.yml` on disk.

To set up a tunnel:

1. Sign in to [Cloudflare Zero Trust](https://one.dash.cloudflare.com)
2. **Networks → Tunnels → Create a tunnel** (type: Cloudflared)
3. Name it after the host (e.g. `coordinator-host`, `broker-host-1`)
4. Choose **Docker** when offered an install method and copy the token
   (the long string after `--token`)
5. **Public Hostname** tab — add one entry per service you want to expose
   on this host (see node-specific tables below)

## Bring-up

```sh
# 1. One-time: create the shared external network
docker network create ingress

# 2. Configure compose env
cp infra/scenarios/orchestrator-onboarding/ingress-cloudflared/.env.example \
   infra/scenarios/orchestrator-onboarding/ingress-cloudflared/.env
$EDITOR infra/scenarios/orchestrator-onboarding/ingress-cloudflared/.env   # paste TUNNEL_TOKEN

# 3. Bring it up
docker compose \
  -f infra/scenarios/orchestrator-onboarding/ingress-cloudflared/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/ingress-cloudflared/.env \
  up -d
```

Within seconds the connector will register with Cloudflare. Verify in
the Zero Trust dashboard: the tunnel should show **HEALTHY**.

## Per-node setup

The orch-coordinator and capability-broker scenarios each ship a
cloudflared **overlay** (`docker-compose.cloudflared.yml`) alongside
their base compose file. The overlay drops the publicly-published port
and attaches the service to the external `ingress` network. cloudflared
reaches the container by its Docker DNS name on that network — that's
the `http://<container-name>:<port>` URL you put in the Zero Trust UI.

### On the Secure Orch host

Skip cloudflared. The Secure Orch host has no inbound internet and
serves no public endpoints.

### On the Orch Coordinator host

**1. Bring up cloudflared (this scenario)**, then:

**2. Bring up the Orch Coordinator with the cloudflared overlay:**

```sh
docker compose \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.cloudflared.yml \
  --env-file infra/scenarios/orchestrator-onboarding/orch-coordinator/.env \
  up -d
```

**3. Add the public hostname mapping in Zero Trust:**

| Field            | Value                                              |
| ---------------- | -------------------------------------------------- |
| Subdomain        | `coordinator`                                      |
| Domain           | _your domain on Cloudflare_                        |
| Path             | `/.well-known/livepeer-registry.json` _(optional, restricts tunnel to this endpoint)_ |
| Service Type     | `HTTP`                                             |
| Service URL      | `orch-coordinator:8081`                            |

The service host (`orch-coordinator`) is the container name from the
orch-coordinator compose file. Port `8081` is the public-listen port.

The full URL `https://coordinator.<your-domain>/.well-known/livepeer-registry.json`
is what you publish on the AI Service Registry contract.

### On each Capability Broker host

Repeat per broker box. **Each broker gets its own tunnel** (with its own
`TUNNEL_TOKEN`), or one tunnel per host with multiple Public Hostname
entries — both work; per-host tunnels are simpler to reason about.

**1. Bring up cloudflared on this broker host** with that host's
   `TUNNEL_TOKEN`.

**2. Bring up the Capability Broker with the cloudflared overlay:**

```sh
docker compose \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.yml \
  -f infra/scenarios/orchestrator-onboarding/capability-broker/docker-compose.cloudflared.yml \
  --env-file infra/scenarios/orchestrator-onboarding/capability-broker/.env \
  up -d
```

**3. Add the public hostname mapping in Zero Trust:**

| Field            | Value                                                                 |
| ---------------- | --------------------------------------------------------------------- |
| Subdomain        | _per-broker, e.g. `broker-a`, `local-rig-1`, `eu-central`_            |
| Domain           | _your domain on Cloudflare_                                           |
| Path             | _(leave empty — broker handles its own routing)_                      |
| Service Type     | `HTTP`                                                                |
| Service URL      | `capability-broker:8080`                                              |

The resulting `https://broker-a.<your-domain>/` URL **must match** the
`base_url` you listed for this broker in your Orch Coordinator's
`coordinator-config.yaml`.

## Centralized Prometheus (advanced)

Skip this section if you only have a few hosts and don't run a centralized
Prom — most orchestrators don't need it.

If you run one Prometheus that scrapes metrics across the fleet, the
common pattern is to add a second external network (`metrics`) and let
cloudflared bridge it the same way it bridges `ingress`:

```sh
docker network create metrics
```

Then uncomment the `metrics` references in `docker-compose.yml` (both
the `networks:` list under the service and the network declaration at
the bottom). Add a Zero Trust public hostname mapping per metrics
endpoint you want to expose to the scraper — protect them with a Zero
Trust Access policy so only your Prom service can reach them.

## Verify

```sh
# Connector running and registered?
docker logs cloudflared 2>&1 | grep -i registered

# Public endpoint reachable through the tunnel?
curl -sI https://coordinator.<your-domain>/.well-known/livepeer-registry.json
curl -sI https://broker-a.<your-domain>/
```

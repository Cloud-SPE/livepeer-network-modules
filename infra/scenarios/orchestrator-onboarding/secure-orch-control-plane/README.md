# Secure Orch

The cold-key host for an Orchestrator. This box holds the ETH keystore and
keystore password — everything else in the network reaches it through the
signing API, never through the chain.

## Threat model

- **No inbound internet.** Firewall this host so nothing from the public
  internet can reach it. It only needs outbound network access to your
  Arbitrum RPC endpoints.
- **The cold key never leaves the box.** `secure-orch-console` signs
  candidates locally; the signed result is what gets published from a
  separate, public-facing `orch-coordinator` host.
- **Audit log is append-only.** Rotate it off-box on your own cadence.

## What runs here

| Service                   | Purpose                                              |
| ------------------------- | ---------------------------------------------------- |
| `protocol-daemon`         | Round-init, reward, on-chain URI writes              |
| `service-registry-daemon` | Resolves active orchestrators from chain (resolver)  |
| `secure-orch-console`     | Signs manifest candidates with the cold key          |

`orch-coordinator` is **not** part of this stack — it runs on a separate
public-facing host and consumes the signed candidates this console produces.

## On-disk layout

Convention assumed by the defaults:

```
/opt/livepeer/
├── keystore.json
└── keystore-password
```

The keystore must hold the private key for `ORCH_ADDRESS`. Both
`protocol-daemon` and `secure-orch-console` mount the same two files
read-only, from the one `KEYSTORE_FILE` / `KEYSTORE_PASSWORD_FILE` pair.

## Bring-up

```sh
cp infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/.env.example \
   infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/.env
$EDITOR infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/.env

# Generate an admin token for the console
openssl rand -hex 32   # paste into SECURE_ORCH_ADMIN_TOKENS

docker compose \
  -f infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/docker-compose.yml \
  --env-file infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/.env \
  up -d
```

## Required values

You must set these in `.env` before bring-up:

- `CHAIN_RPC_URLS` — comma-separated Arbitrum RPC endpoints, primary
  first. `protocol-daemon` and `service-registry-daemon` both read this
  one list and fail over between entries. The public endpoints in the
  example are placeholders; put your own provider first.
- `ORCH_ADDRESS` — your orchestrator's on-chain address
- `SECURE_ORCH_ADMIN_TOKENS` — generated secret, used to authenticate the
  operator UI / CLI against the console

Everything else has a default baked into the compose file and is listed,
commented out, under "Overrides" in `.env.example`. The ones you are most
likely to touch:

- `KEYSTORE_FILE` / `KEYSTORE_PASSWORD_FILE` — only if your keystore lives
  somewhere other than `/opt/livepeer/`
- `SECURE_ORCH_LISTEN` — bind `127.0.0.1:8081` if this host is not fully
  isolated and reach the console over an SSH tunnel

## Optional: automated sign cycle (agent mode)

Layer `docker-compose.agent.yml` on top to replace the hand-carry sign
cycle with the plan 0042 agent: the console pulls candidates from the
coordinator, auto-signs inside your sign-policy envelope (phase 1:
content-identical renewals only), holds everything else for review on
the Manifests page, and pushes signed manifests back. Outbound-only —
the host's no-inbound posture is unchanged.

```sh
# One-time setup
openssl rand -hex 32 > /opt/livepeer/agent-token   # also mount on the coordinator
cp secure-orch-console/examples/sign-policy.json /opt/livepeer/sign-policy.json
$EDITOR infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/.env
# set COORDINATOR_URL, COORDINATOR_PUBLIC_URL, AGENT_TOKEN_FILE,
# SIGN_POLICY_FILE, and (recommended) ALERT_WEBHOOK_URL

docker compose   -f infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/docker-compose.yml   -f infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/docker-compose.agent.yml   --env-file infra/scenarios/orchestrator-onboarding/secure-orch-control-plane/.env   up -d
```

The coordinator host needs the matching overlay
(`infra/scenarios/orchestrator-onboarding/orch-coordinator/docker-compose.agent.yml`)
with the same token file. See
`secure-orch-console/docs/operator-runbook.md` § "Agent mode" for the
policy envelope, the two-week burn-in procedure, kill switches, and the
mandatory manifest-expiry alert.

Kill switch (pauses pull and sign, audited):

```sh
docker compose exec secure-orch-console touch /var/lib/secure-orch/agent.pause
```

## Verify

```sh
# protocol-daemon metrics
curl -s http://127.0.0.1:9094/metrics | head

# service-registry-daemon metrics
curl -s http://127.0.0.1:9095/metrics | head

# secure-orch-console health
curl -s http://127.0.0.1:8081/healthz

# agent liveness (agent overlay only): polls counters + expiry gauge
curl -s http://127.0.0.1:8081/metrics | grep secure_orch_agent
```

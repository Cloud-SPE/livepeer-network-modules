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
`protocol-daemon` and `secure-orch-console` mount these files read-only.

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

- `ORCH_ADDRESS` — your orchestrator's on-chain address
- `ETH_URLS` — one or more Arbitrum RPC endpoints
- `SECURE_ORCH_ADMIN_TOKENS` — generated secret, used to authenticate the
  operator UI / CLI against the console
- `PROTOCOL_KEYSTORE` / `PROTOCOL_KEYSTORE_PASSWORD_FILE` (and the matching
  `SECURE_ORCH_*` vars) — only if your keystore lives somewhere other than
  `/opt/livepeer/`

## Verify

```sh
# protocol-daemon metrics
curl -s http://127.0.0.1:9094/metrics | head

# service-registry-daemon metrics
curl -s http://127.0.0.1:9095/metrics | head

# secure-orch-console health
curl -s http://127.0.0.1:8081/healthz
```

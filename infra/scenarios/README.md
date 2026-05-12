# Scenario stacks

These are operator-facing reference deployments composed from the
module-local images and configs.

Use them for:

- showing how modules fit together in a real topology
- booting a staged environment without stitching modules together by hand
- documenting host roles, public/private listeners, and startup order

Do not treat these as replacements for module-local compose files.
Each deployable component still owns its minimal runnable bundle under
`<component>/compose/`.

## Current scenarios

- `secure-orch-control-plane/`
  - `orch-coordinator`
  - `protocol-daemon`
  - `service-registry-daemon`
  - `secure-orch-console`
- `single-worker-node/`
  - `capability-broker`
  - `payment-daemon` receiver
  - one vLLM-backed worker node
- `video-worker-node/`
  - `capability-broker`
  - `payment-daemon` receiver
  - one ABR runner
  - broker-owned RTMP ingest + LL-HLS egress
- `openai-gateway-manifest/`
  - `openai-gateway`
  - `service-registry-daemon`
  - `payment-daemon` sender
  - `postgres`
- `video-gateway/`
  - `video-gateway`
  - `payment-daemon` sender
  - `postgres`
  - `redis`
  - `rustfs`
- `vtuber-gateway/`
  - `vtuber-gateway`
  - `payment-daemon` sender
  - `postgres`
- `full-minimal-network/`
  - control plane
  - one worker node
  - one OpenAI gateway

Each scenario directory contains:

- `docker-compose.yml`
- `.env.example`
- any scenario-local config files
- a short `README.md`

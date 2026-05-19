# Archived scenarios

These are earlier multi-module deployment examples preserved for
reference. They predate the current onboarding flow and are **not**
maintained against it. Paths and references inside these scenarios may
not match what ships in the active onboarding stacks.

For current guidance, see
[`../orchestrator-onboarding/`](../orchestrator-onboarding/).

## Contents

- `single-worker-node/` — broker + payment-daemon + inline vLLM worker on
  one box. Superseded by `orchestrator-onboarding/capability-broker/`
  plus a separately-deployed runner.
- `video-worker-node/` — broker + payment-daemon + ABR runner with broker-
  owned RTMP/HLS.

Anything reusable from these stacks should be re-landed in the active
onboarding flow rather than pulled directly from here.

## Removed

The previously archived `openai-gateway-manifest/`, `video-gateway/`,
`video-gateway-manifest/`, and `full-minimal-network/` scenarios were
tied to the four product gateways (openai, video, vtuber, daydream)
that have been removed from this repo. See the root `README.md` for the
post-removal repo shape.
</content>
</invoke>
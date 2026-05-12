# Archived scenarios

These are earlier multi-module deployment examples preserved for
reference. They predate the current onboarding flow and are **not**
maintained against it. Paths and references inside these scenarios may
not match what ships in the active onboarding stacks.

For current guidance, see
[`../orchestrator-onboarding/`](../orchestrator-onboarding/) (and, when
it lands, `../gateway-onboarding/`).

## Contents

- `single-worker-node/` — broker + payment-daemon + inline vLLM worker on
  one box. Superseded by `orchestrator-onboarding/capability-broker/`
  plus a separately-deployed runner.
- `video-worker-node/` — broker + payment-daemon + ABR runner with broker-
  owned RTMP/HLS.
- `openai-gateway-manifest/` — early OpenAI gateway scenario with
  manifest-based discovery.
- `video-gateway/` — sender-side video gateway with Postgres + Redis +
  RustFS, static broker mode.
- `video-gateway-manifest/` — same in manifest/resolver mode.
- `full-minimal-network/` — control plane + worker + OpenAI gateway in a
  single all-in-one stack.

The vtuber-gateway scenario that used to live here has moved to
[`../gateway-onboarding/vtuber-gateway/`](../gateway-onboarding/vtuber-gateway/)
as a preview entry alongside OpenAI + Video.

Anything reusable from these stacks should be re-landed in the active
onboarding flow rather than pulled directly from here.

# AGENTS.md

This is `rerank-runner/` — a Python FastAPI workload binary that serves
the `/v1/rerank` capability (Cohere-compatible reranker) to the
capability broker.

Component-local agent map. The repo-root [`../AGENTS.md`](../AGENTS.md) is the
cross-cutting map; this file scopes to runner-specific guidance.

## Operating principles

Inherited from the repo root (agent-first harness pattern). Plus:

- **Runner is blind to customer identity.** No customer auth, no billing,
  no payment validation. The capability broker authenticates upstream and
  forwards a paid request; the runner only sees HTTP method + path +
  body + the informational `Livepeer-Capability` /
  `Livepeer-Offering` headers.
- **Capability identity is image-tag-pinned.** `CAPABILITY_NAME=rerank`
  per OQ1; offering details live in an embedded YAML manifest at
  `/etc/runner/offering.yaml`.
- **GPU probe fails fast.** Runner exits non-zero at startup if
  `DEVICE=cuda` and no GPU is detected (per OQ3); operators set
  `DEVICE=cpu` to fall back.
- **Metrics are opt-in.** `METRICS_ENABLED=true` exposes `/metrics`
  per OQ5; default-off, zero overhead.
- **Multi-arch policy.** ML runner ships amd64-only per OQ4.
- **Inherits the shared Python base.** The runner Dockerfile
  `FROM tztcloud/python-runner-base:<tag>` (built by sibling
  `../openai-runners/python-runner-base/`) per OQ2; adds only
  `sentence-transformers` + `transformers` + `torch`.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Architectural overview | [`DESIGN.md`](./DESIGN.md) |
| Build / run / smoke gestures | [`Makefile`](./Makefile) |
| Compose overlay | [`compose/`](./compose/) |
| Plan brief | [`../docs/exec-plans/completed/0013-runners-byoc-migration.md`](../docs/exec-plans/completed/0013-runners-byoc-migration.md) |

## Doing work in this component

- **All gestures are Docker-first** (per repo-root core belief #15).
- **Image tag is frozen at v0.8.10.** Do not bump without explicit user
  approval.
- **No per-runner LICENSE file.** Repo-root MIT applies.
- **Default model: `zeroentropy/zerank-2`.** A CrossEncoder; ~8GB.
  Use `model-downloader/` to pre-pull weights into a shared volume.

## What lives elsewhere

- `openai-runners/python-runner-base/` — the shared Python base image
  this runner inherits.
- `openai-runners/` — sibling component for OpenAI-shaped capabilities.
- `video-runners/` — sibling component for VOD transcode + ABR ladder.
- `capability-broker/` — the orch-side dispatcher that forwards requests
  to this runner (broker is the client; runner is the server).

# rerank-runner

Python FastAPI workload binary that serves a Cohere-compatible
`/v1/rerank` endpoint. Loads `zeroentropy/zerank-2` (CrossEncoder) at
startup, keeps it warm on GPU, scores docs against a query.

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

One Docker image; one process per broker-dispatched container.

| Endpoint | Purpose |
|---|---|
| `POST /v1/rerank` | Score + reorder docs against a query (Cohere-compatible) |
| `GET /healthz` | 200 ready, 503 during model load |
| `GET /rerank/options` | Scraped by orch-coordinator (plan 0018) |
| `GET /metrics` | Prometheus exposition (opt-in) |

Plus a sibling `model-downloader/` image that pre-pulls the model
weights into a shared volume.

## Status

**v0.1 scaffold.** Code lands per [`docs/exec-plans/active/0013-runners-byoc-migration.md`](../docs/exec-plans/active/0013-runners-byoc-migration.md).

## Build

Per repo-root core belief #15, every gesture is Docker-first.

```bash
make build              # build runner + downloader images
make smoke              # smoke against fixture query + docs
make help               # show all targets
```

## Configuration

| Env var | Required | Purpose |
|---|---|---|
| `CAPABILITY_NAME` | yes | Capability identity (canonical: `rerank`) |
| `DEVICE` | no (default `cuda`) | torch device; fail-fast on `cuda` + no GPU |
| `METRICS_ENABLED` | no (default `false`) | Expose `/metrics` Prometheus endpoint |
| `MODEL_ID` | no (default `zeroentropy/zerank-2`) | CrossEncoder model id |
| `MODEL_DIR` | no (default `/models`) | Local model cache |
| `RUNNER_PORT` | no (default `8080`) | HTTP bind |
| `MAX_QUEUE_SIZE` | no (default `5`) | 429 threshold |
| `DTYPE` | no (default `bfloat16`) | torch dtype |
| `MAX_BATCH_SIZE` | no (default `1000`) | Per-request doc cap |
| `INFERENCE_BATCH_SIZE` | no (default `64`) | Internal `model.predict()` batch |

Offering details (default scoring options, max batch size, rate-card
hints) live in `/etc/runner/offering.yaml` inside the image.

## License

MIT — repo-root [`../LICENSE`](../LICENSE) applies.

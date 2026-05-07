# vtuber-pipeline

Python SaaS pipeline for the Livepeer vtuber product. Ships three
console-script binaries from a single Python package:

- `pipeline-mock-youtube` — chat-source worker (mock; replaced by a
  real YouTube provider in production via config flag).
- `pipeline-streams` — streams orchestrator. Customer-facing API for
  starting/stopping vtuber broadcasts; opens a session on
  `vtuber-gateway/` per stream.
- `pipeline-egress` — RTMP push worker. Receives chunked-POST bodies,
  pipes through ffmpeg, pushes to a downstream RTMP URL.

The pipeline calls `vtuber-gateway/` as a single **meta-customer** (one
shared `LIVEPEER_VTUBER_GATEWAY_API_KEY` per deployment); pipeline-app
bills its own customers internally and does not propagate per-customer
keys to the gateway.

## Quick start

```sh
uv sync
uv run pytest -q
uv run pipeline-mock-youtube  # default port 8090
uv run pipeline-streams       # default port 8092
uv run pipeline-egress        # default port 8091
```

## Layout

See [`AGENTS.md`](./AGENTS.md). The migration brief is
[`docs/exec-plans/completed/0013-vtuber-suite-migration.md`](../docs/exec-plans/completed/0013-vtuber-suite-migration.md).

## License

MIT.

## Source attribution

Ported from
`livepeer-vtuber-project/pipeline-app/src/pipeline/` (see plan
0013-vtuber §5.2). Python package renamed `pipeline` → `vtuber_pipeline`;
streams provider `bridge.py` renamed to `gateway.py`; `Bridge*` symbols
renamed to `Gateway*`.

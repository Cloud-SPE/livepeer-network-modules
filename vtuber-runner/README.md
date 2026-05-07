# vtuber-runner

Python session-runtime workload binary + browser-side avatar renderer
for the Livepeer vtuber product. One runner = one host. Each session
spawns a headless Chromium child loading the avatar-renderer dist; the
Python service drives the renderer over a control-WS, encodes audio +
video (PyAV), mux'es them, and trickle-publishes upstream.

Work-units (per-second) are reported to the broker over the
`SessionRunnerControl.ReportWorkUnits` gRPC bidi-stream (see plan
0012-followup §8 + plan 0013-vtuber OQ5 lock).

## Quick start

```sh
# Python service + tests
uv sync
uv run pytest -q

# Avatar-renderer sub-workspace (browser TS)
cd avatar-renderer
pnpm install
pnpm test
pnpm build           # emits avatar-renderer/dist/

# Container build (3-stage: Vite renderer → uv deps → runtime)
docker build -t vtuber-runner:dev -f vtuber-runner/Dockerfile .
```

## Layout

See [`AGENTS.md`](./AGENTS.md). The migration brief is
[`docs/exec-plans/completed/0013-vtuber-suite-migration.md`](../docs/exec-plans/completed/0013-vtuber-suite-migration.md).

## License

MIT. The vendored upstream at `third_party/olv/` retains its own
license (see `third_party/olv/LICENSE`).

## Source attribution

Ported from `livepeer-vtuber-project/session-runner/` (Python service
+ tests) and `livepeer-vtuber-project/avatar-renderer/` (browser TS)
per plan 0013-vtuber §5.3. OLV upstream is **vendored** at
`third_party/olv/` per OQ2 lock (no git submodule); rebase procedure
in [`third_party/olv/UPSTREAM.md`](./third_party/olv/UPSTREAM.md).

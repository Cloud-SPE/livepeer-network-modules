# pool-member-agent

Host-side agent for connected runners. It attaches **outbound** to a
broker, declares what this host runs, and serves the work the broker
dispatches back down the same connection.

One bundle shape serves both deployments (plan 0043 decision 2): a pool
member and an orchestrator's own hardware ("a pool of one") run this
same binary with the same variables. The only difference is who minted
the attach credential — the pool controller, or the broker's own
`POST /admin/v1/enroll`.

The agent never opens a listener, never holds a price, and never decides
what is sold. Only outbound connectivity is required: no DNS entry, no
TLS certificate, no router forwarding.

## Configuration

| Variable | Meaning |
|---|---|
| `LIVEPEER_BROKER_URL` | Broker base URL; the WebSocket transport. |
| `LIVEPEER_BROKER_QUIC_ADDR` | Broker QUIC address. Preferred when set; the WebSocket is the egress-friendly fallback. |
| `LIVEPEER_ATTACH_CREDENTIAL_FILE` | File holding the attach credential from the bundle. (`LIVEPEER_ATTACH_CREDENTIAL` inline exists for throwaway runs.) |
| `LIVEPEER_HOST_ID` | Stable host id; defaults to the hostname. Must match the enrollment when the store records one. |
| `LIVEPEER_RUNNERS_FILE` | JSON array of runner declarations (below). |
| `LIVEPEER_RUNNER_*` | Single-runner shorthand: `_PROFILE`, `_URL`, `_MODEL`, `_CAPABILITY_ID`, `_LOCAL_ID`, `_PROVIDER`. |
| `LIVEPEER_REFRESH_EVERY` | How often to rebuild the document and re-send it if it changed. Default `1m`. |

## Declaring runners

An operator says where a container is, which profile it is, and what it
loaded. The **profile** supplies every fact only the runner knows —
endpoint path, transports, work unit, extractor, readiness recipe — so
none of it is ever hand-transcribed into broker config:

```json
[
  { "local_id": "chat",    "profile": "openai-compatible", "url": "http://vllm:8000",
    "model": "llama-3-70b", "provider": "vllm",
    "devices": ["GPU-8f3c…"], "extensions": { "x-quantization": "fp8" } },
  { "local_id": "whisper", "profile": "openai-compatible", "url": "http://whisper:9000",
    "capability_id": "openai:audio-transcriptions", "model": "whisper-large-v3" },
  { "local_id": "abr",     "profile": "transcode",        "url": "http://ffmpeg:8080" }
]
```

**Profiles**

- `openai-compatible` — `capability_id` selects the endpoint family:
  `openai:chat-completions` (default), `openai:embeddings`,
  `openai:audio-transcriptions`, `openai:audio-translations`,
  `openai:audio-speech`, `openai:images-generations`. Each carries its
  own path, transports, work unit, and extractor.
- `transcode` — `video:transcode.abr` over multipart, metered in
  `output_seconds` by the `ffmpeg-progress` extractor.

## What the agent puts on the wire

One attach document per connection
([`runner-attach.md`](../livepeer-network-protocol/protocols/runner-attach.md)),
sent as the first frame and re-sent whenever it changes — a GPU
appearing or failing is the common case. The broker answers with a
`register_result`; the agent logs every rejection with the field and
both sides, because that line is the operator's feedback loop:

```
CAPABILITY REJECTED whisper (openai:audio-transcriptions): extractor_unknown
  /capabilities/1/work_unit/extractor/type declared="whisper-seconds" expected="one of: …"
```

A rejected capability gets no work; the rest of the host keeps serving.

Dispatched requests carry `Livepeer-Runner-Local-Id`, and the agent
routes on it — never on the path, since one host can serve the same
capability id under two models. The header is stripped before the
request reaches the container.

The agent no longer reports hardware to `pool-controller`: GPU inventory
rides the attach document, so the broker (and through it the controller)
sees exactly what the runner declared, in one place.

## Build / test

```sh
make build
make check      # go test ./... plus the contract check below
```

`make check-attach-docs` validates `testdata/attach/*.json` — the real
documents this agent builds — against the protocol module's JSON Schema.
The broker's own test suite additionally feeds those same goldens
through its validator, so the two independent implementations are
checked against each other.

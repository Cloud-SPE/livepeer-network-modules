# Architecture overview

The eight-layer sketch. This is the **at-a-glance** view; deep dives go in their own
design-docs. Full provenance lives in
[`../references/2026-05-06-architecture-conversation.md`](../references/2026-05-06-architecture-conversation.md).

## Shape in one sentence

A single workload-agnostic process per orch host — the **capability broker** — that owns
`/registry/offerings`, dispatches paid requests over a small fixed typology of *interaction
modes* to arbitrary backends declared in YAML, with the trust spine preserved by an
operator-driven, cold-key-signed manifest publication cycle.

## Top-level component diagram

Four host archetypes (`secure-orch`, `orch-coordinator`, `worker-orch`, gateway)
plus the chain. Solid arrows are runtime data flow; dotted arrows are control /
configuration paths. Sub-diagrams later in this file zoom into specific flows.

```mermaid
flowchart TD
    subgraph chain["Arbitrum One"]
        direction LR
        SREG["ServiceRegistry<br/>(+ AIServiceRegistry)"]
        TB["TicketBroker"]
        BM["BondingManager"]
        RM["RoundsManager"]
    end

    subgraph secure_host["secure-orch host — FIREWALLED"]
        direction TB
        SOC["secure-orch-console<br/>(LAN-only, diff + sign)"]
        PRD["protocol-daemon<br/>(rounds + reward)"]
        COLD[("cold orch keystore<br/>NEVER LEAVES")]
    end

    subgraph coord_host["orch-coordinator host — public"]
        OC["orch-coordinator<br/>(scrapes brokers,<br/>hosts signed manifest)"]
    end

    subgraph worker_host["worker-orch host × N — public"]
        direction TB
        CB["Capability Broker<br/>(workload-agnostic,<br/>one per host)"]
        WPD["payment-daemon<br/>receiver"]
        subgraph backends["Backends declared in host-config.yaml"]
            direction LR
            VLLM["vLLM / TGI / etc.<br/>(local)"]
            OAIAPI["OpenAI API<br/>(SaaS)"]
            FFMPEG["FFmpeg<br/>(local)"]
            RUNNER["session-runner<br/>(LAN)"]
        end
    end

    subgraph gateway_host["gateway host"]
        direction TB
        GW["gateway shell<br/>(OpenAI / video / vtuber)"]
        GPD["payment-daemon<br/>sender"]
        SRD["service-registry-daemon<br/>resolver"]
        ADAPT["gateway-adapters<br/>(per interaction mode)"]
    end

    SOC -.-> PRD
    SOC -.-> COLD
    PRD --> RM
    PRD --> BM

    SOC -.->|"signed manifest<br/>(manual transport)"| OC
    OC -.->|"scrape /registry/offerings"| CB
    OC --> SREG

    SRD --> SREG
    SRD -.->|"GET /manifest.json<br/>+ verify sig"| OC

    GW --> ADAPT
    GW --> SRD
    GW --> GPD
    GPD --> TB

    ADAPT ==>|"paid HTTP / WS / RTMP /<br/>session-control + media"| CB
    CB --> WPD
    WPD --> TB

    CB ==> VLLM
    CB ==> OAIAPI
    CB ==> FFMPEG
    CB ==> RUNNER
```

The five logical layers, top to bottom:

- **Chain (Arbitrum One)** — `ServiceRegistry` / `AIServiceRegistry` point at
  the orch's signed manifest URL; `TicketBroker` settles payments;
  `BondingManager` + `RoundsManager` drive the round cadence.
- **Trust spine (secure-orch)** — the cold key never leaves. Operator-driven
  sign cycle produces signed manifests that the coordinator hosts.
- **Public orch surface (orch-coordinator)** — no keys, no daemon sockets.
  Scrapes brokers for offerings, builds candidate manifests, hosts the signed
  manifest at the on-chain `serviceURI`.
- **Worker hosts (capability broker + backends)** — one broker per host, fully
  workload-agnostic. Backends are arbitrary (local containers, LAN services,
  third-party APIs). Co-located `payment-daemon` (receiver) validates tickets.
- **Gateway** — resolver + sender + per-mode adapter. Talks to the broker over
  whichever interaction mode the resolved tuple declares.

## Layer 1 — Capability broker

**One process per host, workload-agnostic.** No per-capability Go code. Core jobs:

1. Read a single `host-config.yaml`.
2. Expose `GET /registry/offerings`, `GET /registry/health`, `GET /healthz`,
   `GET /metrics`, plus one canonical path per mode (e.g. `POST /v1/cap` for
   `http-reqresp` — see [`../../livepeer-network-protocol/modes/`](../../livepeer-network-protocol/modes/)).
3. Route inbound requests by **`Livepeer-Capability` header** → look up the
   **backend descriptor** → wrap in the declared **interaction mode** → forward →
   return the response.
4. Report `actualUnits` to co-located `payment-daemon` (receiver) over unix socket — same
   socket regardless of capability.
5. Execute broker-local health probes on cadence and publish normalized
   per-tuple snapshots on `GET /registry/health`.

**The broker contains zero routing semantics upstream of normalized health.**
Capability-specific readiness logic is allowed inside probe recipes, but it
must stop at the broker boundary and publish only the shared outward states
`ready`, `draining`, `degraded`, `unreachable`, and `stale`.

Replaces: `openai-worker-node`, `vtuber-worker-node`, `video-worker-node`.

### Request lifecycle inside the broker

A single `http-reqresp` request, from inbound TLS to settled payment. Streaming
modes (`http-stream`, `ws-realtime`, `session-control-plus-media`,
`rtmp-ingress-hls-egress`) follow the same shape but the "forward + collect
units" step is long-lived — see the streaming-pattern doc for the full picture.

```mermaid
sequenceDiagram
    autonumber
    participant GW as gateway adapter
    participant Broker as Capability Broker
    participant Cfg as host-config.yaml<br/>(loaded once)
    participant PD as payment-daemon<br/>(receiver, unix socket)
    participant Backend as backend<br/>(vLLM / OpenAI / FFmpeg / …)

    GW->>Broker: POST /v1/cap<br/>Livepeer-Capability: <id><br/>Livepeer-Offering: <id><br/>Livepeer-Payment: ticket<br/>Authorization: Bearer <session>?
    Broker->>Cfg: lookup (capability_id, offering_id)
    Cfg-->>Broker: { interaction_mode, work_unit, extractor,<br/>price, backend descriptor }
    Broker->>PD: ProcessPayment(payment_bytes, expected_max_units,<br/>price_per_unit, capability_id, offering_id)
    PD-->>Broker: ok (sender, credited_ev, balance)

    Broker->>Backend: forward (transport from descriptor)
    Backend-->>Broker: response payload

    Broker->>Broker: extractor → actualUnits<br/>(openai-usage / response-jsonpath /<br/>bytes-counted / seconds-elapsed / …)
    Broker->>PD: ReportUsage(work_id, actualUnits)
    PD-->>Broker: ok
    Broker-->>GW: response payload
```

**Key invariants:**

- The broker resolves `(capability_id, offering_id)` from the inbound headers
  before doing anything else — mismatched routing fails closed.
- Payment validation happens **before** the backend call; the only thing the
  broker knows about money is "did the daemon say yes."
- `actualUnits` is whatever the declared extractor returns; the broker doesn't
  know what a "token" or "pixel-second" is.

## Layer 2 — Interaction-mode typology

The fixed wire contracts. Capabilities pick one. Initial set:

| Mode | Wire shape | Examples |
|---|---|---|
| `http-reqresp` | one HTTP req → one HTTP resp | `openai:embeddings`, custom REST |
| `http-stream` | request → SSE / chunked stream | `openai:chat-completions` (stream) |
| `http-multipart` | multipart upload → response | `openai:audio-transcriptions` |
| `ws-realtime` | bidirectional WebSocket | `openai:realtime`, vtuber `/control` |
| `rtmp-ingress-hls-egress` | RTMP in → HLS manifest+segments out | `video:live.rtmp` |
| `session-control-plus-media` | HTTP session-open → broker-managed long-lived media/runtime plane | `livepeer:vtuber-session` |
| `session-control-external-media` | HTTP session-open → external long-lived media plane | `daydream:scope:v1` |

Each mode is implemented once in the broker, once in the gateway. **New capability
under an existing mode = zero code.** New mode = one adapter on each side.

**Modes are specifications, not libraries.** Living in the
`livepeer-network-protocol` spec repo (working name) — not a code dependency.

```mermaid
flowchart LR
    subgraph caps["Capabilities (declared in host-config.yaml)"]
        direction TB
        C1["openai:chat-completions"]
        C2["openai:embeddings"]
        C3["openai:audio-transcriptions"]
        C4["openai:realtime"]
        C5["video:live.rtmp"]
        C6["livepeer:vtuber-session"]
        C8["daydream:scope:v1"]
        C7["customer:custom-rest-api"]
    end

    subgraph modes["Interaction modes (one adapter on each side)"]
        direction TB
        M1["http-reqresp"]
        M2["http-stream"]
        M3["http-multipart"]
        M4["ws-realtime"]
        M5["rtmp-ingress-hls-egress"]
        M6["session-control-plus-media"]
        M7["session-control-external-media"]
    end

    subgraph adapters["One adapter per mode<br/>(broker side + gateway side)"]
        direction TB
        A1["reqresp adapter"]
        A2["stream adapter"]
        A3["multipart adapter"]
        A4["ws adapter"]
        A5["rtmp adapter"]
        A6["session adapter"]
        A7["external-media session adapter"]
    end

    C1 --> M2
    C2 --> M1
    C3 --> M3
    C4 --> M4
    C5 --> M5
    C6 --> M6
    C8 --> M7
    C7 --> M1

    M1 --> A1
    M2 --> A2
    M3 --> A3
    M4 --> A4
    M5 --> A5
    M6 --> A6
    M7 --> A7
```

**Adding a brand-new capability under an existing mode is a YAML edit** —
no broker, gateway, or daemon release. Adding a new mode is the rare case
where code lands in both `capability-broker/` and `gateway-adapters/`.

See [`./interaction-modes.md`](./interaction-modes.md).

## Layer 3 — Declarative capability config

`host-config.yaml`. Three concerns: identity, capabilities, backends.

```yaml
identity:
  orch_eth_address: 0xabc...

capabilities:
  - id: "openai:chat-completions"
    interaction_mode: "http-stream"
    work_unit:
      name: "tokens"
      extractor: { type: "openai-usage" }
    health:
      probe:
        type: "http-openai-model-ready"
        path: "/healthz"
        expect_model: "llama-3-70b"
        timeout_ms: 1500
        interval_ms: 5000
        unhealthy_after: 2
    price:
      amount_wei: 1500000
      per_units: 1
    backend:
      transport: "http"
      url: "http://10.0.0.5:8000/v1/chat/completions"
      auth: "none"
    extra:
      openai:
        model: "llama-3-70b"
      provider: "vllm"
      region: "us-west-2"
      gpu_class: "h100"
```

The `extractor` library is a small fixed set of recipes (`openai-usage`,
`response-jsonpath`, `request-formula`, `bytes-counted`, `seconds-elapsed`,
`ffmpeg-progress`). Adding an extractor is a broker change but extremely rare.

### OpenAI-compatible `extra` shape

For OpenAI-compatible offerings, the canonical `capability_id` stays at the
base endpoint family (`openai:chat-completions`, `openai:embeddings`,
`openai:audio-transcriptions`, `openai:audio-speech`,
`openai:images-generations`, `openai:realtime`). Model identity does **not**
live in `capability_id`; it lives in `extra.openai.model`.

The standardized shape is:

```yaml
extra:
  openai:
    model: "Qwen3.6-27B"
  provider: "vllm"
  served_model_name: "Qwen3.6-27B"
  backend_model: "sakamakismile/Qwen3.6-27B-Text-NVFP4-MTP"
  features:
    streaming: true
    tools: true
    embeddings: false
    json_mode: true
```

Rules:

- `extra.openai.model` is required for current `openai:*` offerings.
- `extra.provider` is required for current `openai:*` offerings.
- `served_model_name`, `backend_model`, and `features.*` are optional stable
  enrichment fields.
- `features.*`, when present, are booleans.
- Operator-owned deployment labels such as `region`, `gpu_class`, and
  `latency_tier` may also live in `extra`.
- For `provider: "vllm"` and `provider: "ollama"` on HTTP backends, the broker
  may probe `GET /v1/models` at startup and fill missing
  `served_model_name`, `backend_model`, and stable `features.*` fields when the
  configured `extra.openai.model` is found upstream.
- For runner families with stable options or presets surfaces, the broker may
  fill missing `extra.audio.*`, `extra.video.*`, or `extra.vtuber.*` fields
  from those family-specific endpoints using the same fill-only merge policy.
- The broker refreshes this metadata on a bounded cadence while running.
  Discovery freshness, provider, last result, and last error are exposed via
  `GET /registry/health`; they do not change the tuple's market identity.
- Prometheus also exposes
  `livepeer_metadata_refresh_total{family,provider,result}` so discovery drift
  and probe failures are visible without polling per-offering health.
- It also exposes refresh latency and freshness signals via
  `livepeer_metadata_refresh_duration_seconds{family,provider,result}`,
  `livepeer_metadata_refresh_last_attempt_timestamp_seconds{family,capability,offering,provider}`,
  and
  `livepeer_metadata_refresh_last_success_timestamp_seconds{family,capability,offering,provider}`.
- For alerting on the current discovery state, it also exposes
  `livepeer_metadata_refresh_current_result{family,capability,offering,provider,result}`,
  where the active result label is `1` and previous results are reset to `0`
  when the offering transitions.
- To surface sustained discovery breakage, it also exposes
  `livepeer_metadata_refresh_consecutive_failures{family,capability,offering,provider}`,
  and the same `consecutive_failures` value appears in
  `GET /registry/health` metadata for each applicable offering.
- `last_result` is family-aware rather than a single generic status. For
  example, OpenAI-compatible offerings may report `model_not_found` or
  `models_probe_failed`, while runner families may report
  `audio_options_probe_failed`, `video_presets_empty`, or
  `vtuber_options_probe_failed`.

Boundary:

- `host-config.yaml` owns operator intent: capability family, offering ID,
  interaction mode, price, metering, backend URL, and routing constraints.
- Runtime discovery may validate and enrich an offering, but it does not invent
  or rewrite its market identity. The broker must not rewrite
  `extra.openai.model`, `offering_id`, `price`, or `constraints`.
- Volatile runtime facts such as full model inventories, queue depth,
  throughput, utilization, or context window belong in live health, metrics,
  or diagnostics, not in the signed manifest.

### Family-specific stable `extra` contracts

The same pattern applies across every runner family in the rewrite:

- `host-config.yaml` defines the offering's market identity.
- Family-specific discovery validates and enriches only stable metadata.
- Volatile runtime state belongs in `GET /registry/health` or metrics, not in
  the signed manifest.

Every family should expose a small stable namespace under `extra`:

- `extra.openai.*`
- `extra.audio.*`
- `extra.video.*`
- `extra.vtuber.*`

with a shared top-level `provider` field naming the backend or runner family.

#### Audio

For audio capabilities, the stable contract separates workload type from
runner-specific live state:

```yaml
extra:
  openai:
    model: "whisper-large-v3"
  provider: "openai-audio-runner"
  served_model_name: "whisper-large-v3"
  backend_model: "openai/whisper-large-v3"
  audio:
    task: "transcription"
    formats:
      input: ["mp3", "wav", "m4a", "flac"]
      output: ["json", "text", "srt", "verbose_json", "vtt"]
```

```yaml
extra:
  openai:
    model: "kokoro"
  provider: "openai-tts-runner"
  served_model_name: "kokoro"
  backend_model: "hexgrad/Kokoro-82M"
  audio:
    task: "speech"
    voices:
      default: "af_bella"
      native: ["af_bella", "am_michael"]
      aliases:
        alloy: "af_bella"
        echo: "am_michael"
    formats:
      output: ["mp3", "wav", "pcm"]
```

Stable:

- model identity
- voice/options catalog
- supported input/output formats
- backend family

Live only:

- model warm state
- queue depth
- GPU readiness
- transient inference failures

#### Video

Video capabilities should publish the stable pipeline shape, not current load:

```yaml
extra:
  provider: "abr-runner"
  video:
    task: "abr-transcode"
    presets: ["abr-standard", "abr-premium"]
    codecs: ["h264", "hevc"]
    packaging: ["hls"]
    hardware:
      gpu_vendor: "nvidia"
```

```yaml
extra:
  provider: "transcode-runner"
  video:
    task: "transcode"
    presets: ["h264-1080p", "hevc-1080p"]
    codecs: ["h264", "hevc"]
    packaging: ["mp4"]
    hardware:
      gpu_vendor: "intel"
```

Stable:

- task shape (`transcode`, `abr-transcode`, etc.)
- supported preset names
- supported video codecs
- packaging outputs
- hardware vendor hints

Live only:

- encoder availability
- scratch-disk pressure
- concurrent job count
- GPU load
- temporary backpressure

#### VTuber

Session-style VTuber workloads should publish stable runtime capabilities and
schema versions, not live session availability:

```yaml
extra:
  provider: "vtuber-runner"
  vtuber:
    task: "session"
    control_schema: "vtuber-control/v1"
    media_schema: "trickle-segment-stream/v1"
    features:
      renderer_control: true
      status_polling: true
      trickle_publish: true
      youtube_egress: true
```

Stable:

- control/media schema identifiers
- supported session features
- runner family

Live only:

- available session slots
- media-plane readiness
- reconnect window state
- renderer warm/cold state

#### New families

Any new runner family should define four things before implementation:

1. the base `capability_id`
2. the minimal stable `extra.<family>` schema
3. the discovery source that fills stable enrichment fields
4. the live-health source for volatile runtime state

This keeps new workloads consistent with the broker's publication boundary:
stable capability facts in `/registry/offerings`, live availability facts in
`/registry/health`, and no direct runner-owned manifest identity.

Live health follows the same pattern: the broker owns a small fixed
library of **probe recipes** and `host-config.yaml` selects one per tuple.
Examples might include:

- `http-status` — shallow HTTP reachability
- `http-jsonpath` — response field must match an expected value
- `http-openai-model-ready` — backend is up and a specific model is loaded
- `tcp-connect` — port accepts connections
- `command-exit-0` — local process or sidecar probe
- `runner-options-match` — backend reports the expected offering or mode
- `manual-drain` — operator intent overrides automatic readiness

The important boundary is:

- **capabilities choose a probe recipe**
- **the broker executes the probe**
- **the broker normalizes the result to generic outward states**

That lets the core modules support specialized health behavior without
teaching the coordinator, resolver, or gateways what "model loaded",
"pipeline warmed", or "TURN path ready" mean for any specific workload.

Just like extractors, new probe recipe types are broker changes and
should be rare. Day-to-day operator work is selecting and tuning existing
recipes in YAML, not writing new code.

This is the operator's entire day-to-day surface.

## Layer 4 — Discovery (workload-agnostic registry)

- **Manifest data model**: a flat list of
  `(capability_id, offering_id, interaction_mode, work_unit_name, price_per_unit_wei, worker_url, eth_address, extra, constraints)`
  tuples. **Host is not a registration unit.**
- **Coordinator UI**: roster is per-capability-tuple, not per-host. Multi-binary-per-host
  vanishes (no separate binaries); multi-broker-per-orch is N more entries.
- Resolver semantics keep their existing shape but the response now carries
  `interaction_mode`.

The current `service-registry-daemon` resolver/publisher split keeps working; what
changes is the manifest schema and the coordinator UX.

**Two on-chain registries point at the same well-known URL.** Livepeer mainnet
(Arbitrum One) has two distinct contracts that name a `serviceURI` per orch:
the legacy `ServiceRegistry` for transcoding workers and the newer
`AIServiceRegistry` for AI workers. The rewrite consolidates the manifest:
one orch publishes one signed manifest at `/.well-known/livepeer-registry.json`,
mixes transcoding and AI tuples in the same `capabilities[]` list, and
registers the same URL with whichever contract address(es) the operator
participates in. The resolver / gateway side is configured with which contract
address(es) to query for a given orch's `serviceURI`; the orch may register
the same URL in both. The on-chain pointer fetch is per-contract, but the
manifest URL it points at is unified. See
[`../../livepeer-network-protocol/manifest/README.md`](../../livepeer-network-protocol/manifest/README.md)
for the manifest-side write-up.

### Resolver fetch flow

What happens when the gateway needs to know "who serves
`openai:chat-completions` with `extra.openai.model=llama-3-70b` right now?" The resolver verifies the
signature on every fetch — the coordinator host is not trusted.

```mermaid
sequenceDiagram
    autonumber
    participant GW as gateway shell
    participant SRD as service-registry-daemon<br/>(resolver, gateway side)
    participant Chain as ServiceRegistry /<br/>AIServiceRegistry
    participant Coord as orch-coordinator<br/>(public host)
    participant BM as BondingManager

    Note over GW,Coord: Per-round refresh (cron-driven, ~19h on Arbitrum One)
    SRD->>BM: GetFirstTranscoderInPool /<br/>GetNextTranscoderInPool
    BM-->>SRD: orch addresses
    loop for each orch
        SRD->>Chain: getServiceURI(orch_addr)
        Chain-->>SRD: well-known manifest URL
        SRD->>Coord: GET /.well-known/livepeer-registry.json
        Coord-->>SRD: signed manifest
        SRD->>SRD: verify sig against on-chain<br/>orch identity (defense in depth)
        SRD->>SRD: flatten into (capability_id,<br/>offering_id, mode, work_unit,<br/>price, worker_url, eth_address) tuples
        SRD->>SRD: cache
    end

    Note over GW,SRD: On the hot path
    GW->>SRD: Resolver.Select(capability_id,<br/>offering_id?, tier?, min_weight?)
    SRD-->>GW: route { worker_url, eth_address,<br/>interaction_mode, work_unit,<br/>price_per_unit_wei, extra }
```

**Two verifications, intentionally.** The coordinator verifies on upload; every
gateway resolver verifies again on fetch. If the coordinator host is ever
compromised, tampered manifests still don't propagate.

**`interaction_mode` is in the resolver response** — the gateway picks the
adapter from this, not from any per-capability lookup table.

## Layer 5 — Trust spine: operator-driven sign cycle

**Hard rule:** secure-orch never accepts inbound connections.

**Operator-driven cycle:**

1. Operator edits `host-config.yaml` on broker host(s).
2. Broker re-advertises locally; orch-coordinator scrapes; coordinator builds candidate
   manifest and exposes it for download.
3. Operator pulls candidate to secure-orch (download via console, scp, USB — operator's
   choice).
4. `secure-orch-console` shows a **diff** of candidate vs. currently-published manifest.
   Operator inspects, taps to sign. Cold key (HSM-backed, never moves) signs.
5. Operator pushes signed manifest back to coordinator.
6. Coordinator atomic-swap publishes.

Friction reduction lives in console UX (diff, one-click sign, clear status). Hand-carry
stays. Revisit automation in v2.

```mermaid
sequenceDiagram
    autonumber
    actor Op as Operator
    participant Broker as Capability Broker<br/>(worker-orch host)
    participant Coord as orch-coordinator<br/>(public host)
    participant SOC as secure-orch-console<br/>(LAN-only)
    participant PRD as protocol-daemon<br/>(publisher path)
    participant Cold as cold orch keystore<br/>(HSM-backed)
    participant Chain as ServiceRegistry

    Note over Op,Broker: 1. Operator edits config on worker host
    Op->>Broker: edit host-config.yaml
    Broker->>Broker: reload /registry/offerings

    Note over Coord,Broker: 2. Coordinator scrapes, builds candidate
    Coord->>Broker: GET /registry/offerings
    Broker-->>Coord: capability tuples
    Coord->>Coord: merge per-host fragments → candidate manifest
    Coord->>Coord: expose candidate for download

    Note over Op,Cold: 3. Operator pulls candidate to secure-orch (scp / USB / console)
    Op->>SOC: import candidate manifest
    SOC->>SOC: render diff vs currently-published manifest
    Op->>SOC: review + tap Sign
    SOC->>PRD: Publisher.BuildAndSign
    PRD->>Cold: sign canonical bytes (HSM)
    Cold-->>PRD: signature
    PRD-->>SOC: signed manifest

    Note over Op,Coord: 4. Operator ships signed manifest back
    Op->>Coord: POST /admin/manifest (signed)
    Coord->>Chain: read on-chain orch identity
    Chain-->>Coord: orch address
    Coord->>Coord: verify sig against orch identity
    Coord->>Coord: atomic-swap publish at<br/>/.well-known/livepeer-registry.json

    Note over Cold: cold key never leaves secure-orch.<br/>Manifests cross host boundaries — keys do not.
```

**Hard invariants** the sign cycle preserves:

- `secure-orch` accepts **zero** inbound connections from outside the LAN.
- The cold key signs canonical manifest bytes only — never naked transactions.
- Both the coordinator and every downstream resolver verify the signature
  against on-chain orch identity. Trust nothing the coordinator says alone.

See [`./trust-model.md`](./trust-model.md).

## Layer 6 — Payment

`payment-daemon` keeps its sender/receiver shape. **The one decoupling**: the daemon
stops enforcing a closed enum of capability/work-unit names. Both become opaque strings;
the daemon does the arithmetic `price_wei = price_per_unit_wei × actualUnits`. Custom
capabilities with custom work units (`barks`, `pixel-seconds`, anything) work without
trunk changes.

The `Livepeer-Payment` header gains `(capability_id, offering_id, expected_max_units)`
so the receiver can refuse mismatched routing.

### Per-request payment (`http-reqresp` / `http-stream` / `http-multipart`)

One ticket per inbound request. Settles on-chain only if the ticket is winning;
otherwise it's expected-value credit. `actualUnits` is reported after the
backend response so over- and under-spend are both true-ups, not gambles.

```mermaid
sequenceDiagram
    autonumber
    participant GW as gateway adapter
    participant Sender as payment-daemon<br/>sender (gateway side)
    participant Broker as Capability Broker
    participant Receiver as payment-daemon<br/>receiver (worker side)
    participant TB as TicketBroker<br/>(chain)
    participant Backend as backend

    GW->>Sender: CreatePayment(face_value, recipient,<br/>capability_id, offering_id,<br/>expected_max_units)
    Sender-->>GW: signed ticket
    GW->>Broker: forward request<br/>+ Livepeer-Payment header
    Broker->>Receiver: ProcessPayment(payment_bytes,<br/>expected_max_units, price_per_unit,<br/>capability_id, offering_id)
    alt ticket is winning
        Receiver->>TB: redeemWinningTicket
        TB-->>Receiver: faceValue credited to orch reserve
    else not winning
        Receiver->>Receiver: expected-value credit (in-memory)
    end
    Receiver-->>Broker: ok (sender, credited_ev)

    Broker->>Backend: forward
    Backend-->>Broker: response + raw usage signal
    Broker->>Broker: extractor → actualUnits
    Broker->>Receiver: ReportUsage(work_id, actualUnits)
    Receiver-->>Broker: ok (final price = price_per_unit × actualUnits)
    Broker-->>GW: response
```

### Streaming / session payment (`ws-realtime` / `session-control-plus-media` / `rtmp-…`)

Amortized billing: one `OpenSession` at attach, periodic `Debit` ticks during
the session, `CloseSession` on teardown. The cross-workload rules live in
[`streaming-workload-pattern.md`](./streaming-workload-pattern.md) — this is the
canonical shape.

```mermaid
sequenceDiagram
    autonumber
    participant GW as gateway adapter
    participant Sender as payment-daemon<br/>sender (gateway)
    participant Broker as Capability Broker
    participant Receiver as payment-daemon<br/>receiver (worker)
    participant Backend as backend<br/>(session-runner / FFmpeg / …)

    Note over GW,Backend: 1. Open — single ticket bootstraps the session balance
    GW->>Sender: CreatePayment(face_value, recipient,<br/>capability_id, offering_id)
    Sender-->>GW: ticket
    GW->>Broker: POST .../sessions/start<br/>+ Livepeer-Payment
    Broker->>Receiver: OpenSession(payment_bytes, work_id,<br/>capability_id, offering_id)
    Receiver-->>Broker: { sender, credited_ev, balance }
    Broker->>Backend: forward open
    Backend-->>Broker: session active
    Broker-->>GW: { work_id, session_id }

    Note over GW,Backend: 2. Live — periodic debits + top-ups against the same work_id
    loop usage tick (continuous)
        Backend-->>Broker: media / control frames<br/>(units accrue)
        Broker->>Receiver: DebitBalance(sender, work_id, units)
        Broker->>Receiver: SufficientBalance(sender, work_id, min_runway)
        Receiver-->>Broker: ok / low-runway warning
        Broker-->>GW: session.usage.tick
        alt low runway
            GW->>Sender: CreatePayment(top_up, recipient,<br/>capability_id, offering_id)
            Sender-->>GW: ticket
            GW->>Broker: TopUp(work_id, payment_bytes)
            Broker->>Receiver: CreditBalance(sender, work_id, payment_bytes)
        end
    end

    Note over GW,Backend: 3. Close — settle remaining balance
    GW->>Broker: CloseSession(work_id)
    Broker->>Receiver: CloseSession(work_id)
    Receiver-->>Broker: final balance
    Broker-->>GW: session.closed
```

**Worker meters, gateway ledgers.** The worker-side receiver is the runtime
enforcement point (cuts the session when balance hits zero); the gateway-side
ledger is the commercial record. Usage ticks are idempotent so a retry never
double-charges.

See [`./payment-decoupling.md`](./payment-decoupling.md).

## Layer 7 — Routing (gateway side)

- `service-registry-daemon` applies Layer 1 + Layer 2 before the gateway sees
  a route: signed-manifest validity plus broker live health.
- Gateway resolves a route → gets the tuple including `interaction_mode`.
- Picks the matching mode adapter (req/resp, stream, ws, RTMP, session) — generic across
  capabilities.
- Wraps with `Authorization` (customer's bearer), `Livepeer-Payment` (ticket from sender
  daemon), `Livepeer-Capability: <id>`, `Livepeer-Offering: <id>`, opens transport,
  forwards.
- Gateway applies Layer 3 locally: recent request outcomes can temporarily
  cool a route even when manifest + live health are still green.
- For session/stream/realtime: payment is amortized
  (`OpenSession + periodic Debit + CloseSession`).

**Gateway code is per-mode, not per-capability.** New capability under an existing mode
lights up automatically once the manifest carries it.

**Gateway health policy is shared, not forked.** The gateways reuse the
workspace package
[`../../gateway-route-health/`](../../gateway-route-health/) for cooldown
tracking, cumulative counters, summary generation, and Prometheus-style
rendering so OpenAI, video, VTuber, and Daydream all apply the same Layer 3
policy shape.

```mermaid
flowchart TD
    Cust["customer request"] --> Shell["gateway shell<br/>(OpenAI / video / vtuber / …)"]
    Shell --> Auth["AuthResolver<br/>(bearer → customer + balance)"]
    Auth --> Resolve["Resolver.Select(capability_id,<br/>offering_id?, tier?, min_weight?)"]
    Resolve --> Tuple["route tuple<br/>{ worker_url, eth_address,<br/>interaction_mode, work_unit,<br/>price_per_unit, extra }"]
    Tuple --> ModeSwitch{interaction_mode?}

    ModeSwitch -->|http-reqresp| A1["reqresp adapter"]
    ModeSwitch -->|http-stream| A2["stream adapter<br/>(SSE / chunked)"]
    ModeSwitch -->|http-multipart| A3["multipart adapter"]
    ModeSwitch -->|ws-realtime| A4["ws adapter"]
    ModeSwitch -->|rtmp-ingress-hls-egress| A5["rtmp adapter"]
    ModeSwitch -->|session-control-plus-media| A6["session adapter"]

    A1 --> Sender["payment-daemon sender<br/>CreatePayment"]
    A2 --> Sender
    A3 --> Sender
    A4 --> Sender
    A5 --> Sender
    A6 --> Sender

    Sender --> Wrap["wrap headers:<br/>Authorization (customer bearer)<br/>Livepeer-Payment (ticket)<br/>Livepeer-Capability / Offering"]
    Wrap --> Broker["Capability Broker<br/>(worker-orch host)"]
```

The shell, the resolver, the sender daemon, and the wrap step are
capability-agnostic. The only per-workload code is the customer-facing surface
(OpenAI-shaped routes, Mux-inspired video routes, vtuber session API) — and
those exist to match the customer contract, not to express anything about how
the network works underneath.

## Layer 8 — Demand visibility

- Every component exposes Prometheus on a documented schema.
- Counters: `livepeer_routes_total{capability,offering,outcome}`
- Histograms: `livepeer_price_paid_wei{capability}`
- Gauges: `livepeer_capacity_available{capability}`
- `service-registry-daemon` also exposes Layer 2 route-admission counters for
  decisions like `allowed_ready`, `excluded_unhealthy`, `excluded_stale`,
  `live_health_missing`, and `live_health_fetch_error`.
- Gateways expose Layer 3 route-health counters and summaries through both
  debug/admin JSON and Prometheus text endpoints.
- Independent third party scrapes both sides → public market data feed.

Architecture provides surfaces; aggregation is third-party.

```mermaid
flowchart LR
    subgraph supply["Supply side"]
        direction TB
        CB["Capability Broker<br/>/metrics"]
        WPD["payment-daemon (receiver)<br/>/metrics"]
        OC["orch-coordinator<br/>/metrics"]
    end

    subgraph demand["Demand side"]
        direction TB
        GW["gateway shell<br/>/metrics"]
        GPD["payment-daemon (sender)<br/>/metrics"]
        SRD["service-registry-daemon<br/>/metrics"]
    end

    Scraper["independent scraper<br/>(third party)"]
    Feed[("public market data feed<br/>capability × price × capacity")]

    CB --> Scraper
    WPD --> Scraper
    OC --> Scraper
    GW --> Scraper
    GPD --> Scraper
    SRD --> Scraper
    Scraper --> Feed
```

**The architecture's job is to expose comparable surfaces on both sides** —
same metric names, same labels (`capability`, `offering`, `outcome`),
documented in the protocol repo. Aggregation, sanity-checking, and
publication are deliberately out-of-band so no operator can rewrite the
market's view of itself.

## What this kills / changes / preserves

### Kills

- The three workload-shaped worker binaries (`openai-worker-node`, `vtuber-worker-node`,
  `video-worker-node`) — replaced by one capability broker.
- Per-capability Go `Module` impls in `worker-runtime`.
- Hardcoded work-unit enums in `livepeer-modules-project`.
- The dead `livepeer-modules-conventions` reference (replaced by
  `livepeer-network-protocol`).
- The "host is the registration unit" assumption in coordinator UX.
- Capacity declarations in the manifest (replaced by 503 + backoff hint).

### Changes

- Manifest schema: flat list of capability tuples; `interaction_mode` in resolver
  response.
- `payment-daemon`: opaque capability/work-unit names; arithmetic only.
- Coordinator UX: capability-as-roster-entry.
- `Livepeer-Payment` header: includes `(capability_id, offering_id, expected_max_units)`.

### Preserves (sacred)

- Cold orch keystore on firewalled secure-orch. Never moves.
- Cold-key signature on every manifest publication.
- Double-verification of signed manifest (coordinator on upload, resolver on fetch).
- On-chain orch identity (`ServiceRegistry`).
- `payment-daemon`'s ticket validation against chain.
- Mainnet-only deployment, image-tags-not-bumped, the rest of the suite's core beliefs.

# Architecture overview

The eight-layer sketch. This is the **at-a-glance** view; deep dives go in their own
design-docs. Full provenance lives in
[`../references/2026-05-06-architecture-conversation.md`](../references/2026-05-06-architecture-conversation.md).

## Shape in one sentence

A single workload-agnostic process per orch host — the **capability broker** — that owns
`/registry/offerings`, dispatches paid requests over a small fixed typology of *interaction
modes* to arbitrary backends declared in YAML, with the trust spine preserved by an
operator-driven, cold-key-signed manifest publication cycle.

## Diagram

```
+----------------------------------------------------------+
|                      Arbitrum One                        |
|     ServiceRegistry  TicketBroker  Bonding  Rounds       |
+----------------------------------------------------------+
       ^                 ^                 ^
       | sigs            | tickets         | rounds
+--------------+   +--------------+   +--------------+
| secure-orch  |   |  gateway     |   | orch-coord   |
|  (cold key,  |   |  resolver    |   |  scrape +    |
|   HSM,       |   |  + payer     |   |  manifest    |
|   protocol)  |   |  daemon      |   |  hosting     |
+--------------+   +--------------+   +--------------+
        ^                 |                  ^
        | operator-       | paid HTTP/       | scrape
        | driven          | WS/RTMP          | /registry/offerings
        | sign cycle      v                  |
        |         +-------------------------+
        |         |   Capability Broker     |
        |         | (one per host, generic) |
        |         |                         |
        |         |  /registry/offerings    |
        |         |  /v1/cap/{id}/...       |
        |         |  RTMP listener          |
        |         |  WS upgrade             |
        |         |  payment-daemon socket  |
        |         |  metrics endpoint       |
        |         +-----------+-------------+
        |                     |
        |                     | per-capability backend descriptor
        |                     v
        |    +----------+ +----------+ +----------+ +----------+
        |    | vLLM     | | OpenAI   | | FFmpeg   | | session- |
        |    | (local)  | | API      | | (local)  | | runner   |
        |    | LAN box  | | (saas)   | |          | |          |
        |    +----------+ +----------+ +----------+ +----------+
```

## Layer 1 — Capability broker

**One process per host, workload-agnostic.** No per-capability Go code. Five jobs:

1. Read a single `host-config.yaml`.
2. Expose `GET /registry/offerings`, `GET /registry/health`, `GET /healthz`,
   `GET /metrics`, plus one canonical path per mode (e.g. `POST /v1/cap` for
   `http-reqresp` — see [`../../livepeer-network-protocol/modes/`](../../livepeer-network-protocol/modes/)).
3. Route inbound requests by **`Livepeer-Capability` header** → look up the
   **backend descriptor** → wrap in the declared **interaction mode** → forward →
   return the response.
4. Report `actualUnits` to co-located `payment-daemon` (receiver) over unix socket — same
   socket regardless of capability.
5. Optionally aggregate `/registry/offerings` from peer brokers on the LAN.

**The broker contains zero capability semantics.**

Replaces: `openai-worker-node`, `vtuber-worker-node`, `video-worker-node`.

## Layer 2 — Interaction-mode typology

The fixed wire contracts. Capabilities pick one. Initial set:

| Mode | Wire shape | Examples |
|---|---|---|
| `http-reqresp` | one HTTP req → one HTTP resp | `openai:embeddings`, custom REST |
| `http-stream` | request → SSE / chunked stream | `openai:chat-completions` (stream) |
| `http-multipart` | multipart upload → response | `openai:audio-transcriptions` |
| `ws-realtime` | bidirectional WebSocket | `openai:realtime`, vtuber `/control` |
| `rtmp-ingress-hls-egress` | RTMP in → HLS manifest+segments out | `video:live.rtmp` |
| `session-control-plus-media` | HTTP session-open → long-lived media plane | `livepeer:vtuber-session` |

Each mode is implemented once in the broker, once in the gateway. **New capability
under an existing mode = zero code.** New mode = one adapter on each side.

**Modes are specifications, not libraries.** Living in the
`livepeer-network-protocol` spec repo (working name) — not a code dependency.

Detail to come: `interaction-modes.md`.

## Layer 3 — Declarative capability config

`host-config.yaml`. Three concerns: identity, capabilities, backends.

```yaml
identity:
  orch_eth_address: 0xabc...

capabilities:
  - id: "openai:chat-completions:llama-3-70b"
    interaction_mode: "http-stream"
    work_unit:
      name: "tokens"
      extractor: { type: "openai-usage" }
    price:
      amount_wei: 1500000
      per_units: 1
    backend:
      transport: "http"
      url: "http://10.0.0.5:8000/v1/chat/completions"
      auth: "none"
```

The `extractor` library is a small fixed set of recipes (`openai-usage`,
`response-jsonpath`, `request-formula`, `bytes-counted`, `seconds-elapsed`,
`ffmpeg-progress`). Adding an extractor is a broker change but extremely rare.

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

Detail to come: `trust-model.md`.

## Layer 6 — Payment

`payment-daemon` keeps its sender/receiver shape. **The one decoupling**: the daemon
stops enforcing a closed enum of capability/work-unit names. Both become opaque strings;
the daemon does the arithmetic `price_wei = price_per_unit_wei × actualUnits`. Custom
capabilities with custom work units (`barks`, `pixel-seconds`, anything) work without
trunk changes.

The `Livepeer-Payment` header gains `(capability_id, offering_id, expected_max_units)`
so the receiver can refuse mismatched routing.

Detail to come: `payment-decoupling.md`.

## Layer 7 — Routing (gateway side)

- Gateway resolves a route → gets the tuple including `interaction_mode`.
- Picks the matching mode adapter (req/resp, stream, ws, RTMP, session) — generic across
  capabilities.
- Wraps with `Authorization` (customer's bearer), `Livepeer-Payment` (ticket from sender
  daemon), `Livepeer-Capability: <id>`, `Livepeer-Offering: <id>`, opens transport,
  forwards.
- For session/stream/realtime: payment is amortized
  (`OpenSession + periodic Debit + CloseSession`).

**Gateway code is per-mode, not per-capability.** New capability under an existing mode
lights up automatically once the manifest carries it.

## Layer 8 — Demand visibility

- Every component exposes Prometheus on a documented schema.
- Counters: `livepeer_routes_total{capability,offering,outcome}`
- Histograms: `livepeer_price_paid_wei{capability}`
- Gauges: `livepeer_capacity_available{capability}`
- Independent third party scrapes both sides → public market data feed.

Architecture provides surfaces; aggregation is third-party.

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

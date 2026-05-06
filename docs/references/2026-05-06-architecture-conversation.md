# Architecture conversation — 2026-05-06

Source-of-truth synthesis of the design conversation that motivated this repo. This is a
**reference** document — point-in-time, comprehensive, not a plan. Newer design-docs may
supersede individual sections; preserve this file as-is for provenance.

> Participant framing: the user is acting as an orchestrator-operator on the existing
> Cloud-SPE Livepeer Network Suite. The conversation began with a code-review of four
> sibling repos in `livepeer-cloud-spe/`, expanded to a review of the suite meta-repo
> (`livepeer-network-suite`), and converged on a workload-agnostic supply-side rearchitecture.

---

## 1. Source architecture review

### 1.1 The four repos under review

Located at `/home/mazup/git-repos/livepeer-cloud-spe/`:

- `openai-worker-node` — Go daemon. Capabilities: `openai:/v1/chat/completions`,
  `openai:/v1/embeddings`, `openai:/v1/images/generations`, `openai:/v1/images/edits`,
  `openai:/v1/audio/speech`, `openai:/v1/audio/transcriptions`. Imports
  `livepeer-modules-project/worker-runtime`. Co-located with `payment-daemon` (receiver)
  over unix socket. Static-on-startup; fail-closed cross-check against daemon catalog.
- `vtuber-worker-node` — Go daemon. One capability: `livepeer:vtuber-session`. Thin
  session-orchestrator: accepts `POST /api/sessions/start`, returns 202, detaches a
  goroutine, debits every 5s via `PaymentSession.Debit`, forwards everything else to a
  separate `session-runner` backend. Vendors a local proto snapshot (`proto/livepeer/payments/`)
  rather than importing `proto-contracts`. Custom `payment-middleware-check` golangci-lint
  analyzer.
- `video-worker-node` — Go daemon, three runtime images (nvidia/intel/amd). Capabilities:
  `video:transcode.vod`, `video:transcode.abr`, `video:live.rtmp`. RTMP listener on `:1935`
  set up via `POST /stream/start`. Pure FFmpeg subprocess driver; `livecdn.Mirror` exports
  HLS to local fs or S3.
- `livepeer-modules-project` — the trunk. Six Go modules: three libraries
  (`worker-runtime`, `chain-commons`, `proto-contracts`) and three daemons
  (`payment-daemon`, `service-registry-daemon`, `protocol-daemon`). `worker-runtime/services/server/middleware.go`
  is the canonical paid-request pipeline. `Module` and `StreamingModule` interfaces are
  the workload-extension points.

**Coupling shape:** workers → modules, one-way. Workers share zero source between siblings
(ADR-003 in vtuber-project mandates "no shared code"). vtuber's vendored proto snapshot is
divergence from the other two.

**Capability registration:** no direct push to orch's `/capability/register`. Workers expose
`GET /registry/offerings`; orch-coordinator scrapes; manifest is signed by cold key on
`secure-orch`. Pull + chain-anchored, not push.

**Dynamic workloads:** none. Capability set is static at build time. Adding a new capability
type requires a new Go `Module` impl + worker recompile + payment-daemon catalog awareness.
The "dynamism" is YAML-level: operators edit `worker.yaml` to swap backends/prices, not
capabilities.

### 1.2 The suite meta-repo

`livepeer-network-suite` is a coordinator, not a monorepo. 14 submodule pins; release artifact
is the tag. Five layers, top to bottom:

1. **Consumer applications** — `livepeer-vtuber-project` (Pipeline SaaS).
2. **Workload binaries** — three workload triples in engine + shell + worker shape:
   OpenAI, Video, VTuber.
3. **Operator surfaces** — `livepeer-up-installer`, `livepeer-secure-orch-console`,
   `livepeer-orch-coordinator`, `livepeer-gateway-console`.
4. **On-chain control plane** — `livepeer-modules`: `payment-daemon`,
   `service-registry-daemon`, `protocol-daemon`, `chain-commons`.
5. **Arbitrum One** — `TicketBroker`, `ServiceRegistry`, `BondingManager`, `RoundsManager`.

**Archetype A** is the only supported deployment as of suite v3.0.0:
- Workers are **registry-invisible**. They expose `/registry/offerings` only. They do
  not run a publisher daemon and do not sign anything.
- Operator hand-carries proposed manifest to firewalled `secure-orch`, signs there with
  cold key, hand-carries the signed file back. Coordinator atomic-swaps publishes at
  `/.well-known/livepeer-registry.json`.
- Gateways resolve via `service-registry-daemon` resolver →
  `(worker_url, eth_address, capability, offering, price_per_work_unit_wei, work_unit, extra, constraints)`.
- Cold key never crosses a host boundary.

**Active exec plans:** `0001-upstream-naming-cleanup`, `0002-suite-wide-alignment`,
`0003-archetype-a-deploy-unblock`.

---

## 2. Concept verification

Confirmed alignment on four core concepts before the design discussion:

### 2.1 Worker node (payee-side)

Go daemon hosting one or more capability modules from `worker-runtime`. Co-located with
`payment-daemon` (receiver) over unix socket. Decodes `Livepeer-Payment` header, opens
session, processes ticket, debits estimated work units, reconciles after `Serve`. Advertises
inventory via `GET /registry/offerings`. Registry-invisible under Archetype A.

### 2.2 Gateway (payer-side)

Customer-facing HTTPS API. Authenticates customer with `Authorization: Bearer <api-key>`,
runs the workload's request pipeline (OpenAI/Video have an OSS engine; vtuber doesn't yet),
resolves a route via the resolver, mints a `Livepeer-Payment` header through
co-located `payment-daemon` in **sender mode**, forwards to the chosen worker. Owns the
customer USD ledger in Postgres (Stripe top-up, free tier + prepaid). For long-lived
surfaces (vtuber sessions): mints `vtbs_*` session-scoped child bearers, relays
`/control` + `/worker-control` WebSockets, debits USD per usage tick.

### 2.3 Two-tier billing — wholesale vs. USD

- **USD side**: customer ↔ gateway. Stripe, free tier + prepaid, Postgres ledger.
  Customer experience.
- **Wholesale side**: gateway ↔ worker. Probabilistic tickets on Arbitrum, denominated
  in ETH. Wholesale price is the worker's `price_per_work_unit_wei` from the resolver's
  selected route.
- Gateway operator's business *is* the spread. Customers never see wei; workers never
  see USD.

### 2.4 Service registry (one daemon, two modes)

- **Publisher mode**: lives on firewalled `secure-orch` with cold orchestrator key.
  `secure-orch-console` drives `Publisher.BuildAndSign` to produce signed rooted manifest.
  Operator hand-carries to public `orch-coordinator`, which atomic-swaps onto
  `/.well-known/livepeer-registry.json`. Workers do not run publisher under Archetype A.
- **Resolver mode**: sidecar on each gateway host. Watches Arbitrum's `ServiceRegistry`
  for orch service URIs, fetches each `/.well-known/livepeer-registry.json`, **verifies
  the signature against on-chain orch identity**, builds local index. Gateway calls
  `Resolver.Select(capability, offering, tier, min_weight)` over unix-socket gRPC.

Manifest signature is verified twice (defense in depth): once at `orch-coordinator` on
upload, again at each resolver on fetch.

---

## 3. The problem statement (operator's voice)

### 3.1 The server-2 incident

Operator setup:
- Server 1: OpenAI chat (vLLM)
- Server 2: OpenAI audio TTS + OpenAI audio STT + a vtuber capability
- Server 3: video transcoding

Server 2 needed **two different worker binaries** simultaneously — `openai-worker-node`
(for audio TTS+STT) and `vtuber-worker-node` (for vtuber-session), because the binaries
are partitioned by **workload family**, not by host.

The orch-coordinator's scrape model treats `worker_url` as the registration unit. There
was no UX path to add multiple worker URLs for one logical host. Operator got vtuber
working at one URL, then could not add the openai capabilities — they were on a different
binary on a different port and the coordinator had no slot for them.

Operator quotes: *"i got 1 scraper working for vtuber and when I went to add openai, i
modified the worker.yaml and realized that it wasnt advertising all the capabilities to
the scraper. then I found the problem where i need to have multiple workers and had NO way
to add them without adding multiple worker node and URL for the orch coordinator."*

`payment-daemon` accepted multiple capabilities; the bottleneck was at the
coordinator/scraper layer.

### 3.2 The wider pain — three layers of coupling

1. `work_unit` is hard-coded in `livepeer-modules`. A custom capability with a custom
   metering dimension requires a trunk PR.
2. Multiple worker binaries are required because there's no plugin/multi-workload host
   agent. Each new workload family means a new binary, a new scrape URL, a new roster
   slot.
3. The "custom worker" recipe is muddy. There is no crisp path a third-party developer
   could follow to ship a new capability without modifying `worker-runtime` and
   coordinating a livepeer-modules release.

### 3.3 The articulated requirement

> **"A workload-agnostic way of registering capabilities, payment for them, discovering
> them, and routing work to them."**

This is the pin. Everything in the proposed architecture flows from it.

---

## 4. Operator scenarios (the supply-side reality)

### 4.1 Heterogeneous backends

The orchestrator's "supply" varies in three orthogonal ways:

1. **What it is** — self-hosted models (vLLM/Ollama/SGLang containers), self-hosted
   TTS/STT, *or* a wrapper around a third-party API (OpenRouter, OpenAI, Anthropic)
   where the orch is reselling someone else's capacity using their API key.
2. **Where it lives** — single box, LAN'd across N boxes, or scattered across the
   public internet. Could be home, colo, AWS, or someone else's cloud.
3. **How it's reachable** — sometimes publicly addressable, sometimes not (NAT'd,
   private IP, only outbound).

### 4.2 Ingress is solved at the orch's edge, not in the livepeer stack

Orchs already use industry-standard tooling:
- **Cloudflare Tunnel** (`cloudflared`) — outbound-only, fronts a home-LAN service.
- **Open ports + Traefik** — public IP, classic reverse proxy.
- **Cloudflare Load Balancer + Traefik** — public LB + reverse proxy fan-out.

The advertised `worker_url` can be any public TLS endpoint that speaks the worker
protocol. The architecture does not need to solve NAT traversal or hole-punching.

### 4.3 Workload switching on existing hardware

When a new gateway brings a new work type to the network, the orch wants to **switch
existing GPU hardware** from a lesser-used capability to the new higher-demand one —
without rebuilding the worker binary.

This presupposes:
- Capabilities are swappable at runtime (workload-as-data, not workload-as-binary).
- Demand visibility exists somewhere — orch can see *what to switch toward*.

### 4.4 Low-friction operator UX

The desired operator workflow is **three declarative steps, no code**:

1. **Define** — declare capabilities offered + price (work unit + denominated value).
2. **Identify** — point at the servers/URLs/API-backed services that execute each.
3. **Serve** — flip it on; work flows; payment + usage tracked.

Implicit: no Go module written, no binary rebuilt, no trunk PR to `livepeer-modules`,
no hand-carry between hosts every time you reprice or repurpose. Restarts at most.

### 4.5 Trust anchor preserved

Orchs are deliberate about offerings and pricing. Capability/price advertisements must
be signed so they cannot be spoofed. Gateway-to-worker work delivery must be authenticated
(ticket validation). Capacity/capability probes must not be spoof-able.

The current trust spine — cold-key-signed manifest, on-chain orch identity, double
verification, ticket-validated work — must be preserved.

---

## 5. Five design forks (questions and answers)

### 5.1 Capability origination

**Q:** Who defines a capability, and where do its semantics live?

**A:** *Many shapes.* Orchs may invent capabilities. Gateways (anyone selling a service)
may originate a new capability concept and recruit orchs as the infra layer. Any
combination.

**Implication:** the registry/manifest treats `capability_id` as opaque data. Nothing in
the trust or routing layer validates *what* a capability does, only *who* offers it at
what price. Open-world strings.

### 5.2 Wire contract

**Q:** What's the wire contract between gateway and worker for a workload-agnostic request?

**A:** *No single contract.* OpenAI uses three patterns (req/resp, SSE-stream,
realtime/WebSocket); video has at least two (VOD HTTP, live RTMP-in/HLS-out); vtuber
is session-control + media plane.

**Implication:** the architecture needs a small fixed typology of **interaction modes**.
The gateway knows how to wrap auth + payment + routing for a *mode*; the capability
declares which mode it uses; the gateway never has to understand capability semantics.

### 5.3 Work-unit counting

**Q:** Who counts work units, and how does the gateway trust the count?

**A:** *Trust the orch's count in v1.* This is a permissionless network with significant
trust+risk. Verifiable approaches are desired but **out of initial scope**. Market
punishes liars over time.

**Implication:** simplifies dramatically — no cap-and-bound estimator per capability, no
signed-receipt machinery, no third-party witness. Worker computes `actualUnits` after
`Serve` and reports; gateway debits. Leave protocol hooks for verifiability later.

### 5.4 Demand visibility

**Q:** Where does the demand-visibility signal come from?

**A:** *Metrics at the edges; market view via independent scrapers.* Gateways and orchs
expose metrics surfaces. An independent third-party can scrape both sides and publish
aggregate market data publicly.

**Implication:** the architecture provides exposed surfaces (Prometheus-style endpoints,
structured logs); demand-visibility is solved out-of-band by anyone who wants to build
a dashboard.

### 5.5 Cold-key signing

**Q:** Cold-key signing — sacred or automatable?

**A:** *Sacred. Automation desired in the right flow.* Particularly for cold wallet
protection — leakage compromises the orch.

**Resolution (after pushback on warm-key proposal):** keep cold key signing every
manifest update; **secure-orch never accepts inbound connections**; operator drives the
cycle via download/sign/upload through `secure-orch-console`; friction reduction lives
in console UX (fast diff, one-click sign), not in the transport. Hand-carry stays.
Revisit automation in v2 once operator-driven flow is solid.

---

## 6. Proposed architecture (8 layers)

### 6.1 Shape in one sentence

**A single workload-agnostic process per orch host — the *capability broker* — that owns
`/registry/offerings`, dispatches paid requests over a small fixed typology of *interaction
modes* to arbitrary backends declared in YAML, with the trust spine preserved by
operator-driven, cold-key-signed manifest publication.**

### 6.2 Diagram

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

### 6.3 Layer 1 — Capability broker (replaces the three worker-node binaries)

**One process per host, workload-agnostic.** No per-capability Go code. Five jobs:

1. Read a single `host-config.yaml`.
2. Expose `GET /registry/offerings`, `GET /registry/health` (live), `GET /healthz`,
   `GET /metrics`.
3. Route inbound requests by `capability_id` → look up the **backend descriptor** →
   wrap in the declared **interaction mode** → forward → return the response.
4. Report `actualUnits` to co-located `payment-daemon` (receiver) over unix socket — same
   socket regardless of capability.
5. Optionally aggregate `/registry/offerings` from peer brokers on the LAN (operator
   choice — one outward scrape URL covering N internal hosts).

**The broker contains zero capability semantics.** It knows transports, headers, framing —
not what a "chat completion" or "doggo bark" actually is.

### 6.4 Layer 2 — Interaction-mode typology

Fixed wire contracts the broker (and gateway) understand. Capabilities pick one. Initial
set:

| Mode | Wire shape | Examples |
|---|---|---|
| `http-reqresp` | one HTTP req → one HTTP resp, JSON or binary | `openai:embeddings`, `openai:images-gen`, custom REST |
| `http-stream` | request → SSE / chunked-response stream | `openai:chat-completions` (stream) |
| `http-multipart` | multipart upload → JSON or binary response | `openai:audio-transcriptions` |
| `ws-realtime` | bidirectional WebSocket | `openai:realtime`, vtuber `/control` |
| `rtmp-ingress-hls-egress` | RTMP in → HLS manifest+segments out | `video:live.rtmp` |
| `session-control-plus-media` | HTTP session-open → long-lived media plane | `livepeer:vtuber-session` |

Each mode is implemented once in the broker (and once in the gateway). New capability
under an existing mode = zero code. New capability under a *new* mode = one mode adapter
on each side. **Modes are the only place workload knowledge is allowed to leak.**

**Code locality (where modes live):** modes are *specifications*, not a library. They
live in a docs/schema repo (see §6.11 below). Each implementer (broker, gateway, third
party) conforms to the spec; no required shared library. Reference implementations are
optional and opt-in. A conformance test suite ships with the spec.

### 6.5 Layer 3 — Declarative capability config

`host-config.yaml`:

```yaml
identity:
  orch_eth_address: 0xabc...

capabilities:
  - id: "openai:chat-completions:llama-3-70b"        # opaque string, anyone invents
    interaction_mode: "http-stream"
    work_unit:
      name: "tokens"
      extractor: { type: "openai-usage" }            # declarative recipe, no code
    price:
      amount_wei: 1500000
      per_units: 1
    backend:
      transport: "http"
      url: "http://10.0.0.5:8000/v1/chat/completions"
      auth: "none"

  - id: "openai:chat-completions:gpt-4o"             # third-party API resale
    interaction_mode: "http-stream"
    work_unit: { name: "tokens", extractor: { type: "openai-usage" } }
    price: { amount_wei: 8500000, per_units: 1 }
    backend:
      transport: "http"
      url: "https://api.openai.com/v1/chat/completions"
      auth:
        method: "bearer"
        secret_ref: "vault://openai-key"

  - id: "kibble:doggo-bark-counter:v1"               # made up by anyone
    interaction_mode: "http-reqresp"
    work_unit:
      name: "barks"
      extractor: { type: "response-jsonpath", path: "$.bark_count" }
    price: { amount_wei: 100, per_units: 1 }
    backend: { transport: "http", url: "http://192.168.1.42:8080/count" }
```

This is the entirety of the operator's day-to-day surface. Edit, reload, re-publish.
Three gestures. Zero code.

The `extractor` library is a small fixed set of recipes (`openai-usage`,
`response-jsonpath`, `request-formula`, `bytes-counted`, `seconds-elapsed`,
`ffmpeg-progress`, …) — same role as a regex library: not workload-specific but flexible
enough to count anything. Adding a new extractor is a broker change but rare.

### 6.6 Layer 4 — Discovery (workload-agnostic registry)

Two concrete changes to the existing flow:

- **Manifest data model**: a flat list of
  `(capability_id, offering_id, interaction_mode, work_unit_name, price_per_unit_wei, worker_url, eth_address, extra, constraints)`
  tuples. **Host is not a registration unit.** Multi-binary-per-host vanishes (there are
  no separate binaries); multi-broker-per-orch is just N more entries. Server-2 problem
  dies.
- **Coordinator UI**: roster is per-capability-tuple, not per-host. Operator sees "I'm
  offering 7 capabilities across 4 brokers" instead of "broker A advertises X, broker B
  advertises Y." Composing a proposal = selecting a subset of scraped tuples.

Resolver semantics keep their existing shape — `Resolver.Select(capability_id, offering_id, tier, min_weight)`
→ tuple — but the response now also carries `interaction_mode`.

### 6.7 Layer 5 — Trust spine: operator-driven sign cycle

Hard rule: **secure-orch never accepts inbound connections.** It serves only `127.0.0.1`
to its own LAN console. No automated push from coordinator. (Revisit in v2 — not now.)

**Operator-driven cycle:**

1. Operator edits `host-config.yaml` on broker host(s).
2. Broker re-advertises locally; orch-coordinator scrapes; coordinator builds candidate
   manifest and exposes it for download.
3. **Operator** pulls the candidate to secure-orch (download via secure-orch-console UI
   fetching it, scp, USB stick — operator's choice; architecture supports any).
4. `secure-orch-console` shows a **diff** of candidate vs. currently-published manifest.
   Operator inspects, taps to sign. Cold key (HSM-backed, never moves) signs.
5. Operator pushes signed manifest back to coordinator (upload button, scp, USB stick).
6. Coordinator atomic-swap publishes.

**Friction reduction is in the *console UX*, not in the transport.** Fast diff,
one-click sign, clear status. Hand-carry stays.

### 6.8 Layer 6 — Payment

`payment-daemon` keeps its shape. The one decoupling: **the daemon stops enforcing a
closed enum of capability/work-unit names**. Both become opaque strings; daemon does the
arithmetic `price_wei = price_per_unit_wei × actualUnits`. Custom capabilities with custom
work units (`barks`, `pixel-seconds`, anything) work without trunk changes.

The `Livepeer-Payment` header gains `(capability_id, offering_id, expected_max_units)`
so the receiver can refuse mismatched routing.

### 6.9 Layer 7 — Routing (gateway side)

- Gateway resolves a route → gets the tuple including `interaction_mode`.
- Picks the matching mode adapter (req/resp, stream, ws, RTMP, session) — generic across
  capabilities.
- Wraps with `Authorization` (customer's bearer), `Livepeer-Payment` (ticket from sender
  daemon), `Livepeer-Capability: <id>`, `Livepeer-Offering: <id>`, opens transport,
  forwards.
- For session/stream/realtime: payment is amortized (`OpenSession + periodic Debit + CloseSession`).

**Gateway code is per-mode, not per-capability.** Brand-new capability under an existing
mode lights up automatically once the manifest carries it.

### 6.10 Layer 8 — Demand visibility (out-of-band, by design)

Every component exposes Prometheus on a documented schema:

- Counters: `livepeer_routes_total{capability,offering,outcome}`
- Histograms: `livepeer_price_paid_wei{capability}`
- Gauges: `livepeer_capacity_available{capability}`

Gateways and orchs publish their own metrics on signed endpoints. Independent third
parties scrape both sides and publish a public market data feed. Architecture provides
surfaces; aggregation is third-party.

### 6.11 The spec repo (replacement for the dead `livepeer-modules-conventions`)

Proposed: a single repo named **`livepeer-network-protocol`** (working name). Not a code
dependency. Layout:

```
livepeer-network-protocol/
├── README.md
├── manifest/
│   ├── schema.json                # JSON Schema for the manifest
│   ├── examples/
│   └── changelog.md
├── modes/
│   ├── http-reqresp.md
│   ├── http-stream.md
│   ├── http-multipart.md
│   ├── ws-realtime.md
│   ├── rtmp-ingress-hls-egress.md
│   ├── session-control-plus-media.md
│   └── _template.md               # for new mode PRs
├── extractors/
│   ├── openai-usage.md
│   ├── response-jsonpath.md
│   ├── request-formula.md
│   ├── bytes-counted.md
│   ├── seconds-elapsed.md
│   └── ffmpeg-progress.md
├── headers/
│   └── livepeer-headers.md
├── metrics/
│   └── conventions.md
├── conformance/
│   ├── broker-tests/
│   └── gateway-tests/
├── reference-impls/               # all OPTIONAL
│   ├── go-broker-middleware/
│   └── ts-gateway-middleware/
└── PROCESS.md
```

Properties: language-neutral, versioned (SemVer per mode), conformance tests as the
trust mechanism, optional reference impls in subdirectories.

---

## 7. Pushbacks and revisions

### 7.1 Layer 5 — secure-orch is egress-only (revised from initial sketch)

Original proposal had an automated mTLS push from coordinator → secure-orch with
hardware-confirm. **Rejected.** Revised: secure-orch never accepts inbound connections;
operator drives the cycle (see §6.7).

### 7.2 Layer 2 — code locality clarified

Original sketch was ambiguous about where mode code lives. Clarified: modes are *specs*,
not libraries. See §6.4 and §6.11.

### 7.3 Streaming/realtime payment cadence — v1

Lift cadence into mode definitions with sensible defaults; carry over what already works:

- `session-control-plus-media` (vtuber): default debit cadence **5 seconds**, runway
  minimum **3 ticks**, grace window **2 ticks**. Carries over today's vtuber-worker
  behavior.
- `rtmp-ingress-hls-egress` (video live): same default cadence; units measured in
  `video-frame-megapixel` per tick.
- Operator can override cadence per offering, but most won't.
- One-shot modes (`http-reqresp`, `http-stream`, `http-multipart`): single debit at
  request end based on extracted units. No cadence needed.

The mode owns the *shape* of the payment lifecycle; the offering owns the *parameters*.

### 7.4 Capacity declaration — drop it

`capacity` is gameable and meaningless cross-workload. **Removed from the manifest tuple.**

Replacement:
- Broker returns **HTTP 503 + `Livepeer-Backoff: <seconds>`** when its backend is full.
- Gateway resolver tracks per-`(orch, capability)` recent-failure-rate; weights down or
  blacklists briefly (~30s).
- Operators may set a self-imposed local concurrency cap in `host-config.yaml`, but it's
  local enforcement, not advertised.

### 7.5 Custom interaction modes — governance

The mode catalog is the one place workload knowledge lives, so it must be governed but
not centrally controlled.

- Modes are versioned specs in `livepeer-network-protocol`. Anyone can submit a PR with
  a new mode (rationale, wire spec, conformance tests).
- Acceptance criteria: at least one demonstrable use case + at least one independent
  reviewer agrees the mode is meaningfully distinct from existing ones.
- Gateways implement modes opt-in. A gateway is free to support only the modes it cares
  about. Capabilities advertise their mode; gateways unaware of that mode simply don't
  route those capabilities.
- Market dynamics resolve everything else.

For v1: ship the 6 modes from §6.4. Accept community PRs after that.

### 7.6 Verifiability hooks — slot reserved

Confirmed: extractor library leaves a `signed_by` slot in the schema (initially empty).
When v2 adds verifiability (e.g., signed OpenAI usage receipts), an extractor can declare
`signed_by: "openai-usage-receipt-v1"` and the gateway can validate before debiting.
Schema-forward-compatible from day one.

### 7.7 `extra` and `constraints` — workload-specific routing metadata

Multi-region routing, latency tier, customer allowlists, GPU-class preferences — all
live in **`extra` (orch-declared metadata)** and **`constraints` (gateway-side filters)**
on the resolver tuple. Protocol stays workload-agnostic; gateway gets all the workload-
specific routing it wants.

`Resolver.Select(capability_id, offering_id, filter_fn)` where `filter_fn` is
gateway-side code examining `extra`.

### 7.8 Backend health — three-layer model

Cleaner separation:

1. **Manifest = the menu.** Signed by cold key. Slow-changing. Declares "this orch *can*
   serve these capabilities at these prices."
2. **`GET /registry/health` on the broker = what's currently live.** Unsigned, fresh,
   returns currently-available capability IDs (subset of manifest) + a `Livepeer-Backoff`
   hint if any are tight. Polled by gateway resolver every 15-30s.
3. **Gateway-side failure-rate tracking.** If health-says-up but routing-says-503,
   gateway weights the orch down for ~30s.

Manifest signing stays rare (only when *offerings* change). Liveness is a fast unsigned
signal. Operator never has to re-sign for a backend hiccup.

---

## 8. Final running list of supply-side requirements

1. **Workload-agnostic register / pay / discover / route** (the pin).
2. **Heterogeneous backends + topologies** — ingress at orch's edge (cloudflared / Traefik / CF-LB).
3. **Capability swappable at runtime** — workload-as-data, not workload-as-binary.
4. **Demand visibility** — orch sees what to switch toward (via metrics scrapers).
5. **Three-step, code-free, declarative operator workflow** — define → identify → serve.
6. **Trust anchor preserved** — signed advertisements, gateway verifies, ticket-validated work.
7. **Open-world capability IDs** — opaque strings; no canonical schema registry.
8. **Typology of interaction modes** — small fixed set; capabilities self-declare; gateways understand modes, not capability semantics.
9. **v1 trusts orch-reported usage** — verifiability deferred; design for honest count, leave hooks.
10. **Metrics exposed at edges; market data via independent scrapers** — architecture publishes surfaces, doesn't aggregate.
11. **Cold key + operator-driven sign cycle** — secure-orch egress-only; cold key signs every change; friction reduction lives in console UX.

---

## 9. Open items / v2 deferments

1. **Verifiability of work-unit reports** — slot reserved in extractor schema; not
   implemented.
2. **Streaming payment cadence parameters** — defaults baked into mode; per-offering
   override exposed but rarely used. Revisit if real-world usage demands richer
   knobs.
3. **Custom interaction modes beyond the initial 6** — governance via spec PRs;
   community-driven.
4. **Live voice-in ASR** — current livepeer-byoc audio runners are blocking multipart;
   a streaming-ASR mode is a future addition.
5. **Recording/DVR for streams** — tee trickle to object storage? v2.
6. **Multi-destination simulcast** — YouTube + Twitch + X via ffmpeg tee. v2.
7. **Conversation/session state persistence across orch swap** — Redis-backed agent
   memory vs. accept-restart. v2.
8. **Pipeline-side architecture** (API surface, persona schema, OAuth, egress internals,
   chat-source fan-in, billing) — out of scope for the network rewrite.
9. **Automated transport for the sign cycle** — explicitly deferred per §7.1.
10. **Warm key as a power-user opt-in** — recommended path stays cold-only.

---

## 10. Migration impact (the suite at-a-glance)

### 10.1 What this kills / changes

| Today | Tomorrow |
|---|---|
| `openai-worker-node`, `vtuber-worker-node`, `video-worker-node` (3 binaries) | One `livepeer-capability-broker` binary |
| Per-capability Go `Module` impls in `worker-runtime` | Mode adapters + extractor library; no per-capability code |
| `worker.yaml` schema with hardcoded work-unit enum | `host-config.yaml` with opaque capability/work-unit strings |
| `payment-daemon` cross-checks against baked catalog | Daemon treats names as opaque, only does arithmetic |
| Manifest schema is host-rooted | Manifest is a flat list of capability tuples |
| Coordinator UX models worker-as-roster-entry | Coordinator UX models capability-as-roster-entry |
| Hand-carry of signed manifest between hosts | Hand-carry stays; UX is fast diff + one-click sign + clear status |
| Gateway has per-capability code paths | Gateway has per-mode adapters; capabilities are opaque strings |
| `livepeer-modules-conventions` (referenced, doesn't exist) | `livepeer-network-protocol` — language-neutral spec repo |

### 10.2 What stays sacred

- Cold orch keystore on firewalled `secure-orch`. Never moves.
- Cold-key signature on every manifest publication.
- Double-verification of signed manifest (coordinator on upload, resolver on fetch).
- On-chain orch identity (`ServiceRegistry`) is the public anchor.
- `payment-daemon`'s ticket validation against chain.
- Mainnet-only, image-tags-not-bumped, and the rest of the suite's core beliefs.

---

## 11. Provenance

- Conversation date: 2026-05-06.
- Roles: user (orchestrator-operator perspective), assistant (architectural sounding
  board / synthesizer).
- Repos read during the conversation:
  - `livepeer-cloud-spe/openai-worker-node`
  - `livepeer-cloud-spe/vtuber-worker-node`
  - `livepeer-cloud-spe/video-worker-node`
  - `livepeer-cloud-spe/livepeer-modules-project`
  - `livepeer-cloud-spe/livepeer-network-suite` (top-level only, no submodule descent)
- Memories carried into the conversation: `project_byoc_vtuber`, `reference_livepeer_byoc`,
  `reference_byoc_capability_names`, `feedback_no_byoc_term`, `feedback_no_bridge_term`.
- Output: this document plus the project scaffold at
  `livepeer-cloud-spe/livepeer-network-rewrite/`.

This document is a **point-in-time reference**. As design-docs in `docs/design-docs/`
mature, they may supersede individual sections. Do not edit this file to reflect later
changes; create a new dated reference instead.

---
title: Pricing overview
status: active
audience: operators, gateway authors, payment-daemon contributors
---

# Pricing overview

End-to-end map of how the Livepeer network prices work in this repo. It
synthesises material already covered piecemeal in
[`payment-decoupling.md`](./payment-decoupling.md),
[`payment-daemon-interactions.md`](./payment-daemon-interactions.md), and
[`architecture-overview.md`](./architecture-overview.md), and replaces the
older `livepeer-modules-project` view (worker.yaml + `KnownWorkUnits` enum +
suite-style payee catalog) with the current model.

This doc cites concrete file paths and line numbers so a reader can jump
straight to the code that implements each stage.

## The model in one paragraph

Capabilities, offerings, and work-unit names are **opaque UTF-8 strings**.
The payment-daemon performs one arithmetic operation:

```
final_price_wei = price_per_work_unit_wei × actualUnits
```

The orchestrator publishes `price_per_work_unit_wei` in `host-config.yaml`;
the broker advertises it in a signed off-chain manifest pointed to by an
on-chain `serviceURI`; the gateway discovers it via the service-registry
resolver; a probabilistic-micropayment ticket carries the wei the gateway
promises to spend; the receiver freezes the price into the session at open
time; the broker extracts `actualUnits` from the response; the receiver
debits `price × units` from the session balance. Winning tickets are queued
and redeemed on-chain through `TicketBroker`.

See [`payment-decoupling.md`](./payment-decoupling.md) for *why* the daemon
no longer enforces a closed enum of names, and
[`payment-daemon-interactions.md`](./payment-daemon-interactions.md) for the
sender/receiver split.

## Where pricing lives in the tree

| Concern | Path |
|---|---|
| Operator config grammar | [`capability-broker/internal/config/config.go`](../../capability-broker/internal/config/config.go) |
| Example host-config | [`capability-broker/examples/host-config.example.yaml`](../../capability-broker/examples/host-config.example.yaml) |
| Manifest schema | [`livepeer-network-protocol/manifest/schema.json`](../../livepeer-network-protocol/manifest/schema.json) |
| Registry proto (advertised price) | [`proto-contracts/livepeer/registry/v1/types.proto`](../../proto-contracts/livepeer/registry/v1/types.proto) |
| Resolver proto (discovered price) | [`proto-contracts/livepeer/registry/v1/resolver.proto`](../../proto-contracts/livepeer/registry/v1/resolver.proto) |
| Selection algorithm | [`service-registry-daemon/internal/service/selection/selection.go`](../../service-registry-daemon/internal/service/selection/selection.go) |
| Wire headers | [`livepeer-network-protocol/headers/livepeer-headers.md`](../../livepeer-network-protocol/headers/livepeer-headers.md) |
| Payee RPC surface | [`livepeer-network-protocol/proto/livepeer/payments/v1/payee_daemon.proto`](../../livepeer-network-protocol/proto/livepeer/payments/v1/payee_daemon.proto) |
| Payer RPC surface | [`livepeer-network-protocol/proto/livepeer/payments/v1/payer_daemon.proto`](../../livepeer-network-protocol/proto/livepeer/payments/v1/payer_daemon.proto) |
| Wire-compat payment types | [`proto-contracts/livepeer/payments/v1/types.proto`](../../proto-contracts/livepeer/payments/v1/types.proto) |
| Sender (payer) daemon | [`payment-daemon/internal/service/sender/sender.go`](../../payment-daemon/internal/service/sender/sender.go) |
| Receiver (payee) daemon | [`payment-daemon/internal/service/receiver/receiver.go`](../../payment-daemon/internal/service/receiver/receiver.go) |
| Session + balance store | [`payment-daemon/internal/store/store.go`](../../payment-daemon/internal/store/store.go) |
| Interaction modes | [`livepeer-network-protocol/modes/`](../../livepeer-network-protocol/modes/) |
| Work-unit extractors | [`livepeer-network-protocol/extractors/`](../../livepeer-network-protocol/extractors/) |
| Pool accounting docs | [`pool-controller/`](../../pool-controller/), [`pool-reconciler/`](../../pool-reconciler/), [`pool-payout-executor/`](../../pool-payout-executor/) |

## End-to-end pricing flow

### 1. The operator declares the price

A capability entry in `host-config.yaml` declares its retail price as a
wei-denominated ratio plus an opaque work-unit name. Example from
[`host-config.example.yaml:45-67`](../../capability-broker/examples/host-config.example.yaml):

```yaml
capabilities:
  - id: "kibble:doggo-bark-counter:v1"
    offering_id: "default"
    interaction_mode: "http-reqresp@v0"
    work_unit:
      name: "barks"
      extractor: { type: "response-jsonpath", path: "$.bark_count" }
    price:
      amount_wei: "100"
      per_units: 1
```

The Go grammar is declared in
[`capability-broker/internal/config/config.go`](../../capability-broker/internal/config/config.go):

- `Capability` struct — `config.go:43-54`
- `WorkUnit` struct (name + extractor map) — `config.go:87-90`
- `Price` struct (`amount_wei` as decimal string, `per_units` as uint64) —
  `config.go:94-97`

The string form for `amount_wei` is deliberate — it preserves precision
beyond JSON's safe-integer range (per the manifest schema).

### 2. Manifest publishing — signed, off-chain, pointed to on-chain

The broker advertises its priced capabilities to the orch-coordinator, which
builds a manifest, the service-registry-daemon signs it with the orch cold
key, and the coordinator hosts it at the on-chain `serviceURI`.

- Manifest JSON schema — [`livepeer-network-protocol/manifest/schema.json`](../../livepeer-network-protocol/manifest/schema.json)
  - Each capability tuple has `capability_id`, `offering_id`,
    `price_per_unit_wei`, `work_unit_name` (line ~71-100)
- Signing path — [`service-registry-daemon/`](../../service-registry-daemon/)
  publisher and trust-model:
  [`docs/design-docs/trust-model.md`](./trust-model.md)
- On-chain footprint is one URI pointer — pricing itself stays off-chain
  ([`architecture-overview.md`](./architecture-overview.md))

### 3. Discovery — gateway resolves a route plus a price

Gateways call `Resolver.Select` and get back a `SelectedRoute` carrying the
price and work-unit metadata for the chosen orch/worker.

Proto — [`proto-contracts/livepeer/registry/v1/resolver.proto`](../../proto-contracts/livepeer/registry/v1/resolver.proto):

- `rpc Select(SelectRequest) returns (SelectResult)` — `resolver.proto:13`
- `SelectRequest{capability, offering, tier, min_weight}` — `resolver.proto:38-44`
- `SelectedRoute{worker_url, eth_address, capability, offering,
  price_per_work_unit_wei, work_unit, ...}` — `resolver.proto:49-58`

Selection logic —
[`service-registry-daemon/internal/service/selection/selection.go`](../../service-registry-daemon/internal/service/selection/selection.go):

- `Filter{Capability, Offering, Tier, MinWeight, ...}` — `selection.go:19-25`
- `Apply` — conjunctive match + stable sort by `Weight` descending —
  `selection.go:33-44`

### 4. Gateway computes target spend, then mints a payment

Once the gateway has `price_per_work_unit_wei` and an estimated
`target_units`, it computes the wei it is willing to spend on this request:

```
requested_face_value_wei = target_units × price_per_work_unit_wei
```

(See [`payment-daemon-interactions.md:108-128`](./payment-daemon-interactions.md).)

The gateway calls `PayerDaemon.CreatePayment` —
[`proto-contracts/livepeer/payments/v1/payer_daemon.proto`](../../proto-contracts/livepeer/payments/v1/payer_daemon.proto)
and the implementation
[`payment-daemon/internal/service/sender/sender.go:69-150`](../../payment-daemon/internal/service/sender/sender.go).
The sender:

1. Validates inputs and queries the broker for sender deposit/reserve state
   (`sender.go:90-99`).
2. Finds or opens a cached session keyed by `(recipient, capability,
   offering, target spend, ticket-params base URL)` (`sender.go:101-111`).
3. Fetches authoritative `TicketParams` from the broker over HTTP at
   `/v1/payment/ticket-params` (`sender.go:5-10` package comment, plus
   `findOrOpenSession` at `sender.go:177-205`). The receiver decides the
   actual ticket `FaceValue` and `WinProb` so that
   `FaceValue × WinProb ≈ requested spend` — see
   [`payment-daemon-interactions.md:182-216`](./payment-daemon-interactions.md).
4. Signs one ticket (`signOneTicket` at `sender.go:218-243`) and marshals a
   wire-format `Payment` (`sender.go:118-132`).

The crucial split: **the gateway requests a target spend; the receiver
chooses redeemable ticket economics that match it in expectation.** Retail
price comes from host-config; ticket face value comes from receiver runtime
economics.

### 5. Wire surface — Livepeer-* HTTP headers

The gateway sends the request to the broker with three pricing-relevant
headers — see
[`livepeer-network-protocol/headers/livepeer-headers.md:28-86`](../../livepeer-network-protocol/headers/livepeer-headers.md):

| Header | Direction | Carries |
|---|---|---|
| `Livepeer-Capability` | request → broker | capability id (opaque string), `livepeer-headers.md:45-53` |
| `Livepeer-Offering` | request → broker | offering id (opaque string), `livepeer-headers.md:55-63` |
| `Livepeer-Payment` | request → broker | base64 protobuf `Payment` envelope, `livepeer-headers.md:65-86` |
| `Livepeer-Mode` | request → broker | interaction mode id, e.g. `http-stream@v1` |
| `Livepeer-Work-Units` | response from broker | `actualUnits` after extraction, `livepeer-headers.md:132-…` |
| `Livepeer-Error` | response on error | machine-readable code such as `insufficient_balance` |

The envelope's `capability_id` and `offering_id` MUST match the headers
(`livepeer-headers.md:74-84`). Mismatch ⇒ broker rejects with
`Livepeer-Error: payment_invalid`.

### 6. Receiver validates the ticket and credits EV

The broker passes the inbound `Payment` to the local
`PayeeDaemon.ProcessPayment` —
[`payment-daemon/internal/service/receiver/receiver.go:139-214`](../../payment-daemon/internal/service/receiver/receiver.go).
This step does *not* debit; it credits EV based on the ticket's expected
value:

```
EV = face_value × win_prob / 2^256
```

(Receiver code at `receiver.go:222-304`.)

Winning tickets (hash check passes inside `validateAndCredit`) are queued
for on-chain redemption via `store.EnqueueRedemption` and the redemption
buckets in [`payment-daemon/internal/store/store.go`](../../payment-daemon/internal/store/store.go).

Before `ProcessPayment` will accept a payment for a given `work_id`, the
broker must have called `OpenSession` —
`receiver.go:85-133`. That call writes the priced binding:

```go
s.store.OpenSession(store.Session{
    WorkID:              req.GetWorkId(),
    Capability:          req.GetCapability(),
    Offering:            req.GetOffering(),
    PricePerWorkUnitWei: priceWei.String(),   // frozen for the session
    WorkUnit:            req.GetWorkUnit(),
    ...
})
```

(`receiver.go:108-117`.) Once stored, the price is immutable for the life of
the session — see the `Session` struct in
[`store.go:47-71`](../../payment-daemon/internal/store/store.go).

### 7. Backend runs in an interaction mode

The interaction mode controls *when* and *how* work units are reported.
Specs in [`livepeer-network-protocol/modes/`](../../livepeer-network-protocol/modes/):

- `http-reqresp@v0` — one request, one response, post-response extraction
- `http-stream@v0` — chunked/SSE response, extraction at stream end
- `http-multipart@v0` — multipart upload, post-response extraction
- `ws-realtime@v0` — bidirectional WebSocket, per-cadence debit
- `rtmp-ingress-hls-egress@v0` — RTMP in, HLS out, session-metered
- `session-control-plus-media@v0` — broker-managed media plane
- `session-control-external-media@v0` — broker reverse-proxies to external media

Pricing math is identical across modes; modes only differ in *when* the
broker can extract `actualUnits` and call `DebitBalance`.

### 8. Extractor produces `actualUnits`

The work-unit name (`barks`, `tokens`, `frames`, …) is opaque; the recipe
that produces the integer count is declarative. Recipes are in
[`livepeer-network-protocol/extractors/`](../../livepeer-network-protocol/extractors/):

| Recipe | Reads | Emits |
|---|---|---|
| `openai-usage` | OpenAI-style `usage.*_tokens` from response JSON | token count |
| `response-jsonpath` | Arbitrary JSONPath over response body | integer |
| `request-formula` | Safe arithmetic over request fields (e.g. `width × height × steps`) | integer |
| `bytes-counted` | Byte length of request, response, or both | bytes |
| `seconds-elapsed` | Wall-clock duration between mode-defined anchors | seconds |
| `ffmpeg-progress` | Parsed FFmpeg `-progress` stream | frames or out-time |

The broker ships the extractor implementations; the operator only references
them by `type` and parameters in `work_unit.extractor`. Adding a new
extractor type is a broker code change; tuning parameters is YAML-only.

### 9. Broker debits the balance — the canonical formula

After extraction the broker calls `PayeeDaemon.DebitBalance` —
`receiver.go:308-323`. The receiver looks up the session, multiplies its
frozen price by the reported units, and subtracts from the balance.
[`payment-daemon/internal/store/store.go:288-294`](../../payment-daemon/internal/store/store.go):

```go
price := parseDecimalBig(sess.PricePerWorkUnitWei)
debitWei := new(big.Int).Mul(price, big.NewInt(workUnits))
bal := parseDecimalBig(sess.BalanceWei)
bal.Sub(bal, debitWei)
sess.BalanceWei = bal.String()
```

That is *the* pricing formula in the running code. Everything upstream is
plumbing to deliver the three values (`price`, `workUnits`, `balance`) to
this multiplication.

`DebitBalance` is idempotent on `(sender, work_id, debit_seq)` — repeats
return the post-debit balance without re-applying
(`store.go:248-308`). `SufficientBalance` at `receiver.go:327` answers
"can the session afford another `min_work_units` at the frozen price?"
without mutating state (formula at `receiver.go:342`).

### 10. Settlement — winning tickets redeemed on-chain

Winning tickets identified during `ProcessPayment` are persisted to the
`redemptions_pending` bucket
([`payment-daemon/internal/store/store.go`](../../payment-daemon/internal/store/store.go)
and [`redemptions.go`](../../payment-daemon/internal/store/redemptions.go)).
A settlement worker drains them and submits to the on-chain `TicketBroker`
contract on Arbitrum One. Per-round revenue is queryable via
`GetRoundRevenue` (`receiver.go:515-528`), which sums confirmed redemptions
only — EV credits are off-chain accounting; revenue is the on-chain truth.

Redemption status lifecycle (queued → submitted → confirmed → failed) is
exposed via `GetRedemptionStatus` (`receiver.go:481-511`).

### 11. Pool accounting — work receipts

The broker emits two receipts to the pool-controller's admin API
(configured under `receipt_sink:` in host-config —
[`config.go:37-41`](../../capability-broker/internal/config/config.go)):

- **stub receipt** — issued before work, with the priced context
  (`capability_id`, `offering_id`, `work_unit`, `price_per_work_unit_wei`)
- **final receipt** — issued after extraction, with `actualUnits` and
  `credited_wei = price × actualUnits`

Pool components consuming these:

- `pool-controller` — owns member records, persists receipts, builds round
  receipts and payout intents
- `pool-reconciler` — closes rounds using `protocol-daemon` round timing,
  `payment-daemon.GetRoundRevenue` for realised revenue, and
  `pool-controller` receipts
- `pool-payout-executor` — sends ETH payouts on Arbitrum One

Receipts preserve every priced field so an operator can audit "what was
charged, for what, at what published price."

## Wire surface — key messages

The most load-bearing proto messages, all using string fields (no enums) for
capability/offering/work-unit:

### Registry — what the network advertises

[`proto-contracts/livepeer/registry/v1/types.proto`](../../proto-contracts/livepeer/registry/v1/types.proto):

```proto
message Capability {
  string             name       = 1;     // line 43
  string             work_unit  = 2;     // line 44
  repeated Offering  offerings  = 3;     // line 45
}

message Offering {
  string id                       = 1;   // line 57
  string price_per_work_unit_wei  = 2;   // line 58 — decimal big-int as string
}
```

### Resolver — what the gateway sees

[`proto-contracts/livepeer/registry/v1/resolver.proto:49-58`](../../proto-contracts/livepeer/registry/v1/resolver.proto):

```proto
message SelectedRoute {
  string worker_url               = 1;
  string eth_address              = 2;
  string capability               = 3;
  string offering                 = 4;
  string price_per_work_unit_wei  = 5;
  string work_unit                = 6;
  bytes  extra_json               = 7;
  bytes  constraints_json         = 8;
}
```

### Payee RPC — what the broker calls

[`livepeer-network-protocol/proto/livepeer/payments/v1/payee_daemon.proto`](../../livepeer-network-protocol/proto/livepeer/payments/v1/payee_daemon.proto):

- `OpenSession(work_id, capability, offering, price_per_work_unit_wei, work_unit)` — RPC at line 47, request at line 156-171. Freezes the price for the session.
- `ProcessPayment(...)` — RPC at line 52. Credits EV.
- `DebitBalance(sender, work_id, work_units, debit_seq)` — RPC at line 58, request at line 210-220. Applies `price × units`. **No price field — uses the frozen one.**
- `SufficientBalance(sender, work_id, min_work_units)` — RPC at line 62. Pre-flight affordability check.
- `GetTicketParams(sender, capability, offering, face_value, recipient)` — RPC at line 38. Canonical sender-bootstrap call; receiver implementation at `receiver.go:397-448`.
- `CloseSession`, `GetBalance`, `ListPendingRedemptions`, `GetRedemptionStatus`, `GetRoundRevenue` — operational.

### Payer RPC — what the gateway calls

[`livepeer-network-protocol/proto/livepeer/payments/v1/payer_daemon.proto`](../../livepeer-network-protocol/proto/livepeer/payments/v1/payer_daemon.proto):

- `CreatePayment(face_value, recipient, capability, offering, expected_max_units, ticket_params_base_url)` — implemented at `sender.go:69-150`.

### Wire-compat payment types

[`proto-contracts/livepeer/payments/v1/types.proto`](../../proto-contracts/livepeer/payments/v1/types.proto)
preserves byte-for-byte wire compatibility with go-livepeer's
`net/lp_rpc.proto`. Field *names* differ
(`price_per_unit` vs. `pricePerUnit`); field *numbers* are identical, so the
encoding stays cross-implementation compatible (see header comment lines
1-9). The same messages live a second time under
`livepeer-network-protocol/proto/livepeer/payments/v1/` for the in-tree Go
packages.

## Retail price vs. acceptance floor

These are different knobs and they answer different questions.
[`payment-daemon-interactions.md:218-248`](./payment-daemon-interactions.md)
covers this in detail; the short version:

- **Retail price** — what gateways charge end users — comes from
  `host-config.yaml` (`capabilities[].price.amount_wei`,
  `capabilities[].price.per_units`, `capabilities[].work_unit.name`).
- **Acceptance floor** — whether a small requested spend can be turned into
  a redeemable winning-ticket — comes from receiver runtime economics
  (`--receiver-ev`, `--redeem-gas`, gas price, sender reserve / `MaxFloat`).

Lowering the YAML price does not make small requests succeed; it only
changes what customers are charged. If small requests fail the operator
should look at acceptance-floor knobs, not retail price.

## What pricing is *not* (in this repo)

To pre-empt confusion from the older `livepeer-modules-project` codebase:

- **No `KnownWorkUnits` enum.** Work-unit names are opaque strings on the
  wire and in storage. See [`payment-decoupling.md`](./payment-decoupling.md)
  for the rationale. The Go session record stores them as `string`
  (`store.go:47-71`).
- **No `worker.yaml`.** The broker-facing operator surface is either the
  standalone broker `host-config.yaml` or, in a pool-managed rollout, the
  `pool-controller` control plane that derives broker runtime from persisted
  state.
- **No catalog-driven `GetQuote` for senders.** Per-offering pricing is a
  future plan; the receiver's `GetQuote` returns `Unimplemented`
  (`receiver.go:451-453`) and `ListCapabilities` returns an empty catalog
  (`receiver.go:457-459`). The canonical sender-bootstrap call is
  `GetTicketParams` (`receiver.go:397-448`), fetched over HTTP from the
  broker's `/v1/payment/ticket-params` endpoint.
- **No on-chain pricing.** The chain stores only a `serviceURI` pointer
  plus the `TicketBroker` redemption ledger. The price itself lives in the
  signed off-chain manifest.

## Glossary

- **capability** — opaque string identifying a unit of work the broker can
  perform (e.g. `kibble:doggo-bark-counter:v1`)
- **offering** — opaque string identifying a priced tier within a capability
  (e.g. `default`, `gpt-oss-20b`)
- **work unit** — opaque string naming the metering dimension
  (e.g. `barks`, `tokens`, `frames`)
- **`price_per_work_unit_wei`** — published retail price as a decimal
  big-integer in wei per work unit
- **`actualUnits`** — integer count produced by the declared extractor at
  request completion
- **`face_value`** — wei per redeemed winning ticket; chosen by the
  receiver, not the gateway
- **`win_prob`** — probability a ticket is winning; chosen by the receiver
  so that `face_value × win_prob ≈ gateway's requested spend`
- **EV** — expected value, `face_value × win_prob / 2^256`; the wei amount
  credited to the off-chain balance per ticket

## Related docs

- [`payment-decoupling.md`](./payment-decoupling.md) — why capability and
  work-unit names are opaque strings
- [`payment-daemon-interactions.md`](./payment-daemon-interactions.md) —
  sender/receiver interaction model, requested-spend vs winning-ticket
  face-value
- [`architecture-overview.md`](./architecture-overview.md) — the 8-layer
  architecture; pricing sits across layers 3-6
- [`interaction-modes.md`](./interaction-modes.md) — full mode catalogue
- [`streaming-workload-pattern.md`](./streaming-workload-pattern.md) —
  long-lived session blueprint for streaming modes
- [`trust-model.md`](./trust-model.md) — manifest signing, cold key
- [`migration-from-suite.md`](./migration-from-suite.md) — component map
  from the older suite

---

# Part 2 — Economics: wholesale pricing, profitability, and fair compensation

The first half of this doc is mechanism. This half is economics: how an
orchestrator decides what wholesale price to publish, how a gateway decides
whether to route to that orch, and how each side reasons about
profitability. None of the content below is enforced by the daemon — the
daemon does `price × units` arithmetic and nothing else. The economics live
in operator decisions and gateway-side billing code.

## Two pricing layers: wholesale (wei) and retail (USD)

```
┌──────────────────┐    USD/unit     ┌─────────────────┐    wei/unit     ┌──────────────────┐
│ End customer     │ ──────────────► │ Gateway service │ ──────────────► │ Orchestrator     │
│ (OpenAI-style    │   retail price  │ (openai-gateway,│   wholesale     │ (capability-     │
│  API caller)     │ ◄────────────── │  video-gateway, │   price         │  broker +        │
│                  │   invoice/usage │  vtuber-gateway,│ ◄────────────── │  payment-daemon  │
│                  │                 │  customer-portal)│ wei debit       │  receiver mode)  │
└──────────────────┘                 └─────────────────┘                 └──────────────────┘
                                                ▲
                                                │ FX boundary lives here:
                                                │  USD ←→ wei conversion
                                                │  customer billing margin
                                                │  retail markup policy
```

**Wholesale layer** — gateway ↔ orchestrator — is **wei only**. Everything
in Part 1 of this doc operates here: `price_per_work_unit_wei` is the
orchestrator's wholesale advertised price, denominated in wei. No
stablecoin, no USD field on the wire, no oracle inside the daemon.

**Retail layer** — customer ↔ gateway — is **USD** (or whatever the
gateway's customer-facing app supports). This is where Replicate/Mux-style
"$0.024 per minute" or "$5 per million tokens" pricing lives. Retail
billing is implemented in the customer-facing gateway services
(`customer-portal/`, `openai-gateway/src/service/chatBilling.ts`, the video
and vtuber gateways), not in the wholesale daemons.

**The conversion boundary lives inside the gateway service**, not in the
payment-daemon. The gateway service:

1. Quotes the customer in USD (retail).
2. Looks up the resolved orch's wholesale `price_per_work_unit_wei`.
3. Computes the wei face value it is willing to spend
   (`target_units × price_per_work_unit_wei`).
4. Calls `PayerDaemon.CreatePayment(face_value, ...)`.

The daemon never sees USD. The gateway operator is responsible for the FX
rate it uses when bridging. There is no daemon-level ETH/USD oracle and no
protocol-level enforcement that retail and wholesale stay aligned.

This layering is deliberate: it lets multiple gateways with different
billing models (consumption-based, subscription, prepaid credit, free tier
with abuse limits) sit on top of one homogeneous wholesale market.

## How orchestrators and gateways converge on a fair wholesale price

**There is no negotiation primitive in the protocol.** The orchestrator
publishes a take-it-or-leave-it `price_per_work_unit_wei` in the signed
manifest. The gateway either resolves to that orch and accepts the price,
or it shops a different orch via `Resolver.Select`. Fairness emerges from
market discovery, not from a handshake.

### Where each side influences the wholesale price

**Orchestrator-side knobs** (host-config.yaml,
[`capability-broker/internal/config/config.go:43-97`](../../capability-broker/internal/config/config.go)):

| Knob | Lever |
|---|---|
| `capabilities[].price.amount_wei` | the advertised wholesale price |
| `capabilities[].price.per_units` | denominator (charge per 1000 tokens vs per 1 token) |
| `capabilities[].work_unit.name` | metering dimension chosen for this offering |
| `capabilities[].work_unit.extractor` | how `actualUnits` is computed (extractor recipe) |
| presence / absence of `capabilities[]` entry | the orch's accepted demand |
| capability `health.drain.enabled` | back-pressure when oversubscribed |
| receiver runtime economics (`--receiver-ev`, `--redeem-gas`, etc.) | acceptance floor — see Part 1 |

**Gateway-side knobs** (resolver client policy + customer-facing app):

| Knob | Lever |
|---|---|
| `SelectRequest{capability, offering, tier, min_weight}` filters | which orchs are eligible |
| gateway operator's preferred-orch list / weight assignment | route bias |
| retail USD pricing in the customer-facing app | customer-side margin |
| willingness to fail-closed when no acceptable orch is available | demand-side floor |
| gateway-side ETH/USD assumption | wholesale budget per request |

### The discovery dynamic

If an orch publishes above what gateways will pay, gateways either route
elsewhere (the resolver's `Select` returns a different node) or the
gateway's customer-facing app raises prices and absorbs lost demand. If an
orch publishes below sustainable cost, it burns through capex or rental
budget and eventually stops advertising. The system reaches equilibrium
when the marginal orch's wholesale price matches the marginal gateway's
willingness to pay.

**Acceptance floor caveat** (covered in Part 1 and
[`payment-daemon-interactions.md:218-248`](./payment-daemon-interactions.md)):
an "accepted" wholesale price may still fail to mint a redeemable ticket
if the receiver's runtime economics reject the resulting face-value.
Fairness on the published price doesn't guarantee fairness on small
requests — the operator must also tune redemption knobs.

## Orchestrator profitability — four cost lenses

A wholesale wei price is "profitable" only relative to a cost basis.
Operators should run **all four** of the following and use the maximum as
the floor for what they publish:

```
min_sustainable_wholesale_revenue_per_hour =
    max(opportunity_cost_per_hour,
        capex_amortization_per_hour + power_per_hour + colo_per_hour,
        target_revenue_per_hour)
```

### Lens 1 — Cloud opportunity cost

> "Would this card make more renting on RunPod / Lambda / Vast.ai than
> running Livepeer work?"

Inputs:

- `cloud_rate_per_hour` — current spot price for this card on a reference
  cloud (RunPod, Lambda, Vast.ai, Together)
- `expected_livepeer_utilization` — fraction of wall-clock the card is
  actually running paid work

Floor:

```
opportunity_cost_per_hour = cloud_rate_per_hour × utilization_adjustment
```

Where `utilization_adjustment` ≥ 1 because Livepeer revenue is only
realized when the card is *busy*. If a card is busy 60% of the time on
Livepeer, the effective rate while busy must be `cloud_rate / 0.6` to
match what a 100% rented card would earn.

### Lens 2 — Capex amortization

> "Am I covering hardware depreciation on a card I already own?"

Inputs:

- `card_msrp_usd` — purchase price (or used-market replacement cost)
- `useful_life_years` — typical 3–5 years
- `hours_per_year` — 8760
- `expected_lifetime_utilization` — fraction of those hours running paid
  work (usually 50–80%)

Floor:

```
useful_hours = useful_life_years × hours_per_year × expected_lifetime_utilization
capex_per_busy_hour = card_msrp_usd / useful_hours
```

This is the "I bought the card, I want to recover its cost before it dies"
view. Cheap, older cards (1080, 2080) have near-zero amortization floors
and live or die on power + opportunity cost. Expensive accelerators (A100,
H100) have material amortization that must be priced in.

### Lens 3 — Power and colocation

> "What does it cost me per hour to keep this card running?"

Inputs:

- `tdp_watts` — sustained power draw under load (not nameplate peak)
- `pue` — power-usage effectiveness, typically 1.2–1.5 for colo,
  ~1.05–1.15 for hyperscale
- `kwh_rate_usd` — local commercial electricity rate, $0.06–$0.20/kWh
- `colo_share_per_hour` — rack space + bandwidth share allocated to this
  card

Floor:

```
power_per_hour = (tdp_watts / 1000) × pue × kwh_rate_usd
total_marginal_per_hour = power_per_hour + colo_share_per_hour
```

This is the **marginal operating cost** — what it costs to leave the card
on. Below this, the card should be powered off.

### Lens 4 — Effective hourly revenue target

> "I want $X per hour per card; work backwards."

This lens skips cost decomposition and treats `target_revenue_per_hour`
as the operator's input. From there:

```
target_revenue_per_hour ≥ price_per_work_unit_wei × units_per_hour × (eth_usd / 1e18)
```

Re-arranged for the knob the operator sets:

```
price_per_work_unit_wei ≥
    (target_revenue_per_hour × 1e18) / (units_per_hour × eth_usd)
```

`units_per_hour` is the **realistic sustained throughput** of the card on
this workload — see throughput baselines below. `eth_usd` is the
operator's planning assumption for ETH price. The repo has no oracle;
operators re-derive when ETH moves materially.

### Putting the lenses together

A complete operator decision looks like:

1. Pick a `target_revenue_per_hour` that exceeds the max of (opportunity
   cost, capex + power + colo). This is the floor.
2. Add an operating margin (10–30%) for risk and overhead.
3. Estimate `units_per_hour` from realistic throughput.
4. Solve for `price_per_work_unit_wei` using Lens 4's formula.
5. Sanity-check the resulting wei value: is it competitive with comparable
   wholesale offerings on other orchs?

## Hardware cost reference table

Planning numbers. Replace with current data before going live. MSRP / used
prices oscillate; cloud rates are spot-market quotes from late-2025 / early-2026.

| Card | TDP (W) | MSRP (USD, new) | Used (USD) | Cloud spot ($/hr, ref) | Capex/hr @ 4yr×70% | Power/hr @ $0.10/kWh×1.4 PUE |
|---|---|---|---|---|---|---|
| GTX 1080 | 180 | EOL | 80–150 | 0.15–0.20 | $0.005 | $0.025 |
| RTX 2080 | 215 | EOL | 250–400 | 0.20–0.30 | $0.013 | $0.030 |
| RTX 3090 | 350 | EOL | 700–1,000 | 0.30–0.45 | $0.035 | $0.049 |
| RTX 4090 | 450 | 1,500–2,000 | — | 0.40–0.65 | $0.071 | $0.063 |
| RTX 5090 | 575 | 2,000–2,500 | — | 0.50–0.80 | $0.092 | $0.080 |
| RTX 4000 Ada | 130 | 1,200–1,500 | — | 0.35–0.50 | $0.055 | $0.018 |
| RTX 6000 Ada | 300 | 6,500–7,500 | — | 0.80–1.20 | $0.286 | $0.042 |
| A100 80GB | 400 | 10,000–15,000 | — | 1.50–2.00 | $0.510 | $0.056 |
| H100 80GB | 700 | 25,000–35,000 | — | 2.50–3.50 | $1.224 | $0.098 |

**Reading the table**: An orchestrator with an H100 facing $3.00/hr cloud
opportunity cost should target wholesale revenue **above** $3.00/hr, not
above the $1.32/hr capex+power sum. Cloud opportunity cost dominates on
hot cards. Conversely, a 1080 has negligible amortization and minimal
power; its floor is set by its (very low) cloud rate.

**Multi-GPU caveat**: For models that span GPUs (Llama 3 70B fp16 needs 2×
80GB cards), multiply all cost columns by the GPU count. A 70B-on-2×A100
deployment has a $1.02/hr capex floor and $0.11/hr power floor before any
margin.

## Workload throughput baselines

The other input Lens 4 needs is `units_per_hour`. These are **planning
ballparks** under batched serving (vLLM / TGI / TensorRT-LLM /
FFmpeg-NVENC), not single-request latencies. Real numbers depend on
quantization, batch size, context length, and software stack. Re-benchmark
on your own hardware before publishing prices.

### LLM inference (output tokens/sec, batched, fp16 unless noted)

| Model | 3090 | 4090 | A100 80GB | H100 80GB |
|---|---|---|---|---|
| Llama 3 8B | 80–120 | 150–200 | 250–350 | 400–600 |
| Llama 3 70B | OOM (1×) | OOM (1×) | 60–100 (2×) | 100–180 (1×) |
| Mixtral 8x7B | 40–60 (2×) | 60–90 (1×) | 100–160 (1×) | 200–300 (1×) |
| GPT-OSS 20B | 60–90 | 100–140 | 150–220 | 250–400 |

(Numbers reflect aggregate output tok/sec across concurrent requests under
vLLM-style batching; single-stream latency is lower.)

### Image generation (steps/sec, 1024×1024)

| Model | 3090 | 4090 | A100 80GB | H100 80GB |
|---|---|---|---|---|
| SDXL base | 12–18 | 20–25 | 25–35 | 50–70 |
| Flux.1 dev | 3–5 | 5–8 | 8–12 | 15–25 |

### Transcoding (real-time 1080p H.264 NVENC streams)

| Generation | 1080p concurrent | 4K equivalent |
|---|---|---|
| GTX 1080 / 2080 | 1–2 | n/a |
| RTX 3090 | 3–5 | 1 |
| RTX 4090 | 6–8 | 2 |
| RTX 5090 | 8–10 | 3 |
| A100 | 8–12 | 3 |
| H100 | 12–20 | 4–6 |

NVENC throughput is gated by the encoder block count, not raw FLOPS —
data-center cards don't dominate transcoding the way they dominate
inference.

## Worked examples — deriving wholesale wei prices

Each example fixes a workload, picks a target $/hr from the lenses above,
picks a representative card, and derives `price_per_work_unit_wei`. The
gateway-side ETH/USD assumption is stated in each example. Operators must
re-derive when ETH moves.

**Conversion math used throughout**:

```
1 USD = (1e18 / eth_usd) wei
price_per_unit_wei = (target_usd_per_unit) × (1e18 / eth_usd)
```

At ETH = $3,500: `1 USD ≈ 2.857 × 10^14 wei ≈ 286 Tera-wei`.

### Example 1 — Llama 3.3 70B served from an H100

- **Reference retail** (for sanity, not for the wholesale price):
  Together.ai ≈ $0.88 / 1M output tokens. OpenRouter aggregates Llama 3 70B
  in the $0.50–1.20 / 1M output tokens range.
- **Lens 1 (opportunity cost)**: H100 spot ~$3.00/hr.
- **Lens 2 (capex)**: $30K / 24,528 useful hr = $1.22/hr.
- **Lens 3 (power)**: 700W × 1.4 PUE × $0.10/kWh = $0.098/hr.
- **Lens 4 target**: $4.00/hr (covers opportunity cost + ~33% margin).
- **Throughput**: 120 output tok/sec batched aggregate
  (conservative — H100 + vLLM + AWQ can hit 200+).
- **Math**:
  - `units_per_hour` = 120 × 3600 = 432,000 tokens/hr
  - target $/tok = $4.00 / 432,000 = $9.26 × 10⁻⁶ per token
  - target $/1M tok = $9.26 (well below Together's $0.88/M retail? No —
    above. Means this card cannot beat Together at the assumed throughput.
    Either the orch needs higher throughput, accepts lower margin, or
    differentiates on something other than price.)
- Sanity-check with higher throughput (batch 8, AWQ, 250 tok/sec sustained):
  - 250 × 3600 = 900,000 tokens/hr
  - target $/tok = $4.00 / 900,000 = $4.44 × 10⁻⁶ per token = $4.44 / 1M
  - At ETH=$3,500: `price_per_work_unit_wei` =
    $4.44 × 10⁻⁶ × (1e18 / 3,500) = **1.27 × 10⁹ wei** per token
    (≈ 1.27 Gwei/token).
- **Operator decision**: publish ~1.27 Gwei/token if confident in 250
  tok/sec sustained. Below that throughput, this card cannot price-compete
  with Together; the orch should consider serving a smaller model or
  accepting margin compression.

### Example 2 — SDXL base on RTX 4090

- **Reference retail**: Replicate charges ~$0.0019/sec on A40; ~$0.05 per
  generated 1024×1024 image at 30 steps.
- **Lens 1 (opportunity cost)**: 4090 spot ~$0.50/hr.
- **Lens 2 (capex)**: $1,800 / 24,528 = $0.073/hr.
- **Lens 3 (power)**: 450W × 1.4 × $0.10 = $0.063/hr.
- **Lens 4 target**: $0.80/hr (above opportunity cost + ~60% margin —
  consumer cards usually need higher % margin to cover failure rates).
- **Throughput**: 22 it/sec at 1024×1024 → 30-step image = 1.36 sec/image
  → 2,640 images/hr.
- **Work unit choice**: `megapixel_step`. 1024² = 1.048 megapixel × 30
  steps = 31.4 megapixel-steps per image.
  - `units_per_hour` = 31.4 × 2,640 = 82,896 megapixel-steps/hr
  - target $/megapixel-step = $0.80 / 82,896 = $9.65 × 10⁻⁶
  - At ETH=$3,500: `price_per_work_unit_wei` =
    9.65 × 10⁻⁶ × (1e18 / 3,500) = **2.76 × 10⁹ wei** per megapixel-step
    (≈ 2.76 Gwei).
- **Sanity vs retail**: 2.76 Gwei × 31.4 = 86.7 Gwei per image
  = $0.000304 wholesale per image. Replicate charges ~$0.05 retail. Gateway
  has ~165× headroom for margin, customer-facing markup, infra cost, and
  abuse buffer. Comfortably competitive.

### Example 3 — Live 1080p H.264 transcode on RTX 3090

- **Reference retail**: Mux ≈ $0.024 / encoded minute (per rendition).
- **Lens 1 (opportunity cost)**: 3090 spot ~$0.40/hr.
- **Lens 2 (capex)**: $850 / 24,528 = $0.035/hr.
- **Lens 3 (power)**: 350W × 1.4 × $0.10 = $0.049/hr.
- **Lens 4 target**: $0.60/hr (above opportunity cost + ~50% margin).
- **Throughput**: 4 concurrent real-time 1080p streams = 240 minutes of
  output per wall-clock hour.
- **Work unit choice**: `megapixel_frame`. 1080p = 2.073 megapixel × 30 fps
  × 60 sec = 3,732 megapixel-frames/minute → 895,680 mp-frames/hr at full
  utilization.
  - target $/megapixel-frame = $0.60 / 895,680 = $6.70 × 10⁻⁷
  - At ETH=$3,500: `price_per_work_unit_wei` =
    6.70 × 10⁻⁷ × (1e18 / 3,500) = **1.91 × 10⁸ wei** per
    megapixel-frame (≈ 191 Mwei).
- **Sanity vs retail**: 191 Mwei × 3,732 = 7.13 × 10¹¹ wei per minute of
  encoded 1080p = $0.0025 wholesale per minute. Mux retail $0.024/min →
  ~9.6× gateway margin available.
- **ABR ladder note**: a typical ABR job emits 3–5 renditions; the work
  unit naturally scales because megapixel-frames sum across renditions
  (240p, 480p, 720p, 1080p sum to ~1.5× the 1080p alone). The published
  wei price stays constant; the units consumed grow.

### Example 4 — Whisper-style audio transcription on RTX 4000 Ada

- **Reference retail**: OpenAI Whisper API $0.006 / minute of audio.
- **Lens 1 (opportunity cost)**: RTX 4000 Ada spot ~$0.40/hr.
- **Lens 2 (capex)**: $1,400 / 24,528 = $0.057/hr.
- **Lens 3 (power)**: 130W × 1.4 × $0.10 = $0.018/hr.
- **Lens 4 target**: $0.65/hr.
- **Throughput**: Whisper-large-v3 on RTX 4000 Ada ≈ 30× realtime → 30
  minutes of audio per minute wall-clock → 1,800 audio-minutes/hr.
- **Work unit choice**: `audio_second`. 1,800 × 60 = 108,000
  audio-seconds/hr.
  - target $/audio-second = $0.65 / 108,000 = $6.02 × 10⁻⁶
  - At ETH=$3,500: `price_per_work_unit_wei` =
    6.02 × 10⁻⁶ × (1e18 / 3,500) = **1.72 × 10⁹ wei** per audio-second
    (≈ 1.72 Gwei).
- **Sanity vs retail**: 1.72 Gwei × 60 = 103 Gwei per audio-minute
  = $0.000361 wholesale per minute. OpenAI retail $0.006/min → ~17×
  gateway headroom.

### Example 5 — Multi-GPU Llama 3 70B on 2× A100

- **Lens 1 (opportunity cost)**: 2× A100 spot ~$3.50/hr combined.
- **Lens 2 (capex)**: 2 × $0.51 = $1.02/hr.
- **Lens 3 (power)**: 2 × $0.056 = $0.112/hr.
- **Lens 4 target**: $4.20/hr (covers opportunity + 20% margin).
- **Throughput**: 70B fp16 across 2× A100 via tensor parallelism ≈ 80
  output tok/sec batched.
- **Math**:
  - 80 × 3600 = 288,000 tokens/hr
  - target $/tok = $4.20 / 288,000 = $1.46 × 10⁻⁵ per token = $14.60/1M
  - At ETH=$3,500: `price_per_work_unit_wei` =
    1.46 × 10⁻⁵ × (1e18 / 3,500) = **4.17 × 10⁹ wei** per token
    (≈ 4.17 Gwei).
- **Operator decision**: this orch will not undercut Together.ai's $0.88/M
  unless A100 spot drops or batched throughput improves. Suggests the orch
  should serve smaller models (Llama 3 8B, Mixtral) where its
  cost-per-token is more competitive, or differentiate on locality / SLA
  rather than price.

### Pattern across the examples

Image and audio workloads have large gateway-margin headroom against
reference retail (3–17×). LLM token serving is the tight one — H100 and
above are needed to price-compete with hyperscale inference providers.
This is consistent with industry reality: LLM serving is the
most-commoditized GPU workload, and consumer cards struggle to undercut
batched hyperscale economics.

## Gateway-side profitability

The gateway's economics are different in shape:

- **Revenue** — retail USD billed to customers (in `openai-gateway`,
  `video-gateway`, `vtuber-gateway`, `customer-portal`).
- **Cost** — wholesale wei paid to orchs (via `CreatePayment` →
  `Livepeer-Payment` envelope → `DebitBalance` on receiver).
- **Margin** — retail revenue minus wholesale cost, minus gateway infra
  (servers, CDN, observability, support).

Operator knobs:

| Knob | Where |
|---|---|
| Retail USD price per unit | customer-facing gateway service |
| Per-customer rate cards / tiers | customer-portal billing |
| ETH/USD planning rate | gateway service config (must be re-derived as ETH moves) |
| Preferred-orch list / weight bias | resolver client policy |
| `min_weight` filter | `SelectRequest.MinWeight` |
| Fail-closed price ceiling | gateway adapter logic (refuse to route if no orch under threshold wei/unit) |
| Failure-rate floor | gateway-route-health to deprioritize flaky orchs |

The gateway operator profits when retail USD × actualUnits exceeds
wholesale wei × actualUnits (converted at the gateway's planning ETH/USD
rate) plus per-request infra cost. Because the conversion isn't enforced
anywhere in the protocol, this is purely gateway-operator accounting.

**ETH volatility risk lives at the gateway boundary**, not at the orch.
If the gateway sells a unit at $5 retail when ETH = $3,500 (paying ~1.43
× 10¹⁵ wei to the orch), and ETH then rises to $5,000, that same wei
costs $7.15 — wiping the margin. Gateways either:

- repriced retail dynamically (per-request ETH lookup), or
- repriced in USD per billing period (accept short-term ETH drift), or
- hedge via stablecoin treasury operations off-protocol.

The orch is unaffected by this — they receive a stable wei stream.

## Tooling opportunities for transparency and operator UX

This is where there is **a lot of room to build**, and where most
operator-facing pain currently sits. Each item below names a tool, what
it does, and which existing repo surfaces it would plug into.

### Orch-side

1. **Profitability calculator CLI** (`tools/orch-profit`).
   - Inputs: card model, expected utilization, ETH/USD assumption, workload
     throughput.
   - Outputs: minimum sustainable `price_per_work_unit_wei` for each lens
     (opportunity, capex, power, target). Suggests a host-config snippet.
   - Plugs into: standalone tool. Reads no live state. Could read
     `host-config.yaml` to validate published prices against the floor.

2. **Live profitability dashboard** (`secure-orch-console` extension).
   - Inputs: live data from `payment-daemon.GetRoundRevenue` (confirmed
     on-chain revenue), `pool-controller` work receipts (units served),
     operator-supplied cost basis.
   - Outputs: realized $/hr per card, vs. each lens's floor; alerts when
     drifting below.
   - Plugs into: `secure-orch-console/` UI + `payment-daemon` RPCs +
     `pool-controller` receipt stream.

3. **Manifest price validator** (`tools/manifest-lint`).
   - Inputs: a signed manifest URL.
   - Outputs: parsed prices in wei *and* in USD (at a supplied ETH/USD
     rate), flagged when below or above sane bands per workload class.
   - Plugs into: `service-registry-daemon` resolver client + a band file.

4. **Acceptance-floor diagnostic** (`tools/floor-check`).
   - Inputs: a target retail wei price, current `--receiver-ev` and gas
     assumptions.
   - Outputs: whether requested face-values down to size X can be turned
     into redeemable winning tickets, or whether the receiver will reject.
   - Plugs into: `payment-daemon/internal/service/receiver` simulation
     mode.

### Gateway-side

5. **Wholesale price-comparison feed** (`tools/wholesale-feed`).
   - Inputs: a list of orch eth addresses (or registry browse).
   - Outputs: a per-capability, per-offering matrix of advertised
     wholesale prices across orchs, normalized to a chosen unit. Updated
     on a cadence.
   - Plugs into: `service-registry-daemon.Resolver.ResolveByAddress` +
     `ListKnown` + a small storage layer.

6. **Live wholesale-vs-retail margin monitor** (`customer-portal`
   extension).
   - Inputs: per-request actual units, wholesale wei debited, retail USD
     billed, current ETH/USD.
   - Outputs: rolling margin per capability, per customer tier; alerts
     when margin compresses below threshold.
   - Plugs into: `customer-portal/` + `payment-daemon` sender-side
     telemetry.

7. **ETH/USD planning oracle** (`tools/eth-oracle-sidecar`).
   - Inputs: chain or off-chain price feed.
   - Outputs: an authoritative ETH/USD rate that gateway services read at
     pricing time, with audit-friendly logs.
   - Plugs into: gateway services that currently hard-code or env-var the
     rate. Out of band from `payment-daemon` deliberately.

8. **Route-quality cost overlay** (`gateway-route-health` extension).
   - Inputs: existing route-health data, plus wholesale price per route.
   - Outputs: a single "$ per successful unit" metric that combines
     reliability with price, so the resolver client biases away from
     cheap-but-flaky orchs.
   - Plugs into: `gateway-route-health/` + `service-registry-daemon`
     selection weight feedback.

### Cross-cutting

9. **Pricing simulator** (`tools/pricing-sim`).
   - Inputs: a workload profile (req/sec, distribution of work-unit
     counts), a fleet of orch advertised prices, a gateway retail policy.
   - Outputs: simulated end-to-end USD/hr revenue for the gateway and
     wei/hr revenue per orch over a synthetic day. Lets operators
     experiment before changing live prices.
   - Plugs into: synthetic tool; no live dependencies.

10. **Capability-onboarding wizard** (`secure-orch-console` extension).
    - Inputs: card hardware, workload type, target $/hr.
    - Outputs: a starter `host-config.yaml` snippet — capability id,
      offering id, work-unit name + extractor, derived
      `price.amount_wei` — that the operator can copy in and tune.
    - Plugs into: `capability-broker/` config tooling +
      `secure-orch-console`.

### Where these naturally consolidate

Items 1, 3, 4, 9, 10 are **standalone tools** in a new `tools/`
subdirectory (or extensions of existing CLIs). Items 2, 6 are
**dashboard surfaces** that should live in `secure-orch-console`
(orch-side) and `customer-portal` (gateway-side). Items 5, 7, 8 are
**sidecar services** that gateways and orchs run alongside their main
daemons.

The repo currently ships none of these. They are pure tooling
opportunities and are independent of the daemon protocol; a tool can be
built and shipped without touching `payment-daemon` or `capability-broker`
internals, as long as it reads the public RPC surface and the published
manifests.

## Reasoning about pricing fairness — checklist for operators

A wholesale price is *fair* when:

- [ ] It clears all four cost lenses with a positive margin.
- [ ] It is reproducible from documented inputs (cost basis, throughput,
      ETH/USD assumption) — not back-fit to match a competitor.
- [ ] It survives reasonable ETH volatility (e.g. ±30%) without dropping
      below the lens floors.
- [ ] It is consistent with the orch's offered SLA (reliability,
      latency, region). An H100 in a Tier-IV colo with 99.99% uptime
      legitimately charges more than the same card on a residential
      connection.
- [ ] The acceptance floor (receiver runtime economics) does not silently
      reject the small requests the price claims to support — see
      [`payment-daemon-interactions.md`](./payment-daemon-interactions.md).
- [ ] It is comparable to other orchs serving the same offering, within a
      band that reflects real cost differences (not a race to the bottom
      that masks unsustainable economics).

A gateway price is *fair* when:

- [ ] The retail USD price is a stated multiple of the wholesale wei cost
      at the gateway's planning ETH/USD rate.
- [ ] Margin is defensible against competing gateways and direct
      hyperscale APIs (OpenAI, Anthropic, Mux, Replicate).
- [ ] Fail-closed behavior is documented: when no orch is below the
      gateway's ceiling, the gateway refuses the request rather than
      silently overcharging the customer.
- [ ] Customers can audit usage: actualUnits, retail USD billed,
      wholesale wei spent — all visible in customer-portal.

---

# Part 3 — Market-anchored pricing

Part 2 derived prices from a **cost floor** — what the orchestrator must
earn to break even on hardware, power, opportunity cost, and target
hourly revenue. That answers "how low can I go?" It does not answer
"what should I actually charge?"

In commoditized markets the answer is set by competing hyperscale
providers (OpenAI, Anthropic, Together, Replicate, Cohere, Mux, HeyGen,
Midjourney, etc.). Customers compare against those providers and the
orchestrator must price below or near them while still clearing the cost
floor. Livepeer's value proposition is "cheaper, decentralized, censorship-
resistant, crypto-native" — *cheaper* requires a deliberate market anchor,
not just a cost-floor calculation.

Profitable orchestrators publish at the **market anchor**, not the cost
floor.

## Cost floor vs market ceiling — the decision rule

```
target_retail   = hyperscale_retail × (1 − discount)
target_wholesale = target_retail / (1 + gateway_margin)

if target_wholesale > cost_floor_with_margin:
    publish target_wholesale          # market-anchored, profitable
elif cost_floor < target_wholesale:
    publish target_wholesale          # market-anchored, thin margin — proceed with eyes open
else:                                 # cost_floor ≥ target_wholesale
    workload is structurally unprofitable on this hardware. Options:
      a) skip the offering
      b) raise effective throughput (better batching, quantization, lower-context model)
      c) move to cheaper hardware (consumer cards for smaller models)
      d) accept break-even or short-term loss as pool participation / customer acquisition
```

The cost floor and the market anchor are not always in the same place.
For some workloads (image gen, rerank, transcoding on consumer cards) the
market anchor is *far above* the floor and the orchestrator pockets the
difference. For others (LLM tokens served by commodity providers like
Together) the market anchor is *below* the floor on premium hardware and
the orchestrator must either compete on a different axis or skip the
offering.

## Hyperscale reference table

Current retail prices, late-2025 / early-2026. Re-verify before deploying:
all of these update without notice.

### LLM token serving

| Provider | Model | Retail (per 1M tokens) |
|---|---|---|
| OpenAI | GPT-4o | $2.50 input / $10.00 output |
| OpenAI | GPT-4o-mini | $0.15 input / $0.60 output |
| Anthropic | Claude Sonnet 4 | $3.00 input / $15.00 output |
| Anthropic | Claude Opus 4 | $15.00 input / $75.00 output |
| Anthropic | Claude Haiku | $0.80 input / $4.00 output |
| Together.ai | Llama 3.3 70B | $0.88 output |
| Together.ai | Llama 3 8B | $0.18 output |
| Together.ai | Mixtral 8x7B | $0.60 output |
| DeepInfra | Llama 3.3 70B | $0.40–0.60 output |
| Fireworks | Llama 3.3 70B | $0.90 output |
| OpenRouter | Llama 3.3 70B (aggregate) | $0.50–1.20 output |
| OpenRouter | Claude Sonnet (resale) | match Anthropic + small markup |

### Image / video generation

| Provider | Model | Retail |
|---|---|---|
| Replicate | SDXL base | ~$0.05 per image |
| Replicate | SDXL Lightning / Turbo | ~$0.02–0.03 per image |
| Replicate | Flux Dev | ~$0.03 per image |
| Replicate | Flux Pro 1.1 | ~$0.05 per image |
| OpenAI | DALL-E 3 standard 1024 | $0.04 per image |
| OpenAI | DALL-E 3 HD 1024 | $0.08 per image |
| Midjourney | Standard plan | ~$0.02–0.04 per image (effective) |
| HeyGen | AI avatar streaming | ~$0.005–0.015 per second |
| Synthesia | AI avatar generation | ~$0.10–0.30 per minute output |

### Transcoding / streaming

| Provider | Workload | Retail |
|---|---|---|
| Mux | 1080p H.264 encode | $0.024 per encoded minute |
| Mux | 4K encode | $0.072 per encoded minute |
| AWS MediaConvert | 1080p basic | ~$0.015 per minute |
| AWS MediaConvert | 1080p professional | ~$0.045 per minute |
| Cloudflare Stream | encode + delivery | $0.005 per minute stored + $1/1000 min delivered |

### Audio / utility

| Provider | Workload | Retail |
|---|---|---|
| OpenAI Whisper API | speech-to-text | $0.006 per audio minute |
| Deepgram | Nova-2 | ~$0.0043 per audio minute |
| Cohere | Rerank v3 English | $2.00 per 1,000 requests = $0.002/req |
| Voyage AI | rerank-2 | $0.05 per 1M tokens reranked |
| Jina AI | reranker-v2 | $0.02 per 1M tokens |

## Workload commoditization spectrum

Discount target varies by how commoditized the workload is. Highly
commoditized workloads have thin margins everywhere; the orchestrator
can only undercut by a little before going negative. Less commoditized
workloads have fat hyperscale margins; the orchestrator can undercut
substantially while still profiting.

| Workload class | Commoditization | Discount band (off hyperscale retail) | Why |
|---|---|---|---|
| LLM open-weight serving | **Very high** | 5–15% | Together, DeepInfra, Fireworks already at thin margins on H100/A100 batched. Hard to undercut materially. |
| LLM closed-model resale | n/a — markup model | +10–20% over orch's upstream cost | Orch pays upstream retail; value is convenience / payment rails / anonymity, not price. |
| Image gen — open weights | High | 25–40% | Replicate and friends have fat managed-service margins. Easy to undercut. |
| Image gen — frontier | Medium | 15–25% | DALL-E 3, Midjourney harder to clone exactly; smaller discount. |
| Video transcoding | Medium | 20–30% | Mux is premium; AWS MediaConvert is closer to marginal cost. |
| Real-time generative video | Low (emerging) | 10–20% | New market, less competition. Ride the wave; don't race-to-zero. |
| AI avatar / VTuber | Medium | 20–30% | HeyGen, Synthesia have moderate margins. |
| Audio transcription | High | 20–30% | Whisper is open-weight; orchs serving Whisper compete on price. |
| Rerank | High | 25–35% | Cohere is the de-facto retail; many open rerank models close-to-quality. |
| Custom / niche | Low | 10–25% case-by-case | No reference price exists; anchor to nearest analog. |

## GPU tier adjustment

The same capability can — and should — be published as **multiple
offerings** at different price points keyed to GPU class. Premium
hardware delivers better throughput / latency / quality and earns the
smaller end of the discount band. Budget hardware earns the larger end
(deeper price discount, lower per-request quality or speed).

| GPU tier | Tier label | Discount adjustment within the workload band |
|---|---|---|
| H100, H200 | premium | floor of the band (smallest discount) |
| A100, RTX 6000 Ada | enterprise | mid-low of the band |
| RTX 5090, 4090 | prosumer | mid of the band |
| RTX 4000 Ada, 3090 | budget | mid-high of the band |
| 2080, 1080 (where supported) | hobbyist | ceiling of the band (largest discount) |

**Concrete example** — three Llama 70B offerings on one orchestrator, all
under capability `openai:chat-completions`, differentiated by
`offering_id`:

```yaml
capabilities:
  - id: "openai:chat-completions"
    offering_id: "llama-3.3-70b-h100"     # premium: 10% off Together
    # price targets retail ≈ $0.79/M output
  - id: "openai:chat-completions"
    offering_id: "llama-3.3-70b-a100"     # mid: 12% off
    # price targets retail ≈ $0.77/M output
  - id: "openai:chat-completions"
    offering_id: "llama-3.3-70b-2x3090-awq"  # budget: 20% off, quantized
    # price targets retail ≈ $0.70/M output, slower / quantized
```

The gateway adapter then picks the offering matching the customer's
SLA / latency / quality preference.

## Resale vs self-hosted — different economic model

**Self-hosted** (orch runs the model on its hardware):

```
wholesale = hyperscale_retail × (1 − discount) / (1 + gateway_margin)
```

The orchestrator captures `(market_price − cost_floor)` and the gateway
captures `gateway_margin`. The discount represents Livepeer's value
proposition over the hyperscale provider.

**Resale** (orch proxies traffic to OpenAI / Anthropic / OpenRouter /
upstream API):

```
orch_cost     = upstream_retail (orch pays full price; rarely negotiated)
wholesale     = orch_cost × (1 + orch_markup)        # orch_markup ≈ 5–15%
retail        = wholesale × (1 + gateway_margin)     # gateway_margin ≈ 30%
```

The retail is now *above* the upstream direct price by
`(1 + orch_markup) × (1 + gateway_margin) − 1` — typically 35–50%. The
customer pays a premium for routing / single-bill / crypto payment /
anonymity, **not** for a cheaper compute price. Discount-based anchoring
does not apply.

Resale only makes economic sense for customers who actively value the
non-price benefits. If a customer just wants the cheapest GPT-4o tokens,
they go to OpenAI direct.

## Worked examples — market-anchored derivation

Re-deriving the cost-floor examples from Part 2 with market anchors.
All conversions at ETH = $2,900: `wei = USD × 3.448 × 10¹⁴`.

### Example 1 — Llama 3 70B on H100, premium tier

- **Reference**: Together.ai $0.88 / 1M output tokens
- **Workload band**: 5–15% (LLM open-weight, very commoditized)
- **GPU tier**: H100 → premium → 10% discount
- **Retail target**: $0.88 × 0.90 = $0.792 / 1M
- **Gateway margin**: 30% → wholesale = $0.792 / 1.30 = **$0.609 / 1M**
- **Per token**: $6.09 × 10⁻⁷
- **Wei**: 6.09e-7 × 3.448e14 = 2.10 × 10⁸ = **210 Mwei / token**

**Cost-floor check** (from Part 2): H100 cost floor with margin is
~$4.00/hr. At 250 tok/s sustained × 3,600 s = 900,000 tok/hr × $6.09e-7
= **$0.55 / hr revenue**. That is **below the cost floor by ~7×**.

This is the structural-unprofitability case. To make Llama 70B on H100
work at competitive pricing, the orchestrator needs **1,800+ tok/s
sustained** aggregate throughput — which requires aggressive vLLM
batching with high concurrent request load. At realistic single-tenant
throughput, the workload loses money. Operator options:

- Run Llama 8B (much higher per-card throughput, smaller tok/s required).
- Quantize to AWQ-INT4 and run on 2× consumer cards (e.g. 2× 4090 with
  shared KV cache) for a budget-tier offering.
- Skip the offering until demand justifies a dedicated batched-serving
  configuration.

### Example 2 — RealVisXL Lightning on RTX 4090

- **Reference**: Replicate ~$0.025 / image (Lightning variant)
- **Workload band**: 25–40% (image gen open, high commoditization)
- **GPU tier**: 4090 → prosumer → 30% discount (mid of band)
- **Retail target**: $0.025 × 0.70 = $0.0175 / image
- **Gateway margin**: 30% → wholesale = $0.0175 / 1.30 = **$0.01346 / image**
- **Wei**: 0.01346 × 3.448e14 = 4.64 × 10¹² = **4.6 Twei / image**

**Cost-floor check**: 4090 cost floor with margin is $0.80/hr. At 6,000
img/hr realistic batched throughput × $0.01346 = **$80.76 / hr revenue**.
That is **100× above the cost floor**. Image gen on consumer cards has
massive market headroom; the binding constraint is demand, not price.

### Example 3 — 1080p H.264 transcode on RTX 3090

- **Reference**: Mux $0.024 / encoded minute → $0.0004 / sec
- **Workload band**: 20–30% (transcoding, medium commoditization)
- **GPU tier**: 3090 → budget → 25% discount (mid-high of band)
- **Retail target**: $0.0004 × 0.75 = $0.0003 / sec
- **Gateway margin**: 30% → wholesale = $0.0003 / 1.30 = **$0.000231 / sec**
- **Wei**: 0.000231 × 3.448e14 = 7.96 × 10¹⁰ ≈ **80 Gwei / sec**

**Cost-floor check**: 3090 cost floor with margin is $0.60/hr ÷ 4 concurrent
NVENC streams = $0.15/hr per stream. At 3,600 sec/hr × $0.000231 = **$0.83
/ hr per stream revenue** = $3.33 / hr per card across 4 streams. **5.5×
above the cost floor.** Transcoding on consumer cards is comfortably
profitable at market anchor.

### Example 4 — GPT-4o resale

- **Orch upstream cost** (blended assumption: 70% output / 30% input):
  $10.00 × 0.7 + $2.50 × 0.3 = $7.75 / 1M
- **Orch markup**: 10% → wholesale = $7.75 × 1.10 = $8.53 / 1M
- **Gateway margin**: 30% → retail = $8.53 × 1.30 = **$11.08 / 1M**

That is ~11% above OpenAI direct ($10 / 1M output) — a reasonable
convenience premium. Wholesale wei: $8.53e-6 × 3.448e14 = 2.94 × 10⁹ =
**2.94 Gwei / token**.

Note this is wholesale **above** the orch's upstream cost, not below.
Resale doesn't undercut; it adds margin layers atop the direct price.

## Realistic-utilization caveat

The examples above compute revenue at full utilization. **Real demand is
uneven.** A card priced at the market anchor only earns full anchor
revenue when it's actually serving requests. Considerations:

- **Time-weighted vs busy-weighted utilization**: a card busy 30% of the
  day earns 30% of full-anchor revenue at the published wholesale price.
- **Demand elasticity**: pricing 50% below the anchor doesn't double
  demand. The relationship is workload-specific. Test empirically.
- **Operator monitoring**: `payment-daemon.GetRoundRevenue` reports
  realised on-chain revenue per round. Compare against the anchor's
  theoretical revenue to compute realised utilization.
- **Bidding-down behaviour**: when an orchestrator's utilization falls
  below a threshold, lower wholesale price by a small step (5–10%) and
  observe demand response. Don't race-to-zero; the floor still applies.

The cost-floor + market-anchor framework gives an orchestrator the
**bounds**. Operating within those bounds requires live data, which is
what the tooling opportunities (Part 2) are meant to provide.

## Where to find each input

| Input | Source | Update cadence |
|---|---|---|
| `hyperscale_retail` | provider pricing pages (rolling table above) | re-verify monthly |
| `discount band` | workload commoditization table | revisit per quarter |
| `gateway_margin` | gateway operator policy | per-gateway, stable |
| `cost_floor` | four lenses from Part 2 | per-card, stable until hardware turnover |
| `ETH/USD` | ETH oracle (or operator planning rate) | hourly to daily |
| `throughput` | live benchmarks on operator's hardware | per-deployment-config |
| `realised utilization` | `payment-daemon.GetRoundRevenue` | per-round |

The tooling list in Part 2 (items 1, 5, 7, 9) maps directly to these
inputs. Without that tooling, anchoring is manual and stale; with it,
operators can re-derive prices weekly.

---

# Part 4 — Operational realities: volume, subsidies, and continuous calibration

Parts 1–3 give an operator the *theory* of pricing: how the daemon
computes debits, what the cost floor is, what the market anchor is.
This part is the *reality* of running an orchestrator. There are three
factors operators must internalize before they treat any published wei
price as "fair":

1. **Volume is the binding constraint.** A correctly priced offering
   only earns its theoretical revenue when it is actually utilized.
2. **LPT inflation rewards are a subsidy floor.** When wholesale revenue
   alone doesn't clear cost, on-chain Orchestrator Rewards
   (`BondingManager.rewardWithHint` per round) close part of the gap —
   but only for orchestrators with active stake.
3. **Premium hardware (H100, large open-weight models) requires demand
   validation.** Structurally unprofitable workloads at market-anchor
   pricing stay structurally unprofitable until demand justifies dedicated
   batched-serving — and that has to be measured, not assumed.

Pricing is a continuous calibration between gateways and orchestrators,
not a one-time configuration. The rest of this part covers each factor
and the bilateral data-sharing tooling needed to support iterative price
discovery.

## Factor 1 — Volume is the binding constraint

Every wei price in Parts 2 and 3 was derived at notional-full-utilization
revenue. A card priced at the market anchor earns the anchor revenue
*only when serving requests*. At 25% utilization it earns 25% of anchor
revenue, regardless of the published price.

### Break-even utilization

The break-even utilization for an offering is:

```
break_even_utilization = cost_floor_per_hour / theoretical_full_util_revenue_per_hour
```

Worked back through the Llama 3 70B / H100 example (Part 3):

- Cost floor (with margin): $4.00/hr
- Market-anchored wholesale: $0.609 / 1M tokens
- Throughput at full util: 250 tok/s × 3,600 = 900K tok/hr
- Theoretical revenue: 900K × $6.09 × 10⁻⁷ = $0.55/hr
- Break-even utilization: $4.00 / $0.55 = **727%** (impossible)

That is the same structural unprofitability finding as Part 3, restated
in utilization terms: the offering cannot clear its floor even at 100%
utilization until throughput rises ~7× (to 1,800+ tok/s aggregate).

By contrast, the RealVisXL / 4090 example clears at any realistic
utilization:

- Cost floor: $0.80/hr
- Market-anchored wholesale: $0.01346 / image
- Throughput at full util: 6,000 img/hr
- Theoretical revenue: $80.76/hr
- Break-even utilization: $0.80 / $80.76 = **0.99%**

That offering is profitable as long as one in a hundred minutes serves a
request. Demand is the binding constraint, not price.

### What operators should monitor

| Metric | Source | Why |
|---|---|---|
| Requests per hour per offering | broker request log | demand level |
| Mean / p95 utilization per card | broker + GPU metrics | how much capacity is actually used |
| Realised wholesale wei per hour | `payment-daemon.GetRoundRevenue` | actual revenue, not theoretical |
| Theoretical-revenue gap | comparison vs anchor × util | how much demand vs price gap explains the gap |
| Acceptance-floor rejections | receiver telemetry | requests refused because ticket economics fail |
| Failed-route rate | gateway-side telemetry | demand the orch could have served but didn't |

**The volume imperative**: an orchestrator with low utilization should
not assume its price is wrong. Low utilization at the right price means
demand for that offering at that price is thin. Lowering the price may
not bring more demand (image gen at 50% off Replicate may still see no
demand if customers don't know the offering exists). Marketing,
discoverability, gateway integration, and SLA reliability often matter
more than further price cuts.

## Factor 2 — LPT inflation rewards (Orchestrator Onchain Rewards)

Livepeer's protocol mints LPT each round and distributes it to active
orchestrators in proportion to delegated stake × work performed.
This is **independent of wholesale wei revenue**: an orchestrator earns
LPT inflation even on a round with zero served requests, as long as the
round was initialized and reward was called.

### How it lives in this repo

- [`protocol-daemon/`](../../protocol-daemon/) handles the two on-chain
  orchestrator responsibilities. Per
  [`protocol-daemon/doc.go:3-6`](../../protocol-daemon/doc.go):

  > protocol-daemon handles two on-chain orchestrator responsibilities:
  > round initialization (RoundsManager.initializeRound) and reward
  > calling (BondingManager.rewardWithHint).

- The reward service is in
  [`protocol-daemon/internal/service/reward/service.go`](../../protocol-daemon/internal/service/reward/service.go) —
  one mode (`--mode=reward`) per orch deployment.
- `RewardStatus` tracks `LastRewardRound` and skip codes including
  `SkipCodeAlreadyRewarded`
  ([`protocol-daemon/internal/runtime/grpc/server.go:68-130`](../../protocol-daemon/internal/runtime/grpc/server.go)).
- Pool accounting wires LPT rewards into the same reconciliation flow as
  wholesale revenue via
  [`pool-reconciler/`](../../pool-reconciler/) — closing rounds with
  protocol-daemon timing and payment-daemon revenue.

### Operator-facing model

Total per-round revenue:

```
total_round_revenue = wholesale_wei_realised        # from served requests
                    + lpt_inflation_share_at_market # from BondingManager.rewardWithHint
                    + ticket_redemptions_confirmed  # wholesale already counted; on-chain confirmation
```

Where:

```
lpt_inflation_share = (orch_active_stake / total_active_stake)
                    × (round_inflation_pct × total_lpt_supply)
                    × performance_modifier
```

`performance_modifier` reflects the orchestrator's cumulative fees that
round — heavily-served orchestrators earn a bigger share than idle ones.
**LPT rewards are not unconditional**: they require active stake AND
some served work (or at least round initialization) to claim.

### Why this matters for pricing

For workloads with thin or negative wholesale margins (Llama 70B / H100
being the canonical case), LPT inflation can be the difference between
running the card and switching it off. An orchestrator's full economic
calculation is:

```
sustainable_per_hour = wholesale_revenue_per_hour
                     + (round_lpt_reward × lpt_usd_price) / round_hours
                     − total_cost_floor_per_hour
```

If `sustainable_per_hour > 0`, the operation makes sense even with thin
wholesale margins. If it is negative even with LPT subsidies, the
hardware should be re-purposed.

### LPT-subsidy caveats

- **Stake required.** An orchestrator with zero LPT stake earns zero
  inflation. Subsidy access requires capital allocation (buying and
  staking LPT) on top of hardware capex.
- **LPT/USD volatility.** Subsidy USD value moves with the LPT market
  price. Hedged operators model a conservative LPT/USD floor in their
  break-even math, not the current spot price.
- **Stake competition.** The total active stake grows over time;
  individual share dilutes unless the orch grows its stake proportionally.
- **Round timing.** Rewards are claimed per round (~22 hours on
  Arbitrum). An orchestrator that misses calling reward on a round
  forfeits that round's share (`SkipCodeAlreadyRewarded` in
  `protocol-daemon`).
- **Subsidies do not justify negative wholesale.** If an orch
  consistently relies on LPT to clear costs, the workload is
  economically marginal. LPT should cushion volatility, not paper over
  structural unprofitability.

### Worked example: Llama 70B / H100 with LPT subsidy

From Factor 1: wholesale revenue at 250 tok/s × full utilization =
$0.55/hr. Cost floor = $4.00/hr. Gap = $3.45/hr.

Suppose the orch has 100,000 LPT staked, total active stake is
15M LPT, and current round inflation share is approximately $581/round
at $7/LPT (illustrative numbers — re-derive against current chain state).

- LPT subsidy per round: $581
- Round duration: ~22 hours
- LPT subsidy per hour: $26.4/hr (across all GPUs the orch operates)
- If the orch operates 8 cards, subsidy per card: $3.30/hr

That subsidy mostly closes the $3.45/hr gap. With 8 H100s and current
LPT economics, the Llama 70B offering is **operationally viable** at
market anchor — but only because of stake-derived inflation rewards.
Without stake, the same hardware loses money.

This is the key insight: **wholesale price-competitiveness and LPT
subsidy access together determine offering viability**. Neither alone
is sufficient.

## Factor 3 — Premium hardware viability requires continuous validation

The Llama 70B / H100 result generalizes: **any premium-hardware,
commodity-workload combination should be treated as conditional**, not
default. Examples:

| Hardware | Workload | Default verdict |
|---|---|---|
| H100 | Llama 3 70B serving | **conditional** — depends on LPT subsidy + batched throughput |
| H100 | DeepSeek / Qwen large-context | conditional — depends on context-window demand |
| A100 | Mixtral 8x7B | likely viable but tight |
| H100 | Real-time generative video | likely viable (less commoditized) |
| 4090 | SDXL / Lightning | confidently viable |
| 4090 | Llama 3 8B | confidently viable |
| 3090 | 1080p transcode | confidently viable |
| H100 | Rerank | over-spec'd — same revenue on a 4090, much higher cost floor |

The "confidently viable" combinations should be the bulk of an
orchestrator's hardware mix. Premium hardware should be deployed only
when the demand for the corresponding premium workload has been
empirically validated — not based on theoretical market anchor pricing.

### Validation cadence

| Cadence | Activity |
|---|---|
| Daily | Monitor realised utilization + revenue per offering |
| Weekly | Compare published wholesale vs aggregated market prices |
| Monthly | Re-derive cost floor (cloud rates, ETH/USD, LPT/USD) |
| Quarterly | Revisit hardware mix; retire structurally unprofitable offerings |
| Annually | Reassess capex amortization (older cards depreciate; newer cards launch) |

### Decision triggers

Retire an offering when:

- realised revenue + LPT share < cost floor for **three consecutive
  months**, and
- demand has not visibly grown in those months, and
- price reductions in the last quarter have not improved utilization.

Add an offering when:

- demand signals show requests routing away from this orch (gateway-side
  telemetry), and
- realised throughput on existing hardware exceeds 70% utilization for
  the closest-substitute offering, and
- market anchor clears cost floor without requiring LPT subsidy to
  break even.

## Bilateral price discovery — what the protocol is missing

The on-chain + manifest layer gives orchs and gateways **one direction**
of visibility: orchestrators publish prices, gateways read them via
`Resolver.Select`. That's necessary but not sufficient for iterative
calibration. What's missing:

| Data flow | Direction | Currently available? | Why it's needed |
|---|---|---|---|
| Published wholesale price | orch → gateway | yes (manifest) | sets the baseline |
| Realised utilization | orch → market | no | tells the market who has spare capacity |
| Demand signal | gateway → orch | no | tells orchs where price could rise |
| Aggregate market clearing price | both ← aggregator | no | reference for anchoring |
| Historical price trends | both ← aggregator | no | for elasticity / curve fitting |
| Realised quality (latency, success) | orch → gateway | partial (route-health) | for price/quality tradeoff |
| LPT subsidy per offering | orch internal | yes (chain) | for total-revenue math |

### What needs to exist

Three roles, none of which currently exist in the repo:

1. **Market data aggregator** — periodically resolves all known
   orchestrator manifests, normalizes per-capability prices to a common
   unit, publishes a public price feed. Could be operated by a coordinator,
   a third party, or trustlessly via on-chain commitments + signed
   manifest scrapes.

2. **Demand signal channel** — gateways can opt-in publish anonymized
   demand telemetry: "we have 500 req/min unmet for `openai:chat-completions`
   / `llama-3.3-70b-h100` at a wholesale ceiling of $0.70/M." Orchs use
   this to decide whether to spin up capacity or raise prices.

3. **Realized-quality channel** — orchs and gateways jointly contribute
   to a shared quality registry: latency, success rate, throughput per
   offering per orch. The market clears on price + quality, not price
   alone. Today this is partially served by `gateway-route-health`, but
   it's gateway-local, not aggregated.

### Tooling additions (appended to Part 2's list)

11. **Market price aggregator** (`tools/market-feed`).
    - Inputs: list of orch eth addresses or registry crawl; periodic
      pull of signed manifests; normalization config (per-unit price
      shape per capability).
    - Outputs: hourly snapshot of all advertised wholesale prices,
      normalized to per-unit USD at a stated ETH/USD rate; CSV / Parquet
      for analysis; optional public API.
    - Plugs into: `service-registry-daemon` + an off-protocol storage
      layer. The orch-coordinator could host this as a community service.

12. **Demand signal API** (`tools/demand-signal`).
    - Inputs: gateway-side request volume per offering per time window,
      retail price at which demand was observed, success / rejection
      counts.
    - Outputs: anonymized, aggregated demand signals queryable by orchs
      ("how much unmet demand exists for offering X at price ≤ Y?").
    - Plugs into: gateway services (`openai-gateway`, `video-gateway`,
      etc.) + a coordinator-operated aggregator. Privacy-preserving
      aggregation (k-anonymity over orchs) is a design requirement.

13. **LPT subsidy modeler** (`tools/lpt-subsidy`).
    - Inputs: orch stake (chain-readable), current total active stake,
      current LPT/USD price, hardware mix.
    - Outputs: expected LPT subsidy per hour per card, with
      sensitivity bands for LPT/USD and stake dilution.
    - Plugs into: `chain-commons.controller` (BondingManager,
      RoundsManager) + an LPT price feed.

14. **Convergence simulator** (`tools/price-simulator`).
    - Inputs: workload, hardware, cost floor, market anchor, LPT subsidy
      model, demand curve assumption.
    - Outputs: simulated revenue trajectory under price adjustments,
      utilization shifts, LPT volatility. Lets operators game out
      adjustments before publishing.
    - Plugs into: synthetic; no live dependencies.

15. **Shared metrics standard** (`docs/design-docs/metrics-contract.md`,
    proposed).
    - Inputs: define a Prometheus / OpenTelemetry namespace and label
      schema that orchs and gateways both expose: `livepeer_wholesale_*`,
      `livepeer_demand_*`, `livepeer_quality_*`. Both sides agree on
      what counters / gauges to publish so that aggregators don't have
      to reverse-engineer schemas.
    - Plugs into: existing `/metrics` endpoints on each daemon (already
      present); requires a written contract, not code.

## Iterative price agreement — the meta-process

A "fair" wholesale price is not a fixed value. It's an equilibrium
between orchestrator cost + subsidy and gateway demand + retail margin,
moving continuously as:

- ETH/USD drifts (changes the wei↔USD conversion for both sides)
- LPT/USD drifts (changes orchestrator's subsidy floor)
- Cloud GPU rates drift (changes orchestrator's opportunity cost)
- Hyperscale provider pricing drifts (changes the market anchor)
- New competing orchs appear (changes the per-orch demand share)
- Hardware generations arrive (changes throughput-per-dollar)
- Customer demand for offerings rises and falls seasonally

The process operators run:

```
1. Publish initial price from cost-floor + market-anchor + LPT-subsidy expectation.
2. Monitor for one billing cycle (typically a week).
3. Read utilization, realised revenue, gateway-side rejections.
4. If utilization < break_even AND demand exists at lower prices:
       lower price by 5–10%; loop.
   If utilization > 80% AND queue depth is rising:
       raise price by 5–10%; loop.
   If utilization < break_even AND demand does not respond to price cuts:
       investigate non-price factors (latency, reliability, marketing);
       if those are tuned and still no demand, retire offering.
5. Quarterly: re-anchor against new market-feed snapshot.
6. Annually: revisit hardware mix.
```

This loop runs forever. There is no terminal "correct" price. Pricing
is an ongoing negotiation between two sides who never directly talk,
mediated by:

- the signed manifest (orch's offer)
- the resolver's `Select` (gateway's accept/reject)
- the redemption ledger (settled-revenue truth)
- shared metrics and aggregator feeds (the calibration substrate)

### What gateways and orchs commit to together

Over time the ecosystem develops norms — soft agreements that hold even
without on-chain enforcement:

- **Price stability windows**: orchs commit to not raising prices more
  than X% per week, so gateways can budget retail prices.
- **Quality SLAs**: orchs publish target latency / availability per
  offering; gateways route based on these.
- **Demand commitments**: high-volume gateways pre-allocate to specific
  orchs in exchange for price ceilings; the protocol doesn't enforce
  this, but reputation and route-health data make it self-policing.
- **Volume-discount tiers**: same offering published at multiple
  `offering_id`s with volume-band-keyed prices, gateways select based
  on their expected throughput.

None of this is currently codified in the protocol. The substrate
(signed manifests, off-chain telemetry, public chain ledger) supports
all of it once the data-sharing tooling above is built. Until then,
price agreement is bilateral, manual, and stale.

## Summary — the three factors restated

1. **Volume**: a price is only as good as the utilization it earns at.
   The orchestrator's job is not just to publish a correct price — it's
   to earn enough demand at that price to clear the cost floor.

2. **LPT subsidy**: the protocol's inflation rewards close part of the
   gap on thin-margin offerings. Operators with stake have a structural
   advantage; unstaked operators must clear cost on wholesale revenue
   alone. The repo wires this through
   [`protocol-daemon/internal/service/reward`](../../protocol-daemon/internal/service/reward/service.go)
   and reconciles it via [`pool-reconciler/`](../../pool-reconciler/).

3. **Continuous validation**: premium hardware running commodity
   workloads should be treated as conditional, not default. Validate
   demand empirically before committing. Retire offerings that fail
   the validation triggers above. Build hardware mix around
   confidently-viable combinations; reserve premium capacity for
   workloads where the market anchor and demand both justify it.

The bilateral data-sharing tooling (items 11–15 above) is the
infrastructure that lets factors 1, 2, 3 be managed continuously rather
than reasoned about in isolation. Building it is the highest-leverage
next step for making pricing on this network transparent and fair.

---
plan: 0038
title: Metrics coverage for payment-daemon, service-registry-daemon, and the broker's dependency surfaces
status: active
phase: implementation
opened: 2026-05-27
owner: harness
related:
  - "completed plan 0016 — chain-integrated payment design"
  - "completed plan 0018 — orch-coordinator design"
  - "completed plan 0024 — quote-free ticket-params flow"
  - "design-doc docs/design-docs/backend-health.md"
---

# Plan 0038 — metrics coverage for payment-daemon, service-registry, and broker dependency surfaces

## 0. Why this plan exists

The capability-broker already exposes a mature Prometheus `/metrics` surface
(`internal/observability/metrics.go`, served on a dedicated listener via
`internal/server/metrics_server.go`, default `:9090`). It covers request
volume/latency, backend selection, metadata refresh, and Pool-snapshot state.

Two of the broker's load-bearing dependencies are nonetheless invisible, and one
sibling daemon ships a full catalog that is switched off by default:

- The broker's **payment-daemon client calls** (`internal/payment`,
  `OpenSession`/`ProcessPayment`/`DebitBalance`/`SufficientBalance`/`GetTicketParams`)
  emit **no** latency, error, or outcome metrics.
- The broker's **registry serving** (`internal/server/registry`,
  `/registry/offerings` + `/registry/health`) bypasses the paid-route metrics
  middleware and is uninstrumented.
- The **payment-daemon** has **no Prometheus at all** — it is gRPC-over-unix-socket
  only, with no `/metrics` listener and no metrics flag.
- The **service-registry-daemon** has a rich catalog (`livepeer_registry_*`) behind a
  private registry, but its HTTP listener is **disabled by default**
  (`--metrics-listen` empty).

## 1. "Enable all possible metrics" — what that means per component

| Component | Infra today | Listener today | "Enable all" =|
|---|---|---|---|
| capability-broker | mature (`promauto`, global registry) | on by default | **coverage fill** (payment client, registry serving) — enablement already done |
| service-registry-daemon | mature (private registry, `Recorder`, metered decorators) | **off** (`--metrics-listen` empty) | **set one flag** → whole catalog lights up; then audit + scrape |
| payment-daemon | none | **none** | **build it** (registry + recorder + gRPC interceptor + HTTP listener + flag), then instrument |

## 2. Conventions to settle first

1. **Namespaces.** payment-daemon → `livepeer_payment_*` (sibling of `livepeer_registry_*`).
   Broker additions keep the broker's bare `livepeer_*` style; registry-serving metrics use a
   `livepeer_broker_registry_*` prefix to avoid clashing with the daemon's `livepeer_registry_*`.
2. **Registry pattern for payment-daemon.** Adopt the service-registry-daemon pattern (private
   `prometheus.NewRegistry()` + a `Recorder` interface + `WithMetrics(...)` decorators +
   `promhttp.HandlerFor`). Not the broker's global `promauto`. It is the more testable of the two
   patterns already in the repo.
3. **Cardinality guardrails.** Never label by `sender` address, `work_id`, ticket hash, or
   `nonce` (all unbounded). Bounded labels only: `capability`, `offering`, `method`, `result`,
   `reason`, `code`, `role`.
4. **Wei-as-counter caveat.** `_wei_total` counters lose float64 precision near 2^53. For EV/credit
   totals use gwei, or pair a count counter with a last-value gauge.

## 3. Workstream A — broker client-side metrics

### 3.1 Payment-daemon client (decorator on `payment.Client`)

Add `payment.WithMetrics(Client) Client` wrapping every interface method; no changes to
`grpc.go` or call sites.

| Metric | Type | Labels |
|---|---|---|
| `livepeer_payment_client_requests_total` | counter | `method`, `result` (`ok`/`error`), `code` |
| `livepeer_payment_client_request_duration_seconds` | histogram | `method` |
| `livepeer_payment_client_in_flight` | gauge | `method` |
| `livepeer_payment_daemon_up` | gauge | — (periodic `Health` probe) |

`method` ∈ `open_session`, `process_payment`, `debit_balance`, `sufficient_balance`,
`get_balance`, `close_session`, `get_ticket_params`.

Optional (broker already receives these in `ProcessPaymentResult`):
`livepeer_payment_client_tickets_rejected_total{reason}`, `..._winners_queued_total`. Double-counts
the daemon-side equivalents; keep only where the broker `/metrics` is scraped standalone.

### 3.2 Registry serving (`/registry/offerings`, `/registry/health`)

| Metric | Type | Labels |
|---|---|---|
| `livepeer_broker_registry_scrape_total` | counter | `endpoint` (`offerings`/`health`), `code` |
| `livepeer_broker_registry_scrape_duration_seconds` | histogram | `endpoint` |
| `livepeer_broker_registry_published_offerings` | gauge | — |
| `livepeer_broker_registry_payload_bytes` | histogram | `endpoint` |

## 4. Workstream B — service-registry-daemon: enable the catalog

1. Turn the listener on in deployment: set `--metrics-listen` (e.g. `:9091`) in infra manifests.
2. Audit for gaps: confirm `grpc_requests_total` carries `code`/`result`; confirm every provider
   decorator (`chain`, `manifestfetcher`, `audit`, `manifestcache`, `resolver`, `publisher`) is
   wired in `run.go`/`providers.go`; add a `resolutions_total{result}` breakdown if missing.
3. Document the catalog + scrape config in the component's `docs/`.

## 5. Workstream C — payment-daemon: greenfield `/metrics`

### 5.1 Scaffolding
- `internal/providers/metrics` package: private registry, `Recorder` interface, `NewPrometheus`,
  standard Go/process collectors, `build_info`, `uptime_seconds`.
- gRPC `UnaryServerInterceptor` → `grpc_requests_total{role,method,code}` +
  `grpc_request_duration_seconds` + `grpc_in_flight` (both `PayeeDaemon` and `PayerDaemon`).
- New HTTP `/metrics` listener + `--metrics-listen` flag (empty disables).

### 5.2 Receiver domain
| Metric | Type | Labels |
|---|---|---|
| `livepeer_payment_sessions_open` | gauge | — |
| `livepeer_payment_sessions_total` | counter | `event` (`opened`/`already_open`/`closed`) |
| `livepeer_payment_tickets_total` | counter | `result` (`accepted`/`rejected`) |
| `livepeer_payment_tickets_rejected_total` | counter | `reason` |
| `livepeer_payment_winning_tickets_total` | counter | — |
| `livepeer_payment_credited_ev_gwei_total` | counter | — |
| `livepeer_payment_debits_total` | counter | `result` (`applied`/`idempotent_retry`) |
| `livepeer_payment_work_units_debited_total` | counter | `capability`, `offering` |

`reason` ∈ `invalid_recipient_rand`, `nonce_replay`, `nonce_cap`, `invalid_signature`, `other`.

### 5.3 Settlement / redemption loop
| Metric | Type | Labels |
|---|---|---|
| `livepeer_payment_redemptions_total` | counter | `result` (`redeemed`/`expired`/`already_used`/`face_value_too_low`/`insufficient_funds`/`tx_error`) |
| `livepeer_payment_redemption_duration_seconds` | histogram | — |
| `livepeer_payment_redemption_queue_depth` | gauge | — |
| `livepeer_payment_redemption_tx_total` | counter | `result` (`submitted`/`confirmed`/`failed`) |
| `livepeer_payment_gas_price_wei` | gauge | — |

### 5.4 Escrow (aggregate only)
`livepeer_payment_escrow_pending_float_wei` (gauge), `livepeer_payment_tracked_senders` (gauge),
`livepeer_payment_escrow_rebuilds_total` (counter).

### 5.5 Chain / clock / gas providers
`livepeer_payment_chain_reads_total{method,result}`,
`livepeer_payment_chain_read_duration_seconds{method}`,
`livepeer_payment_chain_writes_total{method,result}`,
`livepeer_payment_chain_last_success_timestamp_seconds{method}`,
`livepeer_payment_current_round` (gauge),
`livepeer_payment_clock_refresh_total{result}`,
`livepeer_payment_gasprice_refresh_total{result}`.

### 5.6 Sender mode
`livepeer_payment_payments_created_total{result}`, `livepeer_payment_tickets_signed_total`,
`livepeer_payment_ticketparams_fetches_total{result}`,
`livepeer_payment_ticketparams_fetch_duration_seconds`, `livepeer_payment_sender_sessions` (gauge),
`livepeer_payment_deposit_wei` / `_reserve_wei` (gauge).

## 6. Workstream D — dashboards + alerts

Per-component Grafana boards plus a cross-cutting "payment path" board joining broker client-side
latency → payment-daemon RPC latency → redemption outcomes. Candidate alerts: redemption failure
rate, `redemption_queue_depth` growth, `chain_last_success` staleness, `payment_daemon_up == 0`,
ticket-rejection spike by reason, registry scrape error rate, cache hit-ratio collapse. Store rules
+ dashboard JSON under `infra/`.

## 7. Sequencing

1. Broker payment-client decorator (§3.1) — isolated, immediate, no daemon changes.
2. Broker registry-serving metrics (§3.2).
3. Service-registry: flip `--metrics-listen` + audit + docs (§4).
4. Payment-daemon scaffolding (§5.1).
5. Payment-daemon domain (§5.2–5.6) — receiver → settlement → sender, separate PRs.
6. Dashboards + alerts (§6).

## 8. Status (implemented)

All workstreams landed in the working tree (uncommitted). Notes on deviations from
the plan as written:

- **A — broker (done).** `payment.WithMetrics` decorator + `livepeer_payment_client_*`;
  `instrumentRegistryScrape` + `livepeer_broker_registry_*`. Runbook §2.8 updated. Alert
  rules: `capability-broker/docs/operations/prometheus/alerts.yaml`. The `livepeer_payment_daemon_up`
  gauge was **deferred** — it needs a periodic Health-probe loop; the broker instead surfaces
  daemon reachability through the client RPC error rate (`result="Unavailable"`).
- **B — service-registry (done, mostly pre-existing).** The catalog, Grafana dashboard, and
  alerts already shipped under the daemon's completed plan 0003. The catalog audit found no
  real gaps (`grpc_requests_total{service,method,code,registry_code}`, `resolutions_total{mode,freshness}`,
  etc.). The actual gap was the **secure-orch-control-plane compose publishing port 9095 with no
  `--metrics-listen` flag** (a dead port) — now fixed.
- **C — payment-daemon (done, greenfield).** New `internal/providers/metrics` package
  (private registry, `Recorder`, `Noop`, `livepeer_payment_*`), gRPC interceptor (both roles),
  `--metrics-listen` flag + HTTP listener, build_info/uptime. Domain wired into receiver,
  settlement, escrow, sender, and a metered `Broker` decorator for chain reads/writes. The
  `clock_refresh_total` / `gasprice_refresh_total` counters from §5.5 were **not** added as
  standalone metrics (they live in the onchain providers' background tickers); their signal is
  covered by the `current_round` + `gas_price_wei` gauges and the chain read/write metrics.
  Runbook §8 replaced with the real catalog.
- **D — dashboards + alerts (done).** Payment-daemon:
  `docs/operations/{prometheus/alerts.yaml, grafana/livepeer-payment-daemon.json, README.md}`.
  Capability-broker: `docs/operations/{grafana/livepeer-capability-broker.json,
  prometheus/alerts.yaml, README.md}`. Orch-coordinator:
  `docs/operations/{grafana/livepeer-orch-coordinator.json, README.md}` (covers the existing
  `orch_coordinator_*` catalog; metrics already on by default at `:9091`). Coordinator alert
  rules are a noted follow-up (manifest age, broker health, publish outcomes are the natural
  starting points).

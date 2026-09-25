# capability-broker — operator runbook

Cross-cutting operational guidance for running `livepeer-capability-broker`
on an orchestrator host. Pairs with `payment-daemon/docs/operator-runbook.md`
(payment-side concerns) and the spec subfolder
(`livepeer-network-protocol/`) for wire-shape questions.

Rewritten 2026-08-19 for the v1 protocols (`paid-job/v1`,
`paid-session/v1`). The mode-era RTMP/HLS pipeline sections are gone with
the modes.

## 1. Listener topology

| Flag | Default | Purpose | Reachability |
|---|---|---|---|
| `--listen` | `:8080` | Paid surface: `POST /v1/job`, `/v1/session/*` (incl. the runner events endpoint), plus `/registry/*` and ticket-params. | Gateway- and runner-reachable (LAN or public, operator's call). |
| `--metrics` | `:9090` | Prometheus scrape endpoint. | Operator's metrics network only. |

The admin surface (`/admin/v1/*`) rides the paid listener but must be
protected by `admin_auth` and network policy — see §1.1.

`external_base_url` in host-config is the gateway-reachable route identity of
the paid listener and remains the origin bound into spend authorizations and
session control URLs. `runner_callback_base_url` may name a distinct trusted
origin for runner events and certification callbacks (for example, a private
service name); it defaults to `external_base_url`. Neither value is derived
from inbound request headers. If the callback origin is wrong, runners post
events into the void and sessions die by heartbeat loss.

## 1.0 Settlement signing key

`identity.settlement_key_file` names the hot secp256k1 key this broker
signs settlement records with. Without it every record goes out unsigned
and a clearinghouse refuses it for anything financially material.

```sh
# mint (0600, refuses to overwrite) — prints the public key
livepeer-capability-broker settlement-key generate --out /etc/livepeer/broker-settlement.key
# read the public half back later
livepeer-capability-broker settlement-key pubkey --file /etc/livepeer/broker-settlement.key
```

The broker announces the key at `GET /registry/settlement-keys`
(broker-admin §7.1): a statement naming the orch, the key, this broker's
`external_base_url` and its configured window, signed by the key itself.
The orch-coordinator reads it on scrape, verifies the proof, and carries
the key into the manifest candidate; the cold key delegates it when the
operator signs. Nothing is copied by hand.

**The one trap:** `external_base_url` is inside the signed statement, and
the coordinator compares it (scheme and host) against the `base_url` it
scraped this broker at. If the broker says its public hostname while
`coordinator-config.brokers[].base_url` points at a LAN address, the
coordinator's roster shows the key as `unproven: statement names a
different broker URL` and does not delegate it. Make the two agree, or
leave `external_base_url` unset on a broker with no paid-session offers.

`settlement_key_not_before` / `settlement_key_expires_at` are optional.
Leave them unset: the coordinator assigns a window from first sight
(`publish.settlement_key_validity`, default one year), remembers it, and
renews it by a sign cycle. Set them only to make the broker refuse to sign
outside a window you chose, and then mirror the published one.

Rotate by generating a new file, pointing `settlement_key_file` at it and
restarting. The old key stays delegated until its window closes.

## 1.1 Runtime reload in production

Validate a candidate without starting a broker or dialing payment-daemon:

```bash
livepeer-capability-broker config validate --config /etc/livepeer/host-config.yaml
```

For a standalone/file-sourced broker, runtime reload remains an operator
workflow:

- `host-config.yaml` must live at a stable path the broker can re-read
- broker private admin auth must be enabled
- `GET /admin/v1/runtime` and `POST /admin/v1/runtime/reload` must be
  reachable only over a private operator path

The normal production sequence is:

1. the operator stages and validates a new `host-config.yaml`
2. the operator calls broker reload
3. broker emits a broker-local reload `attempt_id`
4. operator automation confirms:
   - broker `last_reload_attempt_id` matches the triggered attempt
   - broker `loaded_revision` matches the staged config

For a Pool broker, set `offers_source: admin`. Static identity, delegated
settlement key, route, store, and callback configuration still follows the
operator workflow above, but pool-controller never renders or reloads that
file. It changes commercial state with idempotent offer and credential pushes
over the broker admin API.

Do not treat file placement alone as convergence. Note that removing a
paid-session capability while sessions for it are active leaves those
sessions unroutable: they wind down via heartbeat enforcement rather than
crashing, but prefer draining first.

## 1.9 Credential store

`credential_store.path` is a second bbolt file with the same persistence
and backup rules as the session store; `sealing_key_file` may be the same
key. Losing the file orphans every runner enrollment (hosts re-enroll);
losing the key makes it unreadable. Enrollment, rotation, and revocation
are admin-API gestures — see
[`design-docs/credential-store.md`](./design-docs/credential-store.md).

## 2. Durable state store

`session_store` in host-config is the broker's persistence layer:
paid-session authority (identifiers, payment counters, usage watermarks,
sealed descriptor private parts) and paid-job idempotency records share
one bbolt file.

```yaml
session_store:
  path: /var/lib/livepeer/broker-state.db
  sealing_key_file: /etc/livepeer/broker-seal.key   # 32 raw bytes or 64 hex chars
```

Operational rules:

- **The path must be a persistent volume.** Losing the file orphans every
  active session: runners keep serving and posting events that 401, and
  wholesale-account reservations remain unresolved. Broker restart recovery
  only works when both this file and the payment daemon's authorization state
  survive.
- **The sealing key is not rotatable in place** (v1): records sealed under
  the old key fail closed on read. Treat key loss as state loss — sessions
  become unreadable and wind down terminally. Back the key up with the same
  care as the payment daemon's keystore.
- **Authorization watermarks in this file are money.** The payment daemon
  durably remembers authorization and advance sequence results. Restoring the
  broker and daemon from inconsistent points can leave work conservatively
  unresolved. Snapshot their stores at one coordinated point and reconcile
  every active authorization after restore.
- Wrong-size key files fail startup loudly; a missing `session_store` with
  paid-job capabilities runs with **in-process** idempotency and logs a
  warning — acceptable for dev, non-conformant for production.
- Housekeeping is automatic: terminal session records evict after the
  retention window, job records after the 24h idempotency window.

## 3. paid-session operations

Per-offering knobs (host-config `session:` block):

| Knob | Default | Meaning |
|---|---|---|
| `heartbeat.interval_seconds` / `missed_threshold` | 10 / 3 | A runner silent past `interval × threshold` is torn down (`heartbeat_lost`): runner terminated, payment closed, capacity released. |
| `lease_max_seconds` | 3600 | Operator cap on the funding-tracking lease. |
| `burn_rate_per_second` | 1 | Units/second estimate used to convert runway into lease time. |
| `min_runway_units` | 0 (policy-derived) | Minimum authorization reservation requested at open and after usage advances; the signed cap still bounds it. |
| `runner.create_path` / `status_path` / `terminate_path` | — | The runner's session API paths (`{id}` substituted). No default URL space exists. |

Terminal `close_reason` values you will see in status responses and logs:
`gateway_close`, `runner_ended`, `runner_failed`, `lease_expired`,
`heartbeat_lost`, `insufficient_balance`, `recovery_failed`,
`open_failed`, `output_failed`. Every winddown is the same idempotent path
(terminate runner → persist stopped state and release capacity → settle authorization → record reason); a repeated
trigger is a no-op.

Restart behavior: before serving, the broker verifies that every nonterminal
record names a durable account authorization. It then queries the payment
daemon and runner. Both still hold it → resume with the same authorization,
credentials, grants, and usage watermark. Missing authorization state or a
lost runner → terminate fail closed as `recovery_failed`. An undrained legacy
record without authorization state refuses broker startup.

### Concurrency and pending cleanup

`offers[].capacity.max_in_flight` limits each runner's host/local-capability pair.
Starting and active sessions each occupy one slot; replay, status, control sockets
and refill use the same slot. Unary, multipart and streamed jobs occupy one slot
until the response completes or the exchange aborts. A zero limit is unlimited.
`queue_limit` is inactive and does not admit extra waiting requests. At capacity,
the broker tries another eligible runner, otherwise returns `503
capacity_exhausted` with `Livepeer-Backoff`.

Before serving after restart, the broker restores opening and live session
ownership from the sealed store. Existing records without an ownership reference
are migrated. Live work without a pinned runner binding fails startup rather than
being left out of occupancy. Lowering a limit does not discard existing occupancy: new work
waits for enough slots to become free by receiving capacity refusals.

Runner disconnects and failed termination retain ownership. A confirmed stop is
persisted before freeing capacity, even when a receiver outage leaves the session
`winding_down`. Settlement and signed terminal publication still require all
financial obligations to be resolved. Repeated cleanup does not release another
session's slot.

An abandoned open with a known runner session ID is retried on recovery/sweep.
For a lost create response, runners can advertise `paths.reconcile`. The broker
posts its durable broker session ID and accepts either an existing runner ID
(which it terminates) or `fenced` (the runner has durably blocked delayed creates
for that ID). A timeout, bare 404, unsupported endpoint or invalid reply retains
the opening reservation and slot. Deploy the broker before runners advertise
the optional path; older strict attach validators reject unknown path fields.

Before initial admission, the broker seals the exact signed authorization and
recovery identity. Recovery atomically fences unused authority at the receiver,
or adopts an accepted admission and settles its verified cumulative usage.
Compute may be released while accounting remains pending. Recovery never repeats
funding. `GET /v1/exchange/{request_id}` reports `IN_FLIGHT`,
`ACCOUNTING_PENDING`, `ADMISSION_REJECTED`, or a terminal `SETTLED` envelope.
A rejected open can obtain scoped signed non-admission; pending admission cannot.
Canceled requests cannot open again, including after restart.

Pre-upgrade reservations missing the exact authorization cannot be automatically
financially reconciled. Those missing broker create identity or a supported runner
reconciliation path also cannot establish an unknown runner outcome. Diagnose
using the request ID, broker session ID and backend binding; do not delete the
reservation or reset counters to make capacity appear free. Keep the broker
sealing key and runner state/key across upgrades and restarts.

The limit applies within one broker. Runners remain responsible for physical
capacity across brokers, and for canceling job execution when its transport ends.

### Authorization revisions and stuck winddown

Revision reservations use the successor's remaining cumulative debit allowance,
after receiver billing is reconciled. A 120-unit runway against a 180-unit
successor with 74 units already billed reserves 106 units at the accepted price.
Both HTTP and control WebSocket top-ups use the same engine path.

Upgrade the payment daemon before the broker: recovery uses the additive
`CancelAuthorizationAdmission` RPC. An older receiver returns `Unimplemented`;
the broker retains an uncertain intent until the recovery RPC is available.
No new authorization, ticket batch or session ID is needed to repair an old
oversized intent. Startup and sweeps repair active-session reservations before
replaying the original admission. During winddown, an unadmitted successor is
atomically fenced; an already-admitted successor is adopted and settled.
An expired-unused revision becomes a durable `refill_refused` response.

Before repairing a reported production session, verify the running broker and
receiver image digests/source revisions and back up their stores together with
the broker sealing key. Inspect the sealed intent through the normal store
reader in a protected environment, retaining only sanitized authorization IDs,
limits, reservation, cumulative billing and sequence in the incident record.
Do not delete intents or hand-edit Bolt records: admission may have succeeded
before its response was lost.

`session revision recovery held` means accounting remains uncertain. Background
retries increase from one second to a one-minute cap, with the next retry time
persisted across restart; an explicit identical top-up retry may bypass that
cooldown. Runner termination continues while payment closure is pending.
`session revision refused; predecessor retained` records a resolved refusal.
Persistent receiver conflicts or outages remain visible as `winding_down` and
must not be treated as a final zero-use settlement.

Revision decisions now preserve the original safe receiver refusal before
cancellation. The `session revision decision` log includes session, gateway,
request, predecessor/successor, revision, outcome, stage, reason, receiver code,
requested/effective reservation, receiver cumulative billing and signed caps.
It never includes authorization/payment bytes. The metric
`livepeer_protocol_session_revision_decisions_total{outcome,stage,reason}` counts
observations (including retries), with bounded labels and no identity labels.
`revision_pending` means retry the identical request or read
`GET /v1/session/{id}/revisions/{request_id}`; a final `refill_refused` response
retains the predecessor. Signed revision evidence gives LOC an independently
queryable successor outcome; the generic non-admission endpoint cannot replace it.
Previously erased rejection reasons cannot be reconstructed by upgrading.

Terminal settlement now freezes its payload and positive broker session sequence
durably. Repeated settlement lookup or close replays the original signed envelope.
Historical terminal records without a snapshot are repaired once before their
next publication. Do not substitute the receiver's sequence into the signed
envelope. A previously rejected sequence-zero terminal can be fetched again after
upgrade; LOC must verify the replacement normally. Existing frozen evidence can
be served even if its offering is subsequently removed.

`authorization_exhausted` with 124 claimed / 120 debited is a valid cap overshoot
when receiver accounting and signed limits confirm it. Only 120 is billed;
`claim_debit_gap_reason: authorization_cap` distinguishes this from an unexplained
gap. A refused successor's `canceled_unused` state survives receiver restart and
prevents future admission; its signed evidence applies to the successor alone.
See the [wire contract](../../livepeer-network-protocol/protocols/paid-session.md#revision-decisions-and-evidence)
for identity checks, retention boundaries and replay rules.

After recovery, verify the active receiver authorization is settled, its
reservation is released, and the signed settlement fetched by broker or gateway
session ID matches cumulative units and billed wei and retains the original
close reason. For a final total of 74 units at 10^12 wei/unit, the bill is
74 × 10^12 wei. The temporary 106 × 10^12 wei reservation is not another charge.
Insufficient wholesale credit remains a separate admission/funding issue.

Run `make test-revisions` from `capability-broker/` for Docker-based race tests
covering the real receiver ledger, restart/lost-response recovery, HTTP/WS
parity and signed cumulative settlement.

Output-producing runners may additionally report `output_state` as `waiting`,
`producing`, or `stalled`, plus a sanitized `last_failure_code`. These are
independent from heartbeat liveness: a stalled callback proves the runner is
responsive, not that customer output is healthy. Status reports `unknown` for
older runners. A continuously stalled session is wound down after 60 seconds
as `output_failed`; the runner may enforce a tighter workload-owned deadline.
Watch `livepeer_protocol_session_output_health_total{state="stalled"}` together
with `livepeer_protocol_session_winddowns_total{reason="output_failed"}`.

### 3.1 Runner self-description

Several fields above are facts only the runner knows: `descriptor_schema`,
`work_unit.name`, the runner's own paths, `metering`. Declaring them here
as well creates two sources of truth, and the broker's runtime checks
exist only because they can disagree — a `work_unit` mismatch rejects
**every** usage event for a session's lifetime, which is an expensive way
to learn about a typo.

Runner facts are no longer configured here at all: a runner attaches and
declares them, and the broker freezes them into the offer
(`runner-attach.md`). A capability the broker rejects at attach is
visible on the coordinator's Runners page with the disagreeing field
named — see plan 0043 item 11 for what replaced the describe path and
its quarantine behaviour.

## 4. paid-job operations

- Idempotency: gateways retry with the same `Livepeer-Request-Id` and
  converge on the recorded outcome. An exchange interrupted mid-flight
  blocks its request id for 10 minutes, then retries converge on a failed
  terminal record.
- The usage claim (`Livepeer-Work-Units`) is on every terminal response —
  header for `unary`/`multipart`, HTTP trailer for `stream`. A gateway
  complaining about missing stream claims is usually an intermediary
  buffering proxy stripping trailers: the paid listener must be reachable
  without a trailer-stripping hop.
- Buffered bodies are capped at 64 MiB per exchange.

### Wholesale accounts

Configure `external_base_url` exactly as payer services use it; authorization
admission rejects a different `broker_uri`. Wholesale accounts are mandatory
for every paid offer and require no `extra.features.wholesale_accounts` flag.
Every job, session open, and session refill requires
`Livepeer-Authorization`; `Livepeer-Payment` alone never admits work.

`POST /v1/payment/account` returns the receiver's read-only account or
authorization observation. Serve it only over the configured HTTPS origin. It
does not grant spend authority, but it reveals wholesale balance and should be
rate-limited at the edge.

`POST /v1/payment/account/fund` accepts a payer-signed `Livepeer-Payment` with
the matching `Livepeer-Capability` and `Livepeer-Offering` headers. It adds
ticket EV to the stable account but cannot reserve or invoke work. This is the
aggregate replenishment path for out-of-path payers; it does not require a
customer session credential or SDK callback. Rate-limit it for resource
protection, but retries are economically idempotent by ticket nonce.

An out-of-path funding intermediary calls this endpoint itself before handing
the workload-bound authorization and broker URL to the end caller; the caller
does not relay the funding ticket.

This release is a hard cut. Before deployment, stop new admissions and drain
all payment-only jobs, sessions, and debit retries. Migrate verified residual
generation balances exactly once, then upgrade broker, receiver, and payer
clients together. Do not synthesize authorization state for an existing
session; mixed versions fail closed.

For sessions, the broker reserves a heartbeat-sized runway rather than the
full cumulative cap. Runner usage advances the cumulative debit and replaces
runway atomically. Session refills carry a successor authorization naming the
predecessor and may also carry only the ticket shortfall needed by the
aggregate account.
A settlement RPC left uncertain remains `accounting_pending` and retries
idempotently rather than releasing value after delivered work.

## 5. Metrics

Registry surface metrics (`livepeer_broker_registry_*`) are unchanged.
Protocol-engine metrics (all on the `--metrics` listener):

| Metric | Labels | Meaning |
|---|---|---|
| `livepeer_protocol_session_opens_total` | `outcome` (opened\|replayed\|failed) | Session opens. A rising `failed` usually means runner create or descriptor validation failures. |
| `livepeer_protocol_session_winddowns_total` | `reason` (the stable close reasons in §3) | Terminal winddowns. Alerting rules for `heartbeat_lost` and `recovery_failed` ship in `docs/operations/prometheus/alerts.yaml`. |
| `livepeer_protocol_session_events_total` | `outcome` (accepted\|duplicate\|rejected\|retryable\|unauthorized) | Runner event intake. `unauthorized` spikes suggest a stale runner or probing; sustained `retryable` means payment-daemon trouble. |
| `livepeer_protocol_session_debited_units_total` | — | Units debited from usage claims (the seller's meter, aggregated). |
| `livepeer_protocol_job_exchanges_total` | `transport`, `outcome` (ok\|client_error\|backend_error\|replayed\|refused) | paid-job exchanges. `replayed` is gateways exercising idempotency; `refused` is transport negotiation misses. |

Per-capability request rate, error ratio, and latency come from the paid
middleware as `livepeer_paid_requests_total{capability,offering,outcome}`,
`livepeer_paid_request_duration_seconds`, and
`livepeer_paid_work_units_total` — these carry the capability/offering
labels the `livepeer_protocol_*` counters deliberately do not.

The `paid request` structured log lines (request id, capability,
protocol, status, work units, duration) remain the per-exchange trace.

**Alerting.** `docs/operations/prometheus/alerts.yaml` ships rules for
both protocol surfaces. The one to understand before it pages you is
`BrokerSessionsDyingByHeartbeat`: it fires when `heartbeat_lost`
*dominates* winddowns rather than merely occurring, because that pattern
is the signature of a wrong `external_base_url` — runners cannot reach
the callback URL, so every session opens, reports nothing, and dies on
schedule. Thresholds in that file are starting points sized for a busy
orchestrator; tune them to your own session volume.

## Settlement-domain upgrade (protocol major 4)

Independent financial ledgers under one payee now have distinct persistent
`settlement_domain_id` values. Read the
[identity and migration contract](../../docs/design-docs/settlement-domain-identity.md)
before upgrading. Drain old authorizations, back up the complete ledger, initialize
the payment daemon, upgrade the broker/coordinator, cold-sign the new routes and
update registry and payer/LOC clients together. An ID is generated once by the
receiver; `--settlement-domain-id` is optional bootstrap import on payment-daemon,
and a configured/stored mismatch refuses startup. Broker configuration does not
own this value.

Clients must retain the ID from `SelectedRoute.settlement_domain_id`, compare it
with `/v1/payment/account` and the ticket-parameter response, include it in funding
intents and spend authorizations, and compare it again on settlement. Account
versions are independent across domains. URL changes do not transfer balances.
The chain probe requires `--settlement-domain-id` from the signed route.


## Definitive payment rejection recovery

`payment_rejected` retains the exact authorization for a definitive receiver
insufficient-balance refusal before runner execution. Exchange lookup reports
`ADMISSION_REJECTED` until the existing non-admission endpoint issues signed proof.
After expiry that endpoint validates the full scope and calls the receiver's
private unexecuted-authorization fence. Receiver outages return 503 without proof;
scope mismatches or unexpired authorization return 409. Successful evidence is
persisted and replayed verbatim; the request cannot run again.

Deploy this broker change before coordinated consumers that depend on recovery
(BlueClaw be-vtu). Preserve the Bolt store and its evidence horizon. Do not delete
admission tombstones or rewrite old terminal records from HTTP logs: historical
ambiguous outcomes lack the new durable refusal evidence. This change does not
automatically repair those records or change deployed image tags. The associated
implementation bead is lnm-9da.

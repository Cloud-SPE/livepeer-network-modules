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

`external_base_url` in host-config is the externally-reachable base of the
paid listener. It is the **only** source for runner callback URLs and
session control URLs — the broker never derives them from inbound request
headers. If it is wrong, runners post events into the void and sessions die
by heartbeat loss: check it first when sessions open and then wind down
with `heartbeat_lost`.

## 1.1 Runtime reload in production

When the broker participates in the Pool control-plane apply path:

- `host-config.yaml` must live at a stable path the broker can re-read
- broker private admin auth must be enabled
- `GET /admin/v1/runtime` and `POST /admin/v1/runtime/reload` must be
  reachable from `pool-controller` over a private path only

The normal production sequence is:

1. `pool-controller` stages a new `host-config.yaml`
2. `pool-controller` calls broker reload
3. broker emits a broker-local reload `attempt_id`
4. `pool-controller` confirms:
   - broker `last_reload_attempt_id` matches the triggered attempt
   - broker `loaded_revision` matches the controller desired revision

Do not treat file placement alone as convergence. Note that removing a
paid-session capability while sessions for it are active leaves those
sessions unroutable: they wind down via heartbeat enforcement rather than
crashing, but prefer draining first.

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
  payee-side payment sessions are left open. The broker's restart recovery
  (rebind-or-terminal) only works when the file survives.
- **The sealing key is not rotatable in place** (v1): records sealed under
  the old key fail closed on read. Treat key loss as state loss — sessions
  become unreadable and wind down terminally. Back the key up with the same
  care as the payment daemon's keystore.
- **The debit-sequence counters in this file are money.** The payee daemon
  durably remembers `(sender, work_id, debit_seq)`; a broker restored from
  an old snapshot re-uses sequence numbers and its real debits are silently
  swallowed as replays (revenue loss, not double-billing). Snapshot the
  state file only together with, or after, payment-daemon state — never
  from before it.
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
| `min_runway_units` | 0 (off) | Post-debit `SufficientBalance` floor; breach winds the session down with `insufficient_balance`. |
| `runner.create_path` / `status_path` / `terminate_path` | — | The runner's session API paths (`{id}` substituted). No default URL space exists. |

Terminal `close_reason` values you will see in status responses and logs:
`gateway_close`, `runner_ended`, `runner_failed`, `lease_expired`,
`heartbeat_lost`, `insufficient_balance`, `recovery_failed`,
`open_failed`. Every winddown is the same idempotent path (terminate
runner → close payment → release capacity → record reason); a repeated
trigger is a no-op.

Restart behavior: on startup the broker queries each active session's
runner. Runner still holds it → rebound silently (same `work_id`,
credentials keep working, grants are never re-minted). Runner lost it →
`recovery_failed` terminal. Runner unreachable → left active for heartbeat
enforcement to decide.

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

## 5. Metrics

Registry surface metrics (`livepeer_broker_registry_*`) are unchanged.
Protocol-engine metrics are being added with the v1 engines; until the
instrumentation lands, session and job health are observable via the
`paid request` structured log lines (request id, capability, protocol,
status, work units, duration) and session status endpoints.

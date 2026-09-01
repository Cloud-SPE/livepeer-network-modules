---
spec_name: certification-steps
version: 1.1.0-draft
status: draft
last_updated: 2026-09-01
---

# Certification steps

How an offer proves a runner can actually serve it, before the runner
gets work or freezes the offer's shape. Four workload-agnostic step types
— `readiness`, `request`, `usage`, `latency` — authored as data on the
offer (standalone) or the pool template, executed by the broker's step
engine through the runner's attach connection, recorded per runner ×
offer (plan 0043 §3.5, decision 6b).

This replaces `pool-controller`'s hardcoded probe families
(`probes.go`: chat, embeddings, audio, speech, transcode) with step
*config* — §7 shows each family written as steps. The controller keeps
the probation/active ladder and reads these results as its input.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as in
RFC 2119. Companion specs: [`runner-attach.md`](./runner-attach.md)
(the runner the steps run against), [`broker-admin.md`](./broker-admin.md)
§6 (where results are read and runs are triggered).

## 0. Contents

1. [Who authors, who runs, who reads](#1-who-authors-who-runs-who-reads)
2. [Step envelope](#2-step-envelope)
3. [Step types](#3-step-types)
4. [Fixtures and substitution](#4-fixtures-and-substitution)
5. [Execution](#5-execution)
6. [Result record and state machine](#6-result-record-and-state-machine)
7. [The controller's probe families as steps](#7-the-controllers-probe-families-as-steps)
8. [Conformance obligations](#8-conformance-obligations)

Schema: [`certification-steps/schema.json`](./certification-steps/schema.json)
(a step list); examples under
[`certification-steps/examples/`](./certification-steps/examples/),
validated by `make validate-examples`.

## 1. Who authors, who runs, who reads

| Party | Does | Never does |
|---|---|---|
| **Offer / template author** (operator, or the pool's curated catalog) | Writes `certification[]` on the offer. | — |
| **Runner** | Serves the requests. MAY propose steps via `x-certification-suggested` in its attach document. | Author, edit, skip, or self-report a step. A suggestion is shown to the author and is inert until copied into the offer. |
| **Broker** | Runs steps in order, records evidence, moves the runner × offer state. A first pass freezes an unfrozen offer. | Interpret a step beyond its type's definition; store request or response bodies; charge or settle. |
| **Pool controller** | Reads results; runs the probation → active → share-cap ladder on top. | Run steps. |
| **Coordinator console** | Shows results; triggers `run`. | — |

Certification traffic is **not paid work**: no payment envelope, no
settlement record, no `Livepeer-Work-Units` claim, no receipt. It counts
for nothing in any ledger. It reaches the runner over the same attach
connection, with `Livepeer-Runner-Local-Id`, so the agent routes it like
any request; an agent MUST NOT treat it specially.

## 2. Step envelope

```yaml
certification:
  - name: ready            # unique within the offer, [A-Za-z0-9._-]{1,64}
    type: readiness        # readiness | request | usage | latency
    required: true         # default true; a non-required failure is recorded, not blocking
    timeout_ms: 10000      # per step; defaults per type (§3)
    config: { … }          # type-specific (§3)
```

Rules:

- `certification[]` MAY be empty. An offer with no steps certifies every
  matched runner immediately — **and therefore freezes on the first
  match**. Authors SHOULD give every offer at least a `readiness` and a
  `request` step; a pool template MUST.
- Steps run in list order (§5). A `usage` or `latency` step that reuses
  "the previous request" refers to the nearest preceding `request` step.
- Unknown `type` or unknown `config` keys for a known type are a
  configuration error: the offer is rejected at load / push
  (`offer_invalid`, `field` = the step), never at run time.
- Size: ≤ 32 steps per offer; `config` ≤ 64 KiB including inline fixtures.

## 3. Step types

### 3.1 `readiness`

Runs the runner-declared readiness probe (`runner-attach.md` §3.2
`readiness{}`) — the type, path, and config the runner gave, not anything
the author writes. The author controls only how much readiness is enough.

| `config` key | Default | Meaning |
|---|---|---|
| `attempts` | 3 | Probe invocations before giving up. |
| `interval_ms` | 2000 | Between attempts. |
| `consecutive` | 1 | Consecutive `ready` results required to pass. |

Default `timeout_ms`: 5000 per attempt. Passes when `consecutive` ready
results are seen within `attempts`. Evidence:
`{ "attempts": n, "ready_at_attempt": k, "probe_type": "…" }`.

### 3.2 `request`

One exchange against the runner, judged by status and assertions. The
same step serves `paid-job` and `paid-session` capabilities; which is
meant follows from the offer's protocol.

**`paid-job` form**

| `config` key | Default | Meaning |
|---|---|---|
| `transport` | first of the runner's declared `transports` | `unary` \| `stream` \| `multipart`. MUST be one the runner declared; otherwise the step is `error` (`transport_not_declared`). |
| `path` | the runner's `paths.invoke` | Override only for runners that expose a dedicated probe path. Relative; same rules as attach paths. |
| `method` | `POST` | HTTP method. |
| `headers` | `{}` | Extra request headers. `Livepeer-*` keys are forbidden (`offer_invalid`). |
| `body` | — | JSON value sent as `application/json`; substitution applies (§4). `unary`/`stream` only. |
| `parts[]` | — | `multipart` only: `{ name, value? \| fixture?, filename?, content_type? }`. Exactly one of `value` (string, substituted) or `fixture` (§4). |
| `expect_status` | `200` | Integer or list. |
| `expect_content_type` | — | Prefix match on the response `Content-Type`. |
| `assert[]` | `[]` | JSONPath assertions on a JSON response body: a bare string means "path exists and is non-null"; an object `{ path, equals? \| matches? \| min? }` compares. For `stream`, the body is the concatenation of SSE `data:` payloads parsed as a JSON array, or the raw body when not SSE. |
| `max_response_bytes` | 1 MiB | Read bound; a larger response fails the step (`response_too_large`). |

Default `timeout_ms`: 30000.

**`paid-session` form** — opens a session via the runner's `paths.create`,
checks the descriptor, and terminates.

| `config` key | Default | Meaning |
|---|---|---|
| `session_params` | `{}` | Passed verbatim as the create body's `session_params`; substitution applies. |
| `expect_descriptor_schema` | any of the runner's `descriptor_schemas` | Tag the create response's `runtime.schema` MUST equal. |
| `assert[]` | `[]` | JSONPath assertions over `runtime.public`. |
| `hold_ms` | 0 | Keep the session open this long before terminate (lets a `usage` step observe events). |
| `expect_status_after_terminate` | `terminated` | What `paths.status` must report after terminate, within `timeout_ms`. |

Default `timeout_ms`: 30000. Passes when create succeeds, the descriptor
envelope validates per `runtime-descriptor.md` §2–§3, assertions hold,
terminate succeeds, and status agrees.

Evidence (job): `{ "transport", "status", "content_type", "bytes",
"duration_ms", "asserted": [paths that passed], "failed_assert": {…}? }`.
Evidence (session): `{ "descriptor_schema", "duration_ms", "asserted",
"terminated": true }`. Bodies are never stored.

### 3.3 `usage`

Proves the frozen (or to-be-frozen) metering path yields a positive
count. Different mechanics per protocol, one semantic: **would this
runner's work be billable?**

| `config` key | Default | Meaning |
|---|---|---|
| `source` | `previous_request` | `previous_request` reuses the response of the nearest preceding `request` step (job) or the session it opened (session); or an inline `request` object with the §3.2 keys to make a dedicated exchange. |
| `min_units` | 1 | Units the extractor / usage claim must reach. |
| `window_ms` | 10000 | Session only: how long to wait for a usage event after open. Requires the source session to still be open (`hold_ms` on the request step, or an inline request). |

- **Job:** the broker runs the capability's declared
  `work_unit.extractor` over the source exchange and passes iff
  `units ≥ min_units`. Evidence: `{ "extractor": "openai-usage", "units": 17 }`.
- **Session:** the broker waits for the first runner usage event
  (`paid-session` §7.2) on the source session and passes iff its cumulative
  claim `≥ min_units` and its `work_unit` equals the declared one.
  Evidence: `{ "work_unit": "participant_seconds", "units": 4, "event_at": "…" }`.

Default `timeout_ms`: 15000. A `usage` step with no preceding `request`
and no inline `request` is `offer_invalid`.

### 3.4 `latency`

Repeats an exchange and bounds a percentile.

| `config` key | Default | Meaning |
|---|---|---|
| `request` | `previous_request` | As `usage.source`: reuse the nearest preceding request step's config, or inline. |
| `samples` | 3 | Measured exchanges (1–20). |
| `warmup` | 0 | Unmeasured exchanges first. |
| `concurrency` | 1 | Exchanges in flight at once (1–4). |
| `p50_max_ms` | — | At least one of `p50_max_ms` / `p95_max_ms` is required. |
| `p95_max_ms` | — | |
| `measure` | `total` | `total` (request start → body complete) or `first_byte` (start → first response byte; the streaming user's number). |

Default `timeout_ms`: `samples × 30000`. Every sample MUST also satisfy
the request step's `expect_status`; a failed sample fails the step.
Evidence: `{ "samples", "p50_ms", "p95_ms", "min_ms", "max_ms",
"bound": { "p50_max_ms": … } }`.

## 4. Fixtures and substitution

**Fixtures** are the binary bodies multipart steps need. A `fixture`
reference is one of:

- `{ "ref": "audio/wav-16k-mono-3s" }` — a **built-in** the broker ships.
  The built-in set is this module's
  [`extractors/fixtures/`](../extractors/fixtures/) tree, addressed by
  `<dir>/<file-without-extension>`, plus `video/mp4-2s-720p` and
  `image/png-64` defined by the broker; the broker lists what it has on
  `GET /admin/v1/runtime` under `certification_fixtures[]`. An unknown ref
  is `offer_invalid` at load, not a run-time error.
- `{ "inline_base64": "…", "content_type": "audio/wav" }` — ≤ 256 KiB,
  for the odd runner that needs a specific file. Counts against the step
  `config` size bound.

**Substitution** lets one template serve many identities. In any string
inside `body`, `parts[].value`, `session_params`, or `headers`, the
broker replaces:

| Token | Value |
|---|---|
| `{{identity.<key>}}` | The runner's declared `identity` value for the dotted key (`{{identity.openai.model}}`). |
| `{{offer.offering_id}}`, `{{offer.capability_id}}` | The offer's ids. |
| `{{offer.extra.<key>}}` | An operator `extra` value (dotted path). |
| `{{run.id}}` | The run id, for runners that log it. |
| `{{fixture_url.<ref>}}` | A URL, scoped to this run, from which the runner can **fetch** the built-in fixture `<ref>` (same refs as `fixture.ref`). For runners that take a source URL rather than a body. Unknown ref: `substitution_missing` at substitution time, so the recipe fails here rather than the runner reporting a 404 as its own fault. |
| `{{sink_url}}` | A URL, scoped to this run, the runner can **PUT** or **POST** its output to. The broker accepts and discards, counting bytes. For runners that take a destination URL. |

**Run-scoped URLs.** Some runners do not take a body at all: a transcode
runner takes a *source* URL and a *destination* URL, and the media never
travels in the request. A multipart fixture cannot certify such a runner,
and any URL a recipe author writes into a JSON body is one the pool
invented that no runner can fetch. So the broker mints two URLs per
certification run — a fixture source and an output sink — exactly as it
mints the session usage callback (§3.3): opened when the run starts,
unguessable, closed when the run ends, swept if abandoned. They exist for
the length of one run and for one runner, which is what keeps a fixture
source from being a public file server and a sink from being an open
write target. Both require the broker's `external_base_url`, since a
runner reaches them over ordinary HTTP; without it the substitution is
`substitution_missing` and names the missing config.

A JSON transcode smoke step therefore reads:

```yaml
- name: smoke
  type: request
  required: true
  config:
    transport: stream
    body:
      source_url: "{{fixture_url.video/mp4-2s-720p}}"
      output_url: "{{sink_url}}"
      preset: "720p"
```

A token whose value is absent makes the step `error`
(`substitution_missing`) — it is a template bug, not a runner failure.
Substitution is textual, JSON-escaped; no expressions, no defaults.

## 5. Execution

1. **Trigger** (§6.2). One run per (runner capability, offer) at a time; a
   new trigger aborts the running one (`aborted`).
2. **Preconditions:** the runner is `connected`, the capability is
   `matched` to the offer. Otherwise the run is recorded `error`
   (`runner_not_matched`) and nothing is sent.
3. **Steps in order.** Each step gets its own `timeout_ms`; the run gets
   `sum(timeout_ms) + 10 s`. A **required** step's failure or error skips
   every later step (`skipped`) and fails the run. A non-required failure
   is recorded; the run continues.
4. **Transport:** every request goes over the runner's attach connection to
   the capability's paths with `Livepeer-Runner-Local-Id`. No payment
   headers. The broker's own capacity accounting MUST NOT count these
   against `max_in_flight`, but the broker SHOULD hold at most one
   certification exchange per host at a time so a run never starves paid
   work.
5. **Evidence bound:** ≤ 8 KiB per step after truncation; bodies are never
   stored; a failed assertion records the JSONPath and the first 256 bytes
   of the value it found.
6. **Outcome:** `passed` iff every required step passed. On `passed`:
   the runner × offer moves to `certified`, then to `eligible` or
   `ineligible` by the frozen-shape rule; an unfrozen offer freezes with
   this runner's projection (`runner-attach.md` §5). On `failed`/`error`:
   the pair stays `matched` and is retried per §6.2.

Reference pseudocode:

```
run(runner, offer):
  if !connected(runner) || state(runner, offer) != matched: return error(runner_not_matched)
  ctx = { identity: runner.identity, offer, run.id }
  last_request = nil
  for step in offer.certification:
    if skipping: record(step, skipped); continue
    res = execute(step.type, substitute(step.config, ctx), last_request)   # bounded by step.timeout_ms
    if step.type == request && res.ok: last_request = res
    record(step, res)
    if !res.ok && step.required: skipping = true
  outcome = all(required steps passed) ? passed : failed
  if outcome == passed: certify(runner, offer)        # may freeze
  else: schedule_retry(runner, offer)
```

## 6. Result record and state machine

### 6.1 Record

One per run, as `broker-admin.md` §6.1 returns it:

```json
{ "host_id": "…", "local_id": "…", "offering_id": "…", "run_id": "run_01jx…",
  "trigger": "match", "state": "passed", "started_at": "…", "finished_at": "…",
  "shape_hash": "sha256:…",
  "steps": [ { "name", "type", "required", "status": "passed|failed|skipped|error",
               "duration_ms", "evidence": { }, "message"? } ] }
```

`shape_hash` is the runner's projection at run time; a later shape
change invalidates the run (§6.2). Retention: the latest run per pair
forever (while the pair exists); prior runs for `certification_retention`
(default 30 days), bounded at 50 per pair.

### 6.2 Triggers and retries

| Trigger | When | Notes |
|---|---|---|
| `match` | A capability enters `matched` (first attach, re-match after an offer push). | Automatic. |
| `runner_change` | A re-attach changes a **non-frozen** field of the capability (`paths`, `readiness`, `devices`, `requirements`) or the runner reconnects after `recertify_after_disconnect` (default 1 h). | Automatic. A change to a *frozen* field drops the pair to `attached` instead and re-matches. |
| `offer_change` | The offer's `certification[]` changed (file reload or push). | Automatic, every matched runner of that offer. Runners already `eligible` stay eligible **until** the new run finishes; a failure then demotes. |
| `recertify` | Periodic `recertify_every` on the offer (`session_policy` sibling; default off), or a pool-controller policy request. | Automatic. Same grace as `offer_change`. |
| `operator` | `POST /admin/v1/certification/{host}/{offer}/run`. | Explicit. |

Retries after a failed automatic run: exponential from 30 s, capped at
`recertify_backoff_max` (default 30 min), reset on any trigger. A runner
that keeps failing stays `matched` — visible with its last result, never
served.

### 6.3 What freezes and what does not

- The **first `passed` run** on an unfrozen offer freezes it. That run's
  `run_id` is recorded as `frozen_by.run_id`.
- A `passed` run on a frozen offer never changes the freeze; it only
  certifies the runner, whose eligibility then follows the shape rule.
- An `accept-shape` (`broker-admin.md` §4.3) does not re-run anything:
  candidates are already certified runners by definition.

## 7. The controller's probe families as steps

The `pool-controller` families, rewritten as template data. Each is a
complete `certification[]` an offer or template can carry verbatim.

**Chat completions** (`openai:chat-completions`, was `probes.go` case 1)

```yaml
certification:
  - { name: ready, type: readiness }
  - name: smoke
    type: request
    config:
      transport: unary
      body: { model: "{{identity.openai.model}}", messages: [ { role: user, content: ping } ], max_tokens: 8 }
      assert: [ "$.choices[0].message.content", { path: "$.usage.total_tokens", min: 1 } ]
  - { name: usage, type: usage, config: { min_units: 1 } }
  - name: stream
    type: request
    required: false
    config:
      transport: stream
      body: { model: "{{identity.openai.model}}", messages: [ { role: user, content: ping } ], stream: true, max_tokens: 8 }
      assert: [ "$[0].choices[0].delta" ]
  - { name: latency, type: latency, required: false, config: { samples: 3, p50_max_ms: 4000, measure: first_byte } }
```

**Embeddings** (`openai:embeddings`, case 2)

```yaml
certification:
  - { name: ready, type: readiness }
  - name: smoke
    type: request
    config:
      body: { model: "{{identity.openai.model}}", input: "ping" }
      assert: [ { path: "$.data[0].embedding", min: 1 }, "$.usage.total_tokens" ]
  - { name: usage, type: usage }
  - { name: latency, type: latency, required: false, config: { samples: 5, p95_max_ms: 1500 } }
```

**Audio transcription / translation** (`openai:audio-*`, case 3 — a multipart upload)

```yaml
certification:
  - { name: ready, type: readiness }
  - name: smoke
    type: request
    config:
      transport: multipart
      parts:
        - { name: model, value: "{{identity.openai.model}}" }
        - { name: file, fixture: { ref: multipart-audio-duration-v1/wav-16k-mono-3s }, filename: probe.wav, content_type: audio/wav }
      assert: [ "$.text" ]
  - { name: usage, type: usage, config: { min_units: 1 } }      # multipart-audio-duration → 3 s
  - { name: latency, type: latency, required: false, config: { samples: 3, p50_max_ms: 6000 } }
```

**Speech** (`openai:audio-speech`, case 4 — JSON in, binary out)

```yaml
certification:
  - { name: ready, type: readiness }
  - name: smoke
    type: request
    config:
      body: { model: "{{identity.openai.model}}", input: "ping", voice: "{{offer.extra.probe_voice}}" }
      expect_content_type: "audio/"
      max_response_bytes: 4194304
  - { name: usage, type: usage }                                  # request-formula over input length, or bytes-counted
```

**Transcode** (`video:transcode.abr`, case 5)

```yaml
certification:
  - { name: ready, type: readiness }
  - name: smoke
    type: request
    config:
      transport: multipart
      timeout_ms: 60000
      parts:
        - { name: profiles, value: '[{"name":"720p30","width":1280,"height":720,"fps":30}]' }
        - { name: source, fixture: { ref: video/mp4-2s-720p }, filename: probe.mp4, content_type: video/mp4 }
      expect_content_type: "video/"
  - { name: usage, type: usage, config: { min_units: 1 } }        # ffmpeg-progress out_time ≥ 1 s
  - { name: latency, type: latency, required: false, config: { samples: 2, p50_max_ms: 20000 } }
```

What used to select the family — `strings.Contains(capability_id,
"transcription")` — is gone; the template that declares the offer declares
its steps.

## 8. Conformance obligations

| Fixture | Asserts |
|---|---|
| `cert-empty-steps-freezes-on-match` | Offer with `certification: []`, matched runner → `passed` run with zero steps, offer `frozen`. |
| `cert-readiness-uses-runner-probe` | Runner declares `http-status /ready`; a `readiness` step hits `/ready`, never a path the author wrote. |
| `cert-request-substitutes-identity` | `{{identity.openai.model}}` in `body` → the runner receives the declared model string. |
| `cert-request-missing-substitution-is-error` | `{{offer.extra.nope}}` → step `error` `substitution_missing`, run `failed`, no request sent. |
| `cert-request-transport-not-declared` | `transport: multipart` on a `unary`-only runner → step `error` `transport_not_declared`. |
| `cert-request-multipart-fixture` | Built-in audio fixture reaches the runner as a file part with the given filename and content type. |
| `cert-request-session-opens-and-terminates` | Session form: create → descriptor schema checked → terminate → status `terminated`; evidence carries `descriptor_schema`. |
| `cert-usage-runs-declared-extractor` | Job `usage` after a `request` step → `evidence.extractor` equals the runner's declared type, `units ≥ min_units`. |
| `cert-usage-session-waits-for-event` | Session `usage` with `hold_ms` → passes on first usage event, fails on `work_unit` mismatch. |
| `cert-required-failure-skips-rest` | Required step 2 fails → steps 3..n `skipped`, run `failed`, pair stays `matched`. |
| `cert-nonrequired-failure-passes-run` | Only a `required: false` latency step fails → run `passed`, step recorded `failed`. |
| `cert-latency-bounds-percentile` | 3 samples, `p50_max_ms` below observed → `failed` with `p50_ms` and `bound` in evidence. |
| `cert-no-payment-no-settlement` | A full run produces no settlement record, no receipt, no `Livepeer-Work-Units`, no capacity slot use. |
| `cert-first-pass-freezes-second-does-not` | Two runners pass in sequence → `frozen_by.run_id` is the first's; second only certifies. |
| `cert-offer-change-recertifies` | Editing `certification[]` triggers `offer_change` runs; an eligible runner stays eligible until its new run fails. |
| `cert-unknown-config-key-rejected-at-load` | `config.expect_stauts` → `offer_invalid` naming the step; no run ever starts. |

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.1.0-draft | 2026-09-01 | §4: `{{fixture_url.<ref>}}` and `{{sink_url}}`, run-scoped URLs the broker mints so a runner that takes source and destination URLs can be certified against a real file. Mirrors the §3.3 usage callback. Additive (plan 0045 §7). |
| 1.0.0-draft | 2026-08-26 | Initial spec (plan 0043 §3.5, item 3). Step envelope; `readiness` (runner-declared probe, author sets sufficiency), `request` (job: transport/body/parts/status/JSONPath asserts; session: create/descriptor/terminate), `usage` (declared extractor or runner usage event ≥ `min_units`), `latency` (p50/p95 over N samples, total or first-byte); built-in and inline fixtures; `{{identity.*}}`/`{{offer.*}}` substitution; execution order and required/skip rule; result record, triggers, retry backoff; first-pass freeze rule; the five controller probe families as YAML; 16 conformance fixtures. |

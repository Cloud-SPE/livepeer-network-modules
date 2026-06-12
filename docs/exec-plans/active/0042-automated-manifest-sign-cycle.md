---
plan: 0042
title: Automated manifest sign cycle (secure-orch agent)
status: active
phase: planning
opened: 2026-06-10
owner: harness
related:
  - "completed plan 0018 — orch-coordinator design"
  - "completed plan 0019 — secure-orch trust spine design"
  - "docs/design-docs/trust-model.md"
  - "secure-orch-console/docs/operator-runbook.md"
audience: secure-orch operators, orch-coordinator maintainers, trust-model reviewers
---

# Plan 0042 — automated manifest sign cycle (secure-orch agent)

**Status:** active (planning). Automates the manifest sign cycle between the
firewalled secure-orch host and the public orch-coordinator, replacing
the hand-carry loop (download candidate → SSH tunnel → upload → diff →
sign → download → re-upload) with an outbound-only agent on the secure
host. Picks up the "automated transport" item that
`docs/design-docs/trust-model.md` § *What's deferred* explicitly parked,
and **deliberately amends sign-cycle invariant #4** ("there is no
automated sign path") — see §3.

## 1. Hard constraints

These are non-negotiable and shape everything below:

1. **secure-orch never accepts inbound connections.** All transport is
   initiated from the secure host: it *pulls* candidates from the
   coordinator and *pushes* signed manifests back. No listener, no
   webhook receiver, no inbound tunnel.
2. **The cold manifest key never leaves the secure host.** The agent
   uses the existing `Signer` abstraction
   (`secure-orch-console/internal/signing/signer.go`); nothing about
   key handling changes.
3. **All trust expansion is config, not code.** The shipped binary
   contains the full policy engine; what it is *allowed* to auto-sign
   is decided by a policy file on the secure host with conservative
   defaults (renewals only).
4. **Every decision is audited.** The existing append-only JSONL audit
   log gains agent event kinds (§9). No silent signs, no silent skips.

## 2. Scope

In scope:

1. ✅ `ETag`/`If-None-Match` conditional-fetch semantics on the
   coordinator's existing candidate routes (§5.1; shipped).
2. ✅ Bearer-token auth admitting the agent on candidate download +
   signed-manifest receive, with mTLS as documented hardening (§5.2;
   shipped — TLS pinning/mTLS remain runbook hardening for the
   agent-side work).
3. ✅ A renewal-window rule in the coordinator's candidate builder so
   expiring manifests produce a fresh-window candidate (§5.3; shipped).
4. An **agent daemon mode in `secure-orch-console`** (not a new
   component): pull loop, stability debounce, change classifier,
   policy engine, auto-sign, held-for-operator queue, push,
   publish confirmation, kill switch (§6–§8).
5. New audit event kinds and outbound webhook alerts (§9).
6. Trust-model doc amendment in the same PR series (§3) — per the
   repo's doc-gardening rule, the invariant changes in the same PR
   that changes the behavior.

Out of scope:

- Hardware-backed signers (YubiHSM 2, Ledger, PKCS#11) — unchanged
  from secure-orch-console plan 0001; the `Signer` interface is the
  seam.
- Changes to the manifest schema, canonicalization (JCS / RFC 8785),
  signature scheme (EIP-191 secp256k1), or the coordinator's
  five-step verify pipeline
  (`orch-coordinator/internal/service/receive/receive.go`). The wire
  format is untouched; this plan is transport + policy only.
- Multi-coordinator fan-out (one agent, one coordinator for v1).
- Resolver-side behavior — resolvers already re-verify every fetch;
  nothing downstream changes.

## 3. Trust-model amendment

Current invariant #4 (`docs/design-docs/trust-model.md` § *Sign-cycle
invariants*):

> **There is no automated sign path.** Every manifest publication is a
> discrete operator action.

Replacement wording (to land in the same PR series):

> **There is no unbounded automated sign path.** The cold key signs
> without an operator only inside a policy envelope the operator
> authored and the audit log records: content-identical renewals
> always; benign content changes only within explicit bounds (price
> delta, domain allowlist, rate limit); everything else is held for a
> discrete operator action. Identity (`eth_address`) and
> `spec_version` changes are never auto-signed.

What the amendment costs, stated honestly: a compromised
orch-coordinator (or a broker feeding it) can now cause signatures to
happen *within the policy envelope* without a human seeing the diff
first. The blast radius is bounded by construction:

| Auto-signed class | Worst case if coordinator is hostile |
|---|---|
| Renewal (content-identical) | Attacker keeps the operator's own already-approved content alive. Zero new capability, price, or URL enters the manifest. |
| Benign within bounds (phase 2) | Price drifts within the configured % bound; capabilities *disappear* (removal is benign — it reduces exposure); no new `worker_url` domain outside the allowlist. Rate limiter caps supersession frequency. |
| Critical / forbidden | Never auto-signed. Held + alerted. Unchanged from today. |

Invariant #2 ("the operator saw the diff") becomes class-scoped: it
holds for every critical change; for auto-signed classes it is replaced
by "the operator saw and authored the policy, and sees the audit
trail." Invariant #3 ("the coordinator did not author the content")
weakens only to the bounded extent in the table above.

## 4. Architecture at a glance

```mermaid
sequenceDiagram
    autonumber
    participant OC as orch-coordinator (hot zone)
    participant AG as secure-orch-console --agent (cold zone)
    participant Cold as cold key
    participant OP as operator (console UI)

    loop poll (If-None-Match: <etag>)
        AG->>OC: GET /admin/candidate/latest
        OC-->>AG: 304 (unchanged) — ~zero bytes
    end
    OC-->>AG: 200 + candidate tarball + ETag
    AG->>AG: debounce: wait until ETag stable N min
    AG->>AG: re-diff vs last-signed.json (local, trusted)
    AG->>AG: classify: renewal | benign | critical | forbidden
    alt policy says auto-sign
        AG->>Cold: SignCanonical(canonical bytes)
        AG->>OC: POST /admin/signed-manifest (bearer)
        AG->>OC: GET /.well-known/livepeer-registry.json
        AG->>AG: confirm published seq/hash; audit
    else held
        AG->>OP: held queue in console UI + webhook alert
        OP->>AG: review diff, confirm gesture, sign (existing flow)
        AG->>OC: POST /admin/signed-manifest (agent pushes)
    end
```

The agent runs *inside* `secure-orch-console` as a daemon mode
(`--agent` flag) because the console already owns every primitive the
loop needs: the `Signer`, the JCS canonicalizer
(`internal/canonical/`), the structural differ, `last-signed.json`
atomic state, the audit log, and the web UI where the held queue must
surface. A separate component would duplicate all of it and split the
audit trail.

## 5. orch-coordinator changes

### 5.1 Conditional fetch on the candidate routes — ✅ shipped

**Decided (closes former Q5):** no new `/admin/candidate/latest`
endpoint. The ETag/`If-None-Match` semantics landed on the *existing*
`GET /candidate.json` and `GET /candidate.tar.gz` routes — one
endpoint surface, so the human download path and the agent path
cannot drift apart. The agent polls `/candidate.tar.gz` (the same
payload the hand-carry download produces: `manifest.json` +
`metadata.json`). 304 polls are not audited; only full tarball
downloads append the existing download audit event.

- **ETag = SHA-256 of the candidate's canonical `manifest.json`
  bytes** (the full payload, including `issued_at`/`expires_at`/
  `publication_seq` — *not* the builder's content-only
  `lastContentHash`). Rationale: the builder already debounces
  `issued_at` when content is unchanged, so the full-bytes hash is
  stable across no-op rebuilds; and it *does* move when a renewal
  window or seq advance produces a candidate the agent must see.
  Using `lastContentHash` would 304 forever and starve renewals.
- `If-None-Match` match → `304 Not Modified`, empty body. A 30–60 s
  poll cadence costs ~zero. Add jitter (±10 %) to the agent's timer.
- `503` with `Retry-After` when no candidate has been built yet.
- Optional follow-up (not v1): long-poll variant
  (`?wait=55s` holds the request until the ETag changes or the
  timeout lapses). Plain conditional GET is cheap enough to ship
  first; measure before adding held connections.

### 5.2 Auth on the receive path — ✅ shipped (coordinator side)

`POST /admin/signed-manifest` (and the web-form twin) currently rides
the optional operator admin token. Add a dedicated **agent bearer
token** (separate credential from the human operator token, so it can
be rotated independently and identified in audit):

- Coordinator flag/env: `--agent-token-file` (file, not CLI literal).
  Implementation note: the bearer admits the agent on the two
  candidate GET routes *and* the signed-manifest POST — the operator
  login is a single-session cookie flow, so a cookie-holding agent
  would lock the human out. Bearer-admitted requests audit as actor
  `agent`; a presented-but-wrong bearer is a hard 401, never a
  fall-through to the login flow.
- Agent flag: `--coordinator-token-file`.
- The manifest signature remains the real content authentication —
  the five-step verify pipeline is unchanged. The bearer only keeps
  anonymous garbage and DoS off the endpoint.
- Documented hardening step (operator runbook): mTLS between agent
  and coordinator with a pinned coordinator certificate
  (`--coordinator-ca-file`), plus an egress firewall on secure-orch
  allowlisting only the coordinator host:port. TLS pinning ships in
  v1 (it is a client-side check, cheap); full mTLS is config the
  runbook documents.

### 5.3 Renewal-window rule in the builder — ✅ shipped

The builder's `issued_at` debounce keeps candidate bytes identical
while content is unchanged — correct for idempotence, wrong for
renewals: a manifest must be re-signed before `expires_at` even when
nothing changed. One rule, implemented in
`orch-coordinator/internal/service/candidate/build.go`
(`--renewal-threshold` flag, default 1/3 of `--manifest-ttl`):

> If the **debounced candidate's** remaining validity
> (`PrevIssuedAt + ManifestTTL − scrape-window end`) is below
> `renewal_threshold`, the next rebuild refreshes
> `issued_at`/`expires_at` to the current scrape window even when the
> content hash is unchanged.

**Refinement over the draft:** the draft keyed the rule on the
*published* manifest's remaining validity. That would churn the
candidate's bytes (and ETag) on every scrape from the moment the
window opened until the sign cycle completed — defeating the agent's
stability debounce exactly when it matters. Keying on the debounced
candidate's own expiry makes the refresh one-shot: the renewed
`issued_at` immediately debounces again for a full
`TTL − threshold`, so the candidate is stable while the agent
debounces, signs, and pushes. The two clocks track each other because
the candidate's debounced `issued_at` is the published `issued_at`
after every successful publish.

That moves the candidate's full-bytes ETag → the agent pulls →
classifies as *renewal* → auto-signs. No new endpoint, no agent-side
clock coupling to the coordinator.

## 6. Agent: pull loop, debounce, and the no-op rule

State machine per pull cycle:

1. **Poll** with `If-None-Match`. On 304: sleep (with jitter), loop.
2. **On 200:** validate the tarball strictly (existing schema +
   strict-JSON checks; every pulled byte is untrusted input). Record
   the new ETag and a `candidate_pulled` audit event.
3. **Debounce:** do nothing until the ETag has been stable for
   `stability_window` (default 5 min). Broker flap then coalesces
   into one sign instead of a thrash of supersessions. A newer ETag
   during the window resets it.
4. **Classify** (§7) by re-diffing locally against
   `last-signed.json` — never trust the coordinator's diff.
5. **The no-op rule (prevents sign loops):** if content-bearing
   fields are identical to last-signed *and* last-signed
   `expires_at − now > renewal_threshold`, record `no_op` and skip.
   This matters because every successful publish advances the
   coordinator's seq, which changes the next candidate's bytes and
   ETag — without this rule the agent would re-sign forever.
   Renewal auto-sign fires only when content is identical **and**
   remaining validity is below the threshold.
6. **Sequence discipline:** the cold side owns the canonical seq
   (unchanged from plan 0019). The agent signs with
   `max(candidate.publication_seq, last_signed.publication_seq + 1)`
   and never reuses or decreases a seq.

## 7. Change classifier and policy file

The classifier diffs candidate vs `last-signed.json` over
content-bearing fields (existing differ: header + per-tuple keyed on
`(capability_id, offering_id)`), and assigns the *highest* class any
single change hits — one critical change holds the whole candidate.

| Class | Triggers | Phase-1 disposition | Phase-2 disposition |
|---|---|---|---|
| `renewal` | content identical; window/seq advance; remaining validity < threshold | **auto-sign** | auto-sign |
| `benign` | tuple removed; `price_per_unit_wei` decrease; price change within ±`price_delta_max_pct`; `worker_url` change within `worker_url_domain_allowlist` | hold **+ `would_auto_sign` shadow audit** | **auto-sign** |
| `critical` | new `(capability_id, offering_id)` tuple; price increase beyond bound; `worker_url` outside allowlist; any `extra`/`constraints` change; `interaction_mode` or `work_unit` change | hold + alert | hold + alert |
| `forbidden` | `orch.eth_address` change; `spec_version` change | refuse + alert (never held as signable; the candidate is rejected outright) | same |

Policy file (`/etc/secure-orch/sign-policy.json`, strict JSON, schema
shipped in-repo, hash recorded in audit on every load):

```json
{
  "policy_version": 1,
  "auto_sign": {
    "renewal": true,
    "benign": false
  },
  "benign_bounds": {
    "price_delta_max_pct": 10,
    "allow_tuple_removal": true,
    "worker_url_domain_allowlist": ["workers.example-orch.net"]
  },
  "rate_limit": {
    "max_auto_signs_per_hour": 4,
    "on_breach": "pause"
  },
  "stability_window_seconds": 300,
  "renewal_threshold_fraction": 0.3333
}
```

Rules:

- The policy file is reloaded on SIGHUP and at each cycle's start;
  a parse/schema failure **pauses auto-signing** (fail closed) and
  alerts — it never falls back to a previous or default policy.
- `rate_limit.on_breach: "pause"` means breaching the hourly cap
  stops *all* auto-signing (including renewals) until the operator
  clears it. A burst of auto-signs is the loudest available signal
  that the coordinator side is misbehaving.
- The phase-2 flip is exactly one line: `"benign": true`. No deploy.

## 8. Push, publish confirmation, held queue, kill switch

**Push + confirm.** After signing, the agent POSTs the envelope to the
coordinator, then confirms by fetching the public
`GET /.well-known/livepeer-registry.json` and checking the published
`publication_seq` and canonical-bytes SHA-256 match what it pushed.
Retry with exponential backoff + jitter (cap ~10 min); after
`push_max_attempts` (default 6) record `publish_failed` and alert.
The signed envelope is also written to `last-signed.json` atomically
*before* the first push attempt (existing `writeLastSignedAtomic`), so
a crash mid-push never loses a signature — on restart the agent
detects last-signed > published and resumes pushing.

**Held queue.** Held candidates persist under
`/var/lib/secure-orch/held/` (candidate bytes + classification
report). The console UI gains a "Pending changes" view: the existing
diff renderer + the existing confirm-gesture sign flow
(`web/handlers.go` `handleSign`), with one change — after the operator
signs, the **agent** pushes (no manual download/re-upload). A newer
candidate superseding a held one replaces it (audit: `held_superseded`);
the operator always reviews the latest, never a stale diff.

**Kill switch.** Three independent stops, any one suffices:

1. `touch /var/lib/secure-orch/agent.pause` — checked every cycle;
   presence pauses pull *and* sign (file-based so it works over a
   plain SSH session with the console down).
2. Policy `auto_sign.{renewal,benign}: false` — stops signing,
   keeps pulling/classifying/alerting (useful for shadow mode).
3. Rate-limit breach auto-pause (§7).

**Alerts.** Outbound-only webhook (`--alert-webhook-url`, optional)
fired on: held item created, forbidden-class candidate, publish
failure, policy load failure, rate-limit pause, agent start/stop.
Webhook delivery is best-effort; the audit log is the system of
record.

## 9. Audit events

New kinds in the existing JSONL log (each carries ETag, canonical
SHA-256, seq, classification, and policy hash where applicable):

`agent_start`, `agent_stop`, `candidate_pulled`, `no_op`,
`classified`, `auto_sign`, `would_auto_sign` (shadow), `held`,
`held_superseded`, `operator_approve`, `push_attempt`,
`publish_confirmed`, `publish_failed`, `policy_loaded`,
`policy_invalid`, `rate_limit_pause`, `agent_paused`, `agent_resumed`.

304 polls are **not** audited (noise); the agent exports Prometheus
counters for poll/304/200 instead, alongside gauges for held-queue
depth, time-since-last-publish-confirm, and seconds-to-expiry of the
published manifest (the page-the-operator metric if the loop wedges).

**Wiring an operator alert on seconds-to-expiry is a phase-1
requirement, not optional.** A rate-limit breach pauses *all*
auto-signing including renewals (§7), and with the default 24 h
`--manifest-ttl` an unnoticed pause can let the published manifest
expire. The gauge alone is not the mitigation; the alert on it is.

## 10. Phasing

**Phase 1 — one build, conservative dial.** Everything in §5–§9
ships. Default policy: `renewal: true, benign: false`. Outcome: zero
hand-carry, automatic renewals, every content change held for a
one-click in-console approval, benign changes logged as
`would_auto_sign`.

**Phase 1 burn-in — shadow mode (≥2 weeks).** Operator reviews the
audit log: every manual approval that the policy *would* have signed
is calibration evidence; any `would_auto_sign` on a change the
operator hesitated over means the bounds tighten before the flip.

**Phase 2 — config flip, no deploy.** `auto_sign.benign: true` with
burn-in-validated bounds. Rate limiter and kill switches are already
live and exercised.

**Explicitly never:** auto-signing `critical` or `forbidden` classes.
That dial does not exist in the policy schema — adding it would be a
new trust-model amendment, not a config change.

## 11. Work breakdown

1. ✅ **Coordinator: candidate routes + ETag** (§5.1) — conditional
   GET on the existing routes, full-bytes hash, tests for 304/200/503
   (+ Retry-After) semantics, 304-not-audited.
2. ✅ **Coordinator: agent bearer + renewal-window rule** (§5.2,
   §5.3) — `--agent-token-file` plumbing, `requireAuthOrAgent` on the
   three agent routes, `--renewal-threshold` builder rule + tests
   proving bytes refresh inside the renewal window, debounce again
   after the refresh (one-shot), and stay idempotent outside it.
3. ✅ **Console: classifier + policy engine** (§7) —
   `internal/policy`: strict fail-closed policy file
   (schema at `docs/sign-policy.schema.json`, example at
   `examples/sign-policy.json`, file-hash for audit), classifier over
   the differ output (differ gained spec_version stability),
   `Decide` mapping classes through the dials (with
   `would_auto_sign` shadow flag), latching rate limiter.
   Table-driven tests per class including the no-op rule and
   highest-class-wins. Q1/Q2 proposals implemented as proposed:
   price decreases bounded by the same pct; allowlist is an explicit
   suffix list with dot-boundary matching.
4. ✅ **Console: agent loop** (§6, §8) — `internal/agent`:
   pull/debounce/sign/push/confirm state machine behind a `--agent`
   daemon mode (`--coordinator-public-url`,
   `--coordinator-token-file`, `--agent-policy`, `--agent-held-dir`,
   `--agent-pause-file`, `--agent-poll-interval`). Push reconcile is
   the crash-recovery rule: last-signed (written atomically before
   first push) ahead of published → resume push; the post-sign push
   rides the same rule. Tested against a fake coordinator: renewal
   auto-sign with seq discipline, no-op, hold + supersede, shadow
   would_auto_sign + phase-2 flip, forbidden refusal, stability-
   window reset, crash recovery, push-failure audit+alert, pause
   file, fail-closed policy, rate-limit latch (degrades to hold).
   Implementation notes: the renewal clock derives TTL from the
   last-signed manifest's own issued_at→expires_at span (no agent
   TTL flag); a published seq *ahead* of last-signed is logged loudly
   and never pushed over; the rate-limit latch clears on process
   restart (operator Clear gesture lands with the item-5 UI).
5. ✅ **Console: held queue UI + agent push-after-approve** (§8) —
   manifests page gains a "Pending changes held by the agent" card
   (class, findings, shadow would_auto_sign note) with a
   load-for-review action that stashes the held candidate into the
   *existing* diff + confirm-gesture flow, applying seq discipline
   at load time so the operator signs exactly what the agent would
   have. Signing a held candidate clears the slot, audits
   operator_approve, and redirects (no download) — the agent's
   reconcile rule pushes the new last-signed on its next cycle, so
   push-after-approve needs no extra transport code.
6. ✅ **Audit + metrics + webhook** (§9) — audit kinds landed with
   item 4. Prometheus exposition (hand-rolled text format — the cold
   host takes no client-library dependency) served at `GET /metrics`
   on the console's existing loopback listener (constraint #1 rules
   out a separate metrics listener): poll outcomes, decision
   counters, held-queue depth, last-publish-confirm timestamp, and
   seconds-to-expiry of the published manifest.
   `--alert-webhook-url` wires the best-effort outbound webhook
   (generic JSON with a Slack-compatible `text` field, Q4 proposal)
   for held / forbidden / publish_failed / policy_invalid /
   rate_limit_pause / agent start/stop. The mandatory expiry alert is
   self-contained: when the published manifest burns through half
   the renewal buffer without a re-publish, the agent fires
   `manifest_expiry_warning` once per crossing — operators without a
   scrape stack still get paged; the gauge backs Prometheus alerting
   on top.
7. **Docs in the same PRs:** trust-model invariant amendment (§3),
   operator-runbook rewrite (agent mode, policy file, burn-in
   procedure, kill switches), tech-debt entry for the long-poll
   follow-up if measurement warrants it.
8. **End-to-end test:** fake coordinator + real console agent in a
   harness: candidate change → debounce → hold → approve → push →
   publish-confirm; renewal path; forbidden-class refusal; rate-limit
   pause.

## 12. Open questions (defaults proposed, decide before phase 2)

1. `price_delta_max_pct` default — proposed 10 %. Is price *decrease*
   unconditionally benign, or bounded too? (Proposed: bounded by the
   same %, so a fat-finger 99 % price cut holds for review.)
2. `worker_url_domain_allowlist` — exact-host vs suffix matching
   (proposed: explicit suffix list, no wildcards).
3. Renewal threshold — 1/3 TTL proposed; with the current
   `--manifest-ttl` this should leave at least several hours of retry
   headroom before expiry even with the coordinator briefly down.
   Validate against the production TTL value.
4. Webhook target — operator's existing alerting stack? Determines
   payload format (proposed: generic JSON, Slack-compatible).
5. ~~Endpoint shape~~ — **decided + shipped**: ETag/`If-None-Match`
   semantics landed on the existing `GET /candidate.json` and
   `GET /candidate.tar.gz` routes; no new endpoint (§5.1).

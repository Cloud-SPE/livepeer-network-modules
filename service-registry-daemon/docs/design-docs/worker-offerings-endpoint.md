---
title: Worker `/registry/offerings` endpoint
status: verified
last-reviewed: 2026-05-19
---

# Worker `/registry/offerings` endpoint

A uniform HTTP endpoint every worker in the suite exposes for
**orch-coordinator scrape + operator confirmation**. Lets workers
self-describe their offerings in one canonical shape.

This is a **convention** — not a wire protocol enforced by this daemon. Workers
that implement it become operator-friendly out of the box; workers that don't
fall back to fully-manual roster entry in the orch-coordinator's SPA. Either
way, this daemon does not call the endpoint; it consumes the
`raw-registry-manifest.json` proposal the orch-coordinator produces from
operator-confirmed roster entries.

## Why this exists

Two earlier designs were considered and rejected:

1. **Pure operator-curated roster.** Operator types every capability + offering
   into a dashboard. Friction-heavy, drift-prone (operator types `gpt-oss-20b`
   but the worker actually serves `gemma4:26b`).
2. **Coordinator scrapes each worker's old `/capabilities`.** Rejected.
   That forced every worker repo into a uniform `/capabilities` shape —
   workload-hostile (vtuber session knobs, transcode preset codes don't
   fit the openai chat-completions shape).
3. **(picked) One suite-wide endpoint per worker.**
   `/registry/offerings` is the uniform worker-advertisement endpoint
   across all workloads and carries the modules-canonical capability
   fragment that the orch-coordinator scrapes. Legacy
   `GET /capabilities` is deleted in v3.0.1.

Cost: each worker repo gets one additional HTTP route + JSON marshaller (~30
LOC). Benefit: operator drift collapses while operator control over what
publishes stays intact (the SPA shows the scraped result as a *draft*; nothing
saves until operator confirms).

## Endpoint contract

```
GET <worker-base-url>/registry/offerings
Authorization: Bearer <token>           # OPTIONAL — see Auth below

200 OK
Content-Type: application/json

{
  "orch_eth_address": "0x1234...abcd",
  "capabilities": [
    {
      "capability_id": "openai:chat-completions",
      "offering_id": "gpt-oss-20b",
      "protocol": "paid-job/v1",              // paid-job/v1 | paid-session/v1
      "job": { "transports": ["unary"] },     // paid-job/* only
      "work_unit": { "name": "token" },
      "price_per_unit_wei": "1250000",
      "per_units": 1,
      "extra": { /* opaque, optional, workload-specific */ },
      "constraints": { /* always present, may be {} */ }
    },
    {
      "capability_id": "livepeer:vtuber-session",
      "offering_id": "vtuber-1080p30",
      "protocol": "paid-session/v1",
      "session": {                            // paid-session/* only
        "descriptor_schema": "vtuber-session/v1",   // <name>/v<major>
        "attachment": "external",               // external | inband-ws
        "metering": "runner-reported",          // runner-reported | broker-observed
        "refill": "extensible",                 // extensible | bounded
        "heartbeat": { "interval_seconds": 10, "missed_threshold": 3 },
        "lease": { "policy": "funding-tracking" }  // funding-tracking | fixed
      },
      "work_unit": { "name": "second" },
      "price_per_unit_wei": "500",
      "per_units": 1,
      "constraints": {}
    }
  ]
}
```

The body is a **flat list of capability tuples**, one per
`(capability_id, offering_id)` pair — not the node-oriented
`capabilities[].offerings[]` nesting of the resolver's projected view.
It matches the manifest payload defined by
`livepeer-network-protocol/manifest/schema.json`.

Each tuple declares exactly one `protocol` (`paid-job/v1` or
`paid-session/v1`) plus the matching axes object: `job` for `paid-job/*`,
`session` for `paid-session/*`. A `paid-*` tuple missing its axes object
produces a manifest that fails schema validation downstream. The field
formerly named `interaction_mode` no longer exists.

This daemon treats `protocol`, `job`, and `session` as pure pass-through —
it gates on none of them (see `internal/types/coordinator_envelope.go`).
Gateways and the broker are the consumers that actually read the axes.

A worker (or the `capability-broker` fronting it) contributes one such
payload. `orch_eth_address` is the orchestrator identity the broker is
configured with; `worker_url` is *not* in the body — the orch-coordinator
fills it in from whichever broker it scraped. The coordinator merges N
payloads into the final signed manifest's `capabilities[]`.

## What the worker omits

- **Node identity** — `id`, `url`, and any operator-owned node metadata are
  chosen outside the worker fragment. The worker doesn't know them. Operator
  types them into the orch-coordinator's roster row alongside the worker URL.
- **Internal routing details** — for example, openai-worker-node's
  `backend_url` (the inference backend the worker dispatches to internally)
  is deliberately omitted from `/registry/offerings`.
- **Workload-native operational fields** — modes, supported codecs,
  per-session capacity limits, etc. Those may appear under `extra` on
  `/registry/offerings` when they're useful for operator review or
  routing-side filtering, but only the operator decides what gets
  published.

## Auth

Optional, off by default. Workers run on the public internet; the orch-
coordinator likewise. The data isn't secret — every offering eventually
publishes in the signed manifest at
`<serviceURI>/.well-known/livepeer-registry.json` — so default-no-auth is
operationally safe.

When the worker wants an additional barrier:

- **Worker side:** optional top-level `auth_token` field in shared
  `worker.yaml`. If set, the endpoint requires
  `Authorization: Bearer <that-token>`; otherwise plain HTTP. 401 on
  mismatch.
- **Orch-coordinator side:** per-worker `offerings_auth_token` field on the
  `fleet_workers` row (operator-typed in the SPA next to the worker URL).
  Sent as a bearer if present, omitted otherwise.

## Operator confirmation flow

Implemented in `livepeer-orch-coordinator` per its plan 0002 §step 4c:

1. Operator adds a worker: types URL, name, optional Prom URL,
   optional offerings auth token.
2. Coordinator hits `<workerUrl>/registry/offerings`.
3. Coordinator validates the body against the modules-canonical Zod schema
   (same schema the manifest uses).
4. Coordinator renders the parsed `capabilities[]` as a pre-filled, editable
   form (the "Offerings draft").
5. Operator reviews/edits/confirms — drops offerings they don't want public,
   tweaks prices, adds `extra` blobs, etc.
6. On confirm, the entry saves to `fleet_workers.capabilities` (a structured
   JSON column, not a flat string list).
7. A "Refresh offerings" button on the worker drilldown re-scrapes and shows
   a diff against the saved row, letting the operator opt into changes.

The coordinator's `composeProposal` reads `fleet_workers.capabilities` verbatim
and emits the modules-canonical `nodes[].capabilities[]` for the
`raw-registry-manifest.json` proposal. No synthesis on the coordinator side; no enforcement on the daemon
side.

## What this daemon does NOT do

- **Does not** dial worker `/registry/offerings` itself. The orch-coordinator
  is the only legitimate caller in the architecture.
- **Does not** enforce the body shape on workers. Workers that implement a
  different shape (or no endpoint at all) just don't get the convenience of
  coordinator-side draft pre-fill — operators type into the dashboard
  manually instead.

## See also

- `livepeer-network-protocol/manifest/schema.json` — the authoritative
  schema for the capability tuples in this body and in the signed manifest.
- [../livepeer-network-protocol/manifest/schema.json](../livepeer-network-protocol/manifest/schema.json) — this daemon's own v3
  node-oriented manifest shape (a *different*, node-grouped projection;
  do not conflate the two).
- [adding-a-new-workload.md](adding-a-new-workload.md) — onramp recipe for
  new workload authors; implementing this endpoint is step 5 of the recipe.
- `livepeer-orch-coordinator` plan 0002 §step 4c — coordinator-side scraper
  + draft + confirm SPA flow.
- `livepeer-network-suite` plan 0003 §Decision 5 — the architectural
  resolution that put this endpoint in place.

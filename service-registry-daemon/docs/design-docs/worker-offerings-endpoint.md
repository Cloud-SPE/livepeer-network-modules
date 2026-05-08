---
title: Worker `/registry/offerings` endpoint
status: verified
last-reviewed: 2026-04-29
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
  "worker_eth_address": "0x1234...abcd",   // OPTIONAL — orch-internal only
  "capabilities": [
    {
      "name": "openai:/v1/chat/completions",
      "work_unit": "token",
      "offerings": [
        {
          "id": "gpt-oss-20b",
          "price_per_work_unit_wei": "1250000"
        }
      ],
      "extra": { /* opaque, optional, workload-specific */ }
    }
  ]
}
```

The body shape is exactly the modules-canonical capability fragment defined in
[manifest-schema.md](manifest-schema.md): `capabilities[i] = {name, work_unit?, offerings?: [...], extra?}`.
A worker contributes one such fragment; the optional top-level
`worker_eth_address` is orch-internal metadata only. The
orch-coordinator may store/display it, but it is excluded from the raw
proposal and signed manifest. The orch-coordinator merges N
workers' fragments (along with operator-supplied node identity:
`id`/`url` and optional node-level `extra`) into the final `nodes[]` of the manifest.

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

- [manifest-schema.md](manifest-schema.md) — what the body fragment slots
  into (the manifest's `nodes[i].capabilities[j]` shape).
- [adding-a-new-workload.md](adding-a-new-workload.md) — onramp recipe for
  new workload authors; implementing this endpoint is step 5 of the recipe.
- `livepeer-orch-coordinator` plan 0002 §step 4c — coordinator-side scraper
  + draft + confirm SPA flow.
- `livepeer-network-suite` plan 0003 §Decision 5 — the architectural
  resolution that put this endpoint in place.

---
title: Migrating to connected runners (plan 0043)
status: active
last-reviewed: 2026-08-27
audience: orchestrator operators, pool operators
---

# Migrating to connected runners

Plan 0043 changed who declares what. **There is no backward
compatibility** (core belief #14): the old grammars, endpoints, and
config knobs are deleted, not deprecated. This note is what an existing
deployment has to do, and what it can throw away.

The one-line summary:

> The operator authors offers — what is sold, at what price, with what
> capacity, where. Runners attach outbound and declare everything else.

## What changed, by component

| Component | Gone | Replaced by |
|---|---|---|
| `capability-broker` | `capabilities[].backend/job/session/work_unit/health`, `runner.describe_path` and its quarantine, per-workload metadata polling, `--metadata-refresh-interval` | `offers[]`; runners attach and declare themselves |
| `pool-member-agent` | controller `/hardware` report, `POST_*` backend-id registration | one attach document per connection; adapter profiles |
| `pool-controller` | `brokerrender`, `runtimeservice`, `/admin/v1/broker-runtime/*`, `broker_apply_command`, `cmd/broker-apply` | `PUT /admin/v1/offers` and `PUT /admin/v1/credentials` |
| `orch-coordinator` | hardcoded `spec_version`, dead `worker_url` override | imported `VERSION`; per-broker `admin_token_ref`; four hot-zone console pages |
| `secure-orch-console` | `renewal_threshold_fraction` | the threshold published in each candidate's `metadata.json` |
| `service-registry-daemon` | v3.0.1 manifest schema and decoder, `Publisher.{BuildManifest,SignManifest,BuildAndSign,ProbeWorker}`, `livepeer-registry-refresh` | the protocol envelope is the only manifest |

## Standalone orchestrator

1. **Rewrite `host-config.yaml` as offers.** Each `capabilities[]` entry
   becomes an `offers[]` entry keeping only what you actually chose:
   `offering_id`, `capability`, `protocol`, `price`, `capacity`, `extra`,
   and a `match` selector over the runner identity you expect. Delete
   `backend`, `job`, `session`, `work_unit` and `health` — the runner
   declares those. `offering_id` is now unique across the whole file, so
   two old entries that shared `(id, offering_id)` collapse into one offer
   that many runners serve. See
   [`capability-broker/examples/host-config.example.yaml`](../../capability-broker/examples/host-config.example.yaml)
   for the annotated reference, or
   [`host-config.offers.example.yaml`](../../capability-broker/examples/host-config.offers.example.yaml)
   for the shortest thing that runs.

2. **Add a credential store and an offers state path.**
   `credential_store.{path, sealing_key_file}` and `offers_state_path`, both
   on a persistent volume. Back them up with the session store: losing the
   credential store means every host re-enrolls, and losing the offers state
   means each offer re-freezes from whichever runner certifies first — a
   silent manifest change.

3. **Enrol each host.** `POST /admin/v1/enroll`, or the coordinator
   console's *Enroll host* page. The credential is shown **once**.

4. **Point the agent at the broker.** The bundle variables are
   `LIVEPEER_BROKER_URL` (or `LIVEPEER_BROKER_QUIC_ADDR`),
   `LIVEPEER_ATTACH_CREDENTIAL_FILE`, `LIVEPEER_HOST_ID` and
   `LIVEPEER_RUNNERS_FILE`. The same variables serve a pool member and
   an orchestrator's own hardware — only who minted the credential
   differs.

5. **Describe the runners.** A runner declaration says where a container
   is, which profile it is, and what it loaded; the profile supplies the
   endpoint, transports, work unit, extractor and readiness recipe. See
   [`pool-member-agent/README.md`](../../pool-member-agent/README.md).

6. **Let it certify.** The first runner that passes an offer's
   certification steps freezes that offer's shape and produces a manifest
   candidate. Sign it as usual. **Until then the offer is not
   advertised** — that is deliberate: an offer nobody can serve is never
   published.

## Pool operator

Steps 2–6 above apply to the broker; additionally:

1. **Set `bootstrap.broker_admin_url` and its auth.** The controller
   pushes offers and credentials there. There is no rendered file and no
   reload, so `broker_apply_command` and any volume that staged the
   broker's config can go.

2. **Expect the push, not the render.** The recorded runtime revision
   now carries `push_error` when the broker refused, and
   `changed_offers` / `revoked_hosts` when it accepted. A failed push
   leaves the broker serving what it last accepted: paid traffic and the
   signed manifest are unaffected.

3. **Hardware arrives by relay.** Members no longer POST inventory. The
   controller reads it back from the broker's runner view, so what it
   records is what the broker matched offers against.

## Coordinator and signing

1. **Add `brokers[].admin_token_ref`** (`env://` or `file://`) for every
   broker you want to manage from the console. A broker without one is
   listed but not administrable, and the pages say so.

2. **Upgrade brokers and coordinator together.** The coordinator refuses
   to merge a broker whose `spec_version` **major** differs from its own,
   and refuses a broker that publishes no `spec_version` at all. The
   error names both versions.

3. **Delete `renewal_threshold_fraction` from `sign-policy.json`.** It is
   rejected as an unknown field now. The threshold comes from the
   candidate.

4. **Expect to type a version.** A `spec_version` change is held as
   `critical` and signing it requires typing the new version. It used to
   be refused outright, which meant a protocol upgrade could never be
   signed.

## What to verify after cutover

- `GET /registry/offerings` carries `spec_version` and only offers with
  a frozen shape.
- The coordinator's **Runners** page shows each host and, for any
  capability not serving, the disagreeing field with both sides.
- **Certification** shows a passing run per runner × offer.
- A second identical runner attaching produces **no config edit, no
  reload, and no new signature** — that is the property the whole change
  exists for.

---
title: Recover uncertain initial session admission and runner creation
status: completed
date: 2026-09-25
beads: lnm-7uh1, lnm-6026
---

# Recover uncertain initial session admission and runner creation

Persist a sealed initial-admission intent before calling the receiver. It binds
the exact signed authorization, request fingerprint, broker/gateway session IDs,
price/quote and runner identity. A lost response or failed progress write cannot
remove this intent. Cleanup uses the receiver's atomic admission fence: either
unused authority is durably canceled, or an existing admission is converted to a
normal winding-down session and settled using receiver-confirmed cumulative
usage. No recovery re-funds an account. Canceled opens retain a stable refusal
and permit scoped non-admission evidence; accepted opens retain terminal evidence.

Recover lost runner-create responses using an optional runner-declared
`paths.reconcile` endpoint. POST with the broker session ID returns the existing
runner session ID or a durable `fenced` outcome. A bare 404 is never proof. The
endpoint serializes with creation; an absent identity is fenced before replying,
so an old delayed create cannot subsequently start work. Existing creation is
idempotent for the exact request. Recovery locates and terminates existing work,
then releases compute and resolves payment independently.

The live transcode runner in the sibling livepeer-modules-transcode-runners repo
persists broker IDs and idempotent create responses. Its authenticated
reconciliation (runners-3xf) now includes durable integrity-protected fences
and tests across create races and runner restart. No source is copied between
repositories. Other runners opt in only after implementing these semantics;
unsupported or unreachable reconciliation leaves capacity held. Old records
missing the broker ID or exact authorization remain explicitly unresolved.

Tests cover admission accepted/refused/unknown, lost fence and settlement replies,
failed progress writes, encrypted intent persistence, scope matching, restart,
concurrent create/reconcile, delayed create after a fence, runner termination
failure and repeated evidence reads. Recovered financial records retain the signed
price even if the offer changes or is removed before cleanup. Request-ID lookup
requires signed terminal evidence before reporting SETTLED.

Validation passed in Docker: full broker tests/vet/build, real receiver race
regressions, full runner tests with race/vet/build, authenticated runner recovery
fixtures, protocol schema examples and all 59 conformance scenarios. The receiver
fixture now installs its signal handler before exposing its readiness socket,
removing a startup/shutdown race exposed by fast recovery tests. Deployment is
separate; upgrade broker attach support before runner advertisement.

---
title: Host-shared resource admission
status: completed
date: 2026-09-13
beads: lnm-8bs
---

# 0052 — Host-shared resource admission

## Purpose

Runner-local concurrency cannot coordinate independent containers placed on
one physical device. The first observed failure is a member running live, VOD,
and ABR transcode containers on one GPU: each runner can remain within its own
limit while their combined, incompatible work oversubscribes the card.

This plan supplies a generic host-local admission domain without teaching the
pool or broker what a workload or cohort means.

## Decisions

1. Templates opt into `flock-files/v1` through `runner_compose.host_admission`.
   They declare a runner environment hook and opaque file suffixes; the
   controller has no capability or cohort table.
2. One domain is derived from enrollment and `HardwareUnit.GPUUUID`. Raw GPU
   UUIDs never become paths. Same unit means shared inodes; different units
   remain isolated.
3. The member agent owns initialization and inode protection. Any unsafe,
   missing, or unmountable namespace fails the affected service closed.
4. Compatible concurrency remains runner-local. The active cohort owns the
   domain until drain; FIFO fairness is not promised in v1. Broker capacity
   backoff and failover bound the operational effect of starvation.
5. Runner `429 capacity_reached` is private vocabulary. The broker exposes
   public `503 capacity_exhausted` with `Livepeer-Backoff`.
6. Refusal before authorization admission produces signed non-admission.
   Refusal after admission produces a signed zero-use settlement and releases
   the reservation. The two are mutually exclusive and restart durable.
7. Callers need no new product-specific behavior. Failover uses a newly bound
   authorization after terminal evidence from the first route.

The binding design is
[`host-shared-resource-admission.md`](../../design-docs/host-shared-resource-admission.md).

## Delivery

| Bead | Work |
|---|---|
| `lnm-8bs.1` | Cross-component design and protocol contract |
| `lnm-8bs.2` | Template schema and controller desired-state rendering |
| `lnm-8bs.3` | Member-agent initialization, mounts, and restart lifecycle |
| `lnm-8bs.4` | Broker capacity normalization and zero-use accounting |
| `lnm-8bs.5` | Certification and caller conformance |
| `lnm-8bs.6` | Golden Compose, multi-GPU, restart, and eu-central evidence |

## Completion

The plan completes when every child bead is closed, the Docker-first component
checks and protocol conformance pass, and the transcode handoff has an immutable
Modules revision plus the requested generated Compose and eu-central evidence.

Completed on 2026-09-13. The immutable implementation revision is
`8c36dc26e44ab9b3b55e0703e9a647e8ef42f645`; the reproducible run and its
consumer-revision boundary are recorded in
[`2026-09-13-host-shared-resource-admission-evidence.md`](../../references/2026-09-13-host-shared-resource-admission-evidence.md).

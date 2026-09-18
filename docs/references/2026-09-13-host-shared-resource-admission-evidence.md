---
title: Host-shared resource admission — eu-central verification and transcode handoff
date: 2026-09-13
status: verified
beads: [lnm-8bs, lnm-8bs.6]
modules_implementation_revision: 8c36dc26e44ab9b3b55e0703e9a647e8ef42f645
---

# Host-shared resource admission evidence

## Scope and revision boundary

This is the Modules-side completion record for epic `lnm-8bs`. The immutable
implementation revision is:

```text
8c36dc26e44ab9b3b55e0703e9a647e8ef42f645
```

That revision contains the cross-component contract, controller rendering,
member-agent inode lifecycle, broker normalization and zero-use accounting,
certification behavior, and caller conformance scenarios. The reproducible
evidence runner is
[`infra/scenarios/shared-resource-admission/`](../../infra/scenarios/shared-resource-admission/).

The run was performed on `dev-rig` in eu-central at
`2026-09-13T13:54:45Z`. Host GPU inventory was:

```text
GPU-d8b9bd88-6416-0f7b-d950-fb7c4d528228, NVIDIA GeForce GTX 1650, 580.178.04
```

The scenario does not change or claim changes in LOC, any retail gateway, or
the transcode repository. It uses the transcode implementation only as an
initial consumer of a generic Modules contract.

## Generated placement evidence

The golden generated fixture is
[`pool-controller/testdata/shared-gpu-compose.yaml`](../../pool-controller/testdata/shared-gpu-compose.yaml).
Its SHA-256 is:

```text
656b11060ece9e32b05d764a0a160f31c56d23ca5dafd7a1d45292fc52e86c5d
```

Docker Compose accepted the fixture. Its live, VOD, and ABR services all
receive this same base path:

```text
/var/lib/livepeer-resource-admission/v1/c0a03daa992a6554643bd62c/lock
```

Each service bind-mounts the same `.mutex`, `.live`, and `.batch` files as
writable individual inodes while the parent directory is read-only. The raw
GPU UUID is absent from the path. Controller tests also generated a different
domain for a second GPU UUID.

## Commands and results

All Modules commands ran in `golang:1.25.7-bookworm` containers with the
source mounted read-only. The complete protocol conformance run passed 58 of
58 scenarios.

```text
go test ./internal/desiredstate \
  -run 'TestBuildSharesAdmissionDomainByPhysicalGPUAndIsolatesDifferentGPUs|TestTranscodeSharedGPUComposeGolden' \
  -count=1 -v
PASS

go test ./internal/desiredstate \
  -run 'TestPrepareHostAdmissions|TestApplyFailsClosedBeforeComposeWhenAdmissionPreparationFails' \
  -count=1 -v
PASS

go test ./internal/server ./internal/sessionengine ./internal/certification \
  -run 'TestRunnerCapacity|TestJobCapacityRefusal|TestSessionOpenNormalizesCapacity|TestOpenCapacityRefusal|TestCapacityRefusal|TestSessionCertificationCapacity' \
  -count=1 -v
PASS

go run ./cmd/livepeer-conformance
58 passed, 0 failed, 0 skipped (58 total)
```

The member-agent suite proved stable inode numbers across repeated prepare,
same-domain inode identity, different-domain isolation, symlink/unsafe-root
rejection, and fail-closed behavior before Compose side effects.

The broker log evidence for both paid protocols was:

```text
INFO paid request ... protocol=paid-job/v1 status=503
  livepeer_error=capacity_exhausted work_units=0 outcome=capacity_exhausted
INFO paid request ... protocol=paid-session/v1 status=503
  livepeer_error=capacity_exhausted work_units=0 outcome=capacity_exhausted
```

The certification tests observed `inconclusive`, honored a bounded retry
delay, then passed once capacity was available. The session recovery test
injected a transient payment-close failure and proved that the durable
zero-use winddown was retried successfully.

The broker request metric uses the same `capacity_exhausted` outcome label as
the structured log, and the work-unit observation is zero. Consumer-side
corroboration exposed admission-refusal counters of one while active workload
counts remained zero for VOD and ABR rejection.

## Transcode consumer corroboration

The following read-only tests were run from the local transcode checkout:

```text
base HEAD: afa12cbe0dd7527ed78e72163d794c2d21d6bdd6
working tree: dirty

transcode-core: GPUAdmission                               PASS
transcode-runner: SharedGPUAdmission                       PASS
abr-runner: SharedGPUAdmission                             PASS
live-runner: GPUAdmission                                  PASS
```

These tests covered:

- two batch and two live leases being admitted within their own cohort, so
  runner-local concurrency remains the limiting policy;
- live-then-batch and batch-then-live refusal;
- a 100-iteration opposing-cohort race with exactly one winner;
- different GPU lock bases operating independently;
- VOD and ABR refusing before their active execution count increments;
- live returning canonical runner capacity and recovering after batch release;
- multiple same-cohort live sessions; and
- process exit and orderly shutdown releasing kernel locks.

Because that consumer checkout was dirty, this record deliberately does not
call `afa12cbe...` an immutable transcode implementation revision. The
transcode team must commit and publish runner images containing those changes
before managed placement can enforce the policy in deployment. The runner
image references in the golden fixture remain catalog inputs; their presence
in this fixture is not evidence that an older published image implements the
consumer contract.

## Caller impact

There is no LOC- or retail-gateway-specific wire change. A caller receives the
existing canonical capacity response and terminal evidence. If it fails over,
it obtains a fresh authorization bound to the new payee/route; it never reuses
the first route's authorization. Backoff is optional caller policy.

## Acceptance conclusion

The Modules-owned mechanism and protocol behavior satisfy `lnm-8bs`. Actual
production rollout remains gated on a committed/published consumer image and
normal deployment verification; this evidence does not represent a production
mutation or a live FFmpeg workload run.

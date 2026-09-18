---
title: Host-shared resource admission
status: accepted
last-reviewed: 2026-09-13
beads: lnm-8bs
---

# Host-shared resource admission

## Decision

Managed runners that share one physical hardware unit may opt into a versioned
host-local admission mechanism. The pool supplies one stable inode namespace
per member and hardware unit; the runner owns the compatibility policy inside
that namespace. Placement, the member agent, and the broker never interpret
workload cohorts.

The first mechanism is `flock-files/v1`. It exists for any cooperating Linux
runner, not specifically transcoding or GPUs. The first consumer is a set of
transcode runners that interpret their declared files as live and batch
cohorts.

## Template contract

The opt-in lives under `runner_compose`, because it is a fact about how that
image must be hosted:

```yaml
runner_compose:
  host_admission:
    mechanism: flock-files/v1
    scope: hardware-unit
    env_var: GPU_ADMISSION_LOCK
    file_suffixes: [.mutex, .live, .batch]
```

| Field | Meaning |
|---|---|
| `mechanism` | Versioned enforcement contract. `flock-files/v1` is the only mechanism in v1. |
| `scope` | Resource identity used for sharing. `hardware-unit` is the only v1 scope. |
| `env_var` | Runner-defined environment variable that receives the clean absolute base path. |
| `file_suffixes` | Opaque, unique suffixes appended to the base path and pre-created by the member agent. |

The controller validates syntax but never assigns meaning to a suffix. In
particular, `.live` and `.batch` are not network capability classes.

An absent `host_admission` block preserves existing placement and compose
behavior. It does not activate a second payment or workload-authorization
path; it only means that image has not declared host-shared coordination.

## Domain identity and paths

The controller derives an admission-domain ID as the first 24 lowercase hex
characters of:

```text
SHA-256("livepeer-host-admission/v1\x00" + enrollment_id + "\x00" + gpu_uuid)
```

`HardwareUnit.GPUUUID` is the stable physical identity, including the
legacy-named stable ID for CPU units. Hashing it means duplicate controller
records for the same member and physical unit still converge on one domain,
while the raw vendor identifier never becomes a path. Enrollment identity
prevents accidental namespace reuse between members, and the controller
rejects an assignment without either identity.

The runner sees this base path:

```text
/var/lib/livepeer-resource-admission/v1/<domain-id>/lock
```

The agent owns the corresponding host state beneath its configured data root.
It creates each declared suffix as a regular file, rejects symlinks and other
file types, and preserves the inode across reconciliation. The directory and
files are mounted so a runner can open and lock the declared files but cannot
unlink, replace, or create lock-file aliases. Failure to initialize or mount
the complete file set fails that service closed.

All opted-in services assigned to the same member/hardware unit receive the
same domain and file inodes. Different domains do not share files. The domain
and declaration participate in the desired-state revision.

## Ownership and lifecycle

| Component | Responsibility |
|---|---|
| Template author | Opt in only images that implement the mechanism; name the environment hook and required suffixes. |
| Pool controller | Validate metadata, derive the stable domain, and render identical declarations for colocated services. |
| Pool member agent | Initialize and preserve the inode namespace, render mounts, and fail closed if protection is unavailable. |
| Runner | Interpret its files, acquire and retain leases for the real workload lifetime, and refuse conflicts before work begins. |
| Capability broker | Normalize a runner capacity refusal; never interpret files, locks, hardware IDs, or cohorts. |

Kernel process exit releases `flock` leases. Reconciliation never replaces a
live inode. Draining keeps the service and its mounts present until the broker
has stopped dispatching and in-flight work has ended.

## Fairness

`flock-files/v1` is a safety mechanism, not a scheduler. Admission is
serialized while a runner checks the opposing cohort, but compatible leases
may continue joining an active cohort. The active cohort therefore owns the
resource until its last lease drains. Linux does not guarantee FIFO handoff,
so a continuously busy cohort can starve an opposing cohort.

That starvation risk is accepted for v1. A refused request is surfaced as
temporary capacity, and routing backoff/failover moves work elsewhere. A later
fair mechanism would require an explicit host admission arbiter or a
runner-coordinated intent queue and therefore gets a new mechanism version;
the controller must not infer such policy from capability names.

## Capacity refusal and accounting

A cooperating runner refuses local contention before workload execution with
HTTP `429` and JSON `{"error":"capacity_reached"}`. It may include a bounded
`Retry-After`. The broker maps this private runner response to:

```text
HTTP 503
Livepeer-Error: capacity_exhausted
Livepeer-Backoff: <bounded seconds>
```

The public response never exposes a host path, domain, cohort, or GPU ID.

There are two mutually exclusive accounting outcomes:

1. If the broker refuses its own capacity before wholesale authorization
   admission, it records signed `NOT_ADMITTED` evidence. No reservation or
   settlement exists.
2. If the broker admitted the authorization before the runner refused local
   capacity, it durably settles the authorization at zero units, releases the
   reservation, and signs that terminal settlement.

The request-id state machine makes the choice durable across retries and
restart. A broker must never emit both outcomes for one request. A route-bound
authorization cannot be reused at another payee; failover requires a new
authorization after the first route has authoritative terminal evidence.

This is compatible with both in-path providers and delegated callers. They can
release retail holds from broker-authoritative evidence, and SDK behavior is
not required for wholesale correctness.

## Non-GPU workloads

The mechanism may protect any one `HardwareUnit`: a GPU, CPU socket,
accelerator, or future host resource represented by placement. Non-GPU
workloads need it only when independent runner processes can make incompatible
use of that same unit. Ordinary runner-local concurrency remains preferable
when one process already owns the resource; adding host locks there creates no
safety benefit.

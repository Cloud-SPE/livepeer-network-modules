# Pool overlay flows

The pool overlay leaves the base architecture
([`architecture-overview.md`](./architecture-overview.md)) untouched from the
outside: gateways still see one orch identity, one coordinator-published
manifest, and one broker endpoint. This doc collects the three pool-specific
flows that span more than one component — member signup, the payout cycle, and
work routing / worker selection.

For the static picture of which services run and how they wire together, see
[`infra/scenarios/pool-orchestrator/`](../../infra/scenarios/pool-orchestrator/).
For per-component internals, see each component's own `docs/`.

## 1. Pool member signup

Prospective member submits a join request → optional backend verification →
operator (or auto-policy) approves → pool-controller materializes member +
member backends → operator assigns offers to backends → broker config is
re-rendered and applied → orch-coordinator re-scrapes and the signed manifest
is republished. Only after the sign cycle is the new member backend reachable
through the orch's published serviceURI.

```mermaid
sequenceDiagram
    autonumber
    actor Op as Pool Operator
    actor Mem as Prospective Member
    participant PC as pool-controller
    participant BV as backendverify
    participant AR as admissionreview / autoapprove
    participant CB as capability-broker<br/>(pool edge)
    participant OC as orch-coordinator
    participant SOC as secure-orch-console<br/>(cold key)

    Note over Mem,PC: 1. Submit
    Mem->>PC: POST /admin/v1/join-requests<br/>{ member_eth_address, payout_mode,<br/>requested_backends[]:<br/>(transport, url, auth, health_probe,<br/>claimed_capabilities[]) }
    PC->>PC: validateJoinRequest()<br/>persist as Status=pending

    Note over PC,BV: 2. Verify each requested backend
    PC->>BV: verify(requestedBackend)
    BV->>Mem: probe URL with declared health probe
    Mem-->>BV: probe result
    BV-->>PC: VerificationStatus +<br/>last_verified_at + error?

    Note over PC,AR: 3. Review (manual or auto)
    alt auto-approve enabled & Approvable
        PC->>AR: autoapprove tick
        AR->>PC: mark approved
    else manual review
        Op->>PC: GET /admin/v1/join-requests<br/>(admin UI)
        Op->>PC: PATCH /admin/v1/join-requests/{id}<br/>{ status: approved | rejected, review_reason }
    end

    Note over PC: 4. Materialize<br/>(only on approved)
    PC->>PC: memberAndBackendsFromJoinRequest()<br/>create MemberRecord + MemberBackend[]<br/>persist SourceJoinRequestID

    Note over Op,PC: 5. Assign offers to backends
    Op->>PC: POST /admin/v1/offers<br/>{ capability_id, offering_id, price, ... }
    Op->>PC: POST /admin/v1/assignments<br/>{ offer_id, member_backend_id }
    PC->>PC: assignment Status=pending → active

    Note over PC,CB: 6. Render + apply broker config
    Op->>PC: POST /admin/v1/broker-runtime/apply
    PC->>PC: brokerrender.Render()<br/>capabilities[] with<br/>extra.pool.member_eth_address
    PC->>CB: write temp config +<br/>bootstrap.broker_apply_command
    CB->>CB: POST /admin/v1/runtime/reload
    PC->>CB: poll GET /admin/v1/runtime<br/>until loaded_revision matches

    Note over OC,SOC: 7. Re-scrape + re-sign manifest
    OC->>CB: GET /registry/offerings
    CB-->>OC: tuples (new member backend included)
    OC->>OC: build candidate manifest
    Op->>SOC: pull candidate → diff → cold-sign
    SOC->>OC: POST /admin/manifest (signed)
    OC->>OC: atomic-swap publish<br/>(member is now routable)
```

JoinRequest status set: `pending`, `approved`, `rejected`, `withdrawn`
(`pool-controller/internal/types/join_requests.go`).

Code anchors:
- Validation + materialization: `pool-controller/cmd/livepeer-pool-controller/main.go:1993` (`validateJoinRequest`) and `:2021` (`memberAndBackendsFromJoinRequest`)
- Auto-approve worker: `pool-controller/internal/service/autoapprove/`, wired at `cmd/.../main.go:1813`
- Admission review + backend verification: `pool-controller/internal/service/{admissionreview,backendverify}/`
- Broker render: `pool-controller/internal/service/brokerrender/render.go` (pool extras at `:96-110`)
- Broker apply protocol: `pool-controller/README.md:190-226`

## 2. Payout cycle

`protocol-daemon` round event → `pool-reconciler` assembles revenue (from
`payment-daemon`) and final receipts (from `pool-controller`) → submits
round-close → `pool-controller` derives per-member payout intents →
`pool-payout-executor` claims with a lease, submits native ETH on Arbitrum,
and writes back `submitted` / `paid` / `failed`.

```mermaid
sequenceDiagram
    autonumber
    participant PRD as protocol-daemon
    participant PRC as pool-reconciler
    participant PD as payment-daemon<br/>(receiver)
    participant PC as pool-controller
    participant PE as pool-payout-executor
    participant Chain as Arbitrum<br/>(native ETH)

    Note over PRD,PRC: 1. Round transition fires close
    PRD-->>PRC: StreamRoundEvents:<br/>RoundEnded(round_id)
    PRC->>PD: GetRoundRevenue(round_id) [gRPC unix sock]
    PD-->>PRC: ConfirmedRevenueWei,<br/>ConfirmedTicketCount
    PRC->>PC: GET /admin/v1/work-receipts<br/>?round_id&status=final
    PC-->>PRC: final receipts[]

    PRC->>PRC: pool_cut_wei =<br/>pool_revenue_wei * commission_bps / 10000
    PRC->>PC: POST /admin/v1/round-close<br/>{ round_id, pool_revenue_wei,<br/>pool_cut_wei, included_work_receipt_ids[] }
    PC->>PC: persist RoundReceipt

    Note over PC: 2. Derive per-member intents
    PC->>PC: POST /admin/v1/payout-intents/derive<br/>buildPayoutIntents()<br/>one PayoutIntent per member,<br/>Status="pending"

    Note over PE,PC: 3. Export (freeze the batch)
    PE->>PC: POST /admin/v1/payout-intents/export
    PC->>PC: pending → exported<br/>(ExportedAt set)

    Note over PE,PC: 4. Claim with lease
    PE->>PC: POST /admin/v1/payout-intents/claim<br/>{ executor_id, lease_ttl_seconds, filters }
    PC->>PC: exported → leased<br/>lease_id = {executor_id}-{nanos}<br/>lease_expires_at = now+TTL
    PC-->>PE: { lease_id, intents[] }

    loop while lease active
        PE->>PC: POST /admin/v1/payout-intents/renew<br/>{ lease_id }
        PC-->>PE: extended TTL
    end

    Note over PE,Chain: 5. Submit on chain
    PE->>Chain: send-native-batch:<br/>signed ETH transfers from hot keystore
    Chain-->>PE: tx_hash (broadcast)
    PE->>PC: POST /admin/v1/payout-intents/status<br/>{ status: submitted, lease_id, tx_hash }
    PC->>PC: leased → submitted<br/>(lease cleared, SubmittedAt set)

    Note over PE,Chain: 6. Confirm
    loop confirm-submitted
        PE->>Chain: tx receipt poll
        Chain-->>PE: receipt
        alt confirmed
            PE->>PC: status=paid + tx_hash
            PC->>PC: submitted → paid (PaidAt set)
        else reverted / dropped
            PE->>PC: status=failed + failure_reason
            PC->>PC: submitted → failed (FailedAt set)
        end
    end

    Note over PE,PC: 7. Recover failures (optional)
    alt manual or auto-requeue
        PE->>PC: POST /admin/v1/payout-intents/requeue
        PC->>PC: failed → exported<br/>RetryCount++, LastRequeuedAt=now
    end

    Note over PE,PC: lease expiry safety
    PE--xPC: (executor crashes)
    PC->>PC: releaseExpiredLease():<br/>leased → exported on lease_expires_at < now
```

PayoutIntent status set (`pool-controller/cmd/livepeer-pool-controller/main.go:3456` `applyPayoutIntentStatus`):

```mermaid
stateDiagram-v2
    [*] --> pending: derive
    pending --> exported: export
    exported --> leased: claim
    leased --> exported: release / lease expired
    leased --> submitted: status=submitted<br/>(matching lease_id)
    exported --> submitted: status=submitted<br/>(no lease)
    submitted --> paid: status=paid
    submitted --> failed: status=failed
    leased --> failed: status=failed<br/>(matching lease_id)
    exported --> failed: status=failed
    failed --> exported: requeue<br/>(RetryCount++)
    paid --> [*]
```

Code anchors:
- Round event stream: `pool-reconciler/internal/protocoldaemon/client.go:79-107`
- Revenue fetch: `pool-reconciler/internal/paymentdaemon/client.go:44-63`
- Payout intent endpoints: `pool-controller/cmd/livepeer-pool-controller/main.go:1292-1627`
- Status transitions: `:3456-3540` (`applyPayoutIntentStatus`, `requeuePayoutIntent`, `releaseExpiredLease`)
- Executor commands: `pool-payout-executor/` (`list-intents`, `send-native-batch`, `confirm-submitted`, `reconcile-loop`)

> Pool v1 ships native ETH on Arbitrum only; multi-asset payout is future
> work. See [`pool-node-production-readiness.md`](./pool-node-production-readiness.md).

## 3. Work routing & worker selection

Single inbound request from gateway → broker picks one pool member backend
using local probe health blended with a polled pool snapshot → records a stub
receipt → forwards via the interaction-mode adapter → records the final
receipt and a backend-outcome data point for future scoring.

```mermaid
sequenceDiagram
    autonumber
    participant GW as gateway adapter
    participant CB as capability-broker
    participant Cfg as host-config<br/>(rendered by pool-controller)
    participant HM as healthMgr<br/>(local probes)
    participant PS as poolsnapshot cache<br/>(polled from controller)
    participant Sel as selection.DecisionFor
    participant PD as payment-daemon<br/>(receiver)
    participant PC as pool-controller
    participant BE as Member Backend
    participant PRC as pool-reconciler

    Note over PC,PS: 0. Background — pool snapshot kept fresh
    loop every pool.snapshot_poll_interval_ms (~30s)
        CB->>PC: GET /admin/v1/backend-selection-snapshot
        PC-->>CB: per-(backend, cap, offering):<br/>state, exclusion_reason,<br/>effective_selection_score,<br/>warmup_modifier, max_share_cap
        CB->>PS: cache
    end

    Note over GW,CB: 1. Inbound paid request
    GW->>CB: POST /v1/cap<br/>Livepeer-Capability, Livepeer-Offering,<br/>Livepeer-Payment, Authorization
    CB->>Cfg: lookup capabilityGroup<br/>(cap_id, offering_id)
    Cfg-->>CB: group.Backends[] (candidates)

    Note over CB,Sel: 2. Selection — selectBackend()
    loop each candidate
        CB->>HM: SnapshotsFor(cap_id, offering_id)
        HM-->>CB: probe Snapshot{Status, consecutive_*}
        CB->>PS: StatusFor(backend_id, cap, off)
        PS-->>CB: poolsnapshot.Status
        CB->>Sel: DecisionFor(snap, poolStatus)
        Sel-->>CB: { Eligible, Weight,<br/>MaxShareCap, Reason }
        alt eligible & weight>0
            CB->>CB: append to candidates
        else
            CB->>CB: deniedReasons[reason]++
            note right of CB: quarantined / excluded /<br/>in-cooldown / draining /<br/>unhealthy / stale → skipped
        end
    end

    alt no eligible candidates
        CB-->>GW: 503 + Backoff: 30
    end

    CB->>CB: applyMaxShareCaps(candidates)<br/>pick = randIntn(Σ weight)<br/>weighted random selection
    Note right of CB: candidate.cap has<br/>extra.pool.member_eth_address

    Note over CB,PD: 3. Pay before backend call
    CB->>PD: ProcessPayment(payment_bytes, work_id)
    PD-->>CB: ok (sender, credited_ev)

    Note over CB,PC: 4. Stub receipt (commitment)
    CB->>PC: POST /admin/v1/work-receipts<br/>{ id, request_id, cap, off,<br/>member_eth_address, backend_id,<br/>status: "stub" }

    Note over CB,BE: 5. Forward via interaction-mode adapter
    CB->>BE: req via transport (http / ws / rtmp / session)
    BE-->>CB: response payload + usage signal

    CB->>CB: extractor → actualUnits

    Note over CB,PD: 6. True-up payment
    CB->>PD: ReportUsage(work_id, actualUnits)
    PD-->>CB: ok (final price)

    Note over CB,PC: 7. Final receipt + outcome
    CB->>PC: POST /admin/v1/work-receipts<br/>{ id (same), status: "final",<br/>actual_units, gateway_revenue_wei }
    CB->>PC: POST /admin/v1/backend-outcomes<br/>{ backend_id, outcome:<br/>success|backend_failure|caller_failure,<br/>latency_metric_ms, occurred_at }
    CB-->>GW: response payload

    Note over PC,PRC: 8. Async — these receipts feed round close (Flow 2)
    PC-->>PRC: GET /admin/v1/work-receipts?status=final<br/>(at round end)
```

Selection decision shape (`capability-broker/internal/selection/decision.go`):

```mermaid
flowchart TD
    Start([snap, poolStatus])
    PoolConfigured{pool entry<br/>configured?}
    PoolState{pool state}
    Health{health.Status}
    Weight[base weight by status:<br/>ready=100, degraded=25,<br/>else 0]
    PoolScore[blend with pool<br/>real_success_score +<br/>real_latency_score<br/>* warmup_modifier]
    Cap[apply max_share_cap]
    OK([Eligible=true, Weight, Cap])
    Deny([Eligible=false,<br/>Reason=quarantined /<br/>excluded / cooldown /<br/>draining / unhealthy /<br/>stale / not_configured])

    Start --> PoolConfigured
    PoolConfigured -- no --> Health
    PoolConfigured -- yes --> PoolState
    PoolState -- quarantined / excluded /<br/>in-cooldown / draining --> Deny
    PoolState -- routable --> Health
    Health -- ready / degraded --> Weight --> PoolScore --> Cap --> OK
    Health -- unreachable / stale --> Deny
```

Code anchors:
- Dispatch entry: `capability-broker/internal/server/dispatch.go`
- Selection: `capability-broker/internal/server/capability_group.go:82-160` (`selectBackend`)
- Decision blend: `capability-broker/internal/selection/decision.go`
- Pool snapshot cache: `capability-broker/internal/poolsnapshot/cache.go`
- Backend overrides (operator escapes): `pool-controller` `/admin/v1/backend-overrides/{quarantine,drain,warmup,max-share-cap}`

## How the three flows hand off

```
[Signup] ──provisions──► broker host-config (per-cap backends w/ member_eth)
                              │
                              ▼
                    [Work Routing] ──emits──► work receipts (stub→final)
                                              backend outcomes
                                                    │
                                                    ▼
                                              [Payout] ── round close →
                                              per-member intents → ETH
```

Signup feeds the routing config. Routing emits the data structures payout
consumes. Payout state never feeds back into routing — operator overrides on
`pool-controller` are the only path that re-shapes selection.

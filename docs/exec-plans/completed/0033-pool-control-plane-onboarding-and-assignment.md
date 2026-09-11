---
plan: 0033
title: Pool control plane onboarding and offer-assignment reset
status: superseded
phase: shipped
opened: 2026-05-18
owner: harness
related:
  - "completed plan 0029 — pool node design"
  - "active plan 0031 — pool follow-up backlog"
  - "completed plan 0018 — orch coordinator design"
---

> **Superseded by plan 0044** ([0044-zero-touch-pool-onboarding.md](../active/0044-zero-touch-pool-onboarding.md)).
> The member model this plan specifies — pool members holding their own backend URLs and offer assignments — was deleted. Placement is now by policy over templates and attached runners. Every type, route and screen below names code that no longer exists.
>
> Kept as provenance: this is what was decided and shipped at the time,
> and the reasoning is still worth reading. It is not a description of
> the system today. Nothing below has been edited.

# Plan 0033 — Pool control plane onboarding and offer-assignment reset

## Completion summary

This plan shipped.

Delivered outcomes:

- orch-owned offers persisted in `pool-controller`
- member join-request API and operator review/approval flow
- approved member and backend persistence
- explicit backend-to-offer assignments with status controls
- assignment-candidate and admission-review helpers for operator workflows
- broker runtime rendered from controller state rather than operator-edited
  member/offer YAML
- admin UI and API surfaces for offers, members, backends, assignments, audit,
  and broker-runtime apply

Representative landed code:

- `pool-controller/internal/server/member/routes.go`
- `pool-controller/internal/server/admin/routes.go`
- `pool-controller/internal/service/brokerrender/render.go`
- `pool-controller/internal/service/admissionreview/service.go`
- `pool-controller/internal/ui/adminpage/`

## 1. Problem

The current Pool implementation is still fundamentally config-driven at the
member/offer layer. `pool-controller` persists operational/accounting state,
but the source of truth for Pool members, their backends, and the offerings
they publish still lives in a YAML file that the orch operator edits manually.

That shape is misaligned with the intended Pool product model:

- the orch operator should define the canonical market offers
- a member should request admission through a backend/API flow, not by asking
  the orch operator to edit a file
- the orch operator should accept or reject the member
- an accepted member backend should be assigned to one or more orch-defined
  offers
- only those approved assignments should render into the broker's advertised
  inventory

Today those concerns are collapsed together in
`members[].backends[].offerings[]`, which makes the implementation behave more
like "operator-curated backend inventory" than "orch-owned offer catalog plus
member admission and assignment."

## 2. Goal

Turn `pool-controller` into the actual Pool control plane for onboarding and
assignment:

- no manual file edits for Pool members
- orch-owned offer catalog
- member join-request flow
- operator approval/rejection flow
- explicit backend-to-offer assignment flow
- broker runtime state derived from persisted controller state
- admin UI/API as the operator surface

The secure-orch sign cycle, `orch-coordinator`, and public gateway-facing
protocols remain unchanged.

## 3. Non-goals

### 3.1 Out of scope for this plan

- changing manifest schema, gateway behavior, or on-chain protocol surfaces
- removing the secure-orch cold-sign cycle
- full HA / clustered `pool-controller`
- fully automatic member approval
- mandatory auto-discovery for every workload family before operator approval
- member-set public pricing

### 3.2 Explicitly preserved

- `capability-broker` remains the paid data-plane ingress
- `orch-coordinator` still scrapes broker `/registry/offerings` and
  `/registry/health`
- `pool-reconciler` and `pool-payout-executor` remain the accounting/payout
  downstream path

## 4. Locked product decisions

### 4.1 Orch owns the public market offer

Canonical public offers are created and edited by the orch operator, not by
Pool members.

Each offer defines the public-facing tuple:

- `capability_id`
- `offering_id`
- `interaction_mode`
- `work_unit`
- `price`
- canonical `extra`
- canonical `constraints`

If a member can serve something the orch has not defined as an offer, that
capability is irrelevant and must not be published.

### 4.2 Member onboarding is API/UI-driven

Pool members do not land in the Pool by direct file editing. They submit a
join request via a backend/API surface. The operator reviews and acts on that
request through an admin UI/API surface.

### 4.3 Admission and assignment are separate actions

Member approval does not imply publication.

The operator first accepts the member, then assigns one or more accepted
member backends to one or more orch-defined offers. Unassigned backends are
kept out of published broker inventory.

### 4.4 Broker config is derived state

The broker `host-config.yaml` remains a valid runtime artifact for
`capability-broker`, but it becomes an internal derived artifact rendered from
controller state rather than the operator-authored source of truth.

### 4.5 Member capability claims are informational until assigned

Join-request backend capability claims, metadata discovery, and health probes
are used to inform operator approval and compatibility checks, but they do not
create public offers or public routing state on their own.

## 5. Required model split

The following first-class entities are required in `pool-controller` state.

### 5.1 Offers

Orch-owned canonical offers.

Fields:

- `id`
- `capability_id`
- `offering_id`
- `interaction_mode`
- `work_unit`
- `price`
- `extra`
- `constraints`
- `status` (`active`, `disabled`)
- timestamps

### 5.2 Join requests

Member-submitted onboarding requests.

Fields:

- `id`
- `member_eth_address`
- `display_name`
- `payout_mode`
- requested backend list
- claimed capability / metadata list
- status (`pending`, `approved`, `rejected`, `withdrawn`)
- review reason
- timestamps

### 5.3 Members

Approved Pool members.

Fields:

- `id`
- `eth_address`
- `display_name`
- `payout_mode`
- status (`active`, `suspended`)
- source join-request ID
- timestamps

### 5.4 Member backends

Concrete serving endpoints controlled by approved members.

Fields:

- `id`
- `member_id`
- `transport`
- `url`
- `auth`
- `health_probe`
- claimed capabilities / discovered metadata
- verification status
- runtime status (`active`, `draining`, `disabled`)
- timestamps

### 5.5 Assignments

Explicit operator-approved mappings from member backend to orch-owned offer.

Fields:

- `id`
- `offer_id`
- `member_backend_id`
- status (`active`, `draining`, `disabled`)
- optional notes / policy fields
- timestamps

## 6. Required backend changes

### 6.1 Replace config-driven member/offer ownership

Current config grammar in
[`pool-controller/internal/config/config.go`](../../../pool-controller/internal/config/config.go)
stores offerings under backends under members. That must no longer be the
Pool source of truth for member onboarding or assignment.

Bootstrap YAML may remain for:

- orch identity
- admin auth bootstrap
- controller listen/bootstrap settings
- optional initial offer import path

But not for ongoing member lifecycle management.

### 6.2 Add persistent repositories for Pool control-plane entities

Introduce first-class storage for:

- offers
- join requests
- members
- member backends
- assignments
- audit events

These belong under `pool-controller/internal/repo/` and become the basis for
admin/member API handlers and runtime rendering.

### 6.3 Rewrite broker render logic around offers + assignments

Current render logic in
[`pool-controller/internal/service/configgen/generate.go`](../../../pool-controller/internal/service/configgen/generate.go)
iterates `members -> backends -> offerings`.

It must instead:

1. load active offers
2. load active members and member backends
3. load active assignments
4. materialize broker `capabilities[]` by merging:
   - orch-owned offer fields
   - backend-owned endpoint/auth/health fields
   - Pool metadata (`member_eth_address`, `member_display_name`,
     `member_backend_id`, `payout_mode`)

That merge is the core mechanical fix for the current architectural mismatch.

### 6.4 Add join-request services

`pool-controller` needs services to:

- accept join requests
- validate shape and auth
- probe backend reachability / metadata where possible
- persist pending review state
- surface verification results to the operator

### 6.5 Add approval/rejection services

Admin actions must support:

- approving a join request into a member + member backends
- rejecting a join request with reason
- suspending/reactivating approved members

### 6.6 Add assignment services

Admin actions must support:

- assigning an approved member backend to an orch offer
- unassigning it
- draining or disabling an assignment
- validating compatibility between backend claims and offer requirements

### 6.7 Add broker desired-state and apply status

The controller needs a notion of broker desired state and rollout status:

- current rendered config revision
- last applied revision
- diff between desired and applied
- apply / reload result tracking

This keeps operator actions tied to runtime convergence rather than only DB
mutation.

## 7. Required API surfaces

### 7.1 Operator admin API

Minimum required endpoints:

- `GET /admin/v1/offers`
- `POST /admin/v1/offers`
- `PATCH /admin/v1/offers/:id`
- `GET /admin/v1/join-requests`
- `GET /admin/v1/join-requests/:id`
- `POST /admin/v1/join-requests/:id/approve`
- `POST /admin/v1/join-requests/:id/reject`
- `GET /admin/v1/members`
- `GET /admin/v1/members/:id`
- `PATCH /admin/v1/members/:id/status`
- `GET /admin/v1/member-backends`
- `POST /admin/v1/member-backends/:id/verify`
- `GET /admin/v1/assignments`
- `POST /admin/v1/assignments`
- `PATCH /admin/v1/assignments/:id/status`
- `DELETE /admin/v1/assignments/:id`
- `GET /admin/v1/broker-config`
- `POST /admin/v1/broker-config/apply`

Exact path naming may differ, but this capability set is required.

### 7.2 Member API

Minimum required endpoints:

- `POST /member/v1/join-requests`
- `GET /member/v1/join-requests/:id`
- `POST /member/v1/join-requests/:id/refresh`

Future member endpoints may be added later, but this is sufficient for the
first no-file-edit onboarding flow.

## 8. Required UI surfaces

### 8.1 Offers screen

Operator can:

- list offers
- create/edit/disable offers
- inspect pricing, mode, constraints, and metadata

### 8.2 Join requests screen

Operator can:

- view pending requests
- inspect backend URLs, claimed capabilities, and verification results
- approve or reject with a reason

### 8.3 Members screen

Operator can:

- view approved and suspended members
- inspect payout mode and member backends
- see backend verification/runtime status

### 8.4 Assignments screen

Operator can:

- assign approved backends to orch offers
- remove assignments
- drain or disable assignments
- see unassigned but approved backends

### 8.5 Broker runtime screen

Operator can:

- inspect current rendered broker state
- compare desired vs applied state
- see apply/reload health and revision status

### 8.6 Accounting screens

Existing accounting surfaces stay, but should link back to:

- member
- backend
- assignment
- offer

where applicable.

## 9. Compatibility and policy rules

### 9.1 Publication rule

A capability is published only if all of the following are true:

1. the orch offer exists and is active
2. the member is approved and active
3. the member backend is active
4. the assignment exists and is active

### 9.2 Capability possession is not publication authority

A member's ability to serve a capability does not grant it the right to publish
that capability under the orch identity. Only operator-approved assignments do
that.

### 9.3 Public pricing is orch-owned

Public gateway-facing price is defined by the orch offer. Member payout terms
are internal Pool economics and must not redefine public offer identity.

## 10. Migration path

### Phase 1 — backend model reset

- add persistent entities for offers, join requests, members, backends,
  assignments
- keep YAML only for bootstrap config
- support operator-created offers and members through admin API

### Phase 2 — render path reset

- rewrite broker config generation around offers + assignments
- preserve existing `extra.pool.*` metadata in rendered capabilities
- expose desired broker config revision and diff

### Phase 3 — onboarding flow

- add member join-request API
- add approval/rejection flow
- add backend verification / metadata discovery

### Phase 4 — admin UI completion

- add offers, join requests, members, assignments, and broker runtime views
- remove operational dependence on member YAML edits entirely

### Phase 5 — rollout/apply hardening

- add explicit broker apply/reload flow
- add applied-vs-desired revision tracking
- add operator-visible rollout status and error handling

## 11. Implementation slices

This section translates the target architecture into concrete repo work. The
recommended sequence is deliberately front-loaded on storage and rendering so
the no-file-edit invariant lands before member self-service polish.

### Slice A — bootstrap/config contraction

**Packages/files:**

- `pool-controller/internal/config/config.go`
- `pool-controller/internal/config/load.go`
- `pool-controller/examples/*`
- `pool-controller/README.md`
- `pool-controller/RUNBOOK.md`

**Work:**

- reduce controller config to bootstrap-only concerns
- preserve orch identity, admin auth, listen, payment-daemon, and optional
  import/bootstrap flags
- remove ongoing member/backend/offering ownership from the runtime source of
  truth
- document that member onboarding no longer depends on operator-edited YAML

**Acceptance criteria:**

- controller can start without `members[].backends[].offerings[]` in config
- bootstrap config remains sufficient for local/dev/prod startup
- docs stop describing member onboarding as config-edit driven

### Slice B — persistent control-plane entities

**Packages/files:**

- `pool-controller/internal/types/`
- `pool-controller/internal/repo/`

**Add:**

- `internal/types/offers.go`
- `internal/types/join_requests.go`
- `internal/types/members.go`
- `internal/types/member_backends.go`
- `internal/types/assignments.go`
- `internal/repo/offers.go`
- `internal/repo/join_requests.go`
- `internal/repo/members.go`
- `internal/repo/member_backends.go`
- `internal/repo/assignments.go`
- `internal/repo/audit.go`

**Work:**

- define first-class persisted entities for offers, join requests, members,
  member backends, assignments, and audit events
- keep existing receipts / payout state intact
- define repository contracts for create/list/get/update/status mutation paths

**Acceptance criteria:**

- entity lifecycle tests exist for each new repo
- controller can persist and reload control-plane state across restarts
- membership and assignment state no longer requires YAML snapshots to exist

### Slice C — broker render reset

**Packages/files:**

- replace or supersede
  `pool-controller/internal/service/configgen/generate.go`
- add `pool-controller/internal/service/brokerrender/`
- touch `pool-controller/cmd/livepeer-pool-controller/main.go`

**Work:**

- render broker desired state from:
  - active offers
  - active members
  - active member backends
  - active assignments
- preserve emitted `extra.pool.*` metadata
- add deterministic desired-state revisioning
- expose desired rendered config independent of static config reload

**Acceptance criteria:**

- rendered broker config contains only active assigned backends
- unassigned approved backends do not appear in rendered capabilities
- repeated renders from identical state are byte-stable
- existing broker consumer assumptions still hold

### Slice D — admin API split and control-plane CRUD

**Packages/files:**

- `pool-controller/cmd/livepeer-pool-controller/main.go`
- add `pool-controller/internal/server/admin/`

**Add:**

- `internal/server/admin/offers.go`
- `internal/server/admin/join_requests.go`
- `internal/server/admin/members.go`
- `internal/server/admin/assignments.go`
- `internal/server/admin/broker_runtime.go`

**Work:**

- move control-plane handlers out of `main.go`
- implement admin CRUD/status flows for offers, members, assignments, and
  broker runtime/apply status
- preserve existing admin auth behavior

**Acceptance criteria:**

- operator can manage offers without touching YAML
- operator can create/inspect members and assignments through API
- control-plane routes are no longer concentrated in one monolithic handler
  file

### Slice E — member onboarding API

**Packages/files:**

- add `pool-controller/internal/server/member/`
- add `pool-controller/internal/service/joinrequests/`
- add `pool-controller/internal/service/backendverify/`

**Work:**

- add join-request submit/status/refresh APIs
- persist pending requests without publishing them
- record verification / discovery results for operator review
- enforce that approval is still an explicit operator action

**Acceptance criteria:**

- a member can request onboarding without operator file edits
- pending join requests are visible to operators before approval
- rejected or withdrawn requests never affect broker desired state

### Slice F — compatibility and assignment policy

**Packages/files:**

- add `pool-controller/internal/service/compat/`
- add `pool-controller/internal/service/offers/`
- add `pool-controller/internal/service/assignments/`

**Work:**

- validate backend claim compatibility before assignment
- encode the locked product rule that capability possession is not publication
  authority
- validate interaction mode, transport, health/probe expectations, and
  workload-family-specific metadata where available

**Acceptance criteria:**

- invalid assignments fail before broker render
- public offer identity and price always come from the orch-owned offer record
- assignment logic is testable independently of HTTP handlers

### Slice G — operator UI

**Packages/files:**

- new or expanded frontend assets under `pool-controller/` as the chosen UI
  implementation lands
- admin route integration in controller server

**Work:**

- add offers screen
- add join requests review screen
- add members screen
- add assignments screen
- add broker runtime / desired-vs-applied screen

**Acceptance criteria:**

- operator can complete onboarding and assignment from UI alone
- UI does not require hidden file edits to make state take effect
- broker runtime state is inspectable from the same operator surface

### Slice H — rollout/apply reconciliation

**Packages/files:**

- `pool-controller/internal/service/brokerrender/`
- admin broker runtime handlers
- any broker reload/apply integration point chosen for the first slice

**Work:**

- track desired revision vs applied revision
- surface apply/reload status and last error
- avoid silent divergence between controller state and broker runtime state

**Acceptance criteria:**

- operator can tell whether a control-plane change is merely persisted or has
  actually reached broker runtime
- last-applied state survives restart
- broker rollout failures are visible and auditable

## 12. Acceptance criteria

This plan is complete when all of the following are true:

1. A new member can onboard without the orch operator editing a file.
2. The orch operator can create and edit canonical offers in UI/API.
3. A member can be approved without being automatically published.
4. Only assigned member backends appear in rendered broker config.
5. Broker config is derived from persisted controller state, not member YAML.
6. Operator UI/API exposes offer management, onboarding review, assignment, and
   broker runtime/apply state.
7. Existing accounting and payout flows still attribute work to the correct
   member/backend/offer lineage.

## 13. First shippable milestone

The first shippable slice should be the minimum architecture correction:

1. introduce persisted offers, members, member backends, and assignments
2. rewrite broker rendering around those entities
3. expose admin APIs for offer management and member/backend assignment
4. allow operator-created members through API/UI

That removes manual member file editing before the full self-service member
join flow lands.

## 14. Recommended PR order

To keep reviews short-lived and reduce migration risk:

1. Slice A + Slice B
2. Slice C
3. Slice D
4. Slice F
5. Slice E
6. Slice G
7. Slice H

This order intentionally lands the architecture correction before the polished
member UX.

## 15. Detailed checklist for Slice A and Slice B

This section is the concrete starting point for implementation. It is written
to support the first code changes directly.

### 15.1 Slice A — bootstrap/config contraction

#### A1. Narrow the config contract

**File:** `pool-controller/internal/config/config.go`

**Change:**

- replace the current config-first member/offering model with a bootstrap-only
  config model
- remove `Members []Member` from the long-term runtime source-of-truth contract
- keep only bootstrap fields needed to start the controller process

**Target bootstrap config shape:**

- `identity`
- `admin_auth`
- `listen`
- `synthetic_probes`
- `scoring`
- `payment_daemon`
- optional:
  - `bootstrap.import_legacy_config_path`
  - `bootstrap.auto_import_legacy_config`

**Add types:**

- `type Bootstrap struct`
- `type LegacyImport struct`

Suggested shape:

```go
type Config struct {
    Identity        Identity        `yaml:"identity"`
    AdminAuth       AdminAuth       `yaml:"admin_auth,omitempty"`
    Listen          Listen          `yaml:"listen,omitempty"`
    SyntheticProbes SyntheticProbes `yaml:"synthetic_probes,omitempty"`
    Scoring         Scoring         `yaml:"scoring,omitempty"`
    PaymentDaemon   PaymentDaemon   `yaml:"payment_daemon,omitempty"`
    Bootstrap       Bootstrap       `yaml:"bootstrap,omitempty"`
}

type Bootstrap struct {
    ImportLegacyConfigPath string `yaml:"import_legacy_config_path,omitempty"`
    AutoImportLegacyConfig bool   `yaml:"auto_import_legacy_config,omitempty"`
}
```

#### A2. Introduce a legacy-import path instead of hard-cut removal

**Files:**

- `pool-controller/internal/config/load.go`
- `pool-controller/internal/config/load_test.go`

**Change:**

- preserve the ability to parse the old member/offering YAML shape only for a
  migration/import command path
- do not require that shape for ordinary `serve`
- add validation for bootstrap-only config

**Historical note:**

This migration note has been superseded by `M4`. The final implementation did
not retain a separate runtime `LegacyConfig` surface; the legacy import/generate
path was removed instead.

#### A3. Historical migration option: explicit import command

**File:** `pool-controller/cmd/livepeer-pool-controller/main.go`

**Outcome:**

- this option was explored during the migration plan
- the final `M4` cut removed `import-legacy-config` rather than preserving it

**Responsibilities:**

- load a legacy member/offering config
- map it into persisted `offers`, `members`, `member_backends`, and
  `assignments`
- write an audit event recording the import
- refuse to overwrite existing state unless an explicit flag is set

**Suggested flags:**

- `--config`
- `--data-dir`
- `--replace-existing=false`
- `--dry-run=false`

#### A4. Stop `serve` from depending on rendered legacy config snapshots

**File:** `pool-controller/cmd/livepeer-pool-controller/main.go`

**Current issue:**

`runServe` currently loads config, renders broker YAML immediately, and stores
that as the active snapshot boundary.

**Change:**

- bootstrap `serve` from:
  - bootstrap config
  - persisted control-plane entities
  - derived broker renderer
- treat rendered broker config as derived state, not the startup truth source

#### A5. Update examples and operator docs

**Files:**

- `pool-controller/examples/pool-controller-config.example.yaml`
- `pool-controller/examples/pool-controller-config.compose.yaml`
- `pool-controller/README.md`
- `pool-controller/RUNBOOK.md`

**Change:**

- replace examples that imply operator-authored member lists as the normal
  production workflow
- if legacy examples are retained, mark them clearly as migration/bootstrap
  aids only

### 15.2 Slice B — persisted control-plane entities

#### B1. Add canonical domain types

**Files to add:**

- `pool-controller/internal/types/offers.go`
- `pool-controller/internal/types/join_requests.go`
- `pool-controller/internal/types/members.go`
- `pool-controller/internal/types/member_backends.go`
- `pool-controller/internal/types/assignments.go`

**Initial structs:**

`offers.go`

```go
type OfferStatus string

const (
    OfferStatusActive   OfferStatus = "active"
    OfferStatusDisabled OfferStatus = "disabled"
)

type Offer struct {
    ID              string         `json:"id"`
    CapabilityID    string         `json:"capability_id"`
    OfferingID      string         `json:"offering_id"`
    InteractionMode string         `json:"interaction_mode"`
    WorkUnit        WorkUnit       `json:"work_unit"`
    Price           Price          `json:"price"`
    Extra           map[string]any `json:"extra,omitempty"`
    Constraints     map[string]any `json:"constraints,omitempty"`
    Status          OfferStatus    `json:"status"`
    CreatedAt       time.Time      `json:"created_at"`
    UpdatedAt       time.Time      `json:"updated_at"`
}
```

`join_requests.go`

```go
type JoinRequestStatus string

const (
    JoinRequestPending   JoinRequestStatus = "pending"
    JoinRequestApproved  JoinRequestStatus = "approved"
    JoinRequestRejected  JoinRequestStatus = "rejected"
    JoinRequestWithdrawn JoinRequestStatus = "withdrawn"
)

type JoinRequest struct {
    ID               string                 `json:"id"`
    MemberEthAddress string                 `json:"member_eth_address"`
    DisplayName      string                 `json:"display_name,omitempty"`
    PayoutMode       string                 `json:"payout_mode"`
    RequestedBackends []RequestedBackend    `json:"requested_backends"`
    Status           JoinRequestStatus      `json:"status"`
    ReviewReason     string                 `json:"review_reason,omitempty"`
    SubmittedAt      time.Time              `json:"submitted_at"`
    ReviewedAt       *time.Time             `json:"reviewed_at,omitempty"`
}

type RequestedBackend struct {
    ID                  string         `json:"id"`
    Transport           string         `json:"transport"`
    URL                 string         `json:"url"`
    Auth                AuthConfig     `json:"auth,omitempty"`
    HealthProbe         HealthProbe    `json:"health_probe,omitempty"`
    ClaimedCapabilities []ClaimedOffer `json:"claimed_capabilities,omitempty"`
}

type ClaimedOffer struct {
    CapabilityID    string         `json:"capability_id"`
    OfferingID      string         `json:"offering_id,omitempty"`
    InteractionMode string         `json:"interaction_mode,omitempty"`
    Extra           map[string]any `json:"extra,omitempty"`
    Constraints     map[string]any `json:"constraints,omitempty"`
}
```

`members.go`

```go
type MemberStatus string

const (
    MemberStatusActive    MemberStatus = "active"
    MemberStatusSuspended MemberStatus = "suspended"
)

type Member struct {
    ID                  string       `json:"id"`
    EthAddress          string       `json:"eth_address"`
    DisplayName         string       `json:"display_name,omitempty"`
    PayoutMode          string       `json:"payout_mode"`
    Status              MemberStatus `json:"status"`
    SourceJoinRequestID string       `json:"source_join_request_id,omitempty"`
    CreatedAt           time.Time    `json:"created_at"`
    UpdatedAt           time.Time    `json:"updated_at"`
}
```

`member_backends.go`

```go
type VerificationStatus string
type BackendStatus string

const (
    VerificationUnknown VerificationStatus = "unknown"
    VerificationPassing VerificationStatus = "passing"
    VerificationFailing VerificationStatus = "failing"
)

const (
    BackendStatusActive   BackendStatus = "active"
    BackendStatusDraining BackendStatus = "draining"
    BackendStatusDisabled BackendStatus = "disabled"
)

type MemberBackend struct {
    ID                  string             `json:"id"`
    MemberID            string             `json:"member_id"`
    Transport           string             `json:"transport"`
    URL                 string             `json:"url"`
    Auth                AuthConfig         `json:"auth,omitempty"`
    HealthProbe         HealthProbe        `json:"health_probe,omitempty"`
    ClaimedCapabilities []ClaimedOffer     `json:"claimed_capabilities,omitempty"`
    VerificationStatus  VerificationStatus `json:"verification_status"`
    VerificationError   string             `json:"verification_error,omitempty"`
    LastVerifiedAt      *time.Time         `json:"last_verified_at,omitempty"`
    Status              BackendStatus      `json:"status"`
    CreatedAt           time.Time          `json:"created_at"`
    UpdatedAt           time.Time          `json:"updated_at"`
}
```

`assignments.go`

```go
type AssignmentStatus string

const (
    AssignmentStatusActive   AssignmentStatus = "active"
    AssignmentStatusDraining AssignmentStatus = "draining"
    AssignmentStatusDisabled AssignmentStatus = "disabled"
)

type Assignment struct {
    ID              string           `json:"id"`
    OfferID         string           `json:"offer_id"`
    MemberBackendID string           `json:"member_backend_id"`
    Status          AssignmentStatus `json:"status"`
    Notes           string           `json:"notes,omitempty"`
    CreatedAt       time.Time        `json:"created_at"`
    UpdatedAt       time.Time        `json:"updated_at"`
}
```

#### B2. Add repositories with narrow, explicit contracts

**Files to add:**

- `pool-controller/internal/repo/offers.go`
- `pool-controller/internal/repo/join_requests.go`
- `pool-controller/internal/repo/members.go`
- `pool-controller/internal/repo/member_backends.go`
- `pool-controller/internal/repo/assignments.go`
- `pool-controller/internal/repo/audit.go`

**Repository method baseline:**

Offers repo:

- `PutOffer`
- `GetOffer`
- `ListOffers`
- `SetOfferStatus`
- `DeleteOffer` if allowed, otherwise disable-only

Join requests repo:

- `PutJoinRequest`
- `GetJoinRequest`
- `ListJoinRequests`
- `SetJoinRequestStatus`

Members repo:

- `PutMember`
- `GetMember`
- `ListMembers`
- `SetMemberStatus`

Member backends repo:

- `PutMemberBackend`
- `GetMemberBackend`
- `ListMemberBackends`
- `ListMemberBackendsByMember`
- `SetMemberBackendStatus`
- `SetVerificationResult`

Assignments repo:

- `PutAssignment`
- `GetAssignment`
- `ListAssignments`
- `ListAssignmentsByOffer`
- `ListAssignmentsByBackend`
- `SetAssignmentStatus`
- `DeleteAssignment` if allowed, otherwise disable-only

Audit repo:

- `AppendAuditEvent`
- `ListAuditEvents`

#### B3. Use stable bucket layout and foreign-key-like validation in service layer

**Files:**

- new repo files
- later service packages

**Decision:**

- Bolt buckets should stay simple
- cross-entity validation should happen in service layer, not Bolt helpers

Suggested buckets:

- `offers`
- `join_requests`
- `members`
- `member_backends`
- `assignments`
- `audit_events`

#### B4. Add lifecycle tests before HTTP work

**Files to add:**

- `pool-controller/internal/repo/offers_test.go`
- `pool-controller/internal/repo/join_requests_test.go`
- `pool-controller/internal/repo/members_test.go`
- `pool-controller/internal/repo/member_backends_test.go`
- `pool-controller/internal/repo/assignments_test.go`
- `pool-controller/internal/repo/audit_test.go`

**Test cases:**

- create/get/list for each entity
- status transitions
- persistence across repo reopen
- deterministic ordering for list responses
- duplicate ID handling

#### B5. Add legacy import mapping helpers

**Files to add:**

- `pool-controller/internal/service/legacyimport/import.go`
- `pool-controller/internal/service/legacyimport/import_test.go`

**Work:**

- convert old `members[].backends[].offerings[]` config into:
  - distinct offers
  - members
  - member backends
  - assignments

**Important mapping rule:**

- legacy offerings that are identical at the orch-public layer should become a
  single canonical `Offer`
- repeated backend candidates should become multiple `Assignments` to that
  single offer

That rule prevents the legacy shape from polluting the new offer model with
member-owned duplication.

#### B6. Record audit lineage from day one

**Why now:**

Approval, rejection, assignment, import, and future apply/reload actions are
operator-sensitive. Audit should not be bolted on later.

**Minimum event kinds:**

- `legacy_import`
- `offer_created`
- `offer_updated`
- `offer_disabled`
- `join_request_submitted`
- `join_request_approved`
- `join_request_rejected`
- `member_status_changed`
- `backend_verified`
- `assignment_created`
- `assignment_status_changed`

### 15.3 Gate to exit Slice A and Slice B

Do not move to new admin/member HTTP surfaces until all of the following are
true:

1. bootstrap config no longer carries live member inventory as required state
2. control-plane entities persist correctly across restart
3. legacy config can be imported into the new state model
4. there is a testable repository boundary for offers, members, backends, and
   assignments
5. broker rendering work can begin without reusing the old nested config model

## 16. Detailed checklist for Slice C — broker render reset

Slice C is where the new control-plane model becomes operational. Until this
slice lands, the system may store offers, members, backends, and assignments,
but it still will not publish the correct broker runtime state.

### 16.1 Replace `configgen` as the source of truth

**Current file:**

- `pool-controller/internal/service/configgen/generate.go`

**Current behavior:**

- iterate `members -> backends -> offerings`
- flatten directly into broker `capabilities[]`
- copy bootstrap-level identity/listen/payment-daemon/receipt-sink fields from
  config

**Required change:**

- keep the output contract broker-compatible
- stop treating nested config as the canonical input model
- render from persisted entities plus bootstrap/runtime settings

This can be done in one of two ways:

1. replace `configgen` in place while preserving its package name, or
2. add a new package such as `internal/service/brokerrender/` and let
   `configgen` become a thin compatibility shim

Preferred approach: add `brokerrender/` and leave `configgen` as a small
wrapper during migration so the old and new call sites can coexist briefly.

### 16.2 Add a dedicated render package

**Files to add:**

- `pool-controller/internal/service/brokerrender/model.go`
- `pool-controller/internal/service/brokerrender/render.go`
- `pool-controller/internal/service/brokerrender/revision.go`
- `pool-controller/internal/service/brokerrender/render_test.go`
- `pool-controller/internal/service/brokerrender/revision_test.go`

**Responsibilities:**

- load effective control-plane state
- materialize broker-facing config model
- produce deterministic YAML bytes
- compute stable revision/hash from the rendered desired state

### 16.3 Separate bootstrap/runtime settings from assignment-derived capabilities

The rendered broker config has two input classes:

1. bootstrap/runtime settings
2. assignment-derived capabilities

**Bootstrap/runtime settings still come from controller bootstrap config:**

- `identity.orch_eth_address`
- optional `identity.label`
- broker listen settings if controller still owns them
- `payment_daemon`
- `receipt_sink`
- optional Pool snapshot/other broker-side settings if retained

**Assignment-derived capability entries come from persisted state:**

- `Offer`
- `Member`
- `MemberBackend`
- `Assignment`

This split should be explicit in the render code so future operator actions
cannot accidentally mutate bootstrap-only values.

### 16.4 Define the render input model explicitly

**File:** `pool-controller/internal/service/brokerrender/model.go`

Suggested inputs:

```go
type BootstrapBrokerSettings struct {
    Identity      config.Identity
    Listen        config.Listen
    PaymentDaemon config.PaymentDaemon
    ReceiptSink   config.ReceiptSink
}

type RenderInput struct {
    Bootstrap   BootstrapBrokerSettings
    Offers      []types.Offer
    Members     []types.Member
    Backends    []types.MemberBackend
    Assignments []types.Assignment
}
```

Suggested output:

```go
type RenderResult struct {
    ConfigYAML []byte
    Revision   string
    Model      BrokerConfig
}
```

Where `BrokerConfig` mirrors the broker runtime contract, not the controller's
control-plane entities.

### 16.5 Reuse a broker-facing config model, not control-plane entities directly

The render package should produce a broker-facing model that is structurally
close to the existing `host-config.yaml` contract:

- `identity`
- `listen`
- `payment_daemon`
- `receipt_sink`
- `capabilities[]`

Suggested internal shape:

```go
type BrokerConfig struct {
    Identity      config.Identity      `yaml:"identity"`
    Listen        config.Listen        `yaml:"listen,omitempty"`
    PaymentDaemon config.PaymentDaemon `yaml:"payment_daemon,omitempty"`
    ReceiptSink   config.ReceiptSink   `yaml:"receipt_sink,omitempty"`
    Capabilities  []BrokerCapability   `yaml:"capabilities"`
}

type BrokerCapability struct {
    ID              string          `yaml:"id"`
    OfferingID      string          `yaml:"offering_id"`
    InteractionMode string          `yaml:"interaction_mode"`
    WorkUnit        config.WorkUnit `yaml:"work_unit"`
    Health          config.Health   `yaml:"health,omitempty"`
    Price           config.Price    `yaml:"price"`
    Backend         BrokerBackend   `yaml:"backend"`
    Extra           map[string]any  `yaml:"extra,omitempty"`
    Constraints     map[string]any  `yaml:"constraints,omitempty"`
}

type BrokerBackend struct {
    ID        string            `yaml:"id,omitempty"`
    Transport string            `yaml:"transport"`
    URL       string            `yaml:"url,omitempty"`
    Auth      config.AuthConfig `yaml:"auth,omitempty"`
}
```

That keeps `capability-broker` compatibility stable while the controller
architecture changes under it.

### 16.6 Define the merge rules precisely

Each rendered `BrokerCapability` must come from one active assignment joining:

- one active `Offer`
- one active `Member`
- one active `MemberBackend`
- one active `Assignment`

**Offer-owned fields:**

- `id` / `capability_id`
- `offering_id`
- `interaction_mode`
- `work_unit`
- `price`
- canonical `extra`
- canonical `constraints`

**Backend-owned fields:**

- `backend.id`
- `backend.transport`
- `backend.url`
- `backend.auth`
- `health`

**Member-owned Pool metadata injected into `extra.pool`:**

- `member_eth_address`
- `member_display_name`
- `member_backend_id`
- `payout_mode`

**Merge precedence:**

1. offer data defines public offer identity and price
2. backend data defines the concrete serving endpoint and health probe
3. pool metadata is additive under `extra.pool`
4. backend claims never override orch-owned public price or public offer ID

### 16.7 Enforce render eligibility gates

The renderer must omit any assignment unless all four linked records are in an
eligible state:

- `Offer.status == active`
- `Member.status == active`
- `MemberBackend.status == active`
- `Assignment.status == active`

This is the enforcement point for the product rule "capability possession is
not publication authority."

### 16.8 Deterministic ordering rules

Render output must be byte-stable for identical effective state.

Suggested sort order for `capabilities[]`:

1. `capability_id`
2. `offering_id`
3. `member_eth_address`
4. `member_backend_id`

This preserves the old deterministic spirit of `configgen` while switching to
the new model.

### 16.9 Revisioning and diff identity

**Files:**

- `pool-controller/internal/service/brokerrender/revision.go`
- `pool-controller/internal/service/brokerrender/revision_test.go`

**Work:**

- define a stable revision derived from the rendered broker config bytes
- expose revision as a hash, for example SHA-256 hex of canonical YAML bytes

Suggested helper:

```go
func RevisionFor(raw []byte) string
```

**Important rule:**

- revision must be based on rendered desired runtime state, not timestamps or
  mutable audit metadata

That allows desired-vs-applied comparisons later.

### 16.10 Track desired broker state separately from config snapshots

Current snapshot persistence in `pool-controller` is config-centric. Slice C
should introduce a cleaner desired-state record for broker runtime.

**Files to add:**

- `pool-controller/internal/repo/broker_runtime.go`
- `pool-controller/internal/repo/broker_runtime_test.go`

Suggested stored fields:

- `revision`
- `rendered_yaml`
- `rendered_at`
- `source_summary`
  - offer count
  - member count
  - backend count
  - assignment count

This repo is for desired broker state, not yet for applied-state orchestration.

### 16.11 Update `serve` to render from persisted state

**File:** `pool-controller/cmd/livepeer-pool-controller/main.go`

**Change:**

- on startup, load bootstrap config
- open state repos
- load persisted offers/members/backends/assignments
- call `brokerrender`
- persist the desired broker runtime record
- serve `GET /admin/v1/broker-config` from desired runtime state instead of
  legacy rendered-config snapshots

This is the main runtime behavior change for Slice C.

### 16.12 Keep a compatibility facade during migration

The existing code and docs refer to "broker config generation" as a controller
surface. This note is now historical only.

Final outcome:

- `generate-broker-config` was removed in `M4`
- broker runtime is now derived internally from persisted state
- normal operator flow goes through control-plane APIs and
  `POST /admin/v1/broker-runtime/apply`

### 16.13 Render tests to add

**File:** `pool-controller/internal/service/brokerrender/render_test.go`

Required test cases:

1. one offer + one member backend + one assignment renders one capability
2. two member backends assigned to one offer render repeated capability tuples
   with different `extra.pool` metadata
3. inactive offer is omitted
4. suspended member is omitted
5. disabled backend is omitted
6. disabled/draining assignment is omitted from active desired render
7. merge precedence preserves offer-owned price and ID
8. sort order is deterministic
9. identical effective input yields byte-identical YAML

### 16.14 Exit gate for Slice C

Do not proceed to large admin/member API additions until all of the following
are true:

1. broker desired state is rendered exclusively from persisted control-plane
   entities plus bootstrap settings
2. `GET /admin/v1/broker-config` can be backed by desired runtime state
3. no active publication path depends on `members[].backends[].offerings[]`
   being the live source of truth
4. deterministic revisioning exists for desired broker state
5. render tests cover inactive/suspended/unassigned omission behavior

## 17. Detailed checklist for Slice D — admin API split and control-plane CRUD

Slice D turns the new persisted model into a usable operator surface. The main
 goal is to stop concentrating Pool control-plane logic in one monolithic
`main.go` file and expose first-class APIs for offers, members, assignments,
and broker runtime state.

### 17.1 Split HTTP handlers out of `main.go`

**Current file:**

- `pool-controller/cmd/livepeer-pool-controller/main.go`

**Current problem:**

- admin routes, public routes, accounting routes, backend scoring overrides,
  and config/reload behaviors are all registered inline in one file

**Change:**

- introduce an internal server package for control-plane admin routes

**Files to add:**

- `pool-controller/internal/server/admin/router.go`
- `pool-controller/internal/server/admin/offers.go`
- `pool-controller/internal/server/admin/join_requests.go`
- `pool-controller/internal/server/admin/members.go`
- `pool-controller/internal/server/admin/member_backends.go`
- `pool-controller/internal/server/admin/assignments.go`
- `pool-controller/internal/server/admin/broker_runtime.go`
- `pool-controller/internal/server/admin/responses.go`
- `pool-controller/internal/server/admin/router_test.go`

**Rule:**

- existing accounting endpoints may stay separate in the first pass
- new control-plane CRUD should not be added inline to `main.go`

### 17.2 Introduce service-layer boundaries before handlers

Do not let handlers talk directly to repos for multi-entity logic.

**Files to add:**

- `pool-controller/internal/service/offers/service.go`
- `pool-controller/internal/service/members/service.go`
- `pool-controller/internal/service/assignments/service.go`
- `pool-controller/internal/service/brokerruntime/service.go`

**Handler rule:**

- simple list/get may delegate to a single repo
- create/update/status transitions should go through service layer
- anything touching more than one entity must go through service layer

### 17.3 Offers admin API

**Files:**

- `pool-controller/internal/server/admin/offers.go`
- `pool-controller/internal/service/offers/service.go`

**Endpoints:**

- `GET /admin/v1/offers`
- `POST /admin/v1/offers`
- `GET /admin/v1/offers/:id`
- `PATCH /admin/v1/offers/:id`
- `POST /admin/v1/offers/:id/disable`
- optional later: `POST /admin/v1/offers/:id/enable`

**Create request body:**

```json
{
  "id": "rerank-zerank2",
  "capability_id": "rerank",
  "offering_id": "zerank-2-default",
  "interaction_mode": "http-reqresp@v0",
  "work_unit": {
    "name": "requests",
    "extractor": {
      "type": "request-formula",
      "expression": "1",
      "fields": {},
      "default": 1
    }
  },
  "price": {
    "amount_wei": "372000000000",
    "per_units": 1
  },
  "extra": {
    "provider": "rerank-runner",
    "model": "zeroentropy/zerank-2"
  },
  "constraints": {
    "gpu_vendor": "NVIDIA"
  }
}
```

**Validation rules:**

- `id` unique
- public tuple shape valid
- `price.per_units > 0`
- no duplicate active public tuple unless deliberately supported by plan later

**Acceptance criteria:**

- operator can define canonical offers without file edits
- changes persist and survive restart
- offer changes affect desired broker state only through Slice C render path

### 17.4 Members and member-backends admin API

**Files:**

- `pool-controller/internal/server/admin/members.go`
- `pool-controller/internal/server/admin/member_backends.go`
- `pool-controller/internal/service/members/service.go`

**Endpoints:**

- `GET /admin/v1/members`
- `GET /admin/v1/members/:id`
- `PATCH /admin/v1/members/:id/status`
- `GET /admin/v1/member-backends`
- `GET /admin/v1/member-backends/:id`
- `PATCH /admin/v1/member-backends/:id/status`
- `POST /admin/v1/member-backends/:id/verify`

**Status values:**

- member: `active`, `suspended`
- backend: `active`, `draining`, `disabled`

**Purpose in first slice:**

- operator can inspect and manage approved members before self-service join is
  complete
- backend verification can be retriggered manually

**Acceptance criteria:**

- operator can suspend a member and watch assigned broker inventory disappear
  via rerender
- operator can disable or drain a backend independently of member status

### 17.5 Assignments admin API

**Files:**

- `pool-controller/internal/server/admin/assignments.go`
- `pool-controller/internal/service/assignments/service.go`

**Endpoints:**

- `GET /admin/v1/assignments`
- `POST /admin/v1/assignments`
- `GET /admin/v1/assignments/:id`
- `PATCH /admin/v1/assignments/:id/status`
- `DELETE /admin/v1/assignments/:id`

**Create request body:**

```json
{
  "id": "assign-member-a-rerank-to-rerank-zerank2",
  "offer_id": "rerank-zerank2",
  "member_backend_id": "member-a-rerank",
  "notes": "first production assignment"
}
```

**Rules:**

- referenced offer must exist and be active
- referenced backend must exist and be active
- backend's parent member must be active
- compatibility validation must pass

**Acceptance criteria:**

- operator can create/remove assignments through API only
- unassigned approved backends remain unpublished
- assignment changes produce predictable desired broker state changes

### 17.6 Broker runtime admin API

**Files:**

- `pool-controller/internal/server/admin/broker_runtime.go`
- `pool-controller/internal/service/brokerruntime/service.go`

**Endpoints:**

- `GET /admin/v1/broker-config`
- `GET /admin/v1/broker-runtime`
- `GET /admin/v1/broker-runtime/diff`
- reserve later:
  - `POST /admin/v1/broker-runtime/apply`

**Response goals:**

- current desired revision
- rendered YAML or structured model
- counts for offers/members/backends/assignments used in render
- applied revision if known
- diff/dirty status if applied-state tracking exists

**Acceptance criteria:**

- operator can inspect desired broker runtime state without reading local files
- API response makes the render lineage understandable

### 17.7 Keep existing accounting API separate during first pass

Current accounting routes in `main.go` are already substantial:

- receipts
- rounds
- payouts
- backend outcomes
- synthetic probes

**Decision:**

- do not block Slice D on a full accounting-route refactor
- only move control-plane CRUD into new handler packages first
- leave accounting route modularization to a follow-up unless it becomes a
  blocker

### 17.8 Shared response / error format

**File:** `pool-controller/internal/server/admin/responses.go`

Add shared helpers for:

- JSON success response writing
- validation errors
- not found
- conflict
- internal errors

Suggested error shape:

```json
{
  "error": {
    "code": "validation_error",
    "message": "offer_id is required"
  }
}
```

This prevents every new control-plane handler from inventing a slightly
different response contract.

### 17.9 Admin API tests

**Files to add:**

- `pool-controller/internal/server/admin/offers_test.go`
- `pool-controller/internal/server/admin/members_test.go`
- `pool-controller/internal/server/admin/assignments_test.go`
- `pool-controller/internal/server/admin/broker_runtime_test.go`

**Required cases:**

- admin auth enforced
- create/list/get/update status flows
- validation errors
- not-found behavior
- assignment create causes desired render change
- member suspend causes broker omission in desired state

### 17.10 Exit gate for Slice D

Do not proceed to member self-service onboarding until all of the following are
true:

1. operator can manage offers, members, and assignments without file edits
2. assignment-driven broker desired state is inspectable through admin API
3. new control-plane CRUD is not implemented as more inline `main.go` sprawl
4. tests cover the key lifecycle transitions

## 18. Detailed checklist for Slice E — member onboarding API

Slice E introduces the member-facing flow that removes the operator from the
"please edit my member record" loop. Approval remains operator-controlled.

### 18.1 Add a dedicated member HTTP surface

**Files to add:**

- `pool-controller/internal/server/member/router.go`
- `pool-controller/internal/server/member/join_requests.go`
- `pool-controller/internal/server/member/responses.go`
- `pool-controller/internal/server/member/router_test.go`

**Decision:**

- keep member routes separate from admin routes in code and path space
- reuse process and auth plumbing where practical

### 18.2 Add join-request services

**Files to add:**

- `pool-controller/internal/service/joinrequests/service.go`
- `pool-controller/internal/service/joinrequests/service_test.go`
- `pool-controller/internal/service/backendverify/service.go`
- `pool-controller/internal/service/backendverify/service_test.go`

**Responsibilities:**

- accept submitted join request
- validate request shape
- persist pending status
- trigger backend verification/metadata discovery
- update join-request-visible verification data

### 18.3 Define the initial join-request request contract

**Endpoint:**

- `POST /member/v1/join-requests`

**Suggested request body:**

```json
{
  "member_eth_address": "0xMEMBER_A_ETH_ADDRESS",
  "display_name": "member-a",
  "payout_mode": "onchain",
  "requested_backends": [
    {
      "id": "member-a-rerank",
      "transport": "http",
      "url": "http://member-a-rerank-runner:8080/v1/rerank",
      "auth": {
        "method": "none"
      },
      "health_probe": {
        "type": "http-status",
        "interval_ms": 5000,
        "timeout_ms": 1500,
        "unhealthy_after": 2,
        "healthy_after": 1,
        "config": {
          "url": "http://member-a-rerank-runner:8080/healthz"
        }
      },
      "claimed_capabilities": [
        {
          "capability_id": "rerank",
          "interaction_mode": "http-reqresp@v0",
          "extra": {
            "provider": "rerank-runner",
            "model": "zeroentropy/zerank-2"
          }
        }
      ]
    }
  ]
}
```

**First-slice validation:**

- member address required
- at least one requested backend
- backend URL/transport required
- reject malformed probe/auth structures

Wallet-signature-based proof can land in the same slice if ready, but the
minimum requirement here is the persisted onboarding workflow boundary.

### 18.4 Join-request lifecycle

The minimum lifecycle:

- `pending`
- `approved`
- `rejected`
- `withdrawn`

**Important behavior:**

- `pending` join requests never affect broker render
- `approved` join requests become members + member backends only through
  explicit operator action
- `rejected` requests remain visible for audit/history

### 18.5 Join-request status/read APIs

**Endpoints:**

- `GET /member/v1/join-requests/:id`
- `POST /member/v1/join-requests/:id/refresh`

**`GET` response goals:**

- request status
- submitted backend list
- latest verification/discovery results
- review reason if rejected

**`refresh` purpose:**

- retrigger backend verification/discovery without resubmitting a new request

### 18.6 Operator approval/rejection flow on top of join requests

Slice D introduced admin routes for join-request review; Slice E makes them
live against real member-submitted requests.

**Approval path:**

- read pending join request
- create `Member`
- create `MemberBackend` rows
- mark join request `approved`
- append audit event

**Rejection path:**

- mark join request `rejected`
- persist review reason
- append audit event

**Rule:**

- approval does not create assignments automatically

### 18.7 Backend verification/discovery first pass

**Service:** `internal/service/backendverify/`

First-pass capabilities:

- reachability check
- health probe execution
- optional metadata fetch where workload family already has stable discovery
  behavior

For the first slice, verification output should be informative, not a hidden
publication side effect.

Suggested stored result:

- `verification_status`
- `verification_error`
- `last_verified_at`
- discovered capability metadata blob if available

### 18.8 Member auth and abuse boundary

This plan does not require full production auth design in the same patch, but
the API should be built so auth can be layered cleanly.

Minimum first-pass rule:

- member routes must have an explicit auth stub or middleware boundary
- do not bury future wallet-signature verification assumptions inside handler
  business logic

### 18.9 Join-request tests

**Files to add:**

- `pool-controller/internal/server/member/join_requests_test.go`
- `pool-controller/internal/service/joinrequests/service_test.go`
- `pool-controller/internal/service/backendverify/service_test.go`

**Required cases:**

- submit valid request
- reject malformed request
- request stays pending after submit
- refresh updates verification result
- approval creates member/backend records
- rejection preserves request history and reason
- approved request still does not publish without assignment

### 18.10 Exit gate for Slice E

Do not proceed to UI-heavy onboarding work until all of the following are true:

1. a member can request onboarding without operator file edits
2. operator can approve or reject that request through admin API
3. approval creates member/backend state but not automatic publication
4. verification results are visible through API
5. tests prove that pending or approved-but-unassigned requests never affect
   broker desired state

## 19. Detailed checklist for Slice F — compatibility and assignment policy

Slice F encodes the business rule that the orch owns the public offer and the
member only proves that it can serve it. This slice prevents assignment from
becoming a shallow foreign-key join.

### 19.1 Add a dedicated compatibility service

**Files to add:**

- `pool-controller/internal/service/compat/service.go`
- `pool-controller/internal/service/compat/service_test.go`

**Responsibility:**

- determine whether a member backend may be assigned to a given orch offer
- return structured reasons when compatibility fails

Suggested interface:

```go
type Result struct {
    Compatible bool
    Reasons    []string
}

func Check(offer types.Offer, backend types.MemberBackend) Result
```

### 19.2 Validate structural compatibility first

Compatibility must at minimum check:

- transport compatibility
- required interaction mode compatibility
- health probe presence/shape where the offer policy requires it
- required backend metadata fields where the workload family depends on them

These checks are generic and should be the first layer.

### 19.3 Add workload-family-specific compatibility checks

The first implementation does not need a universal plugin system, but it does
need family-specific checks for shipped families.

Suggested first families:

- `rerank`
- `openai:chat-completions`
- `openai:embeddings`

Suggested file split:

- `pool-controller/internal/service/compat/rerank.go`
- `pool-controller/internal/service/compat/openai.go`

Examples:

- `rerank` should require a backend claim that is consistent with
  `http-reqresp@v0`
- chat should require a backend claim consistent with the offered interaction
  mode and model metadata expectations

### 19.4 Make assignment creation depend on compatibility success

**Files:**

- `pool-controller/internal/service/assignments/service.go`
- `pool-controller/internal/server/admin/assignments.go`

**Rule:**

- assignment create/update must call compat service before persisting the
  active assignment
- failures should return structured validation/conflict errors

This is where "member capability possession is not publication authority"
becomes a hard API rule.

### 19.5 Preserve orch-owned public identity and price

Compatibility logic must never let backend claims redefine:

- `capability_id`
- `offering_id`
- `interaction_mode`
- `price`

Those always come from `Offer`.

Backend claims can only:

- satisfy or fail compatibility
- enrich verification/discovery context
- provide serving endpoint and health details

### 19.6 Add offer policy hooks without overbuilding

The service should have room for future policy checks such as:

- allowed regions
- allowed GPU/vendor class
- operator-required verification status
- per-offer approval gates

But do not build a full policy DSL in the first slice. A small internal rule
set keyed off `constraints` and known workload families is sufficient.

### 19.7 Compatibility test matrix

**Files to add:**

- `pool-controller/internal/service/compat/rerank_test.go`
- `pool-controller/internal/service/compat/openai_test.go`

Required cases:

- valid rerank assignment passes
- rerank backend with incompatible interaction mode fails
- chat backend missing expected model metadata fails or warns per chosen rule
- offer-owned price remains unchanged regardless of backend claim content
- assignment service rejects incompatible offer/backend pairs

### 19.8 Exit gate for Slice F

Do not proceed to UI-led assignment flows until all of the following are true:

1. assignments are validated by a dedicated compatibility layer
2. invalid assignments fail before render
3. orch-owned offer identity and price cannot be overridden by member backend
   claims
4. compatibility tests cover at least rerank and one OpenAI family

## 20. Detailed checklist for Slice G — operator UI

Slice G turns the admin/member APIs into the intended operator workflow. This
slice should follow the backend/API work, not lead it.

### 20.1 Define the first operator workflow explicitly

The minimum UI workflow must support:

1. create orch offer
2. review join request
3. approve member
4. inspect backend verification result
5. create assignment
6. inspect desired broker state

If the UI cannot complete that flow without file edits or raw API calls, the
slice is incomplete.

### 20.2 Add an admin frontend surface for control-plane entities

The exact frontend technology can follow the component's chosen pattern, but
the UI should be organized by the new entity model, not by old config views.

Minimum views:

- offers
- join requests
- members
- member backends
- assignments
- broker runtime

### 20.3 Offers screen requirements

Capabilities:

- list offers
- create offer
- edit offer
- disable offer
- inspect price / mode / constraints / metadata

Important UI rule:

- offers are shown as orch-owned catalog entries, not as properties of members

### 20.4 Join requests screen requirements

Capabilities:

- list pending / reviewed join requests
- inspect requested backends
- inspect verification/discovery results
- approve / reject with reason

Important UI rule:

- approval must clearly say it does not publish the member yet

### 20.5 Members and backends screen requirements

Capabilities:

- list approved/suspended members
- inspect member backends
- show verification status
- suspend member
- disable or drain backend
- manually retrigger backend verify

### 20.6 Assignments screen requirements

Capabilities:

- create assignment by selecting offer + approved backend
- list active/draining/disabled assignments
- remove or disable assignment
- show unassigned approved backends

Important UI rule:

- the operator should be able to see "approved but not published yet" state

### 20.7 Broker runtime screen requirements

Capabilities:

- show desired broker revision
- show desired config or structured capability summary
- show counts used in render
- show applied revision if available
- show whether desired != applied

This screen is the operator proof that control-plane actions have translated
into runtime state.

### 20.8 UI composition and data dependencies

The UI should consume the new admin APIs directly:

- `/admin/v1/offers`
- `/admin/v1/join-requests`
- `/admin/v1/members`
- `/admin/v1/member-backends`
- `/admin/v1/assignments`
- `/admin/v1/broker-config`
- `/admin/v1/broker-runtime`

Do not build hidden UI-side state derivations that bypass the backend entity
model.

### 20.9 UI acceptance tests / smoke path

Required end-to-end smoke path:

1. operator creates a rerank offer
2. member submits join request
3. operator approves request
4. operator creates assignment
5. operator sees desired broker state update
6. operator disables assignment and sees broker state contract

### 20.10 Exit gate for Slice G

Slice G is complete when:

1. the operator can complete the full onboarding and assignment flow from UI
2. the UI surfaces approved-but-unassigned state clearly
3. broker runtime state is visible from the same UI surface
4. no hidden file edit is required at any step

## 21. Detailed checklist for Slice H — rollout/apply reconciliation

Slice H closes the loop between desired control-plane state and actual broker
runtime convergence. Without it, the controller can be correct in storage but
opaque in operations.

### 21.1 Introduce applied-state tracking

**Files to add:**

- `pool-controller/internal/types/broker_runtime.go`
- `pool-controller/internal/repo/broker_runtime_apply.go`
- `pool-controller/internal/repo/broker_runtime_apply_test.go`

Suggested applied-state record:

- `desired_revision`
- `applied_revision`
- `last_apply_started_at`
- `last_apply_finished_at`
- `last_apply_status`
- `last_apply_error`

### 21.2 Add a broker runtime service

**Files:**

- `pool-controller/internal/service/brokerruntime/service.go`
- `pool-controller/internal/service/brokerruntime/service_test.go`

Responsibilities:

- read desired runtime state
- read applied runtime state
- compute dirty / converged status
- orchestrate apply attempt bookkeeping

### 21.3 Define the first apply contract

The first slice does not need a fully automated broker reload path, but it
does need an explicit apply boundary.

Allowed first implementations:

1. manual-ack apply:
   - operator applies broker config externally
   - controller records applied revision explicitly

2. local broker reload integration:
   - controller triggers known broker reload path
   - controller records result

The plan does not force one immediately, but one must be chosen explicitly.

### 21.4 Add broker runtime apply endpoints

**Files:**

- `pool-controller/internal/server/admin/broker_runtime.go`

Endpoints:

- `GET /admin/v1/broker-runtime`
- `GET /admin/v1/broker-runtime/diff`
- `POST /admin/v1/broker-runtime/apply`
- optional first-slice fallback:
  - `POST /admin/v1/broker-runtime/mark-applied`

### 21.5 Surface desired-vs-applied state clearly

The admin API and UI must expose:

- desired revision
- applied revision
- whether they differ
- last apply result
- last error if any

This must be visible without reading logs directly.

### 21.6 Audit apply actions

Every apply or mark-applied action should record:

- actor
- desired revision
- resulting applied revision
- success/failure
- error text if failed

This belongs in the same audit lineage as approvals and assignments.

### 21.7 Rollout/apply tests

Required cases:

- desired revision differs from applied revision => dirty state
- successful apply marks converged state
- failed apply preserves desired revision and records error
- assignment or offer change produces a new desired revision and dirty state

### 21.8 Exit gate for Slice H

The slice is complete when:

1. operator can tell whether broker runtime has converged to desired state
2. apply attempts are auditable
3. drift between desired and applied state is visible in API and UI
4. broker runtime failures do not silently disappear into logs only

## 22. Remaining milestone map after Slice H

The implementation now covers the core control-plane architecture:

- orch-owned offers
- join requests
- approval and rejection
- assignment policy
- broker desired-state rendering from persisted entities
- desired vs applied runtime state
- admin API and operator UI

What remains is no longer architectural correction. It is production completion.

### 22.1 Milestone M1 — broker-confirmed apply contract

Current state:

- the controller can drive apply attempts
- the controller can optionally run a configured broker-apply command
- the controller can re-render desired state and fail if desired revision drifts

What is still missing:

- a canonical broker-side acknowledgement that a specific revision is actually
  loaded and serving
- a way to distinguish:
  - command succeeded but broker did not load new config
  - command succeeded and broker loaded the target revision
- a durable apply-attempt history beyond the latest state

Required work:

- define a broker-facing apply acknowledgement contract
- record broker-confirmed revision separately from controller-local apply start
  and finish bookkeeping
- decide whether acknowledgement is:
  - synchronous command output
  - broker HTTP callback / pollable admin surface
  - file/socket/systemd-level local contract
- preserve current manual and command-driven fallback paths for debugging

Acceptance bar:

1. the operator can tell which revision the broker itself claims is active
2. controller-side "apply succeeded" is not treated as equivalent to
   broker-confirmed convergence
3. failed or missing broker acknowledgement is surfaced in API, UI, and audit
   history

#### 22.1.1 Preferred first implementation

The preferred first implementation is:

1. `pool-controller` renders desired broker config
2. the configured apply command places that config where the broker expects it
3. the broker exposes a local/private admin reload path
4. the broker exposes a local/private runtime-status path that reports the
   loaded revision
5. `pool-controller` treats apply as successful only when the broker-reported
   loaded revision matches the desired revision

This is better than treating command exit as truth because it separates:

- config placement
- broker reload attempt
- broker-confirmed active revision

#### 22.1.2 Proposed broker contract

The broker should grow a private admin surface with at least:

- `POST /admin/v1/runtime/reload`
- `GET /admin/v1/runtime`

Suggested `GET /admin/v1/runtime` response:

```json
{
  "loaded_revision": "sha256-or-other-stable-revision",
  "last_reload_attempt_id": "reload-1716035696000000000",
  "loaded_config_path": "/etc/livepeer/host-config.yaml",
  "loaded_at": "2026-05-18T12:34:56Z",
  "last_reload_started_at": "2026-05-18T12:34:54Z",
  "last_reload_finished_at": "2026-05-18T12:34:56Z",
  "last_reload_status": "applied",
  "last_reload_error": ""
}
```

Suggested `POST /admin/v1/runtime/reload` behavior:

- re-read the configured `host-config.yaml`
- validate fully before swapping runtime state
- create a fresh reload `attempt_id`
- compute the loaded revision from the exact loaded config bytes
- swap atomically on success
- preserve the previous runtime if reload fails
- update reload status/error fields regardless of success

#### 22.1.3 Revision definition

The revision used by `pool-controller` and the broker must be the same value
for the same config bytes.

Preferred first rule:

- `revision = sha256(rendered broker YAML bytes)`

This matches the controller’s current desired-state revision model and avoids
introducing a second revision source.

#### 22.1.4 Controller apply flow after broker acknowledgement exists

Target controller flow:

1. load current desired runtime revision
2. run configured broker-apply command to stage config
3. trigger broker reload
4. poll or fetch broker runtime status
5. mark applied only if:
   - broker `last_reload_attempt_id` matches the triggered attempt
   - broker reload status is `applied`
   - broker `loaded_revision == desired_revision`
6. otherwise record failure with broker error/status context

At that point, `applied_revision` in `pool-controller` should mean:

- broker-confirmed loaded revision

not merely:

- controller-side apply attempt completed

Manual runtime endpoints may remain in the admin API for debugging and
break-glass workflows, but they are not the normal production operator path
once broker admin reload is configured. The normal path is:

1. `POST /admin/v1/broker-runtime/apply`
2. broker reload attempt
3. broker-confirmed attempt/status/revision correlation

#### 22.1.5 Broker-side implementation notes

At planning time, the broker still lacked a private reload/status surface, so
`M1` required explicit broker work, not just controller work.

That broker work should include:

- loaded runtime revision tracking
- last reload status/error tracking
- local/private admin routes only
- tests for:
  - valid reload swaps runtime
  - invalid reload preserves previous runtime
  - runtime endpoint reports the active loaded revision

#### 22.1.6 Suggested implementation sequence

1. add broker runtime-status struct and loaded revision computation
2. add broker private admin reload endpoint
3. add broker runtime-status endpoint
4. update controller apply command contract to call reload and check runtime
5. update controller API/UI wording so `applied` means broker-confirmed

### 22.2 Milestone M2 — operator UX hardening

Current state:

- the embedded admin UI is usable for the full onboarding and assignment flow
- runtime status, audits, review, and assignment candidates are visible

What is still missing:

- stronger structured editing flows for more mutations
- clearer page organization as the surface grows
- better error, loading, and empty states
- higher-signal drilldown between:
  - offers
  - members
  - backends
  - assignments
  - runtime
  - audit history

Required work:

- decide whether the embedded page remains acceptable or should be replaced by
  a dedicated frontend build path
- improve operator guidance around:
  - verified but unassigned backends
  - failed applies
  - stalled review queues
  - assignment conflicts
- add richer audit and runtime summaries without forcing operators into raw JSON

Acceptance bar:

1. a pool operator can complete common flows without raw JSON editing
2. failed or blocked flows have clear next-action guidance in the UI
3. the operator surface remains understandable as pool size grows

### 22.3 Milestone M3 — production rollout and runbook completion

Current state:

- code paths exist for the new control-plane model
- the component README covers the broker apply command contract
- `pool-controller/RUNBOOK.md` now covers the primary production operator path
  for:
  - offers / join requests / assignments
  - broker runtime apply
  - broker-confirmed convergence checks
  - failure triage
- `infra/scenarios/pool-orchestrator/README.md` now explicitly points back to
  the control-plane runbook for the production workflow
- `docs/design-docs/pool-orchestrator-production-rollout.md` now binds the
  cross-cutting production sequence across:
  - `pool-controller`
  - `capability-broker`
  - `orch-coordinator`
  - secure-orch
- broker-apply deployment patterns are now documented in:
  - `pool-controller/RUNBOOK.md`
  - `capability-broker/docs/operator-runbook.md`
  - `docs/design-docs/pool-orchestrator-production-rollout.md`
- explicit recovery playbooks now exist for:
  - failed broker apply
  - desired revision drift during apply
  - rejected join requests / verification failures
  - approved but unassigned backends
  - suspended members / disabled backends
  - secure-orch publish blockage
  - stalled round close
  - stale submitted payouts
  - failed payout accumulation
  - stuck or near-expiry leases

What is still missing:

- failure-recovery playbooks for:
  - coordinator publish rejection specifics if they become a recurring operator
    issue

Required work:

- update operator docs in `pool-controller/` and cross-cutting rollout docs in
  root `docs/`
- define the production shape for:
  - controller host placement
  - broker reload mechanism
  - secrets injection
  - backup / restore of Bolt-backed control-plane state
- add production smoke-check steps for onboarding, assignment, and apply

Acceptance bar:

1. a new operator can deploy and recover the control plane from checked-in docs
2. the broker apply command contract is documented as an operational procedure,
   not just a code feature
3. rollback and recovery steps are explicit

### 22.4 Milestone M4 — legacy bridge removal

Current state:

- `generate-broker-config` has been removed
- `import-legacy-config` has been removed
- startup auto-import and legacy resync paths have been removed
- runtime selection/probe/summary paths now derive from persisted state rather
  than legacy nested config
- examples now reflect the bootstrap-only controller config shape
- one residual bridge remains in code:
  - `Config.Members` and the legacy nested member/backend/offering types still
    exist as parse/types surfaces, even though they are no longer part of the
    supported runtime workflow

#### 22.4.1 Remaining concrete legacy bridges

The remaining bridges are no longer conceptual. They are specific codepaths
that still treat the legacy config shape as a live runtime input.

1. Legacy config schema is still part of the main runtime config contract.

Files:

- `pool-controller/internal/config/config.go`
- `pool-controller/internal/config/load.go`

Current issue:

- `Config` still includes `Members []Member`
- validation/defaulting still walks the nested legacy member/backend/offering
  hierarchy

Required end state:

- bootstrap config keeps only bootstrap/runtime settings
- legacy member/offering import moves behind an explicit migration-only type and
  path
- the main runtime config no longer advertises `members:` as a supported
  control-plane input

2. Startup auto-import and legacy resync path has been removed.

Status:

- complete
- `serve` now starts from persisted control-plane state only
- snapshots no longer imply legacy config resync

3. CLI compatibility commands have been removed.

Status:

- complete
- `generate-broker-config` is gone
- `import-legacy-config` is gone
- `pool-controller` operator flow is now API/UI plus persisted state only

4. Selection/probe state has been moved off legacy config members.

Status:

- complete
- backend selection sync and synthetic probes now use persisted offers,
  members, backends, and assignments

5. Fallback read surfaces have been moved off legacy config-derived counts.

Status:

- complete
- persisted state is now the source for member/backend/offering summaries

6. Examples and tests were still normalizing the migration-era shape.

Files:

- `pool-controller/examples/pool-controller-config.example.yaml`
- `pool-controller/examples/pool-controller-config.compose.yaml`
- config/configgen/legacyimport-related tests

Status:

- examples are now bootstrap-only
- config/controller tests have been rewritten around the supported runtime path
- the remaining follow-up here is mostly deleting no-longer-needed legacy-only
  helper coverage rather than changing operator behavior

#### 22.4.2 Recommended removal sequence

M4 should not be one giant deletion. The safer order is:

1. Move selection/probe/runtime read paths off `cfg.Members`
2. Remove startup auto-import and legacy resync from `serve` / `reload`
3. Reduce the main `Config` contract further if the residual legacy parse field
   is no longer needed internally
4. Delete any no-longer-needed legacy-only helper code/tests

That order matters because it removes live runtime dependence first, then
removes operator-facing compatibility surfaces second.

#### 22.4.3 Acceptance bar for M4

M4 is complete only when all of the following are true:

1. `pool-controller serve` does not require, auto-import, or resync
   `members[].backends[].offerings[]`
2. runtime selection, probing, and summary views are derived from persisted
   control-plane state
3. the supported runtime config and operator workflow are bootstrap-only plus
   persisted control-plane state
4. no legacy import/generation command remains in the controller surface
5. examples and docs no longer teach legacy member YAML as the normal operator
   workflow

What is still missing:

- execution of the concrete removal sequence above
- a final decision on whether any explicit migration-only CLI remains supported
  after M4 lands

Required work:

- remove live runtime dependence on legacy nested config
- collapse the main config contract to bootstrap-only fields
- either remove or sharply quarantine migration-only commands/examples
- keep only bootstrap-only config plus persisted control-plane state as the
  supported production path

Acceptance bar:

1. the supported runtime model is unambiguous
2. migration scaffolding is either explicitly supported or explicitly removed
3. the codebase no longer blurs live control-plane state with legacy config

### 22.5 Recommended order after current implementation

The recommended order from here is:

1. M1 broker-confirmed apply contract
2. M3 production rollout and runbook completion
3. M2 operator UX hardening
4. M4 legacy bridge removal

Reasoning:

- broker-confirmed convergence is the biggest remaining correctness gap
- production rollout docs are the next operational bottleneck once apply is
  credible
- UI refinement matters, but it should not outrun the true runtime contract
- legacy bridge removal should happen only after the production path is stable

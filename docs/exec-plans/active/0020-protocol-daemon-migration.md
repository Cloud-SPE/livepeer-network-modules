# Plan 0020 — protocol-daemon migration into the rewrite monorepo

**Status:** active  
**Opened:** 2026-05-07  
**Owner:** harness  
**Related:** plan 0018 (`orch-coordinator`), plan 0019 (`secure-orch-console`), plan 0016 (chain-integrated payment), sibling source repo `livepeer-modules-project/protocol-daemon/`

## 1. Why this plan exists

The rewrite monorepo now has a working `orch-coordinator` and `secure-orch-console`
flow for building, signing, and publishing the off-chain registry manifest. What it
does **not** yet have is the chain-side operator daemon that the prior system used for:

- round initialization
- reward calls
- on-chain `ServiceRegistry` / `AIServiceRegistry` `setServiceURI`
- local operator-facing status / force-action RPCs over a unix socket

The old implementation already exists in the sibling repo:

- `/home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/protocol-daemon`

Per direct user instruction in this thread, we are allowed to copy and adapt that code
into this monorepo. The user also explicitly wants the port to include:

- the full `protocol-daemon` logic
- the service-registry / AI-service-registry logic
- whatever `chain-commons` code is needed to make it self-contained here

This plan is the migration shape for doing that without dragging in old repository
structure blindly.

## 2. Scope

### In scope

- Create a new top-level `protocol-daemon/` component in this monorepo.
- Port the existing daemon’s runtime, service, provider, repo, tests, docs, and image.
- Port the required `chain-commons` packages into this monorepo so the daemon is
  self-contained here.
- Preserve the current three modes:
  - `round-init`
  - `reward`
  - `both`
- Preserve the current local unix-socket RPC surface for operator tooling.
- Preserve chain-side service-URI writes, but adapt them to the rewrite’s publication
  model where `orch-coordinator` serves the manifest URL.
- Wire the component into the monorepo’s docs, compose examples, and image build flow.

### Not in scope

- Rewriting the daemon from scratch.
- Changing the chain-side business logic for round init or rewards.
- Changing `orch-coordinator` to perform chain writes directly.
- Hardware-backed signing or any secure-orch changes beyond documenting integration.

## 3. Current-source inventory

The source daemon already contains the major slices we need:

- `cmd/livepeer-protocol-daemon/` — binary entrypoint and mode wiring
- `internal/config/` — validated config
- `internal/runtime/grpc/` — unix-socket RPC handler set and adapters
- `internal/runtime/metrics/` — Prometheus listener
- `internal/runtime/lifecycle/` — process lifecycle and signal handling
- `internal/service/roundinit/` — round initialization logic
- `internal/service/reward/` — reward logic
- `internal/service/serviceregistry/` — `setServiceURI`
- `internal/service/aiserviceregistry/` — `setAIServiceURI`
- `internal/service/orchstatus/` — registration / balance / status reads
- `internal/service/preflight/` — startup validation
- `internal/repo/poolhints/` — Bolt-backed reward hint cache
- `internal/providers/*` — chain bindings and wrappers

The important structural dependency is `chain-commons`, which the old daemon uses
heavily for:

- tx intent management
- multi-RPC failover
- controller address resolution
- reorg-aware receipt tracking
- V3 keystore handling
- round clock / time source
- Bolt-backed stores

That means the migration is not “copy one folder and go”. It is “copy one component
plus the `chain-commons` slices it actually depends on.”

## 4. Target shape in this monorepo

Add a new top-level component:

```text
protocol-daemon/
  AGENTS.md
  README.md
  DESIGN.md
  Dockerfile
  Makefile
  compose/
    docker-compose.yml
  cmd/
    livepeer-protocol-daemon/
  internal/
    config/
    providers/
    repo/
    runtime/
    service/
    types/
  docs/
    operator-runbook.md
```

The component should match the same monorepo conventions as `payment-daemon/`,
`orch-coordinator/`, and `secure-orch-console/`:

- distroless runtime image
- top-level `AGENTS.md`
- component-local `docs/`
- run-only compose
- explicit operator runbook
- tests runnable via `go test ./...`

## 5. Rewrite-specific adaptations

The port should not be byte-for-byte identical. These are the required adaptations:

### 5.1 Service-URI ownership changes

In the old stack, the publisher daemon wrote a manifest file and `protocol-daemon`
updated the on-chain `serviceURI` pointer to that hosted file.

In the rewrite:

- `orch-coordinator` is the public host of `/.well-known/livepeer-registry.json`
- `secure-orch-console` only signs
- `protocol-daemon` should point the chain-side `serviceURI` at the coordinator’s
  public well-known URL, not at any old publisher output path

This is the main architectural adaptation, and it is the reason the daemon still
belongs in the rewrite even though manifest hosting moved.

### 5.2 Public/off-chain integration docs

The new operator flow must document:

1. publish manifest through `orch-coordinator`
2. verify public URL content
3. use `protocol-daemon` to set the on-chain URI to that public URL

That sequence is different from the old stack and must be explicit in the new docs.

### 5.3 RPC surface

Keep the full current RPC surface in the first port, including:

- `Health`
- `GetRoundStatus`
- `GetRewardStatus`
- `ForceInitializeRound`
- `ForceRewardCall`
- `SetServiceURI`
- `GetOnChainServiceURI`
- `IsRegistered`
- `GetWalletBalance`
- `GetTxIntent`
- `StreamRoundEvents`
- `SetAIServiceURI`
- `GetOnChainAIServiceURI`
- AI registration/status helpers already present in the old daemon

The rewrite adaptation is about what URL gets written on chain, not about reducing the
daemon’s operator surface during the port.

## 6. Dependency strategy

The user asked for a self-contained port that brings in the logic the daemon needs,
including the required pieces from `chain-commons`. So this plan uses a single
dependency strategy:

- port `protocol-daemon/` in full
- port the `chain-commons` packages it imports
- port any required proto/contracts support packages those imports depend on
- retarget imports so the daemon builds wholly inside this monorepo

This is a larger move than a thin external-dependency port, but it matches the
deployment requirement: the rewrite monorepo should own the full chain-side operator
daemon story rather than depending on sibling repos at runtime or release time.

## 7. Migration phases

### Phase 1 — scaffold + direct port

- Create `protocol-daemon/` component skeleton in this monorepo.
- Copy source files from the sibling repo into the new component.
- Preserve tests, Dockerfile shape, compose example, and operator docs.
- Update module path/imports to the rewrite monorepo namespace.
- Record copied-source attribution in `protocol-daemon/AGENTS.md`.
- Copy the daemon’s directly required support packages from `chain-commons` and any
  required contract/proto support packages into monorepo-owned locations.

Exit criteria:

- `go test ./...` passes in `protocol-daemon/`
- component image builds
- unix-socket server starts in `--dev`
- no import path in `protocol-daemon/` points back to sibling repos

### Phase 2 — rewrite integration pass

- Update docs and examples to point `SetServiceURI` at coordinator-hosted manifest URLs.
- Add a runbook section for:
  - `orch-coordinator` public URL
  - secure-orch sign/publish cycle
  - `protocol-daemon` service-URI update
- Verify compose examples fit the rewrite monorepo image naming and volume conventions.

Exit criteria:

- operator docs describe the full chain + off-chain flow coherently
- no remaining references that imply the old publisher daemon hosts the manifest

### Phase 3 — integration tests against the rewrite

- Add focused tests for:
  - service URI set/read against the new expected URL shape
  - reward/round-init mode boot
  - unix socket RPC health
- If possible, add a smoke path that pairs:
  - `orch-coordinator`
  - `secure-orch-console`
  - `protocol-daemon`
  at the documentation/example level, even if not as a single automated E2E test.

Exit criteria:

- daemon behavior is proven unchanged where intended
- rewrite-specific service-URI flow is covered by at least one test or smoke script

### Phase 4 — dependency cleanup

- Review the copied `chain-commons` surface after the daemon is green.
- Decide whether the copied support code should remain nested support for
  `protocol-daemon` or be promoted into a shared monorepo component later.

Exit criteria:

- the copied support surface is documented and intentionally owned

## 8. File-copy policy for this migration

The user has explicitly authorized copying from:

- `/home/mazup/git-repos/livepeer-cloud-spe/livepeer-modules-project/protocol-daemon`

For every copied load-bearing file set, the migration commit message must record:

- what was copied
- from where
- that the copy was user-authorized

`protocol-daemon/AGENTS.md` should also include a source-attribution section similar to
what `secure-orch-console/AGENTS.md` already does for its ported canonicalizer/signer.

## 9. Risks

### 9.1 `chain-commons` copy scope drift

The daemon is small; the dependency surface under it is not. A careless copy can turn
into a grab-bag transplant. Keep the port bounded to the packages the daemon actually
imports plus their required support code.

### 9.2 False parity on manifest hosting

If we copy docs and examples mechanically, the daemon may still refer to the old
publisher path. That would be operationally wrong in the rewrite. Service-URI docs are
the most important adaptation.

### 9.3 Over-scoping into old secure-orch behavior

The old console depended on `protocol-daemon` for chain operations. The rewrite
`secure-orch-console` currently does not. Keep that separation. Port the daemon first;
UI integration can come later if wanted.

## 10. Acceptance bar

This plan is complete when:

- `protocol-daemon/` exists as a first-class component in this monorepo
- it builds and tests cleanly
- it can still do round init and reward
- it can set/read the on-chain service URI for the coordinator-hosted manifest
- it preserves the AI service-registry flow present in the old daemon
- its docs match the rewrite architecture rather than the old publisher-centric flow
- it no longer depends on sibling repos for the copied chain-side logic

## 11. Recommended execution order

1. Create `protocol-daemon/` by copying the old component wholesale.
2. Copy in the required `chain-commons` and support packages.
3. Fix module/import paths and dependency wiring.
4. Get tests green before changing behavior.
5. Adapt docs and compose to the rewrite’s coordinator-hosted manifest model.
6. Add the minimum integration coverage around `SetServiceURI`.
7. Document the final copied support surface and ownership boundary.

This keeps the port honest: bring the full daemon and the logic it needs, then adapt
the publication integration point to the rewrite architecture.

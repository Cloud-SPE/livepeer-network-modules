---
status: active
last-reviewed: 2026-05-06
audience: rewrite contributors, suite operators planning a cutover
---

# Migration from the suite

This doc maps every component of the existing `livepeer-network-suite` (and its
prior reference implementation, `livepeer-modules-project/`) to its replacement
in this rewrite, sequences the deprecation in phases gated by the rewrite's
v1.0.0, and identifies what the suite still owns long-term (operational state,
secrets, on-chain identity). Per-component code-level deltas live in
component-specific plans (notably plan 0013 for the suite's
`livepeer-openai-gateway`); this doc is the cross-cutting digest.

## 1. The suite as it stands today

The suite is a meta-repo (`livepeer-network-suite/`) that pins 14 submodules
under the `v4.0.1` / `v4.0.2` (modules) coherent-release contract. Behind it
sits an older multi-module Go reference implementation
(`livepeer-modules-project/`) that several of those submodules already
consumed before the suite-level submoduling pattern existed.

Inventory by status tag (deprecated / preserved / partially-ported / TBD):

**Worker-node binaries** — `livepeer-network-suite/openai-worker-node/`
(v4.0.1 / `80b2347`), `vtuber-worker-node/` (v4.0.1 / `633049f`),
`video-worker-node/` (v4.0.1 / `b32951b`). All three are workload-shaped Go
daemons that bake capability semantics into their build. **Deprecated** —
collapsed into one `capability-broker/` per
`docs/design-docs/architecture-overview.md` lines 196–199.

**Per-workload gateways** — `livepeer-openai-gateway/` (v4.0.1 / `098a2f3`),
`livepeer-openai-gateway-core/` (v4.0.1 / `8737750`), `livepeer-vtuber-gateway/`
(v4.0.1 / `d5cf095`), `livepeer-video-gateway/` (v4.0.1 / `111c9f5`),
`livepeer-video-core/` (v4.0.1 / `cd2a139`). **Partially ported.** The shells
stay live during cutover and migrate per-workload (plan 0013 covers the OpenAI
shell). The `core` packages (engine + adapter contracts) lose their per-mode
hardcoding and re-emerge as `gateway-adapters/` per-mode middleware.

**Suite-level operator surfaces** — `livepeer-secure-orch-console/` (v4.0.1 /
`5d2ccc5`), `livepeer-orch-coordinator/` (v4.0.1 / `b767bfe`),
`livepeer-gateway-console/` (v4.0.1 / `08e9063`), `livepeer-up-installer/`
(v4.0.1 / `6e16246`). **Partially ported.** Their server-side logic gets
re-targeted at the rewrite's manifest schema (capability-as-roster-entry) and
broker socket; UI shells survive and re-skin. Tracked under the planned
`orch-coordinator/`, `secure-orch-console/`, and operator-installer
subfolders.

**Reference Go monorepo** — `livepeer-modules-project/` containing:

- `chain-commons/` (`v0.1.0-chain-commons-feature-complete`): Ethereum/Arbitrum
  glue (RPC, signing, BoltDB durable tx-state, Controller resolver, gas
  oracle, BondingManager + RoundsManager bindings). **Partially ported.**
  The biggest borrower is plan 0016 (chain integration) for the rewrite's
  `payment-daemon/` providers (Broker / KeyStore / Clock / GasPrice).
- `payment-daemon/`
  (`v0.7.0-payment-daemon-fully-on-chain-commons`): probabilistic-micropayment
  ticket handling (sender/receiver, BoltDB ledger). **Partially ported.** The
  rewrite's `payment-daemon/` (plan 0014, completed) is wire-compat at the
  envelope layer; the cryptography/chain code paths remain stubbed under
  provider interfaces and plan 0016 is the dedicated borrow for the on-chain
  paths.
- `service-registry-daemon/` (`v0.4.0-service-registry-on-chain-commons`):
  manifest publisher (`Publisher.BuildAndSign`) + chain-anchored resolver
  (`Resolver.Select`, BondingManager pool walk). **Partially ported.** Plan
  0018 (orch-coordinator UX rework) borrows the publisher/resolver split and
  the round-tick auto-discovery; the manifest schema changes to the flat
  capability-tuple shape per `architecture-overview.md` Layer 4.
- `protocol-daemon/` (`v0.3.0-protocol-daemon`): on-chain `initializeRound` +
  `rewardWithHint` + `setServiceURI`. **TBD — leaning preserved.** This is
  pure on-chain control-plane plumbing; nothing in the rewrite displaces it.
  Most likely outcome: keep running the suite's daemon image against the
  rewrite's secure-orch-console.
- `proto-contracts/` and `worker-runtime/` (Go libraries): wire stubs and
  shared receiver-side scaffolding for external worker repos.
  **Deprecated.** `worker-runtime` is killed by `architecture-overview.md`
  line 200 ("per-capability Go `Module` impls in `worker-runtime`"); the
  proto-contracts get superseded by the language-neutral spec in
  `livepeer-network-protocol/` (a subfolder of this rewrite).

**Vtuber-specific surface** — `livepeer-vtuber-project/` (v4.0.1 / `5dc46d2`).
The consumer-facing SaaS Pipeline, an Open-LLM-VTuber fork. **Preserved.**
This is product code that *consumes* the suite, not infrastructure code that
*is* the suite. It survives the migration and consumes the rewrite via the
new `livepeer:vtuber-session` capability mode the same way it consumes
`livepeer-vtuber-gateway` today.

**Dead reference** — `livepeer-modules-conventions`. Already retired in the
suite's docs and explicitly killed by `architecture-overview.md` line 202.
Replaced by the spec subfolder `livepeer-network-protocol/` in this rewrite.

## 2. Replacement map

| Suite component | Replacement in rewrite | Status | Plan reference |
|---|---|---|---|
| `openai-worker-node` | `capability-broker/` (one host process; OpenAI is config, not code) | deprecated | plans 0003, 0006 (broker + HTTP modes) — completed |
| `vtuber-worker-node` | `capability-broker/` + `session-control-plus-media` mode driver | deprecated | plan 0012 (session-open phase) — completed; 0012-followup for media plane |
| `video-worker-node` | `capability-broker/` + `rtmp-ingress-hls-egress` mode driver | deprecated | plan 0011 (session-open phase) — completed; 0011-followup for RTMP/FFmpeg/HLS pipeline |
| `worker-runtime/` (Go lib) | None — workload-agnostic broker has no per-capability Go to share | deprecated | architecture-overview.md L200 |
| `livepeer-modules-conventions` (reference) | `livepeer-network-protocol/` (modes + extractors + conformance, language-neutral spec) | deprecated | plan 0002 — completed |
| `livepeer-modules-project/payment-daemon` | `payment-daemon/` (this monorepo; sender + receiver, opaque capability/work-unit names, wire-compat envelope) | partially ported | plans 0005, 0014 — completed; 0016 for chain providers |
| `livepeer-modules-project/service-registry-daemon` (publisher) | `secure-orch-console/` + manifest builder using flat capability-tuple schema | partially ported | plan 0018 (orch-coordinator) |
| `livepeer-modules-project/service-registry-daemon` (resolver) | Resolver kept; tuple-shaped response carrying `interaction_mode`; chain auto-discovery preserved | partially ported | plans 0008, 0009 — completed; 0018 for coordinator UX |
| `livepeer-modules-project/protocol-daemon` | None displacing it — the rewrite's `secure-orch-console/` mounts the suite daemon's socket as-is | preserved | (no rewrite plan; runs the suite image) |
| `livepeer-modules-project/chain-commons` | Provider interfaces in `payment-daemon/providers/` (Broker, KeyStore, Clock, GasPrice) backed by Arbitrum One; deliberate code copies, not a Go-module import | partially ported | plan 0016 (chain integration) |
| `livepeer-modules-project/proto-contracts` | `livepeer-network-protocol/modes/` (spec); plus the wire-locked 5-message Payment family copied into `payment-daemon/` per plan 0014 | partially ported | plans 0002, 0014 — completed |
| `livepeer-openai-gateway-core` | `gateway-adapters/` per-mode middleware (HTTP family done in plan 0008); per-mode and per-capability code separates cleanly | partially ported | plan 0008 — completed; 0013 for the migration brief |
| `livepeer-openai-gateway` | `openai-gateway/` (this monorepo, reference impl); the suite shell migrates per plan 0013 | partially ported | plans 0009 — completed; 0013 (suite shell migration) |
| `livepeer-vtuber-gateway` | Per-mode `session-control-plus-media` adapter in `gateway-adapters/`; the vtuber-specific SaaS gateway re-skins onto it | partially ported | plan 0012-followup; gateway-adapters 7-followup |
| `livepeer-video-gateway` + `livepeer-video-core` | Per-mode `rtmp-ingress-hls-egress` adapter in `gateway-adapters/`; the video gateway re-skins onto it | partially ported | plan 0011-followup; gateway-adapters 7-followup |
| `livepeer-secure-orch-console` | `secure-orch-console/` in this monorepo (diff + one-click sign UX) | partially ported | plan 0019 (planned) |
| `livepeer-orch-coordinator` | `orch-coordinator/` in this monorepo (capability-as-roster-entry UX) | partially ported | plan 0018 (planned) |
| `livepeer-gateway-console` | Gateway-side console, planned but not in any active plan; survives in suite form during cutover | TBD | (no plan yet) |
| `livepeer-up-installer` | Operator installer for the rewrite's `host-config.yaml` shape; planned subfolder | partially ported | (no plan yet) |
| `livepeer-vtuber-project` (Pipeline SaaS) | None — this is consumer product code, not infrastructure | preserved | (out of scope for this rewrite) |
| Suite meta-repo (`livepeer-network-suite`) | Long-term: archived or converted to a thin bookkeeping shell once every submodule has a successor | deprecated (timeline-gated) | this doc, phase N |

## 3. Deprecation timeline

Phases run sequentially; each phase has a hard gate before the next opens.

### Phase 1 — Rewrite reaches v1.0.0 (gates everything else)

Acceptance: this monorepo cuts its first tag. Specifically: chain integration
(plan 0016) lands so the `payment-daemon` providers bind to Arbitrum One, the
sender/receiver wire-compat is verified against go-livepeer fixtures, and the
six interaction modes are conformance-passing end-to-end (most are already
green per `PLANS.md`).

Observable signal: a `vX.Y.Z` Git tag in `livepeer-network-rewrite`, the
`tztcloud/<image-name>:vX.Y.Z` images published, and a successful
mainnet smoke (per core-belief #3 — no testnets) of one orch + one
gateway running rewrite images against Arbitrum One.

Suite stays unchanged in this phase. No suite repos archived. No operator
asked to migrate yet.

### Phase 2 — Per-gateway migrations (per plan 0013 for the OpenAI adapter)

Acceptance: the suite's `livepeer-openai-gateway` repo cuts a release that
consumes the rewrite's `gateway-adapters/` (HTTP-family middleware) and the
new resolver tuple shape; equivalent per-gateway plans land for the video and
vtuber gateways using their respective mode adapters. This phase is
fan-out — the three gateway shells migrate in parallel, each on its own
schedule, each behind its own plan.

Acceptance per gateway: the migrated shell passes a byte-for-byte fixture
round-trip against the previous wire (the same harness plan 0014 used to lock
the Payment envelope), the customer-facing API surface is unchanged, and
the gateway is running the rewrite's resolver in production for at least
seven days at dust-traffic volumes before flipping the rest of its fleet.

Observable signal: the gateway repo's release tag references the rewrite's
plan number (e.g. plan 0013 for OpenAI); the gateway's
`livepeer_routes_total` Prometheus counter reports nonzero traffic against a
manifest with the new flat-tuple schema.

### Phase 3 — Worker-node repos archived

Acceptance: each `*-worker-node` repo has been replaced in production by
operators running `capability-broker/` images against equivalent backends. An
operator running zero suite-shaped workers is the precondition; the orch
fleet's published manifests no longer contain entries with the old worker
URL conventions. At that point each worker-node repo's `main` branch gets a
final `DEPRECATED.md` commit pointing here, and the repo is archived on
GitHub.

Observable signal: zero `Livepeer-Capability` headers reaching a
suite-shaped worker URL across the orch fleet (measured by the orch's
Prometheus); each worker-node repo's GitHub status flipped to "archived".

### Phase 4 — Documentation migration

Acceptance: every README in the suite that references a deprecated component
links instead to its rewrite successor. The suite's `docs/design-docs/` is
audited; any cross-cutting design doc that's been superseded by something in
this monorepo carries a clear "superseded by" pointer at the top. The
`livepeer-modules-project/` README adds a banner pointing chain-borrowers at
plan 0016 in this monorepo.

Observable signal: a doc-gardener pass across the suite (the suite already
has one — see suite's `tools/doc-gardener/`) returns zero broken links to
deprecated components.

### Phase 5 — Suite meta-repo archived (or shrunk to bookkeeping)

Acceptance: every submodule has either been archived or has a stable migration
target. The suite meta-repo either gets archived outright or converted to a
thin "historical pins" shell — a single README pointing at the rewrite, a
table of last-known-good submodule SHAs at archival time, and no remaining
release process.

Observable signal: no suite tag cut for 90 days; the suite repo's GitHub
status is "archived" or its README header reads "this repo's components live
in `livepeer-network-rewrite` as of ...".

## 4. What the suite preserves long-term

These are not code; they're operational state, secrets, and chain identity.
None of them migrate. The rewrite is deliberately positioned to consume them
unchanged.

- **The cold-key keystore on the operator's `secure-orch` host.** Per
  core-belief #4 (`docs/design-docs/core-beliefs.md` lines 28–35) and
  `architecture-overview.md` line 217, this never crosses a host boundary
  and never enters this monorepo. Operators who already have a cold orch
  keystore on a hardened host keep it. The rewrite's
  `secure-orch-console/` mounts it the same way the suite's console does.

- **The on-chain orchestrator identity** registered in
  `ServiceRegistry.serviceURI` on Arbitrum One. The orch's ETH address is
  the durable identity; the rewrite changes what's *behind* that URI (the
  manifest schema), not the URI itself. Operators do not re-register
  on-chain to migrate.

- **The on-chain protocol-daemon role** (`initializeRound`, `rewardWithHint`,
  `setServiceURI`). This is pure chain plumbing; the rewrite does not
  re-implement it. Operators keep running the suite's
  `tztcloud/livepeer-protocol-daemon` image.

- **Existing escrow / TicketBroker / BondingManager state.** The rewrite's
  `payment-daemon` ships wire-compat with the suite's envelope (plan 0014)
  precisely so that an operator's existing on-chain reserve, escrow, and
  bond state continue to work without a chain migration.

- **The redeemed-ticket set + replay-protection nonces** in each operator's
  `payment-daemon` BoltDB (`state.db`). Not migrated — when an operator
  flips their host from suite-shaped to broker-shaped, the rewrite's
  `payment-daemon` reads the same store layout (subject to plan-0016
  closeout; tracked there).

- **Operator-authored Grafana dashboards and Prometheus alerts** that
  scrape `livepeer_*` metric names. Per core-belief #9 the metric name space
  is preserved across the rewrite (`livepeer_routes_total`, etc.), so
  operator-side observability survives.

## 5. Code-port flow

When the rewrite borrows from `livepeer-modules-project/`, the process is:

1. **Read** the file in the prior implementation. Do not submodule-import,
   do not git-subtree, do not add a Go-module dependency on the prior tree.
2. **Write** a new file in this monorepo with whatever shape the new design
   wants. The borrow is a *deliberate copy*, not an import (per
   `AGENTS.md` lines 62–66 and core-belief #14).
3. **Attribute** the borrow with a comment at the top of the new file:
   `// Borrowed from livepeer-modules-project/<path> @ <tag-or-sha>` plus a
   one-line summary of *what* was borrowed and *why* it could not be
   imported.
4. **Record** the borrow in the commit message that introduces it: source
   path, source tag/SHA, user-given permission to copy. AGENTS.md lines
   62–66 make this a hard rule.

The biggest borrowers:

- **Plan 0016 (chain integration)** is the heaviest borrower from
  `livepeer-modules-project/`. Specifically: the
  `payment-daemon/`'s `pm/` (probabilistic-micropayment math), `settlement/`
  (redemption + ticket validation), `escrow/` (reserve / deposit reads), and
  `chain-commons/` (RPC + signing + Controller resolver + gas oracle). Each
  is a deliberate copy; the new shape lives behind the
  `payment-daemon/providers/` interface boundary so future re-pinning is
  swap-not-rewrite.

- **Plan 0018 (orch-coordinator UX)** borrows from
  `livepeer-modules-project/service-registry-daemon/` — specifically the
  publisher's `BuildAndSign` flow and the resolver's BondingManager pool walk
  + per-round refresh. The manifest builder's *schema* changes (flat
  capability-tuple list, no per-host registration unit) but the chain
  read/sign mechanics carry over.

- **Plan 0014 (wire-compat envelope)** already borrowed the 5-message
  Payment wire family (`Payment`, `TicketParams`, `TicketSenderParams`,
  `TicketExpirationParams`, `PriceInfo`) into `payment-daemon/`. That borrow
  is now closed; documented as the canonical attribution example.

**Hard no:** never submodule-import, never `git subtree pull`, never
`replace` directive against a sibling tree. Each port is a one-shot,
attributed copy.

## 6. Operator-facing migration

What an operator running the suite today does to migrate. Each phase below
is gated by the corresponding deprecation phase above; no operator action is
needed until phase 2 opens.

### Phase 1 (rewrite reaches v1.0.0) — operator does nothing

The rewrite ships in parallel. Operators continue to run their suite-shaped
fleet. No action required.

### Phase 2 (per-gateway migrations open)

Per gateway type the operator runs (OpenAI, video, vtuber):

1. Pull the migrated gateway shell's release tag (e.g. plan 0013 for OpenAI).
2. Update the gateway's compose file to consume the rewrite's
   `service-registry-daemon` (resolver) and `payment-daemon` (sender)
   images.
3. Wire-compat smoke-test against the operator's existing hot wallet — at
   dust-traffic volume — for at least 24 hours.
4. Flip remaining gateway traffic over.
5. Confirm `livepeer_routes_total` is nonzero on the new shape and
   ticket redemption continues against unchanged escrow.

### Phase 3 (worker fleet migration)

Per worker host:

1. Author a `host-config.yaml` declaring the host's existing capabilities in
   the new shape (one tuple per capability + backend descriptor pointing at
   the existing inference container or LAN service).
2. Pull the rewrite's `tztcloud/capability-broker:vX.Y.Z` image; replace the
   existing `*-worker-node` container in the host's compose file.
3. Restart the host's `payment-daemon` against the new broker socket
   (receiver mode unchanged; only the upstream caller changed).
4. Regenerate the signed manifest from `secure-orch` (operator-driven sign
   cycle per `architecture-overview.md` Layer 5) so the coordinator picks up
   the new capability tuples.
5. Drain old `*-worker-node` containers from the fleet host-by-host. No
   parallel-run of old + new shapes — per core-belief #13, no
   backwards-compatibility shim.
6. After the last host flips, archive the host's old worker-node compose
   stanza locally; the suite repo it came from will be archived in phase 3.

### Phase 4 (documentation migration)

Most operators do nothing here. Operators who maintain custom runbooks
referencing deprecated suite components should update those runbooks to
point at this monorepo's `host-config.yaml`-based flow.

### Phase 5 (suite archival)

Operators do nothing. The suite repo's archival is a publisher-side action.

## 7. Risk ledger

| Risk | Mitigation |
|---|---|
| Suite operators in production during cutover | The rewrite ships in parallel; operators migrate at their own pace; suite repos and image tags stay live until the last operator drains. Phases 3 + 5 are measured by *observed* zero suite-shape traffic, not by a calendar. |
| Wire-compat regressions between rewrite `payment-daemon` and go-livepeer / suite ticket validators | The byte-for-byte fixture round-trip test from plan 0014 (and the chain-integration extension in plan 0016) gates every release. Same fixture suite re-runs in phase 2's per-gateway smoke. |
| Custom capabilities operators have built that don't fit the rewrite's six initial modes | Per `architecture-overview.md` Layer 2, extending modes is supported (new mode = one adapter on each side, not a trunk schema change). Operators with a sixth-mode capability open a plan that adds the mode; their existing custom worker keeps running on the suite shape until that plan lands. |
| Operator on the cold-key path is uncomfortable with the new manifest schema | The `secure-orch-console/` UX surface (plan 0019) is explicitly diff-driven — the operator sees the candidate manifest's flat-tuple shape next to the previously-signed shape before tapping sign. No silent schema upgrade. |
| Chain-state divergence (escrow / reserve / redeemed-ticket set) between suite and rewrite `payment-daemon` instances | All three are persistent on Arbitrum One or in BoltDB; the rewrite reads/writes the same stores. Plan 0016 closes this explicitly with a fixture set against a real Arbitrum One contract address. |
| Plan 0013 (OpenAI gateway migration brief) gets ahead of plan 0016 (chain integration), causing per-gateway smoke to fail against unbacked stub providers | Phase 1 explicitly gates everything else on the rewrite reaching v1.0.0, which requires plan 0016 closed. No phase-2 work opens until phase 1's gate is observable (the v1.0.0 tag + Arbitrum One smoke). |
| Operator who maintains forks of `worker-runtime` to add custom capabilities loses the fork's substrate when `worker-runtime` is killed | The new substrate is a YAML config + an extractor recipe. Where the fork added a capability, the rewrite's flow is: declare the capability in `host-config.yaml`, point its `backend` at the existing custom inference service, pick a matching `extractor` recipe. The recipe set is a small, deliberately curated library (six recipes today); adding a new one is a broker change per `architecture-overview.md` lines 117–119, scoped behind its own plan. |

## 8. Out of scope

This doc deliberately does not cover:

- **Per-component code-level diffs.** Those live in component-specific
  migration plans — plan 0013 for the OpenAI shell is the worked example;
  per-gateway plans for video and vtuber will follow the same template
  when their gateway-adapters 7-followup work is sequenced.
- **Operator support process, contracts, pricing, SLA.** Those are
  operations concerns, not architecture concerns. Suite operators who have
  paid support arrangements continue under those arrangements; the
  contract is between the operator and their support provider.
- **Per-extractor recipe additions.** The fixed extractor set
  (`openai-usage`, `response-jsonpath`, `request-formula`, `bytes-counted`,
  `seconds-elapsed`, `ffmpeg-progress`) is documented in
  `architecture-overview.md` Layer 3; new recipes are a separate plan
  scoped at `capability-broker/`.
- **Image tag ownership during the cutover window.** Per core-belief #14
  (clean-slate rewrite — the suite is untouched), the rewrite does not
  bump or republish suite image tags. Each side maintains its own
  release line; phase 5 archives the suite line.
- **Versioning of the `livepeer-vtuber-project` consumer SaaS.** That repo
  is preserved across the migration and consumes the new
  `livepeer:vtuber-session` capability the same way it consumed the suite
  shape.

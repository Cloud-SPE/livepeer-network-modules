# Core beliefs

Invariants any change in this repo must uphold. These exist because past incidents (or
strong stakeholder preference) made them load-bearing. To change one, open a plan in
`exec-plans/active/` first.

## 1. Workload-agnostic by construction

The pin: *register, pay, discover, and route work without the architecture knowing what
the workload is.* Every leak of workload-specific knowledge into a layer that doesn't
need it (the registry, the payment daemon, the gateway router) is a bug.

The single permitted leak point is the **interaction-mode typology** — and even there,
modes describe transport shape, not capability semantics.

## 2. Capabilities are open-world strings

Anyone — orch, gateway, third-party developer — can invent a capability identifier. No
canonical schema registry. No closed enum in the trunk. The trust layer validates *who*
offers a capability, not *what* the capability does.

## 3. Mainnet-only — no Livepeer testnets

Inherited from the suite. Deploy and smoke-test against Arbitrum One from day one.
Mitigate risk with dust amounts, not testnets. Testnets diverge from mainnet in ways
that mask real failures.

## 4. Cold orch keystore is sacred

The cold key lives on a firewalled `secure-orch` host and never crosses a host boundary.
It signs every manifest publication. **Secure-orch never accepts inbound connections.**
Operator drives the sign cycle (download candidate → sign → upload signed). No automated
push for v1. Hand-carry friction is solved in console UX, not in the transport.

This may be revisited in v2; not now.

## 5. No closed enums, no chokepoints

`payment-daemon` accepts opaque capability and work-unit strings; it does arithmetic
only. The manifest schema is a flat list of capability tuples. The coordinator's roster
is per-capability, not per-host. **No layer should require a `livepeer-modules` change
to onboard a new capability.**

## 6. Modes are specifications, not libraries

Interaction modes live in a language-neutral spec repo (working name:
`livepeer-network-protocol`). Implementers conform to the spec; no required shared library.
Reference implementations are opt-in. Conformance test suites are the trust mechanism.

## 7. Trust the orch's reported usage in v1

Verifiability is desired but explicitly out of initial scope. Worker reports `actualUnits`;
gateway debits. Schema reserves a `signed_by` slot for v2 verifiable-receipt extractors.
Market punishes liars over time.

## 8. Capacity is not advertised

`capacity` numbers are gameable and meaningless cross-workload. Workers return HTTP 503
+ `Livepeer-Backoff: <seconds>` when full; gateway resolver weights down per-`(orch,
capability)` failure rate. Operators may set a self-imposed local concurrency cap, but
it is not advertised.

## 9. Metrics at the edges; aggregation is third-party

Components expose Prometheus on a documented schema. Demand-visibility / market data /
public dashboards are built by independent scrapers, not by the architecture itself.
Permissionless ethos applies.

## 10. Image tags are not bumped silently

Inherited from the suite. Republishing a Cloud-SPE image overwrites the existing named
tag. Version bumps require explicit approval.

## 11. Documentation is enforced, not aspirational

Stale docs are worse than missing docs. Update docs in the same PR that changes the
behavior they describe. References (`docs/references/`) are point-in-time provenance and
do not get edited after the fact — supersede with a new dated reference if the picture
changes.

## 12. Throughput-friendly merge gates

Inherited from the suite. Short-lived PRs. Minimal blocking checks. Test flakes get
follow-up runs, not indefinite blocks. Corrections are cheap; waiting is expensive.

## 13. No backwards-compatibility shims for the old worker shape

The three-worker-binary partition is the problem we're solving. We don't preserve it as
a "legacy mode" or a fallback. The migration story is timed deprecation, not parallel
support.

## 14. Clean-slate rewrite — the existing suite is untouched

This repo is a **completely new thing**. It does not modify, fork, or replace any
submodule of `livepeer-network-suite`. The suite stays as it is.

When this repo needs material from somewhere outside it (manifest schemas, proto
definitions, header conventions, prior implementations), the material is **copied in
on explicit user instruction** — never automatically, never wholesale. Each copy is a
deliberate decision recorded in the commit message that introduces it.

The first time this repo cuts a release, it becomes **v1.0.0**. Until then, all SHAs
are unstable and re-pin-able. Components inside the monorepo do not have independent
versions until they're extracted to standalone repos.

## 15. Docker-first build and run

Every component in this monorepo ships with a Docker-first build and run story:

- a `Dockerfile`,
- a `Makefile` wrapping common gestures (`build`, `test`, `shell`, `publish`),
- a `compose.yaml` when multi-service orchestration is needed.

**Implementers and operators do not install language runtimes** (Go, Node, Python,
ffmpeg, etc.) on their hosts to use a component. They pull a published image or run
`make build`.

**Image namespace:** `tztcloud/<image-name>`. Tags follow the monorepo's SemVer (or
the component's, after extraction).

**Why:** "install Go on your laptop" (or Python, or Node, or matching system ffmpeg)
is a friction barrier that disproportionately hurts adoption by external developers
and CI environments. Containers pin the runtime; Make pins the gestures; compose pins
the multi-service shape.

## 16. Dependencies stay current

Any external dependency — Go module, npm package, Docker base image, Github Action,
pre-commit hook, system package, anything — defaults to its **latest stable release**.
Pinning to an older version is a deliberate decision recorded in the commit message
that creates the pin and added to
[`tech-debt-tracker.md`](../exec-plans/tech-debt-tracker.md) until resolved.

**Why:** outdated dependencies accumulate known security vulnerabilities and missed
bug fixes. Drift between components compounds the cost. The existing suite already
shows the symptom (daemon image versions disagreeing across submodules).

**How to apply:**

- Bumping a dependency includes validation — the build passes, the conformance suite
  passes (where applicable), and any breaking changes are addressed in the **same PR**.
- "Latest where possible without breaking everything" — pragmatic. If a major bump
  requires non-trivial rework, that's a planned exec-plan, not a casual PR.
- Automated update tooling (Dependabot / Renovate / equivalent) runs per component;
  CI failures from bumps are **fixed forward**, not left to rot in a perpetual
  draft PR.

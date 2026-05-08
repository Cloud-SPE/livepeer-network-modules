---
title: Core beliefs
status: accepted
last-reviewed: 2026-05-01
---

# Core beliefs

Non-negotiable invariants. Every design-doc, exec-plan, and line of code in this repo respects these. Changing any of them requires its own design-doc.

## 1. Scaffolding is the artifact

The value we produce is the repository structure, the lints, the CI, the docs, and the exec-plans. Code is generated to fit the scaffold. If a change makes the scaffold weaker, reject it — even if the code is fine.

## 2. Repository knowledge is the system of record

Anything not in-repo doesn't exist. Slack threads, Google Docs, and tribal knowledge are invisible to the agents that maintain this codebase. If a decision matters, it lives in `docs/design-docs/`. If a plan matters, it lives in `docs/exec-plans/`.

## 3. The registry is workload-agnostic

A capability is an opaque string. The registry does not know what `livepeer:transcoder/h264`, `openai:/v1/chat/completions`, or `myco:my-future-thing/v3` mean. There is no AI service, no transcoding service, no openai service. There is one service: capability advertisement. The data inside is opaque to the daemon. Adding a new workload type must require zero code changes in this repo — only documentation and consumer-side parsing.

## 4. Backwards compatibility is non-negotiable

Resolver deployments target exactly one configured registry contract and treat the on-chain `getServiceURI(addr)` value as the exact manifest URL to fetch. Legacy transcoding clients that dial the returned URL remain supported only when operators publish a directly dialable URL there. Structured metadata lives off-chain in the signed manifest at that exact URL. Code paths that would silently reintroduce derived well-known-path probing are rejected at review.

## 5. Manifests are signed claims, not chain truth

The chain provides the trusted *pointer*. The manifest is operator-asserted off-chain content. We require it be signed by the chain-associated eth key so a resolver can verify the operator made the claim, even though the content itself isn't on chain. An unsigned manifest is rejected unless the operator's static-overlay explicitly opts that address into `unsigned-allowed`.

## 6. The providers boundary is the only cross-cutting boundary

`service/*` may not import `github.com/ethereum/*`, `go.etcd.io/bbolt`, `net/http`, or any external cross-cutting dependency directly. Everything external goes through `internal/providers/`. This is enforced mechanically.

## 7. Enforce invariants, not implementations

Lints check structural properties (layer dependencies, structured logging, no-secrets-in-logs, no-unverified-manifest, file-size limits). They do not prescribe libraries, variable names, or stylistic preferences. Agents get freedom within the boundaries.

## 8. Humans steer; agents execute

Humans author design-docs, open exec-plans, and review outcomes. Agents do the implementation. If an agent is struggling, the fix is almost always to make the environment more legible — not to push harder on the task.

## 9. No code without a plan

Non-trivial changes start with an entry in `docs/exec-plans/active/`. Bugs, drive-by fixes, and one-line changes are exempt (see `PLANS.md`). This is how we keep progress visible and reviewable.

## Project-specific invariants

- **Publisher and resolver share a binary.** One process, two modes. Separate binaries would fragment the deps.
- **Unix-socket gRPC only for v1.** TCP exposes the daemon to the network; we want zero trust at the IPC boundary but trust the local caller.
- **Operator holds the publisher key.** The default custody model is daemon-owned keystore. Remote-signer is a v2 provider swap.
- **BoltDB for v1 cache + audit log.** Single-writer, embedded, zero external deps. SQLite or external stores are provider swaps for later.
- **Test coverage ≥ 75% for every package.** Non-negotiable. CI fails if any package under `internal/` falls below 75% statement coverage. The threshold is a floor, not a target — aim higher. Packages with inherent test difficulty (e.g., `cmd/` main entry points) are exempt only when explicitly listed in `lint/coverage-gate/exemptions.txt` with a written reason.
- **Resolver never blocks on chain RPC for cached entries.** Cache reads return immediately; refresh happens in the background. Operator-tunable TTL; last-good fallback on refresh failure.

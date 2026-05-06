# AGENTS.md

This is `openai-gateway/` — the reference OpenAI-compatible gateway
demonstrating the new wire spec end-to-end. Real OpenAI client SDKs hit
this gateway → Livepeer middleware → broker → mock backend. Per
[plan 0009](../docs/exec-plans/completed/0009-openai-gateway-reference.md).

Component-local agent map. Repo-root [`../AGENTS.md`](../AGENTS.md) is the
cross-cutting map.

## Operating principles

Inherited from the repo root. Plus:

- **Reference impl, not production.** This gateway exists to prove the
  wire shape under real OpenAI client traffic and to be the migration
  template for the suite's `livepeer-openai-gateway`. It is NOT a
  customer-facing production gateway: no real auth, no real payment,
  no Postgres ledger, no Stripe billing.
- **Inlined middleware.** v0.1 inlines the Livepeer client logic in
  `src/livepeer/` rather than depending on
  `@tztcloud/livepeer-gateway-middleware` via npm. Mirrors the
  gateway-adapters API; switching to the actual package via npm
  workspaces is tracked as tech-debt.
- **Stub payment.** The broker accepts any non-empty `Livepeer-Payment`
  blob; we send `"stub-payment"`. Real envelope minting is plan 0005.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Architectural overview | [`DESIGN.md`](./DESIGN.md) |
| Build / run / smoke gestures | [`Makefile`](./Makefile) |
| Compose stack | [`compose.yaml`](./compose.yaml) |
| The middleware this mirrors | [`../gateway-adapters/`](../gateway-adapters/) |
| The broker it talks to | [`../capability-broker/`](../capability-broker/) |
| The wire spec | [`../livepeer-network-protocol/`](../livepeer-network-protocol/) |

## Doing work in this component

- Docker-first per core belief #15. Use `make build`, `make smoke`.
- TypeScript strict; tsc is the lint gate.
- Three OpenAI endpoints in v0.1; new endpoints are spec changes (open a
  plan).

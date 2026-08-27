# AGENTS.md

This is `pool-controller/` — the Pool member-management and accounting
component introduced by plan 0029. It no longer generates a broker config
file: it pushes the pool's offer set and attach credentials to the broker over
the broker admin API (plan 0043).

Component-local guidance. The repo-root [`../AGENTS.md`](../AGENTS.md) stays
authoritative for cross-cutting rules.

## Operating principles

- **Not in the data path.** `pool-controller` manages members, config, and
  accounting surfaces; gateway traffic must keep flowing if it is down.
- **The offer push is the first contract.** Deterministic, idempotent pushes
  of the offer set and attach credentials to the broker admin API come before
  speculative admin UI work.
- **The pool never dials a member.** Member hosts attach outbound to the
  broker and declare what they run. That is why there is no join request, no
  admission review, no backend verification, and no operator approval gate —
  all of that was deleted with the legacy member model (plan 0044 §5 phase A).
  Trust comes from certification, run by the broker over the runner's own
  attach connection.
- **Keep Pool-specific policy here.** Generic request routing behavior belongs
  in `../capability-broker/`; Pool membership and settlement policy belong here.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Implementation shape | [`DESIGN.md`](./DESIGN.md) |
| Example operator config | [`examples/pool-controller-config.example.yaml`](./examples/pool-controller-config.example.yaml) |
| Current member model + rollout | [`../docs/exec-plans/active/0044-zero-touch-pool-onboarding.md`](../docs/exec-plans/active/0044-zero-touch-pool-onboarding.md) |
| How a member joins and gets paid | [`../docs/design-docs/pool-overlay-flows.md`](../docs/design-docs/pool-overlay-flows.md) |
| Original driving plan (history) | [`../docs/exec-plans/completed/0029-pool-node-design.md`](../docs/exec-plans/completed/0029-pool-node-design.md) |

## Doing work in this component

- **All gestures are Docker-first** per core belief #15. Add `make build`,
  `make test`, and `make run` surfaces before introducing operator steps that
  assume a host toolchain.
- **Types-first beats UI-first.** Member state must be representable in the
  types/repo layer before any HTTP or web UI surface is added. It is no longer
  representable in operator config: the nested `members[]` config block and its
  compatibility loader are gone, and the supported production config is
  bootstrap-only.
- **Hide private operator data by default.** Attach credentials, auth secrets,
  and payout details must stay out of public-facing output unless a plan says
  otherwise. A member must never see another member's figures.

# AGENTS.md

This is `pool-controller/` — the Pool member-management and broker-config
generation component introduced by plan 0029.

Component-local guidance. The repo-root [`../AGENTS.md`](../AGENTS.md) stays
authoritative for cross-cutting rules.

## Operating principles

- **Not in the data path.** `pool-controller` manages members, config, and
  accounting surfaces; gateway traffic must keep flowing if it is down.
- **Broker config is the first contract.** Early implementation work should
  prefer deterministic generation of `capability-broker` `host-config.yaml`
  over speculative admin UI work.
- **Keep Pool-specific policy here.** Generic request routing behavior belongs
  in `../capability-broker/`; Pool membership and settlement policy belong here.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Implementation shape | [`DESIGN.md`](./DESIGN.md) |
| Example operator config | [`examples/pool-controller-config.example.yaml`](./examples/pool-controller-config.example.yaml) |
| Driving cross-cutting plan | [`../docs/exec-plans/active/0029-pool-node-design.md`](../docs/exec-plans/active/0029-pool-node-design.md) |

## Doing work in this component

- **All gestures are Docker-first** per core belief #15. Add `make build`,
  `make test`, and `make run` surfaces before introducing operator steps that
  assume a host toolchain.
- **Config-first beats UI-first.** Member state must be representable in the
  config/types layer before any HTTP or web UI surface is added.
- **Hide private operator data by default.** Backend URLs, auth secrets, and
  payout details must stay out of public-facing output unless a plan says
  otherwise.

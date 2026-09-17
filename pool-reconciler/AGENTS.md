# AGENTS.md

This is `pool-reconciler/` — the Pool round-close producer boundary introduced
as a follow-up to plan 0029.

Component-local guidance. The repo-root [`../AGENTS.md`](../AGENTS.md) remains
the cross-cutting map.

## Operating principles

- **Round source, not payout sink.** `pool-reconciler` prepares and submits
  canonical round-close payloads; it does not own long-term accounting state.
- **Protocol timing stays outside.** `protocol-daemon` remains the source of
  round events. This component is where round-event consumption meets Pool
  economics.
- **Manual first, automation second.** The first shipped surface was a
  file-driven/manual submit command; the live protocol-daemon loop
  (`watch-rounds`) and regional source collection now sit on the same
  preparation path. Keep the manual commands working.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Current shape | [`DESIGN.md`](./DESIGN.md) |
| Example config | [`examples/pool-reconciler-config.example.yaml`](./examples/pool-reconciler-config.example.yaml) |
| Regional collection | [`docs/regional-collection.md`](./docs/regional-collection.md) |
| Original driving plan (history) | [`../docs/exec-plans/completed/0029-pool-node-design.md`](../docs/exec-plans/completed/0029-pool-node-design.md) |

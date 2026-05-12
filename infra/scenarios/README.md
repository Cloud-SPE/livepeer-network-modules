# Scenarios

Operator-facing deployment examples, organized by audience.

## Layout

- **[`orchestrator-onboarding/`](./orchestrator-onboarding/)** — the
  active orchestrator onboarding guide and every stack it references.
  Start at [`orchestrator-onboarding/README.md`](./orchestrator-onboarding/README.md).
- **[`gateway-onboarding/`](./gateway-onboarding/)** — gateway operator
  onboarding guide and every stack it references. Start at
  [`gateway-onboarding/README.md`](./gateway-onboarding/README.md).
- **[`archive/`](./archive/)** — earlier multi-module scenarios preserved
  for reference. Not maintained against the current onboarding flow; see
  [`archive/README.md`](./archive/README.md) for context.

Each scenario directory inside `orchestrator-onboarding/` contains:

- `docker-compose.yml` and any ingress overlays
- `.env.example`
- scenario-local config files
- a `README.md`

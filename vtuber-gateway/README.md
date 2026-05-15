# vtuber-gateway

Customer-facing gateway for the Livepeer vtuber product. TypeScript +
Fastify 5 + Zod + drizzle-orm + ESM. Owns:

- `POST /v1/vtuber/sessions` — session-open (per-session `payerDaemon.
  createPayment(...)` flow per Q7 lock).
- `GET /v1/vtuber/sessions/:id` — status.
- `POST /v1/vtuber/sessions/:id/end` — customer kill switch.
- `POST /v1/vtuber/sessions/:id/topup` — extend the session's
  face-value mid-stream by sending `session.topup` over the broker
  control WebSocket.
- Broker-hosted `control_url` returned by session-open — selected via
  `service-registry-daemon` in manifest mode or a static broker URL in
  single-orch mode.
- `POST /v1/billing/topup` + `POST /v1/stripe/webhook` — Stripe
  flows delegated to `customer-portal/`.

A vtuber-specific portal SPA lives in `src/frontend/portal/` (per OQ3
lock); it composes the shared shell's auth / API-key / balance / Stripe
widgets and adds vtuber-specific pages (session list, persona
authoring, scene history).

Pipeline-app integrates as a **shared-per-deployment meta-customer**
(per OQ4 lock); direct B2B integrators receive **per-customer** API
keys via the portal SPA. Both modes coexist on the same auth surface.

## Quick start

```sh
pnpm install
pnpm --filter @livepeer-network-modules/vtuber-gateway build
pnpm --filter @livepeer-network-modules/vtuber-gateway test
```

Layer 3 route-health operator endpoints:

- `GET /admin/vtuber/node-health`
- `GET /admin/vtuber/route-health/metrics`

Interpretation:

- `service-registry-daemon` supplies only routes that survive manifest and
  broker live-health checks
- `/admin/vtuber/node-health` adds the gateway's local Layer 3 view on top of
  that resolver-backed set
- `/admin/vtuber/route-health/metrics` exposes the same cooldown and outcome
  counters in Prometheus text format

## Layout

See [`AGENTS.md`](./AGENTS.md). The migration brief is
[`docs/exec-plans/completed/0013-vtuber-suite-migration.md`](../docs/exec-plans/completed/0013-vtuber-suite-migration.md).

## License

MIT.

## Source attribution

Ported from `livepeer-network-suite/livepeer-vtuber-gateway/src/` per
plan 0013-vtuber §5.1. Schema is renumbered + namespaced to `vtuber.*`
(per Q5 lock). Quote-related code is dropped (quote-free flow);
manifest/resolver selection is now part of the rewrite control plane.
The historical "vtuber-livepeer-bridge"
name is retired (per Q6 lock); citations preserve it verbatim where
they reference suite paths.

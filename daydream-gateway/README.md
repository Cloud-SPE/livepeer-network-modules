# daydream-gateway

Thin broadcaster-side gateway exposing the upstream Daydream Scope workload
(`daydreamlive/scope:main`) as a paid capability on the Livepeer network
rewrite. **Not a SaaS** — no customer billing, no API keys, no DB. The
operator running this gateway is the broadcaster.

## What it does

1. Resolves orchestrators advertising the `daydream-scope` capability
   via `service-registry-daemon` (gRPC over unix socket).
2. Picks one randomly.
3. Mints a `Livepeer-Payment` envelope via `payment-daemon` (sender
   mode, gRPC over unix socket).
4. Opens a paid session against the chosen orch's broker
   (`POST /v1/cap`, mode `session-control-external-media@v0`).
5. Hands back the broker's `scope_url` to the consumer.
6. Transparently proxies every subsequent `/api/v1/*` call to that
   `scope_url`, so consumers (e.g. `scope-playground-ui`) work without
   knowing they are talking to a Livepeer orchestrator.

## Architecture

```
   consumer (e.g. scope-playground-ui SPA)
        │
        │  HTTP: /v1/orchs (list),
        │        /v1/sessions (open),
        │        /api/v1/* (Scope-compatible passthrough)
        ▼
   daydream-gateway (this component, TS)
        │
        │  Livepeer wire (POST /v1/cap, ws control)
        │  → chosen orch's capability-broker
        ▼
   capability-broker on orch host
        │
        │  /_scope/<session_id>/* reverse proxy
        ▼
   daydreamlive/scope:main (upstream image, unmodified)
        │
        │  WebRTC media (out of gateway)
        ▼
   Cloudflare TURN ↔ consumer browser
```

## Quick start

```bash
# 1. Make sure payment-daemon (sender mode) is running with a funded
#    Arbitrum One eth_account and exposes a unix socket.
# 2. Make sure service-registry-daemon is running and exposes a unix
#    socket.
# 3. Configure env (see config.ts for the full list):
export DAYDREAM_GATEWAY_LISTEN=:9100
export DAYDREAM_GATEWAY_PAYER_DAEMON_SOCKET=/var/run/livepeer/payer-daemon.sock
export DAYDREAM_GATEWAY_RESOLVER_SOCKET=/var/run/livepeer/service-registry.sock
# 4. Run:
pnpm install
pnpm build
pnpm start
```

Point `scope-playground-ui`'s backend URL at `http://localhost:9100` (or
your gateway's externally-routable URL) and the SPA works unchanged
aside from latency.

## See also

- Component guidance: [AGENTS.md](./AGENTS.md)
- Operator runbook: [docs/operator-runbook.md](./docs/operator-runbook.md)
- Plan: [`../docs/exec-plans/active/0026-daydream-scope-capability.md`](../docs/exec-plans/active/0026-daydream-scope-capability.md)
- Mode spec: [`../livepeer-network-protocol/modes/session-control-external-media.md`](../livepeer-network-protocol/modes/session-control-external-media.md)

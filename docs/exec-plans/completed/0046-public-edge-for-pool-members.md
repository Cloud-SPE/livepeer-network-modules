---
title: Public edge for pool members — external session data planes
status: completed
date: 2026-09-02
beads: lnm-7cj
supersedes: none
---

# 0046 — Public edge for pool members

## 1. Purpose

Decision 13 of the 2026-09-02 walkthrough (plan 0045 §11) made every
paid-session data plane `external`: the caller connects to the runner
directly at the address the descriptor publishes, and the broker is never
in the media path. That is the right shape for live media and it is what
`sfu-room/v1`, `rtmp-hls/v1` and `pcm-transcript/v1` all assume. Before this plan,
the pool did not provide that reachability: a member's runner sat behind the
agent's outbound tunnel, reachable by the broker for HTTP and by nobody
else. Session templates could therefore be placed on
hosts that could not serve their external data planes.

This plan makes "the member is public" a fact the pool can see, gate on,
and prove — and gives the member one way to become public that does not
put TLS in every runner author's hands.

## 2. Decisions

Routine calls made here, in the shape the operator set with decision 13;
the operator-supplied DNS/certificate scope is recorded in §7.

1. **`public_url` is a host-level fact in the attach document** (runner-attach
   §3.1, contract minor 1.2). An `https://` origin — no path, no query — at
   which this host's session runners are reachable from outside. Absent
   means not public. Optional, so a 1.1 agent is still valid; a 1.1 broker
   rejects it as `unknown_field`, which is the versioning rule working as
   written (§8). Not an `x-*` key: placement branches on it, and `x-*` is
   never interpreted.
2. **The agent owns the edge.** One TLS listener per host, in the agent
   container, routing `/r/<local_id>/…` to `http://<local_id>:8080/…` on
   the compose network — the same address the agent already fetches
   contracts from. Runners speak plain HTTP and WebSocket inside the host;
   the certificate lives in exactly one place. `httputil.ReverseProxy`
   carries WebSocket upgrades.
3. **The runner learns its own public base from the environment.** The
   desired-state renderer sets `LIVEPEER_PUBLIC_URL=<public_url>/r/<local_id>`
   on every paid-session service. A runner builds the descriptor's `url`
   from it; it never guesses a hostname.
4. **Placement gates on it.** A paid-session template (all of which are
   `external`) is rejected on a host with no `public_url`, reason
   `host_not_public`, on the exception queue with the others. The fact is
   copied onto each of the host's hardware units at relay, the way the
   member address is, because the planner's input is units.
5. **Certification proves reachability.** The session `open` step gains an
   optional `reach` config: while the session is held open, the broker
   connects from its own vantage to `runtime.public.<field>` with the named
   grant and expects the schema's first sign of life — a WebSocket upgrade
   for a `wss://` field, a 2xx for an `https://` one. The step names the
   field and the grant operation; the broker interprets nothing else in
   the descriptor (runtime-descriptor §4). A member with a wrong
   port-forward fails certification instead of every real caller.
6. **The bundle publishes the edge.** The member's `compose.yaml` maps the
   edge port and mounts a certificate directory; `.env` carries
   `LIVEPEER_PUBLIC_URL` and the listen address.

7. **RTMPS is a second published port, terminated by the agent** (added
   2026-09-05 on the transcode team's review: the HTTP edge cannot carry
   RTMP, and the addendum that said to derive `rtmps://` under `/r/` was
   wrong). A template declares `runner_compose.rtmp_port`; the member
   publishes 1936; the agent terminates TLS on it with the HTTP edge's
   certificate and pipes the raw stream to the one runner with an ingest;
   the pool sets `LIVEPEER_PUBLIC_RTMP_URL=rtmps://<public host>:1936` on
   that service. One port per host — the live stance is one template per
   card and the runner's media router multiplexes by stream key. Not an
   L4/SNI edge (plain RTMP has nothing to route on; a port is a port) and
   not RTMP-over-WebSocket (a wire nobody's encoder speaks).

## 3. What this does not do

- **Issue names or certificates.** The certificate and the public name are
  operator-supplied (a file pair and an env var). Pool-managed DNS and
  agent-managed ACME are outside this feature's accepted scope (§7).
- **Relay media through the broker.** Deleted with decision 13; not
  coming back.
- **Per-runner ports.** The edge routes by path; nothing is published per
  service.

## 4. Implementation record

| § | Commit | What |
|---|---|---|
| 2.1, 2.4 | `163357e` | `public_url` host fact: runner-attach 1.2, agent env, broker validation and view, controller relay onto units, placement `host_not_public`, Validate agrees. |
| 2.2, 2.3, 2.5, 2.6 | `679bf8e` | Agent TLS edge routing `/r/<local_id>/`; desired-state runner URLs; certification `reach` dial; bundle port and certificate mount. |
| 2.7 | `bcd4148`, `83802ba` | RTMPS edge, `rtmp_port`, `LIVEPEER_PUBLIC_RTMP_URL`, bundle port 1936; `any` image key; sink sub-paths; `{{run.id}}` in transcode probes.

## 7. Scope decision — 2026-09-15

The supported deployment uses **operator-supplied public DNS and certificates**.
After reviewing the operational cost, the user accepted keeping the existing
public edge and excluding automatic provisioning. The uncommitted controller
DNS and agent ACME implementation was removed. No DNS service or certificate
issuer is introduced by this plan.

This completes `lnm-7cj` at the agreed scope. Automatic member names and
certificates are not a prerequisite for external sessions. They can be proposed
separately if onboarding experience warrants them. Neither DNS nor ACME solves
CGNAT, port forwarding, or runner-specific media transports.

Outbound-only hosts can serve broker-dispatched jobs. External sessions require
a reachable endpoint, operator-managed certificate renewal and agent restart,
and a certification policy that exercises the public data plane. See the
[member agent deployment instructions](../../../pool-member-agent/README.md#public-endpoints-for-external-sessions).

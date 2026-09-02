---
title: Public edge for pool members — external session data planes
status: implementing
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
`sfu-room/v1`, `rtmp-hls/v1` and `pcm-transcript/v1` all assume. It is also
something nothing in the pool makes true: a member's runner sits behind the
agent's outbound tunnel, reachable by the broker for HTTP and by nobody
else. Both session templates in the catalog therefore load and place on
hosts that cannot serve them.

This plan makes "the member is public" a fact the pool can see, gate on,
and prove — and gives the member one way to become public that does not
put TLS in every runner author's hands.

## 2. Decisions

Routine calls made here, in the shape the operator set with decision 13;
the one that is the operator's is marked open (§7).

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

## 3. What this does not do

- **Issue names or certificates.** The certificate and the public name are
  operator-supplied in this plan (a file pair and an env var). A zero-touch
  member has neither; giving them one — a pool-issued
  `<host-id>.members.<pool-domain>` with the controller answering DNS and
  the agent completing ACME — is the open decision in §7, and the only
  part of `lnm-7cj` this plan leaves.
- **Relay media through the broker.** Deleted with decision 13; not
  coming back.
- **Per-runner ports.** The edge routes by path; nothing is published per
  service.

## 4. Implementation record

Filled in as commits land.

## 7. Open — the operator's

**Who issues the member's name and certificate.** Options: (a) the member
brings a domain and a cert (this plan's floor); (b) the pool issues
`<host-id>.members.<pool-domain>` — the controller serves the zone (or
delegates it) and the agent runs ACME against it; (c) a pool-run edge in
front of members, which reintroduces a relay and is rejected on decision
13's reasoning. (b) is what zero-touch onboarding needs and it commits the
pool to running DNS. Decision needed before members can be public without
operator hands.

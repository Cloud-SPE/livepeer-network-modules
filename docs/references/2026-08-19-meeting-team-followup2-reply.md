# Reply to the meeting-team second follow-up — runner conformance

Date: 2026-08-19. Response to `upstream-migration-followup-2.md`.

## Short answer

**Yes, we want it, and yes, upstream.** You should build the first cut,
and we should do the enabling work and the review. Reasoning below,
including one correction to your sizing assumption that will cost you
more than your note assumes.

You are right about the underlying problem and you have stated it more
precisely than our packet did. Our line — "point it at a broker
configured for your capability and your stack is testable against the
same scenarios" — was true of the broker half of your stack and we
should not have implied the other half. The suite certifies brokers.
Every obligation you list is real, normative, and currently unexecutable.

## Why you should build the first cut

Not because it is your problem to carry — because of a fact about our
repo that makes us the wrong first implementer:

**There is no runner implementation in this repository.** Only test
doubles. If we built a runner-conformance mode here, the only thing we
could run it against is our own fake session runner — a suite designed
around a fake, validating that fake. It would pass immediately and prove
nothing, and the first real runner to meet it would find everything we
got wrong.

You have the only real runner. A runner-conformance suite written
against a real implementation and then generalised is worth considerably
more than one written against a mock and then discovered to be wrong.

Our side of the deal:

- **The enabling refactor is ours.** `Ctx.Runner` is the concrete
  `*fakes.SessionRunner` today; the runner side needs to become a seam
  so a real runner can be substituted. That is our harness architecture
  and we should not hand you a refactor of our code as the price of
  contributing.
- **The shape is agreed up front**, not litigated in review: `--runner-url`
  substitutes the fake, the suite still owns the broker (auto mode), and
  runner scenarios live beside the broker ones in the same report.
- **We review and merge it**, and it ships as part of the suite so the
  next runner author — `rtmp-hls`, `trickle-egress`, `scope-passthrough`,
  or something not yet written — gets it for free. That was your reason
  for offering and it is the right one.

## One correction to your sizing

> The scenarios largely exist already; what changes is which side is
> substituted.

This is optimistic, and since you asked us to help you size it: **18 call
sites in the current scenarios drive the fake runner directly** — posting
events on its behalf, inspecting what it captured (`LastCreate`,
`Terminated`). None of that survives substitution, because a real runner
posts its own events and cannot be introspected.

What carries over is the harness, not the scenario bodies: the `Ctx` and
HTTP helpers, the PASS/FAIL/SKIP report, config generation, and the
broker-under-test lifecycle including restart control. Budget the
scenario bodies as new work.

The good news is that the assertions get *simpler*, because the broker
becomes your oracle. A runner-facing suite mostly drives the product flow
and asserts on what the broker observed:

| Runner obligation | How a runner suite asserts it |
|---|---|
| Create envelope + four-key partition, `schema` matching the offering | The broker already validates. The scenario's job is to **surface the rejection reason** — that diagnostic is most of the value you are asking for. |
| Grants honoured at both operations, scoped to the room | Call `mint_url` and `status_url` for real with the grant secret; assert both succeed and that a foreign room is refused. |
| Event envelope: unique `event_id`, monotonic `sequence`, cumulative `usage.total` in the declared unit | Observe effects through broker status: totals advance, unit matches, no protocol error. A runner violating uniqueness or monotonicity produces visible symptoms. |
| Liveness | Let the session sit past `interval × missed_threshold` and assert it is still active — that proves the runner emits. |
| Terminate idempotency; grants refused after end | End the session, then probe the runner's own status path (the suite configured it, so it knows it) and re-call a grant operation expecting refusal. |

Two obligations are hard to assert black-box and are worth naming as
limits from the start rather than discovering later: `event_id`
uniqueness and `sequence` monotonicity are only observable through their
symptoms, since the events go to the broker and not through the suite.
We would rather the runner README say so than have it imply coverage it
does not have — same discipline as the "what this suite does not cover"
section we just added on the broker side.

## Sequencing

Nothing here blocks your descriptor and open-response migration, and we
would rather you finish that first. When you are ready to start on the
runner suite, tell us and we will land the harness seam before you need
it, so you are never blocked on our refactor.

## Housekeeping

Re-pinning to `9f91299` is fine. Note the branch has moved since: the
suite is now **32 passed, 0 failed, 0 skipped**, having closed the
remaining gaps against the specs' own conformance lists — including
per-schema public-by-contract fixtures for all four shipped schemas
(previously only `sfu-room` had them, so the framework's "a schema change
that moves a sensitive field into `public` fails conformance rather than
review" guarantee was not actually true for the other three), lease-expiry
and bounded-refill scenarios, and wire coverage for the control-WS
binding. Closing those found one more broker gap of the kind you have
been catching: `refill: bounded` was declared in the schema, advertised
by the registry, and never enforced.

# Reply to the meeting-team review of the migration packet

Date: 2026-08-19. Response to `upstream-migration-response.md`
(`lpmmeet-ct-09`, `lpmmeet-ct-10`).

Every point is accepted and applied. Nothing is deferred to a later
version except where noted, and one of your open questions was answered
by work that landed between your review and this reply.

## The defect: fixed

`runtime-descriptor.md` §2's example was tagged `sfu-room/v1` and
contradicted it three ways, exactly as you list. You were right that an
implementer copying it ships a room that fails on the second participant.

The fix goes further than retagging: the example is now a deliberately
fictional `example-runtime/v1`, with a note that the framework page
defines the *envelope*, not any workload's fields, and that the example
exercises optional features (`private`, a bounded `max_uses`) precisely
because real schemas often use none of them. A framework document that
names a real schema will drift from it again; one that names a fictional
schema cannot.

## `sfu-room/v1` — all four points applied (now 1.0.1-draft)

**2a — metering wording. You were right, and this was the most important
catch in your review.** The sentence said participant-minutes "are
computed from the gateway's own mint/TTL records," which contradicted the
offering's own `metering: runner-reported` axis. Your two over-billing
cases (a token minted for someone who never connects; a participant who
leaves at minute three of a five-minute TTL) are exactly right, and they
are not edge cases — they are ordinary.

The schema now says plainly: **the runner is the usage authority**, and
mint/attach records are the first-party **cross-check** the dual-meter
model calls for — what the gateway compares claims against for divergence
detection, and what it *may* choose to bill its own customers from. That
choice belongs to the gateway; the schema does not dictate it. Thank you
for pushing on this; the wording would have taught the next implementer
the wrong thing.

**2b — `status_url` had no credential path. Correct, and fixed as you
proposed.** `room-status` is now a second operation on the same grant,
with the reasoning recorded in the schema: a verifiability hook the
gateway cannot authenticate against is not a hook. One grant, two
operations, both scoped to the room. No need for you to draft it — but
review the wording, since it's your schema.

**2c — TTL.** Adopted as a normative ceiling: participant-token TTL
SHOULD NOT exceed 300 seconds, with your rationale recorded (a token is a
join credential; revocation happens by room removal, not expiry; a short
TTL bounds only the join window).

**2d — immutability and failover.** On the record, in the spec itself:
"in v1 a runtime host migration is a customer-visible session end. A
capability that cannot tolerate that needs the relocation event, which
means a v1.1 — not a descriptor mutation smuggled in behind an immutable
field." Agreed as a deliberate shared constraint.

## Units: `participant_seconds`

Both requests applied.

- `sfu-room.md` no longer names a unit in its typical-axes line. It now
  states that the work unit is an **offering property, not a schema
  property**, and that `participant_seconds` and `participant_minutes`
  are equally valid — nothing in the schema depends on the choice.
- The conformance suite takes `--job-unit` and `--session-unit`. **We ran
  the full suite at `--session-unit participant_seconds`: 20 passed, 0
  failed.**

Your two consequences are correctly identified and we have nothing to add
to them: per-second `price_per_unit_wei`, and runway magnitudes 60× larger
with `runway_seconds_estimate` unaffected.

## Your two observations

Both correct, and both now stated in the spec rather than left to be
rediscovered:

- **Any accepted event refreshes liveness.** `paid-session/v1` §5 now says
  so explicitly: a `session.usage.tick` satisfies heartbeat enforcement, so
  a runner already reporting usage inside `interval × missed_threshold`
  needs no separate `session.heartbeat` emitter. Your 30-second tick is
  fine against any sane interval.
- **Your envelope extras are fine — keep them.** §7.2 now states that
  unknown event-envelope fields are tolerated and ignored, so runners may
  carry their own correlation fields without coordinating a spec change.
  Note the distinction for `usage.delta`: it is ignored *by rule*, not
  merely unread — cumulative `usage.total` is the only debit basis.

## The conformance skips: no longer skips

Your note is the one item your review has overtaken. Between your review
and this reply, both scenarios were implemented for real:

- **`paid-session/restart-rebind`** restarts the broker mid-session (auto
  mode owns the process) and asserts what §9.2 actually requires — either
  branch of rebind-or-terminal — with the forbidden outcomes checked in
  both: no second `work_id`, no silently skipped usage, no runner left
  serving. On the rebind branch it also proves the pre-restart event is
  still a duplicate, the next sequence is accepted, and top-up and end
  still work.
- **`paid-session/heartbeat-enforcement`** opens against a fast-heartbeat
  offering, sends nothing, and waits for the teardown — asserting the
  reason is `heartbeat_lost`, that the *runner* was actually terminated
  rather than a record flipped, and that a late `end` doesn't rewrite the
  close reason.

Suite is **20 passed, 0 failed, 0 skipped**. So: cite the wire suite, not
our engine tests. Your protocol-acceptance criteria for B4 and B5 are met
by scenarios you can run yourself against your own stack in URL mode.

Worth knowing *why* this matters beyond the paperwork: implementing those
two scenarios immediately found two defects and one unimplemented spec
option, all now fixed — (1) rebind never re-asserted the payee-side
payment session, so the first debit after a restart failed and the runner
retried forever, which also meant the broker could not survive a payment
daemon restarting independently; (2) winddown precedence was undefined
and lease won, so a dead runner reported `lease_expired` and sent
operators to funding instead of the runtime — `heartbeat_lost` now takes
precedence; (3) `lease.policy: fixed` was declared in the manifest schema
and never implemented. Your instinct that B4 and B5 deserved wire
coverage was right.

## Timeline: build against the branch, no tag

We are not cutting a tag for this. Work against
`tasks/refactor-interaction-modes-and-billing` directly — it is stable
enough for what you need, and the whole of it (specs, descriptors,
reference broker, conformance suite) is there to integrate and run
against.

If you want the immutability your protocol-fit decision relied on, pin
the SHA you build against on your side rather than waiting for us to
name one — that gives you exactly the property you wanted (a fixed
reference that a moving checkout cannot be mistaken for) without
blocking on our release cadence. The current head as of this reply is:

```
a41ff72e7047ef16dcea6ad348608765b07761c3
```

That commit contains every change described in this document. Later
commits on the branch will be additive against the contracts you
reviewed; if something on it turns out to break a contract you have
built against, that is a bug on our side and we want to hear about it.

## Your side

Your blast-radius list matches our understanding, including the happy
detail that the session `credential` removes a reattach mechanism you had
planned to build — that mechanism existing twice, in two gateways, is
part of what motivated the redesign.

Two notes on your list:

- Your runner-side lifecycle, metering, durable outbox, and restart
  recovery being already implemented is the reason your remaining work is
  days rather than weeks: `paid-session/v1` deliberately did not invent
  new runner obligations beyond the event envelope and the descriptor.
- Folding `gateway_control_grant` into `runtime.grants[]` is the right
  read. Remember grants are delivered by open **exactly once** and never
  by status, and are never re-minted on rebind — a gateway that loses one
  has lost it for that session.

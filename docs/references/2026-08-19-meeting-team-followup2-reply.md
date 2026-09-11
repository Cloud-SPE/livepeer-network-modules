# Reply to the meeting-team second follow-up — runner conformance

Date: 2026-08-19. Response to `upstream-migration-followup-2.md`.

## Short answer

You identified a real gap and we are closing the half of it that is
ours. **We are not going to host a runner-certification harness**, and
we think you should build yours in your own repo. What we owe you
instead already exists as of this reply: a single normative statement of
what a runner must do.

You were right that our packet oversold the suite. "Point it at a broker
configured for your capability and your stack is testable against the
same scenarios" was true of the broker half of your stack, and we should
not have implied the other half. Every obligation in your list is real,
normative, and was not collected anywhere.

## Why not here

Two reasons, and the second is the one that would bite you later.

**This repo has no runner.** Only test doubles — runners live outside it
by design, and the spec repo says as much ("Not a code library.
Implementers conform to the specs here"). A runner-conformance mode built
here could only be exercised against our own fake session runner: a suite
designed around a fake, validating that fake. It would pass on day one
and prove nothing.

**A harness we cannot exercise will rot.** It would be written against
your runner, then drift as the protocol moves, and the next runner author
would trust a stale suite. A stale conformance suite is worse than no
suite, because it launders non-conformance as evidence. We just spent a
round adding a "what this suite does not cover" section precisely to stop
a green run from implying more than it proves; taking on a harness we
cannot run would undo that discipline in a bigger way.

Note also what the existing suite actually is: it verifies *our reference
broker* against the spec, and URL mode is a generous side effect. It was
never a certification service, and runner conformance would make it one.

## What we owe you instead, and it is done

Runner obligations were scattered across `paid-session/v1` §7.1, §7.2,
§7.3 and the descriptor framework — which is why you had to assemble the
list by hand. That was the actual defect, and your note is the evidence
for it.

**`paid-session/v1` §11 — "Runner obligations: the implementer's
checklist"** now collects them in one place: session creation, grants,
usage events, termination, and the three things a runner never does (talk
to the payment layer, set price, treat its own claims as the buyer's
billing truth).

Each row carries the **failure signature** — what the broker does when
you get it wrong. That is the new information and the thing a harness
would otherwise have to teach you the slow way. A sample:

- `usage.unit` not matching the offering's declared unit is a protocol
  error that advances nothing, so it rejects **every** usage event for
  the session's lifetime — the single most common integration failure,
  and exactly the class you flagged with `participant_seconds`.
- An event at or below the committed sequence watermark is treated as a
  duplicate and acknowledged without effect.
- Giving up instead of retrying a `5xx` loses that usage permanently: the
  exactly-once contract deliberately leaves the event uncommitted so your
  retry completes it.
- A grant honoured after the session is terminal is an unmetered runtime,
  and the broker cannot catch it — expiry is a backstop, not the lifetime.

The section is derivative by construction: the numbered sections govern
on conflict, so it cannot drift into a competing contract.

## On you building it

Build it in your repo, and we will link it from the conformance README so
the next runner author finds a working reference rather than repeating
the work. That gets your stated goal — "we would rather the next runner
author not repeat it" — without either of us maintaining something we
cannot run.

One correction to your sizing, since you asked us to help you scope it:

> The scenarios largely exist already; what changes is which side is
> substituted.

Optimistic. **18 call sites in the current scenarios drive the fake
runner directly** — posting events on its behalf, inspecting what it
captured. None survive substitution, because a real runner posts its own
events and cannot be introspected. What carries over is the harness, not
the scenario bodies: the context and HTTP helpers, the PASS/FAIL/SKIP
report, config generation, and the broker-under-test lifecycle.

The good news is the assertions get simpler, because the broker becomes
your oracle. Most runner obligations are observable through it: a bad
descriptor is rejected with a specific reason (surfacing that reason is
most of the diagnostic value you want), usage claims show up in broker
status in the declared unit, and a session that survives past
`interval × missed_threshold` proves the runner is emitting. Grant
honouring is the part you must test directly — call `mint_url` and
`status_url` with the grant secret and assert both work and are scoped.

Two obligations are not black-box assertable at all, and we would rather
you plan around them than discover them: `event_id` uniqueness and
`sequence` monotonicity are only visible through their symptoms, since
those events go to the broker rather than through your harness.

If it turns out to generalise across several runners, come back to us and
we will revisit hosting it — with evidence rather than a guess.

## A related change you should know about

Your question sent us looking at a broader version of the same problem,
and it turned up something that affects you directly:
`host-config.yaml` makes an **operator** hand-transcribe facts only the
**runner** knows — `descriptor_schema`, `work_unit.name`, your own
create/status/terminate paths, transports, metering. Stating the same
fact twice is why the broker has to cross-check it at runtime, and
`usage_unit_mismatch` exists only because the unit is declared in two
places that can disagree. That is the mechanism behind the
`participant_seconds` hazard you raised, not just an instance of it.

We are designing toward runner self-description
(`docs/design-docs/runner-declared-capabilities.md`): the broker asks
your runner what it implements, and host-config shrinks to price,
capacity, and policy — the things an operator legitimately owns. There is
a constraint that shapes it, which we mention because it will look like
caution from your side: those facts flow into a cold-key-signed manifest,
so they cannot be auto-adopted; a runner-side change has to surface as
drift an operator acknowledges, or a runner could silently alter what an
orchestrator advertises and sells.

Nothing there changes what you are building now.

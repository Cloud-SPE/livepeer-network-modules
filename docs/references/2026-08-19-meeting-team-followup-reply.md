# Reply to the meeting-team follow-up

Date: 2026-08-19. Response to `upstream-migration-followup.md`.
Short: two items, both now closed.

## 1. `restart-rebind`: you were right, and we fixed the coverage rather than the claim

Your reading of the run is exactly correct. Auto mode's payment layer is
the broker's in-process mock; it restarts *with* the broker, loses its
state, `OpenSession` returns `AlreadyOpen=false`, and every restarted
session took branch 2. The rebind assertions were written and never
executed. Our previous reply claimed coverage the run did not deliver.

You offered us the accurate-statement option. We took the harness option
instead, because the root cause was a modelling error on our side, not
just a wording one: **an amnesiac payment layer models a daemon that does
not exist.** The real payee daemon persists to BoltDB and survives a
broker restart; our mock did not, so the suite was testing an
unrealistic configuration and calling it recovery.

What changed:

- `payment.Mock` takes optional persistence
  (`payment_daemon.mock_state_path`). Auto mode sets it, so the ledger
  now survives the restart the way the real daemon's store does.
- **`paid-session/restart-rebind` now requires the rebind branch.** A
  broker that goes terminal there is failing recovery, not selecting the
  other branch. The assertions you quoted — pre-restart event still a
  duplicate, next sequence accepted, top-up and end still working — now
  actually execute.
- **New `paid-session/restart-terminal-when-unbillable`** forces the
  other branch deterministically: it wipes only the payment ledger and
  leaves the broker's own session store intact, which is precisely the
  "runner still has it, payment layer does not" case. It requires the
  terminal outcome and checks the forbidden outcomes there too.

Suite is **21 passed, 0 failed, 0 skipped**.

So the answer to what you cite for B5 is unchanged from our last reply,
but now it is actually true: cite the wire suite. Both halves of §9.2 —
the operationally important one included — are demonstrated by scenarios
you can run against your own stack.

One consequence for you in URL mode: **the branch you get depends on
your payment layer, not on your broker.** If your payment daemon does
not outlive your broker restart, you will see the terminal branch and
`restart-rebind` will fail — correctly. The README now says this
explicitly.

## 2. `status_url` versus `status_path`: confirmed, and now in the spec

Your reading is the design intent. They are two endpoints, two callers,
two credentials:

- the broker's configured `session.runner.status_path`, called by the
  broker with operator-configured credentials;
- the descriptor's `status_url`, called by the gateway with the grant
  secret.

Nothing requires them to be the same endpoint. Your plan — a separate
gateway-facing room-status endpoint published as `status_url`, with your
existing session-status endpoint left authenticating the broker's control
token — is exactly right.

It no longer relies on reading the design correctly. It is stated in
`runtime-descriptor.md` as the general principle (descriptor coordinates
are gateway-facing and independent of the broker's backend
configuration, and a runner whose broker-facing endpoint authenticates
only the broker SHOULD expose a separate gateway-facing one) and in
`sfu-room/v1` on the `status_url` row, now at 1.0.2-draft.

## Note on your pin

You pinned `a41ff72`, which predates both of the above — the `status_url`
wording landed at `5c59f74` and the restart work after it. Neither
changes any contract you reviewed: one is a documentation clarification
of behaviour that was already intended, the other is test-harness and
conformance work plus an opt-in mock setting. Your pin remains a valid
integration reference; pick up a later SHA whenever you want the
clarified wording and the fuller suite.

Nothing here blocks your descriptor and open-response migration.

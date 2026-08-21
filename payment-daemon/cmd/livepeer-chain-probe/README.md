# chain-probe — exercise the paid path against a real chain

Every payment defect found so far was invisible to unit tests,
conformance and dev deployments, because all three run against a mock
payment client. A mock credits what it is told to credit and never
closes a session it should not close, so it cannot show:

- a session that bills **zero** because pricing was discarded;
- a price **nobody signed** being used to bill;
- work served against an **empty balance**;
- only the **first** job on a shared payment session ever billing;
- a ledger and a signed settlement **disagreeing** about the same job.

All five were real. All five were found here.

## Cost of a run

Minting a ticket does not move money — a ticket is a signed lottery
claim. Value moves only when one **wins** and the payee redeems it,
which costs the payee gas and draws the ticket's face value from the
payer's deposit.

At the daemon's defaults (face value 0.001 ETH, win probability 1/1024)
a run costs, in expectation, a fraction of a cent, with a 1-in-1024
chance per ticket of actually costing 0.001 ETH. Small, real, and worth
knowing before you type the command.

**This is deliberately not part of `make test` or CI.** It spends real
value and needs real keys. A check that runs by accident against mainnet
is worse than no check.

## Running it

Bring up a payer, a payee and a broker against the same chain, then:

```
go run ./cmd/livepeer-chain-probe \
    --recipient=0x<payee address> \
    --protocol=both
```

Flags worth setting deliberately:

| Flag | Why |
|---|---|
| `--per-units` | **Keep it above 1.** At `per_units: 1` flooring and ceiling agree, so a rounding defect cannot surface — which is exactly how one shipped. |
| `--price-wei` | Pick a price whose product with the unit count leaves a remainder. |
| `--protocol` | `job`, `session`, or `both`. |

## Run it twice

The second run is the one that matters. Several defects only appear on
the **second** exchange over one ticket session — the first bills
correctly, which is precisely the request an operator tests before
declaring victory. A single green run proves less than it looks like.

Restarting the daemons between runs wipes the ticket session and hides
exactly these bugs. Don't.

## What it asserts

Not log lines — money:

- the payment credited something (a zero-EV payment funds no work);
- the ledger balance moved by **credit minus bill**, with the bill
  recomputed here from the normative rule rather than imported from the
  broker, so a wrong implementation cannot agree with itself;
- the settlement is reachable without reading an HTTP trailer;
- the settlement is **signed**, names its own exchange, and carries an
  RFC3339 `issued_at`;
- for sessions: the `work_id` is the payment's, usage debits, a top-up
  replay returns the recorded outcome rather than funding twice, and the
  runner is terminated at end.

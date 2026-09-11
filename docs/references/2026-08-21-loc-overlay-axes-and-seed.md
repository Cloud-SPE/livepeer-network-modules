# To LOC — pin axes fixed, settlement delegation deliberately not

Date: 2026-08-21. Branch `tasks/lpm-v2`, head `05e4d96`.

Both halves of your report are right. They need different answers: one is
a bug we fixed, the other is a boundary we think should hold — and the
thing you actually need sits behind a third door we have now opened.

## 1. Pin axes — a bug, fixed

`convertPin` parsed `rawPinCapability.Extra` and never copied it into
`types.Capability`. Confirmed exactly as you describe: overlay-pinned
routes reached `Select` with their declared compatibility axes missing, so
a consumer could see the capability and the price and still not know
whether it could speak to the route.

Parsed-then-discarded is the worst of the three possible behaviours — the
operator writes axes, the config validates clean, and the route goes out
without them. Fixed in `05e4d96` with a test that fails on the old code.

While in there we removed the per-offering `warm` field, which had the
same shape: accepted by strict parsing and thrown away. It was removed
from the manifest schema in the v3.0.1 reset and only the overlay parser
kept accepting it. It now fails at config load. If you have overlay
fixtures carrying `warm:`, they will need it deleted — sorry for the
churn, but silence was worse.

## 2. Settlement delegation through overlay pins — no, and this is not a gap

We are not going to project `settlement_keys` through overlay pins, and we
want to be explicit about why rather than leave it looking like something
we have not got to yet.

Overlay pins are operator-asserted and the resolver marks them `unsigned`.
`settlement_keys` say *which hot keys are authorized to sign settlement
records*, and the authority for that claim comes from the orchestrator's
cold key signing the manifest that lists them. A YAML file on the resolver
host asserting the same thing establishes nothing — it is precisely the
claim the signature exists to make. Projecting it would mean anyone with
write access to a resolver's overlay file could name the keys your
clearinghouse trusts, which is the property you are relying on the
signature for in the first place.

So, to answer your question directly: **overlay-only is not intended to
support complete paid-v2 routes.** It is right for routing and capacity
curation, and wrong for anything you will settle against. We would rather
say that plainly in the docs — which we now do — than let it look
supported and have you discover the trust gap later.

## 3. The hermetic signed path — `--chain-seed`, new in `05e4d96`

This is your "otherwise" branch, and you were right that there was no
supported answer. There is now.

`--chain-seed` preloads the in-memory chain (so it requires `--dev`) with
address → serviceURI pairs:

```yaml
# seed.yaml
seed:
  - eth_address: "0xabc0000000000000000000000000000000000000"
    service_uri: "http://127.0.0.1:9099/.well-known/livepeer-registry.json"
```

```
livepeer-service-registry-daemon --mode=resolver --dev \
  --chain-seed ./seed.yaml --socket /tmp/registry.sock
```

Serve a **signed** manifest at that URI from any static file server. The
resolver then takes the ordinary well-known path: fetch, canonicalize,
verify the signature, project `settlement_keys` onto every node from that
address. Nothing is stubbed except where the serviceURI came from — which
is the one thing a chain-free run genuinely cannot have.

That gives nightly CI the full chain you need: signed manifest → delegated
`settlement_keys` → broker signs settlements with the delegated key → you
verify. No chain, nothing unsigned, and no special-casing in your verifier.

Guardrails, both of which exist because a silently-ignored seed is worse
than a startup failure:

- `--chain-seed` without `--dev` is refused — a real chain provider
  replaces the in-memory one, so the seed would be read, validated, and
  ignored while CI quietly resolved against mainnet.
- `--chain-seed` with `--discovery=overlay-only` is refused —
  overlay-only never reads the chain, and overlay pins are unsigned, which
  is the reason to reach for a seed at all.

To produce the signed manifest, run the daemon in publisher mode, or see
`examples/minimal-e2e`, which builds and signs one in-process — it is
close to the fixture generator you probably want for CI.

Documented in `service-registry-daemon/docs/operations/running-the-daemon.md`
§"Hermetic runs that need signatures".

## 4. What we would like back

If `--chain-seed` does not fit your CI shape, tell us what does before you
build around it. Two things we guessed at:

- **A file, not flags.** We assumed CI wants a checked-in fixture rather
  than repeated `--seed addr=uri` arguments. Easy to add the flag form if
  you would rather not manage a file.
- **`--dev` as the gate.** It is the existing switch for "in-memory
  providers", so the seed rides on it. If you need seeding in a
  non-`--dev` resolver for some reason, say so — but we would want to
  understand the case first, because the refusal above is deliberate.

## 5. Related, since it lands in the same area

The conformance suite now runs **signed**: a delegated secp256k1 key per
run, with scenarios asserting the settlement recovers to it and that a
tampered payload does not. Verification uses the
`livepeer-network-protocol/verify` module — the same code we would expect
you to verify with, so the grader and the clearinghouse run one
implementation rather than two that agree until they don't.

That work also turned up that the suite's payment envelope was an opaque
stub, so no settlement record was ever built and the suite had never
graded a settlement's *content* at all. Fixed in `dcb8cc8`.

Reference: `05e4d96` (this), `dcb8cc8` (signed conformance), issues
**lnm-7qw** and **lnm-lc2**, both closed.

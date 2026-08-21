# To the OpenAI gateway team — all four were real

Date: 2026-08-21. Branch `tasks/lpm-v2`, head `dcb8cc8`.

You reviewed `81958a3`. Everything below is at `dcb8cc8`; the branch has
moved a fair way since, including three changes that affect you directly
(§6).

Thank you for these. All four questions found something, and #2 and #3 were
defects rather than ambiguities.

---

## 1. Replay semantics

**Your option 3, and it is now normative:** accounting idempotency only.
`paid-job` 1.0.7-draft §4.1.

A replay returns the recorded status, `Livepeer-Work-Units`,
`Livepeer-Work-Unit` and `Livepeer-Job-Id`, and the settlement stays
retrievable at `GET /v1/settlement/{id}`. It does not re-execute the
backend, does not debit twice, and does not reproduce the response body.

We considered replaying the body and rejected it. Doing so obliges every
broker to durably store every customer response — inference output,
transcripts, generated media — for the whole idempotency window, which
turns a metering component into a customer-data store with the retention,
encryption and deletion duties that follow. That is a large liability to
put on all operators to cover a response lost in flight.

So we have written the consequence down rather than leaving it implied:

> a caller whose original response was lost cannot recover the result
> through this protocol. It has paid for work it cannot see.

The replayed exchange is marked as such precisely so you can tell that
case apart from a fresh result and treat it as a failed delivery of a
*charged* exchange. What to do then is yours: surface the charge, absorb
it, or re-submit under a new request id and pay again.

You said you do not want to compensate by persisting every OpenAI response
in the gateway, and we are not asking you to — we are saying the protocol
does not recover that result either, and that neither side should quietly
assume the other is holding it. If response retention turns out to be
worth it for your offerings specifically, it is available as an
offering-level operator feature; it is deliberately not the default.

## 2. Final debit failure

**A real defect, fixed.** `paid-job` 1.0.7-draft §5.2.

You are right that the middleware logged the failure and carried on. Worse
than that: the settlement then attested the *measured* units, which made a
broker whose ledger call failed byte-indistinguishable from one that was
paid. LOC would have booked revenue that never moved, and the failure was
invisible exactly when it mattered.

Now, on a failed final debit:

- `debited_units` reports what the ledger actually took — usually zero, or
  the interim ticks that did succeed on a long exchange;
- `billed_value_wei` reports the value that actually moved;
- `actual_units` still reports the measurement (the measurement is not in
  doubt, the payment is);
- the record carries a new outcome, **`DEBIT_FAILED`**, and a consumer MUST
  NOT treat it as settled.

That is the fail-closed terminal accounting state you asked for, and
`DEBIT_FAILED` is the machine-readable thing LOC gates on.

**One part is not fixed, and you should know before you build.** On a
`unary` exchange the broker commits `Livepeer-Work-Units` in the response
headers *before* the debit runs, so that header can still name units a
subsequent debit fails to take. The settlement is authoritative where the
two disagree. Making the header equal debited units always means debiting
before header commit, which inverts the current middleware/handler
boundary — tracked as **lnm-y08** and stated in the spec so it is not
something you find in production.

If you would rather have durable debit retry than the ordering change, say
so; we picked fail-closed first because it is the part still recoverable
once work has shipped, and because a retry queue that can also exhaust
needs the fail-closed state underneath it anyway.

## 3. Backend error metering

**Also a real defect, fixed.** Non-streaming, non-2xx now claims zero
regardless of extractor configuration. `paid-job` 1.0.7-draft §5.

Your diagnosis was exact: the fixture reached zero only because
`openai-usage` finds no usage object in an error body. A request-derived
extractor — a formula over an image `n`, a per-request constant for rerank
— returned its full count for a request the backend never served. The unit
that decides whether work was billable cannot be the one that never looked
at the outcome.

We went slightly wider than your question: the rule is non-2xx, not only
5xx. A 4xx produced no billable output either.

Streaming is untouched, as you proposed — partial output stays billable as
measured, and how partial is billable is what the extractor declaration
exists to decide.

## 4. Extractor plan

| Product | Unit | Extractor | Status |
|---|---|---|---|
| Chat, embeddings | tokens | `openai-usage` | available |
| Images | count from request `n` | `request-formula`, `fields: {n: "$.n"}` | available |
| Rerank | 1 per request | `request-formula`, constant expression | available |
| TTS | Unicode code points | `request-formula` + **`text_fields`** | **added, `dcb8cc8`** |
| Transcription | input audio seconds | see below | **open, lnm-4xh** |

**TTS.** `request-formula` gained `text_fields`, which resolves a string
path to its Unicode **code point** count:

```yaml
work_unit:
  name: characters
  extractor:
    type: request-formula
    expression: "chars"
    text_fields: { chars: "$.input" }
```

Code points, not bytes and not UTF-16 units: it is what a
character-priced offering means by "character", and the only count that
gives the same number for the same text however it was encoded. A byte
count charges twice as much for Greek as for English and three times for
most CJK. `text_fields` is declared separately from `fields` so the
string→number coercion is a choice in the manifest and not a silent
fallback — a path naming a string where a number was meant falls to the
default rather than quietly billing its length.

**Transcription duration is the one we cannot close unilaterally**, and we
would rather ask than guess. No extractor measures audio duration: it needs
either container parsing in the broker, or a measurement from something
that already decoded the audio.

Our proposal is the second: your transcription runner emits the duration it
already knows in a response header, and the offering declares
`response-header` (or `response-trailer`) as its extractor. That keeps
metering seller-side, needs no container parsing, and — unlike reading
`duration` out of a `verbose_json` body — does not mutate the caller's
request or the response shape they see. Given you are removing forced
`stream_options.include_usage`, we assume you do not want us adding a
forced `response_format` in its place.

**Can your transcription runner emit that header?** If not, the fallback is
a `multipart-audio-duration` extractor that parses the uploaded container,
which we can build but would rather not if a header will do. Either way it
is tracked as **lnm-4xh** and we will have it for the cutover — we need
your answer to know which one to build.

## 5. Signed end-to-end conformance

**Agreed, and the suite now runs signed.** This was the right thing to push
on.

The conformance run generates a delegated secp256k1 key per run, configures
the broker with it, and two new scenarios assert:

- `paid-job/settlement-signature-verifies` — the settlement recovers to the
  delegated key;
- `paid-job/tampered-settlement-fails-verification` — a record with altered
  units does not.

Verification uses the `livepeer-network-protocol/verify` module, which is
the same code LOC should verify with, so the grader and the clearinghouse
run one implementation rather than two that agree until they don't. Suite
is **39/39**.

Getting there turned up something worse than the unsigned run: the suite's
payment envelope was an opaque stub, so no settlement record was ever
built, and the suite had **never graded a settlement's content at all** —
not the fields, not the arithmetic, not the signature. There is now a real
envelope helper for the scenarios that need one.

On the wider run you propose — broker, registry delegation, LOC
verification, gateway settlement — we are in. One scoping note: the
delegation half is manifest-side, not broker-side. The broker signs with
the key it is given; what makes that key *authorized* is the orch cold key
publishing it in the signed manifest's `settlement_keys` with a validity
window, and consumers accepting a record whose `issued_at` falls in that
window. The conformance suite covers the broker's half. The manifest half
needs the publisher and LOC in the room, which is exactly the run you are
asking for. Propose a date and we will bring a broker with a real
delegation.

## 6. Three things that changed since `81958a3`

Worth reading before you retrofit, since two touch surfaces you listed:

1. **`request_id` is now in the signed job settlement** (field 27). LOC
   asked for it: `job_id` is broker-minted and `work_id` is shared across
   every job on a ticket session, so neither binds a record to LOC's
   durable job. Since you are consuming the LOC-provided
   `Livepeer-Request-Id`, this binds end to end — but note the broker
   generates one when the caller sends none, so the binding is only as good
   as the id you send.
2. **`GET /v1/settlement/{id}` resolves more keys** — job id, session id,
   `gateway_session_id`, and `work_id` — and an ambiguous `work_id` now
   answers `ambiguous_identifier` (409) instead of returning whichever
   session sorted last. Your use of the endpoint keyed by `jobId` is
   unaffected.
3. **The `Livepeer-Settlement` trailer is no longer advertised on unary
   responses.** It never worked there — trailers ride only on chunked
   responses, and a unary job carries Content-Length, so net/http dropped
   it silently while the response still named it in `Trailer:`. It is
   delivered on streamed exchanges and retrievable from the query surface
   on both. Since you are using the query endpoint because Node Fetch does
   not expose trailers reliably, this only removes a header that would have
   misled you.

## 7. References

| Item | Where |
|---|---|
| All four fixes + signed conformance | `dcb8cc8` |
| Replay semantics | `paid-job` 1.0.7-draft §4.1 |
| Debit failure | `paid-job` 1.0.7-draft §5.2, outcome `DEBIT_FAILED` |
| Backend-error metering | `paid-job` 1.0.7-draft §5 |
| TTS `text_fields` | `capability-broker/internal/extractors/requestformula` |
| Unary header/debit ordering | **lnm-y08** (open) |
| Transcription duration | **lnm-4xh** (open, needs your answer) |
| Trailer fix | `59bbc27`, `paid-job` 1.0.6-draft §3.2 |
| `request_id` in settlement | `2c86707`, `paid-job` 1.0.5-draft §5.1 |

Two open questions back to you: **durable debit retry or the header
ordering fix** (§2), and **can your transcription runner emit a duration
header** (§4).

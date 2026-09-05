# OpenAI gateway — what to change

Target: `paid-job/v1` against the pilot broker. Everything below is live
on the pilot stack; the offering ids and prices are what it advertises
today.

## 1. Endpoint and headers

| Was | Now |
|---|---|
| `POST /v1/cap` | `POST /v1/job` |
| `Livepeer-Mode`, `Livepeer-Spec-Version` | `Livepeer-Protocol: paid-job/v1` |
| — | `Livepeer-Capability`, `Livepeer-Offering` (headers, **not** body) |
| — | `Livepeer-Request-Id` — **send the LOC-issued one** |
| — | `Livepeer-Payment` — base64 of the minted envelope |

`Livepeer-Request-Id` is the one that matters most: it is signed into the
settlement, and it is the only key you can look an exchange up by later.
If you let the broker generate one, the field still populates and binds
to nothing you hold.

## 2. Transport

Pick with ordinary HTTP negotiation — `Accept: text/event-stream` for
streaming, `multipart/form-data` for uploads. The offering declares what
it supports in `job.transports`.

## 3. Reading the result

Consume `Livepeer-Work-Units`, `Livepeer-Work-Unit`, `Livepeer-Job-Id`.

**Do not rely on the `Livepeer-Settlement` trailer.** It is delivered on
streamed responses only, and never on unary — a unary response carries
Content-Length, and trailers require chunked, so the transport drops it.
We stopped advertising it there rather than leave you waiting on
something that never arrives.

Use `GET /v1/settlement/{jobId}` on every transport, or the request-id
lookup below.

## 4. What to delete

- mode-mismatch retries
- fresh-job retries
- forced `stream_options.include_usage`
- the compensating `settle(0)` logic

That last one is replaced by idempotency: retry the same
`Livepeer-Request-Id` and you converge on the recorded outcome.

## 5. Retry and replay semantics

A replay returns the **accounting** outcome, not the backend body. It
will not re-execute, will not debit twice, and cannot reproduce a lost
OpenAI response. Treat a replayed exchange as a failed delivery of a
*charged* exchange — `upstream_response_lost`, as you proposed — and do
not automatically submit a second paid job.

## 6. Debit failure

`GET /v1/settlement/{jobId}` can answer `202` with
`Livepeer-Error: accounting_pending`. That means delivered, debit
outstanding, retry in progress — bounded at 10 attempts over 30 minutes.
It will reach a terminal state: a signed settlement if the debit lands,
`DEBIT_FAILED` if the budget is spent. Do not charge on a pending state.

## 7. Finding an exchange with only your own id

```
GET /v1/exchange/{request_id}
```

| Outcome | Status | Meaning |
|---|---|---|
| `SETTLED` | 200 | carries `job_id`, units, and the signed settlement |
| `ACCOUNTING_PENDING` | 202 | delivered, debit retrying |
| `IN_FLIGHT` | 202 | still running |
| `ADMITTED_OUTCOME_UNKNOWN` | 200 | admitted, produced no recorded outcome |
| `ADMITTED_EVIDENCE_EXPIRED` | 200 | admitted, record aged out of retention |
| `NO_RECORD` | 404 | this broker has made no claim either way |

`SETTLED` always carries a real signed settlement — it is never reported
from a terminal state alone.

## 8. Funding ceiling for transcription

JSON workloads have a ceiling in their own parameters. Multipart audio
does not, so the offering advertises how to compute one:

```json
"work_unit": {
  "name": "seconds",
  "estimator": {
    "id": "multipart-audio-duration/v1",
    "rounding": "ceil-to-whole-seconds",
    "exactness": "exact-or-reject",
    "fixtures": "livepeer-network-protocol/extractors/fixtures/multipart-audio-duration-v1"
  }
}
```

**You own the TypeScript implementation.** There is no package to
install: the offering advertises no `package`, because there is no
canonical client library any more. What binds the two sides is this
triple plus the fixtures at `fixtures`, which your implementation and the
broker's extractor both run.

Two rules the fixtures enforce, worth stating outright because getting
either wrong is a funding bug rather than a test failure:

- **Ceiling, never an estimate.** Refuse rather than return a bitrate
  guess — in practice this only bites on headerless MP3. Treat the
  refusal as "not fundable" and decline the upload. A ceiling that reads
  low underfunds real work; one that reads high overcharges.
- **Whole seconds, rounded up.** `ceil-to-whole-seconds`, always.

Run the fixtures in your own CI. A disagreement between your
implementation and ours is then a failing test on your side rather than a
settlement that exceeds the ceiling you funded.

Refuse an estimator `id` you do not implement rather than guess a
ceiling. And never treat your local estimate as settlement evidence: it
bounds what may be spent; the signed settlement says what was.

## 9. Persist

LOC job id, broker request id, broker job id, work unit, units, and the
signed settlement. The signed settlement is authoritative over the
immediate response headers wherever they disagree.

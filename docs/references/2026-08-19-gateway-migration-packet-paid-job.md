# Gateway migration packet — OpenAI + transcode → paid-job/v1

Date: 2026-08-19. For the teams behind `livepeer-modules-openai` and
`livepeer-modules-transcode-gateway`. Based on a read of both codebases on
2026-08-18; the workarounds named below are quoted from your source, not
invented as strawmen.

## The short version

The seven-mode interaction taxonomy is gone. One-shot work is now
**`paid-job/v1`** — one protocol with a transport dimension. Most of what
you built around the old contract exists because the contract was missing
three things: idempotent opens, a reliable usage-claim channel, and a way to
*request* a shape instead of guessing one. All three are now normative.

Nothing in this packet asks you to restructure your product. The changes are
at the broker seam only.

## What you can delete

| Workaround you built | Why it existed | What replaces it |
|---|---|---|
| **Mode-mismatch retry loop** (openai `loc/dispatch.ts`: settle 0 with `mode_mismatch`, re-open a fresh job, up to `LOC_JOB_RETRIES`) | The gateway knew the shape it needed but could only *check* what came back | The gateway names `Livepeer-Protocol: paid-job/v1` and negotiates transport per request. A mismatch is now a typed refusal (`protocol_transport_unsupported`) *before* any payment side effect — not a wasted job. |
| **Per-mode offering duplication** (`…-stream` offering variants so mode could be smuggled through offering choice) | Mode was part of the offering identity | One offering declares `job.transports: [unary, stream, multipart]`. Same offering, same price, three transports. |
| **`guessInteractionMode`** (transcode: local heuristic because the catalog carried no mode data) | Mode wasn't discoverable | `protocol` is a required manifest field and rides route metadata; there is nothing left to guess. |
| **Hand-rolled compensating settles** (both repos: mandatory `settle(0)` / `close(0)` on every failure branch; `doJSONRetry` banned on open paths; a durable settle queue in one repo and a janitor in the other; reliance on `409 job_already_settled`) | Opens were not idempotent, so a timeout could mean *anything* | `Livepeer-Request-Id` is a required idempotency key with normative replay semantics: retry the identical request and converge on the recorded outcome — same status, same claim, same `Livepeer-Job-Id`, no second backend execution, no second debit. An in-flight retry gets `job_in_flight` (retryable); reuse with different content gets `request_id_reuse`. |
| **Client body mutation** (openai: rewriting `stream_options.include_usage = true` so a usage frame appears) | The wire claim was unreliable, so usage had to be scraped from the body | `Livepeer-Work-Units` is on **every** terminal response — including errors, as `0` — delivered as an advertised HTTP trailer on streams. `Livepeer-Work-Unit` echoes the unit name so you can reject drift locally without a registry round-trip. |
| **Dummy `RouteCandidate`** (openai: fabricated struct with empty `ethAddress`, `pricePerWorkUnitWei`, `quoteId`, empty fingerprints) | Daemon-era audit code needed fields the new flow no longer produced | Not fixed by this work — it is a clearinghouse-shape issue. See the LOC packet; flagging it here so it isn't mistaken for protocol debt. |
| **Base64 round-trip** (transcode: decode the envelope, then re-encode for the header) | Cosmetic | Still cosmetic. The envelope stays base64 on the wire; `PaymentBytes()` is yours to keep or drop. |

## What changes in your request path

Old: `POST /v1/cap` with `Livepeer-Mode` + `Livepeer-Spec-Version`.
New: `POST /v1/job` with `Livepeer-Protocol: paid-job/v1`.

| Header | Change |
|---|---|
| `Livepeer-Mode` | **Gone.** Replaced by `Livepeer-Protocol` (`<name>/v<major>`). |
| `Livepeer-Spec-Version` | **Gone.** The protocol tag carries its own version; there is no separate wire version to negotiate. |
| `Livepeer-Request-Id` | Now **required** — it is the idempotency key, not just a correlation id. |
| `Livepeer-Capability` / `Offering` / `Payment` | Unchanged. |

Transport selection is ordinary HTTP: `Accept: text/event-stream` picks
`stream`, a multipart content type picks `multipart`, everything else is
`unary`. If the offering doesn't declare it, you get a 400 with
`protocol_transport_unsupported` before payment is touched.

Response additions: `Livepeer-Work-Units` (always), `Livepeer-Work-Unit`
(unit echo), `Livepeer-Job-Id` (audit key joining claim, debit, and
idempotency record).

## One deliberate removal: no balance signaling on jobs

`Livepeer-Balance-Low` is **not** part of `paid-job/v1`. A job is one
envelope funding one exchange, settled once; there is no mid-job refill verb
and never was, so a mid-flight warning had nothing actionable behind it.
Runway is a `paid-session/v1` concept.

For long streams the seller-side protection is a **funded ceiling**: the
broker may end a stream when extractor-measured usage reaches what the
envelope funded, claiming exactly the delivered units in the trailer. If a
workload legitimately needs mid-exchange funding, it is session work, not
job work.

## For the transcode gateway specifically

Your ABR path is a clean `paid-job/v1` fit. Your **live** path is not — it
moves to `paid-session/v1`, and that changes more:

- The undocumented `balance` object your reconciler polls
  (`live_reconciler.go`, threshold on `runway_seconds_estimate`) is now
  **normative**: `status`, `claimed_units`, `debited_units`, `unit`,
  `runway_units`, `runway_seconds_estimate`, and
  `will_refuse_next_refill`. The winddown warning is spec'd with teeth —
  the broker must advertise it *before* refusing a refill, and must never
  accept a top-up it won't honor with lease extension.
- **Top-ups now extend the lease.** In your current code funding and
  session lifetime are unlinked; that's fixed at the protocol level.
- **Push instead of polling**: an optional control-WS (`control.events_ws`)
  pushes `session.usage.tick`, `session.balance`, and `session.ended`, so
  broker-side teardowns no longer wait for your next reconcile tick. HTTP
  stays authoritative — attaching is a latency optimization, not a
  requirement.
- **Credentials stop riding in the body.** Today live open ships MinIO STS
  secrets, session tokens, and the ingest stream key inside the JSON body.
  Buyer-supplied material now has a documented home: `session_params`,
  passed to the runner verbatim and never interpreted, logged, or relayed
  by the broker. Runner-side coordinates come back in a typed runtime
  descriptor with a structural public/private partition.
- **Open item — gateway-owned ingest.** The shipped `rtmp-hls/v1` descriptor
  schema covers the *runner-owns-ingest* case. Your live product relays RTMP
  through your own gateway and hands the runner a private ingest URL, which
  is the inverse direction. That is a schema question, not a protocol
  question: either a `rtmp-hls` variant or a sibling schema, authored by
  whoever owns the capability — no broker or registry change either way. We
  have not written it because it should be shaped by your actual pipeline.
  Tell us the coordinates you need in each direction and it's a short
  document.

## What is *not* fixed by this work

Be clear-eyed about scope: `INVALID_RECIPIENT_RAND` rotation — the failure
that today can kill a live broadcast mid-stream via
`rotation_unrecoverable` — is a payer-side gap at the clearinghouse/daemon
seam. The v1 protocols do not address it. It is named as the joint issue in
the LOC packet and needs a fix on that side.

## Verifying your integration

`livepeer-network-protocol/conformance` runs against any broker in URL mode
and covers the job scenarios end to end: three transports on one offering,
pre-payment transport refusal, `Work-Units` on error responses, advertised
stream trailers, idempotent replay (verified by counting backend
executions), and request-id reuse rejection. Point it at a broker configured
for your capability to check your assumptions before writing client code.

## What we need from you

1. **Your extractor choices per endpoint.** Today four OpenAI endpoints fake
   their units (images bill `n`, TTS bills `input.length`, transcriptions
   and rerank hard-code `1`). The extractor is orchestrator host config, but
   the *unit* is a product decision — tell us what you actually want to bill
   for those four and we'll make sure an extractor exists for it.
2. **The gateway-ingest descriptor shape** (transcode live, above).
3. **Whether you want the control-WS** for live, or will keep polling status.

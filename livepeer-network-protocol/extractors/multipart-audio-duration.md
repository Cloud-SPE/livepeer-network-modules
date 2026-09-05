---
extractor_name: multipart-audio-duration
version: 0.1.0
status: draft
spec_version: 0.1.0
last_updated: 2026-08-21
---

# Extractor: `multipart-audio-duration`

Bill by the playing time of an uploaded audio file. Used by transcription
offerings, where the natural unit is input duration rather than anything
the response contains.

## Why it measures the container

The measurement is seller-side and derived from the uploaded bytes. The
two alternatives are both worse:

- **A duration the caller declares** in a form field is self-reported by
  the party paying. Metering is the seller's job for the same reason the
  extractor exists at all.
- **A duration read from the response body** (OpenAI's `verbose_json`
  carries one) forces a response format on the caller, changing what
  their own client receives so the broker can bill. Gateways are removing
  exactly this kind of request mutation.

A third option — requiring every transcription runner to emit a duration
header — was considered and rejected as the *only* path: it works when an
operator controls their runner, and gateways that integrate third-party
runners cannot guarantee it. `response-header` remains available for
operators who do control theirs, and is cheaper than parsing.

## Configuration in `host-config.yaml`

```yaml
work_unit:
  name: "seconds"
  extractor:
    type: "multipart-audio-duration"
    file_field: "file"      # form field carrying the audio (default "file")
    unit: "seconds"         # or "milliseconds" (default "seconds")
    allow_inexact: false    # bill a bitrate-estimated MP3 (default false)
    default: 0              # units billed when duration cannot be measured
    max_seconds: 0          # refuse beyond this many seconds; 0 = no cap
```

A request body that is not `multipart/*` is treated as the audio itself,
so one extractor serves both a form upload and a raw one.

## The estimator contract (`multipart-audio-duration/v1`)

A caller that must reserve funds **before** the work runs has to reach
the same number the seller will bill. A JSON workload exposes a usable
ceiling in its request parameters; a multipart upload does not, so this
extractor doubles as a client-side estimator.

That makes it the one extractor a counterparty reproduces, which changes
what it owes. Elsewhere how a seller counts is its own business and is
deliberately unadvertised (`offering-axes.md`); here the offering
publishes it:

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

A client MUST refuse an `id` it does not implement rather than guess a
ceiling.

**No `package`.** The optional field names a canonical client
implementation, and there is not one: implementations are independently
owned. The contract is this document, the `id`/`rounding`/`exactness`
triple, and the fixtures below — which is what makes independent
ownership safe. A disagreement between two implementations is then a
fixture failure rather than a settlement that exceeds a ceiling.

**`exact-or-reject`.** The estimator returns an exact whole-second
ceiling or it refuses. It MUST NOT return a bitrate estimate: a ceiling
that reads low underfunds real work, one that reads high overcharges, and
neither is a thing to guess at. In practice only headerless
constant-bitrate MP3 reaches this, and it is refused.

Note the asymmetry with billing. The estimator refuses an inexact
measurement outright; the seller's extractor may, by configuration
(`allow_inexact`), bill one. They are different questions — what may be
spent versus what was — and the same parse serves both.

**The client estimate is never settlement evidence.** It bounds what may
happen. The broker's signed settlement is authoritative for what did.

### Shared fixtures

Two implementations of one measurement disagree eventually, and the
disagreement surfaces as a settlement exceeding a ceiling — a refused
exchange rather than a bug report. So the contract is the bytes:

```
livepeer-network-protocol/extractors/fixtures/multipart-audio-duration-v1/
  manifest.json     expected format, seconds, ceiling_seconds, reject
  *.wav *.flac *.m4a *.webm *.mp3 *.bin
```

An implementation MUST return exactly `ceiling_seconds` for every fixture
with `reject: false`, and MUST refuse every fixture with `reject: true`.
**Refusing is not returning zero** — zero is a claim that the audio has
no duration.

Both the broker's Go implementation and the published TypeScript package
run this set. Regenerate with
`go run ./internal/extractors/audioduration/genfixtures -out <dir>` from
`capability-broker`.

## Formats

| Container | Source of truth | Exact |
|---|---|---|
| WAV | `data` chunk size over the `fmt ` byte rate | yes |
| FLAC | STREAMINFO total samples over sample rate | yes |
| MP4 / M4A | `moov`→`mvhd` duration over timescale | yes |
| Ogg (Opus, Vorbis) | last page granule position over sample rate | yes |
| WebM / Matroska | `Segment`→`Info` Duration × TimecodeScale | yes |
| MP3 with Xing/Info/VBRI | declared frame count × samples per frame | yes |
| MP3 without one | file size over the constant bitrate | **no** |

`allow_inexact` gates that last row alone. A constant-bitrate estimate on
a variable-bitrate file can be far out, so an offering that would rather
under-bill than over-bill leaves it off, which is the default.

## Rounding

Seconds round **up**. A rounding rule that can return `0` for delivered
work bills nothing for it. Millisecond mode rounds up too, for the same
reason.

## When the duration cannot be measured

Every failure path bills `default` and logs the cause. Failures include:
an unrecognized container, a truncated or malformed header, a FLAC or
fragmented MP4 that declares no length, a WebM not yet finalized, an
inexact MP3 with `allow_inexact` off, and a measurement past
`max_seconds`.

Falling back to some other signal would bill for something nobody
measured, and would do so silently on exactly the inputs the parser got
wrong. A duration beyond 24 hours is treated as a misparse rather than a
transcription input, for the same reason.

`max_seconds` bounds what a single exchange can bill, so a container the
parser reads wrongly cannot become a large charge.

# extractors/

Declarative work-unit counting recipes. `paid-job/v1` capabilities pick an
extractor in their host-config; the broker runs the recipe against the
request/response to compute `actualUnits`. **No code per capability.**

**Extractors are a `paid-job/v1` concept only.** `paid-session/v1` usage comes
from runner-reported cumulative claims (`paid-session` §7.2), so a session
capability declaring `work_unit.extractor` is a configuration error — the
broker never runs one. Which extractor a job offering uses is deliberately
*not* advertised: it is a seller-side implementation choice no counterparty
gates on (see [`../protocols/offering-axes.md`](../protocols/offering-axes.md)).

The initial six (per plan 0002):

- [`openai-usage.md`](./openai-usage.md) — read `usage.{prompt|completion|total}_tokens` from OpenAI-shaped response JSON. **Accepted 2026-05-06.**
- [`response-jsonpath.md`](./response-jsonpath.md) — extract a count from a JSONPath in the response body. **Accepted 2026-05-06.**
- [`request-formula.md`](./request-formula.md) — safe arithmetic expression over request fields (e.g., `width × height × steps`). **Accepted 2026-05-06.**
- [`bytes-counted.md`](./bytes-counted.md) — tally bytes in/out (request, response, or both). **Accepted 2026-05-06.**
- [`seconds-elapsed.md`](./seconds-elapsed.md) — wall-clock duration of one exchange, anchored per transport. **Accepted 2026-05-06.**
- [`ffmpeg-progress.md`](./ffmpeg-progress.md) — parse FFmpeg's `-progress` output (frame, frame-megapixel, out-time). **Accepted 2026-05-06.**

**Status:** all six initial extractors accepted.

Each extractor has its own SemVer (frontmatter `version`). Spec-wide SemVer covers
the extractor envelope shape (`{ type, ... }`) but not individual extractor
parameters — those bump per-extractor.

Adding a new extractor type is a broker change (the broker has to know how to
evaluate it) but rare. To propose one, see [`../PROCESS.md`](../PROCESS.md).

# extractors/

Declarative work-unit counting recipes. Capabilities pick an extractor in their
host-config; the broker runs the recipe against the request/response to compute
`actualUnits`. **No code per capability.**

The initial six (per plan 0002):

- [`openai-usage.md`](./openai-usage.md) — read `usage.{prompt|completion|total}_tokens` from OpenAI-shaped response JSON. **Drafted 2026-05-06.**
- [`response-jsonpath.md`](./response-jsonpath.md) — extract a count from a JSONPath in the response body. **Drafted 2026-05-06.**
- [`request-formula.md`](./request-formula.md) — safe arithmetic expression over request fields (e.g., `width × height × steps`). **Drafted 2026-05-06.**
- [`bytes-counted.md`](./bytes-counted.md) — tally bytes in/out (request, response, or both). **Drafted 2026-05-06.**
- [`seconds-elapsed.md`](./seconds-elapsed.md) — wall-clock duration with mode-aware start/end anchors. **Drafted 2026-05-06.**
- [`ffmpeg-progress.md`](./ffmpeg-progress.md) — parse FFmpeg's `-progress` output (frame, frame-megapixel, out-time). **Drafted 2026-05-06.**

**Status:** all six initial extractors drafted; pending review.

Each extractor has its own SemVer (frontmatter `version`). Spec-wide SemVer covers
the extractor envelope shape (`{ type, ... }`) but not individual extractor
parameters — those bump per-extractor.

Adding a new extractor type is a broker change (the broker has to know how to
evaluate it) but rare. To propose one, see [`../PROCESS.md`](../PROCESS.md).

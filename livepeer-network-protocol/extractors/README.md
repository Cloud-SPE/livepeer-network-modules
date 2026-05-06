# extractors/

Declarative work-unit counting recipes. Capabilities pick an extractor in their
manifest entry; the broker runs the recipe against the request/response to compute
`actualUnits`. **No code per capability.**

The initial six (resolution of plan 0002):

- `openai-usage.md` — read `usage.{prompt|completion|total}_tokens` from response JSON.
- `response-jsonpath.md` — extract a count from a JSONPath in the response body.
- `request-formula.md` — compute units from request fields (e.g.,
  `width × height × steps`).
- `bytes-counted.md` — tally bytes in/out (for streaming bandwidth pricing).
- `seconds-elapsed.md` — wall-clock duration of a session.
- `ffmpeg-progress.md` — parse FFmpeg progress output for video-frame counts.

**Status:** specs TBD per [plan 0002](../../docs/exec-plans/active/0002-define-interaction-modes-spec.md).

Adding a new extractor type is a broker change (the broker has to know how to evaluate
it) but rare. To propose one, see [`../PROCESS.md`](../PROCESS.md).

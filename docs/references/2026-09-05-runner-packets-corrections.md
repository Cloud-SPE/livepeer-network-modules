# Corrections to the 2026-09-02 runner and consumer packets

Date: 2026-09-05. Applies to all six `2026-09-02-*-migration-packet-*.md`
documents. Found by the openai-runners team while implementing theirs;
both are errors in the packets, not in the runners.

## 1. `response-header` takes `header`, not `name`

The Florence and NeMo packets write the extractor as
`{"type": "response-header", "name": "X-Livepeer-Work-Units"}`. The
broker's key is `header`:

```json
"extractor": { "type": "response-header", "header": "X-Livepeer-Work-Units" }
```

A contract with `name` is rejected at config load. The openai-runners
packet did not spell the extractor out and their images are correct.

## 2. `--serve-runner` does not take an image

Every packet's "Verifying" section says
`go run ./cmd/livepeer-conformance --broker-url … --serve-runner <image>`.
`--serve-runner` takes no image: it attaches the conformance suite's own
fake runner to a broker and stays up, and runs no scenarios. Nothing in the
protocol repository fetches a real image's contract end to end.

What verifies a real image is a real attach: the image running beside the
pool member agent, the agent fetching its contract and attaching it to a
broker, the broker certifying it against the catalog's template and
advertising the offer. `livepeer-network-modules` runs that — it is the
"confirm it landed" step the ownership decision assigned to this side —
against a pool stood up from this repository, once an image is published.
What a runner author should do before that:

1. `curl -s http://<container>:8080/.well-known/livepeer-runner` returns the
   document (or array) and every `capability_id`, `identity` key and
   `work_unit.extractor` matches the catalog template that should select it
   (`templates/*.yaml`: `capability`, `match`, and the usage step).
2. The container's readiness probe answers at the path the contract names.
3. Send us the tag (and digest). We attach it, watch `GET /admin/v1/runners`
   for the accepted document and `GET /admin/v1/certification/runs` for the
   verdict, and report back by capability.

## 3. `request-formula` counts the characters of a string field

Not an error, an omission: `extractors/request-formula.md` lists only
numeric fields. The broker's implementation evaluates a field whose JSONPath
resolves to a string as its code-point count, so TTS metering is
`{"type": "request-formula", "expression": "chars", "fields": {"chars": "$.input"}}`.
The document will say so.

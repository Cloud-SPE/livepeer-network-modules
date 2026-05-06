---
extractor_name: response-jsonpath
version: 0.1.0
status: accepted
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Extractor: `response-jsonpath`

Generic count-from-response-JSON. Reads an integer from a JSONPath in the response
body.

## When to use

- Any capability whose backend returns JSON and whose work-unit count is
  expressible as a path into that JSON. The escape hatch when no other extractor
  fits.

## Configuration in `host-config.yaml`

```yaml
work_unit:
  name: "barks"
  extractor:
    type: "response-jsonpath"
    path: "$.bark_count"
```

| Field | Required | Default | Notes |
|---|---|---|---|
| `type` | yes | — | `"response-jsonpath"` |
| `path` | yes | — | RFC 9535-compliant JSONPath (or a sane subset; see below) |
| `default` | no | `0` | Value used when path doesn't match or evaluates to non-numeric |

## Recipe

1. Parse the backend response body as JSON.
2. Evaluate `path` against the parsed JSON.
3. If the result is a non-negative integer (or coerces cleanly), that is
   `actualUnits`.
4. If the path doesn't match, returns multiple values, or evaluates to
   non-numeric/negative: use `default` (zero unless overridden) and log a
   warning.

## Supported JSONPath subset

Implementations MUST support at minimum:

- `$` — root.
- `.<key>` — child by name.
- `[<idx>]` — array index.
- `[<n1>,<n2>,...]` — array index list (returns multiple; result must be summed
  if numeric, else fail to default).

Implementations MAY support full RFC 9535 (filters, wildcards, slices) but it is
not required. The extractor is meant to be simple; complex paths are a smell and
suggest a different extractor is needed.

## Streaming caveat

For `http-stream` mode: by default, the extractor evaluates against the
**concatenated final body** (all chunks). For SSE, "final body" means the last
JSON-shaped event. If the capability emits multiple events that each contain a
count, use `request-formula` or a custom extractor instead.

## Example

Backend response:

```json
{ "id": "doggo-1", "result": "loud", "bark_count": 7 }
```

With `extractor: { type: "response-jsonpath", path: "$.bark_count" }`:
`actualUnits = 7`.

Backend response with array sum:

```json
{ "barks": [3, 4, 5] }
```

With `path: "$.barks[*]"` (if implementation supports wildcards):
`actualUnits = 12` (sum).

## Versioning

`0.1.0`.

## Conformance

- Happy path: simple `$.field` returns the expected integer.
- Path doesn't match → `default` value used, warning logged.
- Non-numeric result → `default` used.
- Negative result → `default` used (units can't be negative).
- Unsupported syntax → broker rejects offering at config-load time, not at
  runtime.

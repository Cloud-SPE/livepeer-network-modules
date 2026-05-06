---
extractor_name: request-formula
version: 0.1.0
status: draft (proposed)
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Extractor: `request-formula`

Compute units from request fields via a safe arithmetic expression. Used when
the cost is known **before** the backend processes the request.

## When to use

- Image generation: `width × height × steps` (cost scales with rendered area).
- Audio TTS: input character count.
- Any deterministic-cost capability where work is a function of the request.

## Configuration in `host-config.yaml`

```yaml
work_unit:
  name: "image-step-megapixel"
  extractor:
    type: "request-formula"
    expression: "(width * height * steps) / 1000000"
    fields:
      width: "$.size.width"
      height: "$.size.height"
      steps: "$.num_inference_steps"
    default: 0
```

| Field | Required | Default | Notes |
|---|---|---|---|
| `type` | yes | — | `"request-formula"` |
| `expression` | yes | — | Safe arithmetic expression — operators only; field references by name |
| `fields` | yes | — | Map of identifier → JSONPath into the request body |
| `default` | no | `0` | Used when any field is missing or evaluates to non-numeric |

## Safe expression language

The expression is **NOT** a general-purpose eval. Implementations MUST restrict
to:

- Numeric literals: integers and floats.
- Field references: identifiers declared in `fields`.
- Operators: `+`, `-`, `*`, `/`, `%`, parentheses.
- Functions (optional, implementations MAY support a small allowlist):
  `min(a, b)`, `max(a, b)`, `floor(x)`, `ceil(x)`, `round(x)`.

**Forbidden:** comparison operators, conditionals, loops, function calls
outside the allowlist, string operations, attribute access, dynamic field
references.

Implementations MUST reject offerings whose `expression` parses to anything
outside the allowed grammar at config-load time, not at runtime.

## Recipe

1. Parse the request body as JSON.
2. For each `fields` entry, evaluate the JSONPath; coerce to number.
3. Substitute into `expression`.
4. Evaluate the expression.
5. Floor to non-negative integer; that is `actualUnits`.
6. If any field is missing or non-numeric: use `default`, log a warning.

## Example

Request body:

```json
{ "prompt": "a cat", "size": { "width": 1024, "height": 1024 }, "num_inference_steps": 50 }
```

With:
```yaml
expression: "(width * height * steps) / 1000000"
fields:
  width: "$.size.width"
  height: "$.size.height"
  steps: "$.num_inference_steps"
```

Computation: `(1024 * 1024 * 50) / 1000000 = 52.4288` → floor → `actualUnits = 52`.

## Why request-side, not response-side?

The unit count is known up-front. There's no need to wait for the backend's
response. This makes `request-formula` cheaper to evaluate (no body parsing on
the response path) and gives `expected_max_units == actualUnits` in the common
case (no reconciliation drift).

## Versioning

`0.1.0`.

## Conformance

- Happy path: simple multiplication formula returns the expected integer.
- Missing field → `default` used, warning logged.
- Non-numeric field value → `default` used.
- Forbidden expression syntax → offering rejected at config-load time (broker
  fails to start with a clear error).
- Negative result → clamped to `0`.
- Floats correctly floored to integer.

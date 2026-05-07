# DESIGN

Component-local design summary. Cross-cutting design lives at the repo
root in [`../docs/design-docs/`](../docs/design-docs/).

## What this component is

A Python FastAPI process that loads `zeroentropy/zerank-2` (a Qwen3-3B
CrossEncoder) at startup and serves Cohere-compatible rerank requests:

```
POST /v1/rerank
{
  "query": "...",
  "documents": ["...", "...", ...],
  "top_n": 5
}

→ {"results": [{"index": 2, "relevance_score": 0.94, "document": "..."}, ...]}
```

The runner is a sibling to `openai-runners/`; it inherits the shared
`python-runner-base` image (per OQ2) and adds `transformers` +
`sentence-transformers` + `torch` for the cross-encoder.

## Wire compliance

The runner does not consume `Livepeer-Payment` headers — those are
validated by the broker-side `payment-daemon/`. The runner sees only:

- HTTP method + path + body.
- `Livepeer-Capability` + `Livepeer-Offering` headers (informational).
- The orch-coordinator scrape against `GET /rerank/options`.

## What stays out of this component

- **Customer auth + billing.** Lives in `customer-portal/` and gateway.
- **Payment validation.** Broker-side.
- **Capability registration.** Orch-coordinator scrapes `GET /rerank/options`
  per plan 0018.
- **Resolver logic.** Gateway-side.

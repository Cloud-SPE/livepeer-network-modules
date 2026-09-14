# Product sense

The registry is infrastructure for gateway and integration developers. It
provides one discovery API for arbitrary workloads, with signed orchestrator
identity, prices and broker endpoints.

A gateway operator chooses either on-chain discovery or a static list of
coordinators. YAML is authoritative for local discovery/policy; signed
manifests are authoritative for advertised capability tuples. The operator
should not duplicate broker capabilities, pricing or settlement keys in YAML.

An orchestrator operator publishes through the coordinator and cold-console
signing cycle. A new signed publication updates discovery without a chain
transaction unless the chain pointer itself changes. Editing YAML or live
broker inventory alone does not publish a new signed manifest.

A consumer calls `ResolveByAddress` for inventory, `Select` for the first
matching route, or `SelectMany` for failover candidates. It still implements
the selected workload protocol and payment contract. It must understand the
advertised protocol/axes and settlement-key validity windows; the registry is
not a workload executor or payment authorizer.

Good integration means a static coordinator list and chain pointers feed the
same verification and route projection path. Failures should distinguish
missing pointers, unavailable manifests, invalid signatures, policy exclusions
and unhealthy brokers. A process-level health response alone is insufficient.

The legacy endpoint synthesis path remains for consumers needing a URL. This
daemon cannot guarantee that a URL published on chain is also a dialable
legacy `go-livepeer` endpoint. See [legacy compatibility](docs/product-specs/legacy-compat.md).

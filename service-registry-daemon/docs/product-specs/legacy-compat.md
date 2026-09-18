# Legacy and CSV compatibility

A legacy `go-livepeer` client reads a chain `serviceURI` and dials it directly.
This daemon does not change that client or implement its OrchestratorInfo API.
Operators must publish a dialable workload URL if they expect direct legacy
clients to work. A full coordinator manifest URL is not automatically such an
endpoint. Publisher mode does not write chain pointers or host a sibling HTTP
endpoint.

For resolver-aware clients, explicit `allow_legacy_fallback=true` permits an
endpoint-only result when chain-URL manifest fetching is unavailable or too
large. The result has source/status `legacy`, original pointer URL, no
capabilities and no settlement keys. Invalid manifest verification does not
permit downgrade. An explicit overlay `manifest_url` never downgrades.

CSV pointers are a read-only compatibility format. The resolver extracts URLs
from decoded `nodes[]` and marks them unsigned. It does not fetch
`capabilitiesUrl` or derive prices/capabilities. Bad base64 or JSON returns an
error. Unsigned static/CSV results require daemon, overlay or explicit request
allowance; see [gRPC policy](grpc-surface.md#signature-policy).

Neither legacy nor CSV endpoint-only nodes satisfy Select's required
capability/offering filter. Explicit static pins can carry those fields, but
remain unsigned and cannot establish settlement delegation. Use signed
coordinator discovery for the normal paid route contract.

Historical compatibility proposals and migration plans are retained in
`docs/references/` and completed plans. Their release-specific promises are not
current guarantees of this implementation. The present modes are documented
in [discovery modes](../design-docs/serviceuri-modes.md).

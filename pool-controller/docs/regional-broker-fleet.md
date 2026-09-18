# Regional broker fleets

Each `bootstrap.brokers` entry can specify `public_url` (an HTTPS origin) and
`template_ids` (catalog IDs). When any public URL is set, every broker must have
one, and the single-broker QUIC address must be empty. The controller includes
the complete regional fleet in each enrollment bundle. EU initially has its
transcode broker; US has transcode, audio, and LLM brokers.

An omitted template selector retains legacy all-template behavior. An explicit
empty list disables every adopted offer. A nonempty list enables only selected
templates that the pool has enabled. Other adopted offers are sent disabled,
preserving frozen shapes and historical evidence. Unknown or duplicate selectors
reject the controller configuration when the catalog is loaded.

Set `bootstrap.member_agent_image` to an immutable `repository@sha256:...`
reference (or a locally available `sha256:...` image ID for local validation).
Regional bundles render that exact image. Registry/tag environment variables
cannot override the pin. A tag-only value is rejected. The default legacy image
is retained only when this field is absent.

The agent reads comma-separated `LIVEPEER_BROKER_URLS` and runs one independent
outbound HTTPS/WebSocket reconnect loop per origin. An unavailable broker does
not delay healthy connections. All connections share one desired-state applier,
one enrollment, one GPU ownership generation, and one durable credential pair.
There is no second ownership claim for audio or LLM. A broker receives the same
host inventory but can route only its enabled, certified offers. See
[`regional-credentials.md`](../../pool-member-agent/docs/regional-credentials.md)
for rotation and restart recovery.

These are connections within one immutable pool. Moving a GPU to another region
still uses the shared ownership authority's drain/revoke/actual-stop transfer
fence; adding an origin to an agent is not a regional transfer.

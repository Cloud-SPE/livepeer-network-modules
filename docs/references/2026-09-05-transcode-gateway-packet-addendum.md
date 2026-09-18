# Addendum to the transcode-gateway consumer packet (2026-09-02)

Date: 2026-09-05. Read with
`2026-09-02-consumer-migration-packet-transcode-gateway.md` and
`2026-09-05-runner-packets-corrections.md`. Three things the runner
team's review settled after the packet was written change what the
gateway sends and reads.

## 1. The request body is the runner's v2 schema, not the packet's

The packet showed `{ "source_url", "output_url", "profiles" }`. That was
read from the stale repository. The ABR body is `video-transcode-abr/v2`
(`abr-runner/contract_v2.go`): `schema`, `workload_id`, `input.download_url`,
`ladder.preset`, and an `output` map with a presigned destination for the
manifest and for each rendition's playlist and stream. VOD moves to a
sibling `video-transcode-vod/v2` shape the runner team is defining. The
gateway already owns these bodies; the broker relays them verbatim inside
`POST /v1/job` and never reads them.

## 2. The response is the runner's SSE, and the claim is frame-megapixels

The body of a `stream` response is the runner's typed SSE — progress
events, then a terminal result — relayed verbatim. The usage claim is the
trailer `Livepeer-Work-Units`, in **frame-megapixels aggregated over every
delivered video rendition** (`ceil(Σ frames × width × height / 1e6)`,
audio-only zero), priced per 1,000 units (`price_default.per_units: 1000`
on the three VOD/ABR templates). Not per job. A gateway that cannot read
trailers gets the same figure from `GET /v1/settlement/{id}`. Budget a
job from the input's duration and the preset's rungs; a one-minute
`abr-standard` ladder is ≈ 6,500 units.

## 3. Live: the ingest direction is settled

The 2026-08-19 packet left "gateway-owned ingest" open. The runner team
has said the gateway keeps customer-owned RTMP ingress and uses the
runner's coordinates internally, relaying to the runner's `rtmp_url`.
That is the shipped `rtmp-hls/v1` schema as written — runner-owned
ingest, reached by the gateway — so no variant schema is needed. The
runner advertises `rtmp_url` as `rtmps://<member host>:1936/…` (the
member's RTMPS port, terminated by the pool member agent) and `hls_url`
under the member's HTTPS edge; the gateway connects to both directly, and
the broker is never in the media path. Plan 0046.

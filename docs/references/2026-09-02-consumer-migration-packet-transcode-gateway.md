# Consumer migration packet — `livepeer-modules-transcode-gateway`

Date: 2026-09-02. For the owner of `livepeer-modules-transcode-gateway`
(and the copy under `livepeer-modules-transcode/transcode-gateway`; one of
the two is presumably live — tell us which). Read on 2026-09-02; quoted
paths are from your source. Tracked as `lnm-djk`. Supplements, does not
replace, the 2026-08-19 gateway packet
(`2026-08-19-gateway-migration-packet-paid-job.md`), which still holds for
the request path.

**Dependency order (decision 4):** port any time against the conformance
runner; verified end to end once the transcode runners (their packet) ship.

## The short version

The gateway calls two routes the broker does not have:
`gateway/internal/proxy/livepeer/runner_status.go` builds
`brokerURL + "/v1/video/transcode/abr/status"`, and
`gateway/internal/repo/live.go` polls `GET /v1/cap/{bsess}`. Both are the v0
interaction-mode surface removed with plan 0043. The gateway cannot have
worked against a current broker. The port target is what the transcode
runners now are: **VOD and ABR as `paid-job/v1` `stream`**, **live as
`paid-session/v1`**.

## What you can delete

| What you have | Why it existed | What replaces it |
|---|---|---|
| Status polling (`…/abr/status`) | The runner was asynchronous | The job response IS the progress: read the streamed `ffmpeg -progress` body; the encode is done when the body ends and the trailer carries the claim. |
| `GET /v1/cap/{bsess}` for live | v0 session surface | `paid-session/v1`: `POST /v1/session` returns the `rtmp-hls/v1` descriptor (`rtmp_url`, `hls_url`); `GET /v1/session/{id}` for status; `POST …/end`. |
| `guessInteractionMode` | No mode data on the catalog | `Livepeer-Protocol: paid-job/v1` and `transport: stream` per request (Aug-19 packet). |

## What changes

- **Capability ids** are `video:transcode.vod`, `video:transcode.abr`,
  `video:transcode.live`; offering ids `vod-default`, `abr-default`, and
  the live one from the manifest. Colon form, never `/` (runner-attach §3.2).
- **VOD/ABR request:** `POST /v1/job` with `Livepeer-Capability`,
  `Livepeer-Offering`, `Livepeer-Protocol: paid-job/v1`,
  `Livepeer-Request-Id`, `Accept` for a stream; body `{ "source_url",
  "output_url", "profiles": [...] }`. Response: chunked progress lines,
  then trailer `Livepeer-Work-Units`. For clients that cannot read
  trailers, `GET /v1/settlement/{id}` returns the record.
- **Live:** `POST /v1/session` with the session headers and `session_params`;
  consume `runtime.public.rtmp_url` / `hls_url`; top up on the balance
  events; `POST /v1/session/{id}/end`.

## What is *not* fixed by this work

- The runners' own rewrite (their packet). Until it lands, a VOD job
  against a real runner fails certification and is not advertised, so the
  gateway sees no route — which is the correct state.

## Verifying

```sh
cd livepeer-network-protocol/conformance
go run ./cmd/livepeer-conformance --broker-url http://<broker>:8080 --attach-runner
```

gives you a broker with a fake transcode-shaped runner attached; point the
gateway at it. Real verification is against the shipped runners.

## What we need from you

1. Which of the two gateway copies is live; the other should be archived.
2. Confirmation the port reads trailers (Go does) so you can drop the
   settlement round-trip.

# Runner migration packet — `livepeer-modules-transcode`

Date: 2026-09-02. For the owner of `livepeer-modules-transcode`
(`transcode-runner`, `abr-runner`, `live-runner`; nvidia and intel builds).
Based on a read of the live repository on 2026-09-02 — **not**
`livepeer-modules-transcode-runners`, which is a June extraction and is
stale (see "What we need from you"). Tracked as `lnm-z72`.

**Dependency order (decision 4):** nothing here waits on anything else.
Each runner is independently verifiable: attach, certify, advertise.

## The short version

Three things. The VOD and ABR runners are asynchronous today — `POST
/v1/video/transcode` returns `202` and a `job_id` to poll — and an
asynchronous runner **cannot be billed** under `paid-job/v1`, whose one
exchange is one request, one response, one claim. They become synchronous
streams. All three runners serve the runner contract. And the images are
per vendor: the catalog carries `nvidia` and `intel` keys and the pool picks
at placement.

Governing rule from the operator: a runner that does not serve this contract
is rewritten, not accommodated.

## What you can delete

| What you have | Why it existed | What replaces it |
|---|---|---|
| `202` + `job_id`, `GET …/status` polling on VOD and ABR | The gateway had no streaming contract to consume progress on | `transport: stream`: the encode's `ffmpeg -progress` output IS the response body, streamed as it runs, terminated by the claim. |
| Any describe/options surface | The v0 broker dialled runners | `GET /.well-known/livepeer-runner`. |
| Internal progress parsing used only to drive a status endpoint | Same | You keep parsing progress for your own logs if you like; the broker now parses the streamed body itself with its `ffmpeg-progress` extractor. |

## What changes

### 1. VOD and ABR become synchronous streams

Request, as the catalog's smoke steps send it (`video-transcode-vod.yaml`,
`video-transcode-abr.yaml`):

```json
{ "source_url": "https://…/fixture.mp4",
  "output_url": "https://…/sink",
  "profiles": [ { "name": "720p30", "width": 1280, "height": 720, "fps": 30 } ] }
```

Response: `Transfer-Encoding: chunked`, body = FFmpeg's `-progress pipe:1`
key=value lines as they are produced (`frame=…`, `out_time_us=…`,
`progress=continue|end`), then the trailer `Livepeer-Work-Units: <n>`. The
broker's `ffmpeg-progress` extractor
(`livepeer-network-protocol/extractors/ffmpeg-progress.md`) reads the body
live, so a caller sees the encode advance and the pool meters it as it
goes. The claim for a per-job price is `1`. The runner already parses this
progress internally; this is plumbing it to the response instead of to a
status record.

`source_url` and `output_url` are fetched and written by the runner. In
certification both are broker-minted, run-scoped URLs; in production they
are the caller's.

### 2. Every runner serves the contract

VOD (ABR identical with `video:transcode.abr` and `/v1/video/transcode/abr`):

```json
{ "capability_id": "video:transcode.vod",
  "protocol": "paid-job/v1",
  "transports": ["stream"],
  "work_unit": { "name": "jobs",
                 "extractor": { "type": "request-formula", "expression": "1" } },
  "paths": { "invoke": "/v1/video/transcode" },
  "readiness": { "type": "http-status", "path": "/healthz" },
  "identity": { "provider": "transcode-runner" },
  "schema_versions": { "paid-job/v1": "1.0.15" } }
```

Live is a `paid-session/v1` runner emitting `rtmp-hls/v1`
(`runner-contract.md` §3 "Minimal session runner" is written from it;
`video-transcode-live.yaml` is its template). Its create/status/terminate
paths, `runner_session_id`, usage callback and heartbeat are the
paid-session §11 checklist.

`identity` has no model — a transcoder is not a model. `provider` is
enough; the catalog's templates carry no `match` for this family.

### 3. Images per vendor

The catalog carries:

```yaml
runner_compose:
  image:
    nvidia: tztcloud/transcode-runner-nvidia:v1.4.1
    intel:  tztcloud/transcode-runner-intel:v1.4.1
```

`v1.4.1` is a **placeholder** (decision 8): the templates will point at
whatever tag this rewrite ships as. Your compose default is `v1.3.1`;
tell us the real tag and we change the map. AMD waits for a later
iteration and has no key. The pool renders the device block by vendor
(NVIDIA `device_ids`, Intel `/dev/dri`); the image needs no knowledge of
which card it landed on beyond its own build.

### 4. Cards

The catalog admits `gtx-1080`, `rtx-2080`, `arc-a770`, `arc-b580`,
`flex-170` for all three templates. The 1080 was added on 2026-09-02
(decision 9) on the basis that Pascal NVENC does H.264/HEVC as well as the
product needs; if the nvidia build is compiled for a CUDA base that has
dropped `sm_61`, say so and we remove it.

## What is *not* fixed by this work

- **CPU transcoding.** SVT-AV1 on CPU is the better AV1 VOD encoder and the
  pool cannot place a CPU workload today; that is a placement feature
  (`lnm-iqn`) after this plan. Question for you below.
- The transcode gateway. It is on the deleted v0 surface and has its own
  packet (`2026-09-02-consumer-migration-packet-transcode-gateway.md`).

## Verifying

```sh
cd livepeer-network-protocol/conformance
go run ./cmd/livepeer-conformance --broker-url http://<broker>:8080 --serve-runner tztcloud/transcode-runner-nvidia:<tag>
```

Then against a pool: attach a host with a 2080, confirm the three
templates place, confirm the smoke step streams progress (the certification
run's evidence shows the parsed frame count) and the usage step reports 1.

## What we need from you

1. The post-rewrite tags for the six images (three runners × two vendors).
2. **Archive `livepeer-modules-transcode-runners`** on GitHub (read-only,
   description pointing here). It is a June extraction; we read it first
   and drew a wrong conclusion from it (decision 11). Archive, not delete —
   the multi-arch gencode fix's history is there.
3. Does `codecs-builder` produce an SVT-AV1 build? If a CPU image exists
   or is cheap, the CPU compute-unit feature has something to place.
4. Which CUDA base the nvidia build uses (for the `sm_61` question).

# video worker node

Reference worker-node stack for the current video product surface:

- `capability-broker`
- `payment-daemon` in receiver mode
- one local `abr-runner`
- broker-owned live RTMP ingest + LL-HLS egress

This scenario serves the two video capabilities the rewrite currently
expects:

- `video:transcode.abr`
- `video:live.rtmp`

`transcode-runner` is intentionally not included here. The current
`video-gateway` product model routes offline video work through
`video:transcode.abr`, and live ingest is handled inside the broker's
`rtmp-ingress-hls-egress@v0` mode rather than a separate live runner.

## Bring-up

```sh
cp infra/scenarios/video-worker-node/.env.example infra/scenarios/video-worker-node/.env
docker compose \
  -f infra/scenarios/video-worker-node/docker-compose.yml \
  --env-file infra/scenarios/video-worker-node/.env \
  up -d
```

## Files you must provide

- `./run/hot-wallet-keystore.json`
- `./run/keystore-password`
- `./host-config.yaml` is already provided in this scenario directory;
  edit the orch address, externally-advertised broker URL, and pricing
  before use

## Verify

```sh
curl -s http://127.0.0.1:8080/registry/offerings | jq
curl -s http://127.0.0.1:8080/registry/health | jq
curl -s http://127.0.0.1:9090/metrics | head
curl -s http://127.0.0.1:8087/healthz
```

Then confirm the offerings include both capability IDs:

```sh
curl -s http://127.0.0.1:8080/registry/offerings | jq '.capabilities[].capability_id'
```
